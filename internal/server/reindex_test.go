package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/i5heu/MentisEterna/internal/jobs"
	"github.com/i5heu/MentisEterna/internal/media"
)

// TestReindexSTTEnqueuesErroredAudioMP4 covers the regression where the
// "Re-Index Missing" STT button silently did nothing for a file whose STT run
// errored. Root cause: the reindex query hardcoded a MIME list that drifted
// from media.audioMIMETypes — in particular `audio/mp4` (what DetectMIME
// returns for M4A files) was missing, so the errored file never matched.
func TestReindexSTTEnqueuesErroredAudioMP4(t *testing.T) {
	s := newTestServer(t) // file-backed DB: normal connection pool
	s.sttClient = mockSTT{text: "", err: errors.New("model down")}

	// Wire the media service (newTestServer has none by default).
	cacheDir := filepath.Join(t.TempDir(), "media-cache")
	cfg := media.Config{
		CacheDir: cacheDir,
		Endpoints: []media.EndpointConfig{
			{ID: "primary", Bucket: "test", Endpoint: "http://localhost:9000", AccessKeyID: "k", SecretAccessKey: "s", Region: "us-east-1", ForcePathStyle: true},
		},
	}
	svc := media.NewService(s.db, cfg)
	svc.Store = newFakeMediaStore()
	s.mediaService = svc
	// Leave EnqueueFunc unset until after the upload so the upload does not
	// auto-enqueue STT (we drive the task manually below).

	// Upload an M4A file; DetectMIME must store it as audio/mp4.
	noteID, token := createTestNoteWithSession(t, s)
	ct, body := multipartBody("file", "recording.m4a", "audio/mp4", fakeM4A())
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/files", noteID), body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.uploadAttachment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d: %s", w.Code, w.Body.String())
	}

	var fileID int64
	var mimeType string
	if err := s.db.QueryRow(`SELECT id, mime_type FROM files WHERE filename = 'recording.m4a'`).Scan(&fileID, &mimeType); err != nil {
		t.Fatalf("query uploaded file: %v", err)
	}
	if mimeType != "audio/mp4" {
		t.Fatalf("expected detected MIME audio/mp4, got %q", mimeType)
	}

	// Wire the job manager the way Start() does.
	s.mediaService.EnqueueFunc = s.jobManager.Enqueue
	if err := s.jobManager.RegisterAdHoc("_media", []jobs.CronJob{
		{Name: "stt_file", Task: s.sttFileTask},
	}); err != nil {
		t.Fatalf("register stt_file: %v", err)
	}

	// Simulate an errored STT run: the task fails against the model and
	// persists the error into files_stt.
	if _, err := s.sttFileTask(s.db.DB, []byte(fmt.Sprintf(`{"file_id":%d}`, fileID))); err != nil {
		t.Fatalf("sttFileTask (errored run): %v", err)
	}
	var errMsg string
	if err := s.db.QueryRow(`SELECT COALESCE(error, '') FROM files_stt WHERE file_id = ?`, fileID).Scan(&errMsg); err != nil {
		t.Fatalf("query files_stt error: %v", err)
	}
	if errMsg == "" {
		t.Fatal("expected files_stt.error to be set after the errored run")
	}

	// "Re-Index Missing" must pick the errored audio/mp4 file up and enqueue
	// a fresh stt_file run.
	req2 := httptest.NewRequest(http.MethodPost, "/maintenance/reindex-stt", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	s.handleReindexSTT(w2, req2)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("reindex-stt: expected 202, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode reindex response: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("reindex-stt count = %d, want 1 (errored audio/mp4 file must be picked up)", resp.Count)
	}

	var planned int
	payload := fmt.Sprintf(`{"file_id":%d}`, fileID)
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM job_runs jr
		 JOIN job_definitions jd ON jd.id = jr.job_id
		 WHERE jd.name = 'stt_file' AND jr.payload = ? AND jr.status = 'planned'`,
		payload,
	).Scan(&planned); err != nil {
		t.Fatalf("count planned stt_file runs: %v", err)
	}
	if planned != 1 {
		t.Fatalf("planned stt_file runs = %d, want 1", planned)
	}

	// Once the model recovers, the re-enqueued run must clear the error and
	// store the transcript.
	s.sttClient = mockSTT{text: "hello world"}
	if _, err := s.sttFileTask(s.db.DB, []byte(fmt.Sprintf(`{"file_id":%d}`, fileID))); err != nil {
		t.Fatalf("sttFileTask (recovered run): %v", err)
	}
	var sttText string
	if err := s.db.QueryRow(`SELECT COALESCE(stt_text, '') FROM files_stt WHERE file_id = ?`, fileID).Scan(&sttText); err != nil {
		t.Fatalf("query recovered stt_text: %v", err)
	}
	if sttText != "hello world" {
		t.Fatalf("expected recovered stt_text 'hello world', got %q", sttText)
	}
	var errCleared string
	if err := s.db.QueryRow(`SELECT COALESCE(error, '') FROM files_stt WHERE file_id = ?`, fileID).Scan(&errCleared); err != nil {
		t.Fatalf("query cleared error: %v", err)
	}
	if errCleared != "" {
		t.Fatalf("expected files_stt.error cleared after successful re-run, got %q", errCleared)
	}
}
