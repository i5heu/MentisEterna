package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/i5heu/MentisEterna/internal/media"
)

// Chunked upload temporary directory under os.TempDir().
const chunkTempDirName = "mentis-chunked"

// uploadSessionRow mirrors a row in the upload_sessions table.
type uploadSessionRow struct {
	UploadID     string
	NoteID       int64
	Filename     string
	MimeType     string
	TotalSize    int64
	ChunkSize    int64
	TotalChunks  int
	FileSHA256   *string
	Inline       bool
	ChunksDone   string // JSON array of ints
	Status       string
	FinishResult *string // JSON result blob when status=done
	CreatedAt    string
	ExpiresAt    string
}

// chunksDir returns the temp directory for a given upload session.
func chunksDir(uploadID string) string {
	return filepath.Join(os.TempDir(), chunkTempDirName, uploadID)
}

// --- Route dispatcher ---

// handleChunkedRoute dispatches chunked upload requests based on the URL path.
// Called from the /notes/ dispatcher when the path contains "/chunked/".
func (s *Server) handleChunkedRoute(w http.ResponseWriter, r *http.Request) {
	// Extract note ID from path like "/notes/{noteID}/chunked/..."
	path := strings.TrimPrefix(r.URL.Path, "/notes/")
	idx := strings.Index(path, "/chunked/")
	if idx < 0 {
		http.Error(w, "invalid chunked upload path", http.StatusBadRequest)
		return
	}
	noteID, err := strconv.ParseInt(path[:idx], 10, 64)
	if err != nil || noteID <= 0 {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}

	// Remaining path after "/notes/{noteID}/chunked"
	path = strings.TrimPrefix(path[idx+len("/chunked"):], "/")

	if path == "start" {
		s.handleChunkedStart(w, r, noteID)
		return
	}

	if path == "pending" {
		s.handleChunkedPending(w, r, noteID)
		return
	}

	// Parse "/{uploadID}" or "/{uploadID}/chunk" or "/{uploadID}/finish" or "/{uploadID}/cancel"
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, "missing upload id", http.StatusBadRequest)
		return
	}
	uploadID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		// GET /notes/{id}/chunked/{uploadID} — get session state
		s.handleChunkedStatus(w, r, noteID, uploadID)
	case "chunk":
		s.handleChunkedChunk(w, r, noteID, uploadID)
	case "finish":
		s.handleChunkedFinish(w, r, noteID, uploadID)
	case "cancel":
		s.handleChunkedCancel(w, r, noteID, uploadID)
	default:
		http.Error(w, "unknown chunked action", http.StatusNotFound)
	}
}

// --- Handlers ---

