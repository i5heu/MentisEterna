package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/internal/db"
)

// Device queue tests (offline delivery, at-least-once redelivery, FIFO,
// TTL expiry, caps, cross-user guard, direct-only pairing, /devices).
//
// newTestServer builds the server through New(), so its live hub carries the
// persisted device queue against the test database (migrations included).

// dialQueueDevice dials the live socket, drains live.ready, and announces
// deviceID via device.hello. Returns the connection.
func dialQueueDevice(t *testing.T, wsURL, token, deviceID string) *websocket.Conn {
	t.Helper()
	conn, _, err := dialLiveWebSocket(t, wsURL, token, "")
	if err != nil {
		t.Fatalf("dial %s: %v", deviceID, err)
	}
	requireLiveMessageType(t, conn, liveTypeReady)
	if err := conn.WriteJSON(liveMessage{Type: liveTypeDeviceHello, DeviceID: deviceID}); err != nil {
		t.Fatalf("%s send device.hello: %v", deviceID, err)
	}
	return conn
}

// readUntilLive reads frames until match returns true (skipping unrelated
// traffic such as presence broadcasts), failing after timeout.
func readUntilLive(t *testing.T, conn *websocket.Conn, timeout time.Duration, match func(liveMessage) bool, what string) liveMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for %s", what)
		}
		msg, ok := readLiveMessageTimeout(t, conn, remaining)
		if !ok {
			t.Fatalf("timed out waiting for %s", what)
		}
		if match(msg) {
			return msg
		}
	}
}

func readDeviceStatusFor(t *testing.T, conn *websocket.Conn, clientMsgID, wantStatus string) liveMessage {
	t.Helper()
	return readUntilLive(t, conn, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceStatus && m.ClientMsgID == clientMsgID && m.Status == wantStatus
	}, fmt.Sprintf("device.status %q for %q", wantStatus, clientMsgID))
}

// statusReader reads device.status frames with a look-back buffer: status
// frames arrive in arbitrary order (each enqueued message spawns its own
// drain goroutine, and goroutine scheduling order is not FIFO), so a
// read-for-one-id must not discard the frames other waits still need.
type statusReader struct {
	conn    *websocket.Conn
	pending []liveMessage
}

func newStatusReader(conn *websocket.Conn) *statusReader {
	return &statusReader{conn: conn}
}

