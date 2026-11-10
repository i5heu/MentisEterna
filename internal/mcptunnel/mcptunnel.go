// Package mcptunnel implements the client side of the MCP remote proxy
// tunnel (protocol: docs/MCPProxy.md). MentisEterna sits on a LAN behind a
// firewall and cannot accept inbound connections, so this client dials the
// public proxy once and keeps that connection alive permanently. The proxy
// sends relayed HTTP requests ("req" frames) over the tunnel; the client
// rebuilds them, runs them through the configured http.Handler (the normal
// /mcp handler, auth included), and sends the captured response back as a
// "res" frame.
package mcptunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ProtocolName is the WebSocket subprotocol identifying this frame protocol.
const ProtocolName = "mentis-tunnel-v1"

// DefaultMaxConcurrency is the default cap on concurrently relayed requests.
const DefaultMaxConcurrency = 8

// Wire constants — mirrored by docs/MCPProxy.md and the proxy requirements.
const (
	// maxFrameBytes is the largest accepted tunnel frame; larger frames are
	// treated as a protocol violation and drop the connection.
	maxFrameBytes = 4 << 20
	// maxFrameIDLen is the longest request id the client accepts.
	maxFrameIDLen = 128
	// maxWritesBuffer bounds the outbound frame buffer.
	maxWritesBuffer = 64

	defaultPingInterval = 30 * time.Second
	writeWait           = 10 * time.Second
	readIdleLimit       = 90 * time.Second
	handshakeTimeout    = 15 * time.Second
	backoffInitial      = 1 * time.Second
	backoffCap          = 60 * time.Second

	// defaultRequestTimeout bounds one relayed request. It stays below the
	// proxy's documented 120 s wait (docs/MCPProxy.md P5) so agents normally
	// get a definitive 504 answer instead of a proxy-side timeout.
	defaultRequestTimeout = 115 * time.Second
	// queuePerWorker is the burst headroom per worker in the pending queue;
	// beyond it requests are answered with 503 immediately.
	queuePerWorker = 2
	// minRecommendedTokenLen is only a warning threshold for entropy.
	minRecommendedTokenLen = 32
	// log truncation bounds for proxy-controlled strings.
	maxLogCodeLen    = 64
	maxLogMessageLen = 256
)

// Error frame codes (see docs/MCPProxy.md).
const (
	codeBadFrame   = "bad_frame"
	codeBadRequest = "bad_request"
)

// Config configures the tunnel client.
type Config struct {
	// URL is the ws:// or wss:// tunnel endpoint of the remote proxy.
	URL string
	// Token authenticates the tunnel as "Authorization: Bearer <token>".
	Token string
	// Handler receives the reconstructed HTTP requests. Only frames matching
	// Method/Path reach it; everything else is answered inline (404/405).
	Handler http.Handler
	// Method is the only relayed request method. Default: POST.
	Method string
	// Path is the only relayed request path. Default: "/mcp".
	Path string
	// MaxConcurrency is the number of worker goroutines and the cap on
	// concurrent handler calls. <=0: DefaultMaxConcurrency.
	MaxConcurrency int
	// PingInterval is the WS PING cadence. <=0: defaultPingInterval (30s).
	PingInterval time.Duration
	// RequestTimeout bounds one relayed request before the worker answers
	// 504. <=0: defaultRequestTimeout (115s, below the proxy's 120s wait).
	RequestTimeout time.Duration
}

