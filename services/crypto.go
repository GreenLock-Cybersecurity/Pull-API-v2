package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// CryptoService handles encryption/decryption of sensitive data
type CryptoService struct {
	key []byte
}

// NewCryptoService creates a new crypto service with the given hex key
func NewCryptoService(hexKey string) (*CryptoService, error) {
	if len(hexKey) != 64 {
		return nil, fmt.Errorf("key must be 64 hex characters (32 bytes)")
	}

	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}

	return &CryptoService{key: key}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM
func (c *CryptoService) Encrypt(plaintext string) (string, error) {
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

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext encrypted with AES-256-GCM
func (c *CryptoService) Decrypt(ciphertext string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GenerateKey generates a random 32-byte key as hex string
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// Global crypto service (initialized by DatabaseRouter)
var globalCrypto *CryptoService

// SetGlobalCrypto sets the global crypto service
func SetGlobalCrypto(c *CryptoService) {
	globalCrypto = c
}

// EncryptServiceKey encrypts a Supabase service key using the global crypto service
func EncryptServiceKey(plaintext string) (string, error) {
	if globalCrypto == nil {
		return "", fmt.Errorf("crypto service not initialized")
	}
	return globalCrypto.Encrypt(plaintext)
}

// DecryptServiceKey decrypts a Supabase service key using the global crypto service
func DecryptServiceKey(ciphertext string) (string, error) {
	if globalCrypto == nil {
		return "", fmt.Errorf("crypto service not initialized")
	}
	return globalCrypto.Decrypt(ciphertext)
}
