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
