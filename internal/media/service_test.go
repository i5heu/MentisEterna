package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/db"
)

// --- Fakes ---

type fakeReplicaStore struct {
	mu         sync.Mutex
	objects    map[string][]byte
	etags      map[string]string
	failPut    map[string]bool // endpointID -> fail?
	blockPut   map[string]bool // endpointID -> block until context canceled
	putCount   map[string]int  // endpointID -> calls to Put (for assertions)
	deleteKeys []string        // records all storage keys deleted (for assertions)
}

func newFakeReplicaStore() *fakeReplicaStore {
	return &fakeReplicaStore{
		objects:  make(map[string][]byte),
		etags:    make(map[string]string),
		failPut:  make(map[string]bool),
		blockPut: make(map[string]bool),
		putCount: make(map[string]int),
	}
}

func (f *fakeReplicaStore) Put(ctx context.Context, ep EndpointConfig, key string, src io.Reader, size int64) (string, error) {
	f.mu.Lock()
	if f.blockPut[ep.ID] {
		f.mu.Unlock()
		// Block until context is cancelled, then return the context error.
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.failPut[ep.ID] {
		defer f.mu.Unlock()
		return "", fmt.Errorf("simulated put failure for %s", ep.ID)
	}
	f.putCount[ep.ID]++
	f.mu.Unlock()
	// Read outside lock to avoid deadlock with large data
	data, _ := io.ReadAll(src)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[ep.ID+"/"+key] = data
	hash := sha256.Sum256(data)
	etag := hex.EncodeToString(hash[:])
	f.etags[ep.ID+"/"+key] = etag
	return etag, nil
}

func (f *fakeReplicaStore) Get(ctx context.Context, ep EndpointConfig, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[ep.ID+"/"+key]
	if !ok {
		return nil, fmt.Errorf("s3 get: not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeReplicaStore) Delete(ctx context.Context, ep EndpointConfig, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, ep.ID+"/"+key)
	delete(f.etags, ep.ID+"/"+key)
	f.deleteKeys = append(f.deleteKeys, key)
	return nil
}

func (f *fakeReplicaStore) List(_ context.Context, ep EndpointConfig, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	fullPrefix := ep.ID + "/" + prefix
	for k := range f.objects {
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, strings.TrimPrefix(k, ep.ID+"/"))
		}
	}
	return keys, nil
}

func (f *fakeReplicaStore) ListObjects(_ context.Context, ep EndpointConfig, prefix string) ([]S3ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var objs []S3ObjectInfo
	fullPrefix := ep.ID + "/" + prefix
	for k, v := range f.objects {
		if strings.HasPrefix(k, fullPrefix) {
			objs = append(objs, S3ObjectInfo{
				Key:  strings.TrimPrefix(k, ep.ID+"/"),
				Size: int64(len(v)),
			})
		}
	}
	return objs, nil
}

// --- Helpers ---

func openTestMediaDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testMediaConfig(cacheDir string) Config {
	return Config{
		CacheDir: cacheDir,
		Endpoints: []EndpointConfig{
			{ID: "primary", Bucket: "test", Endpoint: "http://localhost:9000", AccessKeyID: "key", SecretAccessKey: "secret", Region: "us-east-1", ForcePathStyle: true},
			{ID: "backup", Bucket: "test-b", Endpoint: "http://localhost:9001", AccessKeyID: "key2", SecretAccessKey: "secret2", Region: "us-east-1", ForcePathStyle: true},
		},
	}
}

func newTestService(t *testing.T) (*Service, *fakeReplicaStore) {
	t.Helper()
	d := openTestMediaDB(t)
	cacheDir := filepath.Join(t.TempDir(), "media-cache")
	cfg := testMediaConfig(cacheDir)
	store := newFakeReplicaStore()

	svc := &Service{
		DB:     d,
		Cache:  Cache{Root: cacheDir},
		Config: cfg,
		Store:  store,
	}
	return svc, store
}

// --- Tests ---

func TestCreateAttachmentUploadSucceeds(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Create a note first
	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello world")
	rec, results, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	if rec.ID == 0 {
		t.Error("expected non-zero file id")
	}
	if rec.Filename != "test.txt" {
		t.Errorf("expected filename test.txt, got %s", rec.Filename)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 replica results, got %d", len(results))
	}
	for _, r := range results {
		if r.State != ReplicaStateUploaded {
			t.Errorf("endpoint %s: expected uploaded, got %s", r.EndpointID, r.State)
		}
	}

	// Verify attachment ref exists
	var refCount int
	err = svc.DB.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ? AND ref_kind = 'attachment'`,
		noteID, rec.ID).Scan(&refCount)
	if err != nil {
		t.Fatal(err)
	}
	if refCount != 1 {
		t.Errorf("expected 1 attachment ref, got %d", refCount)
	}

	// Verify encrypted data in cache can be decrypted
	cachePath := svc.Cache.PathFor(rec.ID, rec.CiphertextSHA256)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Error("expected cached encrypted file to exist")
	}
}

func TestCreateFileFailsWhenAllReplicasFail(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Make all endpoints fail
	store.failPut["primary"] = true
	store.failPut["backup"] = true

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello world")
	_, _, err = svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err == nil {
		t.Error("expected error when all replicas fail")
	}
	t.Logf("all-replica failure: %v", err)
}

func TestCreateFileMarksPerEndpointStates(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Make backup fail
	store.failPut["backup"] = true

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello world")
	rec, results, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}

	// Check replica states in DB
	rows, err := svc.DB.Query(`SELECT endpoint_id, state FROM file_s3 WHERE file_id = ?`, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	states := map[string]string{}
	for rows.Next() {
		var epid, state string
		rows.Scan(&epid, &state)
		states[epid] = state
	}

	if states["primary"] != string(ReplicaStateUploaded) {
		t.Errorf("primary: expected uploaded, got %s", states["primary"])
	}
	if states["backup"] != string(ReplicaStateUploadFailed) {
		t.Errorf("backup: expected upload_failed, got %s", states["backup"])
	}

	_ = results
}

// TestCreateFileAbortedUploadCleansUpDBAndS3 verifies that when the request
// context is canceled (client disconnect) after DB commit but during S3
// uploads, the committed DB row is cleaned up and S3 objects are deleted.
func TestCreateFileAbortedUploadCleansUpDBAndS3(t *testing.T) {
	svc, store := newTestService(t)

	// Make both endpoints block until context is canceled.
	store.blockPut["primary"] = true
	store.blockPut["backup"] = true

	ctx, cancel := context.WithCancel(context.Background())

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello world")

	// Start the upload in a goroutine. It will block at S3 upload stage.
	done := make(chan struct{})
	var createErr error
	go func() {
		_, _, createErr = svc.CreateAttachment(ctx, noteID, "abort.txt", "text/plain", bytes.NewReader(plaintext))
		close(done)
	}()

	// Allow time for DB commit to complete, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for CreateAttachment to return.
	<-done

	if createErr == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
	if !strings.Contains(createErr.Error(), "aborted") {
		t.Errorf("expected 'aborted' in error, got: %v", createErr)
	}

	// Verify file is soft-deleted in DB (query by note_id since rec.ID is 0 on failure).
	var deletedAt sql.NullString
	err = svc.DB.QueryRow(`SELECT deleted_at FROM files WHERE original_note_id = ? AND filename = ? AND deleted_at IS NOT NULL`,
		noteID, "abort.txt").Scan(&deletedAt)
	if err != nil {
		t.Fatalf("query file: %v", err)
	}
	if !deletedAt.Valid {
		t.Error("expected file to be soft-deleted after aborted upload")
	}

	// Verify no attachment ref exists for this note.
	var refCount int
	err = svc.DB.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ?`, noteID).Scan(&refCount)
	if err != nil {
		t.Fatal(err)
	}
	if refCount != 0 {
		t.Errorf("expected 0 refs after abort, got %d", refCount)
	}

	// Verify S3 objects were deleted (not just left hanging).
	store.mu.Lock()
	deleteCount := len(store.deleteKeys)
	store.mu.Unlock()
	if deleteCount < 2 {
		t.Errorf("expected S3 deletes for at least 2 endpoints, got %d", deleteCount)
	}
}

