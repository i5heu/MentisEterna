package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestMCPTunnelWiringCases(t *testing.T) {
	cases := []struct {
		name      string
		disabled  bool
		proxyURL  string
		tunnelTok string
		mcpTok    string
		wantNil   bool
	}{
		{"off by default", false, "", "", "", true},
		{"disabled mcp wins", true, "ws://example.invalid/tunnel", "t", "a", true},
		{"enabled with tunnel token", false, "ws://example.invalid/tunnel", "t", "", false},
		{"enabled via MCP_TOKEN fallback", false, "ws://example.invalid/tunnel", "", "a", false},
		{"no token configured", false, "ws://example.invalid/tunnel", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.Reset()
			t.Cleanup(config.Reset)
			t.Setenv("MCP_TOKEN", tc.mcpTok)
			t.Setenv("MCP_TUNNEL_TOKEN", tc.tunnelTok)
			config.Get().MCP.Disabled = tc.disabled
			config.Get().MCP.ProxyURL = tc.proxyURL

			s := newTestServer(t)
			if (s.mcpTunnel == nil) != tc.wantNil {
				t.Fatalf("mcpTunnel == nil = %v, want %v", s.mcpTunnel == nil, tc.wantNil)
			}
		})
	}
}

// TestMCPTunnelRelayThroughRealHandler drives a mock proxy that speaks the
// documented tunnel protocol and relays real MCP JSON-RPC through the
// production handleMCP handler — including its auth, which must stay
// authoritative behind the proxy (tunnel token ≠ agent token).
func TestMCPTunnelRelayThroughRealHandler(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	t.Setenv("MCP_TOKEN", "agent-token")
	t.Setenv("MCP_TUNNEL_TOKEN", "tunnel-secret")

	type tunnelRes struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		Status      int    `json:"status"`
		Body        string `json:"body"`
		ContentType string `json:"content_type"`
	}
	resCh := make(chan tunnelRes, 2)

	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tunnel-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{"mentis-tunnel-v1"}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Two relayed agent requests: one authenticated, one with a wrong
		// agent token (the tunnel token stays valid — auth is per request).
		requests := []map[string]any{
			{
				"type": "req", "id": "req-auth", "method": "POST", "path": "/mcp",
				"headers": map[string]any{"Authorization": []string{"Bearer agent-token"}},
				"body":    `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			},
			{
				"type": "req", "id": "req-bad", "method": "POST", "path": "/mcp",
				"headers": map[string]any{"Authorization": []string{"Bearer wrong-token"}},
				"body":    `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
			},
		}
		for _, req := range requests {
			if err := conn.WriteJSON(req); err != nil {
				t.Errorf("proxy write req: %v", err)
				return
			}
			var res tunnelRes
			if err := conn.ReadJSON(&res); err != nil {
				t.Errorf("proxy read res: %v", err)
				return
			}
			resCh <- res
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	proxy := httptest.NewServer(mux)
	t.Cleanup(proxy.Close)
	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/tunnel"

	config.Get().MCP.ProxyURL = wsURL
	s := newTestServer(t)
	if s.mcpTunnel == nil {
		t.Fatal("mcpTunnel is nil — tunnel did not configure itself")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.mcpTunnel.Run(ctx)

	byID := map[string]tunnelRes{}
	for i := 0; i < 2; i++ {
		select {
		case res := <-resCh:
			byID[res.ID] = res
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out after %d relayed responses", i)
		}
	}

	res := byID["req-auth"]
	if res.Type != "res" || res.Status != http.StatusOK {
		t.Fatalf("req-auth res = %+v, want type=res status=200", res)
	}
	if !strings.Contains(res.Body, "list_todos") {
		t.Fatalf("relay tools/list body missing list_todos: %.200s", res.Body)
	}
	var probe struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res.Body), &probe); err != nil {
		t.Fatalf("relay body is not JSON-RPC result: %v", err)
	}
	if len(probe.Result.Tools) == 0 {
		t.Fatal("tools list came back empty")
	}

	res = byID["req-bad"]
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("req-bad res.Status = %d, want 401 (MentisEterna auth, not proxy auth)", res.Status)
	}
	if !strings.Contains(res.Body, "unauthorized") {
		t.Fatalf("req-bad body = %q, want unauthorized", res.Body)
	}

	st := s.mcpTunnel.Status()
	if !st.Connected || st.ConnectCount < 1 {
		t.Fatalf("tunnel status = %+v, want connected", st)
	}
}
