package backup

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

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/media"
)

type fakeBackupStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeBackupStore() *fakeBackupStore {
	return &fakeBackupStore{objects: make(map[string][]byte)}
}

func (f *fakeBackupStore) Put(_ context.Context, ep media.EndpointConfig, key string, src io.Reader, _ int64) (string, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[ep.ID+"/"+key] = data
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func (f *fakeBackupStore) Get(_ context.Context, ep media.EndpointConfig, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[ep.ID+"/"+key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeBackupStore) Delete(_ context.Context, ep media.EndpointConfig, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, ep.ID+"/"+key)
	return nil
}

func (f *fakeBackupStore) List(_ context.Context, ep media.EndpointConfig, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.objects {
		fullPrefix := ep.ID + "/" + prefix
		if strings.HasPrefix(k, fullPrefix) {
			keys = append(keys, strings.TrimPrefix(k, ep.ID+"/"))
		}
	}
	return keys, nil
}

func (f *fakeBackupStore) ListObjects(_ context.Context, ep media.EndpointConfig, prefix string) ([]media.S3ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var objs []media.S3ObjectInfo
	for k, v := range f.objects {
		fullPrefix := ep.ID + "/" + prefix
		if strings.HasPrefix(k, fullPrefix) {
			objs = append(objs, media.S3ObjectInfo{
				Key:  strings.TrimPrefix(k, ep.ID+"/"),
				Size: int64(len(v)),
			})
		}
	}
	return objs, nil
}

func backupTestEndpoints() []media.EndpointConfig {
	return []media.EndpointConfig{
		{ID: "primary", Bucket: "test", Endpoint: "http://localhost:9000", AccessKeyID: "key", SecretAccessKey: "secret", Region: "us-east-1", ForcePathStyle: true},
		{ID: "secondary", Bucket: "test2", Endpoint: "http://localhost:9001", AccessKeyID: "key2", SecretAccessKey: "secret2", Region: "us-east-1", ForcePathStyle: true},
	}
}

func openBackupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "backup-test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func createAttachmentForBackup(t *testing.T, d *db.DB, store media.ReplicaStore, endpoints []media.EndpointConfig) media.FileRecord {
	t.Helper()

	svc := media.NewService(d, media.Config{
		CacheDir:  filepath.Join(t.TempDir(), "cache"),
		Endpoints: endpoints,
	})
	svc.Store = store

	res, err := d.Exec(`INSERT INTO notes (title) VALUES ('backup test note')`)
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	noteID, _ := res.LastInsertId()

	rec, _, err := svc.CreateAttachment(context.Background(), noteID, "hello.txt", "text/plain", bytes.NewReader([]byte("hello backup media")))
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	return rec
}

func TestRunBundlesDatabaseAndMedia(t *testing.T) {
	ctx := context.Background()
	d := openBackupTestDB(t)
	store := newFakeBackupStore()
	endpoints := backupTestEndpoints()
	rec := createAttachmentForBackup(t, d, store, endpoints)
	key := bytes.Repeat([]byte{7}, 32)

	svc := NewService(d.DB, store, endpoints, key)
	remoteKey, err := svc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasSuffix(remoteKey, bundleBackupSuffix) {
		t.Fatalf("expected bundle backup suffix, got %s", remoteKey)
	}

	encrypted, ok := store.objects["primary/"+remoteKey]
	if !ok {
		t.Fatalf("expected uploaded backup object %s", remoteKey)
	}
	plaintext, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	restorePath := filepath.Join(t.TempDir(), "restored.db")
	restoreStore := newFakeBackupStore()
	result, err := RestorePayload(ctx, plaintext, restorePath, restoreStore, endpoints)
	if err != nil {
		t.Fatalf("RestorePayload: %v", err)
	}
	if result.Format != "bundle-v1" {
		t.Fatalf("expected bundle-v1 restore format, got %s", result.Format)
	}
	if result.MediaFiles != 1 {
		t.Fatalf("expected 1 media file restored, got %d", result.MediaFiles)
	}
	if result.MediaCopies != len(endpoints) {
		t.Fatalf("expected %d media uploads, got %d", len(endpoints), result.MediaCopies)
	}

	originalCiphertext := store.objects["primary/"+rec.StorageKey]
	for _, ep := range endpoints {
		restoredCiphertext, ok := restoreStore.objects[ep.ID+"/"+rec.StorageKey]
		if !ok {
			t.Fatalf("missing restored media for %s on endpoint %s", rec.StorageKey, ep.ID)
		}
		if !bytes.Equal(restoredCiphertext, originalCiphertext) {
			t.Fatalf("restored media mismatch on endpoint %s", ep.ID)
		}
	}

	restoredDB, err := sql.Open("sqlite3", restorePath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restoredDB.Close()

	var fileCount int
	if err := restoredDB.QueryRow(`SELECT COUNT(*) FROM files WHERE storage_key = ? AND deleted_at IS NULL`, rec.StorageKey).Scan(&fileCount); err != nil {
		t.Fatalf("query restored files: %v", err)
	}
	if fileCount != 1 {
		t.Fatalf("expected restored file row, got %d", fileCount)
	}

	var replicaCount int
	if err := restoredDB.QueryRow(`SELECT COUNT(*) FROM file_s3 WHERE file_id = ? AND state = 'uploaded'`, rec.ID).Scan(&replicaCount); err != nil {
		t.Fatalf("query restored file_s3: %v", err)
	}
	if replicaCount != len(endpoints) {
		t.Fatalf("expected %d restored replica rows, got %d", len(endpoints), replicaCount)
	}
}

func TestRunFailsWhenActiveMediaIsMissing(t *testing.T) {
	ctx := context.Background()
	d := openBackupTestDB(t)
	store := newFakeBackupStore()
	endpoints := backupTestEndpoints()
	rec := createAttachmentForBackup(t, d, store, endpoints)
	key := bytes.Repeat([]byte{9}, 32)

	for _, ep := range endpoints {
		delete(store.objects, ep.ID+"/"+rec.StorageKey)
	}

	svc := NewService(d.DB, store, endpoints, key)
	_, err := svc.Run(ctx)
	if err == nil {
		t.Fatal("expected backup to fail when active media is missing")
	}
	if !strings.Contains(err.Error(), rec.StorageKey) {
		t.Fatalf("expected missing storage key in error, got %v", err)
	}
}

func TestRestorePayloadSupportsLegacyDatabaseBackups(t *testing.T) {
	payload := []byte("legacy sqlite bytes")
	outputPath := filepath.Join(t.TempDir(), "legacy.db")
	store := newFakeBackupStore()
	endpoints := backupTestEndpoints()

	result, err := RestorePayload(context.Background(), payload, outputPath, store, endpoints)
	if err != nil {
		t.Fatalf("RestorePayload: %v", err)
	}
	if result.Format != "legacy-db" {
		t.Fatalf("expected legacy-db format, got %s", result.Format)
	}
	if result.MediaFiles != 0 || result.MediaCopies != 0 {
		t.Fatalf("expected no media restore for legacy backup, got %+v", result)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read legacy restore output: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("legacy restore output mismatch")
	}
	if len(store.objects) != 0 {
		t.Fatalf("expected no media uploads for legacy restore, got %d", len(store.objects))
	}
}
