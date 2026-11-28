package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/jobs"
)

const (
	liveTypeReady                  = "live.ready"
	liveTypeNotesChange            = "notes.changed"
	liveTypeJobsChange             = "jobs.changed"
	liveTypeJobsProgress           = "jobs.progress"
	liveTypePluginProgress         = "plugin.progress"
	liveReasonInlineUploadResolved = "inline_upload_resolved"
	liveTypeEditSync               = "edit.sync"
	liveTypeEditBody               = "edit.body"
	liveTypeDeviceHello            = "device.hello"
	liveTypeDeviceMsg              = "device.msg"
	liveTypeDeviceAck              = "device.ack"
	liveTypeDeviceStatus           = "device.status"
	liveTypeDeviceOnline           = "device.online"
	liveTypeDeviceOffline          = "device.offline"

	// liveDeviceChannelTeleport is the E2E-encrypted payload channel. It is
	// the only channel eligible for offline queueing: pairing frames carry a
	// live mutual-consent handshake, so delivering them minutes later would
	// only produce stale prompts — they stay direct-only.
	liveDeviceChannelTeleport = "teleport"

	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = (wsPongWait * 9) / 10
	// Must accommodate live edit-body payloads (note markdown), which can be
	// larger than tiny control messages.
	wsMaxMessageSize = 4 << 20
)

type liveUploadResolution struct {
	NoteID           int64     `json:"note_id"`
	PlaceholderToken string    `json:"placeholder_token,omitempty"`
	Markdown         string    `json:"markdown,omitempty"`
	File             *NoteFile `json:"file,omitempty"`
}

// livePluginProgress carries a plugin-streamed progress payload (see
// notetype.ProgressReporter): the plugin chains its own JSON through Data.
type livePluginProgress struct {
	PluginID string          `json:"plugin_id"`
	Data     json.RawMessage `json:"data"`
}

type liveMessage struct {
	Type               string                `json:"type"`
	Timestamp          string                `json:"timestamp,omitempty"`
	Reason             string                `json:"reason,omitempty"`
	NoteIDs            []int64               `json:"note_ids,omitempty"`
	UploadResolution   *liveUploadResolution `json:"upload_resolution,omitempty"`
	Job                *jobs.RunEvent        `json:"job,omitempty"`
	PluginProgress     *livePluginProgress   `json:"plugin_progress,omitempty"`
	ClientSentAtMS     *float64              `json:"client_sent_at_ms,omitempty"`
	ServerReceivedAtUS int64                 `json:"server_received_at_us,omitempty"`
	ServerSentAtUS     int64                 `json:"server_sent_at_us,omitempty"`
	NoteID             int64                 `json:"note_id,omitempty"`
	Editing            *bool                 `json:"editing,omitempty"`
	DeviceID           string                `json:"device_id,omitempty"`
	Body               string                `json:"body,omitempty"`
	ToDeviceID         string                `json:"to_device_id,omitempty"`
	FromDeviceID       string                `json:"from_device_id,omitempty"`
	Channel            string                `json:"channel,omitempty"`
	Data               string                `json:"data,omitempty"`
	ClientMsgID        string                `json:"client_msg_id,omitempty"`
	QueueID            int64                 `json:"queue_id,omitempty"`
	Queued             int64                 `json:"queued,omitempty"`
	Status             string                `json:"status,omitempty"`
	ErrMsg             string                `json:"error,omitempty"`
}

type liveHub struct {
	mu       sync.RWMutex
	clients  map[*liveClient]struct{}
	devices  map[string]*liveClient
	upgrader websocket.Upgrader

	// queue is the persisted device-message queue (nil = queue disabled;
	// device.msg then falls back to direct-only routing). Attached once at
	// server construction; never mutated afterwards.
	queue *deviceQueue

	// drainLocks serializes queue drains per target device id so concurrent
	// triggers (enqueue, device.hello) cannot interleave deliveries or
	// double-advance a connection's watermark.
	drainMu    sync.Mutex
	drainLocks map[string]*sync.Mutex
}