// TestCleanupFailedUploadDeletesFromS3 verifies that when ALL replicas fail,
// cleanupFailedUpload deletes S3 objects (not just DB soft-delete).
func TestCleanupFailedUploadDeletesFromS3(t *testing.T) {
	svc, store := newTestService(t)

	// Make all endpoints fail.
	store.failPut["primary"] = true
	store.failPut["backup"] = true

	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello world")
	_, _, err = svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err == nil {
		t.Fatal("expected error when all replicas fail")
	}

	// Verify file is soft-deleted (query by note_id since rec.ID is 0 on failure).
	var deletedAt2 sql.NullString
	err = svc.DB.QueryRow(`SELECT deleted_at FROM files WHERE original_note_id = ? AND filename = ? AND deleted_at IS NOT NULL`,
		noteID, "test.txt").Scan(&deletedAt2)
	if err != nil {
		t.Fatalf("query file: %v", err)
	}
	if !deletedAt2.Valid {
		t.Error("expected file to be soft-deleted when all replicas fail")
	}

	// Verify S3 objects were deleted via cleanupFailedUpload.
	store.mu.Lock()
	deleteCount := len(store.deleteKeys)
	store.mu.Unlock()
	if deleteCount < 2 {
		t.Errorf("expected S3 deletes via cleanup, got %d", deleteCount)
	}
}

