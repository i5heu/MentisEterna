package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

type Note struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	ParentID   *int64   `json:"parent_id"`
	Type       string   `json:"type"`
	Pinned     bool     `json:"pinned"`
	Body       string   `json:"body"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	CustomData any      `json:"custom_data,omitempty"`
	UISchema   any      `json:"ui_schema,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	// Attachments are files attached to this note.
	Attachments []NoteFile `json:"attachments,omitempty"`
}

// NoteFile is a lightweight view of a file for note JSON responses.
type NoteFile struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
	IsImage   bool   `json:"is_image"`
	IsAudio   bool   `json:"is_audio"`
}

type NoteUpdate struct {
	ID        int64  `json:"id"`
	NoteID    int64  `json:"note_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

const noteSelectSQL = `
	SELECT n.id, n.title, n.parent_id, n.type, n.pinned, n.created_at,
	       COALESCE(u.body, '') AS body,
	       COALESCE(u.created_at, n.created_at) AS updated_at
	FROM notes n
	LEFT JOIN updates u ON u.id = (
		SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
	)
`

func scanNote(row interface{ Scan(...any) error }) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Title, &n.ParentID, &n.Type, &n.Pinned, &n.CreatedAt, &n.Body, &n.UpdatedAt)
	return n, err
}

// enrichNote attaches plugin-specific custom data, UI schema, and tags to a note.
func (s *Server) enrichNote(n *Note) {
	if n == nil {
		return
	}
	plugin, exists := notetype.Registry[n.Type]
	if !exists {
		return
	}
	customData, err := plugin.ProcessLoad(context.Background(), s.db.DB, 0, n.ID)
	if err != nil {
		log.Printf("notetype: load custom data for note %d (type=%s): %v", n.ID, n.Type, err)
		return
	}
	n.CustomData = customData
	n.UISchema = plugin.UISchema()
	// Load tags.
	tags, err := loadTags(s.db.DB, n.ID)
	if err != nil {
		log.Printf("tags: load for note %d: %v", n.ID, err)
	} else {
		n.Tags = tags
	}
}

// loadTags returns the tag names for a note.
func loadTags(d *sql.DB, noteID int64) ([]string, error) {
	rows, err := d.Query(`SELECT t.name FROM tags t JOIN tags_refs tr ON tr.tag_id = t.id WHERE tr.note_id = ? ORDER BY t.name`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, name)
	}
	return tags, rows.Err()
}

// saveTags replaces the tags for a note within a transaction.
// Each tag name is trimmed; blank names are skipped.
func saveTags(tx *sql.Tx, noteID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM tags_refs WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, name := range tags {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		// Upsert the tag.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (name) VALUES (?)`, name); err != nil {
			return err
		}
		// Resolve the tag ID and create the reference.
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO tags_refs (note_id, tag_id)
			SELECT ?, id FROM tags WHERE name = ?
		`, noteID, name); err != nil {
			return err
		}
	}
	return nil
}

// loadNoteAttachments loads attachment metadata for a note from the database.
func (s *Server) loadNoteAttachments(noteID int64) ([]NoteFile, error) {
	rows, err := s.db.Query(`
		SELECT f.id, f.filename, f.mime_type, f.size_bytes
		FROM files f
		JOIN files_refs fr ON fr.file_id = f.id
		WHERE fr.note_id = ? AND fr.ref_kind = 'attachment' AND f.deleted_at IS NULL
	`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []NoteFile
	for rows.Next() {
		var nf NoteFile
		if err := rows.Scan(&nf.ID, &nf.Filename, &nf.MimeType, &nf.SizeBytes); err != nil {
			return nil, err
		}
		nf.URL = fmt.Sprintf("/file/%d/%d", noteID, nf.ID)
		nf.IsImage = isImageMIME(nf.MimeType)
		nf.IsAudio = isAudioMIME(nf.MimeType)
		files = append(files, nf)
	}
	return files, rows.Err()
}

// isImageMIME returns true if the given MIME type represents an image.
func isImageMIME(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml", "image/bmp", "image/tiff":
		return true
	default:
		return false
	}
}

// isAudioMIME returns true if the given MIME type represents an audio file.
func isAudioMIME(mimeType string) bool {
	switch mimeType {
	case "audio/mpeg", "audio/wav", "audio/ogg", "audio/mp4", "audio/webm", "audio/flac", "audio/aac":
		return true
	default:
		return false
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listNotes(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(noteSelectSQL + ` ORDER BY n.pinned DESC, updated_at DESC`)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			writeErr(w, err)
			return
		}
		notes = append(notes, n)
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		ParentID   *int64          `json:"parent_id"`
		Type       string          `json:"type"`
		CustomData json.RawMessage `json:"custom_data"`
		Tags       []string        `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	userProvidedTitle := strings.TrimSpace(in.Title) != ""
	if !userProvidedTitle {
		in.Title = "Untitled"
	}
	if in.Type == "" {
		in.Type = "standard"
	}

	// Validate custom data against the plugin, if any.
	if plugin, exists := notetype.Registry[in.Type]; exists && len(in.CustomData) > 0 {
		if err := plugin.Validate(in.CustomData); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO notes (title, parent_id, type) VALUES (?, ?, ?)`, in.Title, in.ParentID, in.Type)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, _ := res.LastInsertId()

	if _, err = tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, id, in.Body); err != nil {
		writeErr(w, err)
		return
	}

	// Let the plugin persist its custom data.
	if plugin, exists := notetype.Registry[in.Type]; exists && len(in.CustomData) > 0 {
		if err := plugin.ProcessSave(context.Background(), tx, 0, id, in.CustomData); err != nil {
			writeErr(w, err)
			return
		}
	}

	// Save tags.
	if err := saveTags(tx, id, in.Tags); err != nil {
		writeErr(w, err)
		return
	}

	if err = tx.Commit(); err != nil {
		writeErr(w, err)
		return
	}

	// Reconcile inline file refs from markdown body (after commit).
	if s.mediaService != nil && in.Body != "" {
		orphaned, err := s.mediaService.ReconcileInlineRefs(context.Background(), id, in.Body)
		if err != nil {
			log.Printf("media: reconcile inline refs for note %d: %v", id, err)
		} else if len(orphaned) > 0 {
			log.Printf("media: note %d: cleaned up %d unreferenced inline files", id, len(orphaned))
		}
	}

	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	// Enrich with custom data and UI schema.
	s.enrichNote(&n)
	n.Attachments, _ = s.loadNoteAttachments(n.ID)
	// Async VSS embedding sync via job queue
	s.enqueueVSSIndex(id, in.Title, in.Body)
	// Async title generation (only if the user didn't provide one)
	if !userProvidedTitle {
		s.enqueueTitleGeneration(id, in.Body)
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	s.enrichNote(&n)
	n.Attachments, _ = s.loadNoteAttachments(n.ID)
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) updateNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		ParentID   *int64          `json:"parent_id"`
		Type       string          `json:"type"`
		CustomData json.RawMessage `json:"custom_data"`
		Tags       []string        `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	userProvidedTitle := strings.TrimSpace(in.Title) != ""
	if !userProvidedTitle {
		in.Title = "Untitled"
	}
	if in.Type == "" {
		in.Type = "standard"
	}

	// Validate custom data against the plugin.
	if plugin, exists := notetype.Registry[in.Type]; exists && len(in.CustomData) > 0 {
		if err := plugin.Validate(in.CustomData); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE notes SET title = ?, parent_id = ?, type = ? WHERE id = ?`, in.Title, in.ParentID, in.Type, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if _, err = tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, id, in.Body); err != nil {
		writeErr(w, err)
		return
	}

	// Let the plugin persist its custom data.
	if plugin, exists := notetype.Registry[in.Type]; exists {
		if err := plugin.ProcessSave(context.Background(), tx, 0, id, in.CustomData); err != nil {
			writeErr(w, err)
			return
		}
	}

	// Save tags.
	if err := saveTags(tx, id, in.Tags); err != nil {
		writeErr(w, err)
		return
	}

	if err = tx.Commit(); err != nil {
		writeErr(w, err)
		return
	}

	// Reconcile inline file refs from markdown body (after commit).
	if s.mediaService != nil && in.Body != "" {
		orphaned, err := s.mediaService.ReconcileInlineRefs(context.Background(), id, in.Body)
		if err != nil {
			log.Printf("media: reconcile inline refs for note %d: %v", id, err)
		} else if len(orphaned) > 0 {
			log.Printf("media: note %d: cleaned up %d unreferenced inline files", id, len(orphaned))
		}
	}

	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.enrichNote(&n)
	n.Attachments, _ = s.loadNoteAttachments(n.ID)
	// Async VSS embedding sync via job queue
	s.enqueueVSSIndex(id, in.Title, in.Body)
	// Async title generation (only if the user didn't provide one)
	if !userProvidedTitle {
		s.enqueueTitleGeneration(id, in.Body)
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}

	// Collect deletable file IDs BEFORE deleting the note (refs still exist).
	var deletableFiles []int64
	if s.mediaService != nil {
		var err error
		deletableFiles, err = s.mediaService.CollectDeletableFilesAfterNoteDelete(context.Background(), id)
		if err != nil {
			log.Printf("media: collect deletable files for note %d: %v", id, err)
		}
	}

	res, err := s.db.Exec(`DELETE FROM notes WHERE id = ?`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Remove VSS embedding
	if s.db.VSSAvailable() {
		_, _ = s.db.Exec(`DELETE FROM vss_notes WHERE rowid = ?`, id)
	}

	// Soft-delete files that have no remaining refs and enqueue replica deletion.
	if s.mediaService != nil && len(deletableFiles) > 0 {
		if err := s.mediaService.SoftDeleteFiles(deletableFiles); err != nil {
			log.Printf("media: soft delete files for note %d: %v", id, err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// setNotePin toggles the pinned status of a note.
// POST /notes/:id/pin with {"pinned": true|false}
func (s *Server) setNotePin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var in struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	res, err := s.db.Exec(`UPDATE notes SET pinned = ? WHERE id = ?`, in.Pinned, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.enrichNote(&n)
	writeJSON(w, http.StatusOK, n)
}

// getNoteAncestors returns the full ancestor chain (root -> ... -> self) for a note.
// GET /notes/:id/ancestors
func (s *Server) getNoteAncestors(w http.ResponseWriter, r *http.Request) {
	id, ok := noteIDRaw(r.URL.Path)
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var chain []Note
	cur := id
	for {
		n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, cur))
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		chain = append(chain, n)
		if n.ParentID == nil {
			break
		}
		cur = *n.ParentID
	}
	// Reverse so the chain is root-first.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	writeJSON(w, http.StatusOK, chain)
}

// noteIDRaw extracts an int64 id from a path like "/notes/123/ancestors"
func noteIDRaw(path string) (int64, bool) {
	seg := strings.TrimPrefix(path, "/notes/")
	seg = strings.TrimSuffix(seg, "/history")
	seg = strings.TrimSuffix(seg, "/children")
	seg = strings.TrimSuffix(seg, "/ancestors")
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ChildNote extends Note with a child_count for thread indicators.
type ChildNote struct {
	Note
	ChildCount int64 `json:"child_count"`
}

// getNoteChildren returns all notes whose parent_id matches the given note ID.
// GET /notes/:id/children
func (s *Server) getNoteChildren(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}

	// Verify parent note exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes WHERE id = ?)`, id).Scan(&exists); err != nil {
		writeErr(w, err)
		return
	}
	if !exists {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rows, err := s.db.Query(noteSelectSQL+` WHERE n.parent_id = ? ORDER BY n.created_at ASC`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	children := []ChildNote{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			writeErr(w, err)
			return
		}
		cn := ChildNote{Note: n}
		// get child count (number of notes whose parent_id is this child's id)
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM notes WHERE parent_id = ?`, n.ID).Scan(&cn.ChildCount)
		// load attachments (skip on error)
		if atts, err := s.loadNoteAttachments(n.ID); err != nil {
			log.Printf("load attachments for child note %d: %v", n.ID, err)
		} else {
			cn.Attachments = atts
		}
		children = append(children, cn)
	}
	writeJSON(w, http.StatusOK, children)
}

func (s *Server) getNoteHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}

	// Verify note exists
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes WHERE id = ?)`, id).Scan(&exists); err != nil {
		writeErr(w, err)
		return
	}
	if !exists {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	rows, err := s.db.Query(
		`SELECT id, note_id, body, created_at FROM updates WHERE note_id = ? ORDER BY id DESC`, id,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	updates := []NoteUpdate{}
	for rows.Next() {
		var u NoteUpdate
		if err := rows.Scan(&u.ID, &u.NoteID, &u.Body, &u.CreatedAt); err != nil {
			writeErr(w, err)
			return
		}
		updates = append(updates, u)
	}
	writeJSON(w, http.StatusOK, updates)
}

// handlePluginAction handles RPC-style actions for plugins.
// POST /notes/:id/action
func (s *Server) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, ok := noteID(w, r)
	if !ok {
		return
	}

	// Load the note to find its type.
	var noteType string
	err := s.db.QueryRow(`SELECT type FROM notes WHERE id = ?`, id).Scan(&noteType)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	var in struct {
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	handler, exists := pluginActionHandlers[noteType]
	if !exists {
		http.Error(w, "no actions for this note type", http.StatusNotFound)
		return
	}

	result, err := handler(s.db.DB, id, in.Action, in.Params)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// PluginActionHandler is a function that handles a plugin-specific action.
type PluginActionHandler func(db *sql.DB, noteID int64, action string, params json.RawMessage) (any, error)

// pluginActionHandlers maps note types to their action handlers.
var pluginActionHandlers = map[string]PluginActionHandler{}

// RegisterPluginActionHandler allows plugins to expose custom RPC actions.
func RegisterPluginActionHandler(noteType string, handler PluginActionHandler) {
	pluginActionHandlers[noteType] = handler
}

func noteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	seg := strings.TrimPrefix(r.URL.Path, "/notes/")
	seg = strings.TrimSuffix(seg, "/history")
	seg = strings.TrimSuffix(seg, "/children")
	seg = strings.TrimSuffix(seg, "/action")
	seg = strings.TrimSuffix(seg, "/pin")
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// syncEmbeddingTask is the job task handler for VSS embedding generation.
// It accepts a JSON payload with "note_id", "title", and "body" fields.
func (s *Server) syncEmbeddingTask(db *sql.DB, payload []byte) (string, error) {
	var p struct {
		NoteID int64  `json:"note_id"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("vss_index: invalid payload: %w", err)
	}

	text := llm.CombineTitleBody(p.Title, p.Body)
	text = llm.TruncateForEmbedding(text)
	vec, err := s.llm.GenerateEmbedding(text)
	if err != nil {
		return "", fmt.Errorf("generate embedding: %w", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)
	// vss0 virtual tables don't support UPDATE/INSERT OR REPLACE.
	// Must DELETE then INSERT.
	if _, err := db.Exec(`DELETE FROM vss_notes WHERE rowid = ?`, p.NoteID); err != nil {
		return "", fmt.Errorf("delete old embedding: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO vss_notes(rowid, body_embedding) VALUES (?, ?)`,
		p.NoteID, vecJSON,
	); err != nil {
		return "", fmt.Errorf("insert embedding: %w", err)
	}
	return fmt.Sprintf("Indexed note %d (%d chars)", p.NoteID, len(p.Body)), nil
}

// enqueueVSSIndex enqueues a vss_index job for the given note.
func (s *Server) enqueueVSSIndex(noteID int64, title, body string) {
	if s.jobManager == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"note_id": noteID,
		"title":   title,
		"body":    body,
	})
	if _, err := s.jobManager.Enqueue("_system", "vss_index", payload); err != nil {
		log.Printf("vss: enqueue index for note %d: %v", noteID, err)
	}
}

// generateTitleTask is the job task handler for auto-generating a note title.
// It accepts a JSON payload with "note_id" and "body" fields.
func (s *Server) generateTitleTask(db *sql.DB, payload []byte) (string, error) {
	if s.chatClient == nil {
		return "", fmt.Errorf("generate_title: no chat client configured")
	}
	var p struct {
		NoteID int64  `json:"note_id"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("generate_title: invalid payload: %w", err)
	}

	title, err := s.chatClient.GenerateTitle(p.Body)
	if err != nil {
		return "", fmt.Errorf("generate title: %w", err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "Empty title generated, keeping Untitled", nil
	}
	// Safety net: truncate to a reasonable length if the model misbehaves.
	if len(title) > 60 {
		title = title[:60]
	}

	if _, err := db.Exec(`UPDATE notes SET title = ? WHERE id = ?`, title, p.NoteID); err != nil {
		return "", fmt.Errorf("update title: %w", err)
	}
	return fmt.Sprintf("Generated title for note %d: %q", p.NoteID, title), nil
}

// backupTask is the job task handler for encrypted database backups.
// It performs a safe SQLite snapshot, encrypts it with AES-256-GCM, and
// uploads to all configured S3 endpoints. The job payload is unused — the
// task always performs a full backup.
func (s *Server) backupTask(db *sql.DB, _ []byte) (string, error) {
	if s.backupService == nil {
		return "", fmt.Errorf("backup: service not configured")
	}
	ctx := context.Background()
	remoteKey, err := s.backupService.Run(ctx)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	return fmt.Sprintf("Backup uploaded to %s", remoteKey), nil
}

// purgeTask is the job task handler for backup retention cleanup.
// It lists all backups across all configured S3 endpoints, applies the
// default retention policy, and deletes expired backups.
func (s *Server) purgeTask(db *sql.DB, _ []byte) (string, error) {
	if s.backupService == nil {
		return "", fmt.Errorf("backup/purge: service not configured")
	}
	ctx := context.Background()
	summary, err := s.backupService.Purge(ctx)
	if err != nil {
		return "", fmt.Errorf("backup/purge: %w", err)
	}
	return summary, nil
}

// ocrFileTask is the job task handler for OCR on uploaded files.
// It accepts a JSON payload with "file_id" field.
func (s *Server) ocrFileTask(db *sql.DB, payload []byte) (string, error) {
	if s.ocrClient == nil || s.mediaService == nil {
		return "", fmt.Errorf("ocr_file: OCR client or media service not configured")
	}
	var p struct {
		FileID int64 `json:"file_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("ocr_file: invalid payload: %w", err)
	}

	ctx := context.Background()
	result, err := s.mediaService.RunOCRForFile(ctx, p.FileID, s.ocrClient)
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return fmt.Sprintf("OCR for file %d completed with error: %s", p.FileID, result.Error), nil
	}

	// OCR succeeded: generate and store the embedding for this OCR text.
	s.enqueueOCREmbedding(p.FileID, result.OCRText)

	return fmt.Sprintf("OCR for file %d completed: %d chars", p.FileID, len(result.OCRText)), nil
}

// enqueueOCR enqueues an OCR job for the given file ID, if it's an image type.
func (s *Server) enqueueOCR(fileID int64) {
	if s.jobManager == nil || s.mediaService == nil || s.ocrClient == nil {
		return
	}
	s.mediaService.EnqueueOCR(fileID)
}

// syncOCREmbeddingTask generates a VSS embedding for OCR text and stores it
// in vss_files_ocr (rowid = file_id). The payload is {"file_id": N, "ocr_text": "..."}.
func (s *Server) syncOCREmbeddingTask(db *sql.DB, payload []byte) (string, error) {
	if s.llm == nil {
		return "", fmt.Errorf("ocr_embedding: no embedding client configured")
	}
	var p struct {
		FileID  int64  `json:"file_id"`
		OCRText string `json:"ocr_text"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("ocr_embedding: invalid payload: %w", err)
	}
	if p.OCRText == "" {
		return fmt.Sprintf("Skipped embedding for file %d: empty OCR text", p.FileID), nil
	}

	text := llm.TruncateForEmbedding(p.OCRText)
	vec, err := s.llm.GenerateEmbedding(text)
	if err != nil {
		return "", fmt.Errorf("generate OCR embedding: %w", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	// vss0 virtual tables don't support UPDATE/INSERT OR REPLACE.
	if _, err := db.Exec(`DELETE FROM vss_files_ocr WHERE rowid = ?`, p.FileID); err != nil {
		return "", fmt.Errorf("delete old OCR embedding: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO vss_files_ocr(rowid, ocr_embedding) VALUES (?, ?)`,
		p.FileID, vecJSON,
	); err != nil {
		return "", fmt.Errorf("insert OCR embedding: %w", err)
	}
	return fmt.Sprintf("Indexed OCR for file %d (%d chars)", p.FileID, len(p.OCRText)), nil
}

// enqueueOCREmbedding enqueues a sync_ocr_embedding job for the given file.
func (s *Server) enqueueOCREmbedding(fileID int64, ocrText string) {
	if s.jobManager == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"file_id":  fileID,
		"ocr_text": ocrText,
	})
	if _, err := s.jobManager.Enqueue("_system", "sync_ocr_embedding", payload); err != nil {
		log.Printf("ocr: enqueue embedding for file %d: %v", fileID, err)
	}
}

