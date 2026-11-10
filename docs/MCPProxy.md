# MCP Remote Proxy — Requirements & Protocol

This document specifies the **remote proxy** that runs on a publicly reachable
server and relays MCP requests to a firewalled MentisEterna instance. The proxy
is **not part of this repository** — this file is the complete build contract
for it. The client side (the outbound tunnel inside MentisEterna) lives in
`internal/mcptunnel/` and implements exactly this protocol.

```
External agent                     Remote proxy                     MentisEterna
(Mistral Vibe,                     (public server,                  (LAN, behind
 anywhere)                          this spec)                       firewall)

     │                                   │                                │
     │  HTTPS POST /mcp                  │                                │
     │  Authorization: Bearer <mcp-token>                                │
     ├──────────────────────────────────▶│                                │
     │                                   │  WSS tunnel (outbound,         │
     │                                   │  permanent, auth'd)            │
     │                                   │◀═══════════════════════════════╡
     │                                   │ {"type":"req", ...}  ────────▶ │
     │                                   │◀──────────── {"type":"res",...}│
     │◀──────────────────────────────────┤                                │
     │  HTTP response (JSON-RPC)         │                                │
```

Key property: MentisEterna sits on a private LAN and accepts **no inbound
connections from the internet**. It dials the proxy once and keeps that single
connection alive permanently. The proxy exposes a normal HTTPS MCP endpoint to
external agents and multiplexes their requests over that connection.

## Terminology

| Term | Meaning |
|---|---|
| **Proxy** | The program built from this spec. Runs on the public server. |
| **Tunnel** | The single authenticated WebSocket from MentisEterna to the proxy. |
| **Binding** | Proxy-side config tuple: one `public_path` + one `tunnel_token` (+ limits). Serves MentisEterna instances. |
| **Agent** | An external MCP client (e.g. Mistral Vibe) hitting the proxy's public MCP endpoint. |
| **Frame** | One JSON text message on the tunnel WebSocket. |
| **Tunnel token** | Secret authenticating the tunnel (`MCP_TUNNEL_TOKEN`, MentisEterna → proxy). Also configured on the binding. |
| **MCP token** | Secret authenticating agents (`MCP_TOKEN`). Known **only** to agents and MentisEterna — never configured on the proxy. |

## Security model

1. **Two separated secrets.** The tunnel token proves MentisEterna's identity to
   the proxy. The MCP token proves an agent's identity to MentisEterna. The proxy
   relays the `Authorization` header but MUST NOT need to know, store, or
   validate the MCP token. Compromising the proxy must not grant app-level MCP
   access (worst case: denial of the public endpoint).
2. **Authoritative auth stays in MentisEterna.** Relayed requests are processed
   by the exact same handler that serves the LAN `/mcp` endpoint, including its
   `Authorization: Bearer` / cookie checks. Edge checks on the proxy are
   purely protective (see below), never authoritative.
3. **TLS is mandatory** on both public surfaces (agent → proxy, tunnel) unless
   the proxy is intentionally fronted by a TLS-terminating reverse proxy on
   localhost.
4. Neither secret is ever written to logs in the clear (log at most a SHA-256
   prefix).
5. **Token entropy.** Both tokens are bearer credentials; use ≥ 32 random
   bytes (e.g. `openssl rand -hex 32`). MentisEterna logs a warning for
   shorter tunnel tokens. Format restriction: printable ASCII only (the
   token travels in an HTTP header).
