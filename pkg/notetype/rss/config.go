package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// --- Limits and defaults ---

const (
	maxFeedsPerNote   = 50
	minIntervalMins   = 15
	maxIntervalMins   = 10080
	minMaxItems       = 10
	maxMaxItems       = 1000
	minRetentionDays  = 7
	maxRetentionDays  = 3650
	maxThreshold      = 100
	defaultInterval   = 60
	defaultMaxItems   = 100
	defaultRetention  = 90
	defaultThreshold  = 5
	defaultFeedLength = 200_000 // max stored chars per feed_content/full_content
)

// Feed is one feed entry in the reader's config.
type Feed struct {
	URL     string `json:"url"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Config is the persisted config payload for an rss note. ValidateConfig,
// SaveConfig, and LoadConfig all use this exact JSON shape.
type Config struct {
	Feeds            []Feed  `json:"feeds"`
	IntervalMinutes  int     `json:"interval_minutes"`
	AutoClassify     bool    `json:"auto_classify"`
	FetchFullContent bool    `json:"fetch_full_content"`
	MaxItemsPerFeed  int     `json:"max_items_per_feed"`
	RetentionDays    int     `json:"retention_days"`
	RejectThreshold  float64 `json:"reject_threshold"`
}

func defaultConfig() Config {
	return Config{
		Feeds:            []Feed{},
		IntervalMinutes:  defaultInterval,
		AutoClassify:     true,
		FetchFullContent: true,
		MaxItemsPerFeed:  defaultMaxItems,
		RetentionDays:    defaultRetention,
		RejectThreshold:  defaultThreshold,
	}
}

func defaultConfigJSON() json.RawMessage {
	b, err := json.Marshal(defaultConfig())
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// normalizeFeedURL trims the URL and canonicalizes the trivial variants
// (trailing slash) so upserts by URL match.
func normalizeFeedURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// ValidateConfig implements notetype.ConfigValidator.
func (p *Plugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("rss: invalid payload: %w", err)
	}
	return validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if len(cfg.Feeds) > maxFeedsPerNote {
		return fmt.Errorf("rss: too many feeds (%d > limit %d)", len(cfg.Feeds), maxFeedsPerNote)
	}
	seen := make(map[string]bool, len(cfg.Feeds))
	for i, f := range cfg.Feeds {
		normalized := normalizeFeedURL(f.URL)
		u, err := url.Parse(normalized)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("rss: feed %d: URL must be a valid http(s) URL, got %q", i+1, f.URL)
		}
		if seen[normalized] {
			return fmt.Errorf("rss: duplicate feed URL %q", f.URL)
		}
		seen[normalized] = true
	}
	if cfg.IntervalMinutes < 0 || cfg.IntervalMinutes > maxIntervalMins {
		return fmt.Errorf("rss: interval_minutes must be between 0 (default) and %d, got %d", maxIntervalMins, cfg.IntervalMinutes)
	}
	if cfg.MaxItemsPerFeed < 0 || cfg.MaxItemsPerFeed > maxMaxItems {
		return fmt.Errorf("rss: max_items_per_feed must be between 0 (default) and %d, got %d", maxMaxItems, cfg.MaxItemsPerFeed)
	}
	if cfg.RetentionDays < 0 || cfg.RetentionDays > maxRetentionDays {
		return fmt.Errorf("rss: retention_days must be between 0 (default) and %d, got %d", maxRetentionDays, cfg.RetentionDays)
	}
	if cfg.RejectThreshold < 0 || cfg.RejectThreshold > maxThreshold {
		return fmt.Errorf("rss: reject_threshold must be between 0 and %d, got %g", maxThreshold, cfg.RejectThreshold)
	}
	return nil
}

// normalize fills defaults for zero-valued numeric settings and feeds.
func (cfg *Config) normalize() {
	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = defaultInterval
	} else if cfg.IntervalMinutes < minIntervalMins {
		cfg.IntervalMinutes = minIntervalMins
	}
	if cfg.MaxItemsPerFeed == 0 {
		cfg.MaxItemsPerFeed = defaultMaxItems
	} else if cfg.MaxItemsPerFeed < minMaxItems {
		cfg.MaxItemsPerFeed = minMaxItems
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = defaultRetention
	} else if cfg.RetentionDays < minRetentionDays {
		cfg.RetentionDays = minRetentionDays
	}
	if cfg.Feeds == nil {
		cfg.Feeds = []Feed{}
	}
	for i := range cfg.Feeds {
		cfg.Feeds[i].URL = normalizeFeedURL(cfg.Feeds[i].URL)
		cfg.Feeds[i].Name = strings.TrimSpace(cfg.Feeds[i].Name)
	}
}

// SaveConfig implements notetype.ConfigSaver.
//
// Feeds are synced without a blanket DELETE: rows are upserted by
// (note_id, url) — preserving fetch metadata (etag, last_fetch_at, item rows
// via FK) — and only feeds missing from the payload are deleted. A whole-
// table wipe would cascade-delete every fetched item on every config save.
func (p *Plugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	cfg := defaultConfig()
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("rss: unmarshal payload: %w", err)
		}
		if err := validateConfig(cfg); err != nil {
			return err
		}
	}
	cfg.normalize()

	// Settings row.
	if _, err := tx.Exec(`DELETE FROM ct_rss_config WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("rss: delete config: %w", err)
	}
	autoClassify := 0
	if cfg.AutoClassify {
		autoClassify = 1
	}
	fetchFull := 0
	if cfg.FetchFullContent {
		fetchFull = 1
	}
	if _, err := tx.Exec(
		`INSERT INTO ct_rss_config
		 (note_id, interval_minutes, auto_classify, fetch_full_content, max_items_per_feed, retention_days, reject_threshold)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		noteID, cfg.IntervalMinutes, autoClassify, fetchFull, cfg.MaxItemsPerFeed, cfg.RetentionDays, cfg.RejectThreshold,
	); err != nil {
		return fmt.Errorf("rss: insert config: %w", err)
	}

	// Sync feeds: upsert payload feeds, drop feeds missing from the payload.
	for _, f := range cfg.Feeds {
		res, err := tx.Exec(
			`UPDATE ct_rss_feeds SET name = ?, enabled = ? WHERE note_id = ? AND url = ?`,
			f.Name, boolToInt(f.Enabled), noteID, f.URL,
		)
		if err != nil {
			return fmt.Errorf("rss: update feed %q: %w", f.URL, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(
				`INSERT INTO ct_rss_feeds (note_id, url, name, enabled) VALUES (?, ?, ?, ?)`,
				noteID, f.URL, f.Name, boolToInt(f.Enabled),
			); err != nil {
				return fmt.Errorf("rss: insert feed %q: %w", f.URL, err)
			}
		}
	}
	// Delete dropped feeds (items cascade away with them).
	if len(cfg.Feeds) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cfg.Feeds)), ",")
		args := make([]any, 0, len(cfg.Feeds)+1)
		args = append(args, noteID)
		for _, f := range cfg.Feeds {
			args = append(args, f.URL)
		}
		if _, err := tx.Exec(
			`DELETE FROM ct_rss_feeds WHERE note_id = ? AND url NOT IN (`+placeholders+`)`, args...,
		); err != nil {
			return fmt.Errorf("rss: delete dropped feeds: %w", err)
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM ct_rss_feeds WHERE note_id = ?`, noteID); err != nil {
			return fmt.Errorf("rss: delete dropped feeds: %w", err)
		}
	}

	return nil
}

