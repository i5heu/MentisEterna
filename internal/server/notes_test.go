package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
)

// mockEmbedder is a deterministic Embedder for tests.
// It maps text -> a fixed-dimension vector so we can verify the full
// search pipeline without relying on a running LocalAI instance.
// Uses dimension 2560 to match typical embedding model output.
type mockEmbedder struct {
	mu      sync.Mutex
	vectors map[string][]float64
}

const mockEmbeddingDim = 2560

func newMockEmbedder() *mockEmbedder {
	return &mockEmbedder{
		vectors: make(map[string][]float64),
	}
}

func (m *mockEmbedder) GenerateEmbedding(text string) ([]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.vectors[text]; ok {
		return v, nil
	}
	// Derive a deterministic vector from the text hash.
	// Two identical texts produce identical vectors (cosine distance ≈ 0).
	// Two different texts produce different vectors.
	vec := m.deriveVector(text)
	m.vectors[text] = vec
	return vec, nil
}

// deriveVector produces a deterministic 2048-dim vector for a given text.
// Uses FNV-1a inspired hashing to fill all dimensions.
func (m *mockEmbedder) deriveVector(text string) []float64 {
	vec := make([]float64, mockEmbeddingDim)
	// Use multiple seed offsets to fill all 2048 dimensions.
	for d := 0; d < mockEmbeddingDim; d++ {
		// Combine text hash with dimension index for per-dimension variation.
		var h uint64 = 14695981039346656037 // FNV offset basis
		for _, b := range text {
			h ^= uint64(b)
			h *= 1099511628211 // FNV prime
		}
		h ^= uint64(d) * 2654435761 // mix in dimension index
		h *= 1099511628211
		// Map to [-1, 1] range.
		vec[d] = float64(int64(h>>1))/float64(1<<62) - 1.0
	}
	return vec
}

// newTestServerWithEmbedder creates a Server with a mock embedder and
// VSS enabled. Uses the default vss_notes table created by migration.
func newTestServerWithEmbedder(t *testing.T) *Server {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// If vector search is unavailable, we can't test search.
	if !d.VSSAvailable() {
		t.Skip("sqlite-vec extension not available (vec0 missing)")
	}

	m := newMockEmbedder()
	return New(d, ":0", m, nil, nil, nil)
}

// helperCreateNoteSync creates a note and ensures the embedding is
// stored before returning. It calls syncEmbeddingTask synchronously.
func helperCreateNoteSync(t *testing.T, s *Server, title, body string, parentID *int64) NoteDetail {
	t.Helper()
	n := helperCreateNote(t, s, title, body, parentID)
	helperSyncSearchIndex(t, s, n.ID)
	return n
}

func helperSyncSearchIndex(t *testing.T, s *Server, noteID int64) {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{
		"note_id": noteID,
	})
	if _, err := s.syncEmbeddingTask(s.db.DB, payload); err != nil {
		t.Fatalf("syncEmbeddingTask(%d): %v", noteID, err)
	}
}

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

func helperCreateNote(t *testing.T, s *Server, title, body string, parentID *int64) NoteDetail {
	t.Helper()
	var payload string
	if parentID != nil {
		payload = fmt.Sprintf(`{"title":%q,"body":%q,"parent_id":%d}`, title, body, *parentID)
	} else {
		payload = fmt.Sprintf(`{"title":%q,"body":%q}`, title, body)
	}
	return helperCreateNoteRaw(t, s, payload)
}