6. **Fallback caveat.** If MentisEterna is configured without
   `MCP_TUNNEL_TOKEN` it falls back to `MCP_TOKEN` (the agents' token) and
   logs a security warning — in that mode a compromised proxy holds a valid
   MCP token, defeating the separation above. Prefer distinct tokens.

## Tunnel endpoint (proxy side)

The proxy listens for WebSocket upgrades at a configured path (suggest
`/tunnel`), one tunnel per binding, selected by the tunnel token.

**Upgrade request sent by MentisEterna (normative client behavior):**

```
GET /tunnel HTTP/1.1
Host: proxy.example.com
Upgrade: websocket
Connection: Upgrade
Authorization: Bearer <tunnel-token>
Sec-WebSocket-Protocol: mentis-tunnel-v1
Sec-WebSocket-Key: ...
```

Proxy requirements:

| # | Requirement |
|---|---|
| T1 | Validate `Authorization: Bearer` against the binding's `tunnel_token` using a constant-time compare, **before** upgrading. Reject mismatches with HTTP `401` (no upgrade). |
| T2 | If `Sec-WebSocket-Protocol: mentis-tunnel-v1` was offered and supported, the upgrade response MUST echo exactly `mentis-tunnel-v1`. It MUST NOT select a different subprotocol. Omitting the field is tolerated by the client. |
| T3 | **One active tunnel per binding.** A second upgrade attempt with the same token (while the first tunnel is alive) MUST be rejected with HTTP `409`. The existing tunnel stays untouched. The client keeps retrying with capped backoff, so this must be a cheap answer. |
| T4 | The client reconnects with exponential backoff (1 s doubling, cap 60 s, ±20 % jitter) after any disconnect, forever, until shut down. The proxy MUST tolerate unlimited reconnect attempts at that rate (per-token dial rate limiting is allowed, e.g. answer `429`). |
| T5 | The client sends protocol-level WS PINGs every 30 s. The proxy MUST reply with PONG (any RFC 6455 library does this by default — with gorilla/websocket, while the connection is being read), MUST NOT close a tunnel that is quiet otherwise, and MUST NOT close a healthy tunnel before 90 s of silence. |
| T6 | The proxy SHOULD send its own PING every 45 s and close the tunnel after ≥ 2 missed PONGs (half-open sweep). Closing is safe: a dropped tunnel shows up as `503`/`502` for agents, and MentisEterna reconnects. |
| T7 | While a tunnel for a binding is absent, agent requests to that binding's public path MUST fail with `503` (see below). |
| T8 | Tunnel frames larger than 4 MiB MUST NOT be sent by the proxy; the client drops the connection if it receives one. |

## Public MCP endpoint (proxy side, agent-facing)

Per binding, the proxy serves a single route `{public_path}` (suggest `/mcp`)
that behaves like the streamable-http subset MentisEterna serves on the LAN:

| # | Requirement |
|---|---|
| P1 | `POST {public_path}`: validate edge limits (P4–P6), then relay to the bound tunnel as a `req` frame (see below), wait for the matching `res` frame, and answer the agent with that status/Content-Type/body. |
| P2 | Relayed request MUST carry the agent's `Authorization` header unchanged (and MAY carry `Content-Type`); MentisEterna does the real auth. The proxy itself MUST NOT require knowledge of the MCP token. |
| P3 | `GET` / `DELETE` (and any other method) on `{public_path}` MUST be answered locally with `405` + `Allow: POST`. They are never relayed — MentisEterna's local endpoint answers 405 for them anyway. |
| P4 | Request bodies larger than the binding's `max_body_bytes` (SHOULD default 1 MiB, matching MentisEterna's `max_json_body_bytes`) MUST be rejected with `413` before relaying. Hard cap 4 MiB regardless of configuration (frame limit, T8). |
| P5 | The proxy MUST wait at most `request_timeout` for the `res` frame; default SHOULD be 120 s (MCP tool calls can trigger LLM/embedding work, and MentisEterna's own request budget is ~115 s, slightly below, so agents normally get the client's definitive 504 rather than a proxy-side timeout). On timeout, answer the agent with `502` (with a short JSON error body) and abandon the pending request id. A late `res` for an abandoned or unknown id MUST be discarded (log only). Client-sent `503` (overloaded) and `504` (backend timeout) statuses MUST pass through to the agent unchanged. |
| P6 | Edge protection SHOULD be configurable per binding: rate limits (recommend default 30 requests/min per source IP with a burst of 10), and rejecting requests with no `Authorization` header at all (`401`) — this is protective only; a present-but-wrong token is still relayed so MentisEterna's real `401` semantics reach the agent. Edge token allow-lists are an optional extra, not required. |
| P7 | If the tunnel connection breaks while a request is in flight, answer that agent with `502`, JSON body. |
| P8 | The relay is verbatim: do not parse, batch, or re-encode JSON bodies; compression is left to whatever fronts the proxy. |
| P9 | Response to the agent: HTTP status from `res.status`, `Content-Type` from `res.content_type` (default `application/json`), body verbatim from `res.body`. `202 Accepted` (MCP notification) and 4xx/5xx statuses MUST pass through unchanged. The public surface is exactly one route: `POST {public_path}` — no health probe or any other subpath is defined. |
| P10 | Headers with hop-by-hop semantics (Connection, Keep-Alive, Upgrade, ...) are never relayed. Cookies are relayed verbatim; MentisEterna accepts session cookies as an alternative auth, but agents normally use Bearer tokens. |