// sttFileTask is the job task handler for STT on uploaded audio files.
// It accepts a JSON payload with "file_id" field.
func (s *Server) sttFileTask(db *sql.DB, payload []byte) (string, error) {
	if s.sttClient == nil || s.mediaService == nil {
		return "", fmt.Errorf("stt_file: STT client or media service not configured")
	}
	var p struct {
		FileID int64 `json:"file_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("stt_file: invalid payload: %w", err)
	}

	ctx := context.Background()
	result, err := s.mediaService.RunSTTForFile(ctx, p.FileID, s.sttClient)
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return fmt.Sprintf("STT for file %d completed with error: %s", p.FileID, result.Error), nil
	}

	// STT succeeded: generate and store the embedding for this STT text.
	s.enqueueSTTEmbedding(p.FileID, result.STTText)

	return fmt.Sprintf("STT for file %d completed: %d chars", p.FileID, len(result.STTText)), nil
}

// enqueueSTT enqueues an STT job for the given file ID, if it's an audio type.
func (s *Server) enqueueSTT(fileID int64) {
	if s.jobManager == nil || s.mediaService == nil || s.sttClient == nil {
		return
	}
	s.mediaService.EnqueueSTT(fileID)
}

// syncSTTEmbeddingTask generates a VSS embedding for STT text and stores it
// in vss_files_stt (rowid = file_id). The payload is {"file_id": N, "stt_text": "..."}.
func (s *Server) syncSTTEmbeddingTask(db *sql.DB, payload []byte) (string, error) {
	if s.llm == nil {
		return "", fmt.Errorf("stt_embedding: no embedding client configured")
	}
	var p struct {
		FileID  int64  `json:"file_id"`
		STTText string `json:"stt_text"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("stt_embedding: invalid payload: %w", err)
	}
	if p.STTText == "" {
		return fmt.Sprintf("Skipped embedding for file %d: empty STT text", p.FileID), nil
	}

	text := llm.TruncateForEmbedding(p.STTText)
	vec, err := s.llm.GenerateEmbedding(text)
	if err != nil {
		return "", fmt.Errorf("generate STT embedding: %w", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	// vss0 virtual tables don't support UPDATE/INSERT OR REPLACE.
	if _, err := db.Exec(`DELETE FROM vss_files_stt WHERE rowid = ?`, p.FileID); err != nil {
		return "", fmt.Errorf("delete old STT embedding: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO vss_files_stt(rowid, stt_embedding) VALUES (?, ?)`,
		p.FileID, vecJSON,
	); err != nil {
		return "", fmt.Errorf("insert STT embedding: %w", err)
	}
	return fmt.Sprintf("Indexed STT for file %d (%d chars)", p.FileID, len(p.STTText)), nil
}

// enqueueSTTEmbedding enqueues a sync_stt_embedding job for the given file.
func (s *Server) enqueueSTTEmbedding(fileID int64, sttText string) {
	if s.jobManager == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"file_id":  fileID,
		"stt_text": sttText,
	})
	if _, err := s.jobManager.Enqueue("_system", "sync_stt_embedding", payload); err != nil {
		log.Printf("stt: enqueue embedding for file %d: %v", fileID, err)
	}
}

// enqueueTitleGeneration enqueues a generate_title job for the given note.
func (s *Server) enqueueTitleGeneration(noteID int64, body string) {
	if s.jobManager == nil || s.chatClient == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"note_id": noteID,
		"body":    body,
	})
	if _, err := s.jobManager.Enqueue("_system", "generate_title", payload); err != nil {
		log.Printf("title: enqueue generation for note %d: %v", noteID, err)
	}
}

