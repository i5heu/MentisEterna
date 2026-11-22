// LLM call statistics: the inferencer plugin owns the app-wide ledger of
// every chat completion performed through the transport (internal/llm
// reports each call through the global sink; internal/server wires this
// plugin's RecordCall in — see notetype.CallStatsRecorder).
//
// Every row is one call: which app usage fired it, how it was routed
// (remote provider via this note / explicit local pin / plain config.toml
// default), which model served it, the backend-reported token counts,
// latency, and the outcome (ok / error text). The note's view aggregates
// them (per usage×routing×model) so the note answers: what feature used
// how many tokens, and what ran locally vs remotely.

package llminferencer

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// statsRetentionDays: raw call rows older than this are swept once a day
// by the stats_retention cron job. Aggregates cover exactly this window;
// the stats table is a ledger, not an archive.
const statsRetentionDays = 90

// statsUsageTest is the pseudo usage id of the note's connection-test
// action: those are real provider calls and belong in the ledger.
const statsUsageTest = "test"

// --- recording (the server wires this in; see notetype.CallStatsRecorder) ---

// SetCallStatsDB implements notetype.CallStatsRecorder: the server injects
// the app database at startup, before any chat traffic can fire.
func (p *Plugin) SetCallStatsDB(db *sql.DB) {
	p.statsMu.Lock()
	p.statsDB = db
	p.statsMu.Unlock()
}

// callStatsDB returns the injected database handle (nil before startup).
func (p *Plugin) callStatsDB() *sql.DB {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()
	return p.statsDB
}

