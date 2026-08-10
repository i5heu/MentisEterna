package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestBeginBackendUseShutsDownBackendOnFinalRelease(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 0

	var mu sync.Mutex
	calls := 0
	gotMethod := ""
	gotPath := ""
	gotContentType := ""
	gotBody := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		_ = r.Body.Close()

		mu.Lock()
		calls++
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody = string(body)
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "Qwen3-Embedding-4B-GGUF",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 shutdown call, got %d", calls)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/backend/shutdown" {
		t.Fatalf("expected /backend/shutdown, got %s", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected application/json content type, got %q", gotContentType)
	}
	if gotBody != `{"model":"Qwen3-Embedding-4B-GGUF"}` {
		t.Fatalf("unexpected shutdown payload: %s", gotBody)
	}
}

func TestBeginBackendUseReferenceCountsByModel(t *testing.T) {
	t.Helper()
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

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "refcount-test-model",
		http:    srv.Client(),
	}

	releaseA := BeginBackendUse(client)
	releaseB := BeginBackendUse(client)

	releaseA()
	mu.Lock()
	if calls != 0 {
		mu.Unlock()
		t.Fatalf("expected no shutdown before final release, got %d", calls)
	}
	mu.Unlock()

	// Releasing the same lease twice must remain a no-op.
	releaseA()
	mu.Lock()
	if calls != 0 {
		mu.Unlock()
		t.Fatalf("expected duplicate release to stay a no-op, got %d shutdown calls", calls)
	}
	mu.Unlock()

	releaseB()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected shutdown on final release, got %d", calls)
	}
}

func TestBeginBackendUseDelaysShutdown(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 200

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "delay-test-model",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release()

	mu.Lock()
	immediate := calls
	mu.Unlock()
	if immediate != 0 {
		t.Fatalf("expected 0 shutdown calls immediately after release, got %d", immediate)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		c := calls
		mu.Unlock()
		if c >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 1 shutdown call within the deadline, got %d", c)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// time.AfterFunc fires exactly once; give it a moment to settle.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 shutdown call, got %d", calls)
	}
}

func TestBeginBackendUseSameModelRequestCancelsPendingStop(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 5000

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "cancel-test-model",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release() // schedules a stop at +5s

	// A new request for the SAME model within the window cancels the pending
	// stop. Hold its lease open while we wait out the original window.
	release2 := BeginBackendUse(client)

	time.Sleep(5200 * time.Millisecond) // > stop_delay_ms

	mu.Lock()
	if calls != 0 {
		mu.Unlock()
		t.Fatalf("expected 0 shutdown calls (same-model begin cancelled the pending stop), got %d", calls)
	}
	mu.Unlock()

	// Once that request finishes, a fresh idle window starts: the stop fires
	// exactly once after stop_delay_ms.
	release2()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		c := calls
		mu.Unlock()
		if c >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the fresh idle window to fire the stop, got %d calls", c)
		}
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(50 * time.Millisecond) // time.AfterFunc fires exactly once
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected exactly 1 shutdown call (cancelled stop must not fire), got %d", calls)
	}
}

func TestBeginBackendUseStopDisabled(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopBackendOnIdle = false
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

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "stop-disabled-model",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release()

	// Give a wrongly-scheduled stop a chance to fire.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("expected 0 shutdown calls with stop disabled, got %d", calls)
	}
}

func TestShutdownBackendUsesConfiguredEndpoint(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 0
	config.Get().LLM.BackendStopEndpoint = "/custom/stop"

	var mu sync.Mutex
	calls := 0
	gotPath := ""
	gotMethod := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		gotPath = r.URL.Path
		gotMethod = r.Method
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "endpoint-test-model",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 shutdown call, got %d", calls)
	}
	if gotPath != "/custom/stop" {
		t.Fatalf("expected POST /custom/stop, got %s %s", gotMethod, gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
}

func TestShutdownBackend404TreatedAsSuccess(t *testing.T) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.StopDelayMS = 0

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := &EmbeddingClient{
		BaseURL: srv.URL,
		Model:   "already-unloaded-model",
		http:    srv.Client(),
	}

	release := BeginBackendUse(client)
	release()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected the shutdown request to be sent, got %d calls", calls)
	}
	// A 404 from LocalAI (model already unloaded) must not be logged as an
	// error — reach here without a fatal is the assertion the test makes at
	// the log level; verify the release path simply completed.
}
