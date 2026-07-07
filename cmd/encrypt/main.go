package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Usage:
//   go run cmd/encrypt/main.go
//
// This tool encrypts secrets using AES-256-GCM with your APP_KEY.
// Use the encrypted values when inserting venue database configs.

func main() {
	fmt.Println("==============================================")
	fmt.Println("Pull API - Secret Encryption Utility")
	fmt.Println("==============================================")
	fmt.Println()

	// Get APP_KEY from environment or prompt
	appKey := os.Getenv("APP_KEY")
	if appKey == "" {
		fmt.Print("Enter your APP_KEY (64 hex characters): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		appKey = strings.TrimSpace(input)
	}

	if len(appKey) != 64 {
		fmt.Printf("Error: APP_KEY must be exactly 64 hex characters (got %d)\n", len(appKey))
		os.Exit(1)
	}

	key, err := hex.DecodeString(appKey)
	if err != nil {
		fmt.Printf("Error: Invalid hex key: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  1. Encrypt a secret")
		fmt.Println("  2. Decrypt a secret")
		fmt.Println("  3. Generate a new APP_KEY")
		fmt.Println("  4. Exit")
		fmt.Print("\nChoose an option: ")

		option, _ := reader.ReadString('\n')
		option = strings.TrimSpace(option)

		switch option {
		case "1":
			fmt.Print("Enter secret to encrypt: ")
			secret, _ := reader.ReadString('\n')
			secret = strings.TrimSpace(secret)

			encrypted, err := encrypt(key, secret)
			if err != nil {
				fmt.Printf("Error encrypting: %v\n", err)
				continue
			}
			fmt.Println()
			fmt.Println("Encrypted value (copy this):")
			fmt.Println(encrypted)

		case "2":
			fmt.Print("Enter encrypted value: ")
			encrypted, _ := reader.ReadString('\n')
			encrypted = strings.TrimSpace(encrypted)

			decrypted, err := decrypt(key, encrypted)
			if err != nil {
				fmt.Printf("Error decrypting: %v\n", err)
				continue
			}
			fmt.Println()
			fmt.Println("Decrypted value:")
			fmt.Println(decrypted)

		case "3":
			newKey := make([]byte, 32)
			if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
				fmt.Printf("Error generating key: %v\n", err)
				continue
			}
			fmt.Println()
			fmt.Println("New APP_KEY (save this securely):")
			fmt.Println(hex.EncodeToString(newKey))

		case "4":
			fmt.Println("Bye!")
			os.Exit(0)

		default:
			fmt.Println("Invalid option")
		}
	}
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

func decrypt(key []byte, ciphertext string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
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