func (sr *statusReader) waitForStatus(t *testing.T, clientMsgID, wantStatus string) liveMessage {
	t.Helper()
	match := func(m liveMessage) bool {
		return m.Type == liveTypeDeviceStatus && m.ClientMsgID == clientMsgID && m.Status == wantStatus
	}
	what := fmt.Sprintf("device.status %q for %q", wantStatus, clientMsgID)
	deadline := time.Now().Add(3 * time.Second)
	for {
		for i, m := range sr.pending {
			if match(m) {
				sr.pending = append(sr.pending[:i], sr.pending[i+1:]...)
				return m
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for %s", what)
		}
		msg, ok := readLiveMessageTimeout(t, sr.conn, remaining)
		if !ok {
			t.Fatalf("timed out waiting for %s", what)
		}
		sr.pending = append(sr.pending, msg)
	}
}

func queueRowCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM device_queue`).Scan(&n); err != nil {
		t.Fatalf("count device_queue: %v", err)
	}
	return n
}

func waitForQueueRowCount(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := queueRowCount(t, s); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("device_queue row count = %d, want %d", queueRowCount(t, s), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func sendDeviceMsg(t *testing.T, conn *websocket.Conn, to, channel, data, clientMsgID string) {
	t.Helper()
	if err := conn.WriteJSON(liveMessage{
		Type:        liveTypeDeviceMsg,
		ToDeviceID:  to,
		Channel:     channel,
		Data:        data,
		ClientMsgID: clientMsgID,
	}); err != nil {
		t.Fatalf("send device.msg: %v", err)
	}
}

// waitDeviceOffline blocks until the hub has actually unregistered deviceID
// (observed as the device.offline broadcast on peer). Closing a client
// websocket returns before the server notices — sending immediately after
// would race the still-registered device and deliver into the dying socket.
func waitDeviceOffline(t *testing.T, peer *websocket.Conn, deviceID string) {
	t.Helper()
	readUntilLive(t, peer, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOffline && m.DeviceID == deviceID
	}, fmt.Sprintf("peer sees %s offline", deviceID))
}

// assertNoDeviceMsgDelivered asserts that no device.msg arrives within the
// window, tolerating unrelated presence/status traffic.
func assertNoDeviceMsgDelivered(t *testing.T, conn *websocket.Conn, what string) {
	t.Helper()
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		msg, ok := readLiveMessageTimeout(t, conn, remaining)
		if !ok {
			return
		}
		if msg.Type == liveTypeDeviceMsg {
			t.Fatalf("unexpected device.msg (%s): %+v", what, msg)
		}
	}
}

// newDeviceQueueTestServer builds a server whose device queue runs on an
// in-memory SQLite database (fast, and immune to the t.TempDir teardown
// races that file-backed live sockets can hit when teardown goroutines
// still touch the db).
func newDeviceQueueTestServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, ":0", nil, nil, nil, nil, nil, nil)
}

// setupQueuePair wires a test server with alice sessions for devices devA and
// devB plus a bob session, an HTTP server, and returns the tokens.
func setupQueuePair(t *testing.T) (*Server, string, string, string, *httptest.Server, string) {
	t.Helper()
	s := newDeviceQueueTestServer(t)
	tokA, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session: %v", err)
	}
	tokB, _, err := s.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice second session: %v", err)
	}
	tokBob, _, err := s.db.CreateSession("bob")
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}
	httpServer, wsURL := newLiveTestHTTPServer(t, s)
	return s, tokA, tokB, tokBob, httpServer, wsURL
}

// A message sent to an offline (but previously seen) device is persisted and
// reported as queued; the target receives it (with its queue id) as soon as it
// reconnects, the sender is told it was delivered, and the receiver's ack
// retires the row.
func TestDeviceQueueOfflineDelivery(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	// Give the hub time to register devB before tearing it down.
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	// devB is offline: the teleport message must queue, not drop.
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "off-q1", "cmid-1")
	queued := readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)
	if queued.QueueID <= 0 {
		t.Fatalf("queued status missing queue_id: %+v", queued)
	}
	if queued.ToDeviceID != "devB" {
		t.Fatalf("queued status to_device_id = %q, want devB", queued.ToDeviceID)
	}
	if got := queueRowCount(t, s); got != 1 {
		t.Fatalf("device_queue rows = %d, want 1", got)
	}

	// devB reconnects: it gets the message (plus the presence snapshot) in
	// FIFO order, with the queue id it needs to ack.
	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB2.Close()
	readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devA"
	}, "devB presence snapshot of devA")
	got := readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg
	}, "devB queued device.msg")
	if got.Data != "off-q1" || got.FromDeviceID != "devA" || got.ToDeviceID != "devB" ||
		got.Channel != liveDeviceChannelTeleport || got.QueueID != queued.QueueID {
		t.Fatalf("redelivered device.msg mismatch: %+v", got)
	}

	// The sender learns its queued message finally went out.
	readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusDelivered)

	// Ack retires the row.
	if err := connB2.WriteJSON(liveMessage{Type: liveTypeDeviceAck, QueueID: got.QueueID}); err != nil {
		t.Fatalf("devB ack: %v", err)
	}
	waitForQueueRowCount(t, s, 0)
}

// At-least-once contract: a delivered-but-unacked row is redelivered to the
// device's next connection, and only a correct ack retires it.
func TestDeviceQueueAckRedelivery(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "redeliver-1", "cmid-1")
	queued := readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)

	// First reconnect: delivered, but the client never acks.
	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	first := readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg && m.QueueID == queued.QueueID
	}, "first delivery")
	if first.Data != "redeliver-1" {
		t.Fatalf("first delivery data = %q", first.Data)
	}
	if err := connB2.Close(); err != nil {
		t.Fatalf("close devB2: %v", err)
	}
	if got := queueRowCount(t, s); got != 1 {
		t.Fatalf("unacked row must survive: rows = %d, want 1", got)
	}

	// Second reconnect: same row, delivered again (watermark is per
	// connection, so the fresh connection re-receives everything unacked).
	connB3 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB3.Close()
	second := readUntilLive(t, connB3, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg && m.QueueID == queued.QueueID
	}, "redelivery after reconnect")
	if second.Data != "redeliver-1" {
		t.Fatalf("redelivery data = %q", second.Data)
	}

	// A wrong-device ack must not retire the row (guard: username+device).
	if err := connB3.WriteJSON(liveMessage{Type: liveTypeDeviceAck, QueueID: queued.QueueID + 999}); err != nil {
		t.Fatalf("bogus ack: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := queueRowCount(t, s); got != 1 {
		t.Fatalf("bogus ack must not change rows: rows = %d, want 1", got)
	}

	// The correct ack retires it, and the next reconnect delivers nothing.
	if err := connB3.WriteJSON(liveMessage{Type: liveTypeDeviceAck, QueueID: queued.QueueID}); err != nil {
		t.Fatalf("devB ack: %v", err)
	}
	waitForQueueRowCount(t, s, 0)
	if err := connB3.Close(); err != nil {
		t.Fatalf("close devB3: %v", err)
	}
	connB4 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB4.Close()
	readUntilLive(t, connB4, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devA"
	}, "devB4 presence snapshot of devA")
	assertNoDeviceMsgDelivered(t, connB4, "acked row must not be redelivered")
}

// Queued messages are delivered strictly in FIFO order.
func TestDeviceQueueFIFOOrder(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	statuses := newStatusReader(connA)
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "fifo-1", "cmid-1")
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "fifo-2", "cmid-2")
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "fifo-3", "cmid-3")
	for _, id := range []string{"cmid-1", "cmid-2", "cmid-3"} {
		statuses.waitForStatus(t, id, liveDeviceStatusQueued)
	}
	if got := queueRowCount(t, s); got != 3 {
		t.Fatalf("device_queue rows = %d, want 3", got)
	}

	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB2.Close()
	var lastID int64
	for i, want := range []string{"fifo-1", "fifo-2", "fifo-3"} {
		msg := readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
			return m.Type == liveTypeDeviceMsg
		}, fmt.Sprintf("fifo delivery #%d", i+1))
		if msg.Data != want {
			t.Fatalf("delivery #%d data = %q, want %q", i+1, msg.Data, want)
		}
		if msg.QueueID <= lastID {
			t.Fatalf("delivery #%d queue_id = %d, want > %d (FIFO)", i+1, msg.QueueID, lastID)
		}
		lastID = msg.QueueID
	}
}

// Cross-user and never-announced targets are refused and never persisted.
func TestDeviceQueueCrossUserRefused(t *testing.T) {
	s, tokA, _, tokBob, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connBob := dialQueueDevice(t, wsURL, tokBob, "devBob")
	defer connBob.Close()

	// bob → alice's device: same refusal as an unknown device (no oracle for
	// which ids exist under other accounts).
	sendDeviceMsg(t, connBob, "devA", liveDeviceChannelTeleport, "evil", "cmid-bob")
	st := readDeviceStatusFor(t, connBob, "cmid-bob", liveDeviceStatusRefused)
	if !strings.Contains(st.ErrMsg, "unknown device") {
		t.Fatalf("cross-user refusal error = %q, want unknown device", st.ErrMsg)
	}

	// alice → never-announced device: refused too.
	sendDeviceMsg(t, connA, "devGhost", liveDeviceChannelTeleport, "ghost", "cmid-ghost")
	readDeviceStatusFor(t, connA, "cmid-ghost", liveDeviceStatusRefused)

	if got := queueRowCount(t, s); got != 0 {
		t.Fatalf("refused messages must not persist: rows = %d, want 0", got)
	}
}

// TTL: expired rows are neither delivered nor counted; the sweeper reclaims
// them.
func TestDeviceQueueTTLExpiry(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "expired-1", "cmid-1")
	readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)

	// Force the row past its TTL.
	if _, err := s.db.Exec(`UPDATE device_queue SET expires_at = ?`, "2000-01-01T00:00:00.000Z"); err != nil {
		t.Fatalf("expire row: %v", err)
	}

	// Reconnect: nothing is delivered (expired rows are skipped), and the
	// sweeper reclaims the row.
	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB2.Close()
	readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devA"
	}, "devB presence snapshot of devA")
	assertNoDeviceMsgDelivered(t, connB2, "expired row must not be delivered")
	if err := s.liveHub.queue.sweepExpired(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := queueRowCount(t, s); got != 0 {
		t.Fatalf("sweep must remove expired row: rows = %d, want 0", got)
	}
}

// Per-device message-count cap: excess sends are refused with an honest
// error instead of unbounded growth.
func TestDeviceQueueMessageCap(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().Device.QueueMaxMessages = 2

	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	statuses := newStatusReader(connA)
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "cap-1", "cmid-1")
	statuses.waitForStatus(t, "cmid-1", liveDeviceStatusQueued)
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "cap-2", "cmid-2")
	statuses.waitForStatus(t, "cmid-2", liveDeviceStatusQueued)
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "cap-3", "cmid-3")
	st := statuses.waitForStatus(t, "cmid-3", liveDeviceStatusRefused)
	if !strings.Contains(st.ErrMsg, "full") {
		t.Fatalf("cap refusal error = %q, want queue full", st.ErrMsg)
	}
	if got := queueRowCount(t, s); got != 2 {
		t.Fatalf("device_queue rows = %d, want 2 (cap)", got)
	}
}

// The pairing channel is live-only: an offline target is an immediate
// refusal (no stale delayed prompts), an online target gets the frame
// directly (no queue id, nothing persisted).
func TestDevicePairingNotQueued(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")

	// Online: direct delivery, delivered status, nothing persisted.
	sendDeviceMsg(t, connA, "devB", "pairing", "pair-req", "cmid-p1")
	got := readUntilLive(t, connB, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg && m.Channel == "pairing"
	}, "pairing frame direct delivery")
	if got.Data != "pair-req" || got.QueueID != 0 {
		t.Fatalf("pairing direct delivery: %+v", got)
	}
	readDeviceStatusFor(t, connA, "cmid-p1", liveDeviceStatusDelivered)
	if got := queueRowCount(t, s); got != 0 {
		t.Fatalf("pairing must not persist: rows = %d, want 0", got)
	}

	// Offline: refused fast instead of silently dropped.
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")
	sendDeviceMsg(t, connA, "devB", "pairing", "pair-req-2", "cmid-p2")
	st := readDeviceStatusFor(t, connA, "cmid-p2", liveDeviceStatusRefused)
	if !strings.Contains(st.ErrMsg, "offline") {
		t.Fatalf("pairing refusal error = %q, want offline hint", st.ErrMsg)
	}
	if got := queueRowCount(t, s); got != 0 {
		t.Fatalf("pairing must not persist: rows = %d, want 0", got)
	}
}

// An online target receives immediately, but the row survives until the
// receiver acks — the at-least-once core (a connection dying between
// delivery and processing redelivers on reconnect).
func TestDeviceQueueOnlineDeliveredUntilAck(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB.Close()
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")

	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "online-1", "cmid-1")
	got := readUntilLive(t, connB, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg
	}, "online delivery")
	if got.Data != "online-1" || got.QueueID <= 0 {
		t.Fatalf("online delivery: %+v", got)
	}
	readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusDelivered)
	if got := queueRowCount(t, s); got != 1 {
		t.Fatalf("delivered-but-unacked row must persist: rows = %d, want 1", got)
	}

	if err := connB.WriteJSON(liveMessage{Type: liveTypeDeviceAck, QueueID: got.QueueID}); err != nil {
		t.Fatalf("devB ack: %v", err)
	}
	waitForQueueRowCount(t, s, 0)
}

// waitForRegistryEntry polls until deviceID is registered for username
// (device.hello is processed asynchronously).
func waitForRegistryEntry(t *testing.T, s *Server, username, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM device_registry WHERE username = ? AND device_id = ?`,
			username, deviceID,
		).Scan(&n); err != nil {
			t.Fatalf("registry poll: %v", err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("device %s never registered for %s", deviceID, username)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A queue ack from a different user's device must never retire another
// user's row (the ack is guarded by username + target device id).
func TestDeviceQueueAckGuard(t *testing.T) {
	s, tokA, tokB, tokBob, _, wsURL := setupQueuePair(t)

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")
	connBob := dialQueueDevice(t, wsURL, tokBob, "devBob")
	defer connBob.Close()

	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "guard-1", "cmid-1")
	queued := readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)

	// bob's device acks alice's queue id: must be ignored.
	if err := connBob.WriteJSON(liveMessage{Type: liveTypeDeviceAck, QueueID: queued.QueueID}); err != nil {
		t.Fatalf("bob ack: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := queueRowCount(t, s); got != 1 {
		t.Fatalf("cross-user ack must not retire the row: rows = %d, want 1", got)
	}
}

// GET /devices reports the user's registry with live presence and pending
// queue footprints.
func TestDevicesEndpoint(t *testing.T) {
	s, tokA, tokB, tokBob, httpServer, wsURL := setupQueuePair(t)
	_ = httpServer

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")
	connBob := dialQueueDevice(t, wsURL, tokBob, "devBob")
	defer connBob.Close()
	// bob's hello is processed asynchronously — wait for the registry row
	// before asserting the endpoint output.
	waitForRegistryEntry(t, s, "bob", "devBob")

	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "api-1", "cmid-1")
	readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)

	get := func(token string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/devices", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.handleDevices(w, req)
		return w.Code, w.Body.String()
	}

	code, body := get(tokA)
	if code != http.StatusOK {
		t.Fatalf("GET /devices = %d: %s", code, body)
	}
	// devA online with nothing queued, devB offline holding 1 message.
	if !strings.Contains(body, `"device_id":"devA"`) || !strings.Contains(body, `"online":true`) {
		t.Fatalf("devA must be listed online: %s", body)
	}
	if !strings.Contains(body, `"device_id":"devB"`) || !strings.Contains(body, `"online":false`) ||
		!strings.Contains(body, `"queued":1`) {
		t.Fatalf("devB must be listed offline with 1 queued: %s", body)
	}
	if strings.Contains(body, "devBob") {
		t.Fatalf("alice must not see bob's devices: %s", body)
	}

	// bob sees only his own device.
	code, body = get(tokBob)
	if code != http.StatusOK {
		t.Fatalf("GET /devices (bob) = %d: %s", code, body)
	}
	if !strings.Contains(body, `"device_id":"devBob"`) || strings.Contains(body, "devA") {
		t.Fatalf("bob must see only devBob: %s", body)
	}

	// Method guard.
	req := httptest.NewRequest(http.MethodPost, "/devices", nil)
	req.Header.Set("Authorization", "Bearer "+tokA)
	w := httptest.NewRecorder()
	s.handleDevices(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /devices = %d, want 405", w.Code)
	}
}

