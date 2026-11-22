package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/i5heu/MentisEterna/internal/config"
)

// The call-stats sink is package-global shared state: everything that
// installs it in tests must serialize against this mutex (install → emit →
// restore happens under it), which keeps sink-using subtests isolated even
// though the production sink is set exactly once at startup.
var sinkTestMu sync.Mutex

// lockSink serializes a subtest's use of the global sink and restores the
// no-op state afterwards. The returned function restores AND unlocks.
// lockSink serializes a test's use of the global sink and restores the
// no-op state afterwards. The returned function restores AND unlocks. It
// takes a testing.TB so rapid property bodies can use it too.
func lockSink(fn func(CallStats)) func() {
	sinkTestMu.Lock()
	SetCallStatsSink(fn)
	return func() {
		SetCallStatsSink(nil)
		sinkTestMu.Unlock()
	}
}
func chatReply(content, usageJSON string) string {
	usage := ""
	if usageJSON != "" {
		usage = `,"usage":` + usageJSON
	}
	return `{"choices":[{"message":{"role":"assistant","content":` + jsonString(content) + `}}]` + usage + `}`
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// newChatServer serves /v1/chat/completions with the given status and body.
func newChatServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fastRetries zeroes the [llm] retry/throttle knobs for a test (the
// built-in defaults retry 5xx/connection errors 3 times with a 3 s gap and
// pace requests 1 s apart — correct in production, far too slow for tests)
// and restores defaults afterwards.
func fastRetries(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = 0
	config.Get().LLM.RetryDelayMS = 0
	config.Get().LLM.RequestCooldownMS = 0
}

// TestChatClientFactoryTags: every config.toml feature factory tags the
// client with its usage id and the "config" routing source; generic
// constructors leave clients untagged. Failure mode prevented: a factory
// regressing to an untagged client would silently vanish from the
// statistics ledger.
func TestChatClientFactoryTags(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		client *ChatClient
		usage  string
		source string
	}{
		// Title client must report as the "title" usage — the ledger's
		// per-usage token rows depend on it.
		"title": {client: NewTitleClient(), usage: "title", source: ChatSourceConfig},
		// The tags feature is its own usage id, distinct from title.
		"tags": {client: NewAutoTaggerClient(), usage: "tags", source: ChatSourceConfig},
		// Subtask generation must not be conflated with tags/title tokens.
		"subtasks": {client: NewSubTaskClient(), usage: "subtasks", source: ChatSourceConfig},
		// The RSS classifier fallback client (config.toml tier) is the
		// "rss_classify" usage on the local side of the split.
		"rss_classify": {client: NewRSSClassifierClient(), usage: "rss_classify", source: ChatSourceConfig},
		// The RSS summarizer fallback client is its own usage — summarize
		// tokens must not be conflated with evaluation tokens.
		"rss_summarize": {client: NewRSSSummarizerClient(), usage: "rss_summarize", source: ChatSourceConfig},
		// The usage-aware resolver entry point tags like the factories.
		"feature_title": {client: NewFeatureChatClient("title"), usage: "title", source: ChatSourceConfig},
		// Generic clients stay untagged: their rows land as "" usage /
		// "" source instead of pretending to be a known usage.
		"generic_untagged":     {client: NewChatClient("http://x", "m")},
		"generic_key_untagged": {client: NewChatClientWithKey("http://x", "m", "k")},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if tc.client.Usage != tc.usage || tc.client.Source != tc.source {
				t.Fatalf("tags = (%q, %q), want (%q, %q)",
					tc.client.Usage, tc.client.Source, tc.usage, tc.source)
			}
			if tc.client.Provider != "" {
				t.Fatalf("factory client carries provider %q, want empty", tc.client.Provider)
			}
		})
	}
}

// TestNewFeatureChatClientUnknownUsage: unknown usage ids must yield nil
// (callers treat nil as "no client"), never a half-tagged client.
// Failure mode prevented: a wrong nil/instance decision panics call sites.
func TestNewFeatureChatClientUnknownUsage(t *testing.T) {
	t.Parallel()
	if c := NewFeatureChatClient("not-in-catalog"); c != nil {
		t.Fatalf("want nil client for unknown usage, got %+v", c)
	}
}

// TestTagUsageMutatesAndChains: the resolver labels remote clients with
// TagUsage; it must set all three fields and return the same instance so
// one-liner tagging compiles everywhere. Failure mode prevented: a copy or
// a partial tag would record rows under the wrong routing.
func TestTagUsageMutatesAndChains(t *testing.T) {
	t.Parallel()
	c := NewChatClient("http://x", "m")
	same := c.TagUsage("rss_classify", ChatSourceNote, "openrouter")
	if same != c {
		t.Fatal("TagUsage must return the same client for chaining")
	}
	if c.Usage != "rss_classify" || c.Source != ChatSourceNote || c.Provider != "openrouter" {
		t.Fatalf("tags = (%q,%q,%q)", c.Usage, c.Source, c.Provider)
	}
}