type liveClient struct {
	hub      *liveHub
	conn     *websocket.Conn
	send     chan liveMessage
	stop     chan struct{}
	username string
	deviceID string

	// queueWatermark is the highest queue id handed to this connection
	// (guarded by the per-device drain lock): rows at or below it were
	// already delivered on this connection and must not be re-sent on it.
	// A fresh connection starts at 0 and therefore re-receives everything
	// not yet acked — the at-least-once redelivery contract.
	queueWatermark int64
}

func newLiveHub() *liveHub {
	return &liveHub{
		clients:    make(map[*liveClient]struct{}),
		devices:    make(map[string]*liveClient),
		drainLocks: make(map[string]*sync.Mutex),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
}

// attachDeviceQueue wires the persisted queue into the hub. Must be called
// before the hub serves connections (server construction time).
func (h *liveHub) attachDeviceQueue(q *deviceQueue) {
	h.queue = q
}

// startJanitor periodically sweeps expired queue rows until ctx is done.
// Safe to call on a hub without a queue (no-op).
func (h *liveHub) startJanitor(ctx context.Context) {
	if h == nil || h.queue == nil {
		return
	}
	go func() {
		if err := h.queue.sweepExpired(); err != nil {
			log.Printf("device queue: initial sweep: %v", err)
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.queue.sweepExpired(); err != nil {
					log.Printf("device queue: sweep: %v", err)
				}
			}
		}
	}()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
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
	if s.liveHub == nil {
		http.Error(w, "live updates unavailable", http.StatusServiceUnavailable)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if err := s.validateTrustedOrigin(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	conn, err := s.liveHub.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &liveClient{
		hub:      s.liveHub,
		conn:     conn,
		send:     make(chan liveMessage, 64),
		stop:     make(chan struct{}),
		username: username,
	}
	s.liveHub.register(client)
	client.enqueueJSON(liveMessage{Type: liveTypeReady, Timestamp: liveTimestamp()})
	go client.writeLoop()
	client.readLoop()
}

func (s *Server) notifyNotesChanged(reason string, noteIDs ...int64) {
	if s == nil || s.liveHub == nil {
		return
	}
	normalized := uniquePositiveNoteIDs(noteIDs)
	if len(normalized) == 0 {
		return
	}
	s.liveHub.broadcast(liveMessage{
		Type:      liveTypeNotesChange,
		Timestamp: liveTimestamp(),
		Reason:    reason,
		NoteIDs:   normalized,
	})
}

func (s *Server) notifyJobEvent(evt jobs.RunEvent) {
	if s == nil || s.liveHub == nil {
		return
	}
	liveType := liveTypeJobsChange
	if evt.Type == "progress" {
		liveType = liveTypeJobsProgress
	}
	copied := evt
	s.liveHub.broadcast(liveMessage{
		Type:      liveType,
		Timestamp: liveTimestamp(),
		Job:       &copied,
	})
}

// broadcastPluginProgress relays a plugin's streamed progress payload (see
// notetype.ProgressReporter) to all connected clients as a plugin.progress
// live message. Pure relay: the server never interprets the plugin's JSON.
func (s *Server) broadcastPluginProgress(pluginID string, payload json.RawMessage) {
	if s == nil || s.liveHub == nil || len(payload) == 0 {
		return
	}
	s.liveHub.broadcast(liveMessage{
		Type:      liveTypePluginProgress,
		Timestamp: liveTimestamp(),
		PluginProgress: &livePluginProgress{
			PluginID: pluginID,
			Data:     payload,
		},
	})
}

func (s *Server) notifyInlineUploadResolved(noteID int64, placeholderToken, markdown string, file *NoteFile) {
	if s == nil || s.liveHub == nil || noteID <= 0 {
		return
	}

	var fileCopy *NoteFile
	if file != nil {
		copied := *file
		fileCopy = &copied
	}

	s.liveHub.broadcast(liveMessage{
		Type:      liveTypeNotesChange,
		Timestamp: liveTimestamp(),
		Reason:    liveReasonInlineUploadResolved,
		NoteIDs:   []int64{noteID},
		UploadResolution: &liveUploadResolution{
			NoteID:           noteID,
			PlaceholderToken: placeholderToken,
			Markdown:         markdown,
			File:             fileCopy,
		},
	})
}

func (h *liveHub) register(client *liveClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
}

func (h *liveHub) registerDevice(c *liveClient, deviceID string) {
	// Persist the registry entry BEFORE anything is broadcast: from this
	// point on peers may queue messages for the device even while it is
	// offline. A failure is logged but does not block the live path.
	if h.queue != nil {
		if err := h.queue.registerSeen(c.username, deviceID); err != nil {
			log.Printf("device registry: %v", err)
		}
	}

	h.mu.Lock()
	if c.deviceID != "" && h.devices[c.deviceID] == c {
		delete(h.devices, c.deviceID)
	}
	c.deviceID = deviceID
	h.devices[deviceID] = c // last connection wins if the same id reconnects
	// Presence state sync: snapshot the current same-user registry so the
	// (re)announcing connection immediately learns who else is online.
	// device.online broadcasts only fire when a device connects, so without
	// this a fresh hello would know nothing about devices that announced
	// while it was away (e.g. after a reload or right after pairing).
	online := make([]string, 0, len(h.devices))
	for id, cl := range h.devices {
		if id != deviceID && cl.username == c.username {
			online = append(online, id)
		}
	}
	h.mu.Unlock()

	var queued int64
	if h.queue != nil {
		queued = h.queue.pendingCountFor(c.username, deviceID)
	}
	h.broadcastToUser(c.username, c, liveMessage{
		Type:     liveTypeDeviceOnline,
		DeviceID: deviceID,
		Queued:   queued,
	})

	// Deliver the snapshot to this connection only (its own writeLoop drains
	// the send channel; do not broadcast — the peers already learned about
	// this device via the broadcast above).
	for _, id := range online {
		c.enqueueJSON(liveMessage{Type: liveTypeDeviceOnline, DeviceID: id})
	}

	// Drain everything queued for this device while it was away, in FIFO
	// order (asynchronously: a large backlog must not block the hello).
	if h.queue != nil {
		go h.drainDevice(c.username, deviceID, 0, nil, "")
	}
}

func (h *liveHub) routeToDevice(toDeviceID string, from *liveClient, msg liveMessage) {
	h.mu.RLock()
	target := h.devices[toDeviceID]
	h.mu.RUnlock()
	if target == nil || target == from || target.username != from.username {
		return // unknown, self, or cross-user: silently drop
	}
	target.enqueueJSON(msg)
}

// handleDeviceMsg processes one inbound device.msg from a registered sender
// connection. With a queue attached, teleport-channel frames are persisted
// and drained to the target (online now or at its next hello); the sender
// receives a device.status frame with the honest per-message outcome:
//
//	delivered — handed to the target's live connection (still deleted only
//	            on the receiver's ack)
//	queued    — target offline; persisted until its next hello or the TTL
//	refused   — unknown device, cross-user target, caps exceeded, or a
//	            direct-only channel (pairing) with an offline target
//
// Without a queue the hub falls back to the legacy direct-only relay.
func (h *liveHub) handleDeviceMsg(from *liveClient, m liveMessage) {
	if m.ToDeviceID == "" || from.deviceID == "" {
		return // malformed or unannounced sender: ignore
	}
	if m.ToDeviceID == from.deviceID {
		return // self-addressed: ignore (matches the legacy relay guard)
	}
	msg := liveMessage{
		Type:         liveTypeDeviceMsg,
		ToDeviceID:   m.ToDeviceID,
		FromDeviceID: from.deviceID,
		Channel:      m.Channel,
		Data:         m.Data,
	}
	if h.queue == nil {
		h.routeToDevice(m.ToDeviceID, from, msg)
		return
	}

	if m.Channel != liveDeviceChannelTeleport {
		// Direct-only channels (pairing's live consent handshake): never
		// persisted, so an offline target is an immediate refusal instead
		// of a silent drop or a stale delayed prompt.
		target := h.lookupDeviceConn(from.username, m.ToDeviceID)
		if target == nil {
			h.sendStatus(from, liveDeviceStatusRefused, 0, m.ClientMsgID, m.ToDeviceID,
				"device is offline (channel is delivered live only)")
			return
		}
		h.deliver(target, msg)
		h.sendStatus(from, liveDeviceStatusDelivered, 0, m.ClientMsgID, m.ToDeviceID, "")
		return
	}

	// Queued channel: authorize the target (known registry device owned by
	// the sender's own account) before persisting anything.
	owner, err := h.queue.ownerOf(m.ToDeviceID)
	if err != nil {
		log.Printf("device queue: target lookup: %v", err)
		h.sendStatus(from, liveDeviceStatusRefused, 0, m.ClientMsgID, m.ToDeviceID, "queue unavailable")
		return
	}
	if owner != from.username {
		// Unknown device and cross-user targeting share one refusal —
		// no oracle for which device ids exist under other accounts.
		h.sendStatus(from, liveDeviceStatusRefused, 0, m.ClientMsgID, m.ToDeviceID, "unknown device")
		return
	}
	queueID, err := h.queue.enqueue(from.username, from.deviceID, m.ToDeviceID, m.Channel, m.ClientMsgID, m.Data)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceQueueFull):
			h.sendStatus(from, liveDeviceStatusRefused, 0, m.ClientMsgID, m.ToDeviceID,
				"device queue is full — try again after it picks up its backlog")
		default:
			log.Printf("device queue: enqueue: %v", err)
			h.sendStatus(from, liveDeviceStatusRefused, 0, m.ClientMsgID, m.ToDeviceID, "queue unavailable")
		}
		return
	}

	// Deliver now if the target happens to be online. The drain owns the
	// sender's status frame: it knows whether the row actually reached a
	// live connection (delivered) or is still pending (queued).
	go h.drainDevice(from.username, m.ToDeviceID, queueID, from, m.ClientMsgID)
}

