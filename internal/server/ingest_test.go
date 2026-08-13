package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/media"
)

type mockSTT struct {
	text string
	err  error
}

func (m mockSTT) RunSTT(_ []byte, _ string) (string, error) { return m.text, m.err }

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

func TestHandleAudioIngestAcceptsWebM(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.webm", fakeWebM(), nil)
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
	if resp.File.MimeType != "audio/webm" {
		t.Fatalf("expected mime audio/webm (normalized from video/webm), got %q", resp.File.MimeType)
	}
}

func TestHandleIngestToken(t *testing.T) {
	s := newTestServer(t)
	s.ingestToken = "secret"
	token := createTestSession(t, s)

	handler := s.requireAuth(http.HandlerFunc(s.handleIngestToken))
	r := httptest.NewRequest(http.MethodGet, "/ingest/token", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token != "secret" {
		t.Fatalf("expected token %q, got %q", "secret", resp.Token)
	}
}

func TestHandleIngestTokenNotConfigured(t *testing.T) {
	s := newTestServer(t)
	s.ingestToken = ""
	token := createTestSession(t, s)

	handler := s.requireAuth(http.HandlerFunc(s.handleIngestToken))
	r := httptest.NewRequest(http.MethodGet, "/ingest/token", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
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

func TestIngestSTTToNoteTaskGeneratesTitleAndForcesTag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}
	gen := &stubAutoTagGenerator{
		title:      "generated-title",
		suggestion: llm.AutoTagSuggestion{ExistingTags: []string{"project"}, NewTags: []string{"voice memo"}},
	}
	s.titleClient = gen
	s.autoTagger = gen

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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d}`, resp.File.ID, noteID))
	if _, err := s.ingestSTTToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestSTTToNoteTask: %v", err)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "generated-title" {
		t.Fatalf("expected title 'generated-title', got %q", title)
	}

	if gen.lastInput.Title != "generated-title" {
		t.Fatalf("expected auto-tag input title 'generated-title' (title runs before auto tags), got %q", gen.lastInput.Title)
	}

	manualTags, err := loadTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load tags: %v", err)
	}
	if !reflect.DeepEqual(manualTags, []string{"audio-note"}) {
		t.Fatalf("expected manual tags [audio-note], got %v", manualTags)
	}

	autoTags, err := loadAutoTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load auto tags: %v", err)
	}
	if !reflect.DeepEqual(autoTags, []string{"project", "voice memo"}) {
		t.Fatalf("expected auto tags [project voice memo], got %v", autoTags)
	}
}

func TestIngestSTTToNoteTaskSTTFailureSkipsTitleAndTag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{err: errors.New("boom")}
	gen := &stubAutoTagGenerator{}
	s.titleClient = gen
	s.autoTagger = gen

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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d}`, resp.File.ID, noteID))
	msg, err := s.ingestSTTToNoteTask(s.db.DB, payload)
	if err != nil {
		t.Fatalf("ingestSTTToNoteTask: %v", err)
	}
	if !strings.Contains(msg, "completed with error") {
		t.Fatalf("expected message mentioning error, got %q", msg)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "recording" {
		t.Fatalf("expected filename-derived title 'recording', got %q", title)
	}

	manualTags, err := loadTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load tags: %v", err)
	}
	if len(manualTags) != 0 {
		t.Fatalf("expected no manual tags on STT failure, got %v", manualTags)
	}
}

func fakeM4A() []byte {
	return []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}
}

// fakeWebM is an EBML header (the container MediaRecorder emits); Go's
// http.DetectContentType labels it video/webm even though it carries only
// audio. handleAudioIngest normalizes it to audio/webm.
func fakeWebM() []byte {
	return []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
}

func TestHandleAudioIngestParentFromPath(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	res, err := s.db.Exec(`INSERT INTO notes (title) VALUES ('parent')`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("parent id: %v", err)
	}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio/"+fmt.Sprint(parentID), body)
	req.SetPathValue("parent_id", fmt.Sprint(parentID))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Note.ParentID == nil || *resp.Note.ParentID != parentID {
		t.Fatalf("expected parent_id %d, got %v", parentID, resp.Note.ParentID)
	}
}

