package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// CurrentDataKey represents the current active data key
type CurrentDataKey struct {
	PlaintextKey []byte
	EncryptedKey string
	GeneratedAt  time.Time
	ExpiresAt    time.Time
}

// CachedKey represents a cached decrypted data key (for decryption of old data)
type CachedKey struct {
	Key       []byte
	ExpiresAt time.Time
}

// KMSManager handles KMS operations with time-based key rotation
type KMSManager struct {
	client              *kms.Client
	keyID               string
	currentDataKey      *CurrentDataKey
	decryptionCache     map[string]*CachedKey
	mux                 sync.RWMutex
	cacheTTL            time.Duration
	keyRotationInterval time.Duration
}

// NewKMSManager creates a new KMS manager with time-based rotation
func NewKMSManager(keyID string, cacheTTL time.Duration, rotationInterval time.Duration) (*KMSManager, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	manager := &KMSManager{
		client:              kms.NewFromConfig(cfg),
		keyID:               keyID,
		decryptionCache:     make(map[string]*CachedKey),
		cacheTTL:            cacheTTL,
		keyRotationInterval: rotationInterval,
	}

	// Generate initial data key
	ctx := context.Background()
	if err := manager.rotateDataKey(ctx); err != nil {
		return nil, fmt.Errorf("failed to generate initial data key: %w", err)
	}

	return manager, nil
}

// GetCurrentDataKey returns the current data key, rotating if necessary
func (k *KMSManager) GetCurrentDataKey(ctx context.Context) (*CurrentDataKey, error) {
	k.mux.RLock()
	currentKey := k.currentDataKey
	k.mux.RUnlock()

	// Check if rotation is needed
	if currentKey == nil || time.Now().After(currentKey.ExpiresAt) {
		k.mux.Lock()
		// Double-check after acquiring write lock
		if k.currentDataKey == nil || time.Now().After(k.currentDataKey.ExpiresAt) {
			if err := k.rotateDataKeyLocked(ctx); err != nil {
				k.mux.Unlock()
				return nil, err
			}
		}
		currentKey = k.currentDataKey
		k.mux.Unlock()
	}

	return currentKey, nil
}

// rotateDataKey rotates the current data key (public method)
func (k *KMSManager) rotateDataKey(ctx context.Context) error {
	k.mux.Lock()
	defer k.mux.Unlock()
	return k.rotateDataKeyLocked(ctx)
}

// rotateDataKeyLocked rotates the current data key (assumes lock is held)
func (k *KMSManager) rotateDataKeyLocked(ctx context.Context) error {
	log.Printf("Generating new data key...")

	input := &kms.GenerateDataKeyInput{
		KeyId:   aws.String(k.keyID),
		KeySpec: types.DataKeySpecAes256,
		EncryptionContext: map[string]string{
			"service":   "temporal-codec",
			"version":   "1.0",
			"timestamp": fmt.Sprintf("%d", time.Now().Unix()),
		},
	}

	result, err := k.client.GenerateDataKey(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to generate data key: %w", err)
	}

	// Zero out old key if it exists
	if k.currentDataKey != nil {
		for i := range k.currentDataKey.PlaintextKey {
			k.currentDataKey.PlaintextKey[i] = 0
		}
	}

	// Set new current data key
	now := time.Now()
	k.currentDataKey = &CurrentDataKey{
		PlaintextKey: result.Plaintext,
		EncryptedKey: base64.StdEncoding.EncodeToString(result.CiphertextBlob),
		GeneratedAt:  now,
		ExpiresAt:    now.Add(k.keyRotationInterval),
	}

	log.Printf("New data key generated, expires at: %v", k.currentDataKey.ExpiresAt)
	return nil
}

