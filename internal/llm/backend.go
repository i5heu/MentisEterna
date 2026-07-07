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
	mu     sync.Mutex
	counts map[string]int
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
	}
	r.counts[key]++
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
				r.mu.Unlock()
			default:
				r.mu.Unlock()
				return
			}

			if err := shutdownBackend(baseURL, model, httpClient); err != nil {
				log.Printf("llm: shutdown backend for model %q: %v", model, err)
			}
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

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/backend/shutdown"
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shutdown returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