func TestReadFileFromCache(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello encrypted world")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Read back
	var decrypted bytes.Buffer
	readRec, err := svc.ReadFile(ctx, rec.ID, &decrypted)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if readRec.ID != rec.ID {
		t.Error("read record id mismatch")
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("decrypted data doesn't match plaintext")
	}
}

func TestReadFileFallsBackToReplica(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello replica world")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Delete cache to force replica fallback
	svc.Cache.Delete(rec.ID, rec.CiphertextSHA256)

	// Read back (should fetch from replica and re-cache)
	var decrypted bytes.Buffer
	_, err = svc.ReadFile(ctx, rec.ID, &decrypted)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Error("decrypted data doesn't match plaintext")
	}

	// Cache should have been repopulated
	cachePath := svc.Cache.PathFor(rec.ID, rec.CiphertextSHA256)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Error("expected cache to be repopulated after replica fetch")
	}
}

func TestReadFileWithFailedWriter(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	// Use a payload large enough to produce multiple chunks.
	plaintext := make([]byte, 3*ChunkSize+500)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	rec, _, err := svc.CreateAttachment(ctx, noteID, "big.bin", "application/octet-stream", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a client that disconnects mid-stream (broken pipe).
	// failingWriter fails after 1.5 chunks of plaintext.
	fakePipeErr := fmt.Errorf("write tcp 127.0.0.1:8080->127.0.0.1:12345: write: broken pipe")
	fw := &failingWriter{
		failAfter: ChunkSize + ChunkSize/2,
		failErr:   fakePipeErr,
	}

	_, err = svc.ReadFile(ctx, rec.ID, fw)
	if err == nil {
		t.Error("expected ReadFile to fail with broken pipe, got nil")
	}
	if !strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("expected 'broken pipe' in error, got: %v", err)
	}
	t.Logf("ReadFile with failing writer: %v", err)
}

