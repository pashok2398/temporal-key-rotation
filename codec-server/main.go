package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"temporal-key-rotation/shared"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// KMSEncryptionCodec handles encryption/decryption of payloads using AWS KMS
type KMSEncryptionCodec struct {
	kmsManager *KMSManager
}

// NewKMSEncryptionCodec creates a new KMS encryption codec
func NewKMSEncryptionCodec(kmsManager *KMSManager) *KMSEncryptionCodec {
	return &KMSEncryptionCodec{
		kmsManager: kmsManager,
	}
}

// handleEncode handles the /encode endpoint
func (c *KMSEncryptionCodec) handleEncode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req shared.CodecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	var response shared.CodecResponse
	ctx := context.Background()

	for _, payload := range req.Payloads {
		// Only process JSON payloads that aren't already encrypted
		encoding, exists := payload.Metadata["encoding"]
		if !exists || encoding == "json/plain" {
			// Get current data key (with automatic rotation)
			currentKey, err := c.kmsManager.GetCurrentDataKey(ctx)
			if err != nil {
				log.Printf("Failed to get current data key: %v", err)
				http.Error(w, "Key retrieval failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Prepare data to encrypt
			var dataToEncrypt []byte
			if payload.Data != "" {
				// Try to decode as base64 first, if it fails, use as plain text
				if decoded, err := base64.StdEncoding.DecodeString(payload.Data); err == nil {
					dataToEncrypt = decoded
				} else {
					dataToEncrypt = []byte(payload.Data)
				}
			}

			// Encrypt the data with the current data key
			encryptedData, err := EncryptWithDataKey(dataToEncrypt, currentKey.PlaintextKey)
			if err != nil {
				log.Printf("Failed to encrypt data: %v", err)
				http.Error(w, "Encryption failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Create response payload with KMS metadata
			encodedPayload := shared.PayloadData{
				Metadata: map[string]string{
					"encoding": "binary/encrypted",
				},
				Data:             encryptedData,
				KMSKeyID:         c.kmsManager.keyID,
				EncryptedDataKey: currentKey.EncryptedKey,
				Algorithm:        "AES-256-GCM",
			}

			response.Payloads = append(response.Payloads, encodedPayload)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// handleDecode handles the /decode endpoint
func (c *KMSEncryptionCodec) handleDecode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req shared.CodecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	var response shared.CodecResponse
	ctx := context.Background()

	for _, payload := range req.Payloads {
		// Check if this payload is encrypted
		if payload.Metadata["encoding"] != "binary/encrypted" {
			// Not encrypted, return as-is
			response.Payloads = append(response.Payloads, payload)
			continue
		}

		// For KMS encrypted payloads, we need the encrypted data key
		if payload.EncryptedDataKey == "" {
			log.Printf("Missing encrypted data key for encrypted payload")
			http.Error(w, "Missing encrypted data key", http.StatusBadRequest)
			return
		}

		// Decrypt the data key using KMS (with intelligent caching)
		dataKey, err := c.kmsManager.DecryptDataKey(ctx, payload.EncryptedDataKey, payload.KMSKeyID)
		if err != nil {
			log.Printf("Failed to decrypt data key: %v", err)
			http.Error(w, "Key decryption failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Decrypt the actual data
		decryptedData, err := DecryptWithDataKey(payload.Data, dataKey)
		if err != nil {
			log.Printf("Failed to decrypt payload data: %v", err)
			http.Error(w, "Data decryption failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Note: dataKey is not zeroed here as it might be cached for reuse
		// The KMSManager handles secure memory management

		// Create response payload with base64 encoded decrypted data
		decodedPayload := shared.PayloadData{
			Metadata: map[string]string{
				"encoding": "json/plain",
			},
			Data: base64.StdEncoding.EncodeToString(decryptedData),
		}

		response.Payloads = append(response.Payloads, decodedPayload)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// handleStats handles the /stats endpoint for monitoring
func (c *KMSEncryptionCodec) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := c.kmsManager.GetKeyStats()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("Failed to encode stats response: %v", err)
	}
}

func resolveKMSAlias(alias string) (string, error) {
	// Create AWS config
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create KMS client
	kmsClient := kms.NewFromConfig(cfg)

	// Resolve the alias
	result, err := kmsClient.DescribeKey(context.TODO(), &kms.DescribeKeyInput{
		KeyId: aws.String(alias),
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve alias %s: %w", alias, err)
	}

	return *result.KeyMetadata.Arn, nil
}

func main() {
	// Get alias from environment
	keyAlias := os.Getenv("KMS_KEY_ALIAS")
	if keyAlias == "" {
		keyAlias = "alias/temporal-codec-latest" // Default
	}

	// Resolve alias to actual key ARN
	actualKeyARN, err := resolveKMSAlias(keyAlias)
	if err != nil {
		log.Fatalf("Failed to resolve KMS alias %s: %v", keyAlias, err)
	}

	log.Printf("Using KMS alias: %s → %s", keyAlias, actualKeyARN)
	// Parse cache TTL for old keys
	cacheTTLStr := os.Getenv("KMS_CACHE_TTL")
	cacheTTL := 24 * time.Hour // default - keep old keys cached for 24 hours
	if cacheTTLStr != "" {
		if ttl, err := strconv.Atoi(cacheTTLStr); err == nil {
			cacheTTL = time.Duration(ttl) * time.Second
		}
	}

	// Parse key rotation interval
	rotationIntervalStr := os.Getenv("DATA_KEY_ROTATION_INTERVAL")
	rotationInterval := 1 * time.Hour // default - rotate every hour
	if rotationIntervalStr != "" {
		if interval, err := strconv.Atoi(rotationIntervalStr); err == nil {
			rotationInterval = time.Duration(interval) * time.Second
		}
	}

	// Initialize KMS manager with time-based rotation
	kmsManager, err := NewKMSManager(actualKeyARN, cacheTTL, rotationInterval)
	if err != nil {
		log.Fatalf("Failed to initialize KMS manager: %v", err)
	}

	// Start background maintenance routines
	kmsManager.StartCacheCleanup(15 * time.Minute)

	codec := NewKMSEncryptionCodec(kmsManager)

	// Set up routes
	http.HandleFunc("/encode", codec.handleEncode)
	http.HandleFunc("/decode", codec.handleDecode)
	http.HandleFunc("/stats", codec.handleStats)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("KMS Codec server starting on port %s", port)
	log.Printf("Using KMS Key: %s", actualKeyARN)
	log.Printf("Data key rotation interval: %v", rotationInterval)
	log.Printf("Decryption cache TTL: %v", cacheTTL)
	log.Printf("Endpoints: /encode, /decode, /stats, /health")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
