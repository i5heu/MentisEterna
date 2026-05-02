package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %q", resp["status"])
	}
}

func TestNoteID(t *testing.T) {
	cases := []struct {
		path   string
		wantID int64
		wantOK bool
	}{
		{"/notes/1", 1, true},
		{"/notes/42", 42, true},
		{"/notes/42/history", 42, true},
		{"/notes/0", 0, false},
		{"/notes/-1", 0, false},
		{"/notes/abc", 0, false},
		{"/notes/", 0, false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		id, ok := noteID(w, r)
		if ok != tc.wantOK {
			t.Errorf("noteID(%q) ok=%v, want %v", tc.path, ok, tc.wantOK)
			continue
		}
		if ok && id != tc.wantID {
			t.Errorf("noteID(%q) id=%d, want %d", tc.path, id, tc.wantID)
		}
		if !ok && w.Code != http.StatusBadRequest {
			t.Errorf("noteID(%q) wrote %d, want 400", tc.path, w.Code)
		}
	}
}

func helperCreateNote(t *testing.T, s *Server, title, body string, parentID *int64) Note {
	t.Helper()
	var payload string
	if parentID != nil {
		payload = fmt.Sprintf(`{"title":%q,"body":%q,"parent_id":%d}`, title, body, *parentID)
	} else {
		payload = fmt.Sprintf(`{"title":%q,"body":%q}`, title, body)
	}
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("createNote: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var n Note
	if err := json.NewDecoder(w.Body).Decode(&n); err != nil {
		t.Fatalf("createNote decode: %v", err)
	}
	return n
}

func TestListNotesEmpty(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	w := httptest.NewRecorder()
	s.listNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var notes []Note
	if err := json.NewDecoder(w.Body).Decode(&notes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("expected empty list, got %d notes", len(notes))
	}
}

func TestListNotesWithData(t *testing.T) {
	s := newTestServer(t)
	helperCreateNote(t, s, "Alpha", "body a", nil)
	helperCreateNote(t, s, "Beta", "body b", nil)

	r := httptest.NewRequest(http.MethodGet, "/notes", nil)
	w := httptest.NewRecorder()
	s.listNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var notes []Note
	if err := json.NewDecoder(w.Body).Decode(&notes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(notes))
	}
}

func TestCreateNoteValid(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "My Note", "hello world", nil)
	if n.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if n.Title != "My Note" {
		t.Errorf("title: got %q, want %q", n.Title, "My Note")
	}
	if n.Body != "hello world" {
		t.Errorf("body: got %q, want %q", n.Body, "hello world")
	}
	if n.CreatedAt == "" {
		t.Error("expected non-empty created_at")
	}
}

func TestCreateNoteEmptyTitle(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"title":"   ","body":"b"}`))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for blank title, got %d", w.Code)
	}
}

func TestCreateNoteInvalidJSON(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader("notjson"))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestCreateNoteWithParent(t *testing.T) {
	s := newTestServer(t)
	parent := helperCreateNote(t, s, "Parent", "", nil)
	child := helperCreateNote(t, s, "Child", "content", &parent.ID)

	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("expected parent_id=%d, got %v", parent.ID, child.ParentID)
	}
}

func TestGetNoteFound(t *testing.T) {
	s := newTestServer(t)
	created := helperCreateNote(t, s, "Find Me", "some body", nil)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d", created.ID), nil)
	w := httptest.NewRecorder()
	s.getNote(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var n Note
	if err := json.NewDecoder(w.Body).Decode(&n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.ID != created.ID {
		t.Errorf("expected ID %d, got %d", created.ID, n.ID)
	}
	if n.Title != "Find Me" {
		t.Errorf("expected title 'Find Me', got %q", n.Title)
	}
}

func TestGetNoteNotFound(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/notes/9999", nil)
	w := httptest.NewRecorder()
	s.getNote(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetNoteInvalidID(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/notes/abc", nil)
	w := httptest.NewRecorder()
	s.getNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateNoteValid(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Original", "old body", nil)

	payload := `{"title":"Updated","body":"new body"}`
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.updateNote(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated Note
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Title != "Updated" {
		t.Errorf("expected title 'Updated', got %q", updated.Title)
	}
	if updated.Body != "new body" {
		t.Errorf("expected body 'new body', got %q", updated.Body)
	}
}

func TestUpdateNoteNotFound(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPut, "/notes/9999", strings.NewReader(`{"title":"X","body":""}`))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUpdateNoteEmptyTitle(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Has Title", "", nil)
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(`{"title":"","body":""}`))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateNoteInvalidJSON(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Has Title", "", nil)
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader("notjson"))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteNoteValid(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Temp", "", nil)

	r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", n.ID), nil)
	w := httptest.NewRecorder()
	s.deleteNote(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Confirm it's gone
	r2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d", n.ID), nil)
	w2 := httptest.NewRecorder()
	s.getNote(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", w2.Code)
	}
}

func TestDeleteNoteNotFound(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/notes/9999", nil)
	w := httptest.NewRecorder()
	s.deleteNote(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetNoteHistoryValid(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Note", "v1", nil)

	// Add a second update
	payload := `{"title":"Note","body":"v2"}`
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d", w.Code)
	}

	r2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d/history", n.ID), nil)
	w2 := httptest.NewRecorder()
	s.getNoteHistory(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("history: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var updates []NoteUpdate
	if err := json.NewDecoder(w2.Body).Decode(&updates); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(updates) < 2 {
		t.Errorf("expected at least 2 history entries, got %d", len(updates))
	}
}

func TestGetNoteHistoryNotFound(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/notes/9999/history", nil)
	w := httptest.NewRecorder()
	s.getNoteHistory(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetNoteHistoryEmpty(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Note", "", nil)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d/history", n.ID), nil)
	w := httptest.NewRecorder()
	s.getNoteHistory(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var updates []NoteUpdate
	if err := json.NewDecoder(w.Body).Decode(&updates); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(updates) != 1 {
		t.Errorf("expected 1 initial update, got %d", len(updates))
	}
}
