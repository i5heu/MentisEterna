package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/searchindex"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// NoteSummary is the lightweight response shape for list endpoints.
type NoteSummary struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	ParentID  *int64 `json:"parent_id"`
	Type      string `json:"type"`
	Pinned    bool   `json:"pinned"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PluginDetail holds plugin-specific data on detailed note responses.
type PluginDetail struct {
	Type   string `json:"type"`
	Config any    `json:"config,omitempty"`
	View   any    `json:"view,omitempty"`
}

// NoteDetail is the full response shape for single-note endpoints.
type NoteDetail struct {
	ID          int64         `json:"id"`
	Title       string        `json:"title"`
	ParentID    *int64        `json:"parent_id"`
	Type        string        `json:"type"`
	Pinned      bool          `json:"pinned"`
	Body        string        `json:"body"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
	Plugin      *PluginDetail `json:"plugin,omitempty"`
	Tags        []string      `json:"tags"`
	Attachments []NoteFile    `json:"attachments,omitempty"`
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

func scanSummary(row interface{ Scan(...any) error }) (NoteSummary, error) {
	var n NoteSummary
	err := row.Scan(&n.ID, &n.Title, &n.ParentID, &n.Type, &n.Pinned, &n.CreatedAt, &n.Body, &n.UpdatedAt)
	return n, err
}

