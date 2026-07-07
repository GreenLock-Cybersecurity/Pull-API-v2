// +build ignore

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Run: go run scripts/encrypt_keys.go <APP_KEY> <secret_to_encrypt>
//
// Example:
//   go run scripts/encrypt_keys.go abc123...64chars... "your_service_key_here"

func main() {
	if len(os.Args) < 3 {
		// If no args, generate a new APP_KEY
		if len(os.Args) == 1 || os.Args[1] == "generate" {
			key := make([]byte, 32)
			rand.Read(key)
			fmt.Println("New APP_KEY (64 hex chars):")
			fmt.Println(hex.EncodeToString(key))
			return
		}
		fmt.Println("Usage: go run scripts/encrypt_keys.go <APP_KEY> <secret>")
		fmt.Println("   or: go run scripts/encrypt_keys.go generate")
		os.Exit(1)
	}

	appKey := os.Args[1]
	secret := os.Args[2]

	if len(appKey) != 64 {
		fmt.Printf("Error: APP_KEY must be 64 hex chars (got %d)\n", len(appKey))
		os.Exit(1)
	}

	key, err := hex.DecodeString(appKey)
	if err != nil {
		fmt.Printf("Error decoding APP_KEY: %v\n", err)
		os.Exit(1)
	}

	encrypted, err := encrypt(key, secret)
	if err != nil {
		fmt.Printf("Error encrypting: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Encrypted value:")
	fmt.Println(encrypted)
}

func encrypt(key []byte, plaintext string) (string, error) {
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

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
