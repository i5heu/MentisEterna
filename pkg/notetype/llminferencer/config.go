package llminferencer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// --- Limits ---

const (
	maxModels      = 8 // per tier (paranoia: the map only holds 4 tiers anyway)
	maxModelLength = 200
	maxBaseURLLen  = 500
)

// UsageBinding maps one app usage (internal/llm usage catalog, e.g.
// "rss_classify") to a tier of this inferencer, directly to a model, or
// explicitly to the LOCAL LLM ({local: true} — the config.toml feature/tier
// system). Exactly one of Tier / Model / Local must be set. A {local: true}
// pin beats the note's default_tier: the usage keeps using the local LLM
// even when this note maps everything else to the remote provider.
type UsageBinding struct {
	Tier  string `json:"tier,omitempty"`  // big|middle|small|tiny
	Model string `json:"model,omitempty"` // direct model override
	Local bool   `json:"local,omitempty"` // explicit pin to config.toml (local LLM)
}

// TierBinding is the model (and optional endpoint override) for one tier.
type TierBinding struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

// Config is the persisted config payload for an llm_inferencer note.
// ValidateConfig, SaveConfig, and LoadConfig all use this exact JSON shape.
//
// The API key is WRITE-ONLY: it is never included in LoadConfig output
// (api_key is always "" there; api_key_set reports whether one is stored).
// On save, an empty/absent api_key KEEPS the stored key; a non-empty value
// replaces it; the clear_api_key action removes it.
type Config struct {
	Provider    string                  `json:"provider"`
	BaseURL     string                  `json:"base_url"`
	APIKey      string                  `json:"api_key"`     // write-only, ignored by LoadConfig
	APIKeySet   bool                    `json:"api_key_set"` // read-only, computed by LoadConfig
	DefaultTier string                  `json:"default_tier,omitempty"`
	Tiers       map[string]TierBinding  `json:"tiers,omitempty"`
	Usages      map[string]UsageBinding `json:"usages,omitempty"`
}

func defaultConfig() Config {
	return Config{
		Provider: ProviderOpenRouter,
		Tiers:    map[string]TierBinding{},
		Usages:   map[string]UsageBinding{},
	}
}

func defaultConfigJSON() json.RawMessage {
	b, err := json.Marshal(defaultConfig())
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// validProvider reports whether the provider is one of the known providers.
func validProvider(p string) bool {
	switch p {
	case ProviderOpenRouter, ProviderMistral, ProviderCompatible:
		return true
	}
	return false
}

// providerLabel returns the human-readable provider name.
func providerLabel(p string) string {
	switch p {
	case ProviderOpenRouter:
		return "OpenRouter"
	case ProviderMistral:
		return "Mistral"
	case ProviderCompatible:
		return "OpenAI-compatible"
	}
	return p
}

// validateBaseURL checks that a base URL is a valid http(s) URL.
func validateBaseURL(field, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("llm_inferencer: %s must be a valid http(s) URL, got %q", field, raw)
	}
	return nil
}

// ValidateConfig implements notetype.ConfigValidator.
//
// Unknown ids (stale/foreign clients) drop first — see dropUnknownIDs; the
// shapes a user could author still fail loudly with a precise message.
func (p *Plugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("llm_inferencer: invalid payload: %w", err)
	}
	dropUnknownIDs(&cfg)
	return validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if !validProvider(cfg.Provider) {
		return fmt.Errorf("llm_inferencer: provider must be one of openrouter/mistral/compatible, got %q", cfg.Provider)
	}
	if cfg.BaseURL != "" {
		if len(cfg.BaseURL) > maxBaseURLLen {
			return fmt.Errorf("llm_inferencer: base_url is too long (max %d)", maxBaseURLLen)
		}
		if err := validateBaseURL("base_url", cfg.BaseURL); err != nil {
			return err
		}
	} else if cfg.Provider == ProviderCompatible {
		return fmt.Errorf("llm_inferencer: base_url is required for the %q provider (no built-in default)", ProviderCompatible)
	}
	if cfg.DefaultTier != "" && !validTier(cfg.DefaultTier) {
		return fmt.Errorf("llm_inferencer: default_tier must be one of big/middle/small/tiny, got %q", cfg.DefaultTier)
	}

	if len(cfg.Tiers) > len(tierNames())+maxModels {
		return fmt.Errorf("llm_inferencer: too many tier entries (%d)", len(cfg.Tiers))
	}
	for tier, tb := range cfg.Tiers {
		if !validTier(tier) {
			return fmt.Errorf("llm_inferencer: unknown tier %q (must be big/middle/small/tiny)", tier)
		}
		model := strings.TrimSpace(tb.Model)
		if model == "" {
			return fmt.Errorf("llm_inferencer: tier %q: model must not be empty (drop the tier instead)", tier)
		}
		if len(model) > maxModelLength {
			return fmt.Errorf("llm_inferencer: tier %q: model is too long (max %d)", tier, maxModelLength)
		}
		if tb.BaseURL != "" {
			if err := validateBaseURL(fmt.Sprintf("tier %q base_url", tier), tb.BaseURL); err != nil {
				return err
			}
		}
	}

	for usage, ub := range cfg.Usages {
		if llm.UsageSpecByID(usage) == nil {
			return fmt.Errorf("llm_inferencer: unknown usage %q — not in the app usage catalog (valid: %s)",
				usage, strings.Join(llm.KnownUsageIDs(), ", "))
		}
		switch {
		case ub.Local && (ub.Tier != "" || ub.Model != ""):
			return fmt.Errorf("llm_inferencer: usage %q: bind a tier, a model, or local — not several", usage)
		case ub.Tier != "" && ub.Model != "":
			return fmt.Errorf("llm_inferencer: usage %q: bind either a tier or a model, not both", usage)
		case ub.Local:
			// explicit pin to the config.toml local LLM — nothing else to check
		case ub.Tier != "":
			if !validTier(ub.Tier) {
				return fmt.Errorf("llm_inferencer: usage %q: unknown tier %q", usage, ub.Tier)
			}
		case strings.TrimSpace(ub.Model) != "":
			if len(ub.Model) > maxModelLength {
				return fmt.Errorf("llm_inferencer: usage %q: model is too long (max %d)", usage, maxModelLength)
			}
		default:
			return fmt.Errorf("llm_inferencer: usage %q: binding must set tier, model, or local", usage)
		}
	}
	return nil
}