// DecryptDataKey decrypts an encrypted data key using KMS with caching
func (k *KMSManager) DecryptDataKey(ctx context.Context, encryptedKey string, masterKeyARN string) ([]byte, error) {
	// Check if this is the current key (most common case)
	k.mux.RLock()
	if k.currentDataKey != nil && k.currentDataKey.EncryptedKey == encryptedKey {
		key := k.currentDataKey.PlaintextKey
		k.mux.RUnlock()
		return key, nil
	}
	k.mux.RUnlock()

	// Check decryption cache for older keys
	k.mux.RLock()
	cacheKey := fmt.Sprintf("%s:%s", encryptedKey, masterKeyARN)
	if cached, exists := k.decryptionCache[cacheKey]; exists && time.Now().Before(cached.ExpiresAt) {
		key := cached.Key
		k.mux.RUnlock()
		return key, nil
	}
	k.mux.RUnlock()

	// Decrypt using KMS (for older keys)
	encryptedBlob, err := base64.StdEncoding.DecodeString(encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted key: %w", err)
	}

	input := &kms.DecryptInput{
		CiphertextBlob: encryptedBlob,
		KeyId:          aws.String(masterKeyARN),
	}

	result, err := k.client.Decrypt(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data key: %w", err)
	}

	// Cache the decrypted key for future use
	k.mux.Lock()
	k.decryptionCache[encryptedKey] = &CachedKey{
		Key:       result.Plaintext,
		ExpiresAt: time.Now().Add(k.cacheTTL),
	}
	k.mux.Unlock()

	log.Printf("Decrypted and cached older data key")
	return result.Plaintext, nil
}

// CleanupCache removes expired keys from cache
func (k *KMSManager) CleanupCache() {
	k.mux.Lock()
	defer k.mux.Unlock()

	now := time.Now()
	cleanedCount := 0

	for key, cached := range k.decryptionCache {
		if now.After(cached.ExpiresAt) {
			// Zero out the key before deleting
			for i := range cached.Key {
				cached.Key[i] = 0
			}
			delete(k.decryptionCache, key)
			cleanedCount++
		}
	}

	if cleanedCount > 0 {
		log.Printf("Cleaned up %d expired cached keys", cleanedCount)
	}
}

// StartCacheCleanup starts background routines for cache cleanup and key rotation monitoring
func (k *KMSManager) StartCacheCleanup(cleanupInterval time.Duration) {
	// Cache cleanup routine
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			k.CleanupCache()
		}
	}()

	// Key rotation monitoring routine
	go func() {
		ticker := time.NewTicker(1 * time.Minute) // Check every minute
		defer ticker.Stop()
		for range ticker.C {
			k.mux.RLock()
			if k.currentDataKey != nil && time.Until(k.currentDataKey.ExpiresAt) < 5*time.Minute {
				expiresIn := time.Until(k.currentDataKey.ExpiresAt)
				log.Printf("Current data key expires in %v", expiresIn)
			}
			k.mux.RUnlock()
		}
	}()
}

// GetKeyStats returns statistics about current key usage
func (k *KMSManager) GetKeyStats() map[string]interface{} {
	k.mux.RLock()
	defer k.mux.RUnlock()

	stats := map[string]interface{}{
		"cached_keys_count": len(k.decryptionCache),
	}

	if k.currentDataKey != nil {
		stats["current_key_age"] = time.Since(k.currentDataKey.GeneratedAt).String()
		stats["current_key_expires_in"] = time.Until(k.currentDataKey.ExpiresAt).String()
		stats["current_key_expired"] = time.Now().After(k.currentDataKey.ExpiresAt)
	}

	return stats
}

// EncryptWithDataKey encrypts data using AES-GCM with the provided key
func EncryptWithDataKey(data []byte, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes for AES-256")
	}

	block, err := aes.NewCipher(key)
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

// DecryptWithDataKey decrypts base64 encoded data using AES-GCM with the provided key
func DecryptWithDataKey(encodedData string, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes for AES-256")
	}

	data, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	block, err := aes.NewCipher(key)
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
