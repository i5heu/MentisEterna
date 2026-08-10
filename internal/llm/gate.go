package llm

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

// gate serializes requests to one backend host: an optional concurrency cap
// (semaphore) and an optional minimum gap between the end of one request and
// the start of the next. One gate exists per host (scheme://host, port
// included), created on first use.
type gate struct {
	mu         sync.Mutex
	slots      chan struct{} // nil until first acquire with MaxConcurrency > 0
	lastFinish time.Time     // end time of the most recent request, for cooldown
}

var (
	gatesMu sync.Mutex
	gates   = map[string]*gate{}
)

func gateFor(host string) *gate {
	gatesMu.Lock()
	defer gatesMu.Unlock()
	g, ok := gates[host]
	if !ok {
		g = &gate{}
		gates[host] = g
	}
	return g
}

// throttledTransport wraps a base RoundTripper with the configurable
// concurrency cap, cooldown gap, and retry logic from [llm] config. When the
// knobs are left at their zero values it is a pass-through (plus the body
// rewind safety net).
type throttledTransport struct {
	base http.RoundTripper
}

func (t *throttledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := config.Get().LLM

	// Safety net: ensure the body can be rewound for retries. The four
	// clients construct requests via http.NewRequest with *bytes.Reader or
	// *bytes.Buffer bodies, which already sets GetBody; this only covers
	// callers that did not.
	if req.Body != nil && req.GetBody == nil {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	g := gateFor(req.URL.Scheme + "://" + req.URL.Host)

	// Acquire: concurrency slot first, then cooldown wait (reads lastFinish
	// under the mutex but sleeps without holding it).
	if cfg.MaxConcurrency > 0 {
		g.mu.Lock()
		if g.slots == nil {
			g.slots = make(chan struct{}, cfg.MaxConcurrency)
		}
		g.mu.Unlock()
		g.slots <- struct{}{}
	}
	if cfg.RequestCooldownMS > 0 {
		g.mu.Lock()
		last := g.lastFinish
		g.mu.Unlock()
		if wait := time.Until(last.Add(time.Duration(cfg.RequestCooldownMS) * time.Millisecond)); wait > 0 {
			time.Sleep(wait)
		}
	}
	defer func() {
		g.mu.Lock()
		g.lastFinish = time.Now()
		g.mu.Unlock()
		if cfg.MaxConcurrency > 0 {
			<-g.slots
		}
	}()

	// Attempt loop: retry connection errors and HTTP 429/5xx only.
	// Deterministic 4xx responses are returned as-is so the client turns them
	// into its usual error; they are never retried.
	attempts := 0
	var lastErr error
	for attempt := 0; attempt <= cfg.RetryAttempts; attempt++ {
		attempts++
		if attempt > 0 {
			if cfg.RetryDelayMS > 0 {
				time.Sleep(time.Duration(cfg.RetryDelayMS) * time.Millisecond)
			}
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					lastErr = fmt.Errorf("rewind request body: %w", err)
					break
				}
				req.Body = body
			}
		}

		resp, err := t.base.RoundTrip(req)
		if err != nil {
			lastErr = err
			continue
		}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError:
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("localai returned HTTP %d", resp.StatusCode)
			continue
		default:
			return resp, nil
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request failed")
	}
	log.Printf("llm: request to %s failed after %d attempt(s): %v", req.URL, attempts, lastErr)
	return nil, fmt.Errorf("llm request to %s failed after %d attempt(s): %w", req.URL, attempts, lastErr)
}
