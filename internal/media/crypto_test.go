package media

import (
	"bytes"
	"crypto/rand"
	"os"
	"testing"
)

func TestChunkedEncryptDecryptRoundTrip(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	// Create plaintext that spans multiple chunks
	plaintext := make([]byte, 3*ChunkSize+500) // 3 full chunks + partial
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}

	// Encrypt to temp file
	tmp, err := os.CreateTemp("", "enc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	sha, ptSize, ctSize, err := EncryptToFile(bytes.NewReader(plaintext), tmp, key, nonce)
	if err != nil {
		t.Fatalf("EncryptToFile: %v", err)
	}
	if sha == "" {
		t.Error("expected non-empty SHA-256")
	}
	if ptSize != int64(len(plaintext)) {
		t.Errorf("plaintext size: got %d, want %d", ptSize, len(plaintext))
	}
	if ctSize <= ptSize {
		t.Error("ciphertext should be larger than plaintext due to auth tags")
	}
	tmp.Close()

	// Decrypt back
	tmp, err = os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()

	var decrypted bytes.Buffer
	if err := DecryptToWriter(tmp, &decrypted, key, nonce); err != nil {
		t.Fatalf("DecryptToWriter: %v", err)
	}

	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("round-trip: decrypted != plaintext")
	}
	t.Logf("round-trip: %d bytes plain → %d bytes cipher → OK", ptSize, ctSize)
}

func TestChunkTamperIsRejected(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, ChunkSize+100)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}

	tmp, err := os.CreateTemp("", "enc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	_, _, _, err = EncryptToFile(bytes.NewReader(plaintext), tmp, key, nonce)
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	// Tamper with the encrypted file (flip a byte in the second chunk's ciphertext)
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Find second chunk: skip header (9) + first chunk (4 + cipherLen)
	// The first chunk has plainLen = ChunkSize, cipherLen = ChunkSize + 16 (GCM tag)
	offset := 9 + 4 + ChunkSize + 16 + 4 // header + first chunk + second chunk len
	if offset < len(data) {
		data[offset] ^= 0xFF
	}
	if err := os.WriteFile(tmp.Name(), data, 0644); err != nil {
		t.Fatal(err)
	}

	// Decrypt should fail
	tmp, err = os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()

	var decrypted bytes.Buffer
	err = DecryptToWriter(tmp, &decrypted, key, nonce)
	if err == nil {
		t.Error("expected auth failure after tampering, got nil")
	}
	t.Logf("tamper detection: %v", err)
}

func TestNonceDerivationIsUniquePerChunk(t *testing.T) {
	baseNonce := make([]byte, nonceSize)
	if _, err := rand.Read(baseNonce); err != nil {
		t.Fatal(err)
	}

	n0 := deriveChunkNonce(baseNonce, 0)
	n1 := deriveChunkNonce(baseNonce, 1)
	n2 := deriveChunkNonce(baseNonce, 2)

	if bytes.Equal(n0, n1) {
		t.Error("chunk 0 and 1 nonces must not be equal")
	}
	if bytes.Equal(n1, n2) {
		t.Error("chunk 1 and 2 nonces must not be equal")
	}

	// Verify the nonce is deterministic: calling again gives same result
	n0b := deriveChunkNonce(baseNonce, 0)
	if !bytes.Equal(n0, n0b) {
		t.Error("nonce derivation should be deterministic")
	}
}