Result summary for agents: the proxy is invisible — a correct proxy makes
`POST https://proxy.example.com/mcp` behave identically to MentisEterna's LAN
`POST http://mentis.lan:8080/mcp`, plus `502`/`503` when the backend is
unreachable.

## Frame protocol (tunnel)

Transport: one WebSocket text message = one UTF-8 JSON frame (binary messages
MUST NOT be sent by either side). Depth of nesting is whatever JSON allows; both
sides treat frames opaquely aside from the envelope fields below.

### `req` — proxy → MentisEterna

```json
{
  "type": "req",
  "id": "b3f0a2",
  "method": "POST",
  "path": "/mcp",
  "headers": { "Authorization": ["Bearer <agent-token>"] },
  "body": "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}"
}
```

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Always `"req"`. |
| `id` | string | Proxy-generated unique id (per tunnel lifetime). Client echoes it. Free-form, ≤ 128 bytes. |
| `method` | string | HTTP method, always `POST` today. |
| `path` | string | Request path, always `/mcp` today. Kept for protocol evolution; the client validates it. |
| `headers` | object | String arrays per header name, canonical Go casing (`Authorization`). Confidentiality note: values relay 1:1. |
| `body` | string | Raw request body (UTF-8; JSON-RPC bodies are JSON text). Empty string when there is no body. |

### `res` — MentisEterna → proxy

```json
{
  "type": "res",
  "id": "b3f0a2",
  "status": 200,
  "content_type": "application/json",
  "body": "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{...}}"
}
```

| Field | Type | Meaning |
|---|---|---|
| `type` | string | Always `"res"`. |
| `id` | string | Matches the `req`. |
| `status` | int | HTTP status MentisEterna produced (200, 202, 400, 401, 404, 405, 413, 500...). |
| `content_type` | string | Value for the `Content-Type` header. `"application/json"` when omitted. |
| `body` | string | Raw response body, `""` when empty. |

### `error` — either direction

```json
{ "type": "error", "code": "bad_frame", "id": "b3f0a2", "message": "..." }
```

Codes today: `bad_frame` (malformed/unknown frame), `bad_request` (request id
unusable). Unknown codes MUST be tolerated (log only). Overload is not an
error frame: it is a normal `res` with status `503` (see below). A frame the
receiver considers fatal closes the tunnel (the side that closes SHOULD send
a WS close message first; MentisEterna always does). After an `error` the
connection state (pending ids) stays valid — the proxy may keep serving with a
follow-up `res` for the same id.

### Keepalive

Keepalive is protocol-level: WS PING/PONG control frames only (T5/T6). No JSON
frame type exists for it — a receiver that gets an unknown `type` answers with
`error` (`bad_frame`) and continues.

### Concurrency, backpressure, and time budgets

- Frames are exchanged concurrently; there is NO ordering guarantee across
  different ids.
- The proxy MUST support at least 16 in-flight ids per tunnel and MUST NOT
  coalesce or cross-wire pending ids.
- MentisEterna runs a fixed worker pool (`[mcp] proxy_max_concurrency`, default
  8) fed by a bounded queue (2 slots per worker). Excess requests are answered
  immediately with `res` `503` (`body: "overloaded"`) — the proxy MUST relay
  that to the agent unchanged. **The client never queues unbounded**, so a
  flooding proxy cannot exhaust its memory; keep that property if the proxy
  buffers agent requests too (bound per-tunnel in-flight, reject early).
- Each relayed request has a hard budget of ~115 s on the MentisEterna side.
  If the backend wedges, the client answers `res` `504` (`body: "gateway
  timeout"`) and the worker is freed. The proxy's `request_timeout` (P5,
  default 120 s) SHOULD therefore be ≥ the client budget so agents normally
  get the definitive 504 instead of a proxy-side timeout.
- Proxy-controlled frame payloads are truncated before they reach
  MentisEterna's logs (log-flood resilience).
- If either side closes the tunnel with requests outstanding, those requests
  fail: proxy answers its agents `502` (P7), MentisEterna abandons its work.
- The client will not start with a non-ws/wss URL or a token containing
  control characters, and it logs warnings for plaintext `ws://` dials to
  non-private hosts and for short tokens (< 32 chars).

