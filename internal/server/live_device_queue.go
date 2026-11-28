package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/internal/db"
)

// Device-to-device pending-message queue (see live.go for the relay).
//
// The queue gives the device channel offline delivery: a device.msg addressed
// to a device that is currently offline is persisted (opaque ciphertext — the
// server never interprets payloads) and drained in FIFO order when the target
// announces itself again (device.hello). Rows are deleted only when the
// receiving device acks them, so a connection that dies mid-drain redelivers
// on the next reconnect (at-least-once; the frontend reassembly is
// idempotent). Undelivered rows expire after the configured TTL ("a few
// minutes" by default) and are swept by the janitor.
//
// The registry (device_id → username) persists every announced device id, so
// the same-user authorization guard also holds while a target is offline: a
// message may only be queued for a device that is known to belong to the
// sender's own account. Cross-user and unknown targets are refused.

const (
	deviceQueueDefaultTTL         = 15 * time.Minute
	deviceQueueDefaultMaxMessages = 256
	deviceQueueDefaultMaxBytes    = int64(32 << 20)

	// Matches the DATETIME default format used by the schema
	// (strftime('%Y-%m-%dT%H:%M:%fZ','now')) so lexicographic comparisons in
	// SQL line up with Go-generated timestamps.
	deviceQueueTimeLayout = "2006-01-02T15:04:05.000Z"
)

// ErrDeviceQueueFull is returned by enqueue when either per-device cap
// (message count or total bytes) would be exceeded.
var ErrDeviceQueueFull = errors.New("device queue is full")

// ErrDeviceQueueClosed wraps transient database failures.
var ErrDeviceQueueUnavailable = errors.New("device queue unavailable")

type deviceQueueConfig struct {
	TTL         time.Duration
	MaxMessages int
	MaxBytes    int64
}

func (c deviceQueueConfig) normalized() deviceQueueConfig {
	if c.TTL <= 0 {
		c.TTL = deviceQueueDefaultTTL
	}
	if c.MaxMessages <= 0 {
		c.MaxMessages = deviceQueueDefaultMaxMessages
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = deviceQueueDefaultMaxBytes
	}
	return c
}

// deviceQueueConfigFromGlobal reads the [device] config section, applying
// defaults for unset (zero) values.
func deviceQueueConfigFromGlobal() deviceQueueConfig {
	cfg := config.Get().Device
	return deviceQueueConfig{
		TTL:         time.Duration(cfg.QueueTTLMinutes) * time.Minute,
		MaxMessages: cfg.QueueMaxMessages,
		MaxBytes:    cfg.QueueMaxBytes,
	}.normalized()
}

// deviceQueuedMsg is one persisted device.msg row.
type deviceQueuedMsg struct {
	ID           int64
	ToDeviceID   string
	FromDeviceID string
	Channel      string
	ClientMsgID  string
	Data         string
}

// deviceQueueStat is the pending-queue footprint of one device.
type deviceQueueStat struct {
	Messages int64
	Bytes    int64
}

type deviceQueue struct {
	db  *sql.DB
	cfg deviceQueueConfig
}

func newDeviceQueue(db *sql.DB, cfg deviceQueueConfig) *deviceQueue {
	return &deviceQueue{db: db, cfg: cfg.normalized()}
}

func deviceQueueNowUTC() string {
	return time.Now().UTC().Format(deviceQueueTimeLayout)
}

// registerSeen upserts the device registry: the device id's owning username
// and the time it was last announced. A device re-announcing under a new
// username takes over the id (last wins, matching the live registry).
func (q *deviceQueue) registerSeen(username, deviceID string) error {
	if username == "" || deviceID == "" {
		return fmt.Errorf("device registry: empty username or device id")
	}
	_, err := q.db.Exec(`
		INSERT INTO device_registry (device_id, username, last_seen_at)
		VALUES (?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			username = excluded.username,
			last_seen_at = excluded.last_seen_at
	`, deviceID, username, deviceQueueNowUTC())
	if err != nil {
		return fmt.Errorf("device registry upsert: %w", err)
	}
	return nil
}

// ownerOf returns the username that owns deviceID, or "" when the device has
// never announced itself.
func (q *deviceQueue) ownerOf(deviceID string) (string, error) {
	var username string
	err := q.db.QueryRow(`SELECT username FROM device_registry WHERE device_id = ?`, deviceID).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("device registry lookup: %w", err)
	}
	return username, nil
}

