// Package gridfinity implements the "gridfinity" note type: a singleton
// overview dashboard aggregating every "want" (Wants & Wishes) note into one
// grid — urge-log stats, derived cost metrics, and summary totals. Search,
// filter, and sorting are performed client-side on the view payload.
//
// The plugin owns no tables of its own; it reads the want plugin's
// ct_want_config / ct_want_log tables (the same cross-plugin pattern as
// recipe_overview reading recipe tables).
package gridfinity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/want"
)

// PluginID is the unique note-type identifier.
const PluginID = "gridfinity"

// GridfinityPlugin is the note-type plugin instance.
type GridfinityPlugin struct{}

func init() {
	notetype.Register(&GridfinityPlugin{})
}

func (p *GridfinityPlugin) ID() string { return PluginID }

// InitSchema is a no-op: the dashboard owns no tables. It reads the want
// plugin's tables, whose schema is initialized by that plugin.
func (p *GridfinityPlugin) InitSchema(db *sql.DB) error { return nil }

func (p *GridfinityPlugin) CronJobs() []notetype.CronJob {
	return nil
}

func (p *GridfinityPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            PluginID,
		Label:         "Gridfinity",
		Description:   "Dashboard over all Wants & Wishes notes with search, filters, and cost metrics",
		Category:      "Planning",
		SortOrder:     610,
		DefaultConfig: json.RawMessage(`{}`),
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		// One dashboard per app: it aggregates every want globally, so a
		// second note would show identical content.
		Singleton: true,
		HasConfig: false,
		HasView:   true,
	}
}

// Item is one want note summarized for the dashboard grid. The want's cost
// fields are inlined at the top level (embedded Payload).
type Item struct {
	NoteID       int64  `json:"note_id"`
	Title        string `json:"title"`
	HighPrivate  bool   `json:"high_private"`
	LastEditedAt string `json:"last_edited_at"`

	want.LogStats

	want.Payload
	Metrics want.Metrics `json:"metrics"`
}

// Totals aggregates the whole grid. Sums cover known values only; nil means
// no want provided that field.
type Totals struct {
	Wants          int      `json:"wants"`
	WantedToday    int      `json:"wanted_today"`
	WantedLast30   int      `json:"wanted_last30"`
	NeverWanted    int      `json:"never_wanted"`
	SumAcquisition *float64 `json:"sum_acquisition"`
	SumMonthlyNet  *float64 `json:"sum_monthly_net"`
	SumTotalCost   *float64 `json:"sum_total_cost"`
	SumSpaceM3     *float64 `json:"sum_space_m3"`
}

// View is the BuildView payload for a gridfinity note.
type View struct {
	Items  []Item `json:"items"`
	Totals Totals `json:"totals"`
}

func (p *GridfinityPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	if db == nil {
		return nil, fmt.Errorf("gridfinity: no database available")
	}

	// All want notes with their persisted cost fields and last edit time.
	rows, err := db.Query(`
		SELECT n.id, n.title, n.high_private,
			COALESCE(u.created_at, n.created_at) AS last_edited_at,
			c.acquisition_cost, c.operating_expenses_per_month, c.disposal_costs,
			c.potential_profit_per_month, c.durability_years, c.space_requirements_m3
		FROM notes n
		LEFT JOIN ct_want_config c ON c.note_id = n.id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.type = ?
		ORDER BY n.title
	`, want.PluginID)
	if err != nil {
		return nil, fmt.Errorf("gridfinity: load wants: %w", err)
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var it Item
		var acq, opm, disp, prof, dur, space sql.NullFloat64
		if err := rows.Scan(
			&it.NoteID, &it.Title, &it.HighPrivate, &it.LastEditedAt,
			&acq, &opm, &disp, &prof, &dur, &space,
		); err != nil {
			return nil, fmt.Errorf("gridfinity: scan want: %w", err)
		}
		it.Payload = want.Payload{
			AcquisitionCost:           nullFloatPtr(acq),
			OperatingExpensesPerMonth: nullFloatPtr(opm),
			DisposalCosts:             nullFloatPtr(disp),
			PotentialProfitPerMonth:   nullFloatPtr(prof),
			DurabilityYears:           nullFloatPtr(dur),
			SpaceRequirementsM3:       nullFloatPtr(space),
		}
		it.Metrics = want.ComputeMetrics(it.Payload)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gridfinity: iterate wants: %w", err)
	}

	// Urge-log stats per want (single pass over the whole log).
	logRows, err := db.Query(`
		SELECT l.note_id, l.id, l.created_at
		FROM ct_want_log l
		JOIN notes n ON n.id = l.note_id
		WHERE n.type = ?
		ORDER BY l.id
	`, want.PluginID)
	if err != nil {
		return nil, fmt.Errorf("gridfinity: load want log: %w", err)
	}
	defer logRows.Close()

	logs := make(map[int64][]want.LogEntry)
	for logRows.Next() {
		var noteID int64
		var e want.LogEntry
		if err := logRows.Scan(&noteID, &e.ID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("gridfinity: scan want log: %w", err)
		}
		logs[noteID] = append(logs[noteID], e)
	}
	if err := logRows.Err(); err != nil {
		return nil, fmt.Errorf("gridfinity: iterate want log: %w", err)
	}

	now := time.Now()
	sumAcq, sumNet, sumTotal, sumSpace := 0.0, 0.0, 0.0, 0.0
	nAcq, nNet, nTotal, nSpace := 0, 0, 0, 0
	totals := Totals{Wants: len(items)}
	for i := range items {
		it := &items[i]
		it.LogStats = want.AggregateLogStats(logs[it.NoteID], now)
		if it.TodayCount > 0 {
			totals.WantedToday++
		}
		if it.Last30Days > 0 {
			totals.WantedLast30++
		}
		if it.Total == 0 {
			totals.NeverWanted++
		}
		if it.Payload.AcquisitionCost != nil {
			sumAcq += *it.Payload.AcquisitionCost
			nAcq++
		}
		if it.Metrics.MonthlyNet != nil {
			sumNet += *it.Metrics.MonthlyNet
			nNet++
		}
		if it.Metrics.TotalCost != nil {
			sumTotal += *it.Metrics.TotalCost
			nTotal++
		}
		if it.Payload.SpaceRequirementsM3 != nil {
			sumSpace += *it.Payload.SpaceRequirementsM3
			nSpace++
		}
	}
	if nAcq > 0 {
		v := sumAcq
		totals.SumAcquisition = &v
	}
	if nNet > 0 {
		v := sumNet
		totals.SumMonthlyNet = &v
	}
	if nTotal > 0 {
		v := sumTotal
		totals.SumTotalCost = &v
	}
	if nSpace > 0 {
		v := sumSpace
		totals.SumSpaceM3 = &v
	}

	return &View{Items: items, Totals: totals}, nil
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
