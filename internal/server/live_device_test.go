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
	// Presence state sync: a hello also returns the current same-user
	// registry to the announcing connection, so alice-B receives devA a
	// second time (its own hello's snapshot). Drain that copy.
	syncB := requireLiveMessageType(t, connB, liveTypeDeviceOnline)
	if syncB.DeviceID != "devA" {
		t.Fatalf("alice-B: unexpected presence-sync online device %q", syncB.DeviceID)
	}
	assertNoLiveMessage(t, connA, "alice-A must not get a presence snapshot for its own hello")
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

func TestWebSocketPresenceSync(t *testing.T) {
	s, _ := newTestServerWithMedia(t)
	s.liveHub = newLiveHub()
	connA, connB, connBob, wsURL := setupDeviceConns(t, s)
	defer connA.Close()
	defer connB.Close()
	defer connBob.Close()

	// A fresh connection announcing its device (the reload scenario) must
	// receive the current same-user registry as device.online snapshots —
	// presence is otherwise push-only and the announcer would not learn about
	// devices that connected while it was away.
	aliceToken, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session: %v", err)
	}
	connC, _, err := dialLiveWebSocket(t, wsURL, aliceToken, "")
	if err != nil {
		t.Fatalf("dial alice-C: %v", err)
	}
	defer connC.Close()
	requireLiveMessageType(t, connC, liveTypeReady)

	if err := connC.WriteJSON(liveMessage{Type: liveTypeDeviceHello, DeviceID: "devC"}); err != nil {
		t.Fatalf("alice-C device.hello: %v", err)
	}
	// The snapshot covers both existing same-user devices (map iteration
	// order is random, so match as a set).
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		online := requireLiveMessageType(t, connC, liveTypeDeviceOnline)
		seen[online.DeviceID] = true
	}
	if !seen["devA"] || !seen["devB"] {
		t.Fatalf("alice-C presence sync must report devA and devB, got %v", seen)
	}
	assertNoLiveMessage(t, connC, "alice-C presence sync must not include itself")

	// The existing same-user peer sees the new device via the broadcast, but
	// must NOT receive a registry snapshot (that goes only to the announcer).
	onlineB := requireLiveMessageType(t, connB, liveTypeDeviceOnline)
	if onlineB.DeviceID != "devC" {
		t.Fatalf("alice-B: expected devC announce, got %q", onlineB.DeviceID)
	}
	assertNoLiveMessage(t, connB, "alice-B must not get a presence snapshot for another device's hello")
	assertNoLiveMessage(t, connBob, "bob must not see alice presence sync")
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
