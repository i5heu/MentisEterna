package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestOpenAIEndpoint is a black-box contract test for the endpoint builder.
// Failure modes prevented: doubled version segments (…/v1/v1/chat/completions
// → provider 404), broken trailing-slash bases, mangled full-endpoint pastes,
// and empty bases (tests/defaults).
func TestOpenAIEndpoint(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		base string // user-supplied base URL
		path string // OpenAI endpoint path segment (chat/completions, models…)
		want string
	}{
		// Prevents the doubled /v1: a base ending in the version segment must
		// not produce /v1/v1/... (the "404 Not Found" misconfiguration).
		"openai-sdk-style v1 base": {
			base: "https://openrouter.ai/api/v1", path: "chat/completions",
			want: "https://openrouter.ai/api/v1/chat/completions",
		},
		// Prevents regressions for canonical API-root bases like the built-in
		// provider defaults and config.toml [llm] base_url.
		"api root base": {
			base: "https://openrouter.ai/api", path: "chat/completions",
			want: "https://openrouter.ai/api/v1/chat/completions",
		},
		// Host-only bases (a LocalAI root) must still get the version segment
		// exactly once.
		"host only base": {
			base: "http://127.0.0.1:8080", path: "embeddings",
			want: "http://127.0.0.1:8080/v1/embeddings",
		},
		// Trailing slashes are a common paste artifact; the endpoint must be
		// normalized before concatenation.
		"trailing slashes on v1 base": {
			base: "https://api.mistral.ai/v1//", path: "models",
			want: "https://api.mistral.ai/v1/models",
		},
		// Pasting the complete endpoint URL must be accepted verbatim rather
		// than appended to (…/chat/completions/v1/chat/completions).
		"full endpoint pasted": {
			base: "https://openrouter.ai/api/v1/chat/completions", path: "chat/completions",
			want: "https://openrouter.ai/api/v1/chat/completions",
		},
		// Whitespace around the base (spreadsheet copy/paste) must not leak
		// into the URL.
		"padded base": {
			base: "  https://api.mistral.ai/v1 ", path: "chat/completions",
			want: "https://api.mistral.ai/v1/chat/completions",
		},
		// Empty base (unit-test clients, zero values) still yields the bare
		// endpoint instead of panicking or producing a double slash.
		"empty base": {
			base: "", path: "chat/completions",
			want: "/v1/chat/completions",
		},
		// Path arguments given with slashes on both sides (defensive styles)
		// must not double separators.
		"sloppy path separators": {
			base: "https://host/api", path: "/chat/completions/",
			want: "https://host/api/v1/chat/completions",
		},
		// Multi-segment paths (audio/transcriptions) keep their inner slashes.
		"multi segment path": {
			base: "https://host:8188", path: "audio/transcriptions",
			want: "https://host:8188/v1/audio/transcriptions",
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := OpenAIEndpoint(tc.base, tc.path); got != tc.want {
				t.Errorf("OpenAIEndpoint(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
			}
		})
	}
}

// TestOpenAIEndpointConventionsAgree is the actual regression behind the
// user-facing bug: the "documented OpenAI base" (.../v1) and the "API root"
// convention MUST dial the exact same URL — they used to differ by a doubled
// version segment (404 {"error":{"message":"Not Found","code":404}}).
func TestOpenAIEndpointConventionsAgree(t *testing.T) {
	t.Parallel()
	conventions := map[string]string{
		"api root (app convention)": "https://openrouter.ai/api",
		"openai sdk base (+/v1)":    "https://openrouter.ai/api/v1",
		"v1 base with trailing cut": "https://openrouter.ai/api/v1/",
		"full endpoint pasted":      "https://openrouter.ai/api/v1/chat/completions",
	}
	want := "https://openrouter.ai/api/v1/chat/completions"
	for name, base := range conventions {
		base := base
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := OpenAIEndpoint(base, "chat/completions"); got != want {
				t.Errorf("base %q produced %q, want %q", base, got, want)
			}
		})
	}
}