func TestHandleAudioIngestDatePathAccepted(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	res, err := s.db.Exec(`INSERT INTO notes (title) VALUES ('parent')`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("parent id: %v", err)
	}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio/"+fmt.Sprint(parentID)+"/date", body)
	req.SetPathValue("parent_id", fmt.Sprint(parentID))
	req.SetPathValue("flag", "date")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Note.ParentID == nil || *resp.Note.ParentID != parentID {
		t.Fatalf("expected parent_id %d, got %v", parentID, resp.Note.ParentID)
	}
}

func TestHandleAudioIngestRejectsInvalidPathParent(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio/abc", body)
	req.SetPathValue("parent_id", "abc")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAudioIngestRejectsUnknownFlag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}

	ct, body := ingestMultipartBody("recording.m4a", fakeM4A(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/audio/1/foo", body)
	req.SetPathValue("parent_id", "1")
	req.SetPathValue("flag", "foo")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleAudioIngest(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIngestSTTToNoteTaskPrependsDateToTitle(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.sttClient = mockSTT{text: "hello world"}
	gen := &stubAutoTagGenerator{
		title:      "generated-title",
		suggestion: llm.AutoTagSuggestion{ExistingTags: []string{"project"}, NewTags: []string{"voice memo"}},
	}
	s.titleClient = gen
	s.autoTagger = gen

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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d,"date_prefix":"05-08-26"}`, resp.File.ID, noteID))
	if _, err := s.ingestSTTToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestSTTToNoteTask: %v", err)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "05-08-26: generated-title" {
		t.Fatalf("expected title '05-08-26: generated-title', got %q", title)
	}

	if gen.lastInput.Title != "05-08-26: generated-title" {
		t.Fatalf("expected auto-tag input title '05-08-26: generated-title' (prefixed title runs before auto tags), got %q", gen.lastInput.Title)
	}
}

type mockOCR struct {
	text string
	err  error
}

func (m mockOCR) RunOCR(_ []byte) (string, error) { return m.text, m.err }

func fakePNG() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
}

func TestHandleHandwrittenIngestUnauthorized(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleHandwrittenIngestNotConfigured(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = ""
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleHandwrittenIngestOCRNotConfigured(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 notes created, got %d", count)
	}
}

func TestHandleHandwrittenIngestRejectsNonImage(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("notes.txt", []byte("not an image"), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)

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

func TestHandleHandwrittenIngestSuccess(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)

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
	if !resp.File.IsImage {
		t.Fatalf("expected file.is_image true, got false")
	}
	if resp.File.MimeType != "image/png" {
		t.Fatalf("expected mime image/png, got %q", resp.File.MimeType)
	}
}

func TestIngestOCRToNoteTaskAppendsText(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
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
	if _, err := s.ingestOCRToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestOCRToNoteTask: %v", err)
	}

	var bodyText string
	if err := s.db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, resp.Note.ID).Scan(&bodyText); err != nil {
		t.Fatalf("query body: %v", err)
	}
	if bodyText != "hello world" {
		t.Fatalf("expected body 'hello world', got %q", bodyText)
	}
}

func TestIngestOCRToNoteTaskGeneratesTitleAndForcesTag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}
	gen := &stubAutoTagGenerator{
		title:      "generated-title",
		suggestion: llm.AutoTagSuggestion{ExistingTags: []string{"project"}, NewTags: []string{"meeting notes"}},
	}
	s.titleClient = gen
	s.autoTagger = gen

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d}`, resp.File.ID, noteID))
	if _, err := s.ingestOCRToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestOCRToNoteTask: %v", err)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "generated-title" {
		t.Fatalf("expected title 'generated-title', got %q", title)
	}

	if gen.lastInput.Title != "generated-title" {
		t.Fatalf("expected auto-tag input title 'generated-title' (title runs before auto tags), got %q", gen.lastInput.Title)
	}

	manualTags, err := loadTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load tags: %v", err)
	}
	if !reflect.DeepEqual(manualTags, []string{"handwritten-note"}) {
		t.Fatalf("expected manual tags [handwritten-note], got %v", manualTags)
	}

	autoTags, err := loadAutoTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load auto tags: %v", err)
	}
	if !reflect.DeepEqual(autoTags, []string{"meeting notes", "project"}) {
		t.Fatalf("expected auto tags [meeting notes project] (name-sorted), got %v", autoTags)
	}
}

