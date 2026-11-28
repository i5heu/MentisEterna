package gridfinity

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"github.com/i5heu/MentisEterna/pkg/notetype/want"
	"pgregory.net/rapid"
)

func TestGridfinityPluginHarness(t *testing.T) {
	plugintest.Run(t, &GridfinityPlugin{}, plugintest.TestData{})
}

// TestSingletonManifest pins the Singleton flag: the dashboard aggregates
// every want globally, so the server's singletonCheck must reject a second
// gridfinity note (same mechanism as llm_inferencer).
func TestSingletonManifest(t *testing.T) {
	t.Parallel()
	m := (&GridfinityPlugin{}).Manifest()
	if !m.Singleton {
		t.Error("gridfinity manifest must declare Singleton: true")
	}
	if m.HasConfig {
		t.Error("gridfinity has no config — HasConfig must be false")
	}
	if !m.HasView {
		t.Error("gridfinity is view-only — HasView must be true")
	}
}

// TestItemJSONFlattensPayload pins the wire contract the frontend relies on:
// the embedded want.Payload must inline its fields at the top level of each
// item (no nested object, no empty-name key).
func TestItemJSONFlattensPayload(t *testing.T) {
	t.Parallel()
	cost := 42.5
	it := Item{
		NoteID:  7,
		Title:   "Widget",
		Payload: want.Payload{AcquisitionCost: &cost},
	}
	raw, err := json.Marshal(it)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	if v, ok := m["acquisition_cost"]; !ok || v != 42.5 {
		t.Errorf("acquisition_cost must be a top-level field, got: %s", raw)
	}
	for _, bad := range []string{"", "Payload", "payload"} {
		if _, ok := m[bad]; ok {
			t.Errorf("item JSON must not contain a %q key: %s", bad, raw)
		}
	}
	if _, ok := m["metrics"]; !ok {
		t.Errorf("item JSON must contain metrics: %s", raw)
	}
}

func fp(v float64) *float64 { return &v }