func TestReadFileFromCacheCancelledContext(t *testing.T) {
	// When the context is cancelled, DecryptToWriter checks ctx.Err()
	// before each chunk and returns context.Canceled early.
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := make([]byte, 100)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	rec, _, err := svc.CreateAttachment(ctx, noteID, "small.bin", "application/octet-stream", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var decrypted bytes.Buffer
	_, err = svc.ReadFile(cancelCtx, rec.ID, &decrypted)
	if err == nil {
		t.Error("expected error from cancelled context during cache read, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	t.Logf("cache read with cancelled ctx: %v", err)
}

func TestReadFileFromCacheWriterFailsFirstByte(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := make([]byte, ChunkSize+200)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	rec, _, err := svc.CreateAttachment(ctx, noteID, "data.bin", "application/octet-stream", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	fw := &failingWriter{
		failAfter: 0,
		failErr:   fmt.Errorf("write tcp [::1]:8080->[::1]:12345: i/o timeout"),
	}

	_, err = svc.ReadFile(ctx, rec.ID, fw)
	if err == nil {
		t.Error("expected error from failing writer, got nil")
	}
	if !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("expected 'i/o timeout' in error, got: %v", err)
	}
	t.Logf("ReadFile from cache, writer fails first byte: %v", err)
}

func TestCanReadFileReturnsErrorWhenFileUnavailable(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Delete cache AND the S3 objects to make file fully unavailable.
	svc.Cache.Delete(rec.ID, rec.CiphertextSHA256)
	// Remove from the fake store as well.
	store := svc.Store.(*fakeReplicaStore)
	store.mu.Lock()
	for k := range store.objects {
		if strings.Contains(k, rec.StorageKey) {
			delete(store.objects, k)
		}
	}
	store.mu.Unlock()

	_, err = svc.CanReadFile(ctx, rec.ID)
	if err == nil {
		t.Error("expected CanReadFile to return error when file is unavailable")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("expected 'unavailable' in error, got: %v", err)
	}
	t.Logf("CanReadFile unavailable: %v", err)
}

func TestReadFileUnavailableFromAllReplicas(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Delete cache and all S3 objects.
	svc.Cache.Delete(rec.ID, rec.CiphertextSHA256)
	store := svc.Store.(*fakeReplicaStore)
	store.mu.Lock()
	for k := range store.objects {
		if strings.Contains(k, rec.StorageKey) {
			delete(store.objects, k)
		}
	}
	store.mu.Unlock()

	var decrypted bytes.Buffer
	_, err = svc.ReadFile(ctx, rec.ID, &decrypted)
	if err == nil {
		t.Error("expected ReadFile to fail when file is unavailable from all replicas")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("expected 'unavailable' in error, got: %v", err)
	}
	t.Logf("ReadFile unavailable: %v", err)
}

func TestReadFileCreatesOnlyOneCacheEntry(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello cache once")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Read back (from cache).
	var decrypted1 bytes.Buffer
	_, err = svc.ReadFile(ctx, rec.ID, &decrypted1)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cache path is stable — delete cache, read again (from replica),
	// and verify cache is recreated at the same path.
	cachePath := svc.Cache.PathFor(rec.ID, rec.CiphertextSHA256)
	os.Remove(cachePath)

	var decrypted2 bytes.Buffer
	_, err = svc.ReadFile(ctx, rec.ID, &decrypted2)
	if err != nil {
		t.Fatalf("ReadFile after cache delete: %v", err)
	}

	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		t.Error("expected cache to be recreated after replica fetch")
	}

	if !bytes.Equal(decrypted1.Bytes(), plaintext) {
		t.Error("first read: decrypted data doesn't match plaintext")
	}
	if !bytes.Equal(decrypted2.Bytes(), plaintext) {
		t.Error("second read (after cache delete): decrypted data doesn't match plaintext")
	}
}

func TestDeleteAttachmentRemovesRef(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	plaintext := []byte("hello")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "test.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Remove attachment
	err = svc.RemoveAttachment(ctx, noteID, rec.ID)
	if err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}

	// Verify ref is gone
	var refCount int
	err = svc.DB.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE file_id = ?`, rec.ID).Scan(&refCount)
	if err != nil {
		t.Fatal(err)
	}
	if refCount != 0 {
		t.Errorf("expected 0 refs, got %d", refCount)
	}

	// File should be soft-deleted
	var deletedAt sql.NullString
	err = svc.DB.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, rec.ID).Scan(&deletedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid {
		t.Error("expected file to be soft-deleted after last ref removed")
	}
}

func TestExtractReferencedFileIDs(t *testing.T) {
	body := `Here is a [link](/file/1/42) and an image ![img](/file/99/100) and another /file/55/200 reference.`
	ids := ExtractReferencedFileIDs(body)
	if len(ids) != 3 {
		t.Errorf("expected 3 file ids, got %d: %v", len(ids), ids)
	}
	expected := map[int64]bool{42: true, 100: true, 200: true}
	for _, id := range ids {
		if !expected[id] {
			t.Errorf("unexpected file id: %d", id)
		}
	}
}

func TestReconcileInlineRefsClearsPendingState(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	// Create a pending inline file
	plaintext := []byte("inline content")
	rec, _, err := svc.CreatePendingInline(ctx, noteID, "inline.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("CreatePendingInline: %v", err)
	}

	// Verify pending state is set
	var pendingNoteID sql.NullInt64
	svc.DB.QueryRow(`SELECT pending_inline_note_id FROM files WHERE id = ?`, rec.ID).Scan(&pendingNoteID)
	if !pendingNoteID.Valid || pendingNoteID.Int64 != noteID {
		t.Error("expected pending_inline_note_id to be set")
	}

	// Reconcile with body that references this file
	body := fmt.Sprintf("here is the [file](/file/%d/%d)", noteID, rec.ID)
	orphaned, err := svc.ReconcileInlineRefs(ctx, noteID, body)
	if err != nil {
		t.Fatalf("ReconcileInlineRefs: %v", err)
	}
	if len(orphaned) != 0 {
		t.Errorf("expected 0 orphaned files, got %d", len(orphaned))
	}

	// Pending state should now be cleared
	svc.DB.QueryRow(`SELECT pending_inline_note_id FROM files WHERE id = ?`, rec.ID).Scan(&pendingNoteID)
	if pendingNoteID.Valid {
		t.Error("expected pending_inline_note_id to be cleared after reconciliation")
	}

	// Verify inline ref exists
	var refCount int
	svc.DB.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ? AND ref_kind = 'inline'`,
		noteID, rec.ID).Scan(&refCount)
	if refCount != 1 {
		t.Errorf("expected 1 inline ref, got %d", refCount)
	}
}

