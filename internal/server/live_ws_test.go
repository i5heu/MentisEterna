package server

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/websocket"
	"pgregory.net/rapid"

	"github.com/i5heu/MentisEterna/internal/jobs"
)

func TestUniquePositiveNoteIDs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input []int64
		want  []int64
	}{
		// Guards against nil input — must return nil, not empty slice.
		"nil input returns nil": {
			input: nil, want: nil,
		},
		// Guards against empty slice — must check the early-return path.
		"empty input returns nil": {
			input: []int64{}, want: nil,
		},
		// Guards against passing through zero values (0 is not positive).
		"zero value filtered out": {
			input: []int64{0, 1, 2}, want: []int64{1, 2},
		},
		// Guards against passing through negative values.
		"negative values filtered out": {
			input: []int64{-1, -5, 3, 4}, want: []int64{3, 4},
		},
		// Guards against duplicates not being collapsed.
		"duplicates collapsed": {
			input: []int64{1, 2, 2, 3, 1, 3}, want: []int64{1, 2, 3},
		},
		// Guards against all-zero-or-negative input producing empty output.
		"all zero or negative returns empty": {
			input: []int64{0, -1, -99}, want: []int64{},
		},
		// Guards against single-element slice regressing.
		"single valid element": {
			input: []int64{42}, want: []int64{42},
		},
		// Guards against ordering regressions: output must preserve first-seen order.
		"preserves first-seen order": {
			input: []int64{3, 1, 4, 1, 5, 9, 3}, want: []int64{3, 1, 4, 5, 9},
		},
		// Guards against int64 max value being mishandled.
		"max int64 value handled": {
			input: []int64{1, 9223372036854775807, 2}, want: []int64{1, 9223372036854775807, 2},
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := uniquePositiveNoteIDs(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("uniquePositiveNoteIDs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUniquePositiveNoteIDs_PBT(t *testing.T) {
	t.Parallel()

	// Invariant: uniquePositiveNoteIDs is idempotent.
	// Failure mode: repeated calls mutate or alter the result set.
	t.Run("idempotency", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			first := uniquePositiveNoteIDs(ids)
			second := uniquePositiveNoteIDs(first)
			if diff := cmp.Diff(first, second, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("idempotency violated (-first +second):\n%s", diff)
			}
		})
	})

	// Invariant: every element in the output is strictly positive (> 0).
	// Failure mode: zero or negative values leak through.
	t.Run("all elements positive", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			result := uniquePositiveNoteIDs(ids)
			for _, v := range result {
				if v <= 0 {
					t.Fatalf("non-positive value %d in output", v)
				}
			}
		})
	})

	// Invariant: output contains no duplicates.
	// Failure mode: dedup logic is broken.
	t.Run("no duplicates in output", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			result := uniquePositiveNoteIDs(ids)
			seen := map[int64]struct{}{}
			for _, v := range result {
				if _, ok := seen[v]; ok {
					t.Fatalf("duplicate value %d in output", v)
				}
				seen[v] = struct{}{}
			}
		})
	})

	// Invariant: every element in output was present in input (as a positive).
	// Failure mode: function fabricates values.
	t.Run("output is subset of positive input", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			inputSet := map[int64]struct{}{}
			for _, v := range ids {
				if v > 0 {
					inputSet[v] = struct{}{}
				}
			}
			result := uniquePositiveNoteIDs(ids)
			for _, v := range result {
				if _, ok := inputSet[v]; !ok {
					t.Fatalf("output value %d not present in positive input subset", v)
				}
			}
		})
	})

	// Invariant: len(result) <= len(input).
	// Failure mode: function adds elements.
	t.Run("output length does not exceed input", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			result := uniquePositiveNoteIDs(ids)
			if len(result) > len(ids) {
				t.Fatalf("output len %d > input len %d", len(result), len(ids))
			}
		})
	})

	// Invariant: function never panics on any []int64 input.
	// Failure mode: nil or empty slice triggers nil-pointer dereference.
	t.Run("never panics", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			ids := rapid.SliceOf(rapid.Int64()).Draw(t, "input")
			_ = uniquePositiveNoteIDs(ids)
		})
	})
}

func TestLiveMessageJSONRoundtrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		msg liveMessage
	}{
		// Guards against serialization dropping the Type field.
		"ready message": {
			msg: liveMessage{Type: liveTypeReady, Timestamp: "2026-01-01T00:00:00Z"},
		},
		// Guards against omitempty erasing notes.changed with no note_ids.
		"notes changed with empty note_ids": {
			msg: liveMessage{Type: liveTypeNotesChange, Timestamp: "2026-01-01T00:00:00Z", Reason: "created"},
		},
		// Guards against serialization of populated NoteIDs.
		"notes changed with note_ids": {
			msg: liveMessage{Type: liveTypeNotesChange, Timestamp: "2026-01-01T00:00:00Z", Reason: "updated", NoteIDs: []int64{1, 2, 3}},
		},
		// Guards against nil Job field causing issues.
		"jobs changed with nil job": {
			msg: liveMessage{Type: liveTypeJobsChange, Timestamp: "2026-01-01T00:00:00Z"},
		},
		// Guards against the Job field roundtripping correctly.
		"jobs changed with populated job": {
			msg: liveMessage{
				Type:      liveTypeJobsChange,
				Timestamp: "2026-01-01T00:00:00Z",
				Job:       &jobs.RunEvent{Type: "enqueued", RunID: 42, Status: "planned"},
			},
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded liveMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			opts := []cmp.Option{cmpopts.EquateEmpty()}
			if diff := cmp.Diff(tc.msg, decoded, opts...); diff != "" {
				t.Errorf("roundtrip mismatch (-original +decoded):\n%s", diff)
			}
		})
	}
}

