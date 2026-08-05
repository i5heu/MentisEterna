package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i5heu/MentisEterna/internal/media"
)

type mockSTT struct{ text string }

func (m mockSTT) RunSTT(_ []byte, _ string) (string, error) { return m.text, nil }

func ingestMultipartBody(filename string, data []byte, extra map[string]string) (string, *bytes.Buffer) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", filename)
	part.Write(data)
	for k, v := range extra {
		mw.WriteField(k, v)
	}
	mw.Close()
	return mw.FormDataContentType(), &buf
}

func TestHandleAudioIngestUnauthorized(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleAudioIngestNotConfigured(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = ""
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleAudioIngestRejectsNonAudio(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("notes.txt", []byte("not audio"), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", w.Code)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 notes created, got %d", count)
	}
}

func TestHandleAudioIngestSuccess(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail     `json:"note"`
		File media.NoteFile `json:"file"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Note.ID <= 0 {
		t.Fatalf("expected note.id > 0, got %d", resp.Note.ID)
	}
	if !resp.File.IsAudio {
		t.Fatalf("expected file.is_audio true, got false")
	}
	if resp.File.MimeType != "audio/mp4" {
		t.Fatalf("expected mime audio/mp4, got %q", resp.File.MimeType)
	}
}

func TestIngestSTTToNoteTaskAppendsTranscript(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail     `json:"note"`
		File media.NoteFile `json:"file"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d}`, resp.File.ID, resp.Note.ID))
	if _, err := s.ingestSTTToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestSTTToNoteTask: %v", err)
	}

	var bodyText string
	if err := s.db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, resp.Note.ID).Scan(&bodyText); err != nil {
		t.Fatalf("query body: %v", err)
	}
	if bodyText != "hello world" {
		t.Fatalf("expected body 'hello world', got %q", bodyText)
	}
}

func fakeM4A() []byte {
	return []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
}
