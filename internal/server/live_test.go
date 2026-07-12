package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/jobs"
)

type stubTitleGenerator struct {
	title string
	err   error
}

func (g stubTitleGenerator) GenerateTitle(_ string) (string, error) {
	return g.title, g.err
}

func newLiveTestHTTPServer(t *testing.T, s *Server) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	protected := func(h http.HandlerFunc) http.Handler {
		return s.requireAuth(h)
	}
	mux.Handle("/ws", protected(s.handleWebSocket))
	server := httptest.NewServer(s.withSecurityHeaders(s.requireTrustedRequest(mux)))
	t.Cleanup(server.Close)
	s.cfg.TrustedOrigins = map[string]struct{}{normalizeOrigin(server.URL): {}}
	return server, "ws" + server.URL[len("http"):] + "/ws"
}

func dialLiveWebSocket(t *testing.T, wsURL, token, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	if token != "" {
		headers.Add("Cookie", (&http.Cookie{Name: authCookieName, Value: token}).String())
	}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	return conn, resp, err
}

func readLiveMessage(t *testing.T, conn *websocket.Conn) liveMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var msg liveMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return msg
}

func requireLiveMessageType(t *testing.T, conn *websocket.Conn, want string) liveMessage {
	t.Helper()
	msg := readLiveMessage(t, conn)
	if msg.Type != want {
		t.Fatalf("message type = %q, want %q", msg.Type, want)
	}
	return msg
}

func assertNoteIDsEqual(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	gotCopy := append([]int64(nil), got...)
	wantCopy := append([]int64(nil), want...)
	sort.Slice(gotCopy, func(i, j int) bool { return gotCopy[i] < gotCopy[j] })
	sort.Slice(wantCopy, func(i, j int) bool { return wantCopy[i] < wantCopy[j] })
	if len(gotCopy) != len(wantCopy) {
		t.Fatalf("note_ids length = %d (%v), want %d (%v)", len(gotCopy), gotCopy, len(wantCopy), wantCopy)
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			t.Fatalf("note_ids = %v, want %v", gotCopy, wantCopy)
		}
	}
}

func TestWebSocketRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	_, wsURL := newLiveTestHTTPServer(t, s)

	conn, resp, err := dialLiveWebSocket(t, wsURL, "", "")
	if conn != nil {
		conn.Close()
		t.Fatal("expected websocket dial without auth to fail")
	}
	if err == nil {
		t.Fatal("expected websocket dial without auth to fail")
	}
	if resp == nil {
		t.Fatalf("expected HTTP response, got nil (err=%v)", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestWebSocketRejectsUntrustedOrigin(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)
	_, wsURL := newLiveTestHTTPServer(t, s)

	conn, resp, err := dialLiveWebSocket(t, wsURL, token, "https://evil.example")
	if conn != nil {
		conn.Close()
		t.Fatal("expected websocket dial with untrusted origin to fail")
	}
	if err == nil {
		t.Fatal("expected websocket dial with untrusted origin to fail")
	}
	if resp == nil {
		t.Fatalf("expected HTTP response, got nil (err=%v)", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestWebSocketSendsReadyAndJobEvents(t *testing.T) {
	s := newTestServer(t)
	if err := s.jobManager.RegisterAdHoc("test", []jobs.CronJob{{
		Name: "live_job",
		Task: func(_ *sql.DB, _ []byte) (string, error) {
			return "ok", nil
		},
	}}); err != nil {
		t.Fatalf("register ad-hoc job: %v", err)
	}
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	requireLiveMessageType(t, conn, liveTypeReady)

	runID, err := s.jobManager.Enqueue("test", "live_job", nil)
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	msg := requireLiveMessageType(t, conn, liveTypeJobsChange)
	if msg.Job == nil {
		t.Fatal("expected job payload on jobs.changed")
	}
	if msg.Job.Type != "enqueued" || msg.Job.RunID != runID || msg.Job.Status != jobs.StatusPlanned {
		t.Fatalf("unexpected job event: %+v", msg.Job)
	}

	if err := s.jobManager.CancelRun(runID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	msg = requireLiveMessageType(t, conn, liveTypeJobsChange)
	if msg.Job == nil {
		t.Fatal("expected job payload on jobs.changed")
	}
	if msg.Job.Type != "cancelled" || msg.Job.RunID != runID || msg.Job.Status != jobs.StatusCancelled {
		t.Fatalf("unexpected cancelled job event: %+v", msg.Job)
	}
}

func TestCreateChildNoteBroadcastsParentAndChildIDs(t *testing.T) {
	s := newTestServer(t)
	parent := helperCreateNote(t, s, "Parent", "", nil)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	payload := fmt.Sprintf(`{"title":"Child","body":"hello","parent_id":%d}`, parent.ID)
	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.createNote(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create note: status=%d body=%s", w.Code, w.Body.String())
	}
	var created NoteDetail
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created note: %v", err)
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "created" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "created")
	}
	assertNoteIDsEqual(t, msg.NoteIDs, created.ID, parent.ID)
}

func TestUpdateNoteReparentBroadcastsOldAndNewParents(t *testing.T) {
	s := newTestServer(t)
	oldParent := helperCreateNote(t, s, "Old Parent", "", nil)
	newParent := helperCreateNote(t, s, "New Parent", "", nil)
	child := helperCreateNote(t, s, "Child", "body", &oldParent.ID)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	payload := fmt.Sprintf(`{"title":"Child","body":"updated","parent_id":%d}`, newParent.ID)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", child.ID), bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	s.updateNote(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update note: status=%d body=%s", w.Code, w.Body.String())
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "updated" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "updated")
	}
	assertNoteIDsEqual(t, msg.NoteIDs, child.ID, oldParent.ID, newParent.ID)
}

