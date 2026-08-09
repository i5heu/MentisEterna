package media

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// blockingSTTer blocks inside RunSTT until release is closed and signals
// entry by closing entered.
type blockingSTTer struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSTTer) RunSTT(_ []byte, _ string) (string, error) {
	close(b.entered)
	<-b.release
	return "transcribed", nil
}

// TestRunSTTForFileSerializesPerFile verifies that two concurrent
// RunSTTForFile calls for the same file do not run the STT model in parallel:
// the second call blocks until the first completes.
func TestRunSTTForFileSerializesPerFile(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Insert a files row directly (pattern of TestSaveAndGetOCRResult).
	key, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	nonce, err := GenerateBaseNonce()
	if err != nil {
		t.Fatalf("generate nonce: %v", err)
	}

	cipherFile, err := os.CreateTemp(t.TempDir(), "clip-*.m4a")
	if err != nil {
		t.Fatalf("create cipher file: %v", err)
	}
	cipherSHA, _, cipherSize, err := EncryptToFile(bytes.NewReader([]byte("audio")), cipherFile, key, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cipherData, err := os.ReadFile(cipherFile.Name())
	if err != nil {
		t.Fatalf("read cipher file: %v", err)
	}
	cipherFile.Close()

	res, err := svc.DB.Exec(
		`INSERT INTO files (storage_key, filename, mime_type, size_bytes, ciphertext_sha256, aes_key, aes_nonce)
		 VALUES (?, 'clip.m4a', 'audio/mp4', ?, ?, ?, ?)`,
		"stt-test-key", cipherSize, cipherSHA, key, nonce,
	)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Put ciphertext in cache (same call pattern as the replica path in stt.go).
	if err := svc.Cache.Put(fileID, cipherSHA, bytes.NewReader(cipherData)); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	release := make(chan struct{})
	stt1 := &blockingSTTer{entered: make(chan struct{}), release: release}
	stt2 := &blockingSTTer{entered: make(chan struct{}), release: release}

	type result struct {
		res *STTResult
		err error
	}
	results := make(chan result, 2)

	go func() {
		r, err := svc.RunSTTForFile(ctx, fileID, stt1)
		results <- result{r, err}
	}()
	select {
	case <-stt1.entered: // first call is inside the STT model, holding the lock
	case <-time.After(2 * time.Second):
		t.Fatal("first RunSTTForFile never entered the STT model")
	}

	go func() {
		r, err := svc.RunSTTForFile(ctx, fileID, stt2)
		results <- result{r, err}
	}()

	// The second call must NOT enter the STT model while the first is in flight.
	select {
	case <-stt2.entered:
		t.Fatal("second RunSTTForFile entered the STT model while the first was in flight — per-file serialization missing")
	case <-time.After(300 * time.Millisecond):
	}

	close(release) // first call finishes; second proceeds afterwards

	for i := range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("RunSTTForFile %d: %v", i, r.err)
		}
		if r.res == nil || r.res.STTText != "transcribed" {
			t.Fatalf("RunSTTForFile %d: unexpected result %+v", i, r.res)
		}
	}

	// Both calls completed, so the second must have entered the model after the
	// first released (close happens-before the result send we already received).
	select {
	case <-stt2.entered:
	default:
		t.Fatal("second RunSTTForFile never entered the STT model after the lock was released")
	}
}