// Device status values reported back to senders.
const (
	liveDeviceStatusDelivered = "delivered"
	liveDeviceStatusQueued    = "queued"
	liveDeviceStatusRefused   = "refused"
)

// lookupDeviceConn returns the registered live connection for deviceID when
// it currently belongs to username, else nil.
func (h *liveHub) lookupDeviceConn(username, deviceID string) *liveClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.devices[deviceID]
	if c == nil || c.username != username {
		return nil
	}
	return c
}

// deviceDrainLock returns the per-device serialization lock for queue drains.
func (h *liveHub) deviceDrainLock(deviceID string) *sync.Mutex {
	h.drainMu.Lock()
	defer h.drainMu.Unlock()
	mu, ok := h.drainLocks[deviceID]
	if !ok {
		mu = &sync.Mutex{}
		h.drainLocks[deviceID] = mu
	}
	return mu
}

// drainDevice delivers pending queue rows for one target device in FIFO
// order. It is the only writer of a connection's queueWatermark and is
// serialized per device by deviceDrainLock, so deliveries from concurrent
// triggers (enqueue, hello) never interleave or reorder.
//
// triggerID identifies the row whose enqueue spawned this drain (0 for
// hello-triggered drains): if the drain hands that row to a live connection,
// its sender gets a `delivered` status here; otherwise the fallback status is
// `queued`. Every row the drain delivers also earns its own sender a
// `delivered` status, so a device that comes back online effectively
// acknowledges everything that left its queue.
//
// deliverQueued applies real backpressure: a slow target blocks the drain
// (on its own goroutine) instead of overflowing the send buffer, and an
// evicted/unregistered target stops the drain, leaving rows pending for the
// next hello.
func (h *liveHub) drainDevice(username, toDeviceID string, triggerID int64, sender *liveClient, clientMsgID string) {
	if h.queue == nil {
		return
	}
	mu := h.deviceDrainLock(toDeviceID)
	mu.Lock()
	defer mu.Unlock()

	triggerDelivered := false
	for {
		target := h.lookupDeviceConn(username, toDeviceID)
		if target == nil {
			break // offline: rows stay queued for the next hello
		}
		rows, err := h.queue.pendingAfter(username, toDeviceID, target.queueWatermark, 1)
		if err != nil {
			log.Printf("device queue: drain: %v", err)
			break
		}
		if len(rows) == 0 {
			break
		}
		row := rows[0]
		msg := liveMessage{
			Type:         liveTypeDeviceMsg,
			ToDeviceID:   row.ToDeviceID,
			FromDeviceID: row.FromDeviceID,
			Channel:      row.Channel,
			Data:         row.Data,
			QueueID:      row.ID,
		}
		if !h.deliverQueued(target, msg) {
			break // connection gone: row stays queued for the next hello
		}
		target.queueWatermark = row.ID
		h.notifyRowDelivered(username, row)
		if row.ID == triggerID {
			triggerDelivered = true
		}
	}

	if triggerID > 0 && sender != nil && !triggerDelivered {
		h.sendStatus(sender, liveDeviceStatusQueued, triggerID, clientMsgID, toDeviceID, "")
	}
}