func helperCreateNoteRaw(t *testing.T, s *Server, payload string) NoteDetail {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("createNote: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var n NoteDetail
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
	var notes []NoteSummary
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
	var notes []NoteSummary
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
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for blank title (falls back to Untitled), got %d: %s", w.Code, w.Body.String())
	}
	var n NoteDetail
	if err := json.NewDecoder(w.Body).Decode(&n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.Title != "Untitled" {
		t.Errorf("expected title 'Untitled', got %q", n.Title)
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
	var n NoteDetail
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
	var updated NoteDetail
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
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for blank title (falls back to Untitled), got %d: %s", w.Code, w.Body.String())
	}
	var updated2 NoteDetail
	if err := json.NewDecoder(w.Body).Decode(&updated2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated2.Title != "Untitled" {
		t.Errorf("expected title 'Untitled', got %q", updated2.Title)
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

// ---- Semantic Search Tests ----

// TestSearchFindsExactMatch verifies that a note with "Hello World" is found
// when searching for "Hello World". This is the core regression test for the
// bug where semantic search always returned empty results.
func TestSearchFindsExactMatch(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	// Create a note with "Hello World" and ensure its embedding is stored.
	n := helperCreateNoteSync(t, s, "Hello Note", "Hello World", nil)

	// Search for "Hello World"
	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=Hello+World", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("searchNotes: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode search results: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("search returned no results for 'Hello World', expected at least 1")
	}

	found := false
	for _, sr := range results {
		if sr.ID == n.ID {
			found = true
			if sr.Body != "Hello World" {
				t.Errorf("expected body 'Hello World', got %q", sr.Body)
			}
			break
		}
	}
	if !found {
		t.Errorf("note ID %d not found in search results", n.ID)
	}
}

// TestSearchExactQueryReturnsTopResult verifies that when searching for the
// exact paragraph text of a note, that note appears as the top result.
func TestSearchExactQueryReturnsTopResult(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	n := helperCreateNoteSync(t, s, "Exact", "unique phrase here", nil)

	// The body paragraph is indexed as its own chunk, so searching for the same
	// paragraph should produce an exact vector match.
	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=unique+phrase+here", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("searchNotes: expected 200, got %d", w.Code)
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// The top result should be our note.
	if results[0].ID != n.ID {
		t.Errorf("expected top result ID=%d, got ID=%d", n.ID, results[0].ID)
	}

}

// TestSearchEmptyQueryReturns400 verifies that the search endpoint requires
// a non-empty query parameter.
func TestSearchEmptyQueryReturns400(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty query, got %d", w.Code)
	}
}

// TestSearchMissingQueryReturns400 verifies that the search endpoint requires
// the 'q' query parameter.
func TestSearchMissingQueryReturns400(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	r := httptest.NewRequest(http.MethodGet, "/notes/search", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing query, got %d", w.Code)
	}
}

// TestSearchReturns500WhenEmbeddingUnavailable verifies that search fails
// loudly when semantic search infrastructure is unavailable.
func TestSearchReturns500WhenEmbeddingUnavailable(t *testing.T) {
	s := newTestServer(t) // llm is nil

	n := helperCreateNote(t, s, "Some Note", "some text", nil)
	_ = n

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=text", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when semantic search is unavailable, got %d", w.Code)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "semantic search system error") {
		t.Errorf("expected semantic search error message, got %q", w.Body.String())
	}
}

// TestSearchMultipleNotesOrdersByDistance verifies that when searching across
// multiple notes, results are ordered by distance ascending (closest first).
func TestSearchMultipleNotesOrdersByDistance(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	// Create several notes with different content.
	_ = helperCreateNoteSync(t, s, "Python", "Python is a programming language", nil)
	_ = helperCreateNoteSync(t, s, "Dog", "Dogs are loyal pets", nil)
	_ = helperCreateNoteSync(t, s, "Python Tips", "Advanced Python programming techniques", nil)

	// Search for "Python programming" — n1 and n3 should appear before n2.
	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=Python+programming", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("searchNotes: expected 200, got %d", w.Code)
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Verify ordering by distance.
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Errorf("results not sorted by distance: result[%d]=%f < result[%d]=%f",
				i, results[i].Distance, i-1, results[i-1].Distance)
		}
	}
}

// TestSearchAfterUpdateUsesNewBody verifies that updating a note's body
// changes the embedding, so the updated content is searchable.
func TestSearchAfterUpdateUsesNewBody(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	n := helperCreateNoteSync(t, s, "Doc", "initial content about cars", nil)

	// Update to completely different content.
	payload := `{"title":"Doc","body":"updated content about airplanes"}`
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d", w.Code)
	}
	// Call the embedding task synchronously to be certain the embedding is stored.
	payload2, _ := json.Marshal(map[string]interface{}{
		"note_id": n.ID,
		"title":   "Doc",
		"body":    "updated content about airplanes",
	})
	_, err := s.syncEmbeddingTask(s.db.DB, payload2)
	if err != nil {
		t.Fatalf("syncEmbeddingTask: %v", err)
	}

	// Search for "airplanes" should now find the note.
	r2 := httptest.NewRequest(http.MethodGet, "/notes/search?q=airplanes", nil)
	w2 := httptest.NewRecorder()
	s.searchNotes(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d", w2.Code)
	}

	var results []SearchResult
	if err := json.NewDecoder(w2.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	found := false
	for _, sr := range results {
		if sr.ID == n.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("note ID %d not found after updating body to 'airplanes'", n.ID)
	}
}

