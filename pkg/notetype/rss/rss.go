// Package rss implements an "rss" note type ("RSS Reader") plus the
// "rss_prompt" marker note type.
//
// The reader note holds the whole subsystem in one note: the feed list and
// numeric settings live in its config (ct_rss_config / ct_rss_feeds), the ban
// words / penalty rules live in the note body (user-typed lines, read-only for
// the plugin), and fetched articles live in plugin tables (ct_rss_items) —
// the only external piece is the classification prompt, which resides in the
// body of an rss_prompt note resolved from the note hierarchy (nearest wins).
//
// See docs/RSSReader.md for the full design.
package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const (
	// PluginID is the reader note type ("RSS Reader").
	PluginID = "rss"
	// PromptPluginID is the marker note type whose body holds the
	// classification prompt ("RSS Prompt").
	PromptPluginID = "rss_prompt"

	// JobRefreshFeeds is the only job definition: cron-scheduled every 15
	// minutes (due feeds only) and enqueued on demand by the refresh action
	// with payload {"note_id":N,"force":true}.
	JobRefreshFeeds = "refresh_feeds"
)

func init() {
	notetype.Register(&Plugin{})
	notetype.Register(&PromptPlugin{})
}

// --- RSS Reader plugin ---

// Plugin is the "rss" note type: one note = one complete RSS reader.
type Plugin struct {
	// enqueue is injected by the server via notetype.JobEnqueuer; used by the
	// refresh action (rss/refresh_feeds) and save_as_note (_system/vss_index).
	enqueue func(pluginID, jobName string, payload []byte) (int64, error)
}

// ID implements notetype.Plugin.
func (p *Plugin) ID() string { return PluginID }