// Validate reports whether the tunnel can start at all: a ws/wss URL with a
// host, and a bearer token that is safe to place into an HTTP header
// (printable ASCII only — no whitespace or control characters).
func (cfg Config) Validate() error {
	trimmed := strings.TrimSpace(cfg.URL)
	if trimmed == "" {
		return errors.New("proxy_url is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid proxy_url: %v", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("proxy_url must use ws:// or wss://, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("proxy_url has no host")
	}
	if cfg.Token == "" {
		return errors.New("token is empty")
	}
	for i := 0; i < len(cfg.Token); i++ {
		if cfg.Token[i] < 0x21 || cfg.Token[i] > 0x7e {
			return errors.New("token contains spaces or control characters")
		}
	}
	return nil
}

// Warnings returns non-fatal hardening hints for the operator.
func (cfg Config) Warnings() []string {
	var out []string
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err == nil && u.Host != "" && u.Scheme == "ws" && !isLocalOrPrivateHost(u.Hostname()) {
		out = append(out, fmt.Sprintf(
			"proxy_url uses plaintext ws:// to %s — over a public network this leaks the tunnel token and MCP payloads; use wss://", u.Hostname()))
	}
	if len(cfg.Token) < minRecommendedTokenLen {
		out = append(out, fmt.Sprintf(
			"token is only %d characters — use ≥%d random bytes to resist brute force", len(cfg.Token), minRecommendedTokenLen))
	}
	return out
}

// isLocalOrPrivateHost reports whether the dial target stays on the local
// machine or a private network, where plaintext ws:// is a tolerable choice.
func isLocalOrPrivateHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// frameType is the envelope every tunnel frame carries.
type frameType struct {
	Type string `json:"type"`
}

// reqFrame is a relayed HTTP request, proxy -> client.
type reqFrame struct {
	Type    string              `json:"type"`
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

// resFrame is the captured HTTP response, client -> proxy.
type resFrame struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body,omitempty"`
}

// errorFrame reports a protocol-level problem in either direction.
type errorFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Status is a point-in-time snapshot of the tunnel client (for /system/stats).
type Status struct {
	Enabled         bool      `json:"enabled"`
	URL             string    `json:"url"`
	Connected       bool      `json:"connected"`
	ConnectCount    int64     `json:"connect_count"`
	LastConnectedAt time.Time `json:"last_connected_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

// Client maintains the tunnel to the remote proxy. Create with New, start
// with Run (blocking), observe with Status.
type Client struct {
	cfg Config

	mu              sync.Mutex
	connected       bool
	connectCount    int64
	lastConnectedAt time.Time
	lastError       string
}

// New creates a tunnel client. It does nothing until Run is called.
func New(cfg Config) *Client {
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.Handler == nil {
		cfg.Handler = http.NotFoundHandler()
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodPost
	}
	if cfg.Path == "" {
		cfg.Path = "/mcp"
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = DefaultMaxConcurrency
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = defaultPingInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	return &Client{cfg: cfg}
}

// Status returns a snapshot of the tunnel state.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Enabled:         true,
		URL:             c.cfg.URL,
		Connected:       c.connected,
		ConnectCount:    c.connectCount,
		LastConnectedAt: c.lastConnectedAt,
		LastError:       c.lastError,
	}
}

func (c *Client) setStatus(connected bool, errText string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if connected && !c.connected {
		c.connectCount++
		c.lastConnectedAt = time.Now()
		c.lastError = ""
	}
	c.connected = connected
	if errText != "" {
		c.lastError = errText
	}
}

// Run maintains the tunnel until ctx is done: connect, serve, reconnect with
// exponential backoff (1s doubling, 60s cap, ±20% jitter). It blocks, so
// call it in a goroutine.
func (c *Client) Run(ctx context.Context) {
	if err := c.cfg.Validate(); err != nil {
		log.Printf("mcp tunnel: not started: %v", err)
		return
	}
	backoff := backoffInitial
	for ctx.Err() == nil {
		lifetime := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if lifetime >= c.cfg.PingInterval {
			// The tunnel was healthy for a full keepalive cycle; collapse
			// the backoff so an outage does not punish a fresh restart.
			backoff = backoffInitial
		}
		wait := time.Duration((0.8 + 0.4*rand.Float64()) * float64(backoff))
		log.Printf("mcp tunnel: retrying in %s", wait.Round(10*time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > backoffCap {
			backoff = backoffCap
		}
	}
}

// connectAndServe dials the proxy, serves one tunnel session, and returns
// how long the connection lived.
func (c *Client) connectAndServe(ctx context.Context) time.Duration {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)

	dialer := &websocket.Dialer{
		Subprotocols:     []string{ProtocolName},
		HandshakeTimeout: handshakeTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
	}
	conn, resp, err := dialer.DialContext(ctx, c.cfg.URL, header)
	if err != nil {
		msg := fmt.Sprintf("dial: %v", err)
		if resp != nil {
			_ = resp.Body.Close()
			msg = fmt.Sprintf("dial: HTTP %d", resp.StatusCode)
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				msg += " (tunnel token rejected — check MCP_TUNNEL_TOKEN on both sides)"
			case http.StatusConflict:
				msg += " (another tunnel is already connected for this token)"
			case http.StatusTooManyRequests:
				msg += " (proxy rate-limited the dial)"
			}
		}
		c.setStatus(false, msg)
		log.Printf("mcp tunnel: %s", msg)
		return 0
	}
	if sp := conn.Subprotocol(); sp != "" && sp != ProtocolName {
		msg := fmt.Sprintf("proxy selected unknown subprotocol %q", sp)
		_ = conn.Close()
		c.setStatus(false, msg)
		log.Printf("mcp tunnel: %s", msg)
		return 0
	}

	c.setStatus(true, "")
	log.Printf("mcp tunnel: connected to %s", c.cfg.URL)
	lifetime, reason := c.serve(ctx, conn)
	_ = conn.Close()
	errText := ""
	if reason != nil && !errors.Is(reason, context.Canceled) {
		errText = reason.Error()
	}
	c.setStatus(false, errText)
	log.Printf("mcp tunnel: disconnected after %s (%v)", lifetime.Round(time.Second), reason)
	return lifetime
}

// serve runs one tunnel session until the connection fails or ctx is done.
// All frames and PINGs are written by a single writer goroutine. Requests run
// on a fixed worker pool fed by a bounded queue, so a flooding proxy can
// never make the client spawn goroutines or grow memory without bound —
// excess requests are answered with 503 instead of queued.
func (c *Client) serve(ctx context.Context, conn *websocket.Conn) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	writes := make(chan any, maxWritesBuffer)
	writerDone := make(chan struct{})
	go c.writeLoop(ctx, conn, writes, writerDone, func(err error) {
		if err != nil {
			log.Printf("mcp tunnel: write: %v", err)
		}
		cancel()
		// Unblock a stuck reader: the writer is gone, the tunnel is dead.
		_ = conn.Close()
	})

	// Fixed worker pool: concurrency cap and total frame memory are both
	// bounded by construction (pending fills up, then readLoop rejects).
	pending := make(chan reqFrame, c.cfg.MaxConcurrency*queuePerWorker)
	var workers sync.WaitGroup
	for i := 0; i < c.cfg.MaxConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case f := <-pending:
					c.worker(ctx, f, writes)
				}
			}
		}()
	}

	readDone := make(chan error, 1)
	go func() { readDone <- c.readLoop(ctx, conn, writes, pending) }()

	var reason error
	select {
	case reason = <-readDone:
		// Reader finished on its own (network error or deadline).
		cancel()
	case <-ctx.Done():
		// Parent shutdown: force-close to unblock the reader immediately.
		_ = conn.Close()
		reason = <-readDone
		cancel()
	}
	<-writerDone
	workers.Wait()
	return time.Since(start), reason
}

// writeLoop serializes all outbound traffic: res/error frames and PINGs.
func (c *Client) writeLoop(ctx context.Context, conn *websocket.Conn, writes <-chan any, done chan<- struct{}, fail func(error)) {
	defer close(done)
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Best-effort close handshake; the tunnel is being shut down.
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				fail(err)
				return
			}
		case frame := <-writes:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(frame); err != nil {
				fail(err)
				return
			}
		}
	}
}

// readLoop consumes tunnel frames until the connection fails or ctx is done.
// Proxy-controlled strings are truncated before they reach the log.
func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn, writes chan<- any, pending chan<- reqFrame) error {
	conn.SetReadLimit(maxFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(readIdleLimit))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readIdleLimit))
	})
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(readIdleLimit))
		if mt != websocket.TextMessage {
			return errors.New("unexpected binary frame (only JSON text frames are allowed)")
		}
		var env frameType
		if err := json.Unmarshal(data, &env); err != nil {
			c.trySend(ctx, writes, newErrorFrame(codeBadFrame, "frame is not JSON"))
			continue
		}
		switch env.Type {
		case "req":
			var f reqFrame
			if jsonErr := json.Unmarshal(data, &f); jsonErr != nil || f.ID == "" || len(f.ID) > maxFrameIDLen {
				c.trySend(ctx, writes, newErrorFrame(codeBadRequest, "req frame: id is missing or invalid"))
				continue
			}
			select {
			case pending <- f:
			case <-ctx.Done():
				return ctx.Err()
			default:
				// Queue full: answer immediately instead of growing.
				c.trySend(ctx, writes, resFrame{
					Type:        "res",
					ID:          f.ID,
					Status:      http.StatusServiceUnavailable,
					Body:        "overloaded",
					ContentType: "text/plain; charset=utf-8",
				})
			}
		case "error":
			var e errorFrame
			if err := json.Unmarshal(data, &e); err == nil {
				log.Printf("mcp tunnel: proxy reported %s: %s",
					truncateLog(e.Code, maxLogCodeLen), truncateLog(e.Message, maxLogMessageLen))
			}
		default:
			c.trySend(ctx, writes, newErrorFrame(codeBadFrame, fmt.Sprintf("unknown frame type %q", env.Type)))
		}
	}
}

// worker consumes one queued request and queues its response frame.
func (c *Client) worker(ctx context.Context, f reqFrame, writes chan<- any) {
	res, ok := c.runHandler(ctx, f)
	if !ok {
		return
	}
	select {
	case writes <- res:
	case <-ctx.Done():
	}
}

// runHandler runs one relayed request under a hard budget so a wedged
// handler cannot occupy a worker forever. ok=false means the tunnel was torn
// down while the request was in flight and no answer should be sent. The
// abandoned handler goroutine gets a canceled context (best effort — Go code
// that ignores ctx may linger, but it cannot hold the pool hostage).
func (c *Client) runHandler(ctx context.Context, f reqFrame) (resFrame, bool) {
	reqCtx, cancelReq := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancelReq()

	done := make(chan resFrame, 1)
	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("mcp tunnel: panic handling req %s: %v", f.ID, err)
				done <- resFrame{
					Type: "res", ID: f.ID, Status: http.StatusInternalServerError,
					ContentType: "text/plain; charset=utf-8", Body: "internal server error",
				}
			}
		}()
		done <- c.handleReq(reqCtx, f)
	}()

	select {
	case res := <-done:
		return res, true
	case <-reqCtx.Done():
		if ctx.Err() != nil {
			return resFrame{}, false
		}
		return resFrame{
			Type:        "res",
			ID:          f.ID,
			Status:      http.StatusGatewayTimeout,
			Body:        "gateway timeout",
			ContentType: "text/plain; charset=utf-8",
		}, true
	}
}

// truncateLog bounds attacker-influenced strings before they hit the log.
func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// newErrorFrame builds an error frame with the envelope type stamped in.
func newErrorFrame(code, message string) errorFrame {
	return errorFrame{Type: "error", Code: code, Message: message}
}

// trySend queues an outbound frame without blocking the caller indefinitely.
func (c *Client) trySend(ctx context.Context, writes chan<- any, frame any) {
	select {
	case writes <- frame:
	case <-ctx.Done():
	}
}

// handleReq guards the relay surface (method/path), rebuilds the request, and
// captures the handler's response for the res frame.
func (c *Client) handleReq(ctx context.Context, f reqFrame) resFrame {
	if !strings.EqualFold(f.Method, c.cfg.Method) {
		return resFrame{
			Type: "res", ID: f.ID, Status: http.StatusMethodNotAllowed,
			ContentType: "text/plain; charset=utf-8", Body: "method not allowed",
		}
	}
	if f.Path != c.cfg.Path {
		return resFrame{
			Type: "res", ID: f.ID, Status: http.StatusNotFound,
			ContentType: "text/plain; charset=utf-8", Body: "not found",
		}
	}

	r, err := http.NewRequestWithContext(ctx, c.cfg.Method,
		"http://mentis.tunnel"+c.cfg.Path, strings.NewReader(f.Body))
	if err != nil {
		return resFrame{
			Type: "res", ID: f.ID, Status: http.StatusBadRequest,
			ContentType: "text/plain; charset=utf-8", Body: "bad request",
		}
	}
	for key, values := range f.Headers {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}

	rec := newRecorder()
	c.cfg.Handler.ServeHTTP(rec, r)

	res := resFrame{Type: "res", ID: f.ID, Status: rec.status()}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		res.ContentType = ct
	}
	res.Body = rec.Body()
	return res
}

// recorder is a minimal http.ResponseWriter capturing the relayed response.
type recorder struct {
	header http.Header
	code   int
	buf    bytes.Buffer
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.buf.Write(p)
}

func (r *recorder) Flush() {}

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

// Body returns the captured response body.
func (r *recorder) Body() string { return r.buf.String() }
