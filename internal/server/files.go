package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/i5heu/MentisEterna/internal/media"
)

// --- Upload Attachment ---

// uploadAttachment handles POST /notes/:id/files
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	noteID, ok := extractNoteIDFromFilesPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}

	// Verify note exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes WHERE id = ?)`, noteID).Scan(&exists); err != nil || !exists {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	// Lifted write deadline: large file uploads can take minutes (encrypt + S3).
	s.setLongWriteDeadline(w)
	limitUploadBody(w, r, s.cfg.MaxUploadBytes)

	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload exceeds configured size limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "missing file in form field 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := header.Filename
	if filename == "" {
		filename = "untitled"
	}

	rec, results, err := s.mediaService.CreateAttachment(r.Context(), noteID, filename, "", file)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Enqueue OCR for image files
	s.enqueueOCR(rec.ID)
	// Enqueue STT for audio files
	s.enqueueSTT(rec.ID)

	nf := media.NoteFile{
		ID:        rec.ID,
		Filename:  rec.Filename,
		MimeType:  rec.MimeType,
		SizeBytes: rec.SizeBytes,
		URL:       fmt.Sprintf("/file/%d/%d", noteID, rec.ID),
		IsImage:   media.IsImage(rec.MimeType),
		IsAudio:   media.IsAudio(rec.MimeType),
		IsVideo:   media.IsVideo(rec.MimeType),
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"file":    nf,
		"results": results,
	})
	s.notifyNotesChanged("attachment_uploaded", noteID)
}

// --- Upload Inline File ---

// uploadInlineFile handles POST /notes/:id/files/inline
func (s *Server) uploadInlineFile(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	noteID, ok := extractNoteIDFromFilesPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}

	// Verify note exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes WHERE id = ?)`, noteID).Scan(&exists); err != nil || !exists {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	// Lifted write deadline: large file uploads can take minutes (encrypt + S3).
	s.setLongWriteDeadline(w)
	limitUploadBody(w, r, s.cfg.MaxInlineUploadBytes)

	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload exceeds configured size limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "missing file in form field 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := header.Filename
	if filename == "" {
		filename = "untitled"
	}

	rec, results, err := s.mediaService.CreatePendingInline(r.Context(), noteID, filename, "", file)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Enqueue OCR for image files
	s.enqueueOCR(rec.ID)
	// Enqueue STT for audio files
	s.enqueueSTT(rec.ID)

	url := fmt.Sprintf("/file/%d/%d", noteID, rec.ID)

	// Build the markdown insertion string
	var markdown string
	if media.AllowsInline(rec.MimeType) && (media.IsImage(rec.MimeType) || media.IsVideo(rec.MimeType)) {
		markdown = fmt.Sprintf("![%s](%s)", rec.Filename, url)
	} else {
		markdown = fmt.Sprintf("[%s](%s)", rec.Filename, url)
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"file": media.NoteFile{
			ID:        rec.ID,
			Filename:  rec.Filename,
			MimeType:  rec.MimeType,
			SizeBytes: rec.SizeBytes,
			URL:       url,
			IsImage:   media.IsImage(rec.MimeType),
			IsAudio:   media.IsAudio(rec.MimeType),
			IsVideo:   media.IsVideo(rec.MimeType),
		},
		"markdown": markdown,
		"results":  results,
	})
	s.notifyNotesChanged("inline_attachment_uploaded", noteID)
}

// --- Delete Attachment ---

// deleteAttachment handles DELETE /notes/:id/files/:fileID
func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	noteID, fileID, ok := extractNoteAndFileID(r.URL.Path)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if err := s.mediaService.RemoveAttachment(r.Context(), noteID, fileID); err != nil {
		writeErr(w, err)
		return
	}

	s.notifyNotesChanged("attachment_deleted", noteID)
	w.WriteHeader(http.StatusNoContent)
}

// --- Serve File OCR ---

