// Package backup provides AES-256-GCM encrypted database backups stored in S3.
//
// Encryption format: [12-byte nonce][ciphertext + 16-byte GCM auth tag]
// The nonce is randomly generated for each backup and prepended to the output.
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

const NonceSize = 12 // AES-GCM standard nonce size (96 bits)

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce.
// Returns data in the format [12-byte nonce][ciphertext + 16-byte auth tag].
// key must be exactly 32 bytes (256 bits).
func Encrypt(plaintext []byte, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Seal appends the ciphertext (with auth tag) to the nonce.
	// Result format: [nonce][ciphertext + auth tag]
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts data that was encrypted by Encrypt.
// Expects format [12-byte nonce][ciphertext + 16-byte auth tag].
// Returns an error if authentication fails (data was tampered with or key is wrong).
func Decrypt(data []byte, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	if len(data) < NonceSize {
		return nil, fmt.Errorf("data too short: %d bytes (need at least %d)", len(data), NonceSize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	nonce := data[:NonceSize]
	ciphertext := data[NonceSize:]

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt/authenticate: %w", err)
	}
	return plaintext, nil
}

// KeyFromHex decodes a hex-encoded 32-byte (64 hex character) AES-256 key.
func KeyFromHex(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	return key, nil
}

// GenerateKey creates a new random 256-bit AES key and returns its hex encoding.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return hex.EncodeToString(key), nil
}
