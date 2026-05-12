package backup

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := []byte("The quick brown fox jumps over the lazy dog. This is a test of AES-256-GCM encryption for MentisEterna backups.")

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if len(encrypted) < NonceSize {
		t.Fatalf("Encrypted data too short: %d bytes", len(encrypted))
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("Round-trip failed: plaintext != decrypted")
	}
}

func TestDecryptCorruptedData(t *testing.T) {
	key := make([]byte, 32)

	plaintext := []byte("test data for corruption check")
	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the ciphertext
	corrupted := make([]byte, len(encrypted))
	copy(corrupted, encrypted)
	corrupted[len(encrypted)-5] ^= 0x01

	_, err = Decrypt(corrupted, key)
	if err == nil {
		t.Fatal("Expected authentication error for corrupted data, got nil")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1

	plaintext := []byte("test data")
	encrypted, err := Encrypt(plaintext, key1)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("Expected authentication error for wrong key, got nil")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := Decrypt([]byte("short"), key)
	if err == nil {
		t.Fatal("Expected error for too-short data, got nil")
	}
}

func TestEncryptBadKeySize(t *testing.T) {
	_, err := Encrypt([]byte("data"), make([]byte, 16))
	if err == nil {
		t.Fatal("Expected error for 16-byte key, got nil")
	}
}

func TestDecryptBadKeySize(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("test data")
	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = Decrypt(encrypted, make([]byte, 16))
	if err == nil {
		t.Fatal("Expected error for 16-byte key on decrypt, got nil")
	}
}

func TestEncryptNonceUnique(t *testing.T) {
	key := make([]byte, 32)
	data := []byte("test")

	enc1, err := Encrypt(data, key)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	enc2, err := Encrypt(data, key)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	// Nonces (first 12 bytes) should differ.
	nonce1 := enc1[:NonceSize]
	nonce2 := enc2[:NonceSize]
	if bytes.Equal(nonce1, nonce2) {
		t.Fatal("Nonces should be unique per encryption")
	}
}

func TestEncryptLargeData(t *testing.T) {
	key := make([]byte, 32)
	// 10 MB of data — simulates a real database backup
	plaintext := make([]byte, 10*1024*1024)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt large: %v", err)
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt large: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatal("Large round-trip failed")
	}
}

func TestKeyFromHex(t *testing.T) {
	// Generate a key and convert it
	hexKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if len(hexKey) != 64 {
		t.Fatalf("Expected 64 hex chars, got %d", len(hexKey))
	}

	key, err := KeyFromHex(hexKey)
	if err != nil {
		t.Fatalf("KeyFromHex: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("Expected 32 bytes, got %d", len(key))
	}
}

func TestKeyFromHexInvalid(t *testing.T) {
	_, err := KeyFromHex("not-hex")
	if err == nil {
		t.Fatal("Expected error for invalid hex")
	}

	_, err = KeyFromHex("deadbeef") // 4 bytes = too short
	if err == nil {
		t.Fatal("Expected error for short key")
	}
}