func TestIngestOCRToNoteTaskOCRFailureSkipsTitleAndTag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{err: errors.New("boom")}
	gen := &stubAutoTagGenerator{}
	s.titleClient = gen
	s.autoTagger = gen

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d}`, resp.File.ID, noteID))
	msg, err := s.ingestOCRToNoteTask(s.db.DB, payload)
	if err != nil {
		t.Fatalf("ingestOCRToNoteTask: %v", err)
	}
	if !strings.Contains(msg, "completed with error") {
		t.Fatalf("expected message mentioning error, got %q", msg)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "handwritten" {
		t.Fatalf("expected filename-derived title 'handwritten', got %q", title)
	}

	manualTags, err := loadTags(s.db.DB, noteID)
	if err != nil {
		t.Fatalf("load tags: %v", err)
	}
	if len(manualTags) != 0 {
		t.Fatalf("expected no manual tags on OCR failure, got %v", manualTags)
	}
}

func TestHandleHandwrittenIngestParentFromPath(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	res, err := s.db.Exec(`INSERT INTO notes (title) VALUES ('parent')`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("parent id: %v", err)
	}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten/"+fmt.Sprint(parentID), body)
	req.SetPathValue("parent_id", fmt.Sprint(parentID))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Note.ParentID == nil || *resp.Note.ParentID != parentID {
		t.Fatalf("expected parent_id %d, got %v", parentID, resp.Note.ParentID)
	}
}

func TestHandleHandwrittenIngestDatePathAccepted(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	res, err := s.db.Exec(`INSERT INTO notes (title) VALUES ('parent')`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("parent id: %v", err)
	}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten/"+fmt.Sprint(parentID)+"/date", body)
	req.SetPathValue("parent_id", fmt.Sprint(parentID))
	req.SetPathValue("flag", "date")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Note NoteDetail `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Note.ParentID == nil || *resp.Note.ParentID != parentID {
		t.Fatalf("expected parent_id %d, got %v", parentID, resp.Note.ParentID)
	}
}

func TestHandleHandwrittenIngestRejectsInvalidPathParent(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten/abc", body)
	req.SetPathValue("parent_id", "abc")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleHandwrittenIngestRejectsUnknownFlag(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten/1/foo", body)
	req.SetPathValue("parent_id", "1")
	req.SetPathValue("flag", "foo")
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIngestOCRToNoteTaskPrependsDateToTitle(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "secret"
	s.ocrClient = mockOCR{text: "hello world"}
	gen := &stubAutoTagGenerator{
		title:      "generated-title",
		suggestion: llm.AutoTagSuggestion{ExistingTags: []string{"project"}, NewTags: []string{"meeting notes"}},
	}
	s.titleClient = gen
	s.autoTagger = gen

	ct, body := ingestMultipartBody("handwritten.png", fakePNG(), nil)
	req := httptest.NewRequest(http.MethodPost, "/ingest/handwritten", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.handleHandwrittenIngest(w, req)
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
	noteID := resp.Note.ID

	payload := []byte(fmt.Sprintf(`{"file_id":%d,"note_id":%d,"date_prefix":"05-08-26"}`, resp.File.ID, noteID))
	if _, err := s.ingestOCRToNoteTask(s.db.DB, payload); err != nil {
		t.Fatalf("ingestOCRToNoteTask: %v", err)
	}

	var title string
	if err := s.db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "05-08-26: generated-title" {
		t.Fatalf("expected title '05-08-26: generated-title', got %q", title)
	}

	if gen.lastInput.Title != "05-08-26: generated-title" {
		t.Fatalf("expected auto-tag input title '05-08-26: generated-title' (prefixed title runs before auto tags), got %q", gen.lastInput.Title)
	}
}