// notifyRowDelivered tells the row's sender (if currently online) that their
// queued message reached the target device.
func (h *liveHub) notifyRowDelivered(username string, row deviceQueuedMsg) {
	if row.FromDeviceID == "" {
		return
	}
	sender := h.lookupDeviceConn(username, row.FromDeviceID)
	if sender == nil {
		return // sender offline: nothing to report to
	}
	h.sendStatus(sender, liveDeviceStatusDelivered, row.ID, row.ClientMsgID, row.ToDeviceID, "")
}

// sendStatus reports a device.msg outcome to the sender's connection.
func (h *liveHub) sendStatus(to *liveClient, status string, queueID int64, clientMsgID, toDeviceID, errMsg string) {
	if to == nil {
		return
	}
	h.deliver(to, liveMessage{
		Type:        liveTypeDeviceStatus,
		Status:      status,
		QueueID:     queueID,
		ClientMsgID: clientMsgID,
		ToDeviceID:  toDeviceID,
		ErrMsg:      errMsg,
	})
}

// deliverQueued hands msg to a queue-drain target with backpressure: it waits
// for send-buffer space instead of overflowing or evicting, and aborts when
// the connection is (being) torn down. Blocking is safe — drains run on
// their own goroutines and hold no hub lock here. A nil stop channel
// (test-constructed clients) degrades to a non-blocking attempt.
func (h *liveHub) deliverQueued(c *liveClient, msg liveMessage) bool {
	h.mu.RLock()
	_, registered := h.clients[c]
	h.mu.RUnlock()
	if !registered {
		return false
	}
	if c.stop == nil {
		select {
		case c.send <- msg:
			return true
		default:
			return false
		}
	}
	select {
	case c.send <- msg:
		return true
	case <-c.stop:
		return false
	}
}

