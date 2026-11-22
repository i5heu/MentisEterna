package llm

import "sync"

// Routing sources of a chat call (ChatClient.Source / CallStats.Source).
// A call is REMOTE when an llm_inferencer note bound its usage to a remote
// provider; "local" and "config" both land on the app's config.toml
// feature/tier system (the local LLM) — "local" is the explicit
// {local: true} pin, "config" the plain fallback.
const (
	ChatSourceNote   = "note"   // llm_inferencer binding → remote provider
	ChatSourceLocal  = "local"  // explicit {local: true} pin → config.toml
	ChatSourceConfig = "config" // config.toml feature/tier fallback
)

// CallStats describes one chat completion call — successful or failed —
// for the app's LLM statistics (surfaced in the llm_inferencer note's
// view). Token counts are exactly what the backend reported in the
// OpenAI-compatible response `usage` block; backends that omit it leave
// them zero (calls are still counted).
type CallStats struct {
	Usage    string // app usage id ("rss_classify", "title", ...) — "" = untagged client
	Source   string // ChatSource* ("note" = remote, "local"/"config" = config.toml system)
	Provider string // provider of the binding note ("openrouter", "mistral", ...) — "" for local routing
	BaseURL  string
	Model    string
	// OK is false when the call failed (network, HTTP, or decode error);
	// Error carries the error text then, and token counts are zero.
	OK    bool
	Error string
	// Tokens as reported by the backend (0 when absent or the call failed).
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// DurationMS covers the whole exchange (request + response decode).
	DurationMS int64
}

// callStatsSink is the global recorder installed once at server startup
// (internal/server wires the llm_inferencer plugin's RecordCall through
// notetype.CallStatsRecorder). Calls report via emitCallStats.
var (
	callStatsMu   sync.RWMutex
	callStatsSink func(CallStats)
)

// SetCallStatsSink installs the global per-call statistics recorder
// (nil removes it). The sink must be safe for concurrent use, must not
// block (chat calls report inline), and must swallow its own errors —
// statistics are best-effort and never break an LLM call.
func SetCallStatsSink(fn func(CallStats)) {
	callStatsMu.Lock()
	callStatsSink = fn
	callStatsMu.Unlock()
}

// emitCallStats reports one call to the installed sink. No sink → no-op.
// A panicking sink is recovered: telemetry must never take a call down.
func emitCallStats(s CallStats) {
	callStatsMu.RLock()
	fn := callStatsSink
	callStatsMu.RUnlock()
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(s)
}