// enqueue persists one device.msg and returns its queue id (the FIFO
// sequence). Expired rows are swept first so they no longer count towards the
// caps. ErrDeviceQueueFull is returned when either cap would be exceeded;
// ErrDeviceQueueUnavailable wraps transient DB failures.
func (q *deviceQueue) enqueue(username, fromDeviceID, toDeviceID, channel, clientMsgID, data string) (int64, error) {
	now := time.Now().UTC()
	nowStr := now.Format(deviceQueueTimeLayout)
	expiresStr := now.Add(q.cfg.TTL).Format(deviceQueueTimeLayout)

	if err := q.sweepExpiredAt(nowStr); err != nil {
		return 0, err
	}

	var count, bytes int64
	err := q.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(LENGTH(COALESCE(data, ''))), 0)
		FROM device_queue
		WHERE username = ? AND to_device_id = ? AND expires_at > ?
	`, username, toDeviceID, nowStr).Scan(&count, &bytes)
	if err != nil {
		return 0, fmt.Errorf("%w: count: %v", ErrDeviceQueueUnavailable, err)
	}
	if count+1 > int64(q.cfg.MaxMessages) || bytes+int64(len(data)) > q.cfg.MaxBytes {
		return 0, ErrDeviceQueueFull
	}

	res, err := q.db.Exec(`
		INSERT INTO device_queue
			(username, to_device_id, from_device_id, channel, client_msg_id, data, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, username, toDeviceID, fromDeviceID, channel, clientMsgID, data, nowStr, expiresStr)
	if err != nil {
		return 0, fmt.Errorf("%w: insert: %v", ErrDeviceQueueUnavailable, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("%w: insert id: %v", ErrDeviceQueueUnavailable, err)
	}
	return id, nil
}

