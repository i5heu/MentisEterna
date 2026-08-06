package server

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// setupDeviceConns dials alice-A, alice-B, and bob, drains their live.ready,
// and registers device hellos devA/devB/devBob. It returns the three conns.
func setupDeviceConns(t *testing.T, s *Server) (connA, connB, connBob *websocket.Conn, wsURL string) {
	t.Helper()
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

	httpServer, url := newLiveTestHTTPServer(t, s)
	connA, _, err = dialLiveWebSocket(t, url, aliceA, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-A: %v", err)
	}
	connB, _, err = dialLiveWebSocket(t, url, aliceB, httpServer.URL)
	if err != nil {
		t.Fatalf("dial alice-B: %v", err)
	}
	connBob, _, err = dialLiveWebSocket(t, url, bob, httpServer.URL)
	if err != nil {
		t.Fatalf("dial bob: %v", err)
	}

	requireLiveMessageType(t, connA, liveTypeReady)
	requireLiveMessageType(t, connB, liveTypeReady)
	requireLiveMessageType(t, connBob, liveTypeReady)

	if err := connA.WriteJSON(liveMessage{Type: liveTypeDeviceHello, DeviceID: "devA"}); err != nil {
		t.Fatalf("alice-A send device.hello: %v", err)
	}
	if err := connB.WriteJSON(liveMessage{Type: liveTypeDeviceHello, DeviceID: "devB"}); err != nil {
		t.Fatalf("alice-B send device.hello: %v", err)
	}
	if err := connBob.WriteJSON(liveMessage{Type: liveTypeDeviceHello, DeviceID: "devBob"}); err != nil {
		t.Fatalf("bob send device.hello: %v", err)
	}

	// Each same-user peer sees the other's device.online.
	onlineA := requireLiveMessageType(t, connA, liveTypeDeviceOnline)
	if onlineA.DeviceID != "devB" {
		t.Fatalf("alice-A: unexpected online device %q", onlineA.DeviceID)
	}
	onlineB := requireLiveMessageType(t, connB, liveTypeDeviceOnline)
	if onlineB.DeviceID != "devA" {
		t.Fatalf("alice-B: unexpected online device %q", onlineB.DeviceID)
	}
	assertNoLiveMessage(t, connBob, "bob must not see alice device.online")
	return connA, connB, connBob, url
}

func TestWebSocketDeviceRouting(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.liveHub = newLiveHub()
	connA, connB, connBob, _ := setupDeviceConns(t, s)
	defer connA.Close()
	defer connB.Close()
	defer connBob.Close()

	// alice-A -> alice-B: targeted unicast relay with opaque payload.
	if err := connA.WriteJSON(liveMessage{
		Type: liveTypeDeviceMsg, ToDeviceID: "devB", Channel: "teleport", Data: "SGVsbG8=",
	}); err != nil {
		t.Fatalf("alice-A send device.msg: %v", err)
	}
	msg, ok := readLiveMessageTimeout(t, connB, 3*time.Second)
	if !ok {
		t.Fatal("alice-B: expected device.msg, got none")
	}
	if msg.Type != liveTypeDeviceMsg || msg.FromDeviceID != "devA" ||
		msg.ToDeviceID != "devB" || msg.Channel != "teleport" || msg.Data != "SGVsbG8=" {
		t.Fatalf("alice-B: unexpected device.msg: %+v", msg)
	}
	// No self-echo, no cross-user leak.
	assertNoLiveMessage(t, connA, "device.msg self-echo check")
	assertNoLiveMessage(t, connBob, "device.msg cross-user isolation check")

	// Cross-user targeting is dropped (the security guard).
	if err := connBob.WriteJSON(liveMessage{
		Type: liveTypeDeviceMsg, ToDeviceID: "devA", Channel: "teleport", Data: "ZXZpbA==",
	}); err != nil {
		t.Fatalf("bob send cross-user device.msg: %v", err)
	}
	assertNoLiveMessage(t, connA, "cross-user targeted device.msg check")
	assertNoLiveMessage(t, connB, "cross-user targeted device.msg check 2")

	// Malformed (empty to_device_id) is ignored without panicking.
	if err := connB.WriteJSON(liveMessage{Type: liveTypeDeviceMsg, Channel: "teleport", Data: "eA=="}); err != nil {
		t.Fatalf("alice-B send malformed device.msg: %v", err)
	}
	assertNoLiveMessage(t, connA, "malformed device.msg check")
	assertNoLiveMessage(t, connB, "malformed device.msg check 2")
}

func TestWebSocketDevicePresence(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.liveHub = newLiveHub()
	connA, connB, connBob, _ := setupDeviceConns(t, s)
	defer connB.Close()
	defer connBob.Close()

	// Closing alice-A's socket unregisters devA and announces offline to
	// same-user peers only.
	if err := connA.Close(); err != nil {
		t.Fatalf("close alice-A: %v", err)
	}
	offline := requireLiveMessageType(t, connB, liveTypeDeviceOffline)
	if offline.DeviceID != "devA" {
		t.Fatalf("alice-B: unexpected offline device %q", offline.DeviceID)
	}
	assertNoLiveMessage(t, connBob, "bob must not see alice device.offline")
}
