// Package want implements the "want" note type ("Wants & Wishes"): a single
// desired purchase with economic decision fields (acquisition cost, monthly
// operating expenses, disposal costs, potential profit per month, durability
// in years, space requirements in m³) and an append-only urge log fed by the
// "Today I wanted this" action.
//
// Storage follows the plugin conventions: config in ct_want_config (one row
// per note, nullable REAL columns — NULL means "unknown", never zero), the
// urge log in ct_want_log. The note body stays user markdown.
package want

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// PluginID is the unique note-type identifier.
const PluginID = "want"

// configEmptyJSON is the canonical config JSON for a want note without a
// saved config row: every field present and null. ValidateConfig, SaveConfig
// and LoadConfig always agree on this exact structure.
const configEmptyJSON = `{"acquisition_cost":null,"operating_expenses_per_month":null,"disposal_costs":null,"potential_profit_per_month":null,"durability_years":null,"space_requirements_m3":null}`

// maxViewEntries caps the log entries returned in the view (newest first).
// Aggregates always cover the full log.
const maxViewEntries = 50

// Payload is the JSON config structure of a want note. All fields are
// optional nullable numbers; nil means "unknown", not zero — a blank field
// never silently counts as a free purchase.
type Payload struct {
	AcquisitionCost           *float64 `json:"acquisition_cost"`
	OperatingExpensesPerMonth *float64 `json:"operating_expenses_per_month"`
	DisposalCosts             *float64 `json:"disposal_costs"`
	PotentialProfitPerMonth   *float64 `json:"potential_profit_per_month"`
	DurabilityYears           *float64 `json:"durability_years"`
	SpaceRequirementsM3       *float64 `json:"space_requirements_m3"`
}

// Metrics are derived decision metrics computed from a Payload. A metric is
// nil whenever any of its inputs is unknown — partial data never masquerades
// as a result.
type Metrics struct {
	// MonthlyNet is operating expenses minus potential profit per month
	// (negative = the item nets a gain every month).
	MonthlyNet *float64 `json:"monthly_net"`
	// LifetimeMonths is durability in years times 12.
	LifetimeMonths *float64 `json:"lifetime_months"`
	// TotalCost is the whole-life cost: acquisition + disposal +
	// monthly net over the full lifetime (can be negative).
	TotalCost *float64 `json:"total_cost"`
	// CostPerMonth spreads TotalCost evenly over the lifetime.
	CostPerMonth *float64 `json:"cost_per_month"`
	// PaybackMonths is (acquisition + disposal) / potential profit per
	// month — how long until the profit repays the investment.
	PaybackMonths *float64 `json:"payback_months"`
}

// ComputeMetrics derives the decision metrics from a payload. Unknown fields
// (nil) never default to zero: every metric whose inputs are incomplete
// stays nil so the UI can render it as unknown.
func ComputeMetrics(p Payload) Metrics {
	m := Metrics{}
	if p.OperatingExpensesPerMonth != nil && p.PotentialProfitPerMonth != nil {
		v := *p.OperatingExpensesPerMonth - *p.PotentialProfitPerMonth
		m.MonthlyNet = &v
	}
	if p.DurabilityYears != nil {
		v := *p.DurabilityYears * 12
		m.LifetimeMonths = &v
	}
	if p.AcquisitionCost != nil && p.DisposalCosts != nil && m.MonthlyNet != nil && m.LifetimeMonths != nil {
		v := *p.AcquisitionCost + *p.DisposalCosts + (*m.MonthlyNet)*(*m.LifetimeMonths)
		m.TotalCost = &v
		if *m.LifetimeMonths > 0 {
			c := v / *m.LifetimeMonths
			m.CostPerMonth = &c
		}
	}
	if p.AcquisitionCost != nil && p.DisposalCosts != nil && p.PotentialProfitPerMonth != nil && *p.PotentialProfitPerMonth > 0 {
		v := (*p.AcquisitionCost + *p.DisposalCosts) / *p.PotentialProfitPerMonth
		m.PaybackMonths = &v
	}
	return m
}

// WantPlugin is the note-type plugin instance.
type WantPlugin struct{}

func init() {
	notetype.Register(&WantPlugin{})
}

func (p *WantPlugin) ID() string { return PluginID }

func (p *WantPlugin) InitSchema(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_want_config (
			note_id                      INTEGER NOT NULL UNIQUE,
			acquisition_cost             REAL,
			operating_expenses_per_month REAL,
			disposal_costs               REAL,
			potential_profit_per_month   REAL,
			durability_years             REAL,
			space_requirements_m3        REAL,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
	`); err != nil {
		return err
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_want_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id    INTEGER NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_want_log_note
			ON ct_want_log(note_id);
	`)
	return err
}

func (p *WantPlugin) CronJobs() []notetype.CronJob {
	return nil // no background jobs for individual wants
}

