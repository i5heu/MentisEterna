package jobs

import (
	"context"
	"testing"
	"time"
)

// TestQueueToleratesUnknownStatusRows covers pre-v2 / foreign rows in
// job_runs: status strings the v2 queue never writes itself (v1 used
// its own vocabulary; chunked uploads write 'finished'; manual DB
// edits can write anything). No such row may wedge the queue: it must
// pass through listings untouched, be rejected by cancel/retry, stay
// invisible to the workers, and break no stats query.
func TestQueueToleratesUnknownStatusRows(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Registered definition so recovery does not orphan-mark the rows
	// (that path is covered elsewhere); the statuses under test are the
	// variable.
	res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('legacy', 'vintage', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	defID, _ := res.LastInsertId()

	// status → why it must be tolerated.
	rows := map[string]int64{}
	for status, why := range map[string]string{
		"completed": "v1's done — the frontend still renders it",
		"failed":    "v1's errored — the frontend still offers retry for it",
		"pending":   "v1's planned — must not be claimed by v2 dequeue",
		"finished":  "written by the chunked-upload subsystem, shares the column vocabulary",
		"":          "empty string from a manual edit; NOT NULL only bans NULL",
	} {
		id, err := insertRawRun(t, d, defID, status, "")
		if err != nil {
			t.Fatalf("insert run with status %q (%s): %v", status, why, err)
		}
		rows[status] = id
	}

	startManager(t, m)

	// Grace window: workers poll at 20 ms here; 250 ms is ample for a
	// wrongly-claimed row to surface. The assertion is that nothing
	// happens — a negative check needs the window, not a condition.
	time.Sleep(250 * time.Millisecond)

	for status, runID := range rows {
		// Never claimed, never recovered, never rewritten.
		gotStatus, _ := fetchRun(t, d, runID)
		if gotStatus != status {
			t.Errorf("row with status %q was rewritten to %q — the queue must leave foreign statuses alone", status, gotStatus)
		}
		// Cancel rejects it instead of corrupting it.
		if err := m.CancelRun(runID); err == nil {
			t.Errorf("cancel accepted a %q run", status)
		}
		// Retry rejects it too (only errored/cancelled are retryable).
		if _, err := m.RetryRun(runID); err == nil {
			t.Errorf("retry accepted a %q run", status)
		}
		// ListRuns passes the status through verbatim.
		resp, err := m.ListRuns(50)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		var found *Run
		for i := range resp.Runs {
			if resp.Runs[i].ID == runID {
				found = &resp.Runs[i]
			}
		}
		if found == nil {
			t.Fatalf("run %d (%q) missing from listing", runID, status)
		}
		if found.Status != status {
			t.Errorf("listing rewrote status %q to %q", status, found.Status)
		}
	}

	// Stats over a database full of foreign statuses: no 500s, the
	// queue counters stay clean (foreign statuses are not planned), and
	// the definition's ledger is untouched.
	stats, err := m.Stats()
	if err != nil {
		t.Fatalf("stats over legacy rows: %v", err)
	}
	if stats.Queue.Planned != 0 || stats.Queue.Running != 0 {
		t.Fatalf("foreign rows leaked into queue counters: %+v", stats.Queue)
	}
	if stats.Last24h.Done != 0 || stats.Last24h.Errored != 0 {
		t.Fatalf("foreign rows leaked into the 24h window: %+v", stats.Last24h)
	}
	for _, def := range stats.Definitions {
		if def.ID == defID && (def.RunsTotal != 0 || def.Running != 0 || def.Queued != 0) {
			t.Fatalf("foreign rows leaked into definition counters: %+v", def)
		}
	}
}

// TestNullPayloadRowsRunWithoutDedupeCollapse covers the cron row shape
// at the queue level: NULL payload. SQLite unique indexes treat NULLs
// as distinct, so multiple active NULL-payload rows are legal (cron
// ticks and crash recovery rely on it) and Enqueue(nil) does NOT
// dedupe — every enqueue inserts, and the scheduler prevents cron
// pile-up with its own active-run check instead. Workers must execute
// NULL-payload runs with an empty (COALESCE'd), non-nil payload.
func TestNullPayloadRowsRunWithoutDedupeCollapse(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	payloads := make(chan []byte, 4)
	if err := m.UpsertDefinitions("legacy", []CronJob{{
		Name: "cronish",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			payloads <- rt.Payload
			return "ran", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	defID := mustDefinitionID(t, d, "legacy", "cronish")

	// Two seeded NULL-payload rows — the crash/cron shape the index
	// explicitly allows.
	for i := range 2 {
		if _, err := d.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, 'planned')`, defID); err != nil {
			t.Fatalf("seed NULL-payload run %d: %v", i, err)
		}
	}

	// Enqueue(nil) inserts a third row: the dedupe is index-enforced and
	// NULLs never violate the partial unique index (the fallback lookup
	// only runs on a rejected INSERT). This pins deliberate behavior —
	// "fixing" it would silently swallow manual retries of NULL-payload
	// runs whenever a cron tick's run happens to be active.
	thirdID, err := m.Enqueue("legacy", "cronish", nil)
	if err != nil {
		t.Fatalf("enqueue nil payload: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id = ?`, defID).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 3 {
		t.Fatalf("enqueue(nil) must insert (NULLs are distinct), got %d rows", count)
	}
	var maxID int64
	if err := d.QueryRow(`SELECT MAX(id) FROM job_runs WHERE job_id = ?`, defID).Scan(&maxID); err != nil {
		t.Fatalf("max id: %v", err)
	}
	if thirdID != maxID {
		t.Fatalf("enqueue(nil) returned run %d, want the freshly inserted run %d", thirdID, maxID)
	}

	// All three NULL-payload rows execute: the index allows them,
	// dequeue COALESCEs the NULL into an empty payload string.
	startManager(t, m)
	if !waitUntil(2*time.Second, func() bool {
		counts := runCountByStatus(t, d, defID)
		return counts[StatusDone] == 3
	}) {
		t.Fatalf("NULL-payload runs did not all execute: %v", runCountByStatus(t, d, defID))
	}
	for range 3 {
		select {
		case p := <-payloads:
			// COALESCE(payload, '') → empty but NOT nil: a nil payload
			// would crash tasks that index or marshal it.
			if p == nil || len(p) != 0 {
				t.Fatalf("NULL-payload run delivered payload %#v, want empty non-nil", p)
			}
		default:
			t.Fatal("NULL-payload run never reached the task")
		}
	}

	var total, done int
	if err := d.QueryRow(
		`SELECT runs_total, runs_done FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &done); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 3 || done != 3 {
		t.Fatalf("ledger after NULL-payload runs = %d/%d, want 3/3", total, done)
	}
}

// TestNextRunAtGateToleratesLegacyFormats covers the delayed-retry due
// gate against pre-v2 and malformed next_run_at values. v1 rows never
// carried the column (NULL → immediately eligible), old SQLite
// datetime strings ('2020-01-01 00:00:00', space separator, no Z)
// string-sort before now and must run, while garbage and future
// timestamps must hold the row back — without breaking ListRuns.
func TestNextRunAtGateToleratesLegacyFormats(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("legacy", []CronJob{{
		Name: "duegate",
		Task: func(context.Context, *RunContext) (string, error) { return "ran", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	defID := mustDefinitionID(t, d, "legacy", "duegate")

	// next_run_at → why the gate must handle it.
	seed := map[string]string{
		"v1 row (column absent → NULL)": "",
		"old SQLite datetime format":    "2020-01-01 00:00:00",
		"garbage timestamp":             "soon",
		"canonical far future":          "9999-12-31T00:00:00.000Z",
	}
	var nullID int64
	if _, err := d.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, 'planned')`, defID); err != nil {
		t.Fatalf("seed NULL row: %v", err)
	}
	if err := d.QueryRow(`SELECT last_insert_rowid()`).Scan(&nullID); err != nil {
		t.Fatalf("null row id: %v", err)
	}
	ids := map[string]int64{"v1 row (column absent → NULL)": nullID}
	for label, due := range seed {
		if due == "" {
			continue
		}
		res, err := d.Exec(`INSERT INTO job_runs (job_id, status, next_run_at) VALUES (?, 'planned', ?)`, defID, due)
		if err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
		id, _ := res.LastInsertId()
		ids[label] = id
	}

	startManager(t, m)

	// NULL and the old-format row must both execute.
	if !waitUntil(2*time.Second, func() bool {
		a, _ := fetchRun(t, d, ids["v1 row (column absent → NULL)"])
		b, _ := fetchRun(t, d, ids["old SQLite datetime format"])
		return a == StatusDone && b == StatusDone
	}) {
		a, _ := fetchRun(t, d, ids["v1 row (column absent → NULL)"])
		b, _ := fetchRun(t, d, ids["old SQLite datetime format"])
		t.Fatalf("legacy next_run_at rows did not run: NULL=%q old-format=%q", a, b)
	}

	// Garbage and far-future rows must hold (string comparison ranks
	// both after "now").
	for _, label := range []string{"garbage timestamp", "canonical far future"} {
		if status, _ := fetchRun(t, d, ids[label]); status != StatusPlanned {
			t.Fatalf("%s row was claimed despite a future due time (status %q)", label, status)
		}
	}

	// ListRuns survives every variant. Note the driver quirk: a
	// DATETIME column holding garbage ('soon') comes back from
	// go-sqlite3 as Go's zero time ("0001-01-01T00:00:00Z"), which
	// parseTimePtr happily parses — garbage surfaces as a zero
	// NextRunAt, never as an error. The old SQLite datetime format
	// round-trips into a parsed value; the canonical future parses too.
	resp, err := m.ListRuns(50)
	if err != nil {
		t.Fatalf("list runs over legacy due times: %v", err)
	}
	byID := map[int64]*Run{}
	for i := range resp.Runs {
		byID[resp.Runs[i].ID] = &resp.Runs[i]
	}
	if r := byID[ids["old SQLite datetime format"]]; r == nil || r.NextRunAt == nil || !r.NextRunAt.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("old-format due time must parse to 2020-01-01T00:00:00Z, got %+v", r)
	}
	if r := byID[ids["garbage timestamp"]]; r == nil || r.NextRunAt == nil || !r.NextRunAt.IsZero() {
		t.Fatalf("garbage due time must surface as the zero time (driver normalization), got %+v", r)
	}
	if r := byID[ids["canonical far future"]]; r == nil || r.NextRunAt == nil {
		t.Fatalf("canonical future due time must parse, got %+v", r)
	}
	var statsResp *StatsResponse
	if statsResp, err = m.Stats(); err != nil {
		t.Fatalf("stats over legacy due times: %v", err)
	}
	// Only the two still-planned rows count as queue depth.
	if statsResp.Queue.Planned != 2 {
		t.Fatalf("queue.planned = %d, want 2 (garbage + future rows)", statsResp.Queue.Planned)
	}
}