// handleFileOCR handles GET /files/:fileID/ocr
// Returns the OCR result for a file.
func (s *Server) handleFileOCR(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract file ID from /files/:fileID/ocr
	path := strings.TrimPrefix(r.URL.Path, "/files/")
	path = strings.TrimSuffix(path, "/ocr")
	fileID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || fileID <= 0 {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	// Verify file exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM files WHERE id = ? AND deleted_at IS NULL)`, fileID).Scan(&exists); err != nil || !exists {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	result, err := s.mediaService.GetOCRResult(fileID)
	if err != nil {
		http.Error(w, "OCR result not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleTriggerSTT handles POST /files/:fileID/stt
// Triggers a new stt_file job for the given file.
func (s *Server) handleTriggerSTT(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil || s.sttClient == nil {
		http.Error(w, "STT service not configured", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/files/")
	path = strings.TrimSuffix(path, "/stt")
	fileID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || fileID <= 0 {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM files WHERE id = ? AND deleted_at IS NULL)`, fileID).Scan(&exists); err != nil || !exists {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	s.enqueueSTT(fileID)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// --- Serve File ---

// handleFileSTT handles GET /files/:fileID/stt
// Returns the STT result for a file.
func (s *Server) handleFileSTT(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract file ID from /files/:fileID/stt
	path := strings.TrimPrefix(r.URL.Path, "/files/")
	path = strings.TrimSuffix(path, "/stt")
	fileID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || fileID <= 0 {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	// Verify file exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM files WHERE id = ? AND deleted_at IS NULL)`, fileID).Scan(&exists); err != nil || !exists {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	result, err := s.mediaService.GetSTTResult(fileID)
	if err != nil {
		http.Error(w, "STT result not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleTriggerOCR handles POST /files/:fileID/ocr
// Triggers a new ocr_file job for the given file.
func (s *Server) handleTriggerOCR(w http.ResponseWriter, r *http.Request) {
	if s.mediaService == nil || s.ocrClient == nil {
		http.Error(w, "OCR service not configured", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/files/")
	path = strings.TrimSuffix(path, "/ocr")
	fileID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || fileID <= 0 {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM files WHERE id = ? AND deleted_at IS NULL)`, fileID).Scan(&exists); err != nil || !exists {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	s.enqueueOCR(fileID)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// --- Serve File ---

// serveFile handles GET /file/:noteID/:fileID
// noteID is cosmetic; auth + fileID control access.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.mediaService == nil {
		http.Error(w, "media not configured", http.StatusServiceUnavailable)
		return
	}

	fileID, ok := extractFileIDFromPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	// Load file record for content-type detection
	var mimeType, filename string
	var sizeBytes int64
	err := s.db.QueryRow(`SELECT mime_type, filename, size_bytes FROM files WHERE id = ? AND deleted_at IS NULL`, fileID).Scan(&mimeType, &filename, &sizeBytes)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if _, err := s.mediaService.CanReadFile(r.Context(), fileID); err != nil {
		http.Error(w, "file unavailable", http.StatusBadGateway)
		return
	}

	applyFileResponseHeaders(w, mimeType, filename, sizeBytes)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	rec, err := s.mediaService.ReadFile(r.Context(), fileID, w)
	if err != nil {
		log.Printf("media: serve file %d: %v", fileID, err)
		return
	}
	_ = rec // metadata for logging if needed
}

// --- Helpers ---

// extractNoteIDFromFilesPath extracts the note ID from paths like "/notes/123/files" or "/notes/123/files/inline"
func extractNoteIDFromFilesPath(path string) (int64, bool) {
	path = strings.TrimPrefix(path, "/notes/")
	// Remove "/files" suffix
	if idx := strings.Index(path, "/files"); idx > 0 {
		path = path[:idx]
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// extractNoteAndFileID extracts noteID and fileID from "/notes/:noteID/files/:fileID"
func extractNoteAndFileID(path string) (int64, int64, bool) {
	path = strings.TrimPrefix(path, "/notes/")
	idx := strings.LastIndex(path, "/files/")
	if idx < 0 {
		return 0, 0, false
	}
	noteID, err := strconv.ParseInt(path[:idx], 10, 64)
	if err != nil || noteID <= 0 {
		return 0, 0, false
	}
	fileID, err := strconv.ParseInt(path[idx+7:], 10, 64)
	if err != nil || fileID <= 0 {
		return 0, 0, false
	}
	return noteID, fileID, true
}

// setLongWriteDeadline lifts the http.Server.WriteTimeout for the duration of
// this handler. Large file uploads (encrypt + multi-S3 replica) can take
// minutes; without this, the global 10s WriteTimeout closes the connection
// prematurely.
func (s *Server) setLongWriteDeadline(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})
}

// extractFileIDFromPath extracts the file ID from "/file/:noteID/:fileID"
func extractFileIDFromPath(path string) (int64, bool) {
	path = strings.TrimPrefix(path, "/file/")
	// Format: /file/123/456
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
