package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// Singleton note types (one per app, e.g. llm_inferencer): the server must
// reject creating a second note of the type and switching another note to
// it, while editing the existing singleton note stays legal.

// helperCreateNoteRawResp POSTs a raw create payload and returns the raw
// response (no status assertion — used for the rejection cases).
func helperCreateNoteRawResp(t *testing.T, s *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.createNote(w, r)
	return w
}

func TestSingletonNoteTypeCreateRejected(t *testing.T) {
	s := newTestServer(t)

	first := helperCreateNoteRaw(t, s, `{"title":"The LLM","body":"","type":"llm_inferencer"}`)
	if first.Type != "llm_inferencer" {
		t.Fatalf("first note type = %q", first.Type)
	}

	w := helperCreateNoteRawResp(t, s, `{"title":"Second LLM","body":"","type":"llm_inferencer"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a second llm_inferencer note, got %d: %s", w.Code, w.Body.String())
	}
	if msg := w.Body.String(); !strings.Contains(msg, "LLM Inferencer") {
		t.Errorf("error should name the note type label, got: %s", msg)
	}
}

func TestSingletonNoteTypeUpdateRejected(t *testing.T) {
	s := newTestServer(t)
	// Plugin schemas are created by Start(), which unit tests skip.
	if err := notetype.Registry["llm_inferencer"].InitSchema(s.db.DB); err != nil {
		t.Fatalf("llm_inferencer schema: %v", err)
	}
	singleton := helperCreateNoteRaw(t, s, `{"title":"The LLM","body":"","type":"llm_inferencer"}`)
	victim := helperCreateNoteRaw(t, s, `{"title":"Plain","body":""}`)

	// Switching another note to the singleton type → 400.
	payload := fmt.Sprintf(`{"title":"Impostor","body":"","type":"llm_inferencer"}`)
	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", victim.ID), strings.NewReader(payload))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 switching a note to llm_inferencer, got %d: %s", w.Code, w.Body.String())
	}

	// Editing the existing singleton note (keeping its type) stays legal.
	payload = fmt.Sprintf(`{"title":"The LLM, edited","body":"","type":"llm_inferencer","custom_data":{"provider":"mistral"}}`)
	r = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", singleton.ID), strings.NewReader(payload))
	w = httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("editing the singleton itself must stay allowed, got %d: %s", w.Code, w.Body.String())
	}

	// Turning the singleton into a standard note frees the slot again.
	payload = fmt.Sprintf(`{"title":"Downgraded","body":"","type":"standard"}`)
	r = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", singleton.ID), strings.NewReader(payload))
	w = httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("downgrading the singleton failed: %d: %s", w.Code, w.Body.String())
	}
	if w2 := helperCreateNoteRawResp(t, s, `{"title":"New LLM","body":"","type":"llm_inferencer"}`); w2.Code != http.StatusCreated {
		t.Fatalf("after deleting the old one a new llm_inferencer must be creatable, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestSingletonFlagInNoteTypesCatalog(t *testing.T) {
	s := newTestServer(t)

	r := httptest.NewRequest(http.MethodGet, "/note-types", nil)
	w := httptest.NewRecorder()
	s.handleNoteTypes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("note-types: %d", w.Code)
	}
	var catalog []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	found := false
	for _, m := range catalog {
		if m["id"] != "llm_inferencer" {
			continue
		}
		found = true
		if m["singleton"] != true {
			t.Errorf("llm_inferencer manifest must expose singleton=true, got %v", m["singleton"])
		}
	}
	if !found {
		t.Fatal("llm_inferencer missing from the catalog")
	}

	// Only llm_inferencer declares singleton; other types stay creatable.
	rssTwo := 0
	for _, m := range catalog {
		if m["id"] == "rss" {
			rssTwo++
		}
	}
	if rssTwo != 1 {
		t.Error("rss manifest missing from catalog")
	}
	a := helperCreateNoteRaw(t, s, `{"title":"RSS One","body":"","type":"rss"}`)
	b := helperCreateNoteRaw(t, s, `{"title":"RSS Two","body":"","type":"rss"}`)
	if a.ID == 0 || b.ID == 0 || a.ID == b.ID {
		t.Error("non-singleton types must allow multiple notes")
	}
}
