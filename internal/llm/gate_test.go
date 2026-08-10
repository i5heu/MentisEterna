package llm

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestThrottledTransportMaxConcurrency(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.MaxConcurrency = 1

	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newLLMHTTPClient()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Errorf("GET failed: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent requests = %d, want 1", got)
	}
}

func TestThrottledTransportCooldown(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RequestCooldownMS = 200

	var mu sync.Mutex
	var starts []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newLLMHTTPClient()
	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(starts))
	}
	if gap := starts[1].Sub(starts[0]); gap < 200*time.Millisecond {
		t.Fatalf("gap between request starts = %v, want >= 200ms", gap)
	}
}

func TestThrottledTransportRetriesOn5xx(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = 2
	config.Get().LLM.RetryDelayMS = 50

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newLLMHTTPClient()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if got := hits.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected final 200, got %d", resp.StatusCode)
	}
}

func TestThrottledTransportDoesNotRetry4xx(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = 2

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	client := newLLMHTTPClient()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if got := hits.Load(); got != 1 {
		t.Fatalf("expected 1 attempt (4xx is never retried), got %d", got)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected the 400 response to surface, got %d", resp.StatusCode)
	}
}

func TestThrottledTransportRetriesOnTransportError(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = 2
	config.Get().LLM.RetryDelayMS = 50

	// Listener that accepts and immediately closes every connection, so every
	// RoundTrip fails with a connection error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var accepts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()

	client := newLLMHTTPClient()
	_, err = client.Get("http://" + ln.Addr().String())
	if err == nil {
		t.Fatal("expected a transport error after retries are exhausted")
	}
	if got := accepts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", got)
	}
}

func TestThrottledTransportRewindsBodyForRetry(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().LLM.RetryAttempts = 2
	config.Get().LLM.RetryDelayMS = 50

	const payload = "identical-body-on-every-attempt"
	var hits atomic.Int32
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newLLMHTTPClient()
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(bodies))
	}
	if string(bodies[0]) != payload || string(bodies[1]) != payload {
		t.Fatalf("body changed across retries: %q vs %q", bodies[0], bodies[1])
	}
}
