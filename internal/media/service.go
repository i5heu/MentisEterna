package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/i5heu/MentisEterna/internal/db"
)

// Service orchestrates encrypted file storage, S3 replication, and local caching.
type Service struct {
	DB     *db.DB
	Cache  Cache
	Config Config
	Store  ReplicaStore

	// EnqueueFunc is called after commit to schedule background repair/delete jobs.
	// (pluginID, jobName, jsonPayload) -> (runID, error)
	EnqueueFunc func(pluginID, jobName string, payload []byte) (int64, error)
}

// NewService creates a new media Service with defaults.
func NewService(d *db.DB, cfg Config) *Service {
	return &Service{
		DB:     d,
		Cache:  Cache{Root: cfg.CacheDir},
		Config: cfg,
		Store:  NewS3Store(),
	}
}

// CreateAttachment creates a persistent attachment file for a note.
// The file is encrypted once, stored to all configured S3 endpoints,
// and a DB record is created with an 'attachment' ref.
func (s *Service) CreateAttachment(ctx context.Context, noteID int64, filename, mime string, src io.Reader) (FileRecord, []ReplicaResult, error) {
	return s.createFile(ctx, noteID, nil, filename, mime, src, RefKindAttachment)
}

// CreatePendingInline creates a pending-inline file for drag/drop.
// The file's pending_inline_note_id is set so the next save can reconcile it.
func (s *Service) CreatePendingInline(ctx context.Context, noteID int64, filename, mime string, src io.Reader) (FileRecord, []ReplicaResult, error) {
	now := time.Now()
	rec, results, err := s.createFile(ctx, noteID, &now, filename, mime, src, RefKindInline)
	if err != nil {
		return rec, results, err
	}
	// Update with pending_inline fields after create
	_, err = s.DB.Exec(
		`UPDATE files SET pending_inline_note_id = ?, pending_inline_at = ? WHERE id = ?`,
		noteID, now.UTC().Format("2006-01-02T15:04:05.000Z"), rec.ID,
	)
	if err != nil {
		return rec, results, fmt.Errorf("set pending inline: %w", err)
	}
	rec.PendingInlineNoteID = &noteID
	rec.PendingInlineAt = &now
	return rec, results, nil
}