// TestCompleteEmitsCallStats: every outcome of a chat exchange reports
// exactly one CallStats — tokens on success, the error text on failure.
// Failure mode prevented: a broken path (HTTP error, transport error,
// bad body) skipping emission would undercount calls in the ledger.
func TestCompleteEmitsCallStats(t *testing.T) {
	// The failure rows below must not pay the production retry budget;
	// the sink and the config are process-global, so the subtests run
	// sequentially under the sink lock.
	fastRetries(t)
	cases := map[string]struct {
		status      int
		body        string
		url         string // "" = live test server
		wantOK      bool
		wantReply   string
		wantErrPart string
		wantStats   CallStats
	}{
		// Success with a usage block: token counts flow through 1:1.
		"ok_with_usage": {
			status: 200, body: chatReply("hello", `{"prompt_tokens":21,"completion_tokens":7,"total_tokens":28}`),
			wantOK: true, wantReply: "hello",
			wantStats: CallStats{OK: true, PromptTokens: 21, CompletionTokens: 7, TotalTokens: 28},
		},
		// Backends may omit the usage block: call counted, tokens zero.
		"ok_without_usage": {
			status: 200, body: chatReply("hi", ""),
			wantOK: true, wantReply: "hi",
			wantStats: CallStats{OK: true},
		},
		// HTTP failure: OK=false, error text carries the provider reply.
		"http_error": {
			status: 500, body: "quota exhausted",
			wantOK: false, wantErrPart: "500",
			wantStats: CallStats{OK: false, Error: "llm request to "},
		},
		// Undecodable success body: a decode failure must not emit OK=true.
		"garbage_body": {
			status: 200, body: "not json at all",
			wantOK: false, wantErrPart: "decode",
			wantStats: CallStats{OK: false, Error: "decode"},
		},
		// Valid JSON without choices: the model returned nothing usable.
		"no_choices": {
			status: 200, body: `{"choices":[]}`,
			wantOK: false, wantErrPart: "no choices",
			wantStats: CallStats{OK: false, Error: "llm returned no choices"},
		},
		// Unreachable endpoint (server closed before the call): a network
		// error still records the call attempt with OK=false.
		"unreachable": {
			url: "closed", wantOK: false, wantErrPart: "llm request",
			wantStats: CallStats{OK: false, Error: "llm request"},
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			var url string
			if tc.url == "closed" {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				srv.Close() // dead endpoint: connection refused
				url = srv.URL
			} else {
				url = newChatServer(t, tc.status, tc.body).URL
			}

			ch := make(chan CallStats, 1)
			unlock := lockSink(func(s CallStats) { ch <- s })
			defer unlock()

			client := NewChatClient(url, "test-model").
				TagUsage("rss_classify", ChatSourceNote, "openrouter")
			reply, err := client.Complete([]ChatMessage{{Role: "user", Content: "ping"}})

			if (err == nil) != tc.wantOK {
				t.Fatalf("Complete err = %v, wantOK = %v", err, tc.wantOK)
			}
			if tc.wantOK && reply != tc.wantReply {
				t.Fatalf("reply = %q, want %q", reply, tc.wantReply)
			}
			if !tc.wantOK && !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Fatalf("err = %q, want it to contain %q", err.Error(), tc.wantErrPart)
			}

			select {
			case got := <-ch:
				want := tc.wantStats
				want.Usage = "rss_classify"
				want.Source = ChatSourceNote
				want.Provider = "openrouter"
				want.Model = "test-model"
				want.BaseURL = url
				if !tc.wantOK {
					want.Error = got.Error // asserted by the err check above
				}
				if got.DurationMS < 0 {
					t.Fatalf("negative duration %d", got.DurationMS)
				}
				want.DurationMS = got.DurationMS
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("CallStats mismatch (-want +got):\n%s", diff)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no CallStats emitted")
			}
		})
	}
}

// TestEmitCallStatsWithoutSink: with no sink installed (tests, tool
// binaries without the server), emitting must be a silent no-op — the
// llm package is usable standalone. Failure mode prevented: stats
// plumbing turning ordinary LLM calls into panics when unwired.
func TestEmitCallStatsWithoutSink(t *testing.T) {
	unlock := lockSink(nil)
	defer unlock()
	emitCallStats(CallStats{OK: true}) // must not panic or block
	srv := newChatServer(t, 200, chatReply("ok", ""))
	if _, err := NewChatClient(srv.URL, "m").Complete([]ChatMessage{{Role: "user", Content: "x"}}); err != nil {
		t.Fatalf("Complete with no sink: %v", err)
	}
}

