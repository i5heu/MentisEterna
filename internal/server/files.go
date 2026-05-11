package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

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

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form field 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	mime := header.Header.Get("Content-Type")
	filename := header.Filename
	if filename == "" {
		filename = "untitled"
	}

	rec, results, err := s.mediaService.CreateAttachment(context.Background(), noteID, filename, mime, file)
	if err != nil {
		writeErr(w, err)
		return
	}

	nf := media.NoteFile{
		ID:        rec.ID,
		Filename:  rec.Filename,
		MimeType:  rec.MimeType,
		SizeBytes: rec.SizeBytes,
		URL:       fmt.Sprintf("/file/%d/%d", noteID, rec.ID),
		IsImage:   media.IsImage(rec.MimeType),
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"file":    nf,
		"results": results,
	})
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

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form field 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	mime := header.Header.Get("Content-Type")
	filename := header.Filename
	if filename == "" {
		filename = "untitled"
	}

	rec, results, err := s.mediaService.CreatePendingInline(context.Background(), noteID, filename, mime, file)
	if err != nil {
		writeErr(w, err)
		return
	}

	url := fmt.Sprintf("/file/%d/%d", noteID, rec.ID)

	// Build the markdown insertion string
	var markdown string
	if media.IsImage(rec.MimeType) {
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
		},
		"markdown": markdown,
		"results":  results,
	})
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

	if err := s.mediaService.RemoveAttachment(context.Background(), noteID, fileID); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Serve File ---

// serveFile handles GET /file/:noteID/:fileID
// noteID is cosmetic; auth + fileID control access.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
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
	err := s.db.QueryRow(`SELECT mime_type, filename FROM files WHERE id = ? AND deleted_at IS NULL`, fileID).Scan(&mimeType, &filename)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Set headers
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))

	// Decrypt and serve directly to response
	rec, err := s.mediaService.ReadFile(context.Background(), fileID, w)
	if err != nil {
		// If headers were already written, we can't change the status
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
