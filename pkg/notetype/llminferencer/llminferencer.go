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
	"database/sql"
	"encoding/json"

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
type Plugin struct{}

// ID implements notetype.Plugin.
func (p *Plugin) ID() string { return PluginID }

// InitSchema creates the plugin tables. The config row (including the API
// key) and tier models cascade away with their note.
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
		},
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
		Singleton:  true,
	}
}

// CronJobs implements notetype.Plugin.
func (p *Plugin) CronJobs() []notetype.CronJob { return nil }

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
