package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/i5heu/MentisEterna/internal/config"
)

// enableRetry sets the [llm] retry knobs for a test and restores defaults on
// cleanup. All tests here construct clients through the REAL production
// factories (NewEmbeddingClient / NewChatClient / NewOCRClient / NewSTTClient),
// so they exercise the exact path production traffic takes.
func enableRetry(t *testing.T, attempts, delayMS int) {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = attempts
	config.Get().LLM.RetryDelayMS = delayMS
}

// embeddingServer returns a server that fails with 500 the first failCount
// requests and then answers valid embedding JSON.
func embeddingServer(t *testing.T, failCount int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= failCount {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// newEmbeddingClientFor points NewEmbeddingClient (which reads the base URL
// from config) at the given test server.
func newEmbeddingClientFor(t *testing.T, srv *httptest.Server) *EmbeddingClient {
	t.Helper()
	config.Get().LLM.BaseURL = srv.URL
	return NewEmbeddingClient()
}

func TestNewEmbeddingClientRetriesOn5xx(t *testing.T) {
	enableRetry(t, 2, 20)
	srv, hits := embeddingServer(t, 2)

	vec, err := newEmbeddingClientFor(t, srv).GenerateEmbedding("hello")
	if err != nil {
		t.Fatalf("GenerateEmbedding: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[2] != 0.3 {
		t.Fatalf("unexpected embedding: %v", vec)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
}

func TestNewEmbeddingClientRetryExhausted(t *testing.T) {
	enableRetry(t, 1, 20)
	srv, hits := embeddingServer(t, 100) // always 500

	_, err := newEmbeddingClientFor(t, srv).GenerateEmbedding("hello")
	if err == nil {
		t.Fatal("expected error after retries are exhausted")
	}
	if !strings.Contains(err.Error(), "after 2 attempt") {
		t.Fatalf("error should report the attempt count, got: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected exactly 2 attempts (1 + 1 retry), got %d", got)
	}
}

func TestNewEmbeddingClientDoesNotRetry4xx(t *testing.T) {
	enableRetry(t, 2, 20)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	_, err := newEmbeddingClientFor(t, srv).GenerateEmbedding("hello")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected 1 attempt (4xx is never retried), got %d", got)
	}
}

func TestNewEmbeddingClientNoRetryByDefault(t *testing.T) {
	// Defaults leave RetryAttempts at 0: one attempt, immediate failure.
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.BaseURL = "http://127.0.0.1:0" // connection refused

	_, err := NewEmbeddingClient().GenerateEmbedding("hello")
	if err == nil {
		t.Fatal("expected error against a dead backend")
	}
}

func TestNewChatClientRetriesOn5xx(t *testing.T) {
	enableRetry(t, 2, 20)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"my-title"}}]}`))
	}))
	t.Cleanup(srv.Close)

	title, err := NewChatClient(srv.URL, "chat-model").GenerateTitle("some note body")
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if title != "my-title" {
		t.Fatalf("title = %q, want %q", title, "my-title")
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestNewChatClientRetriesSubtaskGeneration(t *testing.T) {
	enableRetry(t, 2, 20)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"subtasks\":[{\"label\":\"step one\"},{\"label\":\"step two\"}]}"}}]}`))
	}))
	t.Cleanup(srv.Close)

	out, err := NewChatClient(srv.URL, "chat-model").GenerateSubTasks(SubTaskGenerationInput{Title: "t"})
	if err != nil {
		t.Fatalf("GenerateSubTasks: %v", err)
	}
	if len(out.Subtasks) != 2 || out.Subtasks[0].Label != "step one" {
		t.Fatalf("unexpected subtasks: %+v", out.Subtasks)
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestNewOCRClientRetriesOn5xx(t *testing.T) {
	enableRetry(t, 2, 20)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ocr text"}}]}`))
	}))
	t.Cleanup(srv.Close)

	text, err := NewOCRClient(srv.URL, "ocr-model").RunOCR([]byte{0x89, 0x50, 0x4E, 0x47})
	if err != nil {
		t.Fatalf("RunOCR: %v", err)
	}
	if text != "ocr text" {
		t.Fatalf("ocr text = %q, want %q", text, "ocr text")
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestNewSTTClientRetriesOn5xx(t *testing.T) {
	enableRetry(t, 2, 20)

	var mu sync.Mutex
	var bodies [][]byte
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read STT body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcribed"}`))
	}))
	t.Cleanup(srv.Close)

	text, err := NewSTTClient(srv.URL, "stt-model").RunSTT([]byte("fake-audio-bytes"), "clip.wav")
	if err != nil {
		t.Fatalf("RunSTT: %v", err)
	}
	if text != "transcribed" {
		t.Fatalf("stt text = %q, want %q", text, "transcribed")
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}

	// The multipart body must be rewound identically across retries.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("expected 3 bodies, got %d", len(bodies))
	}
	for i := 1; i < len(bodies); i++ {
		if string(bodies[i]) != string(bodies[0]) {
			t.Fatalf("STT body changed between attempt 0 and %d", i)
		}
	}
}