func (p *WantPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:          PluginID,
		Label:       "Wants & Wishes",
		Description: "A desired purchase with cost fields and a \"Today I wanted this\" urge log",
		Category:    "Planning",
		SortOrder:   600,
		DefaultConfig: json.RawMessage(`{
			"acquisition_cost": null,
			"operating_expenses_per_month": null,
			"disposal_costs": null,
			"potential_profit_per_month": null,
			"durability_years": null,
			"space_requirements_m3": null
		}`),
		Editor: notetype.EditorMeta{Mode: "custom"},
		Viewer: notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "log_want",
				Label:           "Today I wanted this",
				Description:     "Append an entry to this want's urge log",
				RefreshStrategy: "none",
				SuccessMessage:  "Want logged",
			},
			{
				ID:              "remove_want_log",
				Label:           "Remove Log Entry",
				Description:     "Remove a single urge-log entry (e.g. an accidental press)",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"entry_id":{"type":"integer"}},"required":["entry_id"],"additionalProperties":false}`),
				RefreshStrategy: "none",
				SuccessMessage:  "Entry removed",
			},
			{
				ID:              "clear_want_log",
				Label:           "Clear Want Log",
				Description:     "Delete every urge-log entry of this want",
				Dangerous:       true,
				RefreshStrategy: "none",
				SuccessMessage:  "Want log cleared",
			},
		},
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
	}
}

// --- Config ---

func (p *WantPlugin) ValidateConfig(config json.RawMessage) error {
	if len(config) == 0 {
		return nil // optional
	}
	var payload Payload
	if err := json.Unmarshal(config, &payload); err != nil {
		return fmt.Errorf("want: invalid payload: %w", err)
	}
	fields := []struct {
		name  string
		value *float64
	}{
		{"acquisition_cost", payload.AcquisitionCost},
		{"operating_expenses_per_month", payload.OperatingExpensesPerMonth},
		{"disposal_costs", payload.DisposalCosts},
		{"potential_profit_per_month", payload.PotentialProfitPerMonth},
		{"durability_years", payload.DurabilityYears},
		{"space_requirements_m3", payload.SpaceRequirementsM3},
	}
	for _, f := range fields {
		if f.value == nil {
			continue
		}
		if math.IsNaN(*f.value) || math.IsInf(*f.value, 0) {
			return fmt.Errorf("want: %s must be a finite number", f.name)
		}
		if *f.value < 0 {
			return fmt.Errorf("want: %s must not be negative", f.name)
		}
	}
	return nil
}

// nullableFloat converts a *float64 into a driver value (nil for unknown).
func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (p *WantPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	if len(config) == 0 {
		return nil
	}
	var payload Payload
	if err := json.Unmarshal(config, &payload); err != nil {
		return fmt.Errorf("want: unmarshal payload: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO ct_want_config (
			note_id, acquisition_cost, operating_expenses_per_month, disposal_costs,
			potential_profit_per_month, durability_years, space_requirements_m3
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			acquisition_cost             = excluded.acquisition_cost,
			operating_expenses_per_month = excluded.operating_expenses_per_month,
			disposal_costs               = excluded.disposal_costs,
			potential_profit_per_month   = excluded.potential_profit_per_month,
			durability_years             = excluded.durability_years,
			space_requirements_m3        = excluded.space_requirements_m3
	`,
		noteID,
		nullableFloat(payload.AcquisitionCost),
		nullableFloat(payload.OperatingExpensesPerMonth),
		nullableFloat(payload.DisposalCosts),
		nullableFloat(payload.PotentialProfitPerMonth),
		nullableFloat(payload.DurabilityYears),
		nullableFloat(payload.SpaceRequirementsM3),
	); err != nil {
		return fmt.Errorf("want: upsert config: %w", err)
	}
	return nil
}

func (p *WantPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	var acq, opm, disp, prof, dur, space sql.NullFloat64
	err := db.QueryRow(`
		SELECT acquisition_cost, operating_expenses_per_month, disposal_costs,
			potential_profit_per_month, durability_years, space_requirements_m3
		FROM ct_want_config WHERE note_id = ?
	`, noteID).Scan(&acq, &opm, &disp, &prof, &dur, &space)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No config saved yet: return the canonical empty shape so the
			// config structure is identical with and without a row.
			return json.RawMessage(configEmptyJSON), nil
		}
		return nil, fmt.Errorf("want: load config: %w", err)
	}
	result, err := json.Marshal(Payload{
		AcquisitionCost:           nullFloatFrom(acq),
		OperatingExpensesPerMonth: nullFloatFrom(opm),
		DisposalCosts:             nullFloatFrom(disp),
		PotentialProfitPerMonth:   nullFloatFrom(prof),
		DurabilityYears:           nullFloatFrom(dur),
		SpaceRequirementsM3:       nullFloatFrom(space),
	})
	if err != nil {
		return nil, fmt.Errorf("want: marshal payload: %w", err)
	}
	return json.RawMessage(result), nil
}