// RecordCall implements notetype.CallStatsRecorder and persists one chat
// call. Best-effort by contract: before startup (nil db) and on any write
// error the call is dropped — statistics must never fail an LLM request.
func (p *Plugin) RecordCall(cs llm.CallStats) {
	db := p.callStatsDB()
	if db == nil {
		return
	}
	status := "ok"
	if !cs.OK {
		status = "error"
	}
	_, err := db.Exec(
		`INSERT INTO ct_llm_inferencer_stats
		 (usage, source, provider, base_url, model, status, error,
		  prompt_tokens, completion_tokens, total_tokens, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cs.Usage, cs.Source, cs.Provider, cs.BaseURL, cs.Model, status,
		trimForJSON(cs.Error, 500),
		cs.PromptTokens, cs.CompletionTokens, cs.TotalTokens, cs.DurationMS,
	)
	_ = err // best-effort ledger; a failed insert must not surface anywhere
}

// --- view ---

// StatsRow is one aggregate of the ledger: every call of one usage routed
// one way (source) to one model. Token counts are backend-reported sums.
type StatsRow struct {
	Usage      string `json:"usage"`
	Label      string `json:"label,omitempty"` // catalog label ("Connection Test" for test)
	Source     string `json:"source"`          // llm.ChatSource*: note | local | config
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model"`
	Calls      int64  `json:"calls"`
	Errors     int64  `json:"errors"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	AvgDurationMS    int64 `json:"avg_duration_ms"` // over successful calls
	LastUsedAt       string `json:"last_used_at,omitempty"`
}

// StatsRecentCall is one of the latest recorded calls (detail incl. errors),
// handy to see what actually fired lately.
type StatsRecentCall struct {
	At                string `json:"at"`
	Usage             string `json:"usage"`
	Label             string `json:"label,omitempty"`
	Source            string `json:"source"`
	Provider          string `json:"provider,omitempty"`
	BaseURL           string `json:"base_url,omitempty"`
	Model             string `json:"model"`
	OK                bool   `json:"ok"`
	Error             string `json:"error,omitempty"`
	PromptTokens      int64  `json:"prompt_tokens"`
	CompletionTokens  int64  `json:"completion_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
	DurationMS        int64  `json:"duration_ms"`
}

// StatsSplit rolls calls/tokens up on one side of the local↔remote split.
type StatsSplit struct {
	Calls            int64 `json:"calls"`
	Errors           int64 `json:"errors"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// StatsView is the statistics block of the note's view: what feature used
// how many tokens, and what ran on the local config.toml system vs this
// note's remote provider.
type StatsView struct {
	Rows           []StatsRow        `json:"rows"`
	Recent         []StatsRecentCall `json:"recent"`
	Totals         StatsSplit        `json:"totals"`
	Local          StatsSplit        `json:"local"`  // source local + config (config.toml system)
	Remote         StatsSplit        `json:"remote"` // source note (this note's provider)
	FirstRecordedAt string           `json:"first_recorded_at,omitempty"`
	LastRecordedAt  string           `json:"last_recorded_at,omitempty"`
	RetentionDays   int              `json:"retention_days"`
}

// emptyStatsView is what the view shows before any call was recorded.
func emptyStatsView() *StatsView {
	return &StatsView{
		Rows:         []StatsRow{},
		Recent:       []StatsRecentCall{},
		RetentionDays: statsRetentionDays,
	}
}

// statsUsageLabel maps a usage id to its catalog label (unknown ids pass
// through; the connection-test pseudo usage gets its own label).
func statsUsageLabel(id string) string {
	if id == "" {
		return "(untagged)"
	}
	if id == statsUsageTest {
		return "Connection Test"
	}
	if spec := llm.UsageSpecByID(id); spec != nil {
		return spec.Label
	}
	return id
}

// loadStatsView aggregates the ledger for the note view.
func loadStatsView(db *sql.DB) (*StatsView, error) {
	out := emptyStatsView()

	rows, err := db.Query(`
		SELECT usage, source, provider, model,
		       COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(CAST(AVG(CASE WHEN status = 'ok' THEN duration_ms END) AS INTEGER), 0),
		       COALESCE(MAX(created_at), '')
		FROM ct_llm_inferencer_stats
		GROUP BY usage, source, provider, model
		ORDER BY usage, source, model`)
	if err != nil {
		return nil, fmt.Errorf("llm_inferencer: stats aggregate: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r StatsRow
		if err := rows.Scan(&r.Usage, &r.Source, &r.Provider, &r.Model,
			&r.Calls, &r.Errors, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.AvgDurationMS, &r.LastUsedAt); err != nil {
			return nil, fmt.Errorf("llm_inferencer: stats scan: %w", err)
		}
		r.Label = statsUsageLabel(r.Usage)
		out.Rows = append(out.Rows, r)
		addSplit(&out.Totals, r)
		if r.Source == llm.ChatSourceNote {
			addSplit(&out.Remote, r)
		} else {
			addSplit(&out.Local, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("llm_inferencer: stats iterate: %w", err)
	}

	recent, err := db.Query(`
		SELECT created_at, usage, source, provider, base_url, model,
		       status, error, prompt_tokens, completion_tokens, total_tokens, duration_ms
		FROM ct_llm_inferencer_stats
		ORDER BY id DESC
		LIMIT 15`)
	if err != nil {
		return nil, fmt.Errorf("llm_inferencer: stats recent: %w", err)
	}
	defer recent.Close()
	for recent.Next() {
		var rc StatsRecentCall
		var status string
		if err := recent.Scan(&rc.At, &rc.Usage, &rc.Source, &rc.Provider, &rc.BaseURL, &rc.Model,
			&status, &rc.Error, &rc.PromptTokens, &rc.CompletionTokens, &rc.TotalTokens, &rc.DurationMS); err != nil {
			return nil, fmt.Errorf("llm_inferencer: stats recent scan: %w", err)
		}
		rc.OK = status == "ok"
		if !rc.OK {
			rc.Error = strings.TrimSpace(rc.Error)
		}
		rc.Label = statsUsageLabel(rc.Usage)
		out.Recent = append(out.Recent, rc)
	}
	if err := recent.Err(); err != nil {
		return nil, fmt.Errorf("llm_inferencer: stats recent iterate: %w", err)
	}

	// Window of the ledger (the retention sweep bounds it).
	_ = db.QueryRow(`SELECT COALESCE(MIN(created_at), ''), COALESCE(MAX(created_at), '')
	                  FROM ct_llm_inferencer_stats`).Scan(&out.FirstRecordedAt, &out.LastRecordedAt)

	return out, nil
}

// addSplit accumulates one aggregate row into a roll-up split.
func addSplit(dst *StatsSplit, r StatsRow) {
	dst.Calls += r.Calls
	dst.Errors += r.Errors
	dst.PromptTokens += r.PromptTokens
	dst.CompletionTokens += r.CompletionTokens
	dst.TotalTokens += r.TotalTokens
}

// clearStats wipes the ledger (the clear_stats action).
func clearStats(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM ct_llm_inferencer_stats`); err != nil {
		return fmt.Errorf("llm_inferencer: clear stats: %w", err)
	}
	return nil
}

// sweepStats deletes call rows older than the retention window
// (the stats_retention cron job, daily).
func sweepStats(db *sql.DB) (int64, error) {
	if db == nil {
		return 0, nil
	}
	res, err := db.Exec(
		`DELETE FROM ct_llm_inferencer_stats WHERE created_at < datetime('now', ?)`,
		fmt.Sprintf("-%d days", statsRetentionDays))
	if err != nil {
		return 0, fmt.Errorf("llm_inferencer: stats retention: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

var _ notetype.CallStatsRecorder = (*Plugin)(nil)
