package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedChunkedRaceFixture creates a two-chunk inline upload session with
// all chunks on disk and chunks_done complete — minus the auto-finish
// goroutine the last chunk would have spawned (total_chunks=2, only
// chunk 0 goes through the handler, chunk 1 is added manually). Each
// test then controls which finalize path "wins".
func seedChunkedRaceFixture(t *testing.T, s *Server) (noteID int64, uploadID string) {
	t.Helper()

	res, err := s.db.Exec(`INSERT INTO notes (title) VALUES ('chunked-race-fixture')`)
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	noteID, err = res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	// Start a 2-chunk inline session through the real handler.
	startBody := `{"filename":"pic.png","mime_type":"image/png","total_size":11,"chunk_size":1048576,"total_chunks":2,"inline":true}`
	startReq := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/notes/%d/chunked/start", noteID), bytes.NewReader([]byte(startBody)))
	startReq.Header.Set("Content-Type", "application/json")
	sw := httptest.NewRecorder()
	s.handleChunkedStart(sw, startReq, noteID)
	if sw.Code != http.StatusOK {
		t.Fatalf("start session: %d: %s", sw.Code, sw.Body.String())
	}
	var startResp struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(sw.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("parse start response: %v", err)
	}

	// Chunk 0 through the real handler — 1/2 chunks means no
	// auto-finish goroutine is spawned.
	var chunkBuf bytes.Buffer
	mw := multipart.NewWriter(&chunkBuf)
	part, _ := mw.CreateFormFile("chunk", "chunk")
	part.Write([]byte("hello "))
	mw.Close()
	chunkReq := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/notes/%d/chunked/%s/chunk?index=0", noteID, startResp.UploadID), &chunkBuf)
	chunkReq.Header.Set("Content-Type", mw.FormDataContentType())
	cw := httptest.NewRecorder()
	s.handleChunkedChunk(cw, chunkReq, noteID, startResp.UploadID)
	if cw.Code != http.StatusOK {
		t.Fatalf("upload chunk 0: %d: %s", cw.Code, cw.Body.String())
	}

	// Chunk 1 written directly: the session is now complete without
	// the auto-finish side effect.
	if err := os.WriteFile(filepath.Join(chunksDir(startResp.UploadID), "1.chunk"), []byte("world"), 0o600); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE upload_sessions SET chunks_done = '[0,1]' WHERE upload_id = ?`, startResp.UploadID); err != nil {
		t.Fatalf("complete session: %v", err)
	}
	return noteID, startResp.UploadID
}

// callFinish drives POST .../finish through the handler.
func callFinish(t *testing.T, s *Server, noteID int64, uploadID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/notes/%d/chunked/%s/finish", noteID, uploadID), nil)
	w := httptest.NewRecorder()
	s.handleChunkedFinish(w, req, noteID, uploadID)
	return w
}

// TestChunkedFinishIdempotentAfterConcurrentFinalize pins the
// auto-finish-wins order: a finalize that already completed the
// session (the background goroutine's exact effect) must make the
// explicit finish endpoint return the SAME 201 response — not bounce
// the client off a 409 "upload already completed".
func TestChunkedFinishIdempotentAfterConcurrentFinalize(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	noteID, uploadID := seedChunkedRaceFixture(t, s)

	// The "auto-finish": a finalize that runs to completion before the
	// HTTP finish call arrives.
	winnerResp, _, err := s.finalizeChunkedUploadSession(noteID, uploadID)
	if err != nil {
		t.Fatalf("concurrent finalize: %v", err)
	}
	winnerFile, ok := winnerResp["file"].(*NoteFile)
	if !ok || winnerFile == nil {
		t.Fatalf("winner response has no file: %+v", winnerResp)
	}

	w := callFinish(t, s, noteID, uploadID)
	if w.Code != http.StatusCreated {
		t.Fatalf("finish after concurrent finalize = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		File     map[string]interface{} `json:"file"`
		Markdown string                 `json:"markdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse finish response: %v", err)
	}
	// The loser returns the winner's persisted result verbatim.
	if resp.File["id"] != float64(winnerFile.ID) || resp.File["url"] != winnerFile.URL {
		t.Fatalf("finish response %+v does not match the winner's file id=%d url=%s", resp.File, winnerFile.ID, winnerFile.URL)
	}
	if resp.Markdown == "" {
		t.Fatal("inline finish response lost its markdown")
	}
}

