package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestBeginBackendUseShutsDownBackendOnFinalRelease(t *testing.T) {
	t.Helper()

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
