package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestRecordAudioBrowserE2E serves the built SPA (FrontEndDist) against a live
// test server so a real browser can drive the /recordaudio/<parentId>[/date]
// recorder page end-to-end: login, session + ingest-token boot, mic capture,
// and the /ingest/audio upload that creates the recording note.
//
// It is skipped unless RECORD_AUDIO_E2E=1 and requires a fresh
// `npm run build` (the new RecordAudioView must be baked into FrontEndDist).
// The executor reads the RECORD_AUDIO_E2E_READY URL from the test log and
// drives the browser, then this test polls the DB for the resulting note.
func TestRecordAudioBrowserE2E(t *testing.T) {
	if os.Getenv("RECORD_AUDIO_E2E") != "1" {
		t.Skip("set RECORD_AUDIO_E2E=1 to run the browser E2E")
	}

	s, _ := newTestServerWithMedia(t)
	s.ingestToken = "e2e-secret"
	s.sttClient = mockSTT{text: "hello world"}
	createTestSession(t, s)

	parent := helperCreateNote(t, s, "e2e-parent", "", nil)

	mux := http.NewServeMux()
	protected := func(h http.HandlerFunc) http.Handler {
		return s.requireAuth(h)
	}
	mux.HandleFunc("/login", s.handleLogin)
	mux.Handle("/session", protected(s.handleSession))
	mux.Handle("/ingest/token", protected(s.handleIngestToken))
	mux.HandleFunc("/ingest/audio", s.handleAudioIngest)
	mux.HandleFunc("/ingest/audio/{parent_id}", s.handleAudioIngest)
	mux.HandleFunc("/ingest/audio/{parent_id}/{flag}", s.handleAudioIngest)
	mux.Handle("/", newSPAHandler("../../FrontEndDist"))

	srv := httptest.NewServer(s.withSecurityHeaders(s.requireTrustedRequest(mux)))
	t.Cleanup(srv.Close)
	s.cfg.TrustedOrigins = map[string]struct{}{normalizeOrigin(srv.URL): {}}

	t.Logf("RECORD_AUDIO_E2E_READY %s", srv.URL)
	t.Logf("RECORD_AUDIO_E2E_PARENT %d", parent.ID)

	// The recorder page ends with a successful upload creating a note titled
	// recording-* under the parent. Poll until it appears (or 120s elapse).
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM notes WHERE parent_id = ? AND title LIKE 'recording-%'`,
			parent.ID,
		).Scan(&count)
		if err == nil && count > 0 {
			t.Logf("RECORD_AUDIO_E2E_OK recording note created under parent %d", parent.ID)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a recording note under parent %d", parent.ID)
}
