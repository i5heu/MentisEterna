package media

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Cache manages encrypted local file storage.
type Cache struct {
	Root string
}

// PathFor returns the cache file path for a given file ID and ciphertext SHA-256.
func (c Cache) PathFor(fileID int64, ciphertextSHA256 string) string {
	return filepath.Join(c.Root, fmt.Sprintf("%d-%s.enc", fileID, ciphertextSHA256))
}

// Put atomically writes encrypted data to the cache.
func (c Cache) Put(fileID int64, ciphertextSHA256 string, src io.Reader) error {
	dst := c.PathFor(fileID, ciphertextSHA256)

	// Ensure cache directory exists
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	// Write to temp file, then rename for atomicity
	tmp, err := os.CreateTemp(c.Root, "tmp-*.enc")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("rename to cache: %w", err)
	}
	return nil
}

// Open returns a reader for an encrypted file in the cache.
// Returns os.ErrNotExist if the file is not cached.
func (c Cache) Open(fileID int64, ciphertextSHA256 string) (*os.File, error) {
	path := c.PathFor(fileID, ciphertextSHA256)
	return os.Open(path)
}

// Delete removes an encrypted file from the cache.
func (c Cache) Delete(fileID int64, ciphertextSHA256 string) error {
	path := c.PathFor(fileID, ciphertextSHA256)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete cache: %w", err)
	}
	return nil
}