// LoadConfig implements notetype.ConfigLoader. Returns the default config
// JSON when nothing is persisted yet.
func (p *Plugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	cfg := defaultConfig()
	err := db.QueryRow(
		`SELECT interval_minutes, auto_classify, fetch_full_content, max_items_per_feed, retention_days, reject_threshold
		 FROM ct_rss_config WHERE note_id = ?`, noteID,
	).Scan(
		&cfg.IntervalMinutes, &cfg.AutoClassify, &cfg.FetchFullContent,
		&cfg.MaxItemsPerFeed, &cfg.RetentionDays, &cfg.RejectThreshold,
	)
	if err == sql.ErrNoRows {
		return defaultConfigJSON(), nil
	} else if err != nil {
		return nil, fmt.Errorf("rss: load config: %w", err)
	}

	rows, err := db.Query(
		`SELECT url, name, enabled FROM ct_rss_feeds WHERE note_id = ? ORDER BY id`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: load feeds: %w", err)
	}
	defer rows.Close()

	cfg.Feeds = nil
	for rows.Next() {
		var f Feed
		var enabled int
		if err := rows.Scan(&f.URL, &f.Name, &enabled); err != nil {
			return nil, fmt.Errorf("rss: scan feed: %w", err)
		}
		f.Enabled = enabled != 0
		cfg.Feeds = append(cfg.Feeds, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rss: iterate feeds: %w", err)
	}
	if cfg.Feeds == nil {
		cfg.Feeds = []Feed{}
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("rss: marshal config: %w", err)
	}
	return out, nil
}

// loadConfigRow loads persisted settings for a note; defaults when absent.
func loadConfigRow(db *sql.DB, noteID int64) (Config, error) {
	cfg := defaultConfig()
	err := db.QueryRow(
		`SELECT interval_minutes, auto_classify, fetch_full_content, max_items_per_feed, retention_days, reject_threshold
		 FROM ct_rss_config WHERE note_id = ?`, noteID,
	).Scan(
		&cfg.IntervalMinutes, &cfg.AutoClassify, &cfg.FetchFullContent,
		&cfg.MaxItemsPerFeed, &cfg.RetentionDays, &cfg.RejectThreshold,
	)
	if err == sql.ErrNoRows {
		return cfg, nil
	} else if err != nil {
		return cfg, fmt.Errorf("rss: load config row: %w", err)
	}
	return cfg, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var _ notetype.ConfigValidator = (*Plugin)(nil)
var _ notetype.ConfigSaver = (*Plugin)(nil)
var _ notetype.ConfigLoader = (*Plugin)(nil)
