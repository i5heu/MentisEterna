package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/internal/media"
	internaltags "github.com/i5heu/MentisEterna/internal/tags"
)

// handleAudioIngest accepts an audio file upload over HTTP, creates a standard
// note, attaches the file, and queues a background job that runs the STT
// pipeline and appends the transcript to the note body. It is secured by a
// secret bearer token (INGEST_TOKEN env) rather than a session, so it bypasses
// the session `protected()` wrapper.
func (s *Server) handleAudioIngest(w http.ResponseWriter, r *http.Request) {
	if s.ingestToken == "" {
		http.Error(w, "audio ingest not configured", http.StatusServiceUnavailable)
		return
	}

	provided := extractBearerToken(r)
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.ingestToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.mediaService == nil || s.sttClient == nil {
		http.Error(w, "STT service not configured", http.StatusServiceUnavailable)
		return
	}

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

	// Sniff the first bytes and reject non-audio uploads before any side effect.
	var sniff [512]byte
	n, _ := io.ReadFull(file, sniff[:])
	src := io.MultiReader(bytes.NewReader(sniff[:n]), file)
	mime := media.DetectMIME(sniff[:n])
	if !media.IsSTTable(mime) {
		http.Error(w, "file is not a supported audio type", http.StatusUnsupportedMediaType)
		return
	}

	filename := header.Filename
	if filename == "" {
		filename = "untitled"
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = audioTitleFromFilename(filename)
	}

	var parentID *int64
	if v := strings.TrimSpace(r.FormValue("parent_id")); v != "" {
		pid, convErr := strconv.ParseInt(v, 10, 64)
		if convErr != nil || pid <= 0 {
			http.Error(w, "invalid parent_id", http.StatusBadRequest)
			return
		}
		parentID = &pid
	}

	noteID, err := s.insertIngestNote(title, parentID)
	if err != nil {
		writeErr(w, err)
		return
	}

	rec, results, err := s.mediaService.CreateAttachment(r.Context(), noteID, filename, mime, src)
	if err != nil {
		writeErr(w, err)
		return
	}

	s.enqueueSTTToNote(rec.ID, noteID)

	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, noteID))
	if err != nil {
		writeErr(w, err)
		return
	}
	note := NoteDetail{
		ID:        sum.ID,
		Title:     sum.Title,
		ParentID:  sum.ParentID,
		Type:      sum.Type,
		Pinned:    sum.Pinned,
		Body:      sum.Body,
		CreatedAt: sum.CreatedAt,
		UpdatedAt: sum.UpdatedAt,
	}
	s.enrichDetail(&note)
	note.Attachments, _ = s.loadNoteAttachments(noteID)

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

	s.notifyNotesChanged("created", noteID)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"note": note, "file": nf, "results": results})
}

// audioNoteTag is the manual tag forced onto every ingested audio note after a
// successful STT transcription. It is stored as a manual tag (tags_refs) so it
// survives auto-tag regeneration.
const audioNoteTag = "audio-note"

// audioTitleFromFilename derives a human-readable note title from an uploaded
// file's name (e.g. "recording.m4a" → "recording").
func audioTitleFromFilename(filename string) string {
	base := filepath.Base(filename)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return "Audio note"
	}
	return base
}

// insertIngestNote creates a standard note with an empty body update inside a
// transaction. It mirrors createNote's inserts only (no tags, no custom data).
func (s *Server) insertIngestNote(title string, parentID *int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO notes (title, parent_id, type) VALUES (?, ?, 'standard')`, title, parentID)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, '')`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// enqueueSTTToNote enqueues an stt_to_note job for the given file, threading the
// destination note ID so the transcript can be appended to the note body.
func (s *Server) enqueueSTTToNote(fileID, noteID int64) {
	if s.jobManager == nil || s.mediaService == nil || s.sttClient == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{"file_id": fileID, "note_id": noteID})
	if _, err := s.jobManager.Enqueue("_media", "stt_to_note", payload); err != nil {
		log.Printf("stt_to_note: enqueue file %d: %v", fileID, err)
	}
}

// appendNoteTranscript appends the transcript as a new body update to the note
// and refreshes its search index. Empty transcripts are a no-op.
func (s *Server) appendNoteTranscript(noteID int64, transcript string) error {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return nil
	}
	var current string
	err := s.db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = ""
	} else if err != nil {
		return err
	}
	var body string
	if strings.TrimSpace(current) == "" {
		body = transcript
	} else {
		body = strings.TrimRight(current, "\n") + "\n\n" + transcript
	}
	if _, err := s.db.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, body); err != nil {
		return err
	}
	s.enqueueVSSIndex(noteID)
	s.notifyNotesChanged("updated", noteID)
	return nil
}

// ingestSTTToNoteTask is the background job handler for an ingested audio file:
// it runs the existing STT pipeline and appends the resulting transcript to the
// note body. It mirrors sttFileTask semantics (including the no-retry error
// handling) with the extra note-body append.
func (s *Server) ingestSTTToNoteTask(db *sql.DB, payload []byte) (string, error) {
	if s.sttClient == nil || s.mediaService == nil {
		return "", fmt.Errorf("stt_to_note: STT client or media service not configured")
	}
	var p struct {
		FileID int64 `json:"file_id"`
		NoteID int64 `json:"note_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("stt_to_note: invalid payload: %w", err)
	}

	ctx := context.Background()
	result, err := s.mediaService.RunSTTForFile(ctx, p.FileID, s.sttClient)
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return fmt.Sprintf("STT for file %d completed with error: %s", p.FileID, result.Error), nil
	}

	if err := s.appendNoteTranscript(p.NoteID, result.STTText); err != nil {
		return "", fmt.Errorf("stt_to_note: append to note %d: %w", p.NoteID, err)
	}
	s.enqueueSTTEmbedding(p.FileID, result.STTText)

	// STT succeeded: let the title model title the note from the transcript.
	if s.titleClient != nil {
		if _, err := s.generateTitleForNote(db, p.NoteID, result.STTText); err != nil {
			log.Printf("stt_to_note: title generation for note %d: %v", p.NoteID, err)
		}
	}

	// Auto tags: the model now sees the generated title. Failures are logged,
	// not fatal — the transcript is already persisted.
	if s.autoTagger != nil {
		if _, err := s.generateAndPersistAutoTags(ctx, db, p.NoteID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Sprintf("Skipped auto tags for missing note %d", p.NoteID), nil
			}
			log.Printf("stt_to_note: auto tags for note %d: %v", p.NoteID, err)
		}
	}

	// Force the audio-note manual tag (merge so any user-added manual tags are kept).
	manualTags, err := loadTags(db, p.NoteID)
	if err != nil {
		return "", fmt.Errorf("stt_to_note: load tags: %w", err)
	}
	forced := internaltags.NormalizeNames(append(manualTags, audioNoteTag))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := saveTags(tx, p.NoteID, forced); err != nil {
		return "", fmt.Errorf("stt_to_note: save tags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if s.llm != nil {
		s.enqueueVSSIndex(p.NoteID)
	}
	s.notifyNotesChanged("auto_tags_generated", p.NoteID)

	return fmt.Sprintf("STT for file %d appended to note %d: %d chars", p.FileID, p.NoteID, len(result.STTText)), nil
}