func TestDeleteNoteBroadcastsParentAndDetachedChildren(t *testing.T) {
	s := newTestServer(t)
	root := helperCreateNote(t, s, "Root", "", nil)
	parent := helperCreateNote(t, s, "Parent", "", &root.ID)
	child := helperCreateNote(t, s, "Child", "", &parent.ID)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/notes/%d", parent.ID), nil)
	s.deleteNote(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete note: status=%d body=%s", w.Code, w.Body.String())
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "deleted" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "deleted")
	}
	assertNoteIDsEqual(t, msg.NoteIDs, parent.ID, root.ID, child.ID)
}

func TestActionRelatedNoteIDsIncludesEmbeddedIDs(t *testing.T) {
	result := map[string]any{
		"task_note_id":     float64(7),
		"created_note_ids": []any{float64(11), float64(12), float64(-1)},
		"nested": map[string]any{
			"primary_note_id": float64(19),
		},
	}
	got := actionRelatedNoteIDs(3, result)
	assertNoteIDsEqual(t, got, 3, 7, 11, 12, 19)
}

func TestGenerateTitleTaskBroadcastsNoteChange(t *testing.T) {
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	s := New(d, ":0", nil, stubTitleGenerator{title: "Generated Title"}, nil, nil)
	note := helperCreateNote(t, s, "Untitled", "body", nil)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	payload, _ := json.Marshal(map[string]any{"note_id": note.ID, "body": "body"})
	if _, err := s.generateTitleTask(s.db.DB, payload); err != nil {
		t.Fatalf("generateTitleTask: %v", err)
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "title_generated" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "title_generated")
	}
	assertNoteIDsEqual(t, msg.NoteIDs, note.ID)
}

func TestWebSocketAppPingPong(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	requireLiveMessageType(t, conn, liveTypeReady)

	// Send application-level ping.
	beforeSend := time.Now()
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	// Expect a pong response.
	msg := requireLiveMessageType(t, conn, "pong")
	rtt := time.Since(beforeSend)
	if rtt > 2*time.Second {
		t.Errorf("pong RTT too high: %v", rtt)
	}
	if msg.Timestamp == "" {
		t.Error("pong message has empty timestamp")
	}
	if _, err := time.Parse(time.RFC3339Nano, msg.Timestamp); err != nil {
		t.Errorf("pong timestamp %q is not valid RFC3339Nano: %v", msg.Timestamp, err)
	}

	// Verify the connection is still healthy by sending another ping and
	// checking the channel is not closed.
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatalf("write second ping: %v", err)
	}
	msg2 := requireLiveMessageType(t, conn, "pong")
	if msg2.Timestamp == "" {
		t.Error("second pong message has empty timestamp")
	}
}