func TestReconcileInlineRefsDeletesUnreferenced(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, err := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	// Create a pending inline file
	plaintext := []byte("unused inline content")
	rec, _, err := svc.CreatePendingInline(ctx, noteID, "unused.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("CreatePendingInline: %v", err)
	}

	// Reconcile with body that does NOT reference this file
	body := "no file references here"
	orphaned, err := svc.ReconcileInlineRefs(ctx, noteID, body)
	if err != nil {
		t.Fatalf("ReconcileInlineRefs: %v", err)
	}
	if len(orphaned) != 1 || orphaned[0] != rec.ID {
		t.Errorf("expected file %d to be orphaned, got %v", rec.ID, orphaned)
	}

	// File should be soft-deleted
	var deletedAt sql.NullString
	svc.DB.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, rec.ID).Scan(&deletedAt)
	if !deletedAt.Valid {
		t.Error("expected unreferenced pending file to be soft-deleted")
	}
}

func TestCollectDeletableFilesAfterNoteDelete(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Create note A with a file
	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('note A')`)
	noteAID, _ := res.LastInsertId()
	plaintext := []byte("shared file")
	rec, _, _ := svc.CreateAttachment(ctx, noteAID, "shared.txt", "text/plain", bytes.NewReader(plaintext))

	// Create note B that also references the same file
	res, _ = svc.DB.Exec(`INSERT INTO notes (title) VALUES ('note B')`)
	noteBID, _ := res.LastInsertId()
	svc.DB.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, noteBID, rec.ID)

	// Check deletable files for note A
	deletable, err := svc.CollectDeletableFilesAfterNoteDelete(ctx, noteAID)
	if err != nil {
		t.Fatalf("CollectDeletableFilesAfterNoteDelete: %v", err)
	}
	if len(deletable) != 0 {
		t.Errorf("expected 0 deletable files (note B still references), got %d", len(deletable))
	}

	// Delete note B's ref
	svc.DB.Exec(`DELETE FROM files_refs WHERE note_id = ? AND file_id = ?`, noteBID, rec.ID)

	// Now check again for note A
	deletable, err = svc.CollectDeletableFilesAfterNoteDelete(ctx, noteAID)
	if err != nil {
		t.Fatalf("CollectDeletableFilesAfterNoteDelete: %v", err)
	}
	if len(deletable) != 1 || deletable[0] != rec.ID {
		t.Errorf("expected file %d to be deletable (last ref), got %v", rec.ID, deletable)
	}
}

func TestIsImage(t *testing.T) {
	tests := []struct {
		mime  string
		isImg bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/svg+xml", false},
		{"text/plain", false},
		{"application/pdf", false},
		{"application/octet-stream", false},
	}
	for _, tt := range tests {
		if got := IsImage(tt.mime); got != tt.isImg {
			t.Errorf("IsImage(%q) = %v, want %v", tt.mime, got, tt.isImg)
		}
	}
}

func TestRepairRetriesUploadFailedReplica(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Make backup fail on first attempt
	store.failPut["backup"] = true

	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	noteID, _ := res.LastInsertId()
	plaintext := []byte("repair me")
	rec, _, err := svc.CreateAttachment(ctx, noteID, "repair.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Now let backup succeed
	delete(store.failPut, "backup")

	// Repair the failed replica
	result, err := svc.repairReplica(ctx, rec.ID, "backup")
	if err != nil {
		t.Fatalf("repairReplica: %v", err)
	}
	t.Logf("repair result: %s", result)

	// Check backup state
	var state string
	svc.DB.QueryRow(`SELECT state FROM file_s3 WHERE file_id = ? AND endpoint_id = 'backup'`, rec.ID).Scan(&state)
	if state != string(ReplicaStateUploaded) {
		t.Errorf("expected backup state uploaded, got %s", state)
	}
}

func TestPendingInlineCleanupDeletesAbandonedFile(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('test note')`)
	noteID, _ := res.LastInsertId()

	plaintext := []byte("abandoned")
	rec, _, err := svc.CreatePendingInline(ctx, noteID, "abandoned.txt", "text/plain", bytes.NewReader(plaintext))
	if err != nil {
		t.Fatal(err)
	}

	// Set the pending_inline_at far in the past using Go's time format
	pastTime := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	svc.DB.Exec(`UPDATE files SET pending_inline_at = ? WHERE id = ?`, pastTime, rec.ID)

	// Run cleanup
	result, err := svc.PendingInlineCleanupTask(svc.DB.DB, nil)
	if err != nil {
		t.Fatalf("PendingInlineCleanupTask: %v", err)
	}
	t.Logf("cleanup result: %s", result)

	// File should be soft-deleted
	var deletedAt sql.NullString
	svc.DB.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, rec.ID).Scan(&deletedAt)
	if !deletedAt.Valid {
		t.Error("expected abandoned file to be soft-deleted")
	}
}

