package llminferencer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// TestResult is the outcome of the test action (one tiny chat completion).
type TestResult struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Reply     string `json:"reply,omitempty"`
}

// ModelEntry is one provider model from the list_models action.
type ModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// HandleAction implements notetype.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "test":
		return p.actionTest(ctx, db, noteID, params)
	case "list_models":
		return p.actionListModels(ctx, db, noteID)
	case "clear_api_key":
		return p.actionClearAPIKey(ctx, db, noteID)
	case "clear_stats":
		return p.actionClearStats(ctx, db)
	default:
		return nil, notetype.ErrUnknownAction
	}
}

// actionTest sends one minimal chat completion through the exact production
// client the resolver would hand out (same transport, auth, and retries).
func (p *Plugin) actionTest(ctx context.Context, db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	if db == nil {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: test requires a database"}
	}
	var pr struct {
		Tier  string `json:"tier"`
		Model string `json:"model"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &pr); err != nil {
			return nil, &notetype.BadRequestError{Message: fmt.Sprintf("llm_inferencer: invalid params: %v", err)}
		}
	}
	pr.Tier = strings.TrimSpace(pr.Tier)
	pr.Model = strings.TrimSpace(pr.Model)
	if pr.Tier != "" && !validTier(pr.Tier) {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("llm_inferencer: unknown tier %q (big/middle/small/tiny)", pr.Tier)}
	}
	if pr.Tier != "" && pr.Model != "" {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: pass tier or model, not both"}
	}

	row, ok, err := loadInferencerRow(db, noteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: save the note's config first"}
	}

	// Resolve what to test: explicit model > explicit tier > default tier.
	tier := pr.Tier
	model := pr.Model
	if tier == "" && model == "" {
		if validTier(row.DefaultTier) {
			tier = row.DefaultTier
		} else {
			return nil, &notetype.BadRequestError{Message: "llm_inferencer: provide tier or model (this note has no default tier)"}
		}
	}
	if tier != "" {
		tb, has := row.Tiers[tier]
		if !has || strings.TrimSpace(tb.Model) == "" {
			return nil, &notetype.BadRequestError{Message: fmt.Sprintf("llm_inferencer: tier %q has no model configured", tier)}
		}
		model = strings.TrimSpace(tb.Model)
	}
	if model == "" {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: no model to test"}
	}

	baseURL, err := row.effectiveBaseURL(tier)
	if err != nil {
		return nil, &notetype.BadRequestError{Message: err.Error()}
	}

	// The production client: same transport, auth header, and retry policy
	// ResolveChat hands to consumers. Tagged as a connection test so its
	// (real, billable) provider calls show up in the ledger's statistics.
	client := llm.NewChatClientWithKey(baseURL, model, row.Key).
		TagUsage(statsUsageTest, SourceNote, row.Provider)
	start := time.Now()
	reply, err := client.Complete([]llm.ChatMessage{{Role: "user", Content: "Reply with the single word: OK"}})
	res := TestResult{
		OK:        err == nil,
		Provider:  row.Provider,
		Model:     model,
		BaseURL:   baseURL,
		LatencyMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		res.Error = trimForJSON(err.Error(), 500)
	} else {
		res.Reply = trimForJSON(reply, 500)
	}
	return res, nil
}

// actionListModels fetches the provider's model catalog (for the pickers).
// Both OpenRouter and Mistral expose OpenAI-compatible GET /v1/models.
func (p *Plugin) actionListModels(ctx context.Context, db *sql.DB, noteID int64) (any, error) {
	if db == nil {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: list_models requires a database"}
	}
	row, ok, err := loadInferencerRow(db, noteID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: save the note's config first"}
	}
	baseURL, err := row.effectiveBaseURL("")
	if err != nil {
		return nil, &notetype.BadRequestError{Message: err.Error()}
	}

	models, total, err := fetchProviderModels(baseURL, row.Key)
	if err != nil {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("llm_inferencer: list models: %v", err)}
	}
	return map[string]any{
		"provider":  row.Provider,
		"base_url":  baseURL,
		"models":    models,
		"total":     total,
		"truncated": total > len(models),
	}, nil
}

// actionClearAPIKey wipes the stored API key (the write-only complement).
func (p *Plugin) actionClearAPIKey(ctx context.Context, db *sql.DB, noteID int64) (any, error) {
	if db == nil {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: clear_api_key requires a database"}
	}
	if _, err := db.Exec(`UPDATE ct_llm_inferencer_config SET api_key = '' WHERE note_id = ?`, noteID); err != nil {
		return nil, fmt.Errorf("llm_inferencer: clear api key: %w", err)
	}
	return map[string]any{"cleared": true, "api_key_set": false}, nil
}

// actionClearStats wipes the LLM call ledger (the clear_stats action).
func (p *Plugin) actionClearStats(ctx context.Context, db *sql.DB) (any, error) {
	if db == nil {
		return nil, &notetype.BadRequestError{Message: "llm_inferencer: clear_stats requires a database"}
	}
	if err := clearStats(db); err != nil {
		return nil, err
	}
	return map[string]any{"cleared": true}, nil
}

// --- provider HTTP helpers ---

// fetchProviderModels GETs the OpenAI-compatible /v1/models catalog
// (OpenRouter: https://openrouter.ai/api/v1/models, Mistral:
// https://api.mistral.ai/v1/models). The result is capped at 500 entries;
// total reports the uncapped count.
func fetchProviderModels(baseURL, key string) ([]ModelEntry, int, error) {
	url := llm.OpenAIEndpoint(baseURL, "models")
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The URL is part of the error: endpoint 404s are base-URL
		// mismatches, and the called URL is the first thing to check.
		return nil, 0, fmt.Errorf("%s: %d: %s", url, resp.StatusCode, trimForJSON(string(body), 300))
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, 0, fmt.Errorf("decode response: %w", err)
	}
	entries := make([]ModelEntry, 0, len(out.Data))
	for _, m := range out.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		entries = append(entries, ModelEntry{ID: m.ID, Name: m.Name})
	}
	total := len(entries)
	const cap = 500
	if len(entries) > cap {
		entries = entries[:cap]
	}
	return entries, total, nil
}

// trimForJSON bounds a string for action/API responses.
func trimForJSON(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return string([]rune(s)[:max]) + "…"
	}
	return s
}

var _ notetype.ActionHandler = (*Plugin)(nil)