// TestOpenAIEndpointIdempotent: building an endpoint from the built endpoint
// must be a fixed point (idempotence) — consumers that re-normalize a URL
// they already built must not keep stripping/growing anything.
func TestOpenAIEndpointIdempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		scheme := rapid.SampledFrom([]string{"http", "https"}).Draw(rt, "scheme")
		host := rapid.StringMatching(`[a-z0-9.-]+(\.[a-z]{2,10})`).Draw(rt, "host")
		hasPort := rapid.Bool().Draw(rt, "hasPort")
		port := ""
		if hasPort {
			port = fmt.Sprintf(":%d", rapid.IntRange(1, 65535).Draw(rt, "port"))
		}
		base := scheme + "://" + host + port
		if rapid.Bool().Draw(rt, "hasV1") {
			base += "/v1"
		}
		endpoint := OpenAIEndpoint(base, "chat/completions")
		// Invariant: rebuilding from the built URL changes nothing.
		if again := OpenAIEndpoint(endpoint, "chat/completions"); again != endpoint {
			rt.Errorf("not idempotent: %q → %q", endpoint, again)
		}
		// Invariant: the result always carries the versioned path exactly once.
		if !strings.HasSuffix(endpoint, "/v1/chat/completions") {
			rt.Errorf("endpoint %q must end with /v1/chat/completions", endpoint)
		}
		if strings.Contains(endpoint, "/v1/v1") {
			rt.Errorf("endpoint %q contains a doubled version segment", endpoint)
		}
	})
}

// recordingServer accepts exactly the expected endpoint, records the first
// requested path, and 404s everything else (the real-world provider
// behavior for a doubled version segment). The recorded path is read via a
// buffered channel — no sleeps.
func recordingServer(t *testing.T, expectedPath, reply string) (baseV1 string, paths <-chan string) {
	t.Helper()
	ch := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case ch <- r.URL.Path:
		default:
		}
		if r.URL.Path != expectedPath {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not Found","code":404}}`))
			return
		}
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1", ch
}

// TestChatClientAcceptsV1StyleBase is the end-to-end regression for the
// reported bug: a base URL with the OpenAI-SDK version segment
// (e.g. https://openrouter.ai/api/v1 — what provider docs tell users to
// paste) previously produced /v1/v1/chat/completions and the provider
// 404'd. The client must dial the same endpoint as an API-root base.
func TestChatClientAcceptsV1StyleBase(t *testing.T) {
	t.Parallel()
	// The server only serves /v1/chat/completions — a doubled path 404s,
	// which failed the Complete call before the fix.
	baseV1, paths := recordingServer(t,
		"/v1/chat/completions",
		`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`)

	c := NewChatClientWithKey(baseV1, "prov/tiny", "sk-key")
	reply, err := c.Complete([]ChatMessage{{Role: "user", Content: "Reply with the single word: OK"}})
	if err != nil {
		t.Fatalf("Complete with /v1-style base: %v", err)
	}
	if reply != "OK" {
		t.Errorf("reply = %q, want OK", reply)
	}
	select {
	case got := <-paths:
		if got != "/v1/chat/completions" {
			t.Errorf("dialed %q, want /v1/chat/completions", got)
		}
	default:
		t.Error("no request recorded")
	}
}

// TestChatClientErrorIncludesURL: non-200 responses must name the endpoint
// they dialed — a bare "404" is undebuggable when the base URL is the
// problem (it usually is).
func TestChatClientErrorIncludesURL(t *testing.T) {
	t.Parallel()
	baseV1, _ := recordingServer(t, "/v1/chat/completions", "")
	wrongBase := strings.TrimSuffix(baseV1, "/v1") + "/wrong-root"
	c := NewChatClient(wrongBase, "m")

	_, err := c.Complete([]ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected an error against a wrong base URL")
	}
	// The error must expose the dialed URL for debugging (suffix match, not
	// an equality check on the full message — the body is provider data).
	if !strings.Contains(err.Error(), "/wrong-root/v1/chat/completions") {
		t.Errorf("error should name the dialed URL, got: %v", err)
	}
}

// TestEmbeddingClientAcceptsV1StyleBase keeps the /v1 normalization honest
// for the embeddings endpoint too (config.toml [llm] base_url, backfill).
func TestEmbeddingClientAcceptsV1StyleBase(t *testing.T) {
	t.Parallel()
	baseV1, paths := recordingServer(t,
		"/v1/embeddings",
		`{"data":[{"embedding":[0.1,0.2,0.3]}]}`)

	c := &EmbeddingClient{
		BaseURL: baseV1,
		Model:   "embed",
		http:    newLLMHTTPClient(),
	}
	emb, err := c.GenerateEmbedding("hello")
	if err != nil {
		t.Fatalf("GenerateEmbedding with /v1-style base: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("embedding = %v, want 3 dims", emb)
	}
	select {
	case got := <-paths:
		if got != "/v1/embeddings" {
			t.Errorf("dialed %q, want /v1/embeddings", got)
		}
	default:
		t.Error("no request recorded")
	}
}
