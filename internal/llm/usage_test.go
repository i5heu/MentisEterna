package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestChatClientAPIKeySentAsBearer(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer srv.Close()

	c := NewChatClientWithKey(srv.URL, "test-model", "sk-secret-123")
	out, err := c.Complete([]ChatMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if out != "OK" {
		t.Fatalf("reply = %q, want OK", out)
	}
	if gotAuth != "Bearer sk-secret-123" {
		t.Errorf("Authorization = %q, want Bearer sk-secret-123", gotAuth)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", gotBody["model"])
	}
}

func TestChatClientWithoutKeySendsNoAuthHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hadAuth = r.Header.Get("Authorization") != ""
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer srv.Close()

	c := NewChatClient(srv.URL, "m")
	if _, err := c.Complete([]ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header sent for a keyless client")
	}
}

func TestBeginBackendUseSkipsIdleStopForKeyedClient(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Any request outside /v1/chat/completions means the idle-stop
		// machinery fired against an external endpoint.
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected idle-stop request to external endpoint: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewChatClientWithKey(srv.URL, "remote-model", "sk-key")
	release := BeginBackendUse(c)
	release()

	// Give a zero-delay idle-stop (StopDelayMS = 0) time to misfire.
	time.Sleep(50 * time.Millisecond)
}

func TestUsageCatalog(t *testing.T) {
	specs := UsageSpecs()
	if len(specs) < 4 {
		t.Fatalf("catalog has %d entries, want at least 4", len(specs))
	}
	ids := map[string]bool{}
	for _, s := range specs {
		if ids[s.ID] {
			t.Errorf("duplicate usage id %q", s.ID)
		}
		ids[s.ID] = true
		if s.Label == "" || s.Description == "" {
			t.Errorf("usage %q missing label/description", s.ID)
		}
	}
	if !ids["rss_classify"] || !ids["title"] || !ids["tags"] || !ids["subtasks"] {
		t.Error("catalog missing a core usage")
	}
	if !UsageSpecByID("rss_classify").Wired {
		t.Error("rss_classify must be wired")
	}
	if spec := UsageSpecByID("title"); spec == nil || spec.Wired {
		t.Error("title must exist and be unwired for now")
	}
	if KnownUsage("nope") {
		t.Error("KnownUsage must reject unknown ids")
	}

	// The catalog must be a copy: mutations must not leak.
	mutated := UsageSpecs()
	mutated[0].Label = "clobbered"
	if UsageSpecs()[0].Label == "clobbered" {
		t.Error("UsageSpecs returned a mutable reference to the catalog")
	}
}

func TestNewFeatureChatClient(t *testing.T) {
	cases := []struct {
		usage string
		want  func(*ChatClient) bool
	}{
		{"title", func(c *ChatClient) bool { return c.Model == FeatureModel("title") }},
		{"tags", func(c *ChatClient) bool { return c.Model == FeatureModel("tags") }},
		{"subtasks", func(c *ChatClient) bool { return c.Model == FeatureModel("subtasks") }},
		{"rss_classify", func(c *ChatClient) bool { return c.Model == FeatureModel("rss_classify") }},
	}
	for _, tc := range cases {
		c := NewFeatureChatClient(tc.usage)
		if c == nil {
			t.Errorf("usage %q: nil client", tc.usage)
			continue
		}
		if !tc.want(c) {
			t.Errorf("usage %q: client model = %q, does not match feature tier system", tc.usage, c.Model)
		}
	}
	if NewFeatureChatClient("ocr") != nil {
		t.Error("ocr is not a chat usage; factory must return nil")
	}
	if NewFeatureChatClient("does-not-exist") != nil {
		t.Error("unknown usage must return nil")
	}
}