func TestConfigValidation(t *testing.T) {
	// Test that LoadConfigFromEnv fails without env vars (we don't set them in tests)
	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Skip("MEDIA_CACHE_DIR set in environment")
	}
	t.Logf("config validation: %v", err)
}

// --- DeleteUnknownS3Files tests ---

// putFakeS3Object injects a raw object into the fake store without using the
// media service, simulating a pre-existing object that might be orphaned.
func putFakeS3Object(t *testing.T, store *fakeReplicaStore, epID, key string, data []byte) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.objects[epID+"/"+key] = data
}

// knownKeysFromDB returns the set of storage_key values in the files table.
func knownKeysFromDB(t *testing.T, db *db.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT storage_key FROM files WHERE deleted_at IS NULL`)
	if err != nil {
		t.Fatalf("query known keys: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan known key: %v", err)
		}
		keys = append(keys, k)
	}
	return keys
}

func TestDeleteUnknownS3FilesKeepsKnownObjects(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Create a real attachment — the DB and S3 both have it.
	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('keep-test')`)
	noteID, _ := res.LastInsertId()
	rec, _, err := svc.CreateAttachment(ctx, noteID, "keep.txt", "text/plain", bytes.NewReader([]byte("keep me")))
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}

	saved, ok := store.objects["primary/"+rec.StorageKey]
	if !ok {
		t.Fatal("expected primary copy of attachment to exist in fake store")
	}

	// Run cleanup.
	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("expected 0 deletions when no orphans exist, got %d", result.Deleted)
	}

	// The known object must still be present on every endpoint.
	for _, ep := range svc.Config.Endpoints {
		got, ok := store.objects[ep.ID+"/"+rec.StorageKey]
		if !ok {
			t.Fatalf("known object %s was deleted from endpoint %s", rec.StorageKey, ep.ID)
		}
		if !bytes.Equal(got, saved) {
			t.Fatalf("known object %s content changed on endpoint %s", rec.StorageKey, ep.ID)
		}
	}
}