// TestBuildViewAggregatesWants pins the whole dashboard wiring: only want
// notes are included, cost fields and metrics are attached, urge-log stats
// are aggregated per want, and the totals summarize the grid.
func TestBuildViewAggregatesWants(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &GridfinityPlugin{})
	// The dashboard reads the want plugin's tables — its schema must exist.
	if err := (&want.WantPlugin{}).InitSchema(d.DB); err != nil {
		t.Fatalf("want InitSchema: %v", err)
	}
	wp := &want.WantPlugin{}
	ctx := context.Background()

	printerID := plugintest.CreateNote(t, d, "3D Printer", wp)
	laserID := plugintest.CreateNote(t, d, "Laser Cutter", wp)
	secretID := plugintest.CreateNote(t, d, "Secret Sauce", wp)

	// Non-want notes must not appear in the dashboard.
	if _, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('Not a want', 'standard')`); err != nil {
		t.Fatalf("insert standard note: %v", err)
	}

	// 3D Printer: full economics + urge log (2 presses today, 1 six weeks ago).
	printerPayload := want.Payload{
		AcquisitionCost:           fp(1000),
		OperatingExpensesPerMonth: fp(10),
		DisposalCosts:             fp(50),
		PotentialProfitPerMonth:   fp(30),
		DurabilityYears:           fp(5),
		SpaceRequirementsM3:       fp(0.25),
	}
	if err := saveWantConfig(t, d, wp, printerID, printerPayload); err != nil {
		t.Fatalf("save printer config: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := wp.HandleAction(ctx, d.DB, 0, printerID, "log_want", nil); err != nil {
			t.Fatalf("log_want %d: %v", i, err)
		}
	}
	old := time.Now().Add(-42 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO ct_want_log (note_id, created_at) VALUES (?, ?)`, printerID, old); err != nil {
		t.Fatalf("insert old log entry: %v", err)
	}
	// A body update pins last_edited_at (latest updates row wins).
	if _, err := d.Exec(`INSERT INTO updates (note_id, body, created_at) VALUES (?, 'why I want it', '2026-01-05T09:15:00.000Z')`, printerID); err != nil {
		t.Fatalf("insert update: %v", err)
	}

	// Laser Cutter: no config, no log — the fresh-want state.
	// Secret Sauce: highly private with a lone acquisition cost.
	if _, err := d.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, secretID); err != nil {
		t.Fatalf("mark private: %v", err)
	}
	if err := saveWantConfig(t, d, wp, secretID, want.Payload{AcquisitionCost: fp(500)}); err != nil {
		t.Fatalf("save secret config: %v", err)
	}

	dashID := plugintest.CreateNote(t, d, "The Gridfinity", &GridfinityPlugin{})
	res, err := (&GridfinityPlugin{}).BuildView(ctx, d.DB, 0, dashID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view, ok := res.(*View)
	if !ok {
		t.Fatalf("expected *View, got %T", res)
	}

	// Exactly the three wants, sorted by title.
	if len(view.Items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(view.Items), view.Items)
	}
	if view.Items[0].Title != "3D Printer" || view.Items[1].Title != "Laser Cutter" || view.Items[2].Title != "Secret Sauce" {
		t.Fatalf("items must be title-sorted, got %q / %q / %q", view.Items[0].Title, view.Items[1].Title, view.Items[2].Title)
	}

	printer := view.Items[0]
	if printer.NoteID != printerID {
		t.Fatalf("printer item note id = %d, want %d", printer.NoteID, printerID)
	}
	// Metrics come from want.ComputeMetrics — same numbers as the want tests.
	wantMetrics := want.ComputeMetrics(printerPayload)
	if diff := cmp.Diff(wantMetrics, printer.Metrics); diff != "" {
		t.Errorf("printer metrics mismatch (-want +got):\n%s", diff)
	}
	// Urge-log stats: 3 presses, 2 distinct days (today ×2 + 42d ago),
	// 2 within the last 30 days, 2 today.
	if printer.Total != 3 || printer.WantedDays != 2 || printer.Last30Days != 2 || printer.TodayCount != 2 {
		t.Errorf("printer log stats = %+v", printer.LogStats)
	}
	if printer.LastWantedAt == "" {
		t.Error("printer LastWantedAt must be set")
	}
	if printer.HighPrivate {
		t.Error("printer must not be flagged high_private")
	}
	if printer.LastEditedAt == "" {
		t.Error("printer LastEditedAt must be set")
	}
	if printer.SpaceRequirementsM3 == nil || *printer.SpaceRequirementsM3 != 0.25 {
		t.Errorf("printer space = %+v, want 0.25", printer.SpaceRequirementsM3)
	}

	laser := view.Items[1]
	if laser.NoteID != laserID {
		t.Errorf("laser item note id = %d, want %d", laser.NoteID, laserID)
	}
	if laser.Total != 0 || laser.WantedDays != 0 || laser.TodayCount != 0 {
		t.Errorf("laser must have no log entries: %+v", laser.LogStats)
	}
	// A want without a config row is all-unknown — never zero-filled.
	if diff := cmp.Diff(want.Payload{}, laser.Payload); diff != "" {
		t.Errorf("laser payload mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(want.Metrics{}, laser.Metrics); diff != "" {
		t.Errorf("laser metrics mismatch (-want +got):\n%s", diff)
	}

	secret := view.Items[2]
	if !secret.HighPrivate {
		t.Error("secret want must carry the high_private flag")
	}
	if secret.AcquisitionCost == nil || *secret.AcquisitionCost != 500 {
		t.Errorf("secret acquisition = %+v, want 500", secret.AcquisitionCost)
	}

	// Totals summarize the grid over known values only.
	wantTotals := Totals{
		Wants:          3,
		WantedToday:    1,
		WantedLast30:   1,
		NeverWanted:    2,
		SumAcquisition: fp(1500),
		SumMonthlyNet:  fp(-20),
		SumTotalCost:   fp(-150),
		SumSpaceM3:     fp(0.25),
	}
	if diff := cmp.Diff(wantTotals, view.Totals); diff != "" {
		t.Errorf("totals mismatch (-want +got):\n%s", diff)
	}
}