// TestSearchAfterDeleteDoesNotReturnNote verifies that a deleted note's
// embedding is also removed, so it no longer shows up in search.
func TestSearchAfterDeleteDoesNotReturnNote(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	n := helperCreateNoteSync(t, s, "Temp", "some unique content to delete", nil)

	// Verify it's found before delete.
	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=unique+content", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)
	var results []SearchResult
	json.NewDecoder(w.Body).Decode(&results)

	found := false
	for _, sr := range results {
		if sr.ID == n.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("prerequisite: note should be found before deletion")
	}

	// Delete it.
	rd := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", n.ID), nil)
	wd := httptest.NewRecorder()
	s.deleteNote(wd, rd)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", wd.Code)
	}

	// Search again — should not find it.
	r2 := httptest.NewRequest(http.MethodGet, "/notes/search?q=unique+content", nil)
	w2 := httptest.NewRecorder()
	s.searchNotes(w2, r2)

	var results2 []SearchResult
	if err := json.NewDecoder(w2.Body).Decode(&results2); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, sr := range results2 {
		if sr.ID == n.ID {
			t.Errorf("note ID %d should not appear in search results after deletion", n.ID)
		}
	}
}

// TestSearchResultContainsAllFields verifies that search results include
// all necessary Note fields plus the Distance field.
func TestSearchResultContainsAllFields(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	_ = helperCreateNoteSync(t, s, "Check Fields", "field check body", nil)

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=field+check", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d", w.Code)
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	sr := results[0]
	if sr.ID == 0 {
		t.Error("ID is zero")
	}
	if sr.Title == "" {
		t.Error("Title is empty")
	}
	if sr.Body == "" {
		t.Error("Body is empty")
	}
	if sr.CreatedAt == "" {
		t.Error("CreatedAt is empty")
	}
	if sr.UpdatedAt == "" {
		t.Error("UpdatedAt is empty")
	}
	// Distance is always present (can be 0 for exact match).
	_ = sr.Distance
}