// SearchResult extends Note with a distance field for ranked search results.
type SearchResult struct {
	Note
	Distance float64 `json:"distance"`
}

// searchNotes performs a semantic search over notes using sqlite-vss.
// GET /notes/search?q=your+query
func (s *Server) searchNotes(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	if !s.db.VSSAvailable() || s.llm == nil {
		// Fallback: return empty results if VSS is unavailable.
		writeJSON(w, http.StatusOK, []SearchResult{})
		return
	}

	query = llm.TruncateForEmbedding(query)
	vec, err := s.llm.GenerateEmbedding(query)
	if err != nil {
		log.Printf("vss: search embedding: %v", err)
		http.Error(w, "failed to generate search embedding", http.StatusInternalServerError)
		return
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	// Guard: if vss_notes is empty, vss_search crashes faiss with "k > 0".
	var vssCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM vss_notes`).Scan(&vssCount); err != nil {
		writeErr(w, err)
		return
	}

	// Collect distances by note ID from vss_notes, vss_files_ocr, and vss_files_stt.
	distByID := make(map[int64]float64)

	if vssCount > 0 {
		// Step 1a: Search vss_notes (note body embeddings).
		vssRows, err := s.db.Query(`
			SELECT rowid, distance
			FROM vss_notes
			WHERE vss_search(body_embedding, ?)
			ORDER BY distance ASC
			LIMIT 10
		`, vecJSON)
		if err != nil {
			writeErr(w, err)
			return
		}
		for vssRows.Next() {
			var rowid int64
			var dist float64
			if err := vssRows.Scan(&rowid, &dist); err != nil {
				vssRows.Close()
				writeErr(w, err)
				return
			}
			distByID[rowid] = dist
		}
		vssRows.Close()
	}

	// Step 1b: Search vss_files_ocr (OCR text embeddings) and resolve to notes.
	// Check if the OCR VSS table exists and has data.
	var ocrVSSExists bool
	s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='vss_files_ocr')`).Scan(&ocrVSSExists)
	if ocrVSSExists {
		var ocrCount int
		s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_ocr`).Scan(&ocrCount)
		if ocrCount > 0 {
			ocrRows, err := s.db.Query(`
				SELECT rowid, distance
				FROM vss_files_ocr
				WHERE vss_search(ocr_embedding, ?)
				ORDER BY distance ASC
				LIMIT 20
			`, vecJSON)
			if err == nil {
				// Collect file IDs from OCR hits.
				ocrDistByFile := map[int64]float64{}
				for ocrRows.Next() {
					var fileID int64
					var dist float64
					if err := ocrRows.Scan(&fileID, &dist); err != nil {
						ocrRows.Close()
						writeErr(w, err)
						return
					}
					ocrDistByFile[fileID] = dist
				}
				ocrRows.Close()

				// Resolve file IDs to note IDs via files_refs.
				if len(ocrDistByFile) > 0 {
					fileIDs := make([]int64, 0, len(ocrDistByFile))
					for fid := range ocrDistByFile {
						fileIDs = append(fileIDs, fid)
					}
					// Build IN clause.
					filePlaceholders := make([]string, len(fileIDs))
					fileArgs := make([]any, len(fileIDs))
					for i, fid := range fileIDs {
						filePlaceholders[i] = "?"
						fileArgs[i] = fid
					}
					refRows, err := s.db.Query(`
							SELECT DISTINCT fr.note_id, fr.file_id
							FROM files_refs fr
							JOIN files f ON f.id = fr.file_id
							WHERE fr.file_id IN (`+strings.Join(filePlaceholders, ",")+`)
							  AND f.deleted_at IS NULL
						`, fileArgs...)
					if err == nil {
						for refRows.Next() {
							var noteID, fileID int64
							if err := refRows.Scan(&noteID, &fileID); err != nil {
								refRows.Close()
								writeErr(w, err)
								return
							}
							dist := ocrDistByFile[fileID]
							// Merge: keep the best (minimum) distance per note.
							if existing, ok := distByID[noteID]; !ok || dist < existing {
								distByID[noteID] = dist
							}
						}
						refRows.Close()
					}
				}
			}
		}
	}

	// Step 1c: Search vss_files_stt (STT text embeddings) and resolve to notes.
	// Check if the STT VSS table exists and has data.
	var sttVSSExists bool
	s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='vss_files_stt')`).Scan(&sttVSSExists)
	if sttVSSExists {
		var sttCount int
		s.db.QueryRow(`SELECT COUNT(*) FROM vss_files_stt`).Scan(&sttCount)
		if sttCount > 0 {
			sttRows, err := s.db.Query(`
				SELECT rowid, distance
				FROM vss_files_stt
				WHERE vss_search(stt_embedding, ?)
				ORDER BY distance ASC
				LIMIT 20
			`, vecJSON)
			if err == nil {
				// Collect file IDs from STT hits.
				sttDistByFile := map[int64]float64{}
				for sttRows.Next() {
					var fileID int64
					var dist float64
					if err := sttRows.Scan(&fileID, &dist); err != nil {
						sttRows.Close()
						writeErr(w, err)
						return
					}
					sttDistByFile[fileID] = dist
				}
				sttRows.Close()

				// Resolve file IDs to note IDs via files_refs.
				if len(sttDistByFile) > 0 {
					fileIDs := make([]int64, 0, len(sttDistByFile))
					for fid := range sttDistByFile {
						fileIDs = append(fileIDs, fid)
					}
					// Build IN clause.
					filePlaceholders := make([]string, len(fileIDs))
					fileArgs := make([]any, len(fileIDs))
					for i, fid := range fileIDs {
						filePlaceholders[i] = "?"
						fileArgs[i] = fid
					}
					refRows, err := s.db.Query(`
							SELECT DISTINCT fr.note_id, fr.file_id
							FROM files_refs fr
							JOIN files f ON f.id = fr.file_id
							WHERE fr.file_id IN (`+strings.Join(filePlaceholders, ",")+`)
							  AND f.deleted_at IS NULL
						`, fileArgs...)
					if err == nil {
						for refRows.Next() {
							var noteID, fileID int64
							if err := refRows.Scan(&noteID, &fileID); err != nil {
								refRows.Close()
								writeErr(w, err)
								return
							}
							dist := sttDistByFile[fileID]
							// Merge: keep the best (minimum) distance per note.
							if existing, ok := distByID[noteID]; !ok || dist < existing {
								distByID[noteID] = dist
							}
						}
						refRows.Close()
					}
				}
			}
		}
	}

	if len(distByID) == 0 {
		writeJSON(w, http.StatusOK, []SearchResult{})
		return
	}

	// Collect top N (up to 10) note IDs sorted by distance.
	type idDist struct {
		id   int64
		dist float64
	}
	sorted := make([]idDist, 0, len(distByID))
	for id, dist := range distByID {
		sorted = append(sorted, idDist{id, dist})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].dist < sorted[j].dist })
	limit := 10
	if len(sorted) < limit {
		limit = len(sorted)
	}
	sorted = sorted[:limit]

	// Collect IDs in order for the IN clause.
	ids := make([]any, len(sorted))
	placeholders := make([]string, len(sorted))
	for i, h := range sorted {
		ids[i] = h.id
		placeholders[i] = "?"
	}

	noteRows, err := s.db.Query(`
		SELECT n.id, n.title, n.parent_id, n.type, n.pinned, n.created_at,
		       COALESCE(u.body, '') AS body,
		       COALESCE(u.created_at, n.created_at) AS updated_at
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.id IN (`+strings.Join(placeholders, ",")+`)
	`, ids...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer noteRows.Close()

	results := []SearchResult{}
	for noteRows.Next() {
		var sr SearchResult
		err := noteRows.Scan(&sr.ID, &sr.Title, &sr.ParentID, &sr.Type, &sr.Pinned, &sr.CreatedAt,
			&sr.Body, &sr.UpdatedAt)
		if err != nil {
			writeErr(w, err)
			return
		}
		sr.Distance = distByID[sr.ID]
		results = append(results, sr)
	}
	if err := noteRows.Err(); err != nil {
		writeErr(w, err)
		return
	}

	// Re-sort by distance since IN clause doesn't preserve order.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	writeJSON(w, http.StatusOK, results)
}

// handleTags returns known tags filtered by an optional ?q= prefix query.
// GET /tags?q=foo
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var rows *sql.Rows
	var err error
	if q == "" {
		rows, err = s.db.Query(`SELECT DISTINCT t.name FROM tags t JOIN tags_refs tr ON tr.tag_id = t.id ORDER BY t.name`)
	} else {
		rows, err = s.db.Query(`SELECT DISTINCT t.name FROM tags t JOIN tags_refs tr ON tr.tag_id = t.id WHERE t.name LIKE ? ORDER BY t.name LIMIT 20`, q+"%")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			writeErr(w, err)
			return
		}
		names = append(names, name)
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}