func nullFloatFrom(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// --- Log helpers ---

// LogEntry is a single "Today I wanted this" record.
type LogEntry struct {
	ID        int64  `json:"id"`
	CreatedAt string `json:"created_at"`
}

// LogStats summarizes a set of urge-log entries. Order-agnostic: it works on
// entries in any order.
type LogStats struct {
	Total        int    `json:"total"`
	WantedDays   int    `json:"wanted_days"`
	Last30Days   int    `json:"last30_days"`
	TodayCount   int    `json:"today_count"`
	LastWantedAt string `json:"last_wanted_at"`
}

// parseLogTime parses a log created_at timestamp as stored by SQLite
// (RFC3339 with fractional seconds).
func parseLogTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want: parse log time %q: %w", s, err)
	}
	return t, nil
}

// AggregateLogStats computes urge-log stats from the given entries relative
// to now. "Today" and day counting use now's location (the server's local
// timezone in production); entries with unparseable timestamps still count
// toward Total but no other metric.
func AggregateLogStats(entries []LogEntry, now time.Time) LogStats {
	stats := LogStats{}
	today := now.Format("2006-01-02")
	cutoff := now.Add(-30 * 24 * time.Hour)
	days := make(map[string]struct{}, len(entries))
	var last time.Time
	for _, e := range entries {
		stats.Total++
		t, err := parseLogTime(e.CreatedAt)
		if err != nil {
			continue
		}
		localDate := t.In(now.Location()).Format("2006-01-02")
		days[localDate] = struct{}{}
		if !t.Before(cutoff) {
			stats.Last30Days++
		}
		if localDate == today {
			stats.TodayCount++
		}
		if last.IsZero() || t.After(last) {
			last = t
			stats.LastWantedAt = e.CreatedAt
		}
	}
	stats.WantedDays = len(days)
	return stats
}

// --- View ---

// WantView is the computed view of a want note: urge-log aggregates, the
// newest log entries, and the derived cost metrics.
type WantView struct {
	LogStats
	Entries []LogEntry `json:"entries"`
	Metrics Metrics    `json:"metrics"`
}

func (p *WantPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	return p.buildView(db, noteID)
}

// buildView assembles the view for one want note. It is also the return
// payload of every log action so the UI can update in place.
func (p *WantPlugin) buildView(db *sql.DB, noteID int64) (*WantView, error) {
	// Cost fields (a want without a saved config row is simply all-unknown).
	var acq, opm, disp, prof, dur, space sql.NullFloat64
	err := db.QueryRow(`
		SELECT acquisition_cost, operating_expenses_per_month, disposal_costs,
			potential_profit_per_month, durability_years, space_requirements_m3
		FROM ct_want_config WHERE note_id = ?
	`, noteID).Scan(&acq, &opm, &disp, &prof, &dur, &space)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("want: load config for view: %w", err)
	}
	payload := Payload{
		AcquisitionCost:           nullFloatFrom(acq),
		OperatingExpensesPerMonth: nullFloatFrom(opm),
		DisposalCosts:             nullFloatFrom(disp),
		PotentialProfitPerMonth:   nullFloatFrom(prof),
		DurabilityYears:           nullFloatFrom(dur),
		SpaceRequirementsM3:       nullFloatFrom(space),
	}

	// Urge log: newest first for display; aggregates cover everything.
	rows, err := db.Query(`
		SELECT id, created_at FROM ct_want_log WHERE note_id = ? ORDER BY id DESC
	`, noteID)
	if err != nil {
		return nil, fmt.Errorf("want: load log: %w", err)
	}
	defer rows.Close()

	entries := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("want: scan log entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("want: iterate log: %w", err)
	}

	view := &WantView{
		LogStats: AggregateLogStats(entries, time.Now()),
		Entries:  entries,
		Metrics:  ComputeMetrics(payload),
	}
	if len(view.Entries) > maxViewEntries {
		view.Entries = view.Entries[:maxViewEntries]
	}
	return view, nil
}

// --- Actions ---

func (p *WantPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	if db == nil {
		return nil, fmt.Errorf("want: no database available")
	}
	switch actionID {
	case "log_want":
		if _, err := db.Exec(`INSERT INTO ct_want_log (note_id) VALUES (?)`, noteID); err != nil {
			return nil, fmt.Errorf("want: log want: %w", err)
		}
		return p.buildView(db, noteID)
	case "remove_want_log":
		var in struct {
			EntryID int64 `json:"entry_id"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &in); err != nil {
				return nil, &notetype.BadRequestError{Message: fmt.Sprintf("want: invalid params: %v", err)}
			}
		}
		if in.EntryID <= 0 {
			return nil, &notetype.BadRequestError{Message: "want: entry_id is required"}
		}
		res, err := db.Exec(`DELETE FROM ct_want_log WHERE id = ? AND note_id = ?`, in.EntryID, noteID)
		if err != nil {
			return nil, fmt.Errorf("want: remove log entry: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, &notetype.BadRequestError{Message: "want: log entry not found"}
		}
		return p.buildView(db, noteID)
	case "clear_want_log":
		if _, err := db.Exec(`DELETE FROM ct_want_log WHERE note_id = ?`, noteID); err != nil {
			return nil, fmt.Errorf("want: clear log: %w", err)
		}
		return p.buildView(db, noteID)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}