// TestChunkedFinishWaitsForInFlightFinalize pins the
// explicit-finish-loses order: while a finalize is claimed and still
// running (status 'assembling'), the finish endpoint must wait for the
// winner to persist its result and then return the same 201 — instead
// of the 409 that used to race under load.
func TestChunkedFinishWaitsForInFlightFinalize(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	noteID, uploadID := seedChunkedRaceFixture(t, s)

	// Simulate a claimed, in-flight finalize.
	if _, err := s.db.Exec(`UPDATE upload_sessions SET status = 'assembling' WHERE upload_id = ?`, uploadID); err != nil {
		t.Fatalf("claim session: %v", err)
	}

	// The winner completes a moment later with its persisted result
	// blob (the sleep is the scenario — the in-flight finalize's
	// duration — not a synchronization; the handler's await loop is
	// the mechanism under test).
	finishBlob := `{"file":{"id":4242,"filename":"pic.png","mime_type":"image/png","size_bytes":11,"url":"/file/1/4242","is_image":true},"markdown":"![pic.png](/file/1/4242)"}`
	go func() {
		time.Sleep(150 * time.Millisecond)
		if _, err := s.db.Exec(
			`UPDATE upload_sessions SET status = 'done', finish_result = ? WHERE upload_id = ?`,
			finishBlob, uploadID); err != nil {
			t.Errorf("complete winner: %v", err)
		}
	}()

	// Fast poll for a snappy test; restored afterwards.
	prevPoll := chunkedFinishAwaitPoll
	chunkedFinishAwaitPoll = 5 * time.Millisecond
	t.Cleanup(func() { chunkedFinishAwaitPoll = prevPoll })

	w := callFinish(t, s, noteID, uploadID)
	if w.Code != http.StatusCreated {
		t.Fatalf("finish during in-flight finalize = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		File     map[string]interface{} `json:"file"`
		Markdown string                 `json:"markdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse finish response: %v", err)
	}
	if resp.File["id"] != float64(4242) {
		t.Fatalf("finish response = %+v, want the winner's persisted result", resp.File)
	}
	if resp.Markdown == "" {
		t.Fatal("awaited finish response lost its markdown")
	}
}

// TestChunkedFinishStillConflictsWhenFinalizeNeverCompletes keeps the
// honest 409: when the claimed finalize never reaches an outcome, the
// finish endpoint must still report the conflict instead of hanging
// forever.
func TestChunkedFinishStillConflictsWhenFinalizeNeverCompletes(t *testing.T) {
	s := newTestServerWithMediaForNotes(t)
	noteID, uploadID := seedChunkedRaceFixture(t, s)

	if _, err := s.db.Exec(`UPDATE upload_sessions SET status = 'assembling' WHERE upload_id = ?`, uploadID); err != nil {
		t.Fatalf("claim session: %v", err)
	}

	prevPoll, prevTimeout := chunkedFinishAwaitPoll, chunkedFinishAwaitTimeout
	chunkedFinishAwaitPoll = 5 * time.Millisecond
	chunkedFinishAwaitTimeout = 40 * time.Millisecond
	t.Cleanup(func() {
		chunkedFinishAwaitPoll = prevPoll
		chunkedFinishAwaitTimeout = prevTimeout
	})

	w := callFinish(t, s, noteID, uploadID)
	if w.Code != http.StatusConflict {
		t.Fatalf("finish on wedged finalize = %d: %s", w.Code, w.Body.String())
	}
	if want := "already being finalized"; !bytes.Contains(w.Body.Bytes(), []byte(want)) {
		t.Fatalf("conflict body %q missing %q", w.Body.String(), want)
	}
}
