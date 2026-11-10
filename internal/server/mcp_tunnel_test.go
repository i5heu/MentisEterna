package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
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
		{"generated tunnel token when only MCP_TOKEN is set", false, "ws://example.invalid/tunnel", "", "agent-token", false},
		{"generated tunnel token when nothing is set", false, "ws://example.invalid/tunnel", "", "", false},
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
			if tc.wantNil {
				return
			}
			if tc.tunnelTok != "" {
				return // explicit env token used as-is
			}
			// Without a tunnel env token, a distinct token must have been
			// generated and persisted in the secrets table — never a reuse
			// of MCP_TOKEN.
			stored, err := s.db.GetSecret("mcp_tunnel_token")
			if err != nil {
				t.Fatalf("read stored tunnel token: %v", err)
			}
			if len(stored) != 64 {
				t.Fatalf("stored tunnel token has %d chars, want 64 (32 bytes hex)", len(stored))
			}
			if _, err := hex.DecodeString(stored); err != nil {
				t.Fatalf("stored tunnel token is not hex: %v", err)
			}
			if tc.mcpTok != "" && stored == tc.mcpTok {
				t.Fatal("tunnel token must not reuse MCP_TOKEN")
			}
		})
	}
}

// TestMCPTunnelTokenPersistence pins the token lifecycle: auto-generated on
// first use, persisted in the DB, reused on the next start on the same DB,
// and overridable via MCP_TUNNEL_TOKEN (which leaves the stored value alone).
func TestMCPTunnelTokenPersistence(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	t.Setenv("MCP_TOKEN", "agent-token") // must never be picked for the tunnel
	config.Get().MCP.ProxyURL = "ws://example.invalid/tunnel"

	dbPath := filepath.Join(t.TempDir(), "server.db")

	// First server on this DB: token generated, printed once, stored.
	d1, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d1.Close() })
	s1 := New(d1, ":0", nil, nil, nil, nil, nil, nil)
	if s1.mcpTunnel == nil {
		t.Fatal("mcpTunnel is nil — tunnel did not configure itself")
	}
	stored1, err := d1.GetSecret("mcp_tunnel_token")
	if err != nil || stored1 == "" {
		t.Fatalf("stored tunnel token = %q, %v; want a generated value", stored1, err)
	}
	if stored1 == "agent-token" {
		t.Fatal("tunnel token must be distinct from MCP_TOKEN")
	}

	// Second server on the same DB: same token, reported as stored.
	d2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { d2.Close() })
	s2 := New(d2, ":0", nil, nil, nil, nil, nil, nil)
	token2, source2, err := s2.resolveMCPTunnelToken()
	if err != nil {
		t.Fatalf("resolveMCPTunnelToken: %v", err)
	}
	if token2 != stored1 {
		t.Fatalf("token changed across restarts: %q vs %q", token2, stored1)
	}
	if !strings.Contains(source2, "stored secret") {
		t.Fatalf("source = %q, want stored secret", source2)
	}

	// Explicit env token wins and leaves the stored value untouched.
	t.Setenv("MCP_TUNNEL_TOKEN", "manual-tunnel-token")
	token3, source3, err := s2.resolveMCPTunnelToken()
	if err != nil {
		t.Fatalf("resolveMCPTunnelToken with env: %v", err)
	}
	if token3 != "manual-tunnel-token" || source3 != "MCP_TUNNEL_TOKEN" {
		t.Fatalf("env override: token=%q source=%q", token3, source3)
	}
	storedAfter, err := d2.GetSecret("mcp_tunnel_token")
	if err != nil || storedAfter != stored1 {
		t.Fatalf("env override must not rewrite the stored token: %q, %v", storedAfter, err)
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