// TestEmitCallStatsRecoversPanic: a panicking sink must never take an
// LLM call down — telemetry is best-effort by contract. Regression here
// would turn a buggy recorder into broken inference for the whole app.
func TestEmitCallStatsRecoversPanic(t *testing.T) {
	unlock := lockSink(func(CallStats) { panic("boom") })
	defer unlock()
	// Direct emission with a panicking sink.
	emitCallStats(CallStats{OK: true}) // recovered
	// And through a real call: the reply still comes back.
	srv := newChatServer(t, 200, chatReply("still-ok", ""))
	reply, err := NewChatClient(srv.URL, "m").Complete([]ChatMessage{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("Complete despite panicking sink: %v", err)
	}
	if reply != "still-ok" {
		t.Fatalf("reply = %q", reply)
	}
}

// TestCompleteEmitsUnderConcurrency: concurrent completions against one
// backend must each be reported exactly once — the sink sees one record
// per call, none lost or duplicated (WaitGroup-synchronized, no sleeps).
// Failure mode prevented: a data race or lost emission under the chained
// transport mutexes would corrupt the token ledger.
func TestCompleteEmitsUnderConcurrency(t *testing.T) {
	fastRetries(t) // success-only: no retries, but do not pay the 1 s pacing
	const n = 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatReply("ok", `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`)))
	}))
	defer srv.Close()

	var mu sync.Mutex
	got := make(map[string]int) // by usage tag
	unlock := lockSink(func(s CallStats) {
		mu.Lock()
		got[s.Usage]++
		mu.Unlock()
	})
	defer unlock()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		usage := "usage-" + string(rune('a'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := NewChatClient(srv.URL, "m").TagUsage(usage, ChatSourceConfig, "")
			if _, err := client.Complete([]ChatMessage{{Role: "user", Content: "x"}}); err != nil {
				t.Errorf("complete: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(got) != n {
		t.Fatalf("emitted %d distinct usages, want %d (lost record?)", len(got), n)
	}
	for u, c := range got {
		if c != 1 {
			t.Fatalf("usage %q emitted %d times, want 1", u, c)
		}
	}
}

// TestCallStatsPBT: property-based — for arbitrary backend-reported token
// triplets the emitted CallStats must echo them exactly, with OK=true and
// a non-negative duration. Invariant: token accounting is a faithful
// pass-through of the backend's usage block (no drift, no clamping).
func TestCallStatsPBT(t *testing.T) {
	fastRetries(t) // success-only: skip the per-request pacing
	// The global sink lock is held for the whole property run — running
	// this in parallel would just serialize all sink tests behind it.
	rapid.Check(t, func(t *rapid.T) {
		prompt := rapid.IntRange(0, 1_000_000).Draw(t, "prompt")
		completion := rapid.IntRange(0, 1_000_000).Draw(t, "completion")
		// Safe printable string: naive JSON quoting in chatReply
		// cannot express control characters, and none are needed —
		// the property is pass-through fidelity of the reply.
		reply := fmt.Sprintf("reply #%d (ok: %t)", rapid.IntRange(0, 1_000_000).Draw(t, "seed"),
			rapid.Bool().Draw(t, "flag"))
		usageBlock := `{"prompt_tokens":` + itoa(prompt) +
			`,"completion_tokens":` + itoa(completion) +
			`,"total_tokens":` + itoa(prompt+completion) + `}`

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(chatReply(reply, usageBlock)))
		}))
		defer srv.Close()

		ch := make(chan CallStats, 1)
		unlock := lockSink(func(s CallStats) { ch <- s })
		defer unlock()

		client := NewChatClient(srv.URL, "m").TagUsage("pbt", ChatSourceNote, "openrouter")
		gotReply, err := client.Complete([]ChatMessage{{Role: "user", Content: "x"}})
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		select {
		case s := <-ch:
			if !s.OK || s.Error != "" {
				t.Fatalf("stats marked failure: %+v", s)
			}
			if s.PromptTokens != prompt || s.CompletionTokens != completion || s.TotalTokens != prompt+completion {
				t.Fatalf("tokens drifted: got (%d,%d,%d), want (%d,%d,%d)",
					s.PromptTokens, s.CompletionTokens, s.TotalTokens, prompt, completion, prompt+completion)
			}
			if s.DurationMS < 0 {
				t.Fatal("negative duration")
			}
			if gotReply != reply {
				t.Fatalf("reply = %q, want %q", gotReply, reply)
			}
		default:
			t.Fatal("no stats emitted")
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