// TestBuildViewEmptyGrid pins the empty dashboard: zero wants must produce an
// empty item list and all-unknown totals (nil sums, not zeros).
func TestBuildViewEmptyGrid(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &GridfinityPlugin{})
	if err := (&want.WantPlugin{}).InitSchema(d.DB); err != nil {
		t.Fatalf("want InitSchema: %v", err)
	}
	dashID := plugintest.CreateNote(t, d, "Empty Grid", &GridfinityPlugin{})
	res, err := (&GridfinityPlugin{}).BuildView(context.Background(), d.DB, 0, dashID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view := res.(*View)
	if len(view.Items) != 0 {
		t.Errorf("expected no items, got %d", len(view.Items))
	}
	if diff := cmp.Diff(Totals{}, view.Totals); diff != "" {
		t.Errorf("empty totals mismatch (-want +got):\n%s", diff)
	}
}

// TestBuildViewExcludesNonWantLogRows pins the type filter on the log pass:
// orphaned log rows under a note that is no longer a want must not leak into
// the dashboard stats.
func TestBuildViewExcludesNonWantLogRows(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &GridfinityPlugin{})
	if err := (&want.WantPlugin{}).InitSchema(d.DB); err != nil {
		t.Fatalf("want InitSchema: %v", err)
	}
	wp := &want.WantPlugin{}
	ctx := context.Background()

	// A want that later became a standard note leaves its log rows behind.
	strayID := plugintest.CreateNote(t, d, "Ex Want", wp)
	if _, err := wp.HandleAction(ctx, d.DB, 0, strayID, "log_want", nil); err != nil {
		t.Fatalf("log_want: %v", err)
	}
	if _, err := d.Exec(`UPDATE notes SET type = 'standard' WHERE id = ?`, strayID); err != nil {
		t.Fatalf("retype note: %v", err)
	}

	dashID := plugintest.CreateNote(t, d, "The Gridfinity", &GridfinityPlugin{})
	res, err := (&GridfinityPlugin{}).BuildView(ctx, d.DB, 0, dashID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view := res.(*View)
	if len(view.Items) != 0 {
		t.Errorf("retyped note must not appear in the dashboard, got %+v", view.Items)
	}
}