// pendingAfter returns up to limit unexpired rows for one target device with
// id > afterID, in FIFO (id) order. afterID is the delivery watermark of the
// target's current connection: rows at or below it were already handed to
// that connection and must not be re-sent on it (at-least-once redelivery
// happens per connection, not within one).
func (q *deviceQueue) pendingAfter(username, toDeviceID string, afterID int64, limit int) ([]deviceQueuedMsg, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := q.db.Query(`
		SELECT id, to_device_id, from_device_id, channel, client_msg_id, data
		FROM device_queue
		WHERE username = ? AND to_device_id = ? AND id > ? AND expires_at > ?
		ORDER BY id
		LIMIT ?
	`, username, toDeviceID, afterID, deviceQueueNowUTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("%w: pending: %v", ErrDeviceQueueUnavailable, err)
	}
	defer rows.Close()
	var out []deviceQueuedMsg
	for rows.Next() {
		var m deviceQueuedMsg
		if err := rows.Scan(&m.ID, &m.ToDeviceID, &m.FromDeviceID, &m.Channel, &m.ClientMsgID, &m.Data); err != nil {
			return nil, fmt.Errorf("%w: pending scan: %v", ErrDeviceQueueUnavailable, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ack deletes one delivered row. Guarded by the acking device's username and
// device id, so a connection can only retire messages addressed to itself.
// Unknown ids (duplicate or late acks) are not an error.
func (q *deviceQueue) ack(username, toDeviceID string, queueID int64) error {
	if queueID <= 0 || username == "" || toDeviceID == "" {
		return nil
	}
	if _, err := q.db.Exec(`
		DELETE FROM device_queue WHERE id = ? AND username = ? AND to_device_id = ?
	`, queueID, username, toDeviceID); err != nil {
		return fmt.Errorf("%w: ack: %v", ErrDeviceQueueUnavailable, err)
	}
	return nil
}

// sweepExpired removes TTL-past rows; called opportunistically on enqueue and
// periodically by the janitor.
func (q *deviceQueue) sweepExpired() error {
	return q.sweepExpiredAt(deviceQueueNowUTC())
}

func (q *deviceQueue) sweepExpiredAt(nowStr string) error {
	if _, err := q.db.Exec(`DELETE FROM device_queue WHERE expires_at <= ?`, nowStr); err != nil {
		return fmt.Errorf("%w: sweep: %v", ErrDeviceQueueUnavailable, err)
	}
	return nil
}

// pendingStatsFor returns per-device pending queue footprints for one user
// (device id → stat). Devices with nothing queued are absent.
func (q *deviceQueue) pendingStatsFor(username string) (map[string]deviceQueueStat, error) {
	rows, err := q.db.Query(`
		SELECT to_device_id, COUNT(*), COALESCE(SUM(LENGTH(COALESCE(data, ''))), 0)
		FROM device_queue
		WHERE username = ? AND expires_at > ?
		GROUP BY to_device_id
	`, username, deviceQueueNowUTC())
	if err != nil {
		return nil, fmt.Errorf("%w: stats: %v", ErrDeviceQueueUnavailable, err)
	}
	defer rows.Close()
	out := make(map[string]deviceQueueStat)
	for rows.Next() {
		var id string
		var st deviceQueueStat
		if err := rows.Scan(&id, &st.Messages, &st.Bytes); err != nil {
			return nil, fmt.Errorf("%w: stats scan: %v", ErrDeviceQueueUnavailable, err)
		}
		out[id] = st
	}
	return out, rows.Err()
}

// pendingCountFor is the queued-message count for one device of one user.
func (q *deviceQueue) pendingCountFor(username, deviceID string) int64 {
	var n int64
	_ = q.db.QueryRow(`
		SELECT COUNT(*) FROM device_queue
		WHERE username = ? AND to_device_id = ? AND expires_at > ?
	`, username, deviceID, deviceQueueNowUTC()).Scan(&n)
	return n
}

// listDevices returns the registry rows for one username, newest activity
// first, for the GET /devices endpoint. The caller layers live presence on
// top.
type deviceRegistryEntry struct {
	DeviceID    string `json:"device_id"`
	Online      bool   `json:"online"`
	LastSeenAt  string `json:"last_seen_at"`
	Queued      int64  `json:"queued"`
	QueuedBytes int64  `json:"queued_bytes"`
}

func (q *deviceQueue) listDevices(username string) ([]deviceRegistryEntry, error) {
	rows, err := q.db.Query(`
		SELECT device_id, last_seen_at FROM device_registry
		WHERE username = ?
		ORDER BY last_seen_at DESC
	`, username)
	if err != nil {
		return nil, fmt.Errorf("%w: list: %v", ErrDeviceQueueUnavailable, err)
	}
	defer rows.Close()
	var out []deviceRegistryEntry
	seen := make(map[string]struct{})
	for rows.Next() {
		var e deviceRegistryEntry
		if err := rows.Scan(&e.DeviceID, &e.LastSeenAt); err != nil {
			return nil, fmt.Errorf("%w: list scan: %v", ErrDeviceQueueUnavailable, err)
		}
		if _, dup := seen[e.DeviceID]; dup {
			continue
		}
		seen[e.DeviceID] = struct{}{}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: list: %v", ErrDeviceQueueUnavailable, err)
	}
	stats, err := q.pendingStatsFor(username)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if st, ok := stats[out[i].DeviceID]; ok {
			out[i].Queued = st.Messages
			out[i].QueuedBytes = st.Bytes
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// GET /devices — registry + presence + queue footprint for the session user
// ---------------------------------------------------------------------------

// onlineDeviceIDs returns the user's currently-connected device ids.
func (h *liveHub) onlineDeviceIDs(username string) map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]bool, len(h.devices))
	for id, c := range h.devices {
		if c.username == username {
			out[id] = true
		}
	}
	return out
}

// handleDevices serves GET /devices: every device this account has announced
// (via device.hello), with live presence and pending-queue counts.
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username, _, err := s.sessionUsername(r)
	if errors.Is(err, db.ErrNotFound) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	} else if err != nil {
		writeErr(w, err)
		return
	}
	if s.liveHub == nil || s.liveHub.queue == nil {
		http.Error(w, "device channel unavailable", http.StatusServiceUnavailable)
		return
	}
	entries, err := s.liveHub.queue.listDevices(username)
	if err != nil {
		writeErr(w, err)
		return
	}
	online := s.liveHub.onlineDeviceIDs(username)
	for i := range entries {
		entries[i].Online = online[entries[i].DeviceID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": entries})
}