// InitSchema creates the plugin tables. Feeds and items cascade away with
// their note (reader deleted → feeds → items).
func (p *Plugin) InitSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_rss_config (
			note_id            INTEGER PRIMARY KEY,
			interval_minutes   INTEGER NOT NULL DEFAULT 60,
			auto_classify      INTEGER NOT NULL DEFAULT 1,
			fetch_full_content INTEGER NOT NULL DEFAULT 1,
			max_items_per_feed INTEGER NOT NULL DEFAULT 100,
			retention_days     INTEGER NOT NULL DEFAULT 90,
			reject_threshold   REAL    NOT NULL DEFAULT 5,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS ct_rss_feeds (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id       INTEGER NOT NULL,
			url           TEXT    NOT NULL,
			name          TEXT    NOT NULL DEFAULT '',
			enabled       INTEGER NOT NULL DEFAULT 1,
			etag          TEXT    NOT NULL DEFAULT '',
			last_modified TEXT    NOT NULL DEFAULT '',
			last_fetch_at TEXT,
			last_status   TEXT    NOT NULL DEFAULT '',
			last_error    TEXT    NOT NULL DEFAULT '',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE,
			UNIQUE(note_id, url)
		);

		CREATE TABLE IF NOT EXISTS ct_rss_items (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id         INTEGER NOT NULL,
			item_key        TEXT    NOT NULL,
			title           TEXT    NOT NULL,
			url             TEXT    NOT NULL DEFAULT '',
			author          TEXT    NOT NULL DEFAULT '',
			published_at    TEXT    NOT NULL DEFAULT '',
			fetched_at      TEXT    NOT NULL,
			prompt_note_id  INTEGER NOT NULL DEFAULT 0,
			feed_content    TEXT    NOT NULL DEFAULT '',
			full_content    TEXT    NOT NULL DEFAULT '',
			full_status    TEXT    NOT NULL DEFAULT 'none',
			category        TEXT    NOT NULL DEFAULT '',
			summary         TEXT    NOT NULL DEFAULT '',
			tags_json       TEXT    NOT NULL DEFAULT '[]',
			quality         INTEGER NOT NULL DEFAULT 0,
			significance    REAL    NOT NULL DEFAULT 0,   -- LLM evaluation 0..1
			negativity      REAL    NOT NULL DEFAULT 0,   -- 0 positive .. 1 negative
			personal_relevance REAL NOT NULL DEFAULT 0,  -- 0..1
			affected_now_direct   REAL NOT NULL DEFAULT 0, -- millions
			affected_now_indirect  REAL NOT NULL DEFAULT 0,
			affected_future_direct REAL NOT NULL DEFAULT 0,
			affected_future_indirect REAL NOT NULL DEFAULT 0,
			reflection      TEXT    NOT NULL DEFAULT '',  -- evaluationReflectionParagraph
			points          REAL    NOT NULL DEFAULT 0,   -- computed 0..10
			evaluated       INTEGER NOT NULL DEFAULT 0,    -- classification carried evaluation data
			summary_json    TEXT    NOT NULL DEFAULT '',   -- telegraph F/O/Q summary (what/why/implications)
			summary_status  TEXT    NOT NULL DEFAULT '',   -- '' legacy | pending | done | error | disabled
			summary_error   TEXT    NOT NULL DEFAULT '',
			classify_status TEXT    NOT NULL DEFAULT 'pending',
			classify_error  TEXT    NOT NULL DEFAULT '',
			penalty_score   REAL    NOT NULL DEFAULT 0,
			penalty_json    TEXT    NOT NULL DEFAULT '[]',
			banned          INTEGER NOT NULL DEFAULT 0,
			state           TEXT    NOT NULL DEFAULT 'new',
			saved_note_id   INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(feed_id) REFERENCES ct_rss_feeds(id) ON DELETE CASCADE,
			UNIQUE(feed_id, item_key)
		);

		CREATE INDEX IF NOT EXISTS idx_ct_rss_items_feed  ON ct_rss_items(feed_id, published_at DESC);
		CREATE INDEX IF NOT EXISTS idx_ct_rss_items_state ON ct_rss_items(state, fetched_at DESC);
	`); err != nil {
		return err
	}
	return ensureItemColumns(db)
}

// ensureItemColumns upgrades databases whose ct_rss_items table predates the
// significance-evaluation columns: any missing column is added with its
// default (ALTER TABLE ADD COLUMN per gap; idempotent).
func ensureItemColumns(db *sql.DB) error {
	wanted := [][2]string{
		{"significance", "significance REAL NOT NULL DEFAULT 0"},
		{"negativity", "negativity REAL NOT NULL DEFAULT 0"},
		{"personal_relevance", "personal_relevance REAL NOT NULL DEFAULT 0"},
		{"affected_now_direct", "affected_now_direct REAL NOT NULL DEFAULT 0"},
		{"affected_now_indirect", "affected_now_indirect REAL NOT NULL DEFAULT 0"},
		{"affected_future_direct", "affected_future_direct REAL NOT NULL DEFAULT 0"},
		{"affected_future_indirect", "affected_future_indirect REAL NOT NULL DEFAULT 0"},
		{"reflection", "reflection TEXT NOT NULL DEFAULT ''"},
		{"points", "points REAL NOT NULL DEFAULT 0"},
		{"evaluated", "evaluated INTEGER NOT NULL DEFAULT 0"},
		{"summary_json", "summary_json TEXT NOT NULL DEFAULT ''"},
		{"summary_status", "summary_status TEXT NOT NULL DEFAULT ''"},
		{"summary_error", "summary_error TEXT NOT NULL DEFAULT ''"},
	}

	rows, err := db.Query(`PRAGMA table_info(ct_rss_items)`)
	if err != nil {
		return fmt.Errorf("rss: schema check: %w", err)
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, w := range wanted {
		if existing[w[0]] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ct_rss_items ADD COLUMN ` + w[1]); err != nil {
			return fmt.Errorf("rss: add column %s: %w", w[0], err)
		}
	}
	return nil
}