// enrichDetail attaches plugin config/view, tags, and attachments to a note detail.
func (s *Server) enrichDetail(n *NoteDetail) {
	if n == nil {
		return
	}

	// Load tags for all note types (including standard).
	tags, err := loadTags(s.db.DB, n.ID)
	if err != nil {
		log.Printf("tags: load for note %d: %v", n.ID, err)
		tags = []string{}
	} else if tags == nil {
		tags = []string{}
	}
	n.Tags = tags

	plugin, exists := notetype.Registry[n.Type]
	if !exists {
		return
	}

	pd := &PluginDetail{Type: n.Type}

	// Config
	if cl, ok := plugin.(notetype.ConfigLoader); ok {
		config, err := cl.LoadConfig(context.Background(), s.db.DB, 0, n.ID)
		if err != nil {
			log.Printf("notetype: load config for note %d (type=%s): %v", n.ID, n.Type, err)
		} else if len(config) > 0 {
			var cfg any
			if err := json.Unmarshal(config, &cfg); err == nil {
				pd.Config = cfg
			}
		}
	}

	// View
	if vb, ok := plugin.(notetype.ViewBuilder); ok {
		view, err := vb.BuildView(context.Background(), s.db.DB, 0, n.ID)
		if err != nil {
			log.Printf("notetype: build view for note %d (type=%s): %v", n.ID, n.Type, err)
		} else {
			pd.View = view
		}
	}

	n.Plugin = pd
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
		WHERE fr.note_id = ? AND fr.ref_kind IN ('attachment', 'inline') AND f.deleted_at IS NULL
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listNotes(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(noteSelectSQL + ` ORDER BY n.pinned DESC, updated_at DESC`)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	notes := []NoteSummary{}
	for rows.Next() {
		n, err := scanSummary(rows)
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
	if !s.decodeJSONBody(w, r, &in) {
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
		if cv, ok := plugin.(notetype.ConfigValidator); ok {
			if err := cv.ValidateConfig(in.CustomData); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
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

	// Let the plugin persist its config.
	if plugin, exists := notetype.Registry[in.Type]; exists && len(in.CustomData) > 0 {
		if cs, ok := plugin.(notetype.ConfigSaver); ok {
			if err := cs.SaveConfig(context.Background(), tx, 0, id, in.CustomData); err != nil {
				writeErr(w, err)
				return
			}
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
	if in.Type == "recipe" {
		s.classifyRecipeIngredientsForNotes(id)
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

	var n NoteDetail
	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	n = NoteDetail{
		ID: sum.ID, Title: sum.Title, ParentID: sum.ParentID,
		Type: sum.Type, Pinned: sum.Pinned,
		Body: sum.Body, CreatedAt: sum.CreatedAt, UpdatedAt: sum.UpdatedAt,
	}
	s.enrichDetail(&n)
	n.Attachments, _ = s.loadNoteAttachments(n.ID)
	// Async search embedding sync via job queue.
	s.enqueueVSSIndex(id)
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
	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	n := NoteDetail{
		ID: sum.ID, Title: sum.Title, ParentID: sum.ParentID,
		Type: sum.Type, Pinned: sum.Pinned,
		Body: sum.Body, CreatedAt: sum.CreatedAt, UpdatedAt: sum.UpdatedAt,
	}
	s.enrichDetail(&n)
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
	if !s.decodeJSONBody(w, r, &in) {
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
		if cv, ok := plugin.(notetype.ConfigValidator); ok {
			if err := cv.ValidateConfig(in.CustomData); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
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

	// Let the plugin persist its config.
	if plugin, exists := notetype.Registry[in.Type]; exists {
		if cs, ok := plugin.(notetype.ConfigSaver); ok {
			if err := cs.SaveConfig(context.Background(), tx, 0, id, in.CustomData); err != nil {
				writeErr(w, err)
				return
			}
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
	if in.Type == "recipe" {
		s.classifyRecipeIngredientsForNotes(id)
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

	var n NoteDetail
	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	n = NoteDetail{
		ID: sum.ID, Title: sum.Title, ParentID: sum.ParentID,
		Type: sum.Type, Pinned: sum.Pinned,
		Body: sum.Body, CreatedAt: sum.CreatedAt, UpdatedAt: sum.UpdatedAt,
	}
	s.enrichDetail(&n)
	n.Attachments, _ = s.loadNoteAttachments(n.ID)
	// Async search embedding sync via job queue.
	s.enqueueVSSIndex(id)
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

	if s.db.VSSAvailable() {
		if err := searchindex.DeleteNoteIndex(s.db.DB, id); err != nil {
			log.Printf("search: delete index for note %d: %v", id, err)
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

	// Clean up any legacy whole-note vector row.
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
	if !s.decodeJSONBody(w, r, &in) {
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

	var n NoteDetail
	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	n = NoteDetail{
		ID: sum.ID, Title: sum.Title, ParentID: sum.ParentID,
		Type: sum.Type, Pinned: sum.Pinned,
		Body: sum.Body, CreatedAt: sum.CreatedAt, UpdatedAt: sum.UpdatedAt,
	}
	s.enrichDetail(&n)
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

	var chain []NoteSummary
	cur := id
	for {
		n, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, cur))
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

// ChildNote extends NoteSummary with a child_count for thread indicators.
type ChildNote struct {
	NoteSummary
	ChildCount  int64      `json:"child_count"`
	Attachments []NoteFile `json:"attachments,omitempty"`
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
		n, err := scanSummary(rows)
		if err != nil {
			writeErr(w, err)
			return
		}
		cn := ChildNote{NoteSummary: n}
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
// POST /notes/:id/action  (legacy; delegates to the unified dispatcher)
func (s *Server) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, ok := noteID(w, r)
	if !ok {
		return
	}

	var in struct {
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	if !s.decodeJSONBody(w, r, &in) {
		return
	}

	s.dispatchAction(w, r, id, in.Action, in.Params)
}

// handlePluginActionV2 handles the new action route.
// POST /notes/:id/actions/:actionID
func (s *Server) handlePluginActionV2(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, actionID, ok := noteIDAndAction(w, r)
	if !ok {
		return
	}

	var params json.RawMessage
	if r.Body != nil {
		var in struct {
			Params json.RawMessage `json:"params"`
		}
		if !s.decodeOptionalJSONBody(w, r, &in) {
			return
		}
		params = in.Params
	}

	s.dispatchAction(w, r, id, actionID, params)
}

// dispatchAction resolves the note type, finds the action handler, and executes.
func (s *Server) dispatchAction(w http.ResponseWriter, r *http.Request, noteID int64, actionID string, params json.RawMessage) {
	// Load the note to find its type.
	var noteType string
	err := s.db.QueryRow(`SELECT type FROM notes WHERE id = ?`, noteID).Scan(&noteType)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	plugin, exists := notetype.Registry[noteType]
	if !exists {
		http.Error(w, "no actions for this note type", http.StatusNotFound)
		return
	}

	ah, ok := plugin.(notetype.ActionHandler)
	if !ok {
		http.Error(w, "no actions for this note type", http.StatusNotFound)
		return
	}

	result, err := ah.HandleAction(context.Background(), s.db.DB, 0, noteID, actionID, params)
	if err != nil {
		if errors.Is(err, notetype.ErrUnknownAction) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		var badReq *notetype.BadRequestError
		if errors.As(err, &badReq) {
			http.Error(w, badReq.Error(), http.StatusBadRequest)
			return
		}
		writeErr(w, err)
		return
	}
	s.maybePostProcessRecipeAction(noteType, actionID, noteID, result)
	writeJSON(w, http.StatusOK, result)
}

func noteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	seg := strings.TrimPrefix(r.URL.Path, "/notes/")
	seg = strings.TrimSuffix(seg, "/history")
	seg = strings.TrimSuffix(seg, "/children")
	seg = strings.TrimSuffix(seg, "/action")
	seg = strings.TrimSuffix(seg, "/pin")
	// Strip /actions/:actionID suffix if present.
	if idx := strings.LastIndex(seg, "/actions/"); idx >= 0 {
		seg = seg[:idx]
	}
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// noteIDAndAction extracts a note ID and action ID from a path like "/notes/123/actions/generate".
func noteIDAndAction(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	seg := strings.TrimPrefix(r.URL.Path, "/notes/")
	parts := strings.SplitN(seg, "/actions/", 2)
	if len(parts) != 2 || parts[1] == "" {
		http.Error(w, "invalid action path", http.StatusBadRequest)
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, "", false
	}
	return id, parts[1], true
}

// syncEmbeddingTask is the job task handler for vector embedding generation.
// It re-reads the current note state so title/path/tag changes are indexed too.
func (s *Server) syncEmbeddingTask(db *sql.DB, payload []byte) (string, error) {
	if s.llm == nil {
		return "", fmt.Errorf("vss_index: no embedding client configured")
	}
	var p struct {
		NoteID int64 `json:"note_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("vss_index: invalid payload: %w", err)
	}
	if p.NoteID <= 0 {
		return "", fmt.Errorf("vss_index: invalid note_id")
	}

	doc, err := searchindex.LoadDocument(db, p.NoteID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("Skipped note %d: deleted before indexing", p.NoteID), nil
	}
	if err != nil {
		return "", fmt.Errorf("load note document: %w", err)
	}
	chunkCount, err := searchindex.ReplaceNoteIndex(db, s.llm, doc)
	if err != nil {
		return "", fmt.Errorf("replace note search index: %w", err)
	}
	// Clean up any legacy whole-note vector row.
	_, _ = db.Exec(`DELETE FROM vss_notes WHERE rowid = ?`, p.NoteID)
	return fmt.Sprintf("Indexed note %d (%d search chunks)", p.NoteID, chunkCount), nil
}

// enqueueVSSIndex enqueues a vss_index job for the given note.
func (s *Server) enqueueVSSIndex(noteID int64) {
	if s.jobManager == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"note_id": noteID,
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

	release := llm.BeginBackendUse(s.chatClient)
	defer release()
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
	s.enqueueVSSIndex(p.NoteID)
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

// syncOCREmbeddingTask generates a vector embedding for OCR text and stores it
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

	release := llm.BeginBackendUse(s.llm)
	defer release()

	text := llm.TruncateForEmbedding(p.OCRText)
	vec, err := s.llm.GenerateEmbedding(text)
	if err != nil {
		return "", fmt.Errorf("generate OCR embedding: %w", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)
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

// syncSTTEmbeddingTask generates a vector embedding for STT text and stores it
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

	release := llm.BeginBackendUse(s.llm)
	defer release()

	text := llm.TruncateForEmbedding(p.STTText)
	vec, err := s.llm.GenerateEmbedding(text)
	if err != nil {
		return "", fmt.Errorf("generate STT embedding: %w", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)
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

// handleNoteTypes returns the catalog of all available note types.
// GET /note-types
func (s *Server) handleNoteTypes(w http.ResponseWriter, r *http.Request) {
	// Start with the synthetic "standard" type.
	catalog := []notetype.Manifest{
		{
			ID:          "standard",
			Label:       "Standard Note",
			Description: "A plain markdown note with no special structure.",
			Category:    "General",
			SortOrder:   0,
			Editor:      notetype.EditorMeta{Mode: "none"},
			Viewer:      notetype.ViewerMeta{Mode: "none"},
			HasConfig:   false,
			HasView:     false,
			HasActions:  false,
		},
	}

	// Append all registered plugin manifests.
	catalog = append(catalog, notetype.ListManifests()...)

	writeJSON(w, http.StatusOK, catalog)
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
