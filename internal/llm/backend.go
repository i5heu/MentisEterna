package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/i5heu/MentisEterna/internal/config"
)

type backendLeaseClient interface {
	BeginBackendUse() func()
}

// BeginBackendUse registers a higher-level use of a model backend and returns a
// release function. When the last in-flight use for a model finishes, the
// corresponding LocalAI backend is asked to shut down.
func BeginBackendUse(client any) func() {
	if managed, ok := client.(backendLeaseClient); ok {
		return managed.BeginBackendUse()
	}
	return func() {}
}

type backendUseRegistry struct {
	mu      sync.Mutex
	counts  map[string]int
	pending map[string]*time.Timer // scheduled idle-stops, keyed like counts
}

var sharedBackendUseRegistry backendUseRegistry

func (c *EmbeddingClient) BeginBackendUse() func() {
	return sharedBackendUseRegistry.begin(c.BaseURL, c.Model, c.http)
}

func (c *ChatClient) BeginBackendUse() func() {
	return sharedBackendUseRegistry.begin(c.BaseURL, c.Model, c.http)
}

func (c *OCRClient) BeginBackendUse() func() {
	return sharedBackendUseRegistry.begin(c.BaseURL, c.Model, c.http)
}

func (c *STTClient) BeginBackendUse() func() {
	// An API-keyed STT client targets an external hosted provider: the
	// idle-stop machinery is a LocalAI-only behavior (POST /backend/shutdown)
	// and must not fire against a third-party endpoint.
	if c.APIKey != "" {
		return func() {}
	}
	// Local backend: the lease spans the WHOLE retry cycle (RunSTT is a single
	// http.Do whose gate transport retries internally), so the model is never
	// stopped between retry attempts. The idle-stop fires only after the final
	// attempt releases the lease.
	return sharedBackendUseRegistry.begin(c.BaseURL, c.Model, c.http)
}

func (r *backendUseRegistry) begin(baseURL, model string, httpClient *http.Client) func() {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model = strings.TrimSpace(model)
	if baseURL == "" || model == "" {
		return func() {}
	}

	key := baseURL + "\x00" + model

	r.mu.Lock()
	if r.counts == nil {
		r.counts = make(map[string]int)
		r.pending = make(map[string]*time.Timer)
	}
	r.counts[key]++
	// A new request for this model cancels any scheduled idle-stop so the
	// backend stays loaded (avoiding an expensive model reload).
	if t := r.pending[key]; t != nil {
		t.Stop()
		delete(r.pending, key)
	}
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			count := r.counts[key]
			switch {
			case count > 1:
				r.counts[key] = count - 1
				r.mu.Unlock()
				return
			case count == 1:
				delete(r.counts, key)
			default:
				r.mu.Unlock()
				return
			}

			stop := func() {
				if err := shutdownBackend(baseURL, model, httpClient); err != nil {
					log.Printf("llm: shutdown backend for model %q: %v", model, err)
					return
				}
				log.Printf("llm: stopped backend model %q (idle)", model)
			}

			if !config.Get().LLM.StopBackendOnIdle {
				r.mu.Unlock()
				return
			}
			delay := config.Get().LLM.StopDelayMS
			if delay <= 0 {
				r.mu.Unlock()
				stop()
				return
			}
			r.pending[key] = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
				// Re-check under the lock: a begin() may have cancelled this
				// stop in the meantime.
				r.mu.Lock()
				if r.pending[key] == nil {
					r.mu.Unlock()
					return
				}
				delete(r.pending, key)
				r.mu.Unlock()
				stop()
			})
			r.mu.Unlock()
		})
	}
}

type backendShutdownRequest struct {
	Model string `json:"model"`
}

func shutdownBackend(baseURL, model string, httpClient *http.Client) error {
	payload, err := json.Marshal(backendShutdownRequest{Model: model})
	if err != nil {
		return fmt.Errorf("marshal shutdown request: %w", err)
	}

	endpoint := strings.TrimRight(strings.TrimSpace(config.Get().LLM.BackendStopEndpoint), "/")
	if endpoint == "" {
		endpoint = "/backend/shutdown"
	}
	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + endpoint
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create shutdown request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := httpClient
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send shutdown request: %w", err)
	}
	defer resp.Body.Close()

	// LocalAI returns 404 for an already-unloaded model (e.g. swapped out by a
	// different-model request); that is not an error.
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shutdown returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