// Manifest implements notetype.Plugin.
func (p *Plugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:          PluginID,
		Label:       "RSS Reader",
		Description: "Fetches RSS/Atom feeds, resolves full article content, and evaluates items with an LLM (significance, negativity, personal relevance, humans affected — condensed into ranking points) plus a telegraph-style F/O/Q summary; ban words / penalty points / point-rule weights are typed in the note body",
		Category:    "Information",
		SortOrder:   500,
		DefaultConfig: json.RawMessage(`{
			"feeds": [],
			"interval_minutes": 60,
			"auto_classify": true,
			"fetch_full_content": true,
			"max_items_per_feed": 100,
			"retention_days": 90,
			"reject_threshold": 5
		}`),
		Editor: notetype.EditorMeta{Mode: "custom"},
		Viewer: notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:          "refresh",
				Label:       "Refresh Feeds",
				Description: "Schedule an immediate refresh of this reader's feeds",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Feed refresh scheduled",
			},
			{
				ID:          "save_as_note",
				Label:       "Save as Note",
				Description: "Save an article as a real child note (searchable, embedded)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {"item_id": {"type": "integer"}},
					"required": ["item_id"]
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Article saved as note",
			},
			{
				ID:          "set_item_state",
				Label:       "Set Item State",
				Description: "Mark an article read / new / starred / rejected",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"item_id": {"type": "integer"},
						"state": {"type": "string", "enum": ["new", "read", "starred", "rejected"]}
					},
					"required": ["item_id", "state"]
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Item state updated",
			},
			{
				ID:          "get_item",
				Label:       "View Article",
				Description: "Return one article with its stored content (extracted full text, falling back to the feed snippet) for inline reading",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {"item_id": {"type": "integer"}},
					"required": ["item_id"]
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "",
			},
			{
				ID:          "reclassify",
				Label:       "Re-evaluate Item",
				Description: "Re-run the LLM evaluation for one article",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {"item_id": {"type": "integer"}},
					"required": ["item_id"]
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Article re-evaluated",
			},
			{
				ID:          "reclassify_all",
				Label:       "Re-evaluate All",
				Description: "Queue every article of this reader for a fresh LLM evaluation and schedule a refresh (overwrites existing scores)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Re-evaluation scheduled",
			},
			{
				ID:          "evaluate_missing",
				Label:       "Evaluate Missing",
				Description: "Queue only articles with no LLM evaluation for evaluation and schedule a refresh (already-evaluated scores are kept)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Missing evaluations scheduled",
			},
			{
				ID:          "summarize_item",
				Label:       "Summarize Item",
				Description: "Run the LLM summarizer on one article and store the telegraph-style F/O/Q summary",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {"item_id": {"type": "integer"}},
					"required": ["item_id"]
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Article summarized",
			},
			{
				ID:          "summarize_missing",
				Label:       "Summarize Missing",
				Description: "Queue every article without a stored summary for LLM summarization and schedule a refresh (existing summaries are kept)",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Missing summaries scheduled",
			},
			{
				ID:          "reapply_rules",
				Label:       "Reapply Rules",
				Description: "Re-read the ban words / penalty rules from the note body and re-score all articles",
				ParamsSchema: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"required": []
				}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Rules reapplied",
			},
		},
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
	}
}

// CronJobs implements notetype.Plugin. The single definition serves both the
// scheduler (nil payload = all due feeds across all readers) and the refresh
// action (payload {"note_id":N,"force":true}).
func (p *Plugin) CronJobs() []notetype.CronJob {
	return []notetype.CronJob{
		{
			Name:        JobRefreshFeeds,
			Schedule:    "@every 15m",
			MaxParallel: 2,                // different readers may refresh concurrently
			Timeout:     45 * time.Minute, // large batches > interval must not pile up (scheduler also skips ticks)
			MaxRetries:  1,
			Task:        p.refreshFeedsTask,
		},
	}
}

// SetJobEnqueueFunc implements notetype.JobEnqueuer.
func (p *Plugin) SetJobEnqueueFunc(fn func(pluginID, jobName string, payload []byte) (int64, error)) {
	p.enqueue = fn
}

// progressFunc is the injected live-progress broadcast (the server wires it
// via notetype.ProgressReporter at startup). Package-level because the
// classify drain runs as a plain function off the job task, not off the
// plugin instance. Broadcasts are fire-and-forget: nil-guarded in the
// emitter, never block the drain.
var progressFunc func(payload json.RawMessage)

// SetProgressFunc implements notetype.ProgressReporter: the evaluation
// drain streams per-item progress {note_id, done, total, finished} which
// the server broadcasts as plugin.progress live messages.
func (p *Plugin) SetProgressFunc(fn func(payload json.RawMessage)) {
	progressFunc = fn
}

// --- RSS Prompt marker plugin ---

// PromptPlugin is the "rss_prompt" marker note type. It has no config, view,
// or actions — the note body (markdown) simply holds the classification
// prompt. Readers resolve the nearest one in their hierarchy; rendering falls
// through to the standard body textarea exactly like the synthetic
// "standard" type.
type PromptPlugin struct{}

// ID implements notetype.Plugin.
func (p *PromptPlugin) ID() string { return PromptPluginID }

// InitSchema implements notetype.Plugin. No tables — prompt text lives in the
// note body.
func (p *PromptPlugin) InitSchema(db *sql.DB) error { return nil }

// Manifest implements notetype.Plugin.
func (p *PromptPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:          PromptPluginID,
		Label:       "RSS Prompt",
		Description: "Holds the LLM classification prompt for RSS readers in its body; the nearest one in the hierarchy applies",
		Category:    "Information",
		SortOrder:   501,
		Editor:      notetype.EditorMeta{Mode: "none"},
		Viewer:      notetype.ViewerMeta{Mode: "none"},
		HasConfig:   false,
		HasView:     false,
		HasActions:  false,
	}
}

// CronJobs implements notetype.Plugin.
func (p *PromptPlugin) CronJobs() []notetype.CronJob { return nil }
