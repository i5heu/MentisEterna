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
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listNotes(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(`SELECT id, title, body, created_at, updated_at FROM notes ORDER BY id DESC`)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	notes := []Note{}
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			writeErr(w, err)
			return
		}
		notes = append(notes, n)
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	res, err := s.db.Exec(`INSERT INTO notes (title, body) VALUES (?, ?)`, in.Title, in.Body)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, _ := res.LastInsertId()

	var n Note
	if err := s.db.QueryRow(
		`SELECT id, title, body, created_at, updated_at FROM notes WHERE id = ?`, id,
	).Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
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
	var n Note
	err := s.db.QueryRow(
		`SELECT id, title, body, created_at, updated_at FROM notes WHERE id = ?`, id,
	).Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
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
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	res, err := s.db.Exec(
		`UPDATE notes SET title = ?, body = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		in.Title, in.Body, id,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var n Note
	_ = s.db.QueryRow(
		`SELECT id, title, body, created_at, updated_at FROM notes WHERE id = ?`, id,
	).Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
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

func noteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	seg := strings.TrimPrefix(r.URL.Path, "/notes/")
	id, err := strconv.ParseInt(seg, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