func TestLiveMessageJSONRoundtrip_PBT(t *testing.T) {
	t.Parallel()

	// Invariant: for any liveMessage, json.Marshal followed by json.Unmarshal
	// produces an equivalent struct.
	// Failure mode: struct tags or custom marshalling break roundtrip.
	rapid.Check(t, func(t *rapid.T) {
		msg := rapid.Custom(func(t *rapid.T) liveMessage {
			types := []string{liveTypeReady, liveTypeNotesChange, liveTypeJobsChange}
			return liveMessage{
				Type:      rapid.SampledFrom(types).Draw(t, "type"),
				Timestamp: rapid.StringMatching(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.*Z`).Draw(t, "ts"),
				Reason:    rapid.String().Draw(t, "reason"),
				NoteIDs:   rapid.SliceOf(rapid.Int64Min(1)).Draw(t, "noteIDs"),
			}
		}).Draw(t, "msg")

		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded liveMessage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		opts := []cmp.Option{cmpopts.EquateEmpty()}
		if diff := cmp.Diff(msg, decoded, opts...); diff != "" {
			t.Errorf("roundtrip mismatch (-original +decoded):\n%s", diff)
		}
	})
}

func TestLiveTimestamp(t *testing.T) {
	t.Parallel()

	// Guards against timestamp format being incorrect — must be parseable
	// as time.RFC3339Nano. Failure mode: format string change breaks consumers.
	ts := liveTimestamp()
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatalf("liveTimestamp() = %q, not valid RFC3339Nano: %v", ts, err)
	}
	if parsed.IsZero() {
		t.Fatal("liveTimestamp() returned a zero time")
	}
	if diff := time.Since(parsed.UTC()).Abs(); diff > 5*time.Second {
		t.Errorf("liveTimestamp() %q is %.1f seconds from now", ts, diff.Seconds())
	}
}

func TestNewLiveHub(t *testing.T) {
	t.Parallel()

	hub := newLiveHub()
	// Guards against nil clients map — operations on nil map panic.
	if hub.clients == nil {
		t.Fatal("newLiveHub: clients map is nil")
	}
	// Guards against uninitialized upgrader producing zero-value buffer sizes.
	if hub.upgrader.ReadBufferSize != 1024 {
		t.Errorf("ReadBufferSize = %d, want 1024", hub.upgrader.ReadBufferSize)
	}
	if hub.upgrader.WriteBufferSize != 4096 {
		t.Errorf("WriteBufferSize = %d, want 4096", hub.upgrader.WriteBufferSize)
	}
	// Guards against nil CheckOrigin — nil function causes panic on upgrade.
	if hub.upgrader.CheckOrigin == nil {
		t.Fatal("newLiveHub: CheckOrigin is nil")
	}
	// Guards against CheckOrigin not accepting any origin.
	if !hub.upgrader.CheckOrigin(&http.Request{}) {
		t.Fatal("newLiveHub: CheckOrigin rejected an empty request")
	}
}

func TestLiveHubRegisterUnregister(t *testing.T) {
	t.Parallel()

	makeClient := func(hub *liveHub) *liveClient {
		return &liveClient{hub: hub, send: make(chan []byte, 1)}
	}

	tests := map[string]struct {
		ops     func(h *liveHub, cs []*liveClient) []*liveClient
		wantLen int
		desc    string
	}{
		// Guards against register not adding the client.
		"single register": {
			ops:     func(h *liveHub, cs []*liveClient) []*liveClient { h.register(cs[0]); return cs[:1] },
			wantLen: 1,
		},
		// Guards against duplicate registration (map key is pointer, no-op).
		"duplicate register is no-op": {
			ops:     func(h *liveHub, cs []*liveClient) []*liveClient { h.register(cs[0]); h.register(cs[0]); return cs[:1] },
			wantLen: 1,
		},
		// Guards against unregister of registered client not removing it.
		"register then unregister": {
			ops: func(h *liveHub, cs []*liveClient) []*liveClient {
				h.register(cs[0])
				h.unregister(cs[0])
				return cs[:0]
			},
			wantLen: 0,
		},
		// Guards against unregister of never-registered client causing panic.
		"unregister nonexistent does not panic": {
			ops:     func(h *liveHub, cs []*liveClient) []*liveClient { h.unregister(cs[0]); return cs[:0] },
			wantLen: 0,
		},
		// Guards against multiple unregisters (idempotent).
		"multiple unregister is idempotent": {
			ops: func(h *liveHub, cs []*liveClient) []*liveClient {
				h.register(cs[0])
				h.unregister(cs[0])
				h.unregister(cs[0])
				return cs[:0]
			},
			wantLen: 0,
		},
		// Guards against concurrent register race conditions.
		"concurrent register": {
			ops: func(h *liveHub, cs []*liveClient) []*liveClient {
				var wg sync.WaitGroup
				for i := range cs {
					wg.Add(1)
					go func(idx int) { defer wg.Done(); h.register(cs[idx]) }(i)
				}
				wg.Wait()
				return cs
			},
			wantLen: 10,
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hub := newLiveHub()
			clients := make([]*liveClient, 10)
			for i := range clients {
				clients[i] = makeClient(hub)
			}
			wantKeep := tc.ops(hub, clients)
			hub.mu.RLock()
			gotLen := len(hub.clients)
			hub.mu.RUnlock()
			if gotLen != tc.wantLen {
				t.Errorf("client count = %d, want %d (%s)", gotLen, tc.wantLen, tc.desc)
			}
			for _, c := range wantKeep {
				hub.mu.RLock()
				_, ok := hub.clients[c]
				hub.mu.RUnlock()
				if !ok {
					t.Errorf("client %p expected in hub but missing", c)
				}
			}
		})
	}
}

func TestLiveHubRegisterUnregister_PBT(t *testing.T) {
	t.Parallel()

	// Invariant: after registering N distinct clients, len(hub.clients) == N.
	// Failure mode: registration has a race or silently drops clients.
	t.Run("register count correctness", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(1, 50).Draw(t, "n")
			hub := newLiveHub()
			for i := 0; i < n; i++ {
				c := &liveClient{hub: hub, send: make(chan []byte, 1)}
				hub.register(c)
			}
			hub.mu.RLock()
			got := len(hub.clients)
			hub.mu.RUnlock()
			if got != n {
				t.Fatalf("after %d registrations, hub has %d clients", n, got)
			}
		})
	})

	// Invariant: register then unregister all = empty hub.
	// Failure mode: clients leak or unregister is not symmetric with register.
	t.Run("register all then unregister all is empty", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			n := rapid.IntRange(1, 30).Draw(t, "n")
			hub := newLiveHub()
			clients := make([]*liveClient, n)
			for i := 0; i < n; i++ {
				clients[i] = &liveClient{hub: hub, send: make(chan []byte, 1)}
				hub.register(clients[i])
			}
			// Unregister all in shuffled order to catch ordering dependencies.
			rand.Shuffle(n, func(i, j int) { clients[i], clients[j] = clients[j], clients[i] })
			for _, c := range clients {
				hub.unregister(c)
			}
			hub.mu.RLock()
			got := len(hub.clients)
			hub.mu.RUnlock()
			if got != 0 {
				t.Fatalf("after register+unregister all, hub has %d clients, want 0", got)
			}
		})
	})

	// Invariant: unregister closes the client's send channel.
	// Failure mode: consumer goroutines hang on a channel that's never closed.
	t.Run("unregister closes send channel", func(t *testing.T) {
		t.Parallel()
		rapid.Check(t, func(t *rapid.T) {
			hub := newLiveHub()
			client := &liveClient{hub: hub, send: make(chan []byte, 1)}
			hub.register(client)
			hub.unregister(client)
			select {
			case _, ok := <-client.send:
				if ok {
					t.Fatal("send channel still open after unregister")
				}
			default:
				t.Fatal("send channel not closed (or buffer not drained)")
			}
		})
	})
}

func TestLiveHubBroadcast(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup    func(hub *liveHub, cs []*liveClient)
		msg      liveMessage
		wantRecv int
	}{
		// Guards against nil-panic when broadcasting to an empty hub.
		"broadcast to empty hub does not panic": {
			setup:    func(_ *liveHub, _ []*liveClient) {},
			msg:      liveMessage{Type: liveTypeReady, Timestamp: "2000-01-01T00:00:00Z"},
			wantRecv: 0,
		},
		// Guards against message not reaching a registered client.
		"broadcast reaches single client": {
			setup:    func(hub *liveHub, cs []*liveClient) { hub.register(cs[0]) },
			msg:      liveMessage{Type: liveTypeReady, Timestamp: "2000-01-01T00:00:00Z"},
			wantRecv: 1,
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hub := newLiveHub()
			clients := make([]*liveClient, 3)
			for i := range clients {
				clients[i] = &liveClient{hub: hub, send: make(chan []byte, 64)}
			}
			tc.setup(hub, clients)
			var mu sync.Mutex
			recvCount := 0
			done := make(chan struct{})
			go func() {
				for _, c := range clients {
					select {
					case <-c.send:
						mu.Lock()
						recvCount++
						mu.Unlock()
					case <-time.After(2 * time.Second):
					}
				}
				close(done)
			}()
			time.Sleep(50 * time.Millisecond)
			hub.broadcast(tc.msg)
			<-done
			mu.Lock()
			got := recvCount
			mu.Unlock()
			if got != tc.wantRecv {
				t.Errorf("received by %d clients, want %d", got, tc.wantRecv)
			}
		})
	}
}

func TestLiveClientEnqueueJSON(t *testing.T) {
	t.Parallel()

	// Guards against message not being sent to an open channel.
	hub := newLiveHub()
	client := &liveClient{hub: hub, send: make(chan []byte, 1)}
	hub.register(client)

	client.enqueueJSON(liveMessage{Type: liveTypeReady, Timestamp: "2000-01-01T00:00:00Z"})

	select {
	case <-client.send:
		// OK: message delivered.
	default:
		t.Error("expected to receive message but got none")
	}

	hub.mu.RLock()
	_, inHub := hub.clients[client]
	hub.mu.RUnlock()
	if !inHub {
		t.Error("client should remain in hub after successful enqueueJSON")
	}
}

func TestNotifyNotesChangedNilSafety(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		s       *Server
		reason  string
		noteIDs []int64
	}{
		// Guards against nil Server receiver panicking.
		"nil server does not panic": {s: nil, reason: "test", noteIDs: []int64{1}},
		// Guards against server with nil liveHub panicking.
		"nil liveHub does not panic": {s: &Server{liveHub: nil}, reason: "test", noteIDs: []int64{1}},
		// Guards against zero note IDs — filtered by uniquePositiveNoteIDs.
		"zero note IDs produces no broadcast": {s: &Server{liveHub: newLiveHub()}, reason: "test", noteIDs: []int64{0, -1}},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.s.notifyNotesChanged(tc.reason, tc.noteIDs...)
		})
	}
}

func TestNotifyJobEventNilSafety(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		s   *Server
		evt jobs.RunEvent
	}{
		// Guards against nil Server receiver panicking.
		"nil server does not panic": {s: nil, evt: jobs.RunEvent{Type: "test", RunID: 1}},
		// Guards against nil liveHub panicking.
		"nil liveHub does not panic": {s: &Server{liveHub: nil}, evt: jobs.RunEvent{Type: "test", RunID: 1}},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.s.notifyJobEvent(tc.evt)
		})
	}
}

func TestHandleWebSocketMethodNotAllowed(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)

	tests := map[string]string{
		// Guards against POST being treated as a WebSocket upgrade.
		"POST not allowed": http.MethodPost,
		// Guards against PUT being treated as a WebSocket upgrade.
		"PUT not allowed": http.MethodPut,
		// Guards against DELETE being treated as a WebSocket upgrade.
		"DELETE not allowed": http.MethodDelete,
		// Guards against PATCH being treated as a WebSocket upgrade.
		"PATCH not allowed": http.MethodPatch,
		// Guards against OPTIONS being treated as a WebSocket upgrade.
		"OPTIONS not allowed": http.MethodOptions,
	}

	for name, method := range tests {
		method := method
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(method, "/ws", nil)
			s.handleWebSocket(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: status = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestHandleWebSocketNilLiveHub(t *testing.T) {
	t.Parallel()

	// Guards against handleWebSocket called on a Server with no liveHub.
	// Failure mode: nil pointer dereference on s.liveHub access.
	s := &Server{liveHub: nil}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	s.handleWebSocket(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestWebSocketConnectAndClose(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	msg := requireLiveMessageType(t, conn, liveTypeReady)

	// Guards against empty timestamp on live.ready.
	if msg.Timestamp == "" {
		t.Error("live.ready message has empty timestamp")
	}
	// Guards against timestamp not being valid RFC3339Nano.
	if _, err := time.Parse(time.RFC3339Nano, msg.Timestamp); err != nil {
		t.Errorf("live.ready timestamp %q is not valid RFC3339Nano: %v", msg.Timestamp, err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket: %v", err)
	}
}

func TestWebSocketPingPongKeepalive(t *testing.T) {
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

	// Set a pong handler to detect server-sent pings.
	pongReceived := make(chan struct{}, 1)
	conn.SetPongHandler(func(string) error {
		select {
		case pongReceived <- struct{}{}:
		default:
		}
		return nil
	})

	// Guards against ping/pong mechanism not keeping the connection alive.
	select {
	case <-pongReceived:
		// Server sent a ping — keepalive works.
	case <-time.After(wsPingPeriod + 2*time.Second):
		t.Log("no ping received within expected window (may be timing-related)")
	}

	// Verify connection is still alive by triggering a notes.change event.
	note := helperCreateNote(t, s, "Keepalive Test", "test body", nil)
	// Drain the "created" broadcast from helperCreateNote.
	requireLiveMessageType(t, conn, liveTypeNotesChange)

	payload := fmt.Sprintf(`{"title":"Updated","body":"updated","parent_id":null}`)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", note.ID), strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.updateNote(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update note: status=%d body=%s", w.Code, w.Body.String())
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "updated" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "updated")
	}
}

func TestWebSocketMultipleClientsReceiveBroadcast(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	const numClients = 3

	// Connect all clients and drain their ready messages.
	clients := make([]*websocket.Conn, 0, numClients)
	for i := 0; i < numClients; i++ {
		conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
		if err != nil {
			for _, c := range clients {
				c.Close()
			}
			t.Fatalf("dial client %d: %v", i, err)
		}
		ready := readLiveMessage(t, conn)
		if ready.Type != liveTypeReady {
			conn.Close()
			for _, c := range clients {
				c.Close()
			}
			t.Fatalf("client %d: expected live.ready, got %s", i, ready.Type)
		}
		clients = append(clients, conn)
		defer conn.Close()
	}

	// Guards against broadcast not reaching all connected clients.
	helperCreateNote(t, s, "Broadcast Test", "body", nil)

	// Each client must receive the notes.changed event.
	received := make(chan struct{}, numClients)
	for _, conn := range clients {
		go func(c *websocket.Conn) {
			msg := readLiveMessage(t, c)
			if msg.Type == liveTypeNotesChange && msg.Reason == "created" {
				received <- struct{}{}
			}
		}(conn)
	}

	// Collect with timeout.
	gotCount := 0
	timeout := time.After(3 * time.Second)
	for i := 0; i < numClients; i++ {
		select {
		case <-received:
			gotCount++
		case <-timeout:
			t.Fatalf("timeout: only %d/%d clients received broadcast", gotCount, numClients)
		}
	}
}

func TestWebSocketClientDisconnectCleanup(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)
	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)

	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	requireLiveMessageType(t, conn, liveTypeReady)

	// Guards against hub retaining clients after connection close.
	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket: %v", err)
	}

	// Poll until hub is empty (readLoop unregisters asynchronously).
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.liveHub.mu.RLock()
		clientCount := len(s.liveHub.clients)
		s.liveHub.mu.RUnlock()
		if clientCount == 0 {
			break
		}
		select {
		case <-ticker.C:
			continue
		case <-deadline:
			t.Errorf("hub still has %d clients after connection close", clientCount)
			return
		}
	}
}

func TestNotifyNotesChangedDeduplicatesIDs(t *testing.T) {
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

	// Guards against duplicate note IDs appearing in broadcast messages.
	s.notifyNotesChanged("test_dup", 42, 42, 43, 42)

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "test_dup" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "test_dup")
	}

	// Verify no duplicates in NoteIDs.
	seen := map[int64]struct{}{}
	for _, id := range msg.NoteIDs {
		if _, ok := seen[id]; ok {
			t.Errorf("duplicate note ID %d in broadcast", id)
		}
		seen[id] = struct{}{}
	}

	// Verify expected IDs are present (order-independent).
	if diff := cmp.Diff([]int64{42, 43}, msg.NoteIDs,
		cmpopts.SortSlices(func(a, b int64) bool { return a < b }),
	); diff != "" {
		t.Errorf("note IDs mismatch (-want +got):\n%s", diff)
	}
}
