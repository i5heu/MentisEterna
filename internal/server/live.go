package server

import (
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
	liveReasonInlineUploadResolved = "inline_upload_resolved"
	liveTypeEditSync               = "edit.sync"
	liveTypeEditBody               = "edit.body"
	liveTypeDeviceHello            = "device.hello"
	liveTypeDeviceMsg              = "device.msg"
	liveTypeDeviceOnline           = "device.online"
	liveTypeDeviceOffline          = "device.offline"

	wsWriteWait      = 10 * time.Second
	wsPongWait       = 60 * time.Second
	wsPingPeriod     = (wsPongWait * 9) / 10
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

type liveMessage struct {
	Type               string                `json:"type"`
	Timestamp          string                `json:"timestamp,omitempty"`
	Reason             string                `json:"reason,omitempty"`
	NoteIDs            []int64               `json:"note_ids,omitempty"`
	UploadResolution   *liveUploadResolution `json:"upload_resolution,omitempty"`
	Job                *jobs.RunEvent        `json:"job,omitempty"`
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
}

type liveHub struct {
	mu       sync.RWMutex
	clients  map[*liveClient]struct{}
	devices  map[string]*liveClient
	upgrader websocket.Upgrader
}

type liveClient struct {
	hub      *liveHub
	conn     *websocket.Conn
	send     chan liveMessage
	username string
	deviceID string
}

func newLiveHub() *liveHub {
	return &liveHub{
		clients: make(map[*liveClient]struct{}),
		devices: make(map[string]*liveClient),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
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
	copied := evt
	s.liveHub.broadcast(liveMessage{
		Type:      liveTypeJobsChange,
		Timestamp: liveTimestamp(),
		Job:       &copied,
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

	h.broadcastToUser(c.username, c, liveMessage{Type: liveTypeDeviceOnline, DeviceID: deviceID})

	// Deliver the snapshot to this connection only (its own writeLoop drains
	// the send channel; do not broadcast — the peers already learned about
	// this device via the broadcast above).
	for _, id := range online {
		c.enqueueJSON(liveMessage{Type: liveTypeDeviceOnline, DeviceID: id})
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

func (h *liveHub) unregister(client *liveClient) {
	h.mu.Lock()
	var gone *liveClient
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
	}
	if client.deviceID != "" && h.devices[client.deviceID] == client {
		delete(h.devices, client.deviceID)
		gone = client
	}
	h.mu.Unlock()
	if gone != nil {
		h.broadcastToUser(gone.username, nil, liveMessage{Type: liveTypeDeviceOffline, DeviceID: gone.deviceID})
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
		select {
		case client.send <- msg:
		default:
			h.unregister(client)
			_ = client.conn.Close()
		}
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
		cl.enqueueJSON(msg)
	}
}

func (c *liveClient) enqueueJSON(msg liveMessage) {
	select {
	case c.send <- msg:
	default:
		c.hub.unregister(c)
		_ = c.conn.Close()
	}
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
					Type:   liveTypeEditBody,
					NoteID: m.NoteID,
					Body:   m.Body,
					DeviceID: m.DeviceID,
				})
			case liveTypeDeviceHello:
				if m.DeviceID == "" {
					continue
				}
				c.hub.registerDevice(c, m.DeviceID)
			case liveTypeDeviceMsg:
				if m.ToDeviceID == "" {
					continue
				}
				c.hub.routeToDevice(m.ToDeviceID, c, liveMessage{
					Type:         liveTypeDeviceMsg,
					ToDeviceID:   m.ToDeviceID,
					FromDeviceID: c.deviceID,
					Channel:      m.Channel,
					Data:         m.Data,
				})
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
