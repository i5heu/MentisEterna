package backup

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/i5heu/MentisEterna/internal/media"
)

// Service orchestrates encrypted database backups to S3-compatible storage.
// It uses SQLite's Online Backup API for safe, consistent snapshots of a
// running WAL-mode database, then encrypts with AES-256-GCM and uploads to
// all configured S3 endpoints.
type Service struct {
	DB        *sql.DB
	Store     media.ReplicaStore
	Endpoints []media.EndpointConfig
	Key       []byte // 32-byte AES-256 key
}

// NewService creates a new backup Service.
func NewService(db *sql.DB, store media.ReplicaStore, endpoints []media.EndpointConfig, key []byte) *Service {
	return &Service{
		DB:        db,
		Store:     store,
		Endpoints: endpoints,
		Key:       key,
	}
}

// Run performs a full backup: safe snapshot → encrypt → upload to all S3 endpoints.
// Returns the S3 key of the uploaded backup on success.
func (s *Service) Run(ctx context.Context) (string, error) {
	log.Printf("backup: starting")
	start := time.Now()

	// Step 1: Create a safe, consistent snapshot via SQLite Backup API.
	snapshot, err := s.snapshot()
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	log.Printf("backup: snapshot complete (%d bytes in %v)", len(snapshot), time.Since(start))

	// Step 2: Build a self-contained backup bundle with the DB snapshot plus all
	// active media ciphertext objects referenced by that snapshot.
	bundleStart := time.Now()
	bundle, mediaCount, err := s.buildBundle(ctx, snapshot)
	if err != nil {
		return "", fmt.Errorf("bundle: %w", err)
	}
	log.Printf("backup: bundled %d media object(s) (%d bytes in %v)", mediaCount, len(bundle), time.Since(bundleStart))

	// Step 3: Encrypt with AES-256-GCM.
	encStart := time.Now()
	encrypted, err := Encrypt(bundle, s.Key)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	log.Printf("backup: encrypted (%d bytes in %v)", len(encrypted), time.Since(encStart))

	// Step 4: Upload to each configured S3 endpoint.
	remoteKey := backupObjectKey(time.Now().UTC())
	uploaded := 0
	for _, ep := range s.Endpoints {
		upStart := time.Now()
		etag, err := s.Store.Put(ctx, ep, remoteKey, bytes.NewReader(encrypted), int64(len(encrypted)))
		if err != nil {
			log.Printf("backup: upload to %s failed: %v", ep.ID, err)
			continue
		}
		uploaded++
		log.Printf("backup: uploaded to %s etag=%s key=%s (%v)", ep.ID, etag, remoteKey, time.Since(upStart))
	}

	if uploaded == 0 {
		return "", fmt.Errorf("all %d endpoint uploads failed", len(s.Endpoints))
	}

	log.Printf("backup: complete in %v (key=%s)", time.Since(start), remoteKey)
	return remoteKey, nil
}

// Purge performs retention-based cleanup of old backups across all configured
// S3 endpoints. It lists all backups under the "backups/" prefix, applies the
// retention policy, and deletes backups that should be expired.
//
// Purge processes each endpoint independently — a failure on one endpoint does
// not prevent cleanup on others.
//
// Returns a summary string describing how many backups were deleted on each
// endpoint.
func (s *Service) Purge(ctx context.Context) (string, error) {
	log.Printf("backup/purge: starting retention cleanup")
	start := time.Now()

	policy := DefaultRetentionPolicy()
	now := time.Now().UTC()

	var parts []string
	totalDeleted := 0

	for _, ep := range s.Endpoints {
		keys, err := s.Store.List(ctx, ep, "backups/")
		if err != nil {
			log.Printf("backup/purge: list on %s failed: %v", ep.ID, err)
			parts = append(parts, fmt.Sprintf("%s: list error", ep.ID))
			continue
		}

		if len(keys) == 0 {
			log.Printf("backup/purge: no backups found on %s", ep.ID)
			parts = append(parts, fmt.Sprintf("%s: 0 found, 0 deleted", ep.ID))
			continue
		}

		_, toDelete := ClassifyBackups(keys, now, policy)

		log.Printf("backup/purge: %s has %d total backups, %d to delete",
			ep.ID, len(keys), len(toDelete))

		deleted := 0
		for _, key := range toDelete {
			if err := s.Store.Delete(ctx, ep, key); err != nil {
				log.Printf("backup/purge: delete %s on %s failed: %v", key, ep.ID, err)
				continue
			}
			deleted++
			log.Printf("backup/purge: deleted %s from %s", key, ep.ID)
		}
		totalDeleted += deleted
		parts = append(parts, fmt.Sprintf("%s: %d found, %d deleted", ep.ID, len(keys), deleted))
	}

	log.Printf("backup/purge: complete in %v (%d total deleted)", time.Since(start), totalDeleted)
	return fmt.Sprintf("Retention purge: %s", strings.Join(parts, "; ")), nil
}

// snapshot creates a consistent point-in-time copy of the database using
// SQLite's Online Backup API. It copies to a temporary file, reads it into
// memory, and cleans up. This is safe to run while the database is being
// written to (WAL mode).
func (s *Service) snapshot() ([]byte, error) {
	// Create a temp file for the backup destination.
	tmpFile, err := os.CreateTemp("", "mentis-backup-*.db")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Open the temp file as a plain SQLite database (no WAL, no VSS).
	dstDB, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		return nil, fmt.Errorf("open destination db: %w", err)
	}
	dstDB.SetMaxOpenConns(1)

	// Use a single connection from source and destination.
	srcConn, err := s.DB.Conn(context.Background())
	if err != nil {
		dstDB.Close()
		return nil, fmt.Errorf("acquire source connection: %w", err)
	}
	defer srcConn.Close()

	dstConn, err := dstDB.Conn(context.Background())
	if err != nil {
		dstDB.Close()
		return nil, fmt.Errorf("acquire destination connection: %w", err)
	}
	defer dstConn.Close()

	// Execute the backup using raw SQLite connections.
	err = srcConn.Raw(func(srcRaw interface{}) error {
		srcSQLite := srcRaw.(*sqlite3.SQLiteConn)

		return dstConn.Raw(func(dstRaw interface{}) error {
			dstSQLite := dstRaw.(*sqlite3.SQLiteConn)

			bk, bkErr := dstSQLite.Backup("main", srcSQLite, "main")
			if bkErr != nil {
				return fmt.Errorf("backup init: %w", bkErr)
			}

			// Step(-1) copies all remaining pages in a single call.
			done, stepErr := bk.Step(-1)
			if stepErr != nil {
				bk.Finish()
				return fmt.Errorf("backup step: %w", stepErr)
			}
			if !done {
				bk.Finish()
				return fmt.Errorf("backup: Step(-1) returned not done")
			}

			if finishErr := bk.Finish(); finishErr != nil {
				return fmt.Errorf("backup finish: %w", finishErr)
			}
			return nil
		})
	})

	// Close connections so all data is flushed to the temp file.
	dstConn.Close()
	srcConn.Close()
	dstDB.Close()

	if err != nil {
		return nil, err
	}

	// Read the temp file into memory.
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read snapshot file: %w", err)
	}

	return data, nil
}
