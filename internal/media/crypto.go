package media

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	objectMagic       = "MEF1"  // MentisEterna File v1
	ChunkSize         = 1 << 20 // 1 MiB chunks
	nonceSize         = 12      // AES-GCM standard nonce
	maxChunkPlainSize = ChunkSize
)

// EncryptToFile encrypts plaintext from src to dst using AES-256-GCM in chunked mode.
// key must be 32 bytes. baseNonce must be at least nonceSize bytes.
// Returns hex-encoded SHA-256 of the ciphertext, plaintext size, and ciphertext size.
func EncryptToFile(src io.Reader, dst *os.File, key, baseNonce []byte) (ciphertextSHA256 string, plaintextSize int64, ciphertextSize int64, err error) {
	if len(key) != 32 {
		return "", 0, 0, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	if len(baseNonce) < nonceSize {
		return "", 0, 0, fmt.Errorf("base nonce must be at least %d bytes, got %d", nonceSize, len(baseNonce))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", 0, 0, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", 0, 0, fmt.Errorf("gcm: %w", err)
	}

	// Write header: magic[4] | version[1] | chunk_size[4]
	header := make([]byte, 9)
	copy(header[0:4], objectMagic)
	header[4] = 1 // version
	binary.BigEndian.PutUint32(header[5:9], ChunkSize)
	if _, err := dst.Write(header); err != nil {
		return "", 0, 0, fmt.Errorf("write header: %w", err)
	}
	ciphertextSize = 9

	h := sha256.New()
	h.Write(header)

	chunkIdx := uint64(0)
	plainBuf := make([]byte, maxChunkPlainSize)

	for {
		n, readErr := io.ReadFull(src, plainBuf)
		if n == 0 && readErr == io.EOF {
			break
		}
		if n > 0 {
			chunkNonce := deriveChunkNonce(baseNonce, chunkIdx)
			cipherChunk := aead.Seal(nil, chunkNonce, plainBuf[:n], nil)

			// Write length-prefixed: plain_len[4] | ciphertext
			lenBuf := make([]byte, 4)
			binary.BigEndian.PutUint32(lenBuf, uint32(n))
			if _, err := dst.Write(lenBuf); err != nil {
				return "", 0, 0, fmt.Errorf("write chunk len: %w", err)
			}
			h.Write(lenBuf)

			if _, err := dst.Write(cipherChunk); err != nil {
				return "", 0, 0, fmt.Errorf("write chunk %d: %w", chunkIdx, err)
			}
			h.Write(cipherChunk)

			ciphertextSize += int64(4 + len(cipherChunk))
			plaintextSize += int64(n)
			chunkIdx++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return "", 0, 0, fmt.Errorf("read plaintext: %w", readErr)
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil)), plaintextSize, ciphertextSize, nil
}

// DecryptToWriter decrypts encrypted data from src to dst using AES-256-GCM.
// Each chunk is authenticated before its plaintext is written to dst.
func DecryptToWriter(src io.Reader, dst io.Writer, key, baseNonce []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	if len(baseNonce) < nonceSize {
		return fmt.Errorf("base nonce must be at least %d bytes, got %d", nonceSize, len(baseNonce))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("gcm: %w", err)
	}

	// Read and verify header
	header := make([]byte, 9)
	if _, err := io.ReadFull(src, header); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if string(header[0:4]) != objectMagic {
		return errors.New("invalid magic bytes")
	}
	if header[4] != 1 {
		return fmt.Errorf("unsupported version: %d", header[4])
	}

	chunkIdx := uint64(0)
	lenBuf := make([]byte, 4)

	for {
		_, err := io.ReadFull(src, lenBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read chunk %d len: %w", chunkIdx, err)
		}
		plainLen := binary.BigEndian.Uint32(lenBuf)

		cipherLen := int(plainLen) + aead.Overhead()
		cipherBuf := make([]byte, cipherLen)
		if _, err := io.ReadFull(src, cipherBuf); err != nil {
			return fmt.Errorf("read chunk %d ciphertext: %w", chunkIdx, err)
		}

		chunkNonce := deriveChunkNonce(baseNonce, chunkIdx)
		plainBuf, err := aead.Open(nil, chunkNonce, cipherBuf, nil)
		if err != nil {
			return fmt.Errorf("decrypt/verify chunk %d: %w", chunkIdx, err)
		}

		if _, err := dst.Write(plainBuf); err != nil {
			return fmt.Errorf("write chunk %d plaintext: %w", chunkIdx, err)
		}
		chunkIdx++
	}

	return nil
}

// deriveChunkNonce creates a unique nonce by XOR-ing the chunk index
// into the last 8 bytes of the base nonce.
func deriveChunkNonce(baseNonce []byte, chunkIdx uint64) []byte {
	nonce := make([]byte, nonceSize)
	copy(nonce, baseNonce[:nonceSize])
	for i := 0; i < 8; i++ {
		nonce[nonceSize-8+i] ^= byte(chunkIdx >> (i * 8))
	}
	return nonce
}

// GenerateFileKey creates a random 256-bit AES key.
func GenerateFileKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// GenerateBaseNonce creates a random 12-byte base nonce for AES-GCM.
func GenerateBaseNonce() ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return nonce, nil
}