// deliver hands msg to one client's write loop. The registered-check,
// the send, and any eviction share the hub lock with unregister's
// stop-channel close — teardown is signalled through c.stop (the send
// channel itself is never closed, so queue drains may block on it safely),
// and the buffer check guarantees delivery never blocks while holding the
// lock.
func (h *liveHub) deliver(c *liveClient, msg liveMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return // already unregistered
	}
	select {
	case c.send <- msg:
	default:
		// Wedged write loop: evict under the same lock discipline as
		// unregister (device registry included, stop signalled exactly once).
		h.removeLocked(c)
		_ = c.conn.Close()
	}
}

// removeLocked unregisters c: signals its write loop exactly once via c.stop
// and drops its device-registry entry. Caller holds h.mu. A nil stop channel
// (test-constructed clients) is skipped.
func (h *liveHub) removeLocked(c *liveClient) {
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		if c.stop != nil {
			close(c.stop)
		}
	}
	if c.deviceID != "" && h.devices[c.deviceID] == c {
		delete(h.devices, c.deviceID)
	}
}

func (h *liveHub) unregister(client *liveClient) {
	h.mu.Lock()
	deviceRegistered := client.deviceID != "" && h.devices[client.deviceID] == client
	deviceID := client.deviceID
	username := client.username
	h.removeLocked(client)
	h.mu.Unlock()
	if deviceRegistered {
		var queued int64
		if h.queue != nil {
			queued = h.queue.pendingCountFor(username, deviceID)
		}
		h.broadcastToUser(username, nil, liveMessage{
			Type:     liveTypeDeviceOffline,
			DeviceID: deviceID,
			Queued:   queued,
		})
	}
}