// device.online/offline presence frames now carry the device's pending queue
// count so peers can badge "waiting to deliver".
func TestDevicePresenceCarriesQueuedCount(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)
	_ = s // presence counts are asserted from the wire, not the DB

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	statuses := newStatusReader(connA)
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "presence-1", "cmid-1")
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "presence-2", "cmid-2")
	statuses.waitForStatus(t, "cmid-1", liveDeviceStatusQueued)
	statuses.waitForStatus(t, "cmid-2", liveDeviceStatusQueued)

	// devB comes back: its own hello broadcast must report the 2 queued
	// messages... to its peers (broadcast excludes the announcer, so read it
	// on devA).
	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB2.Close()
	online := readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB back online")
	if online.Queued != 2 {
		t.Fatalf("device.online queued = %d, want 2", online.Queued)
	}
	// Drain devB2's snapshot + deliveries before close so the offline frame
	// is not lost to a racy teardown.
	readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg
	}, "devB2 deliveries")
}

// waitHubIdle polls until the hub has no registered clients or devices left
// (all readLoop goroutines finished their unregister work), so a test can
// close the database deterministically.
func waitHubIdle(t *testing.T, h *liveHub) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.RLock()
		n := len(h.clients) + len(h.devices)
		h.mu.RUnlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("live hub never went idle: %d clients/devices left", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The queue is persisted, not in-memory: a message queued for an offline
// device survives a full server restart (new process, fresh hub) and is
// delivered when the device reconnects to the new server.
func TestDeviceQueueSurvivesServerRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart.db")

	// First server incarnation: devA and devB announce, devB goes offline
	// with one message queued for it.
	d1, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	s1 := New(d1, ":0", nil, nil, nil, nil, nil, nil)
	tokA1, _, err := s1.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session: %v", err)
	}
	tokB1, _, err := s1.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice second session: %v", err)
	}
	http1, wsURL1 := newLiveTestHTTPServer(t, s1)

	connA := dialQueueDevice(t, wsURL1, tokA1, "devA")
	connB := dialQueueDevice(t, wsURL1, tokB1, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")
	sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport, "restart-1", "cmid-1")
	readDeviceStatusFor(t, connA, "cmid-1", liveDeviceStatusQueued)

	// Deterministic teardown of the first incarnation: no live goroutines
	// may touch the db once it closes.
	if err := connA.Close(); err != nil {
		t.Fatalf("close devA: %v", err)
	}
	waitHubIdle(t, s1.liveHub)
	http1.Close()
	if err := d1.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Second incarnation on the same database file: devB reconnects and
	// receives the message the previous process had queued for it.
	d2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { d2.Close() })
	s2 := New(d2, ":0", nil, nil, nil, nil, nil, nil)
	tokB2, _, err := s2.db.CreateSession("alice")
	if err != nil {
		t.Fatalf("create alice session (second server): %v", err)
	}
	_, wsURL2 := newLiveTestHTTPServer(t, s2)

	connB2 := dialQueueDevice(t, wsURL2, tokB2, "devB")
	defer connB2.Close()
	msg := readUntilLive(t, connB2, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceMsg
	}, "queued message after restart")
	if msg.Data != "restart-1" || msg.FromDeviceID != "devA" || msg.QueueID <= 0 {
		t.Fatalf("post-restart delivery mismatch: %+v", msg)
	}
}

