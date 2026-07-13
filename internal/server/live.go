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
	liveTypeReady       = "live.ready"
	liveTypeNotesChange = "notes.changed"
	liveTypeJobsChange  = "jobs.changed"

	wsWriteWait      = 10 * time.Second
	wsPongWait       = 60 * time.Second
	wsPingPeriod     = (wsPongWait * 9) / 10
	wsMaxMessageSize = 1024
)

type liveMessage struct {
	Type               string         `json:"type"`
	Timestamp          string         `json:"timestamp,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	NoteIDs            []int64        `json:"note_ids,omitempty"`
	Job                *jobs.RunEvent `json:"job,omitempty"`
	ClientSentAtMS     *float64       `json:"client_sent_at_ms,omitempty"`
	ServerReceivedAtUS int64          `json:"server_received_at_us,omitempty"`
	ServerSentAtUS     int64          `json:"server_sent_at_us,omitempty"`
}

type liveHub struct {
	mu       sync.RWMutex
	clients  map[*liveClient]struct{}
	upgrader websocket.Upgrader
}

type liveClient struct {
	hub  *liveHub
	conn *websocket.Conn
	send chan liveMessage
}

func newLiveHub() *liveHub {
	return &liveHub{
		clients: make(map[*liveClient]struct{}),
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
	if _, _, err := s.sessionUsername(r); errors.Is(err, db.ErrNotFound) {
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
		hub:  s.liveHub,
		conn: conn,
		send: make(chan liveMessage, 64),
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

func (h *liveHub) register(client *liveClient) {
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
}

func (h *liveHub) unregister(client *liveClient) {
	h.mu.Lock()
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
	}
	h.mu.Unlock()
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
			}
			if json.Unmarshal(msg, &m) == nil && m.Type == "ping" {
				receivedAt := time.Now().UTC()
				c.enqueueJSON(liveMessage{
					Type:               "pong",
					ClientSentAtMS:     m.ClientSentAtMS,
					ServerReceivedAtUS: receivedAt.UnixMicro(),
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