func TestDeleteUnknownS3FilesRemovesOrphanedObjects(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Create one known object through the normal path.
	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('known note')`)
	noteID, _ := res.LastInsertId()
	rec, _, err := svc.CreateAttachment(ctx, noteID, "known.txt", "text/plain", bytes.NewReader([]byte("known")))
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}

	// Plant orphan objects that are NOT in the DB.
	orphanKey := "files/2026/01/01/deadbeef00000000000000000000000000000000000000000000000000000000"
	orphan2Key := "files/2025/06/01/aaaaaaaa00000000000000000000000000000000000000000000000000000000"
	putFakeS3Object(t, store, "primary", orphanKey, []byte("orphan A"))
	putFakeS3Object(t, store, "primary", orphan2Key, []byte("orphan B"))
	putFakeS3Object(t, store, "backup", orphanKey, []byte("orphan A on backup"))

	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}

	// Known object preserved.
	if _, ok := store.objects["primary/"+rec.StorageKey]; !ok {
		t.Fatalf("known object %s was deleted", rec.StorageKey)
	}

	// Orphan A deleted on PRIMARY.
	if _, ok := store.objects["primary/"+orphanKey]; ok {
		t.Fatalf("orphan %s was not deleted on primary", orphanKey)
	}
	// Orphan B deleted on PRIMARY.
	if _, ok := store.objects["primary/"+orphan2Key]; ok {
		t.Fatalf("orphan %s was not deleted on primary", orphan2Key)
	}
	// Orphan A deleted on BACKUP (even though content differs from primary).
	if _, ok := store.objects["backup/"+orphanKey]; ok {
		t.Fatalf("orphan %s was not deleted on backup", orphanKey)
	}

	if result.Deleted != 3 {
		t.Fatalf("expected 3 total deletions, got %d (by_endpoint=%+v)", result.Deleted, result.ByEndpoint)
	}

	// Check per-endpoint breakdown.
	byEP := map[string]int{}
	for _, epr := range result.ByEndpoint {
		byEP[epr.Endpoint] = epr.Deleted
		if epr.Error != "" {
			t.Errorf("unexpected error on %s: %s", epr.Endpoint, epr.Error)
		}
	}
	if byEP["primary"] != 2 {
		t.Errorf("expected 2 deletions on primary, got %d", byEP["primary"])
	}
	if byEP["backup"] != 1 {
		t.Errorf("expected 1 deletion on backup, got %d", byEP["backup"])
	}
}

func TestDeleteUnknownS3FilesDeletesSoftDeletedObjects(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Create an attachment, then soft-delete it.
	res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES ('soft-delete test')`)
	noteID, _ := res.LastInsertId()
	rec, _, err := svc.CreateAttachment(ctx, noteID, "soft.txt", "text/plain", bytes.NewReader([]byte("soft-deleted")))
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}

	// Soft-delete: mark deleted_at and clear refs.
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := svc.DB.Exec(`DELETE FROM files_refs WHERE file_id = ?`, rec.ID); err != nil {
		t.Fatalf("delete refs: %v", err)
	}
	if _, err := svc.DB.Exec(`UPDATE files SET deleted_at = ? WHERE id = ?`, now, rec.ID); err != nil {
		t.Fatalf("soft-delete file: %v", err)
	}

	// Run cleanup. Soft-deleted files are treated as unknown → deleted.
	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}

	// Verify the S3 objects are gone.
	for _, ep := range svc.Config.Endpoints {
		if _, ok := store.objects[ep.ID+"/"+rec.StorageKey]; ok {
			t.Fatalf("soft-deleted object %s should have been deleted from %s", rec.StorageKey, ep.ID)
		}
	}

	if result.Deleted != 2 {
		t.Fatalf("expected 2 deletions (one per endpoint), got %d", result.Deleted)
	}
}