// normalize trims values and fills defaults for maps and provider.
func (cfg *Config) normalize() {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.DefaultTier = strings.TrimSpace(cfg.DefaultTier)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Tiers == nil {
		cfg.Tiers = map[string]TierBinding{}
	}
	if cfg.Usages == nil {
		cfg.Usages = map[string]UsageBinding{}
	}
	for tier, tb := range cfg.Tiers {
		nb := TierBinding{
			Model:   strings.TrimSpace(tb.Model),
			BaseURL: strings.TrimSpace(tb.BaseURL),
		}
		cfg.Tiers[tier] = nb
	}
	for usage, ub := range cfg.Usages {
		nb := UsageBinding{
			Tier:  strings.TrimSpace(ub.Tier),
			Model: strings.TrimSpace(ub.Model),
			Local: ub.Local,
		}
		// A binding that trims to nothing (no tier, no model, no local pin)
		// is dead — drop it instead of persisting an empty object.
		if nb.Tier == "" && nb.Model == "" && !nb.Local {
			delete(cfg.Usages, usage)
			continue
		}
		cfg.Usages[usage] = nb
	}
}

// dropUnknownIDs removes entries whose ids are SERVER catalog metadata this
// server does not know: usage ids outside internal/llm's catalog and tier
// names outside the four capability tiers. Both validation gates run it —
// a stale client carrying ids this server has never heard of (the "unknown
// usage 1" failure) must never wedge a save. What REMAINS is still strictly
// validated: shapes a real user could author (binding modes, provider,
// endpoints, default tier) fail loudly.
func dropUnknownIDs(cfg *Config) {
	for usage := range cfg.Usages {
		if !llm.KnownUsage(usage) {
			delete(cfg.Usages, usage)
		}
	}
	for tier := range cfg.Tiers {
		if !validTier(tier) {
			delete(cfg.Tiers, tier)
		}
	}
}

// sanitizeConfig makes a foreign payload persistable (SaveConfig only):
// dropUnknownIDs plus canonicalizing what remains — conflicts resolve to a
// single binding (local > tier > model), empty bindings and tiers without
// a model drop, an unknown provider resets to the default. ValidateConfig
// stays strict about these shapes; SaveConfig is the tolerant last mile so
// a wedged, unsavable note is impossible.
func sanitizeConfig(cfg *Config) {
	dropUnknownIDs(cfg)
	for usage, ub := range cfg.Usages {
		switch {
		case ub.Local:
			cfg.Usages[usage] = UsageBinding{Local: true}
		case ub.Tier != "":
			cfg.Usages[usage] = UsageBinding{Tier: strings.TrimSpace(ub.Tier)}
		case strings.TrimSpace(ub.Model) != "":
			cfg.Usages[usage] = UsageBinding{Model: strings.TrimSpace(ub.Model)}
		default:
			delete(cfg.Usages, usage) // nothing meaningful to keep
		}
	}
	for tier, tb := range cfg.Tiers {
		if strings.TrimSpace(tb.Model) == "" {
			delete(cfg.Tiers, tier)
		}
	}
	if !validTier(cfg.DefaultTier) {
		cfg.DefaultTier = ""
	}
	if !validProvider(cfg.Provider) {
		cfg.Provider = ProviderOpenRouter
	}
}

