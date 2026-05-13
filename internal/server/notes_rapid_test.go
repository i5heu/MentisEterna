package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"pgregory.net/rapid"
)

// newServerRapid opens a fresh Server with an in-memory database.
// Must use the outer *testing.T so that Cleanup works correctly
// even when called inside rapid.Check callbacks.
func newServerRapid(t *testing.T) *Server {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, ":0", nil, nil, nil, nil)
}

func rapidCreateNote(rt *rapid.T, s *Server, title, body string, parentID *int64) NoteDetail {
	type req struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		ParentID *int64 `json:"parent_id,omitempty"`
	}
	b, err := json.Marshal(req{Title: title, Body: body, ParentID: parentID})
	if err != nil {
		rt.Fatalf("marshal createNote: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	if w.Code != http.StatusCreated {
		rt.Fatalf("createNote: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var n NoteDetail
	if err := json.NewDecoder(w.Body).Decode(&n); err != nil {
		rt.Fatalf("createNote decode: %v", err)
	}
	return n
}

func rapidUpdateNote(rt *rapid.T, s *Server, id int64, title, body string) NoteDetail {
	type req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	b, err := json.Marshal(req{Title: title, Body: body})
	if err != nil {
		rt.Fatalf("marshal updateNote: %v", err)
	}
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", id), bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		rt.Fatalf("updateNote: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var n NoteDetail
	if err := json.NewDecoder(w.Body).Decode(&n); err != nil {
		rt.Fatalf("updateNote decode: %v", err)
	}
	return n
}

// TestPropCreateAndGetNote: a note retrieved immediately after creation must match title and body.
func TestPropCreateAndGetNote(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		s := newServerRapid(t)
		title := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9 _-]{0,49}`).Draw(rt, "title")
		body := rapid.String().Draw(rt, "body")

		created := rapidCreateNote(rt, s, title, body, nil)

		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d", created.ID), nil)
		w := httptest.NewRecorder()
		s.getNote(w, r)
		if w.Code != http.StatusOK {
			rt.Fatalf("getNote: expected 200, got %d", w.Code)
		}
		var got NoteDetail
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			rt.Fatalf("decode: %v", err)
		}
		if got.Title != title {
			rt.Fatalf("title: want %q, got %q", title, got.Title)
		}
		if got.Body != body {
			rt.Fatalf("body: want %q, got %q", body, got.Body)
		}
	})
}

// TestPropEachUpdateAddsOneHistoryEntry: N updates must grow history to exactly N+1 entries.
func TestPropEachUpdateAddsOneHistoryEntry(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		s := newServerRapid(t)
		updateCount := rapid.IntRange(1, 8).Draw(rt, "updates")
		title := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,29}`).Draw(rt, "title")

		n := rapidCreateNote(rt, s, title, "initial body", nil)

		for i := 0; i < updateCount; i++ {
			newTitle := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,29}`).Draw(rt, fmt.Sprintf("title_%d", i))
			newBody := rapid.String().Draw(rt, fmt.Sprintf("body_%d", i))
			rapidUpdateNote(rt, s, n.ID, newTitle, newBody)
		}

		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d/history", n.ID), nil)
		w := httptest.NewRecorder()
		s.getNoteHistory(w, r)
		if w.Code != http.StatusOK {
			rt.Fatalf("getNoteHistory: expected 200, got %d", w.Code)
		}
		var history []NoteUpdate
		if err := json.NewDecoder(w.Body).Decode(&history); err != nil {
			rt.Fatalf("decode history: %v", err)
		}
		want := updateCount + 1 // +1 for the initial update created alongside the note
		if len(history) != want {
			rt.Fatalf("history length: want %d, got %d", want, len(history))
		}
	})
}

// TestPropDeleteNoteRemovesUpdatesFromDB: after a note is deleted, its updates must not
// remain in the database (verifies ON DELETE CASCADE on updates.note_id).
func TestPropDeleteNoteRemovesUpdatesFromDB(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		s := newServerRapid(t)
		updateCount := rapid.IntRange(0, 6).Draw(rt, "updates")
		title := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,29}`).Draw(rt, "title")

		n := rapidCreateNote(rt, s, title, "body", nil)
		for i := 0; i < updateCount; i++ {
			rapidUpdateNote(rt, s, n.ID, title, fmt.Sprintf("body-%d", i))
		}

		r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", n.ID), nil)
		w := httptest.NewRecorder()
		s.deleteNote(w, r)
		if w.Code != http.StatusNoContent {
			rt.Fatalf("deleteNote: expected 204, got %d", w.Code)
		}

		var remaining int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM updates WHERE note_id = ?`, n.ID).Scan(&remaining); err != nil {
			rt.Fatalf("count updates: %v", err)
		}
		if remaining != 0 {
			rt.Fatalf("expected 0 updates after note delete, got %d (had %d before)", remaining, updateCount+1)
		}
	})
}

// TestPropNoteListCountMatchesCreated: after creating N notes, listing must return exactly N results.
func TestPropNoteListCountMatchesCreated(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		s := newServerRapid(t)
		n := rapid.IntRange(1, 10).Draw(rt, "count")

		for i := 0; i < n; i++ {
			title := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,29}`).Draw(rt, fmt.Sprintf("title_%d", i))
			rapidCreateNote(rt, s, title, "", nil)
		}

		r := httptest.NewRequest(http.MethodGet, "/notes", nil)
		w := httptest.NewRecorder()
		s.listNotes(w, r)
		if w.Code != http.StatusOK {
			rt.Fatalf("listNotes: expected 200, got %d", w.Code)
		}
		var notes []NoteSummary
		if err := json.NewDecoder(w.Body).Decode(&notes); err != nil {
			rt.Fatalf("decode: %v", err)
		}
		if len(notes) != n {
			rt.Fatalf("list count: want %d, got %d", n, len(notes))
		}
	})
}

// TestPropLatestBodyIsReturned: getting a note must always reflect the body from the most recent update.
func TestPropLatestBodyIsReturned(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		s := newServerRapid(t)
		title := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,29}`).Draw(rt, "title")
		updateCount := rapid.IntRange(1, 6).Draw(rt, "updates")

		n := rapidCreateNote(rt, s, title, "initial", nil)

		var lastBody string
		for i := 0; i < updateCount; i++ {
			lastBody = rapid.String().Draw(rt, fmt.Sprintf("body_%d", i))
			rapidUpdateNote(rt, s, n.ID, title, lastBody)
		}

		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d", n.ID), nil)
		w := httptest.NewRecorder()
		s.getNote(w, r)
		if w.Code != http.StatusOK {
			rt.Fatalf("getNote: expected 200, got %d", w.Code)
		}
		var got NoteDetail
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			rt.Fatalf("decode: %v", err)
		}
		if got.Body != lastBody {
			rt.Fatalf("body: want %q (last update), got %q", lastBody, got.Body)
		}
	})
}
