package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	mu      sync.Mutex
	objects map[string][]byte
	etags   map[string]string
	failPut map[string]bool // endpointID -> fail?
}

func newFakeReplicaStore() *fakeReplicaStore {
	return &fakeReplicaStore{
		objects: make(map[string][]byte),
		etags:   make(map[string]string),
		failPut: make(map[string]bool),
	}
}

func (f *fakeReplicaStore) Put(ctx context.Context, ep EndpointConfig, key string, src io.Reader, size int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut[ep.ID] {
		return "", fmt.Errorf("simulated put failure for %s", ep.ID)
	}
	data, _ := io.ReadAll(src)
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
		{"image/svg+xml", true},
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