// SaveConfig implements notetype.ConfigSaver.
//
// Write-only API key semantics: the payload's api_key only ever REPLACES the
// stored key — an empty value keeps the existing one (so config round-trips
// from the UI never wipe it); removal is done via the clear_api_key action.
func (p *Plugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	cfg := defaultConfig()
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("llm_inferencer: unmarshal payload: %w", err)
		}
		// Tolerate ids this server doesn't know (see sanitizeConfig), then
		// validate what remains and normalize (trim/default maps).
		sanitizeConfig(&cfg)
		if err := validateConfig(cfg); err != nil {
			return err
		}
		cfg.normalize()
	}

	// Preserve the stored key when the payload sends an empty/masked one.
	storedKey := ""
	var existing sql.NullString
	err := tx.QueryRow(`SELECT api_key FROM ct_llm_inferencer_config WHERE note_id = ?`, noteID).Scan(&existing)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("llm_inferencer: load stored key: %w", err)
	}
	storedKey = existing.String
	if cfg.APIKey != "" {
		storedKey = cfg.APIKey
	}

	usagesJSON, err := json.Marshal(cfg.Usages)
	if err != nil || len(usagesJSON) == 0 {
		usagesJSON = []byte("{}")
	}

	if _, err := tx.Exec(`DELETE FROM ct_llm_inferencer_config WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("llm_inferencer: delete config: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO ct_llm_inferencer_config (note_id, provider, base_url, api_key, default_tier, usages_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		noteID, cfg.Provider, cfg.BaseURL, storedKey, cfg.DefaultTier, string(usagesJSON),
	); err != nil {
		return fmt.Errorf("llm_inferencer: insert config: %w", err)
	}

	// Tiers: plain delete + insert (no metadata to preserve).
	if _, err := tx.Exec(`DELETE FROM ct_llm_inferencer_tiers WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("llm_inferencer: delete tiers: %w", err)
	}
	for _, tier := range tierNames() {
		tb, ok := cfg.Tiers[tier]
		if !ok {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO ct_llm_inferencer_tiers (note_id, tier, model, base_url) VALUES (?, ?, ?, ?)`,
			noteID, tier, tb.Model, tb.BaseURL,
		); err != nil {
			return fmt.Errorf("llm_inferencer: insert tier %q: %w", tier, err)
		}
	}
	return nil
}

// LoadConfig implements notetype.ConfigLoader.
//
// The API key is WRITE-ONLY: it is never returned. api_key is always ""
// and api_key_set reports whether a key is stored.
func (p *Plugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	cfg := defaultConfig()
	var usagesJSON string
	var keySet bool
	err := db.QueryRow(
		`SELECT provider, base_url, default_tier, usages_json, api_key != '' FROM ct_llm_inferencer_config WHERE note_id = ?`,
		noteID,
	).Scan(&cfg.Provider, &cfg.BaseURL, &cfg.DefaultTier, &usagesJSON, &keySet)
	if err == sql.ErrNoRows {
		return defaultConfigJSON(), nil
	} else if err != nil {
		return nil, fmt.Errorf("llm_inferencer: load config: %w", err)
	}

	if usagesJSON != "" {
		if err := json.Unmarshal([]byte(usagesJSON), &cfg.Usages); err != nil {
			return nil, fmt.Errorf("llm_inferencer: unmarshal usages: %w", err)
		}
	}

	rows, err := db.Query(`SELECT tier, model, base_url FROM ct_llm_inferencer_tiers WHERE note_id = ?`, noteID)
	if err != nil {
		return nil, fmt.Errorf("llm_inferencer: load tiers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier, model, baseURL string
		if err := rows.Scan(&tier, &model, &baseURL); err != nil {
			return nil, fmt.Errorf("llm_inferencer: scan tier: %w", err)
		}
		if validTier(tier) {
			cfg.Tiers[tier] = TierBinding{Model: model, BaseURL: baseURL}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("llm_inferencer: iterate tiers: %w", err)
	}

	cfg.APIKey = "" // write-only: never returned
	cfg.APIKeySet = keySet
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("llm_inferencer: marshal config: %w", err)
	}
	return out, nil
}

// storedAPIKey returns the persisted key for a note (server-side only — this
// is the one place the key feeds into client construction) and whether one
// is stored.
func storedAPIKey(db *sql.DB, noteID int64) (string, bool, error) {
	var key string
	err := db.QueryRow(`SELECT api_key FROM ct_llm_inferencer_config WHERE note_id = ?`, noteID).Scan(&key)
	if err == sql.ErrNoRows {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("llm_inferencer: load api key: %w", err)
	}
	return key, key != "", nil
}

var _ notetype.ConfigValidator = (*Plugin)(nil)
var _ notetype.ConfigSaver = (*Plugin)(nil)
var _ notetype.ConfigLoader = (*Plugin)(nil)