func TestSearchIndexesBodyParagraphsSeparately(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	n := helperCreateNoteSync(t, s, "Paragraph Note", "First paragraph\n\nSecond paragraph", nil)

	var chunkCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM note_search_chunks WHERE note_id = ? AND field = 'body'`, n.ID).Scan(&chunkCount); err != nil {
		t.Fatalf("count body chunks: %v", err)
	}
	if chunkCount != 2 {
		t.Fatalf("expected 2 body chunks, got %d", chunkCount)
	}

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=Second+paragraph", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("searchNotes: expected 200, got %d", w.Code)
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) == 0 || results[0].ID != n.ID {
		t.Fatalf("expected paragraph note %d to be top result, got %+v", n.ID, results)
	}
}

func TestSearchFindsTitlePathAndTags(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	root := helperCreateNoteSync(t, s, "Projects Hub", "Root note", nil)
	child := helperCreateNoteRaw(t, s, fmt.Sprintf(
		`{"title":"Release Checklist","body":"Deploy checklist","parent_id":%d,"tags":["ops","urgent"]}`,
		root.ID,
	))
	helperSyncSearchIndex(t, s, child.ID)

	assertFound := func(query string) SearchResult {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/notes/search?q="+query, nil)
		w := httptest.NewRecorder()
		s.searchNotes(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("search %q: expected 200, got %d", query, w.Code)
		}
		var results []SearchResult
		if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
			t.Fatalf("decode %q: %v", query, err)
		}
		for _, sr := range results {
			if sr.ID == child.ID {
				return sr
			}
		}
		t.Fatalf("child note %d not found for query %q", child.ID, query)
		return SearchResult{}
	}

	assertFound("Release+Checklist")
	pathHit := assertFound("Projects+Hub")
	tagHit := assertFound("urgent")

	if pathHit.Path == "" || !strings.Contains(pathHit.Path, "Projects Hub") {
		t.Fatalf("expected populated path containing ancestor title, got %q", pathHit.Path)
	}
	if len(tagHit.Tags) == 0 || tagHit.Tags[0] == "" {
		t.Fatalf("expected populated tags in search result, got %+v", tagHit.Tags)
	}
	foundUrgent := false
	for _, tag := range tagHit.Tags {
		if tag == "urgent" {
			foundUrgent = true
			break
		}
	}
	if !foundUrgent {
		t.Fatalf("expected urgent tag in search result, got %+v", tagHit.Tags)
	}
}

func TestSearchTypeFilter(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	standard := helperCreateNoteSync(t, s, "Standard Match", "shared filter phrase", nil)
	recipe := helperCreateNoteRaw(t, s, `{"title":"Recipe Match","body":"shared filter phrase","type":"custom_type"}`)
	helperSyncSearchIndex(t, s, recipe.ID)

	searchIDs := func(url string) []int64 {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		s.searchNotes(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("search %q: expected 200, got %d", url, w.Code)
		}
		var results []SearchResult
		if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
			t.Fatalf("decode %q: %v", url, err)
		}
		ids := make([]int64, 0, len(results))
		for _, sr := range results {
			ids = append(ids, sr.ID)
		}
		return ids
	}

	standardOnly := searchIDs("/notes/search?q=shared+filter+phrase&types=standard")
	for _, id := range standardOnly {
		if id == recipe.ID {
			t.Fatalf("recipe note %d should be excluded from standard filter", recipe.ID)
		}
	}
	foundStandard := false
	for _, id := range standardOnly {
		if id == standard.ID {
			foundStandard = true
			break
		}
	}
	if !foundStandard {
		t.Fatalf("standard note %d not found in standard-only results: %+v", standard.ID, standardOnly)
	}

	recipeOnly := searchIDs("/notes/search?q=shared+filter+phrase&types=custom_type")
	for _, id := range recipeOnly {
		if id == standard.ID {
			t.Fatalf("standard note %d should be excluded from recipe filter", standard.ID)
		}
	}
	foundRecipe := false
	for _, id := range recipeOnly {
		if id == recipe.ID {
			foundRecipe = true
			break
		}
	}
	if !foundRecipe {
		t.Fatalf("recipe note %d not found in recipe-only results: %+v", recipe.ID, recipeOnly)
	}
}

type searchStreamSectionEnvelope struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Results []SearchResult `json:"results"`
}

type searchStreamEventEnvelope struct {
	Type    string                       `json:"type"`
	Phase   string                       `json:"phase"`
	Message string                       `json:"message"`
	Total   int                          `json:"total"`
	Section *searchStreamSectionEnvelope `json:"section"`
}

func decodeSearchStreamEvents(t *testing.T, body string) []searchStreamEventEnvelope {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	events := make([]searchStreamEventEnvelope, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event searchStreamEventEnvelope
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode stream line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func TestSearchStreamEmitsLiteralSectionsBeforeSemantic(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	tagged := helperCreateNoteRaw(t, s, `{"title":"Ops Review","body":"Operations checklist","tags":["urgent"]}`)
	helperSyncSearchIndex(t, s, tagged.ID)
	semantic := helperCreateNoteSync(t, s, "Release Follow Up", "urgent release follow up", nil)

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=urgent&stream=1", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search stream: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	events := decodeSearchStreamEvents(t, w.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected multiple stream events, got %+v", events)
	}

	sectionKeys := make([]string, 0, len(events))
	semanticSection := -1
	for i, event := range events {
		if event.Section == nil {
			continue
		}
		sectionKeys = append(sectionKeys, event.Section.Key)
		if event.Section.Key == "semantic" {
			semanticSection = i
		}
	}
	if len(sectionKeys) == 0 || sectionKeys[0] != "tags" {
		t.Fatalf("expected first search section to be tags, got %+v", sectionKeys)
	}
	if semanticSection == -1 {
		t.Fatalf("expected a semantic section, got %+v", sectionKeys)
	}

	tagFound := false
	semanticFound := false
	for i, event := range events {
		if event.Section == nil {
			continue
		}
		for _, result := range event.Section.Results {
			if event.Section.Key == "tags" && result.ID == tagged.ID {
				tagFound = true
			}
			if i == semanticSection && result.ID == semantic.ID {
				semanticFound = true
			}
		}
	}
	if !tagFound {
		t.Fatalf("expected tagged note %d in tag section", tagged.ID)
	}
	if !semanticFound {
		t.Fatalf("expected semantic note %d in semantic section", semantic.ID)
	}
}

func TestSearchStreamReturnsExactMatchesWhenEmbeddingUnavailable(t *testing.T) {
	s := newTestServer(t)

	note := helperCreateNote(t, s, "Urgent Planning", "plain body", nil)

	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=urgent&stream=1", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search stream without embeddings: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	events := decodeSearchStreamEvents(t, w.Body.String())
	foundTitleSection := false
	foundNote := false
	foundDone := false
	for _, event := range events {
		if event.Type == "done" {
			foundDone = true
		}
		if event.Section == nil || event.Section.Key != "titles" {
			continue
		}
		foundTitleSection = true
		for _, result := range event.Section.Results {
			if result.ID == note.ID {
				foundNote = true
			}
		}
	}
	if !foundTitleSection {
		t.Fatalf("expected a title section in streamed results, got %+v", events)
	}
	if !foundNote {
		t.Fatalf("expected note %d in streamed title results", note.ID)
	}
	if !foundDone {
		t.Fatalf("expected a done event in streamed results, got %+v", events)
	}
}

// TestSearchFindsNoteByOCRText verifies that OCR text from uploaded files
// is indexed and searchable via vss_files_ocr.
func TestSearchFindsNoteByOCRText(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	// Create a note.
	n := helperCreateNoteSync(t, s, "Photo Note", "A note with a scanned photo", nil)

	// Create a file referencing this note.
	res, err := s.db.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (?, 'ocr-search-key', 'scan.png', 'image/png', 100,
		        'aa', 'bb', x'0001', x'0002')
	`, n.ID)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Create a files_refs row linking the file to the note.
	_, err = s.db.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, n.ID, fileID)
	if err != nil {
		t.Fatalf("insert ref: %v", err)
	}

	// Store OCR result for the file.
	ocrText := "Invoice #12345 from Acme Corp"
	_, err = s.db.Exec(`INSERT INTO files_ocr (file_id, ocr_text, model) VALUES (?, ?, 'glm-ocr:latest')`, fileID, ocrText)
	if err != nil {
		t.Fatalf("insert ocr: %v", err)
	}

	// Generate embedding for the OCR text and insert into vss_files_ocr.
	vec, err := s.llm.GenerateEmbedding(llm.TruncateForEmbedding(ocrText))
	if err != nil {
		t.Fatalf("generate ocr embedding: %v", err)
	}
	vecJSON := llm.EmbeddingToJSON(vec)
	_, err = s.db.Exec(`DELETE FROM vss_files_ocr WHERE rowid = ?`, fileID)
	if err != nil {
		t.Fatalf("delete old ocr embedding: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO vss_files_ocr(rowid, ocr_embedding) VALUES (?, ?)`, fileID, vecJSON)
	if err != nil {
		t.Fatalf("insert ocr embedding: %v", err)
	}

	// Search for text that only appears in the OCR content.
	r := httptest.NewRequest(http.MethodGet, "/notes/search?q=Acme+Corp+invoice", nil)
	w := httptest.NewRecorder()
	s.searchNotes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []SearchResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The note should appear in results because the OCR text matches.
	found := false
	for _, sr := range results {
		if sr.ID == n.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected note %d to appear in search results for OCR text, got %d results", n.ID, len(results))
	}
}

// --- Note lifecycle + file retention tests ---

// newTestServerWithMediaForNotes creates a server with media for note lifecycle tests.
func newTestServerWithMediaForNotes(t *testing.T) *Server {
	t.Helper()
	s, _ := newTestServerWithMedia(t)
	return s
}

func TestUpdateNoteConvertsPendingInlineToInlineRef(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	_, token := createTestNoteWithSession(t, s)

	// Create a note
	body := `{"title":"test","body":"initial"}`
	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.createNote(w, req)

	var noteID int64
	s.db.QueryRow(`SELECT id FROM notes ORDER BY id DESC LIMIT 1`).Scan(&noteID)

	// Upload an inline file
	ct, mpBody := multipartBody("file", "ref-me.txt", "text/plain", []byte("ref me"))
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files/inline", noteID), mpBody)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", ct)
	w2 := httptest.NewRecorder()
	s.uploadInlineFile(w2, req2)

	var fileID int64
	s.db.QueryRow(`SELECT id FROM files WHERE filename = 'ref-me.txt'`).Scan(&fileID)

	// Now update the note to reference the file in its body
	updatedBody := fmt.Sprintf(`{"title":"test","body":"see [file](/file/%d/%d)"}`, noteID, fileID)
	req3 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", noteID), bytes.NewReader([]byte(updatedBody)))
	req3.Header.Set("Authorization", "Bearer "+token)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	s.updateNote(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", w3.Code, w3.Body.String())
	}

	// Verify inline ref exists and pending state is cleared
	var refCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ? AND ref_kind = 'inline'`,
		noteID, fileID).Scan(&refCount)
	if refCount != 1 {
		t.Errorf("expected 1 inline ref, got %d", refCount)
	}

	var pendingNoteID *int64
	s.db.QueryRow(`SELECT pending_inline_note_id FROM files WHERE id = ?`, fileID).Scan(&pendingNoteID)
	if pendingNoteID != nil {
		t.Error("expected pending_inline_note_id to be cleared")
	}
}

func TestUpdateNoteDeletesUnusedPendingInlineFiles(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	_, token := createTestNoteWithSession(t, s)

	// Create a note
	body := `{"title":"test","body":"initial"}`
	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.createNote(w, req)

	var noteID int64
	s.db.QueryRow(`SELECT id FROM notes ORDER BY id DESC LIMIT 1`).Scan(&noteID)

	// Upload an inline file that will NOT be referenced
	ct, mpBody := multipartBody("file", "unused.txt", "text/plain", []byte("unused"))
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files/inline", noteID), mpBody)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", ct)
	w2 := httptest.NewRecorder()
	s.uploadInlineFile(w2, req2)

	var fileID int64
	s.db.QueryRow(`SELECT id FROM files WHERE filename = 'unused.txt'`).Scan(&fileID)

	// Update the note WITHOUT referencing the file
	updatedBody := `{"title":"test","body":"no file references here"}`
	req3 := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", noteID), bytes.NewReader([]byte(updatedBody)))
	req3.Header.Set("Authorization", "Bearer "+token)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	s.updateNote(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", w3.Code, w3.Body.String())
	}

	// File should be soft-deleted
	var deletedAt *string
	s.db.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, fileID).Scan(&deletedAt)
	if deletedAt == nil {
		t.Error("expected unreferenced pending file to be soft-deleted")
	}
}

func TestDeleteNoteKeepsFileWhenAnotherNoteStillReferencesIt(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	_, token := createTestNoteWithSession(t, s)

	// Create note A
	reqA := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(`{"title":"note A","body":"a"}`)))
	reqA.Header.Set("Authorization", "Bearer "+token)
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	s.createNote(wA, reqA)
	var noteAID int64
	s.db.QueryRow(`SELECT id FROM notes WHERE title = 'note A'`).Scan(&noteAID)

	// Create note B
	reqB := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(`{"title":"note B","body":"b"}`)))
	reqB.Header.Set("Authorization", "Bearer "+token)
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	s.createNote(wB, reqB)
	var noteBID int64
	s.db.QueryRow(`SELECT id FROM notes WHERE title = 'note B'`).Scan(&noteBID)

	// Upload file to note A
	ct, mpBody := multipartBody("file", "shared.txt", "text/plain", []byte("shared"))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteAID), mpBody)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)

	var fileID int64
	s.db.QueryRow(`SELECT id FROM files WHERE filename = 'shared.txt'`).Scan(&fileID)

	// Also reference the file from note B
	s.db.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, noteBID, fileID)

	// Delete note A
	req2 := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", noteAID), nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	s.deleteNote(w2, req2)

	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w2.Code)
	}

	// File should NOT be soft-deleted (note B still references it)
	var deletedAt *string
	s.db.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, fileID).Scan(&deletedAt)
	if deletedAt != nil {
		t.Error("file should not be deleted while note B still references it")
	}

	// Note B's ref should still exist
	var refCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE note_id = ? AND file_id = ?`, noteBID, fileID).Scan(&refCount)
	if refCount != 1 {
		t.Errorf("expected note B ref to remain, got %d", refCount)
	}
}

func TestDeleteNoteDeletesFileWhenLastRefDisappears(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	_, token := createTestNoteWithSession(t, s)

	// Create a note
	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte(`{"title":"lonely","body":"lonely"}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.createNote(w, req)
	var noteID int64
	s.db.QueryRow(`SELECT id FROM notes WHERE title = 'lonely'`).Scan(&noteID)

	// Upload file
	ct, mpBody := multipartBody("file", "lonely.txt", "text/plain", []byte("lonely"))
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), mpBody)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", ct)
	w2 := httptest.NewRecorder()
	s.uploadAttachment(w2, req2)

	var fileID int64
	s.db.QueryRow(`SELECT id FROM files WHERE filename = 'lonely.txt'`).Scan(&fileID)

	// Delete the note (last reference)
	req3 := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", noteID), nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	w3 := httptest.NewRecorder()
	s.deleteNote(w3, req3)

	if w3.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w3.Code)
	}

	// File should be soft-deleted (no refs remaining)
	var deletedAt *string
	s.db.QueryRow(`SELECT deleted_at FROM files WHERE id = ?`, fileID).Scan(&deletedAt)
	if deletedAt == nil {
		t.Error("file should be soft-deleted when last ref disappears")
	}
}