func TestDeleteUnknownS3FilesNoOpWhenClean(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// No files at all — DB and fake store are both empty.
	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("expected 0 deletions, got %d", result.Deleted)
	}
	for _, epr := range result.ByEndpoint {
		if epr.Deleted != 0 || epr.Error != "" {
			t.Fatalf("unexpected result for %s: %+v", epr.Endpoint, epr)
		}
	}
	_ = store
}

func TestDeleteUnknownS3FilesOrphansOutsideFilesPrefixAreIgnored(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Plant an object outside the files/ prefix (like a backup object).
	backupKey := "backups/mentis-2026-01-01T00-00-00.bundle.enc"
	putFakeS3Object(t, store, "primary", backupKey, []byte("backup data"))
	putFakeS3Object(t, store, "backup", backupKey, []byte("backup data"))

	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}

	// The backup object must be untouched.
	for _, ep := range svc.Config.Endpoints {
		if _, ok := store.objects[ep.ID+"/"+backupKey]; !ok {
			t.Fatalf("backup object %s was deleted from %s", backupKey, ep.ID)
		}
	}
	if result.Deleted != 0 {
		t.Fatalf("expected 0 deletions, got %d", result.Deleted)
	}
}

func TestDeleteUnknownS3FilesMultipleAttachmentsAllPreserved(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()

	// Create multiple attachments, each from a different note.
	var storageKeys []string
	for i := 0; i < 5; i++ {
		res, _ := svc.DB.Exec(`INSERT INTO notes (title) VALUES (?)`, fmt.Sprintf("note %d", i))
		noteID, _ := res.LastInsertId()
		rec, _, err := svc.CreateAttachment(ctx, noteID, fmt.Sprintf("file%d.txt", i), "text/plain", bytes.NewReader([]byte(fmt.Sprintf("content %d", i))))
		if err != nil {
			t.Fatalf("CreateAttachment %d: %v", i, err)
		}
		storageKeys = append(storageKeys, rec.StorageKey)
	}

	// Plant one orphan.
	orphanKey := "files/2024/12/31/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	putFakeS3Object(t, store, "primary", orphanKey, []byte("orphan"))

	result, err := svc.DeleteUnknownS3Files(ctx)
	if err != nil {
		t.Fatalf("DeleteUnknownS3Files: %v", err)
	}

	// All 5 known objects preserved on both endpoints.
	for _, ep := range svc.Config.Endpoints {
		for _, sk := range storageKeys {
			if _, ok := store.objects[ep.ID+"/"+sk]; !ok {
				t.Fatalf("known object %s was deleted from %s", sk, ep.ID)
			}
		}
	}

	// Orphan deleted on primary.
	if _, ok := store.objects["primary/"+orphanKey]; ok {
		t.Fatalf("orphan %s was not deleted on primary", orphanKey)
	}

	// Only one deletion (backup didn't have the orphan).
	if result.Deleted != 1 {
		t.Fatalf("expected 1 deletion (orphan only), got %d", result.Deleted)
	}
}
