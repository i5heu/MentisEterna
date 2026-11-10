package mcptunnel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockProxy is a minimal test double of the remote relay proxy as specified
// in docs/MCPProxy.md: it authenticates the tunnel, echoes the subprotocol,
// counts connections, and hands each tunnel to a per-test callback.
type mockProxy struct {
	URL      string // ws://127.0.0.1:port/tunnel
	token    string
	connects atomic.Int64
	onConns  atomic.Value // func(*websocket.Conn)
}

func startMockProxy(t *testing.T, token string) *mockProxy {
	t.Helper()
	p := &mockProxy{token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+p.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{ProtocolName}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		p.connects.Add(1)
		if fn, ok := p.onConns.Load().(func(*websocket.Conn)); ok && fn != nil {
			fn(conn)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.URL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/tunnel"
	return p
}

func (p *mockProxy) onConnect(fn func(*websocket.Conn)) { p.onConns.Store(fn) }

// drain keeps the mock tunnel readable until the client drops it.
func drain(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// runClient starts the tunnel client in the background, cleaned up by the test.
func runClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	if cfg.PingInterval == 0 {
		cfg.PingInterval = time.Hour // keep pings out of the way of most tests
	}
	c := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

func TestRelaysRequestThroughHandler(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")

	type seenReq struct {
		method, path, auth, body string
	}
	seen := make(chan seenReq, 1)
	resCh := make(chan resFrame, 1)
	p.onConnect(func(conn *websocket.Conn) {
		err := conn.WriteJSON(reqFrame{
			Type:    "req",
			ID:      "r-1",
			Method:  http.MethodPost,
			Path:    "/mcp",
			Headers: map[string][]string{"Authorization": {"Bearer agent-token"}},
			Body:    `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		})
		if err != nil {
			t.Errorf("proxy write req: %v", err)
			return
		}
		var res resFrame
		if err := conn.ReadJSON(&res); err != nil {
			t.Errorf("proxy read res: %v", err)
			return
		}
		resCh <- res
		drain(conn)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- seenReq{r.Method, r.URL.Path, r.Header.Get("Authorization"), string(b)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})

	c := runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: handler})

	var res resFrame
	select {
	case res = <-resCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for res frame")
	}
	if res.Type != "res" || res.ID != "r-1" {
		t.Fatalf("res frame = %+v, want type=res id=r-1", res)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("res.Status = %d, want 200", res.Status)
	}
	if res.Body != `{"ok":true}` {
		t.Fatalf("res.Body = %q", res.Body)
	}
	if res.ContentType != "application/json" {
		t.Fatalf("res.ContentType = %q, want application/json", res.ContentType)
	}

	select {
	case got := <-seen:
		if got.method != http.MethodPost || got.path != "/mcp" {
			t.Fatalf("handler saw %s %s", got.method, got.path)
		}
		if got.auth != "Bearer agent-token" {
			t.Fatalf("Authorization = %q, want relayed bearer token", got.auth)
		}
		if got.body != `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` {
			t.Fatalf("body = %q, want relayed JSON-RPC body", got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler was never called")
	}

	st := c.Status()
	if !st.Connected || st.URL != p.URL || st.ConnectCount != 1 || st.Enabled != true {
		t.Fatalf("status = %+v, want connected client for %s", st, p.URL)
	}
}

func TestRejectsNonMCPRequests(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	handlerHits := make(chan struct{}, 8)
	resCh := make(chan resFrame, 4)
	p.onConnect(func(conn *websocket.Conn) {
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "a", Method: http.MethodGet, Path: "/mcp"})
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "b", Method: http.MethodPost, Path: "/notes"})
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "c", Method: http.MethodPost, Path: "/mcp",
			Headers: map[string][]string{"Authorization": {"Bearer agent-token"}}, Body: "{}"})
		for i := 0; i < 3; i++ {
			var res resFrame
			if err := conn.ReadJSON(&res); err != nil {
				t.Errorf("proxy read res: %v", err)
				return
			}
			resCh <- res
		}
		drain(conn)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerHits <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: handler})

	byID := map[string]resFrame{}
	for i := 0; i < 3; i++ {
		select {
		case res := <-resCh:
			byID[res.ID] = res
		case <-time.After(10 * time.Second):
			t.Fatal("timed out collecting res frames")
		}
	}
	if res := byID["a"]; res.Status != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp → %d, want 405", res.Status)
	}
	if res := byID["b"]; res.Status != http.StatusNotFound {
		t.Fatalf("POST /notes → %d, want 404", res.Status)
	}
	if res := byID["c"]; res.Status != http.StatusOK {
		t.Fatalf("POST /mcp → %d, want 200", res.Status)
	}
	if len(handlerHits) != 1 {
		t.Fatalf("handler called %d times, want exactly 1 (only the POST /mcp frame)", len(handlerHits))
	}
}

func TestReconnectsAfterDrop(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	resCh := make(chan resFrame, 1)
	p.onConnect(func(conn *websocket.Conn) {
		if p.connects.Load() == 1 {
			// Kill the very first tunnel as soon as it is up.
			_ = conn.Close()
			return
		}
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "probe", Method: http.MethodPost, Path: "/mcp"})
		var res resFrame
		if err := conn.ReadJSON(&res); err != nil {
			t.Errorf("proxy read res: %v", err)
			return
		}
		resCh <- res
		drain(conn)
	})

	c := runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: okHandler()})

	waitFor(t, 15*time.Second, "second tunnel connection", func() bool {
		return p.connects.Load() >= 2
	})
	select {
	case res := <-resCh:
		if res.ID != "probe" || res.Status != http.StatusOK {
			t.Fatalf("res = %+v, want probe/200", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no res on the reconnected tunnel")
	}
	waitFor(t, 5*time.Second, "connect count in status", func() bool {
		return c.Status().ConnectCount >= 2
	})
}

func TestStatusRecordsDialFailure(t *testing.T) {
	// Proxy expects a different token: the client's dials are rejected 401.
	p := startMockProxy(t, "expected-token")
	c := runClient(t, Config{URL: p.URL, Token: "wrong-token", Handler: okHandler()})

	waitFor(t, 10*time.Second, "dial failure in status", func() bool {
		st := c.Status()
		return !st.Connected && strings.Contains(st.LastError, "HTTP 401")
	})
	if p.connects.Load() != 0 {
		t.Fatalf("proxy counted %d tunnels, want 0", p.connects.Load())
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	p.onConnect(func(conn *websocket.Conn) { drain(conn) })

	c := New(Config{URL: p.URL, Token: "tunnel-secret", Handler: okHandler(), PingInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	waitFor(t, 10*time.Second, "tunnel to connect", func() bool { return c.Status().Connected })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestSendsKeepalivePings(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	pings := make(chan struct{}, 16)
	p.onConnect(func(conn *websocket.Conn) {
		conn.SetPingHandler(func(string) error {
			pings <- struct{}{}
			return nil
		})
		drain(conn)
	})

	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: okHandler(),
		PingInterval: 100 * time.Millisecond})

	deadline := time.Now().Add(5 * time.Second)
	got := 0
	for got < 2 {
		select {
		case <-pings:
			got++
		case <-time.After(time.Until(deadline)):
			t.Fatalf("received %d pings, want at least 2", got)
		}
	}
}

func TestConcurrencyCapped(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")

	var inflight, peak atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inflight.Add(1)
		for {
			pk := peak.Load()
			if cur <= pk || peak.CompareAndSwap(pk, cur) {
				break
			}
		}
		time.Sleep(120 * time.Millisecond)
		inflight.Add(-1)
		w.WriteHeader(http.StatusOK)
	})

	const total = 4
	resCh := make(chan resFrame, total)
	p.onConnect(func(conn *websocket.Conn) {
		for i := 0; i < total; i++ {
			_ = conn.WriteJSON(reqFrame{Type: "req", ID: fmt.Sprint(i), Method: http.MethodPost, Path: "/mcp"})
		}
		for i := 0; i < total; i++ {
			var res resFrame
			if err := conn.ReadJSON(&res); err != nil {
				t.Errorf("proxy read res: %v", err)
				return
			}
			resCh <- res
		}
		drain(conn)
	})

	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: handler, MaxConcurrency: 2})

	ids := map[string]bool{}
	for i := 0; i < total; i++ {
		select {
		case res := <-resCh:
			if res.Status != http.StatusOK {
				t.Fatalf("res %s → %d, want 200", res.ID, res.Status)
			}
			ids[res.ID] = true
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out after %d res frames", i)
		}
	}
	if len(ids) != total {
		t.Fatalf("got %d distinct ids, want %d", len(ids), total)
	}
	if pk := peak.Load(); pk > 2 {
		t.Fatalf("peak concurrent handler calls = %d, want ≤ 2", pk)
	}
}

func TestRecoversFromMalformedFrame(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	resCh := make(chan resFrame, 1)
	p.onConnect(func(conn *websocket.Conn) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte("this is not json")); err != nil {
			t.Errorf("proxy write garbage: %v", err)
			return
		}
		var ef errorFrame
		if err := conn.ReadJSON(&ef); err != nil {
			t.Errorf("proxy read error frame: %v", err)
			return
		}
		if ef.Type != "error" || ef.Code != codeBadFrame {
			t.Errorf("error frame = %+v, want type=error code=%s", ef, codeBadFrame)
		}
		// The tunnel must stay usable after the bad frame.
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "after", Method: http.MethodPost, Path: "/mcp"})
		var res resFrame
		if err := conn.ReadJSON(&res); err != nil {
			t.Errorf("proxy read res: %v", err)
			return
		}
		resCh <- res
		drain(conn)
	})

	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: okHandler()})

	select {
	case res := <-resCh:
		if res.ID != "after" || res.Status != http.StatusOK {
			t.Fatalf("res = %+v, want after/200", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel did not recover after a malformed frame")
	}
}

func TestPanicInHandlerYields500(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	resCh := make(chan resFrame, 1)
	p.onConnect(func(conn *websocket.Conn) {
		_ = conn.WriteJSON(reqFrame{Type: "req", ID: "p1", Method: http.MethodPost, Path: "/mcp"})
		var res resFrame
		if err := conn.ReadJSON(&res); err != nil {
			t.Errorf("proxy read res: %v", err)
			return
		}
		resCh <- res
		drain(conn)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: handler})

	select {
	case res := <-resCh:
		if res.Status != http.StatusInternalServerError {
			t.Fatalf("res.Status = %d, want 500", res.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no res frame after handler panic")
	}
}

func TestBinaryFrameDropsConnection(t *testing.T) {
	p := startMockProxy(t, "tunnel-secret")
	p.onConnect(func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3})
		drain(conn)
	})

	_ = runClient(t, Config{URL: p.URL, Token: "tunnel-secret", Handler: okHandler()})

	// The client must drop the tunnel on the protocol violation and reconnect.
	waitFor(t, 15*time.Second, "reconnect after binary frame", func() bool {
		return p.connects.Load() >= 2
	})
}