// TestPropGridfinityTotalsMatchItems: for any set of wants, the reported
// totals must be exactly the independent recomputation over the returned
// items, and every want note must appear exactly once with its own payload.
func TestPropGridfinityTotalsMatchItems(t *testing.T) {
	t.Parallel()
	d := plugintest.DB(t, &GridfinityPlugin{})
	if err := (&want.WantPlugin{}).InitSchema(d.DB); err != nil {
		t.Fatalf("want InitSchema: %v", err)
	}
	wp := &want.WantPlugin{}
	gp := &GridfinityPlugin{}
	ctx := context.Background()
	dashID := plugintest.CreateNote(t, d, "PBT Grid", gp)

	rapid.Check(t, func(rt *rapid.T) {
		// Reset the grid: cascades wipe ct_want_config / ct_want_log too.
		if _, err := d.Exec(`DELETE FROM notes WHERE id != ?`, dashID); err != nil {
			rt.Fatalf("reset: %v", err)
		}

		n := rapid.IntRange(0, 6).Draw(rt, "wants")
		ids := make([]int64, 0, n)
		payloads := make(map[int64]want.Payload, n)
		for i := 0; i < n; i++ {
			res, err := d.Exec(`INSERT INTO notes (title, type) VALUES (?, 'want')`, fmt.Sprintf("want %d", i))
			if err != nil {
				rt.Fatalf("insert want: %v", err)
			}
			id, _ := res.LastInsertId()
			payload := genPayload(rt)
			if err := saveWantConfig(t, d, wp, id, payload); err != nil {
				rt.Fatalf("save config: %v", err)
			}
			presses := rapid.IntRange(0, 3).Draw(rt, "presses")
			for j := 0; j < presses; j++ {
				if _, err := wp.HandleAction(ctx, d.DB, 0, id, "log_want", nil); err != nil {
					rt.Fatalf("log_want: %v", err)
				}
			}
			ids = append(ids, id)
			payloads[id] = payload
		}

		res, err := gp.BuildView(ctx, d.DB, 0, dashID)
		if err != nil {
			rt.Fatalf("BuildView: %v", err)
		}
		view := res.(*View)

		// Invariant: every want appears exactly once.
		if len(view.Items) != n {
			rt.Fatalf("expected %d items, got %d", n, len(view.Items))
		}
		seen := make(map[int64]bool, n)
		for _, it := range view.Items {
			if seen[it.NoteID] {
				rt.Fatalf("note %d appears twice", it.NoteID)
			}
			seen[it.NoteID] = true
			// Invariant: the item carries exactly the saved payload.
			if diff := cmp.Diff(payloads[it.NoteID], it.Payload); diff != "" {
				rt.Fatalf("payload mismatch for note %d (-want +got):\n%s", it.NoteID, diff)
			}
		}
		for _, id := range ids {
			if !seen[id] {
				rt.Fatalf("want note %d missing from the dashboard", id)
			}
		}

		// Invariant: totals are the exact recomputation over items.
		want := Totals{Wants: n}
		var sumAcq, sumNet, sumTotal, sumSpace float64
		var nAcq, nNet, nTotal, nSpace int
		for _, it := range view.Items {
			if it.TodayCount > 0 {
				want.WantedToday++
			}
			if it.Last30Days > 0 {
				want.WantedLast30++
			}
			if it.Total == 0 {
				want.NeverWanted++
			}
			if it.AcquisitionCost != nil {
				sumAcq += *it.AcquisitionCost
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
			if it.SpaceRequirementsM3 != nil {
				sumSpace += *it.SpaceRequirementsM3
				nSpace++
			}
		}
		if nAcq > 0 {
			want.SumAcquisition = &sumAcq
		}
		if nNet > 0 {
			want.SumMonthlyNet = &sumNet
		}
		if nTotal > 0 {
			want.SumTotalCost = &sumTotal
		}
		if nSpace > 0 {
			want.SumSpaceM3 = &sumSpace
		}
		if diff := cmp.Diff(want, view.Totals); diff != "" {
			rt.Fatalf("totals mismatch (-want +got):\n%s", diff)
		}
	})
}

// --- Helpers ---

// genOptionalFloat generates a finite non-negative float that may be absent
// (nil = unknown), mirroring the want payload contract.
func genOptionalFloat(rt *rapid.T, name string) *float64 {
	if !rapid.Bool().Draw(rt, "has_"+name) {
		return nil
	}
	v := rapid.Float64Range(0, 1e6).Draw(rt, name)
	return &v
}

func genPayload(rt *rapid.T) want.Payload {
	return want.Payload{
		AcquisitionCost:           genOptionalFloat(rt, "acquisition_cost"),
		OperatingExpensesPerMonth: genOptionalFloat(rt, "operating_expenses_per_month"),
		DisposalCosts:             genOptionalFloat(rt, "disposal_costs"),
		PotentialProfitPerMonth:   genOptionalFloat(rt, "potential_profit_per_month"),
		DurabilityYears:           genOptionalFloat(rt, "durability_years"),
		SpaceRequirementsM3:       genOptionalFloat(rt, "space_requirements_m3"),
	}
}

// saveWantConfig persists a want payload in a transaction like the server does.
func saveWantConfig(t *testing.T, d *db.DB, wp *want.WantPlugin, noteID int64, payload want.Payload) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if err := wp.SaveConfig(context.Background(), tx, 0, noteID, raw); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