// createFile is the shared upload path: encrypt, insert DB rows, upload to S3 replicas.
func (s *Service) createFile(ctx context.Context, origNoteID int64, pendingAt *time.Time, filename, mime string, src io.Reader, refKind RefKind) (FileRecord, []ReplicaResult, error) {
	// Generate encryption material
	aesKey, err := GenerateFileKey()
	if err != nil {
		return FileRecord{}, nil, err
	}
	baseNonce, err := GenerateBaseNonce()
	if err != nil {
		return FileRecord{}, nil, err
	}

	// Read plaintext into memory
	plaintext, err := io.ReadAll(src)
	if err != nil {
		return FileRecord{}, nil, fmt.Errorf("read upload: %w", err)
	}
	ptSize := int64(len(plaintext))

	// Compute plaintext SHA-256
	ptHash := sha256.Sum256(plaintext)
	ptSHA256 := hex.EncodeToString(ptHash[:])

	// Encrypt to temp file
	tmpFile, err := os.CreateTemp("", "media-enc-*")
	if err != nil {
		return FileRecord{}, nil, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	ctSHA256, _, ctSize, err := EncryptToFile(bytes.NewReader(plaintext), tmpFile, aesKey, baseNonce)
	if err != nil {
		tmpFile.Close()
		return FileRecord{}, nil, fmt.Errorf("encrypt: %w", err)
	}
	tmpFile.Close()

	// Generate storage key
	storageKey := fmt.Sprintf("files/%s/%s", time.Now().UTC().Format("2006/01/02"), ctSHA256)

	// Sniff MIME if not provided
	if mime == "" || mime == "application/octet-stream" {
		mime = sniffMIME(plaintext)
	}

	// Read encrypted file for upload
	ctData, err := os.ReadFile(tmpPath)
	if err != nil {
		return FileRecord{}, nil, fmt.Errorf("read encrypted: %w", err)
	}

	// Begin transaction
	tx, err := s.DB.Begin()
	if err != nil {
		return FileRecord{}, nil, err
	}
	defer tx.Rollback()

	// Insert files row
	res, err := tx.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		origNoteID, storageKey, filename, mime, ptSize,
		ptSHA256, ctSHA256, aesKey, baseNonce,
	)
	if err != nil {
		return FileRecord{}, nil, fmt.Errorf("insert file: %w", err)
	}
	fileID, _ := res.LastInsertId()

	// Insert files_refs row only for attachments (inline refs are created during reconciliation).
	if refKind == RefKindAttachment {
		_, err = tx.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, ?)`,
			origNoteID, fileID, string(refKind))
		if err != nil {
			return FileRecord{}, nil, fmt.Errorf("insert ref: %w", err)
		}
	}

	// Insert file_s3 rows for each endpoint (initially 'uploading')
	for _, ep := range s.Config.Endpoints {
		_, err = tx.Exec(`
			INSERT INTO file_s3 (file_id, endpoint_id, state, remote_key, ciphertext_size)
			VALUES (?, ?, ?, ?, ?)`,
			fileID, ep.ID, string(ReplicaStateUploading), storageKey, ctSize,
		)
		if err != nil {
			return FileRecord{}, nil, fmt.Errorf("insert s3 row for %s: %w", ep.ID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return FileRecord{}, nil, err
	}

	// Upload to all endpoints concurrently (first wave)
	results := s.uploadToReplicas(ctx, fileID, storageKey, ctData, ctSize)

	// Check for client disconnect before proceeding.
	// If the request was aborted, the committed DB row is no longer wanted.
	// Clean up DB + S3 and return an error so no response is sent.
	if ctx.Err() != nil {
		s.cleanupFailedUpload(fileID, storageKey)
		return FileRecord{}, results, fmt.Errorf("upload aborted: %w", ctx.Err())
	}

	// If ALL replicas failed, clean up and return error
	allFailed := true
	for _, r := range results {
		if r.State == ReplicaStateUploaded {
			allFailed = false
			break
		}
	}
	if allFailed {
		// Clean up: soft-delete the file and remote garbage
		s.cleanupFailedUpload(fileID, storageKey)
		return FileRecord{}, results, fmt.Errorf("all %d replica uploads failed", len(results))
	}

	// Enqueue repair jobs for failed replicas (after commit)
	for _, r := range results {
		if r.State == ReplicaStateUploadFailed {
			s.enqueueRepair(fileID, r.EndpointID, storageKey, ctSize)
		}
	}

	// Cache encrypted bytes locally
	if err := s.Cache.Put(fileID, ctSHA256, bytes.NewReader(ctData)); err != nil {
		log.Printf("media: cache put file %d: %v", fileID, err)
	}

	rec := FileRecord{
		ID:               fileID,
		OriginalNoteID:   &origNoteID,
		StorageKey:       storageKey,
		Filename:         filename,
		MimeType:         mime,
		SizeBytes:        ptSize,
		PlaintextSHA256:  ptSHA256,
		CiphertextSHA256: ctSHA256,
		AESKey:           aesKey,
		AESNonce:         baseNonce,
		CreatedAt:        time.Now().UTC(),
	}
	return rec, results, nil
}

// uploadToReplicas uploads encrypted data to all configured endpoints concurrently.
func (s *Service) uploadToReplicas(ctx context.Context, fileID int64, storageKey string, data []byte, size int64) []ReplicaResult {
	var wg sync.WaitGroup
	results := make([]ReplicaResult, len(s.Config.Endpoints))

	for i, ep := range s.Config.Endpoints {
		wg.Add(1)
		go func(idx int, endpoint EndpointConfig) {
			defer wg.Done()
			etag, err := s.Store.Put(ctx, endpoint, storageKey, bytes.NewReader(data), size)
			now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			if err != nil {
				results[idx] = ReplicaResult{
					EndpointID: endpoint.ID,
					State:      ReplicaStateUploadFailed,
					Error:      err.Error(),
				}
				s.DB.Exec(`
					UPDATE file_s3 SET state = ?, last_error = ?, last_attempt_at = ?, retry_count = retry_count + 1,
					                  next_retry_at = ?, updated_at = ?
					WHERE file_id = ? AND endpoint_id = ?`,
					string(ReplicaStateUploadFailed), err.Error(), now,
					time.Now().UTC().Add(1*time.Minute).Format("2006-01-02T15:04:05.000Z"), now,
					fileID, endpoint.ID,
				)
			} else {
				results[idx] = ReplicaResult{
					EndpointID: endpoint.ID,
					State:      ReplicaStateUploaded,
					ETag:       etag,
				}
				s.DB.Exec(`
					UPDATE file_s3 SET state = ?, etag = ?, last_success_at = ?, last_attempt_at = ?,
					                  next_retry_at = NULL, updated_at = ?
					WHERE file_id = ? AND endpoint_id = ?`,
					string(ReplicaStateUploaded), etag, now, now, now,
					fileID, endpoint.ID,
				)
			}
		}(i, ep)
	}
	wg.Wait()
	return results
}

// cleanupFailedUpload removes DB entries, cached data, and S3 objects for a
// completely failed upload. It runs best-effort (errors are logged, not
// returned) because this is already on a failure path.
func (s *Service) cleanupFailedUpload(fileID int64, storageKey string) {
	// Delete all refs for this file (they were committed in the same tx).
	s.DB.Exec(`DELETE FROM files_refs WHERE file_id = ?`, fileID)
	// Soft-delete the file
	s.DB.Exec(`UPDATE files SET deleted_at = ? WHERE id = ?`,
		time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), fileID)
	// Remove cached data
	s.Cache.Delete(fileID, storageKey)
	// Delete from S3 replicas (this was missing — previously orphans could
	// remain if uploads succeeded before the failure was detected).
	for _, ep := range s.Config.Endpoints {
		if err := s.Store.Delete(context.Background(), ep, storageKey); err != nil {
			log.Printf("media: cleanup failed upload %d on %s: %v", fileID, ep.ID, err)
		}
	}
}

// ReadFile reads and decrypts a file, using local cache first then falling back to S3.
func (s *Service) ReadFile(ctx context.Context, fileID int64, w io.Writer) (FileRecord, error) {
	rec, err := s.loadFileRecord(fileID)
	if err != nil {
		return FileRecord{}, err
	}

	// Try local cache first
	ctReader, cacheErr := s.Cache.Open(fileID, rec.CiphertextSHA256)
	if cacheErr == nil {
		defer ctReader.Close()
		if err := DecryptToWriter(ctReader, w, rec.AESKey, rec.AESNonce); err != nil {
			return rec, fmt.Errorf("decrypt cache: %w", err)
		}
		return rec, nil
	}

	// Cache miss: fetch from a healthy replica
	for _, ep := range s.Config.Endpoints {
		body, err := s.Store.Get(ctx, ep, rec.StorageKey)
		if err != nil {
			continue
		}
		defer body.Close()

		// Read encrypted bytes
		ctData, err := io.ReadAll(body)
		if err != nil {
			continue
		}

		// Cache encrypted bytes
		if cacheErr := s.Cache.Put(fileID, rec.CiphertextSHA256, bytes.NewReader(ctData)); cacheErr != nil {
			log.Printf("media: cache put after fetch file %d: %v", fileID, cacheErr)
		}

		// Decrypt
		if err := DecryptToWriter(bytes.NewReader(ctData), w, rec.AESKey, rec.AESNonce); err != nil {
			return rec, fmt.Errorf("decrypt replica: %w", err)
		}
		return rec, nil
	}

	return rec, fmt.Errorf("media: file %d unavailable from any replica", fileID)
}

// RemoveAttachment removes a file ref for a note. If no refs remain, soft-deletes the file.
func (s *Service) RemoveAttachment(ctx context.Context, noteID, fileID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete all refs for this note+file pair (attachment and inline)
	_, err = tx.Exec(`DELETE FROM files_refs WHERE note_id = ? AND file_id = ?`,
		noteID, fileID)
	if err != nil {
		return fmt.Errorf("delete ref: %w", err)
	}

	// Check remaining refs
	var refCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE file_id = ?`, fileID).Scan(&refCount); err != nil {
		return err
	}

	if refCount == 0 {
		// Soft-delete the file
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := tx.Exec(`UPDATE files SET deleted_at = ? WHERE id = ?`, now, fileID); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if refCount == 0 {
		// Enqueue delete jobs for all replicas + cache
		s.enqueueDelete(fileID)
	}

	return nil
}

