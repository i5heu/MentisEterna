package server

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// readLiveMessageTimeout reads a liveMessage, failing the test if none arrives
// within the given deadline. Returns (msg, true) on success.
func readLiveMessageTimeout(t *testing.T, conn *websocket.Conn, d time.Duration) (liveMessage, bool) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var msg liveMessage
	if err := conn.ReadJSON(&msg); err != nil {
		return msg, false
	}
	return msg, true
}

// assertNoLiveMessage asserts that no liveMessage arrives within the window.
func assertNoLiveMessage(t *testing.T, conn *websocket.Conn, announce string) {
	t.Helper()
	if _, ok := readLiveMessageTimeout(t, conn, 200*time.Millisecond); ok {
		t.Fatalf("unexpected live message (want none after %s)", announce)
	}
}

func TestWebSocketEditSyncRelay(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.liveHub = newLiveHub()

	aliceTokenA, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session: %v", err)
	}
	aliceTokenB, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice second session: %v", err)
	}
	bobToken, _, err := s.db.CreateSession("bob")
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	aliceA, _, err := dialLiveWebSocket(t, wsURL, aliceTokenA, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-A: %v", err)
	}
	defer aliceA.Close()
	aliceB, _, err := dialLiveWebSocket(t, wsURL, aliceTokenB, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-B: %v", err)
	}
	defer aliceB.Close()
	bob, _, err := dialLiveWebSocket(t, wsURL, bobToken, httpServer.URL)
	if err != nil {
		t.Fatalf("dial bob: %v", err)
	}
	defer bob.Close()

	// Drain initial live.ready from each connection.
	requireLiveMessageType(t, aliceA, liveTypeReady)
	requireLiveMessageType(t, aliceB, liveTypeReady)
	requireLiveMessageType(t, bob, liveTypeReady)

	editingTrue := true
	editingFalse := false

	// alice-A enters edit mode on note 7.
	if err := aliceA.WriteJSON(liveMessage{
		Type: liveTypeEditSync, NoteID: 7, Editing: &editingTrue, DeviceID: "devA",
	}); err != nil {
		t.Fatalf("alice-A send edit.sync true: %v", err)
	}

	// alice-B receives it.
	msg, ok := readLiveMessageTimeout(t, aliceB, 3*time.Second)
	if !ok {
		t.Fatal("alice-B: expected edit.sync, got none")
	}
	if msg.Type != liveTypeEditSync || msg.NoteID != 7 || msg.Editing == nil || !*msg.Editing || msg.DeviceID != "devA" {
		t.Fatalf("alice-B: unexpected relayed message: %+v", msg)
	}

	// No self-echo to alice-A.
	assertNoLiveMessage(t, aliceA, "self-echo check")
	// Cross-user isolation: bob must not receive anything.
	assertNoLiveMessage(t, bob, "cross-user isolation check")

	// alice-A leaves edit mode.
	if err := aliceA.WriteJSON(liveMessage{
		Type: liveTypeEditSync, NoteID: 7, Editing: &editingFalse, DeviceID: "devA",
	}); err != nil {
		t.Fatalf("alice-A send edit.sync false: %v", err)
	}

	msg, ok = readLiveMessageTimeout(t, aliceB, 3*time.Second)
	if !ok {
		t.Fatal("alice-B: expected edit.sync false, got none")
	}
	if msg.Type != liveTypeEditSync || msg.NoteID != 7 || msg.Editing == nil || *msg.Editing {
		t.Fatalf("alice-B: unexpected relayed message: %+v", msg)
	}

	// Malformed messages are ignored: no relay, no panic.
	if err := aliceA.WriteJSON(map[string]any{
		"type": liveTypeEditSync, "note_id": 0, "editing": true,
	}); err != nil {
		t.Fatalf("alice-A send malformed note_id: %v", err)
	}
	if err := aliceA.WriteJSON(map[string]any{
		"type": liveTypeEditSync, "note_id": 7, // editing absent
	}); err != nil {
		t.Fatalf("alice-A send malformed editing: %v", err)
	}
	assertNoLiveMessage(t, aliceB, "malformed relay check")
}

func TestWebSocketEditBodyRelay(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.liveHub = newLiveHub()

	aliceA, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session: %v", err)
	}
	aliceB, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice second session: %v", err)
	}
	bob, _, err := s.db.CreateSession("bob")
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	httpServer, wsURL := newLiveTestHTTPServer(t, s)
	connA, _, err := dialLiveWebSocket(t, wsURL, aliceA, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-A: %v", err)
	}
	defer connA.Close()
	connB, _, err := dialLiveWebSocket(t, wsURL, aliceB, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-B: %v", err)
	}
	defer connB.Close()
	connBob, _, err := dialLiveWebSocket(t, wsURL, bob, httpServer.URL)
	if err != nil {
		t.Fatalf("dial bob: %v", err)
	}
	defer connBob.Close()

	requireLiveMessageType(t, connA, liveTypeReady)
	requireLiveMessageType(t, connB, liveTypeReady)
	requireLiveMessageType(t, connBob, liveTypeReady)

	body := "hello from device A\n\nsecond line"
	if err := connA.WriteJSON(liveMessage{
		Type: liveTypeEditBody, NoteID: 7, Body: body, DeviceID: "devA",
	}); err != nil {
		t.Fatalf("alice-A send edit.body: %v", err)
	}

	msg, ok := readLiveMessageTimeout(t, connB, 3*time.Second)
	if !ok {
		t.Fatal("alice-B: expected edit.body, got none")
	}
	if msg.Type != liveTypeEditBody || msg.NoteID != 7 || msg.Body != body || msg.DeviceID != "devA" {
		t.Fatalf("alice-B: unexpected edit.body: %+v", msg)
	}

	// No self-echo, no cross-user leak.
	assertNoLiveMessage(t, connA, "edit.body self-echo check")
	assertNoLiveMessage(t, connBob, "edit.body cross-user isolation check")

	// Malformed (missing note_id) is ignored.
	if err := connA.WriteJSON(liveMessage{Type: liveTypeEditBody, Body: "boom"}); err != nil {
		t.Fatalf("alice-A send malformed edit.body: %v", err)
	}
	assertNoLiveMessage(t, connB, "malformed edit.body check")
}
