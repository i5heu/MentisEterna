// Package llminferencer implements the "llm_inferencer" note type: an LLM
// inference provider (OpenRouter, Mistral La Plateforme, or any OpenAI-
// compatible endpoint) configured entirely in a note — the "remote LLM"
// paradigm for MentisEterna.
//
// One llm_inferencer note defines:
//
//   - a provider and its endpoint (base URL; pre-filled defaults for
//     OpenRouter and Mistral),
//   - an API key (write-only: stored in the plugin table, never returned to
//     the client on view/load — an empty api_key on save keeps the stored
//     key, and the clear_api_key action removes it),
//   - models for the four capability tiers: big / middle / small / tiny,
//   - a usage map binding app LLM usages (see internal/llm usage catalog,
//     e.g. rss_classify) to a tier, directly to a model, or explicitly to
//     the LOCAL LLM (the config.toml feature/tier system),
//   - an optional default tier used for usages without an explicit binding.
//
// The note type is a SINGLETON: at most one llm_inferencer note may exist in
// the whole app (enforced on create and on type changes by the server). That
// one note applies globally — usages resolve through it from anywhere in
// the note tree and fall back to the config.toml feature/tier system for
// usages it does not bind (or pins to local).
//
// See docs/LLMInferencer.md for the full design.
package llminferencer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/i5heu/MentisEterna/internal/jobs"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// PluginID is the inferencer note type ("LLM Inferencer").
const PluginID = "llm_inferencer"

// Tier names in capability order (biggest → smallest).
const (
	TierBig    = "big"
	TierMiddle = "middle"
	TierSmall  = "small"
	TierTiny   = "tiny"
)

// Providers.
const (
	ProviderOpenRouter = "openrouter"
	ProviderMistral    = "mistral"
	ProviderCompatible = "compatible"
)

// providerDefaultBaseURL returns the built-in base URL for a provider.
// These are API ROOT URLs: llm.OpenAIEndpoint appends /v1/chat/completions
// (and /v1/models for list_models). User-entered bases may carry a trailing
// "/v1" (OpenAI-SDK-style, e.g. https://openrouter.ai/api/v1) or even be the
// full endpoint — both are normalized, so no path is doubled.
// OpenRouter → https://openrouter.ai/api, Mistral → https://api.mistral.ai.
var providerDefaultBaseURL = map[string]string{
	ProviderOpenRouter: "https://openrouter.ai/api",
	ProviderMistral:    "https://api.mistral.ai",
}

func init() {
	notetype.Register(&Plugin{})
}

// Plugin is the "llm_inferencer" note type.
type Plugin struct {
	// Stats sink state (notetype.CallStatsRecorder): the db the server
	// injects at startup; every chat call in the app reports through
	// RecordCall into ct_llm_inferencer_stats (see stats.go).
	statsMu sync.RWMutex
	statsDB *sql.DB
}

// ID implements notetype.Plugin.
func (p *Plugin) ID() string { return PluginID }

// InitSchema creates the plugin tables. The config row (including the API
// key) and tier models cascade away with their note.
//
// The stats table is the ONE deliberate exception to the note_id-FK rule:
// it is the app-wide LLM call ledger — rows are recorded even when no
// llm_inferencer note exists and for usages that never route through this
// note (local/config sources, title/tags/subtasks on the config.toml
// system), so it must not cascade away with the singleton note. The llm
// note SURFACES the ledger; it does not own its lifetime.
func (p *Plugin) InitSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_llm_inferencer_config (
			note_id      INTEGER PRIMARY KEY,
			provider     TEXT    NOT NULL DEFAULT '',
			base_url     TEXT    NOT NULL DEFAULT '',
			api_key      TEXT    NOT NULL DEFAULT '',
			default_tier TEXT    NOT NULL DEFAULT '',
			usages_json  TEXT    NOT NULL DEFAULT '{}',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS ct_llm_inferencer_tiers (
			note_id  INTEGER NOT NULL,
			tier     TEXT    NOT NULL,
			model    TEXT    NOT NULL DEFAULT '',
			base_url TEXT    NOT NULL DEFAULT '',
			PRIMARY KEY (note_id, tier),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS ct_llm_inferencer_stats (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at        TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			usage             TEXT    NOT NULL DEFAULT '',
			source            TEXT    NOT NULL DEFAULT '',
			provider          TEXT    NOT NULL DEFAULT '',
			base_url          TEXT    NOT NULL DEFAULT '',
			model             TEXT    NOT NULL DEFAULT '',
			status            TEXT    NOT NULL DEFAULT 'ok',
			error             TEXT    NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			duration_ms       INTEGER NOT NULL DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_ct_llm_inferencer_stats_usage
			ON ct_llm_inferencer_stats(usage);
		CREATE INDEX IF NOT EXISTS idx_ct_llm_inferencer_stats_created
			ON ct_llm_inferencer_stats(created_at);
	`); err != nil {
		return err
	}
	return nil
}

// Manifest implements notetype.Plugin.
func (p *Plugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:          PluginID,
		Label:       "LLM Inferencer",
		Description: "The app's remote LLM inference provider (OpenRouter, Mistral, or any OpenAI-compatible endpoint) with models per tier (big/middle/small/tiny); app usages like RSS classification resolve their model through this note or fall back to the config.toml local LLM. The API key is write-only and never shown again. Only ONE LLM Inferencer note can exist",
		Category:    "Tools",
		SortOrder:   520,
		DefaultConfig: json.RawMessage(`{
			"provider": "openrouter",
			"base_url": "",
			"api_key": "",
			"api_key_set": false,
			"default_tier": "",
			"tiers": {},
			"usages": {}
		}`),
		Editor: notetype.EditorMeta{Mode: "custom"},
		Viewer: notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:          "test",
				Label:       "Test Connection",
				Description: "Send a tiny chat completion through a tier's model (or an explicit model) and report the reply and latency",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"tier":  {"type": "string", "enum": ["big", "middle", "small", "tiny"]},
						"model": {"type": "string"}
					},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Provider responded",
			},
			{
				ID:          "list_models",
				Label:       "List Models",
				Description: "Fetch the provider's model catalog (for the model pickers)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "",
			},
			{
				ID:          "clear_api_key",
				Label:       "Clear Stored API Key",
				Description: "Remove the stored API key (write-only field: viewing never shows it)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "API key cleared",
			},
			{
				ID:          "clear_stats",
				Label:       "Clear Statistics",
				Description: "Delete ALL recorded LLM call statistics (tokens, routing, latency) — every usage, local and remote. Irreversible",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       true,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Statistics cleared",
			},
		},
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
		Singleton:  true,
	}
}

// CronJobs implements notetype.Plugin: one daily sweep that keeps the LLM
// call ledger (ct_llm_inferencer_stats) bounded — raw rows older than the
// retention window (90 days) are deleted.
func (p *Plugin) CronJobs() []notetype.CronJob {
	return []notetype.CronJob{
		{
			Name:     "stats_retention",
			Schedule: "@every 24h",
			Timeout:  10 * time.Minute,
			Task: func(_ context.Context, rt *jobs.RunContext) (string, error) {
				n, err := sweepStats(rt.DB)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("deleted %d llm call stats rows older than %d days", n, statsRetentionDays), nil
			},
		},
	}
}

// validTier reports whether a tier name is one of the four capability tiers.
func validTier(tier string) bool {
	switch tier {
	case TierBig, TierMiddle, TierSmall, TierTiny:
		return true
	}
	return false
}

// tierNames returns the four tiers in capability order.
func tierNames() []string {
	return []string{TierBig, TierMiddle, TierSmall, TierTiny}
}