// ReconcileInlineRefs updates inline refs based on the current markdown body.
// Returns file IDs that are now orphaned (were pending inline but not referenced).
func (s *Service) ReconcileInlineRefs(ctx context.Context, noteID int64, body string) (orphanedFileIDs []int64, err error) {
	referencedIDs := ExtractReferencedFileIDs(body)

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Delete existing inline refs for this note
	if _, err := tx.Exec(`DELETE FROM files_refs WHERE note_id = ? AND ref_kind = ?`, noteID, string(RefKindInline)); err != nil {
		return nil, fmt.Errorf("delete old inline refs: %w", err)
	}

	// Insert current inline refs
	for _, fid := range referencedIDs {
		_, err := tx.Exec(`INSERT OR IGNORE INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, ?)`,
			noteID, fid, string(RefKindInline))
		if err != nil {
			return nil, fmt.Errorf("insert inline ref: %w", err)
		}

		// Clear pending-inline state for files that now have a reference
		_, _ = tx.Exec(`UPDATE files SET pending_inline_note_id = NULL, pending_inline_at = NULL WHERE id = ? AND pending_inline_note_id = ?`,
			fid, noteID)
	}

	// Find pending-inline files for this note that are NOT referenced
	rows, err := tx.Query(`
		SELECT id FROM files
		WHERE pending_inline_note_id = ? AND deleted_at IS NULL AND id NOT IN (
			SELECT file_id FROM files_refs WHERE note_id = ? AND ref_kind = 'inline'
		)`, noteID, noteID)
	if err != nil {
		return nil, fmt.Errorf("query unreferenced pending: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return nil, err
		}
		orphanedFileIDs = append(orphanedFileIDs, fid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Soft-delete orphaned files
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, fid := range orphanedFileIDs {
		if _, err := tx.Exec(`UPDATE files SET deleted_at = ? WHERE id = ?`, now, fid); err != nil {
			return nil, fmt.Errorf("soft delete orphaned file %d: %w", fid, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Enqueue delete jobs for orphaned files
	for _, fid := range orphanedFileIDs {
		s.enqueueDelete(fid)
	}

	return orphanedFileIDs, nil
}

// CollectDeletableFilesAfterNoteDelete returns file IDs that should be deleted
// because they have no remaining refs after the note is deleted.
// This must be called BEFORE the note is deleted so refs still exist.
func (s *Service) CollectDeletableFilesAfterNoteDelete(ctx context.Context, noteID int64) ([]int64, error) {
	// Collect all file IDs referenced by this note
	refRows, err := s.DB.Query(`SELECT file_id FROM files_refs WHERE note_id = ?`, noteID)
	if err != nil {
		return nil, err
	}
	defer refRows.Close()

	var fileIDs []int64
	for refRows.Next() {
		var fid int64
		if err := refRows.Scan(&fid); err != nil {
			return nil, err
		}
		fileIDs = append(fileIDs, fid)
	}
	if err := refRows.Err(); err != nil {
		return nil, err
	}

	// Also collect pending-inline files owned by this note
	pendingRows, err := s.DB.Query(`SELECT id FROM files WHERE pending_inline_note_id = ? AND deleted_at IS NULL`, noteID)
	if err != nil {
		return nil, err
	}
	defer pendingRows.Close()
	for pendingRows.Next() {
		var fid int64
		if err := pendingRows.Scan(&fid); err != nil {
			return nil, err
		}
		fileIDs = append(fileIDs, fid)
	}

	// For each file, check if any OTHER note still references it
	// (this check runs BEFORE the note is deleted)
	var deletable []int64
	for _, fid := range fileIDs {
		var refCount int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE file_id = ?`, fid).Scan(&refCount); err != nil {
			return nil, err
		}
		// If only this note references it, and there are no pending-inline notes keeping it alive
		var pendingOwner *int64
		s.DB.QueryRow(`SELECT pending_inline_note_id FROM files WHERE id = ?`, fid).Scan(&pendingOwner)
		if refCount <= 1 && (pendingOwner == nil || *pendingOwner == noteID) {
			deletable = append(deletable, fid)
		}
	}

	return deletable, nil
}

// SoftDeleteFiles marks files as deleted and enqueues replica delete jobs.
func (s *Service) SoftDeleteFiles(fileIDs []int64) error {
	if len(fileIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, fid := range fileIDs {
		if _, err := s.DB.Exec(`UPDATE files SET deleted_at = ? WHERE id = ?`, now, fid); err != nil {
			return fmt.Errorf("soft delete file %d: %w", fid, err)
		}
		s.enqueueDelete(fid)
	}
	return nil
}

// --- Repair and Delete Tasks ---

// RepairReplicaTask retries uploading a failed replica.
func (s *Service) RepairReplicaTask(d *sql.DB, payload []byte) (string, error) {
	// payload: {"file_id": <id>, "endpoint_id": "<id>"}
	var p struct {
		FileID     int64  `json:"file_id"`
		EndpointID string `json:"endpoint_id"`
	}
	if err := jsonUnmarshal(payload, &p); err != nil {
		return "", err
	}

	return s.repairReplica(context.Background(), p.FileID, p.EndpointID)
}

// DeleteReplicaTask deletes a file from all replicas and cache.
func (s *Service) DeleteReplicaTask(d *sql.DB, payload []byte) (string, error) {
	var p struct {
		FileID int64 `json:"file_id"`
	}
	if err := jsonUnmarshal(payload, &p); err != nil {
		return "", err
	}

	return s.deleteReplicas(context.Background(), p.FileID)
}

// RepairSweepTask finds and repairs all upload_failed replicas due for retry.
func (s *Service) RepairSweepTask(d *sql.DB, payload []byte) (string, error) {
	rows, err := d.Query(`
		SELECT file_id, endpoint_id FROM file_s3
		WHERE state = 'upload_failed' AND (next_retry_at IS NULL OR next_retry_at <= ?)
		ORDER BY next_retry_at ASC LIMIT 10`,
		time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	repaired := 0
	for rows.Next() {
		var fileID int64
		var endpointID string
		if err := rows.Scan(&fileID, &endpointID); err != nil {
			return "", err
		}
		if _, err := s.repairReplica(context.Background(), fileID, endpointID); err != nil {
			log.Printf("media: repair sweep %d/%s: %v", fileID, endpointID, err)
			continue
		}
		repaired++
	}
	return fmt.Sprintf("Repaired %d replicas", repaired), rows.Err()
}

// PendingInlineCleanupTask deletes pending-inline files abandoned for >1 hour.
func (s *Service) PendingInlineCleanupTask(d *sql.DB, payload []byte) (string, error) {
	cutoff := time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	rows, err := d.Query(`
		SELECT f.id FROM files f
		WHERE f.pending_inline_at IS NOT NULL AND f.pending_inline_at <= ? AND f.deleted_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM files_refs fr WHERE fr.file_id = f.id)
	`, cutoff)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var fileIDs []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return "", err
		}
		fileIDs = append(fileIDs, fid)
	}

	if len(fileIDs) == 0 {
		return "No abandoned pending-inline files to clean up", nil
	}

	// Soft-delete
	if err := s.SoftDeleteFiles(fileIDs); err != nil {
		return "", err
	}

	return fmt.Sprintf("Cleaned up %d abandoned pending-inline files", len(fileIDs)), nil
}

// --- Internal Helpers ---

func (s *Service) loadFileRecord(fileID int64) (FileRecord, error) {
	var rec FileRecord
	var origNoteID, pendingNoteID sql.NullInt64
	var pendingAt, deletedAt sql.NullString
	var ptSHA256 sql.NullString

	err := s.DB.QueryRow(`
		SELECT id, original_note_id, pending_inline_note_id, pending_inline_at,
		       storage_key, filename, mime_type, size_bytes,
		       plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce, created_at, deleted_at
		FROM files WHERE id = ?`, fileID,
	).Scan(&rec.ID, &origNoteID, &pendingNoteID, &pendingAt,
		&rec.StorageKey, &rec.Filename, &rec.MimeType, &rec.SizeBytes,
		&ptSHA256, &rec.CiphertextSHA256, &rec.AESKey, &rec.AESNonce, &rec.CreatedAt, &deletedAt)
	if err != nil {
		return rec, fmt.Errorf("load file %d: %w", fileID, err)
	}

	if origNoteID.Valid {
		rec.OriginalNoteID = &origNoteID.Int64
	}
	if pendingNoteID.Valid {
		rec.PendingInlineNoteID = &pendingNoteID.Int64
	}
	if pendingAt.Valid {
		t, _ := time.Parse("2006-01-02T15:04:05.000Z", pendingAt.String)
		rec.PendingInlineAt = &t
	}
	if ptSHA256.Valid {
		rec.PlaintextSHA256 = ptSHA256.String
	}
	if deletedAt.Valid {
		t, _ := time.Parse("2006-01-02T15:04:05.000Z", deletedAt.String)
		rec.DeletedAt = &t
	}
	return rec, nil
}

func (s *Service) repairReplica(ctx context.Context, fileID int64, endpointID string) (string, error) {
	// Find the endpoint config
	var ep *EndpointConfig
	for i := range s.Config.Endpoints {
		if s.Config.Endpoints[i].ID == endpointID {
			ep = &s.Config.Endpoints[i]
			break
		}
	}
	if ep == nil {
		return "", fmt.Errorf("unknown endpoint: %s", endpointID)
	}

	// Load file record to get key and cached data
	rec, err := s.loadFileRecord(fileID)
	if err != nil {
		return "", err
	}
	if rec.DeletedAt != nil {
		return "File deleted, skipping repair", nil
	}

	// Get encrypted data from cache or another replica
	var ctData []byte
	ctReader, cacheErr := s.Cache.Open(fileID, rec.CiphertextSHA256)
	if cacheErr == nil {
		ctData, err = io.ReadAll(ctReader)
		ctReader.Close()
		if err != nil {
			return "", fmt.Errorf("read cache: %w", err)
		}
	} else {
		// Fall back to another healthy replica
		for _, otherEP := range s.Config.Endpoints {
			if otherEP.ID == endpointID {
				continue
			}
			body, getErr := s.Store.Get(ctx, otherEP, rec.StorageKey)
			if getErr != nil {
				continue
			}
			ctData, err = io.ReadAll(body)
			body.Close()
			if err == nil {
				break
			}
		}
	}

	if len(ctData) == 0 {
		return "", fmt.Errorf("no source for repair of file %d", fileID)
	}

	etag, err := s.Store.Put(ctx, *ep, rec.StorageKey, bytes.NewReader(ctData), int64(len(ctData)))
	if err != nil {
		return "", fmt.Errorf("repair upload: %w", err)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	_, dbErr := s.DB.Exec(`
		UPDATE file_s3 SET state = ?, etag = ?, last_success_at = ?, last_attempt_at = ?,
		                  next_retry_at = NULL, retry_count = retry_count + 1, updated_at = ?
		WHERE file_id = ? AND endpoint_id = ?`,
		string(ReplicaStateUploaded), etag, now, now, now, fileID, endpointID,
	)
	if dbErr != nil {
		return "", dbErr
	}

	return fmt.Sprintf("Repaired file %d on %s", fileID, endpointID), nil
}

func (s *Service) deleteReplicas(ctx context.Context, fileID int64) (string, error) {
	rec, err := s.loadFileRecord(fileID)
	if err != nil {
		return "", err
	}

	// Delete from cache
	s.Cache.Delete(fileID, rec.CiphertextSHA256)

	// Delete from all endpoints
	var deleted, failed int
	for _, ep := range s.Config.Endpoints {
		err := s.Store.Delete(ctx, ep, rec.StorageKey)
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		if err != nil {
			failed++
			s.DB.Exec(`
				UPDATE file_s3 SET state = ?, last_error = ?, last_attempt_at = ?, updated_at = ?
				WHERE file_id = ? AND endpoint_id = ?`,
				string(ReplicaStateDeleteFailed), err.Error(), now, now, fileID, ep.ID,
			)
		} else {
			deleted++
			s.DB.Exec(`
				UPDATE file_s3 SET state = ?, last_success_at = ?, last_attempt_at = ?, updated_at = ?
				WHERE file_id = ? AND endpoint_id = ?`,
				string(ReplicaStateDeleted), now, now, now, fileID, ep.ID,
			)
		}
	}

	return fmt.Sprintf("Deleted file %d: %d replicas removed, %d failed", fileID, deleted, failed), nil
}

func (s *Service) enqueueRepair(fileID int64, endpointID, storageKey string, size int64) {
	if s.EnqueueFunc == nil {
		return
	}
	payload := fmt.Sprintf(`{"file_id":%d,"endpoint_id":"%s"}`, fileID, endpointID)
	s.EnqueueFunc("_media", "repair_file_replica", []byte(payload))
}

func (s *Service) enqueueDelete(fileID int64) {
	if s.EnqueueFunc == nil {
		return
	}
	payload := fmt.Sprintf(`{"file_id":%d}`, fileID)
	s.EnqueueFunc("_media", "delete_file_replica", []byte(payload))
}

func sniffMIME(data []byte) string {
	if len(data) == 0 {
		return "application/octet-stream"
	}
	// Simple magic-byte detection
	switch {
	case len(data) >= 4 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
		return "image/png"
	case len(data) >= 6 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
		return "image/gif"
	case len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50:
		return "image/webp"
	case len(data) >= 4 && data[0] == 0x25 && data[1] == 0x50 && data[2] == 0x44 && data[3] == 0x46:
		return "application/pdf"
	case len(data) >= 2 && data[0] == 0x50 && data[1] == 0x4B:
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

// jsonUnmarshal unmarshals JSON data into v.
func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// DeleteUnknownS3FilesResult is returned by DeleteUnknownS3Files.
type DeleteUnknownS3FilesResult struct {
	Deleted    int                    `json:"deleted"`
	Errors     []string               `json:"errors,omitempty"`
	ByEndpoint []EndpointDeleteResult `json:"by_endpoint"`
}

// EndpointDeleteResult reports per-endpoint delete counts.
type EndpointDeleteResult struct {
	Endpoint string `json:"endpoint"`
	Deleted  int    `json:"deleted"`
	Error    string `json:"error,omitempty"`
}

// DeleteUnknownS3Files lists all objects under files/ on each configured
// endpoint, compares them against the active storage_key values in the DB,
// and permanently deletes objects no longer referenced. Deleted objects are
// logged and cannot be recovered.
func (s *Service) DeleteUnknownS3Files(ctx context.Context) (*DeleteUnknownS3FilesResult, error) {
	// Collect known storage keys (active files not soft-deleted).
	rows, err := s.DB.Query(`SELECT storage_key FROM files WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("query known files: %w", err)
	}
	known := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan known file: %w", err)
		}
		known[k] = true
	}
	rows.Close()

	var totalDeleted int
	var allErrors []string
	result := &DeleteUnknownS3FilesResult{}

	for _, ep := range s.Config.Endpoints {
		epResult := EndpointDeleteResult{Endpoint: ep.ID}

		keys, err := s.Store.List(ctx, ep, "files/")
		if err != nil {
			epResult.Error = err.Error()
			result.ByEndpoint = append(result.ByEndpoint, epResult)
			allErrors = append(allErrors, fmt.Sprintf("%s: list error: %v", ep.ID, err))
			continue
		}

		for _, key := range keys {
			if known[key] {
				continue
			}
			if err := s.Store.Delete(ctx, ep, key); err != nil {
				allErrors = append(allErrors, fmt.Sprintf("%s: delete %s: %v", ep.ID, key, err))
				continue
			}
			epResult.Deleted++
			totalDeleted++
			log.Printf("media/cleanup: deleted unknown S3 object %s from %s", key, ep.ID)
		}

		result.ByEndpoint = append(result.ByEndpoint, epResult)
	}

	result.Deleted = totalDeleted
	if len(allErrors) > 0 {
		result.Errors = allErrors
	}

	return result, nil
}