// handleChunkedStart starts a new chunked upload session.
// POST /notes/{id}/chunked/start
func (s *Server) handleChunkedStart(w http.ResponseWriter, r *http.Request, noteID int64) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify note exists.
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes WHERE id = ?)`, noteID).Scan(&exists); err != nil || !exists {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	var req struct {
		Filename    string `json:"filename"`
		MimeType    string `json:"mime_type"`
		TotalSize   int64  `json:"total_size"`
		ChunkSize   int64  `json:"chunk_size"`
		TotalChunks int    `json:"total_chunks"`
		FileSHA256  string `json:"file_sha256,omitempty"`
		Inline      bool   `json:"inline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Filename == "" {
		req.Filename = "untitled"
	}
	if req.TotalSize <= 0 || req.ChunkSize <= 0 || req.TotalChunks <= 0 {
		http.Error(w, "total_size, chunk_size, and total_chunks must be positive", http.StatusBadRequest)
		return
	}
	// Validate total size against configured limits.
	maxBytes := s.cfg.MaxUploadBytes
	if req.Inline {
		maxBytes = s.cfg.MaxInlineUploadBytes
	}
	if req.TotalSize > maxBytes {
		http.Error(w, "total file size exceeds configured limit", http.StatusRequestEntityTooLarge)
		return
	}

	var fileSHA256 *string
	if req.FileSHA256 != "" {
		fileSHA256 = &req.FileSHA256
	}

	// Idempotency: if a non-expired, non-terminal session already exists for
	// this note with the same file_sha256, return the existing session so the
	// frontend can resume after a tab close or browser crash.
	if fileSHA256 != nil && *fileSHA256 != "" {
		existing := s.loadUploadSessionByHash(noteID, *fileSHA256)
		if existing != nil {
			var chunksDone []int
			_ = json.Unmarshal([]byte(existing.ChunksDone), &chunksDone)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"upload_id":   existing.UploadID,
				"chunks_done": chunksDone,
			})
			return
		}
	}

	uploadID := uuid.New().String()
	expiresAt := time.Now().UTC().Add(12 * time.Hour)
	inlineVal := 0
	if req.Inline {
		inlineVal = 1
	}

	_, err := s.db.Exec(
		`INSERT INTO upload_sessions (upload_id, note_id, filename, mime_type, total_size, chunk_size, total_chunks, file_sha256, inline, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uploadID, noteID, req.Filename, req.MimeType, req.TotalSize, req.ChunkSize, req.TotalChunks,
		fileSHA256, inlineVal, expiresAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"upload_id":   uploadID,
		"chunks_done": []int{},
	})
}

// handleChunkedChunk uploads a single chunk.
// POST /notes/{id}/chunked/{uploadID}/chunk
func (s *Server) handleChunkedChunk(w http.ResponseWriter, r *http.Request, noteID int64, uploadID string) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	row, err := s.loadUploadSession(uploadID, noteID)
	if err != nil {
		http.Error(w, "upload session not found", http.StatusNotFound)
		return
	}

	if t := parseTime(row.ExpiresAt); t.IsZero() || time.Now().UTC().After(t) {
		http.Error(w, "upload session expired", http.StatusGone)
		return
	}

	// Parse chunk index from the query string or form value — this does not
	// require reading the multipart body, so we can short-circuit early for
	// chunks that are already done.
	indexStr := r.FormValue("index")
	chunkIndex, err := strconv.Atoi(indexStr)
	if err != nil || chunkIndex < 0 || chunkIndex >= row.TotalChunks {
		http.Error(w, "invalid or out-of-range chunk index", http.StatusBadRequest)
		return
	}

	// Skip re-upload if this chunk is already recorded.
	var chunksDone []int
	_ = json.Unmarshal([]byte(row.ChunksDone), &chunksDone)
	for _, idx := range chunksDone {
		if idx == chunkIndex {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"index":    chunkIndex,
				"received": true,
				"_meta":    map[string]interface{}{"chunks_done": len(chunksDone), "total_chunks": row.TotalChunks},
			})
			return
		}
	}

	// Read multipart form.
	// The chunk body is limited to chunk_size + some overhead.
	s.setLongWriteDeadline(w)
	limitUploadBody(w, r, row.ChunkSize+1<<20) // allow ~1MB overhead for multipart framing

	chunkReader, _, err := r.FormFile("chunk")
	if err != nil {
		http.Error(w, "missing chunk in form field 'chunk'", http.StatusBadRequest)
		return
	}
	defer chunkReader.Close()

	expectedSHA256 := r.FormValue("sha256")

	// Read chunk into memory.
	chunkBytes, err := io.ReadAll(io.LimitReader(chunkReader, row.ChunkSize+1))
	if err != nil {
		writeErr(w, err)
		return
	}
	if int64(len(chunkBytes)) > row.ChunkSize {
		http.Error(w, "chunk exceeds declared chunk_size", http.StatusRequestEntityTooLarge)
		return
	}

	// Verify SHA-256.
	if expectedSHA256 != "" {
		actual := sha256Hex(chunkBytes)
		if !strings.EqualFold(actual, expectedSHA256) {
			http.Error(w, fmt.Sprintf("chunk sha256 mismatch: got %s, expected %s", actual, expectedSHA256), http.StatusBadRequest)
			return
		}
	}

	// Save chunk to disk.
	dir := chunksDir(uploadID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		writeErr(w, fmt.Errorf("create chunk dir: %w", err))
		return
	}
	chunkPath := filepath.Join(dir, fmt.Sprintf("%d.chunk", chunkIndex))
	if err := os.WriteFile(chunkPath, chunkBytes, 0600); err != nil {
		writeErr(w, fmt.Errorf("write chunk: %w", err))
		return
	}

	// Update chunks_done in DB.
	done, err := s.addChunkDone(uploadID, chunkIndex, row.TotalChunks)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"index":    chunkIndex,
		"received": true,
		"_meta":    map[string]interface{}{"chunks_done": len(done), "total_chunks": row.TotalChunks},
	})
}

// handleChunkedStatus returns the current state of a chunked upload session.
// GET /notes/{id}/chunked/{uploadID}
func (s *Server) handleChunkedStatus(w http.ResponseWriter, r *http.Request, noteID int64, uploadID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	row, err := s.loadUploadSession(uploadID, noteID)
	if err != nil {
		// Session may have been cleaned up concurrently (finish handler runs
		// cleanup after writing the response). Return 200 with a terminal
		// status so the frontend poller doesn't log 404 errors.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "not_found",
		})
		return
	}

	var chunksDone []int
	_ = json.Unmarshal([]byte(row.ChunksDone), &chunksDone)

	resp := map[string]interface{}{
		"upload_id":    row.UploadID,
		"filename":     row.Filename,
		"total_chunks": row.TotalChunks,
		"total_size":   row.TotalSize,
		"chunk_size":   row.ChunkSize,
		"chunks_done":  chunksDone,
		"inline":       row.Inline,
		"mime_type":    row.MimeType,
		"status":       row.Status,
	}
	// Include finish result when the upload is done, so the worker
	// can get the file record even if finish returned 409 (reload scenario).
	if row.FinishResult != nil && *row.FinishResult != "" {
		var result json.RawMessage
		if json.Unmarshal([]byte(*row.FinishResult), &result) == nil {
			resp["result"] = result
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleChunkedFinish assembles all chunks, verifies the whole-file SHA-256 if
// provided, and finalizes the upload via the existing media service.
// POST /notes/{id}/chunked/{uploadID}/finish
//
// Encryption and S3 upload use context.Background() so they survive HTTP
// request cancellation (page reload, tab close). The assembled-and-verified
// file is guaranteed to land in the media store regardless of client state.
//
// This endpoint is guarded against re-entry: if the session is already being
// finalized (processing) or completed (done), subsequent calls are rejected
// to prevent duplicate file rows.
func (s *Server) handleChunkedFinish(w http.ResponseWriter, r *http.Request, noteID int64, uploadID string) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	row, err := s.loadUploadSession(uploadID, noteID)
	if err != nil {
		http.Error(w, "upload session not found", http.StatusNotFound)
		return
	}

	if t := parseTime(row.ExpiresAt); t.IsZero() || time.Now().UTC().After(t) {
		http.Error(w, "upload session expired", http.StatusGone)
		return
	}

	// Guard: reject re-entry while a finish is already in progress or completed.
	// Without this, a browser reload during the finish phase would cause the
	// new frontend to call finish again, creating duplicate file rows.
	if row.Status == "processing" || row.Status == "assembling" || row.Status == "verifying" {
		http.Error(w, "upload already being finalized", http.StatusConflict)
		return
	}
	if row.Status == "done" {
		// Upload already completed. Return success without creating a new file.
		// The session row was already cleaned up by the first finish call, so
		// we shouldn't reach here — but guard defensively.
		http.Error(w, "upload already completed", http.StatusConflict)
		return
	}
	if row.Status != "" && row.Status != "uploading" {
		http.Error(w, "upload session is in terminal state: "+row.Status, http.StatusConflict)
		return
	}

	// Check all chunks are present.
	var chunksDone []int
	_ = json.Unmarshal([]byte(row.ChunksDone), &chunksDone)
	if len(chunksDone) != row.TotalChunks {
		s.updateUploadStatus(uploadID, "failed")
		http.Error(w, fmt.Sprintf("not all chunks received: %d/%d", len(chunksDone), row.TotalChunks), http.StatusConflict)
		return
	}

	s.updateUploadStatus(uploadID, "assembling")

	// Assemble file from chunks.
	dir := chunksDir(uploadID)
	assembledPath := filepath.Join(dir, "assembled")
	out, err := os.Create(assembledPath)
	if err != nil {
		s.updateUploadStatus(uploadID, "failed")
		writeErr(w, fmt.Errorf("create assembled file: %w", err))
		return
	}
	hasher := sha256.New()
	multiWriter := io.MultiWriter(out, hasher)

	for i := 0; i < row.TotalChunks; i++ {
		chunkPath := filepath.Join(dir, fmt.Sprintf("%d.chunk", i))
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			out.Close()
			s.updateUploadStatus(uploadID, "failed")
			http.Error(w, fmt.Sprintf("missing chunk %d", i), http.StatusConflict)
			return
		}
		if _, err := multiWriter.Write(chunkData); err != nil {
			out.Close()
			s.updateUploadStatus(uploadID, "failed")
			writeErr(w, fmt.Errorf("assemble chunk %d: %w", i, err))
			return
		}
	}
	out.Close()

	s.updateUploadStatus(uploadID, "verifying")

	// Verify whole-file SHA-256 if provided.
	if row.FileSHA256 != nil && *row.FileSHA256 != "" {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, *row.FileSHA256) {
			s.updateUploadStatus(uploadID, "failed")
			http.Error(w, fmt.Sprintf("file sha256 mismatch: got %s, expected %s", actual, *row.FileSHA256), http.StatusBadRequest)
			return
		}
	}

	s.updateUploadStatus(uploadID, "processing")

	// Pass assembled file to the existing media service.
	f, err := os.Open(assembledPath)
	if err != nil {
		s.updateUploadStatus(uploadID, "failed")
		writeErr(w, fmt.Errorf("open assembled file: %w", err))
		return
	}
	defer f.Close()

	s.setLongWriteDeadline(w)

	// Use context.Background() so encryption + S3 upload survives HTTP
	// request cancellation (page reload, tab close, etc.).
	bgCtx := context.Background()

	var rec media.FileRecord
	var results []media.ReplicaResult
	if row.Inline {
		rec, results, err = s.mediaService.CreatePendingInline(bgCtx, noteID, row.Filename, row.MimeType, f)
	} else {
		rec, results, err = s.mediaService.CreateAttachment(bgCtx, noteID, row.Filename, row.MimeType, f)
	}
	if err != nil {
		s.updateUploadStatus(uploadID, "failed")
		writeErr(w, err)
		return
	}

	// Enqueue OCR/STT.
	s.enqueueOCR(rec.ID)
	s.enqueueSTT(rec.ID)

	url := fmt.Sprintf("/file/%d/%d", noteID, rec.ID)
	nf := media.NoteFile{
		ID:        rec.ID,
		Filename:  rec.Filename,
		MimeType:  rec.MimeType,
		SizeBytes: rec.SizeBytes,
		URL:       url,
		IsImage:   media.IsImage(rec.MimeType),
		IsAudio:   media.IsAudio(rec.MimeType),
		IsVideo:   media.IsVideo(rec.MimeType),
	}

	resp := map[string]interface{}{
		"file":    nf,
		"results": results,
	}
	if row.Inline {
		var markdown string
		// Use image syntax (![](url)) for any media type that renders inline:
		// images, videos, and audio.
		if media.AllowsInline(rec.MimeType) && (media.IsImage(rec.MimeType) || media.IsVideo(rec.MimeType) || media.IsAudio(rec.MimeType)) {
			markdown = fmt.Sprintf("![%s](%s)", rec.Filename, url)
		} else {
			markdown = fmt.Sprintf("[%s](%s)", rec.Filename, url)
		}
		resp["markdown"] = markdown
	}

	reason := "attachment_uploaded"
	if row.Inline {
		reason = "inline_attachment_uploaded"
		// For inline uploads, the frontend's onComplete callback already
		// handles inserting the markdown and updating attachments. Skip
		// notifyNotesChanged to avoid triggering a live refresh that would
		// show the "newer version available" banner while the user edits.
	} else {
		s.notifyNotesChanged(reason, noteID)
	}
	writeJSON(w, http.StatusCreated, resp)

	// Store the result on the session row so the status poller can retrieve
	// it even if the original HTTP response was lost (browser reload).
	respBytes, _ := json.Marshal(resp)
	s.db.Exec(`UPDATE upload_sessions SET status = 'done', finish_result = ? WHERE upload_id = ?`, string(respBytes), uploadID)

	// Clean up chunk files after responding. The session row stays in the DB
	// with status=done+finish_result. Expired sessions are cleaned up
	// periodically by CleanupExpiredUploadSessions.
	dir = chunksDir(uploadID)
	_ = os.RemoveAll(dir)
}

// handleChunkedCancel cancels an upload session and cleans up its chunks.
// POST /notes/{id}/chunked/{uploadID}/cancel
func (s *Server) handleChunkedCancel(w http.ResponseWriter, r *http.Request, noteID int64, uploadID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := s.loadUploadSession(uploadID, noteID)
	if err != nil {
		http.Error(w, "upload session not found", http.StatusNotFound)
		return
	}

	s.cleanupUploadSession(uploadID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleChunkedPending returns all unfinished (non-terminal) upload sessions for
// a note so the frontend can resume uploads after tab close or browser crash.
// GET /notes/{id}/chunked/pending
func (s *Server) handleChunkedPending(w http.ResponseWriter, r *http.Request, noteID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.db.Query(
		`SELECT upload_id, note_id, filename, mime_type, total_size, chunk_size, total_chunks, file_sha256, inline, chunks_done, status, finish_result, created_at, expires_at
		 FROM upload_sessions
		 WHERE note_id = ? AND expires_at > ?
		   AND status NOT IN ('done', 'failed', 'finished')
		 ORDER BY created_at ASC`,
		noteID, now,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	type pendingSession struct {
		UploadID    string `json:"upload_id"`
		Filename    string `json:"filename"`
		MimeType    string `json:"mime_type"`
		TotalSize   int64  `json:"total_size"`
		ChunkSize   int64  `json:"chunk_size"`
		TotalChunks int    `json:"total_chunks"`
		FileSHA256  string `json:"file_sha256,omitempty"`
		Inline      bool   `json:"inline"`
		ChunksDone  []int  `json:"chunks_done"`
		Status      string `json:"status"`
	}

	var pending []pendingSession
	for rows.Next() {
		var r uploadSessionRow
		var inlineVal int
		var fileSHA256 sql.NullString
		var finishResult sql.NullString
		if err := rows.Scan(&r.UploadID, &r.NoteID, &r.Filename, &r.MimeType,
			&r.TotalSize, &r.ChunkSize, &r.TotalChunks, &fileSHA256, &inlineVal,
			&r.ChunksDone, &r.Status, &finishResult, &r.CreatedAt, &r.ExpiresAt); err != nil {
			continue
		}
		var chunksDone []int
		_ = json.Unmarshal([]byte(r.ChunksDone), &chunksDone)
		ps := pendingSession{
			UploadID:    r.UploadID,
			Filename:    r.Filename,
			MimeType:    r.MimeType,
			TotalSize:   r.TotalSize,
			ChunkSize:   r.ChunkSize,
			TotalChunks: r.TotalChunks,
			Inline:      inlineVal != 0,
			ChunksDone:  chunksDone,
			Status:      r.Status,
		}
		if fileSHA256.Valid {
			ps.FileSHA256 = fileSHA256.String
		}
		pending = append(pending, ps)
	}
	if pending == nil {
		pending = []pendingSession{}
	}
	writeJSON(w, http.StatusOK, pending)
}

// --- Helpers ---

// loadUploadSession fetches a session row, verifying it belongs to the given note.
func (s *Server) loadUploadSession(uploadID string, noteID int64) (*uploadSessionRow, error) {
	return scanUploadSession(s.db.QueryRow(
		`SELECT upload_id, note_id, filename, mime_type, total_size, chunk_size, total_chunks, file_sha256, inline, chunks_done, status, finish_result, created_at, expires_at
		 FROM upload_sessions WHERE upload_id = ? AND note_id = ?`,
		uploadID, noteID,
	))
}

// loadUploadSessionByHash finds a non-expired, non-terminal session for a note
// matching the given file_sha256. Returns nil if none found.
func (s *Server) loadUploadSessionByHash(noteID int64, fileSHA256 string) *uploadSessionRow {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	row, err := scanUploadSession(s.db.QueryRow(
		`SELECT upload_id, note_id, filename, mime_type, total_size, chunk_size, total_chunks, file_sha256, inline, chunks_done, status, finish_result, created_at, expires_at
		 FROM upload_sessions
		 WHERE note_id = ? AND file_sha256 = ? AND expires_at > ?
		   AND status NOT IN ('done', 'failed', 'finished')
		 ORDER BY created_at DESC LIMIT 1`,
		noteID, fileSHA256, now,
	))
	if err != nil {
		return nil
	}
	return row
}

// scanUploadSession scans an uploadSessionRow from a *sql.Row.
func scanUploadSession(row *sql.Row) (*uploadSessionRow, error) {
	r := &uploadSessionRow{}
	var inlineVal int
	var fileSHA256 sql.NullString
	var finishResult sql.NullString
	err := row.Scan(&r.UploadID, &r.NoteID, &r.Filename, &r.MimeType, &r.TotalSize,
		&r.ChunkSize, &r.TotalChunks, &fileSHA256, &inlineVal,
		&r.ChunksDone, &r.Status, &finishResult, &r.CreatedAt, &r.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	if fileSHA256.Valid {
		r.FileSHA256 = &fileSHA256.String
	}
	if finishResult.Valid {
		r.FinishResult = &finishResult.String
	}
	r.Inline = inlineVal != 0
	return r, nil
}

// addChunkDone appends a chunk index to the session's chunks_done JSON array.
// It de-duplicates (idempotent for re-uploaded chunks).
func (s *Server) addChunkDone(uploadID string, chunkIndex int, totalChunks int) ([]int, error) {
	var current string
	err := s.db.QueryRow(`SELECT chunks_done FROM upload_sessions WHERE upload_id = ?`, uploadID).Scan(&current)
	if err != nil {
		return nil, err
	}

	var done []int
	if err := json.Unmarshal([]byte(current), &done); err != nil {
		done = []int{}
	}

	// Deduplicate.
	found := false
	for _, idx := range done {
		if idx == chunkIndex {
			found = true
			break
		}
	}
	if !found {
		done = append(done, chunkIndex)
	}

	newJSON, err := json.Marshal(done)
	if err != nil {
		return nil, err
	}

	_, err = s.db.Exec(`UPDATE upload_sessions SET chunks_done = ? WHERE upload_id = ?`, string(newJSON), uploadID)
	return done, err
}

// updateUploadStatus updates the status column of an upload session.
func (s *Server) updateUploadStatus(uploadID, status string) {
	if _, err := s.db.Exec(`UPDATE upload_sessions SET status = ? WHERE upload_id = ?`, status, uploadID); err != nil {
		log.Printf("updateUploadStatus(%q, %q): %v", uploadID, status, err)
	}
}

// cleanupUploadSession removes the upload session row and all its chunk files.
func (s *Server) cleanupUploadSession(uploadID string) {
	dir := chunksDir(uploadID)
	_ = os.RemoveAll(dir)
	_, _ = s.db.Exec(`DELETE FROM upload_sessions WHERE upload_id = ?`, uploadID)
}

// parseTime parses an SQLite datetime string in the format used by the app.
// The app always stores timestamps as UTC with 3-digit millisecond precision
// using Go's time.RFC3339Nano-like format "2006-01-02T15:04:05.000Z".
// If parsing fails, it logs the error and returns the zero time — callers should
// check for zero time and treat as expired.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		// Try without fractional seconds as a fallback (e.g. from SQLite defaults).
		t, err = time.Parse("2006-01-02T15:04:05Z", s)
		if err != nil {
			log.Printf("chunked upload: parse time %q: %v", s, err)
			return time.Time{}
		}
	}
	return t
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// CleanupExpiredUploadSessions removes expired upload sessions and their chunks.
// Call this at startup or periodically.
func (s *Server) CleanupExpiredUploadSessions() {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.db.Query(`SELECT upload_id FROM upload_sessions WHERE expires_at < ?`, now)
	if err != nil {
		log.Printf("chunked upload: cleanup query: %v", err)
		return
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		s.cleanupUploadSession(id)
	}
	if len(ids) > 0 {
		log.Printf("chunked upload: cleaned up %d expired session(s)", len(ids))
	}
}