## Deliverables & acceptance checklist

The proxy may be written in any language/stack (a small Go, Node, or Rust
service is equally fine, as is nginx-fronted deployment). It MUST pass:

1. `initialize` JSON-RPC POST through the proxy returns a JSON-RPC result with
   `serverInfo.name = "MentisEterna"` (curl with a *wrong* Bearer first: the
   relayed answer must be `401` — auth is MentisEterna's, not the proxy's).
2. `tools/list` returns all 5 tools.
3. Tunnel down (MentisEterna stopped): proxied POST returns `503`, and a fresh
   start of MentisEterna reconnects automatically and serves again.
4. Killing an established tunnel (client side or network side close) → next
   agent request `503` or `502`, and reconnect succeeds within the backoff cap.
5. Idle (no agent traffic) for 10 minutes with only PING/pong traffic on the
   tunnel → still connected.
6. Second tunnel with the same token while one is alive → upgrade rejected
   `409`.
7. `GET`/`DELETE` on `{public_path}` → `405` + `Allow: POST`, no relay.
8. Agent body larger than `max_body_bytes` → `413`, no relay.
9. Agent request slower than `request_timeout` → agent gets `502` (P5), tunnel
   keeps working afterwards.
10. 16 concurrent agent requests are all answered correctly (no cross-talked
    ids, no mixed bodies).

Hardening checks (the failure modes the client must survive — mirror these
against the proxy in test builds):

11. Flood: 1000 rapid relayed requests with a slow backend → every id answered
    exactly once, a mixture of 200/503 (`overloaded`), tunnel stays healthy.
12. A handler that never returns → client answers `504` after ~115 s, tunnel
    keeps serving the next request.
13. A handler that panics → client answers `500`, tunnel stays alive.
14. Oversized (binary > 4 MiB read limit) or non-JSON text frames → tunnel
    dropped; client reconnects; no leaked goroutines.
15. `ws://` (non-TLS) proxy URLs to public hosts and control-character tokens
    are refused/warned about at startup rather than silently accepted.

Suggested proxy configuration surface (binding list):

```toml
listen = "0.0.0.0:443"        # or front by nginx/caddy
tunnel_path = "/tunnel"

[[binding]]
public_path = "/mcp"          # agent-facing path of this binding
tunnel_token = "..."          # = MentisEterna's MCP_TUNNEL_TOKEN (env there)
max_body_bytes = 1048576
request_timeout = "120s"
require_authorization = true  # protective 401 for header-less requests
# rate_limit  = "30/minute burst=10 per ip"
```

## MentisEterna side (implemented in this repo)

For reference, so builder and client match:

| Setting | Value |
|---|---|
| Enable | `[mcp] proxy_url` non-empty **and** `MCP_TUNNEL_TOKEN` env set (falls back to `MCP_TOKEN` with a security warning), **and** `[mcp] disabled = false` |
| Startup validation | Refuses to run with a non-`ws`/`wss` URL or a token containing control characters; warns on plaintext `ws://` to non-private hosts and tokens < 32 chars |
| Dial | `websocket.Dialer` with `Subprotocols: ["mentis-tunnel-v1"]`, `Authorization: Bearer <tunnel-token>` |
| Backoff | Exponential 1 s → 60 s cap, ±20 % jitter; immediate first attempt; reconnects forever until ctx cancel |
| Ping | WS PING every 30 s (write deadline 10 s); connection dropped if no traffic ~90 s |
| Frame cap | Reads > 4 MiB → drop connection (T8) |
| Concurrency | Fixed worker pool of 8 (`proxy_max_concurrency`) + bounded queue (2/worker); overflow → `res` 503 "overloaded", never unbounded queueing |
| Request budget | ~115 s per request; wedged handler → `res` 504 "gateway timeout", worker freed |
| Panic safety | A panicking handler answers `res` 500; the connection and process stay alive |
| Valid frames | method `POST` + path `/mcp` only; anything else → `res` 404/405 without calling the app. Malformed JSON frames → `error` frame, tunnel stays usable; binary frames or a mismatching subprotocol → tunnel closed |
| Disconnect semantics | WS close message 1000 on shutdown; pending results are dropped (P5 covers the agent side) |
| Logging | Proxy-controlled strings truncated (64/256 bytes); tokens never logged |
| LAN `/mcp` | Unchanged — the tunnel adds the public path, it replaces nothing |