// A backlog larger than the connection's send buffer (64) drains completely
// and in order: the drain blocks on backpressure while the target's write
// loop catches up, and the per-device lock serializes the concurrent
// enqueue-triggered drains.
func TestDeviceQueueLargeBacklogDrainsInOrder(t *testing.T) {
	s, tokA, tokB, _, _, wsURL := setupQueuePair(t)
	_ = s

	connA := dialQueueDevice(t, wsURL, tokA, "devA")
	defer connA.Close()
	connB := dialQueueDevice(t, wsURL, tokB, "devB")
	readUntilLive(t, connA, 3*time.Second, func(m liveMessage) bool {
		return m.Type == liveTypeDeviceOnline && m.DeviceID == "devB"
	}, "devA sees devB online")
	if err := connB.Close(); err != nil {
		t.Fatalf("close devB: %v", err)
	}
	waitDeviceOffline(t, connA, "devB")

	const backlog = 80 // > send-channel buffer (64)
	statuses := newStatusReader(connA)
	for i := 0; i < backlog; i++ {
		sendDeviceMsg(t, connA, "devB", liveDeviceChannelTeleport,
			fmt.Sprintf("backlog-%02d", i), fmt.Sprintf("cmid-%02d", i))
	}
	for i := 0; i < backlog; i++ {
		statuses.waitForStatus(t, fmt.Sprintf("cmid-%02d", i), liveDeviceStatusQueued)
	}

	connB2 := dialQueueDevice(t, wsURL, tokB, "devB")
	defer connB2.Close()
	last := int64(0)
	for i := 0; i < backlog; i++ {
		msg := readUntilLive(t, connB2, 5*time.Second, func(m liveMessage) bool {
			return m.Type == liveTypeDeviceMsg
		}, fmt.Sprintf("backlog delivery #%d", i+1))
		want := fmt.Sprintf("backlog-%02d", i)
		if msg.Data != want {
			t.Fatalf("delivery #%d data = %q, want %q", i+1, msg.Data, want)
		}
		if msg.QueueID <= last {
			t.Fatalf("delivery #%d queue_id = %d, want > %d (FIFO)", i+1, msg.QueueID, last)
		}
		last = msg.QueueID
	}
}
