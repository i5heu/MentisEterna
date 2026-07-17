package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
	if err := DecryptToWriter(context.Background(), tmp, &decrypted, key, nonce); err != nil {
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
	err = DecryptToWriter(context.Background(), tmp, &decrypted, key, nonce)
	if err == nil {
		t.Error("expected auth failure after tampering, got nil")
	}
	t.Logf("tamper detection: %v", err)
}

// --- DecryptToWriter edge cases ---

// failingWriter fails with the given error after n bytes have been written.
type failingWriter struct {
	total     int
	failAfter int
	failErr   error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.total >= w.failAfter {
		return 0, w.failErr
	}
	// Write as much as possible without exceeding the fail point.
	n := len(p)
	remaining := w.failAfter - w.total
	if n > remaining {
		n = remaining
	}
	w.total += n
	return n, nil
}

func TestDecryptToWriterSingleChunk(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, 100)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}

	// Create a temp file for encryption
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

	ctReader, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ctReader.Close()

	var decrypted bytes.Buffer
	if err := DecryptToWriter(context.Background(), ctReader, &decrypted, key, nonce); err != nil {
		t.Fatalf("DecryptToWriter: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("single chunk: decrypted != plaintext")
	}
}

func TestDecryptToWriterEmptyFile(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	// Empty file: just header, no chunks.
	tmp, err := os.CreateTemp("", "enc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	_, _, _, err = EncryptToFile(bytes.NewReader([]byte{}), tmp, key, nonce)
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	ctReader, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ctReader.Close()

	var decrypted bytes.Buffer
	if err := DecryptToWriter(context.Background(), ctReader, &decrypted, key, nonce); err != nil {
		t.Fatalf("DecryptToWriter empty: %v", err)
	}
	if decrypted.Len() != 0 {
		t.Errorf("expected 0 bytes, got %d", decrypted.Len())
	}
}

func TestDecryptToWriterWriterFailsMidStream(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, 3*ChunkSize+500) // 3 full chunks + partial
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

	ctReader, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ctReader.Close()

	// Simulate broken pipe: writer fails after 1.5 chunks.
	fakePipeErr := fmt.Errorf("write tcp 127.0.0.1:8080->127.0.0.1:12345: write: broken pipe")
	fw := &failingWriter{
		failAfter: ChunkSize + ChunkSize/2,
		failErr:   fakePipeErr,
	}

	err = DecryptToWriter(context.Background(), ctReader, fw, key, nonce)
	if err == nil {
		t.Error("expected error from failing writer, got nil")
	}
	if !strings.Contains(err.Error(), "write chunk") {
		t.Errorf("expected error to reference chunk write, got: %v", err)
	}
	if !strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("expected error to wrap broken pipe, got: %v", err)
	}
	t.Logf("mid-stream failure: %v", err)
}

func TestDecryptToWriterWriterFailsOnFirstChunk(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, 2*ChunkSize)
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

	ctReader, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ctReader.Close()

	// Writer fails immediately — simulates a client that disconnected
	// before any data was sent.
	fw := &failingWriter{
		failAfter: 0,
		failErr:   fmt.Errorf("write: broken pipe"),
	}

	err = DecryptToWriter(context.Background(), ctReader, fw, key, nonce)
	if err == nil {
		t.Error("expected error from failing writer, got nil")
	}
	// First chunk error should reference chunk 0.
	if !strings.Contains(err.Error(), "write chunk 0") {
		t.Errorf("expected 'write chunk 0' in error, got: %v", err)
	}
	t.Logf("first-chunk failure: %v", err)
}

func TestDecryptToWriterCancelledContextBeforeChunks(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, 3*ChunkSize)
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

	ctReader, err := os.Open(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ctReader.Close()

	// Cancel before starting — the loop should abort before the first chunk write.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = DecryptToWriter(cancelCtx, ctReader, io.Discard, key, nonce)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	t.Logf("cancelled context: %v", err)
}

func TestDecryptToWriterTruncatedCiphertext(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	plaintext := make([]byte, ChunkSize+500)
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

	// Read the full ciphertext, then truncate the last chunk.
	fullData, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Truncate: keep header + first chunk (9 + 4 + ChunkSize + 16 = ChunkSize + 29)
	// This leaves the second chunk's length prefix partially present.
	truncLen := 9 + 4 + ChunkSize + 16 + 2 // partial second len prefix
	if truncLen > len(fullData) {
		t.Skip("test data too small")
	}
	truncated := fullData[:truncLen]

	err = DecryptToWriter(context.Background(), bytes.NewReader(truncated), io.Discard, key, nonce)
	if err == nil {
		t.Error("expected error from truncated ciphertext, got nil")
	}
	// Should fail reading chunk len, not decrypt.
	t.Logf("truncated error: %v", err)
}

func TestDecryptToWriterCorruptedHeader(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	// Corrupted magic bytes
	data := make([]byte, 9)
	copy(data[0:4], "XXXX")
	data[4] = 1

	err := DecryptToWriter(context.Background(), bytes.NewReader(data), io.Discard, key, nonce)
	if err == nil {
		t.Error("expected error from corrupted magic bytes")
	}
	if !strings.Contains(err.Error(), "invalid magic") {
		t.Errorf("expected 'invalid magic' in error, got: %v", err)
	}
}

func TestDecryptToWriterUnsupportedVersion(t *testing.T) {
	key, _ := GenerateFileKey()
	nonce, _ := GenerateBaseNonce()

	data := make([]byte, 9)
	copy(data[0:4], objectMagic)
	data[4] = 99 // unsupported version

	err := DecryptToWriter(context.Background(), bytes.NewReader(data), io.Discard, key, nonce)
	if err == nil {
		t.Error("expected error from unsupported version")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("expected 'unsupported version' in error, got: %v", err)
	}
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