func (h *liveHub) broadcast(msg liveMessage) {
	h.mu.RLock()
	clients := make([]*liveClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	for _, client := range clients {
		h.deliver(client, msg)
	}
}

func (h *liveHub) broadcastToUser(username string, except *liveClient, msg liveMessage) {
	h.mu.RLock()
	targets := make([]*liveClient, 0, len(h.clients))
	for cl := range h.clients {
		if cl.username == username && cl != except {
			targets = append(targets, cl)
		}
	}
	h.mu.RUnlock()

	for _, cl := range targets {
		h.deliver(cl, msg)
	}
}

func (c *liveClient) enqueueJSON(msg liveMessage) {
	c.hub.deliver(c, msg)
}

func (c *liveClient) readLoop() {
	defer func() {
		c.hub.unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(wsMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		msgType, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.TextMessage {
			var m struct {
				Type           string   `json:"type"`
				ClientSentAtMS *float64 `json:"client_sent_at_ms"`
				NoteID         int64    `json:"note_id"`
				Editing        *bool    `json:"editing"`
				DeviceID       string   `json:"device_id"`
				Body           string   `json:"body"`
				ToDeviceID     string   `json:"to_device_id"`
				Channel        string   `json:"channel"`
				Data           string   `json:"data"`
				ClientMsgID    string   `json:"client_msg_id"`
				QueueID        int64    `json:"queue_id"`
			}
			if json.Unmarshal(msg, &m) != nil {
				continue
			}
			switch m.Type {
			case "ping":
				receivedAt := time.Now().UTC()
				c.enqueueJSON(liveMessage{
					Type:               "pong",
					ClientSentAtMS:     m.ClientSentAtMS,
					ServerReceivedAtUS: receivedAt.UnixMicro(),
				})
			case liveTypeEditSync:
				if m.Editing == nil || m.NoteID <= 0 {
					continue // malformed: ignore silently
				}
				c.hub.broadcastToUser(c.username, c, liveMessage{
					Type:     liveTypeEditSync,
					NoteID:   m.NoteID,
					Editing:  m.Editing,
					DeviceID: m.DeviceID,
				})
			case liveTypeEditBody:
				if m.NoteID <= 0 {
					continue // malformed: ignore silently
				}
				c.hub.broadcastToUser(c.username, c, liveMessage{
					Type:     liveTypeEditBody,
					NoteID:   m.NoteID,
					Body:     m.Body,
					DeviceID: m.DeviceID,
				})
			case liveTypeDeviceHello:
				if m.DeviceID == "" {
					continue
				}
				c.hub.registerDevice(c, m.DeviceID)
			case liveTypeDeviceMsg:
				c.hub.handleDeviceMsg(c, liveMessage{
					ToDeviceID:  m.ToDeviceID,
					Channel:     m.Channel,
					Data:        m.Data,
					ClientMsgID: m.ClientMsgID,
				})
			case liveTypeDeviceAck:
				// The receiver confirms a delivered queue row; the row is
				// retired (guarded by this connection's device id and user).
				// Acks for unknown rows (duplicates, redeliveries) are fine.
				if m.QueueID > 0 && c.deviceID != "" && c.hub.queue != nil {
					if err := c.hub.queue.ack(c.username, c.deviceID, m.QueueID); err != nil {
						log.Printf("device queue: ack: %v", err)
					}
				}
			}
		}
	}
}

func (c *liveClient) writeLoop() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if msg.Type == "pong" {
				sentAt := time.Now().UTC()
				if msg.ServerSentAtUS == 0 {
					msg.ServerSentAtUS = sentAt.UnixMicro()
				}
				if msg.Timestamp == "" {
					msg.Timestamp = sentAt.Format(time.RFC3339Nano)
				}
			} else if msg.Timestamp == "" {
				msg.Timestamp = liveTimestamp()
			}
			payload, err := json.Marshal(msg)
			if err != nil {
				log.Printf("live: marshal event %s: %v", msg.Type, err)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-c.stop:
			// Unregistered (evicted or socket dead): retire gracefully. The
			// send channel is intentionally never closed, so drain back-
			// pressure (deliverQueued) can block on it safely.
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func liveTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func uniquePositiveNoteIDs(noteIDs []int64) []int64 {
	if len(noteIDs) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(noteIDs))
	out := make([]int64, 0, len(noteIDs))
	for _, noteID := range noteIDs {
		if noteID <= 0 {
			continue
		}
		if _, ok := seen[noteID]; ok {
			continue
		}
		seen[noteID] = struct{}{}
		out = append(out, noteID)
	}
	return out
}
