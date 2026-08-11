package llm

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestSTTClientSendsBearerTokenWhenKeyed(t *testing.T) {
	var mu sync.Mutex
	gotAuth := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcribed"}`))
	}))
	defer srv.Close()

	c := NewSTTClient(srv.URL, "whisper-1", "sk-secret")
	if c.APIKey != "sk-secret" {
		t.Fatalf("APIKey = %q, want sk-secret", c.APIKey)
	}
	if _, err := c.RunSTT([]byte("fake-audio-bytes"), "clip.wav"); err != nil {
		t.Fatalf("RunSTT: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-secret" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-secret")
	}
}

func TestSTTClientOmitsAuthHeaderWhenUnkeyed(t *testing.T) {
	var mu sync.Mutex
	gotAuth := "unset"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcribed"}`))
	}))
	defer srv.Close()

	c := NewSTTClient(srv.URL, "vibevoice-cpp-asr", "")
	if _, err := c.RunSTT([]byte("fake-audio-bytes"), "clip.wav"); err != nil {
		t.Fatalf("RunSTT: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty for unkeyed client", gotAuth)
	}
}

func TestNewTieredSTTClientReadsAPIKeyEnv(t *testing.T) {
	t.Setenv("STT_API_KEY", "sk-tiered")
	c := NewTieredSTTClient()
	if c.APIKey != "sk-tiered" {
		t.Fatalf("APIKey = %q, want %q from STT_API_KEY", c.APIKey, "sk-tiered")
	}
}

func TestSTTClientIdleStopsAfterRelease(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 0 // stop fires immediately on release

	var mu sync.Mutex
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Local unkeyed client: the model must stay loaded BETWEEN retry attempts
	// (the lease is held for the whole retry cycle) but the idle-stop fires
	// once all retries are finished and the lease is released.
	c := NewSTTClient(srv.URL, "vibevoice-cpp-asr", "")
	release := BeginBackendUse(c)
	release()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 shutdown call after release, got %d", calls)
	}
}

func TestKeyedSTTClientSkipsIdleShutdown(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 0

	var mu sync.Mutex
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewSTTClient(srv.URL, "whisper-1", "sk-secret")
	release := BeginBackendUse(c)
	release()

	// Give a wrongly-scheduled stop a chance to fire.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("expected 0 shutdown calls for keyed (external) STT client, got %d", calls)
	}
}
