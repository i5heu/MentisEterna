package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type Note struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	ParentID  *int64  `json:"parent_id"`
	Body      string  `json:"body"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type NoteUpdate struct {
	ID        int64  `json:"id"`
	NoteID    int64  `json:"note_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

const noteSelectSQL = `
	SELECT n.id, n.title, n.parent_id, n.created_at,
	       COALESCE(u.body, '') AS body,
	       COALESCE(u.created_at, n.created_at) AS updated_at
	FROM notes n
	LEFT JOIN updates u ON u.id = (
		SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
	)
`

func scanNote(row interface{ Scan(...any) error }) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Title, &n.ParentID, &n.CreatedAt, &n.Body, &n.UpdatedAt)
	return n, err
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listNotes(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(noteSelectSQL + ` ORDER BY updated_at DESC`)
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
		Title    string `json:"title"`
		Body     string `json:"body"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO notes (title, parent_id) VALUES (?, ?)`, in.Title, in.ParentID)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, _ := res.LastInsertId()

	if _, err = tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, id, in.Body); err != nil {
		writeErr(w, err)
		return
	}

	if err = tx.Commit(); err != nil {
		writeErr(w, err)
		return
	}

	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
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
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) updateNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE notes SET title = ?, parent_id = ? WHERE id = ?`, in.Title, in.ParentID, id)
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

	if err = tx.Commit(); err != nil {
		writeErr(w, err)
		return
	}

	n, err := scanNote(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	id, ok := noteID(w, r)
	if !ok {
		return
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
	w.WriteHeader(http.StatusNoContent)
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

func noteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	seg := strings.TrimPrefix(r.URL.Path, "/notes/")
	seg = strings.TrimSuffix(seg, "/history")
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
