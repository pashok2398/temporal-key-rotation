package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"temporal-worker/shared"
)

// EncryptionCodec handles encryption/decryption of payloads
type EncryptionCodec struct {
	keyID string
	key   []byte
}

// NewEncryptionCodec creates a new encryption codec with a 32-byte key
func NewEncryptionCodec(key []byte, keyID string) *EncryptionCodec {
	if len(key) != 32 {
		panic("Key must be 32 bytes for AES-256")
	}
	return &EncryptionCodec{key: key, keyID: keyID}
}

// encrypt encrypts data using AES-GCM and returns base64 encoded result
func (c *EncryptionCodec) encrypt(data []byte) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decrypts base64 encoded data using AES-GCM
func (c *EncryptionCodec) decrypt(encodedData string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// handleEncode handles the /encode endpoint
func (c *EncryptionCodec) handleEncode(w http.ResponseWriter, r *http.Request) {
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
	for _, payload := range req.Payloads {
		// Only process JSON payloads that aren't already encrypted
		encoding, exists := payload.Metadata["encoding"]
		if !exists || encoding == "json/plain" {
			// Encrypt the data (input is base64 decoded first if needed)
			var dataToEncrypt []byte
			if payload.Data != "" {
				// Try to decode as base64 first, if it fails, use as plain text
				if decoded, err := base64.StdEncoding.DecodeString(payload.Data); err == nil {
					dataToEncrypt = decoded
				} else {
					dataToEncrypt = []byte(payload.Data)
				}
			}

			encryptedData, err := c.encrypt(dataToEncrypt)
			if err != nil {
				http.Error(w, "Encryption failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Create response payload
			encodedPayload := shared.PayloadData{
				Metadata: map[string]string{
					"encoding": "binary/encrypted",
					"keyID":    c.keyID,
				},
				Data: encryptedData,
			}

			response.Payloads = append(response.Payloads, encodedPayload)
		} else {
			// Already encrypted or different encoding, return as-is
			response.Payloads = append(response.Payloads, payload)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// handleDecode handles the /decode endpoint
func (c *EncryptionCodec) handleDecode(w http.ResponseWriter, r *http.Request) {
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
	for _, payload := range req.Payloads {
		// Check if this payload is encrypted
		if payload.Metadata["encoding"] != "binary/encrypted" {
			// Not encrypted, return as-is
			response.Payloads = append(response.Payloads, payload)
			continue
		}

		// Decrypt the data
		decryptedData, err := c.decrypt(payload.Data)
		if err != nil {
			http.Error(w, "Decryption failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Create response payload with base64 encoded decrypted data
		decodedPayload := shared.PayloadData{
			Metadata: map[string]string{
				"encoding": "json/plain",
				"keyID":    c.keyID,
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

func main() {
	// Get encryption key from environment variable or use default (not recommended for production)
	keyStr := os.Getenv("CODEC_KEY")
	if keyStr == "" {
		log.Println("WARNING: Using default key. Set CODEC_KEY environment variable for production!")
		keyStr = "12345678901234567890123456789012" // 32 bytes
	}

	if len(keyStr) != 32 {
		log.Fatal("CODEC_KEY must be exactly 32 bytes")
	}
	keyID := "123abc"

	codec := NewEncryptionCodec([]byte(keyStr), keyID)

	// Set up routes
	http.HandleFunc("/encode", codec.handleEncode)
	http.HandleFunc("/decode", codec.handleDecode)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Codec server starting on port %s", port)
	log.Printf("Endpoints: /encode, /decode, /health")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
