package want

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"pgregory.net/rapid"
)

// fp is a helper for building *float64 literals in tests.
func fp(v float64) *float64 { return &v }

func TestWantPluginHarness(t *testing.T) {
	plugintest.Run(t, &WantPlugin{}, plugintest.TestData{
		ValidPayload:   `{"acquisition_cost":1200,"operating_expenses_per_month":5.5,"disposal_costs":30,"potential_profit_per_month":10,"durability_years":4,"space_requirements_m3":0.25}`,
		InvalidPayload: `{"acquisition_cost":-1}`,
	})
}

func TestComputeMetrics(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		payload Payload
		want    Metrics
	}{
		// Pins every formula on a fully-specified want: net, lifetime,
		// whole-life total (which may be negative = lifetime gain),
		// spread per month, and profit payback.
		"all fields known": {
			payload: Payload{
				AcquisitionCost:           fp(1000),
				OperatingExpensesPerMonth: fp(10),
				DisposalCosts:             fp(50),
				PotentialProfitPerMonth:   fp(30),
				DurabilityYears:           fp(5),
			},
			want: Metrics{
				MonthlyNet:     fp(-20),
				LifetimeMonths: fp(60),
				TotalCost:      fp(-150),
				CostPerMonth:   fp(-2.5),
				PaybackMonths:  fp(35),
			},
		},
		// Zero profit must yield metrics but never a payback (guard
		// against division by zero).
		"zero profit has no payback": {
			payload: Payload{
				AcquisitionCost:           fp(100),
				OperatingExpensesPerMonth: fp(10),
				DisposalCosts:             fp(0),
				PotentialProfitPerMonth:   fp(0),
				DurabilityYears:           fp(1),
			},
			want: Metrics{
				MonthlyNet:     fp(10),
				LifetimeMonths: fp(12),
				TotalCost:      fp(220),
				CostPerMonth:   fp(220.0 / 12.0),
			},
		},
		// Unknown operating expenses poisons every metric that needs the
		// monthly net, but payback and lifetime stay computable.
		"unknown operating expenses keeps payback": {
			payload: Payload{
				AcquisitionCost:         fp(200),
				DisposalCosts:           fp(20),
				PotentialProfitPerMonth: fp(10),
				DurabilityYears:         fp(2),
			},
			want: Metrics{
				LifetimeMonths: fp(24),
				PaybackMonths:  fp(22),
			},
		},
		// Unknown durability poisons lifetime, total, and per-month cost,
		// but the monthly net is still meaningful.
		"unknown durability keeps monthly net": {
			payload: Payload{
				AcquisitionCost:           fp(200),
				OperatingExpensesPerMonth: fp(3),
				DisposalCosts:             fp(20),
				PotentialProfitPerMonth:   fp(10),
			},
			want: Metrics{MonthlyNet: fp(-7), PaybackMonths: fp(22)},
		},
		// Zero durability is a valid value (not unknown): lifetime is 0
		// months, total collapses to acquisition + disposal, and there
		// is nothing to spread per month.
		"zero durability": {
			payload: Payload{
				AcquisitionCost:           fp(100),
				OperatingExpensesPerMonth: fp(10),
				DisposalCosts:             fp(20),
				PotentialProfitPerMonth:   fp(0),
				DurabilityYears:           fp(0),
			},
			want: Metrics{
				MonthlyNet:     fp(10),
				LifetimeMonths: fp(0),
				TotalCost:      fp(120),
			},
		},
		// A lone acquisition cost must not pretend to be a total: every
		// derived metric stays unknown (nil), never zero-filled.
		"only acquisition known yields all-unknown metrics": {
			payload: Payload{AcquisitionCost: fp(300)},
			want:    Metrics{},
		},
		// A fully empty payload is the fresh-note state: no metric may
		// appear out of thin air.
		"empty payload": {
			payload: Payload{},
			want:    Metrics{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ComputeMetrics(tc.payload)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ComputeMetrics() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		payload string
		wantErr bool
	}{
		// The canonical full payload must validate — this is exactly
		// what the editor saves.
		"full payload accepted": {
			payload: `{"acquisition_cost":1200,"operating_expenses_per_month":5.5,"disposal_costs":30,"potential_profit_per_month":10,"durability_years":4,"space_requirements_m3":0.25}`,
		},
		// Partial saves are legal: the user fills one field at a time.
		"partial payload accepted":      {payload: `{"acquisition_cost": 10}`},
		"nulls accepted":                {payload: configEmptyJSON},
		"empty object accepted":         {payload: `{}`},
		"json null accepted":            {payload: `null`},
		"empty raw message accepted":    {payload: ``},
		"extra keys tolerated":          {payload: `{"acquisition_cost": 1, "future_field": true}`},
		"negative acquisition rejected": {payload: `{"acquisition_cost": -0.01}`, wantErr: true},
		"negative operating rejected":   {payload: `{"operating_expenses_per_month": -1}`, wantErr: true},
		"negative disposal rejected":    {payload: `{"disposal_costs": -1}`, wantErr: true},
		"negative profit rejected":      {payload: `{"potential_profit_per_month": -1}`, wantErr: true},
		"negative durability rejected":  {payload: `{"durability_years": -1}`, wantErr: true},
		"negative space rejected":       {payload: `{"space_requirements_m3": -1}`, wantErr: true},
		"string value rejected":         {payload: `{"acquisition_cost": "100"}`, wantErr: true},
		"object value rejected":         {payload: `{"durability_years": {}}`, wantErr: true},
		"boolean value rejected":        {payload: `{"space_requirements_m3": true}`, wantErr: true},
		"malformed json rejected":       {payload: `{`, wantErr: true},
		"scientific notation accepted":  {payload: `{"acquisition_cost": 1.2e3}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := (&WantPlugin{}).ValidateConfig(json.RawMessage(tc.payload))
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateConfig(%s) error = %v, wantErr = %v", tc.payload, err, tc.wantErr)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		payload Payload
	}{
		// Every field must survive the DB round trip bit-for-bit.
		"all fields": {payload: Payload{
			AcquisitionCost:           fp(1234.56),
			OperatingExpensesPerMonth: fp(7.25),
			DisposalCosts:             fp(49.99),
			PotentialProfitPerMonth:   fp(3.5),
			DurabilityYears:           fp(12.5),
			SpaceRequirementsM3:       fp(0.05),
		}},
		// NULL columns must load back as nil, not zero.
		"all unknown": {payload: Payload{}},
		// Mixed known/unknown pins per-column NULL handling.
		"partial": {payload: Payload{AcquisitionCost: fp(10), DurabilityYears: fp(3)}},
		// Zero is a value: it must round trip as 0, distinct from NULL.
		"zeros": {payload: Payload{
			AcquisitionCost:           fp(0),
			OperatingExpensesPerMonth: fp(0),
			DisposalCosts:             fp(0),
			PotentialProfitPerMonth:   fp(0),
			DurabilityYears:           fp(0),
			SpaceRequirementsM3:       fp(0),
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := plugintest.DB(t, &WantPlugin{})
			noteID := plugintest.CreateNote(t, d, "Round Trip", &WantPlugin{})
			p := &WantPlugin{}
			if err := p.ValidateConfig(mustJSON(t, tc.payload)); err != nil {
				t.Fatalf("ValidateConfig: %v", err)
			}
			if err := saveConfig(t, d, p, noteID, tc.payload); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}
			loaded, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			var got Payload
			if err := json.Unmarshal(loaded, &got); err != nil {
				t.Fatalf("unmarshal loaded config: %v", err)
			}
			if diff := cmp.Diff(tc.payload, got); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
			// The loaded config must always pass validation again —
			// this is the plugintest Config_RoundTrip contract.
			if err := p.ValidateConfig(loaded); err != nil {
				t.Errorf("loaded config fails ValidateConfig: %v", err)
			}
		})
	}
}

// TestSaveConfigUpsertReplacesValues pins the ON CONFLICT(note_id) DO UPDATE
// path: saving again on the same note must replace, not duplicate or merge.
func TestSaveConfigUpsertReplacesValues(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	noteID := plugintest.CreateNote(t, d, "Upsert", &WantPlugin{})
	p := &WantPlugin{}

	first := Payload{AcquisitionCost: fp(100), DurabilityYears: fp(5)}
	if err := saveConfig(t, d, p, noteID, first); err != nil {
		t.Fatalf("first SaveConfig: %v", err)
	}
	second := Payload{AcquisitionCost: fp(200)} // durability intentionally dropped
	if err := saveConfig(t, d, p, noteID, second); err != nil {
		t.Fatalf("second SaveConfig: %v", err)
	}

	var got Payload
	loaded, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(second, got); diff != "" {
		t.Errorf("upsert mismatch (-want +got):\n%s", diff)
	}
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_want_config WHERE note_id = ?`, noteID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected exactly 1 config row after re-save, got %d", rows)
	}
}

// TestLoadConfigWithoutRow pins the canonical empty shape: a note without a
// saved config still loads a well-formed all-null payload (shape consistency
// between ValidateConfig/SaveConfig/LoadConfig).
func TestLoadConfigWithoutRow(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	noteID := plugintest.CreateNote(t, d, "No Config Yet", &WantPlugin{})
	p := &WantPlugin{}

	loaded, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(loaded, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantKeys := []string{
		"acquisition_cost", "operating_expenses_per_month", "disposal_costs",
		"potential_profit_per_month", "durability_years", "space_requirements_m3",
	}
	if len(raw) != len(wantKeys) {
		t.Fatalf("expected %d keys, got %d: %s", len(wantKeys), len(raw), string(loaded))
	}
	for _, k := range wantKeys {
		v, ok := raw[k]
		if !ok {
			t.Errorf("missing key %q in %s", k, string(loaded))
			continue
		}
		if v != nil {
			t.Errorf("key %q must be null for a note without config, got %v", k, v)
		}
	}
	if err := p.ValidateConfig(loaded); err != nil {
		t.Errorf("empty shape fails ValidateConfig: %v", err)
	}
}

func TestAggregateLogStats(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		entries []LogEntry
		now     time.Time
		want    LogStats
	}{
		// No entries must produce the zero value, not a bogus "last" date.
		"no entries": {
			entries: nil,
			now:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want:    LogStats{},
		},
		// Two presses the same day: one distinct day, both count as today
		// and as recent; the newer entry wins "last wanted".
		"two presses same day": {
			entries: []LogEntry{
				{ID: 1, CreatedAt: "2026-03-15T09:00:00.000Z"},
				{ID: 2, CreatedAt: "2026-03-15T18:30:00.000Z"},
			},
			now: time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want: LogStats{
				Total:        2,
				WantedDays:   1,
				Last30Days:   2,
				TodayCount:   2,
				LastWantedAt: "2026-03-15T18:30:00.000Z",
			},
		},
		// An entry older than 30 days counts toward total and distinct
		// days but neither today nor the 30-day window.
		"entry older than 30 days": {
			entries: []LogEntry{{ID: 1, CreatedAt: "2026-02-01T00:00:00.000Z"}},
			now:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want: LogStats{
				Total:        1,
				WantedDays:   1,
				LastWantedAt: "2026-02-01T00:00:00.000Z",
			},
		},
		// The 30-day boundary is inclusive: exactly now-30d still counts.
		"boundary entry exactly at cutoff": {
			entries: []LogEntry{{ID: 1, CreatedAt: "2026-02-13T12:00:00.000Z"}},
			now:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want: LogStats{
				Total:        1,
				WantedDays:   1,
				Last30Days:   1,
				LastWantedAt: "2026-02-13T12:00:00.000Z",
			},
		},
		// Corrupt timestamps must never crash or silently vanish from the
		// total, but they must not leak into day/recent/today stats.
		"unparseable timestamp counts toward total only": {
			entries: []LogEntry{{ID: 1, CreatedAt: "not-a-timestamp"}},
			now:     time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want:    LogStats{Total: 1},
		},
		// With mixed entries, "last wanted" must come from the newest
		// parseable entry, not from the corrupt or older ones.
		"last wanted ignores unparseable entries": {
			entries: []LogEntry{
				{ID: 1, CreatedAt: "garbage"},
				{ID: 2, CreatedAt: "2026-03-10T08:00:00.000Z"},
				{ID: 3, CreatedAt: "2026-03-14T08:00:00.000Z"},
			},
			now: time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want: LogStats{
				Total:        3,
				WantedDays:   2,
				Last30Days:   2,
				LastWantedAt: "2026-03-14T08:00:00.000Z",
			},
		},
		// "Today" buckets by the server's local day: an entry stored in
		// UTC late evening is already the next local day in UTC+2.
		"today uses the local day": {
			entries: []LogEntry{{ID: 1, CreatedAt: "2026-03-15T23:30:00.000Z"}},
			now:     time.Date(2026, 3, 16, 1, 0, 0, 0, time.FixedZone("TEST", 2*60*60)),
			want: LogStats{
				Total:        1,
				WantedDays:   1,
				Last30Days:   1,
				TodayCount:   1,
				LastWantedAt: "2026-03-15T23:30:00.000Z",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := AggregateLogStats(tc.entries, tc.now)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("AggregateLogStats() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// mustAction runs HandleAction and fails the test on error.
func mustAction(t *testing.T, p *WantPlugin, ctx context.Context, db *sql.DB, noteID int64, action string, params json.RawMessage) *WantView {
	t.Helper()
	res, err := p.HandleAction(ctx, db, 0, noteID, action, params)
	if err != nil {
		t.Fatalf("action %s: %v", action, err)
	}
	view, ok := res.(*WantView)
	if !ok {
		t.Fatalf("action %s: expected *WantView, got %T", action, res)
	}
	return view
}

func TestActionsLifecycle(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	p := &WantPlugin{}
	ctx := context.Background()
	noteID := plugintest.CreateNote(t, d, "Espresso Machine", &WantPlugin{})
	otherID := plugintest.CreateNote(t, d, "Standing Desk", &WantPlugin{})

	// Pressing the button twice logs two entries; both are "today".
	v1 := mustAction(t, p, ctx, d.DB, noteID, "log_want", nil)
	if v1.Total != 1 || v1.TodayCount != 1 || len(v1.Entries) != 1 {
		t.Fatalf("after first press: total=%d today=%d entries=%d", v1.Total, v1.TodayCount, len(v1.Entries))
	}
	v2 := mustAction(t, p, ctx, d.DB, noteID, "log_want", nil)
	if v2.Total != 2 || v2.TodayCount != 2 {
		t.Fatalf("after second press: total=%d today=%d", v2.Total, v2.TodayCount)
	}
	// Entries are newest-first so the UI can render the recent log.
	if len(v2.Entries) != 2 || v2.Entries[0].ID <= v2.Entries[1].ID {
		t.Fatalf("entries not newest-first: %+v", v2.Entries)
	}
	if v2.LastWantedAt == "" {
		t.Fatal("LastWantedAt must be set after logging")
	}

	// Removing an entry returns the updated view.
	removed := mustAction(t, p, ctx, d.DB, noteID, "remove_want_log", json.RawMessage(fmt.Sprintf(`{"entry_id":%d}`, v2.Entries[0].ID)))
	if removed.Total != 1 || len(removed.Entries) != 1 {
		t.Fatalf("after remove: total=%d entries=%d", removed.Total, len(removed.Entries))
	}

	// Removing a nonexistent entry is a 400-style bad request, not a 500.
	if _, err := p.HandleAction(ctx, d.DB, 0, noteID, "remove_want_log", json.RawMessage(`{"entry_id":999999}`)); err == nil {
		t.Fatal("expected error removing nonexistent entry")
	} else {
		var bad *notetype.BadRequestError
		if !errors.As(err, &bad) {
			t.Fatalf("expected BadRequestError, got %T: %v", err, err)
		}
	}

	// An entry of another want must not be removable through this note.
	otherView := mustAction(t, p, ctx, d.DB, otherID, "log_want", nil)
	if len(otherView.Entries) != 1 {
		t.Fatalf("other note should have 1 entry, got %d", len(otherView.Entries))
	}
	foreign := json.RawMessage(fmt.Sprintf(`{"entry_id":%d}`, otherView.Entries[0].ID))
	if _, err := p.HandleAction(ctx, d.DB, 0, noteID, "remove_want_log", foreign); err == nil {
		t.Fatal("expected error removing a foreign note's entry")
	} else {
		var bad *notetype.BadRequestError
		if !errors.As(err, &bad) {
			t.Fatalf("expected BadRequestError for foreign entry, got %T: %v", err, err)
		}
	}

	// Clearing the other note's log empties it.
	cleared := mustAction(t, p, ctx, d.DB, otherID, "clear_want_log", nil)
	if cleared.Total != 0 || len(cleared.Entries) != 0 {
		t.Fatalf("after clear: total=%d entries=%d", cleared.Total, len(cleared.Entries))
	}

	// Unknown actions must surface ErrUnknownAction (server maps it to 404).
	if _, err := p.HandleAction(ctx, d.DB, 0, noteID, "teleport", nil); !errors.Is(err, notetype.ErrUnknownAction) {
		t.Fatalf("expected ErrUnknownAction, got %v", err)
	}

	// Nil DB (the plugintest dry-run path) must error, never panic.
	if _, err := p.HandleAction(ctx, nil, 0, noteID, "log_want", nil); err == nil {
		t.Fatal("expected error with nil db")
	}
}

// TestRemoveWantLogRejectsBadParams pins the params validation of the remove
// action: missing, non-positive, or malformed entry IDs are 400s.
func TestRemoveWantLogRejectsBadParams(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	p := &WantPlugin{}
	ctx := context.Background()
	noteID := plugintest.CreateNote(t, d, "Want", &WantPlugin{})

	cases := map[string]struct {
		params json.RawMessage
	}{
		"missing entry_id": {params: nil},
		"zero entry_id":    {params: json.RawMessage(`{"entry_id":0}`)},
		"negative entry":   {params: json.RawMessage(`{"entry_id":-3}`)},
		"malformed json":   {params: json.RawMessage(`{`)},
		"string entry_id":  {params: json.RawMessage(`{"entry_id":"7"}`)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := p.HandleAction(ctx, d.DB, 0, noteID, "remove_want_log", tc.params)
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
			var bad *notetype.BadRequestError
			if !errors.As(err, &bad) {
				t.Fatalf("expected BadRequestError for %s, got %T: %v", name, err, err)
			}
		})
	}
}

// TestLogWantRequiresExistingNote pins the FK guard: logging against a note
// id that does not exist must fail (the log table cascades with real notes).
func TestLogWantRequiresExistingNote(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	p := &WantPlugin{}
	if _, err := p.HandleAction(context.Background(), d.DB, 0, 424242, "log_want", nil); err == nil {
		t.Fatal("expected FK violation error for a nonexistent note")
	}
}

// TestBuildViewCapsEntries pins the view size contract: aggregates cover the
// full log, but the entries list is capped (newest first).
func TestBuildViewCapsEntries(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	noteID := plugintest.CreateNote(t, d, "Many Presses", &WantPlugin{})
	for i := 0; i < maxViewEntries+10; i++ {
		if _, err := d.Exec(`INSERT INTO ct_want_log (note_id) VALUES (?)`, noteID); err != nil {
			t.Fatalf("insert log entry %d: %v", i, err)
		}
	}
	res, err := (&WantPlugin{}).BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view, ok := res.(*WantView)
	if !ok {
		t.Fatalf("expected *WantView, got %T", res)
	}
	if view.Total != maxViewEntries+10 {
		t.Errorf("Total must cover the full log, got %d", view.Total)
	}
	if len(view.Entries) != maxViewEntries {
		t.Errorf("entries must be capped at %d, got %d", maxViewEntries, len(view.Entries))
	}
}

// TestBuildViewMetricsFromConfig pins that the view carries server-computed
// metrics for the note's own config.
func TestBuildViewMetricsFromConfig(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	noteID := plugintest.CreateNote(t, d, "Metrics Want", &WantPlugin{})
	p := &WantPlugin{}
	payload := Payload{
		AcquisitionCost:           fp(1000),
		OperatingExpensesPerMonth: fp(10),
		DisposalCosts:             fp(50),
		PotentialProfitPerMonth:   fp(30),
		DurabilityYears:           fp(5),
	}
	if err := saveConfig(t, d, p, noteID, payload); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	res, err := p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view := res.(*WantView)
	if diff := cmp.Diff(ComputeMetrics(payload), view.Metrics); diff != "" {
		t.Errorf("view metrics mismatch (-want +got):\n%s", diff)
	}
}

// --- Property-based tests ---

// genOptionalFloat generates a finite non-negative float that may be absent
// (nil = unknown), mirroring the payload contract.
func genOptionalFloat(rt *rapid.T, name string) *float64 {
	if !rapid.Bool().Draw(rt, "has_"+name) {
		return nil
	}
	v := rapid.Float64Range(0, 1e6).Draw(rt, name)
	return &v
}

func genPayload(rt *rapid.T) Payload {
	return Payload{
		AcquisitionCost:           genOptionalFloat(rt, "acquisition_cost"),
		OperatingExpensesPerMonth: genOptionalFloat(rt, "operating_expenses_per_month"),
		DisposalCosts:             genOptionalFloat(rt, "disposal_costs"),
		PotentialProfitPerMonth:   genOptionalFloat(rt, "potential_profit_per_month"),
		DurabilityYears:           genOptionalFloat(rt, "durability_years"),
		SpaceRequirementsM3:       genOptionalFloat(rt, "space_requirements_m3"),
	}
}

// TestPropComputeMetricsArithmetic: for any payload, every metric must (a)
// match independent recomputation from the inputs, and (b) stay nil whenever
// an input is unknown — partial data must never masquerade as a result.
func TestPropComputeMetricsArithmetic(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		p := genPayload(rt)
		m := ComputeMetrics(p)

		// Invariant: monthly net is exactly operating − profit, and is
		// nil iff either input is unknown.
		if p.OperatingExpensesPerMonth != nil && p.PotentialProfitPerMonth != nil {
			if m.MonthlyNet == nil {
				rt.Fatalf("MonthlyNet nil with both inputs known: %+v", p)
			}
			want := *p.OperatingExpensesPerMonth - *p.PotentialProfitPerMonth
			if *m.MonthlyNet != want {
				rt.Fatalf("MonthlyNet = %v, want %v", *m.MonthlyNet, want)
			}
		} else if m.MonthlyNet != nil {
			rt.Fatalf("MonthlyNet must be nil when inputs are unknown: %+v", p)
		}

		// Invariant: lifetime months is exactly durability × 12.
		if p.DurabilityYears != nil {
			if m.LifetimeMonths == nil || *m.LifetimeMonths != *p.DurabilityYears*12 {
				rt.Fatalf("LifetimeMonths wrong for %v: %+v", *p.DurabilityYears, m.LifetimeMonths)
			}
		} else if m.LifetimeMonths != nil {
			rt.Fatalf("LifetimeMonths must be nil without durability: %+v", p)
		}

		// Invariant: total cost is the whole-life identity, nil unless
		// every input is known.
		fullyKnown := p.AcquisitionCost != nil && p.DisposalCosts != nil &&
			p.OperatingExpensesPerMonth != nil && p.PotentialProfitPerMonth != nil &&
			p.DurabilityYears != nil
		if fullyKnown {
			if m.TotalCost == nil {
				rt.Fatalf("TotalCost nil with all inputs known: %+v", p)
			}
			want := *p.AcquisitionCost + *p.DisposalCosts +
				(*p.OperatingExpensesPerMonth-*p.PotentialProfitPerMonth)*(*p.DurabilityYears)*12
			if math.Abs(*m.TotalCost-want) > 1e-9*math.Max(1, math.Abs(want)) {
				rt.Fatalf("TotalCost = %v, want %v", *m.TotalCost, want)
			}
		} else if m.TotalCost != nil {
			rt.Fatalf("TotalCost must be nil with partial inputs: %+v", p)
		}

		// Invariant: cost per month spread over the lifetime reproduces
		// the total (within float rounding).
		if m.CostPerMonth != nil && m.LifetimeMonths != nil && *m.LifetimeMonths > 0 {
			spread := *m.CostPerMonth * *m.LifetimeMonths
			if m.TotalCost == nil || math.Abs(spread-*m.TotalCost) > 1e-6*math.Max(1, math.Abs(*m.TotalCost)) {
				rt.Fatalf("cost per month %v × %v = %v does not reproduce total %+v", *m.CostPerMonth, *m.LifetimeMonths, spread, m.TotalCost)
			}
		}

		// Invariant: payback is (acquisition + disposal) / profit, only
		// when the profit is strictly positive.
		if p.AcquisitionCost != nil && p.DisposalCosts != nil && p.PotentialProfitPerMonth != nil && *p.PotentialProfitPerMonth > 0 {
			if m.PaybackMonths == nil {
				rt.Fatalf("PaybackMonths nil with positive profit: %+v", p)
			}
			want := (*p.AcquisitionCost + *p.DisposalCosts) / *p.PotentialProfitPerMonth
			if math.Abs(*m.PaybackMonths-want) > 1e-9*math.Max(1, want) {
				rt.Fatalf("PaybackMonths = %v, want %v", *m.PaybackMonths, want)
			}
		} else if m.PaybackMonths != nil {
			rt.Fatalf("PaybackMonths must be nil without positive profit: %+v", p)
		}
	})
}

// TestPropComputeMetricsIdempotent: metrics are a pure function — computing
// them twice from the same payload must always agree.
func TestPropComputeMetricsIdempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		p := genPayload(rt)
		first := ComputeMetrics(p)
		second := ComputeMetrics(p)
		if diff := cmp.Diff(first, second); diff != "" {
			rt.Fatalf("ComputeMetrics not idempotent (-first +second):\n%s", diff)
		}
	})
}

// TestPropConfigRoundTripViaDB: any valid payload must survive the DB round
// trip unchanged and still validate afterwards.
func TestPropConfigRoundTripViaDB(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &WantPlugin{})
	noteID := plugintest.CreateNote(t, d, "PBT Want", &WantPlugin{})
	p := &WantPlugin{}
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		payload := genPayload(rt)
		raw := mustJSON(t, payload)
		if err := p.ValidateConfig(raw); err != nil {
			rt.Fatalf("generated payload does not validate: %v", err)
		}
		if err := saveConfig(t, d, p, noteID, payload); err != nil {
			rt.Fatalf("SaveConfig: %v", err)
		}
		loaded, err := p.LoadConfig(ctx, d.DB, 0, noteID)
		if err != nil {
			rt.Fatalf("LoadConfig: %v", err)
		}
		var got Payload
		if err := json.Unmarshal(loaded, &got); err != nil {
			rt.Fatalf("unmarshal: %v", err)
		}
		if diff := cmp.Diff(payload, got); diff != "" {
			rt.Fatalf("round trip mismatch (-want +got):\n%s", diff)
		}
		if err := p.ValidateConfig(loaded); err != nil {
			rt.Fatalf("loaded config does not validate: %v", err)
		}
	})
}

// --- Helpers ---

func mustJSON(t *testing.T, p Payload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return json.RawMessage(raw)
}

// saveConfig wraps SaveConfig in a transaction like the server does.
func saveConfig(t *testing.T, d *db.DB, p *WantPlugin, noteID int64, payload Payload) error {
	t.Helper()
	raw := mustJSON(t, payload)
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, raw); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
