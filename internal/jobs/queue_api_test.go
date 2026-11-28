package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// --- enqueue ---

func TestEnqueueUnknownDefinitionFails(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Prevents a typo'd plugin/job pair (caller updated but the plugin
	// not registered yet) being misreported as run id 0 with no error.
	_, err := m.Enqueue("nope", "nope", nil)
	if err == nil {
		t.Fatal("Enqueue for a missing definition must fail")
	}
	if !isNoRows(err) {
		t.Fatalf("Enqueue error %v does not wrap sql.ErrNoRows", err)
	}
}

func TestEnqueueSurfacesSchemaErrors(t *testing.T) {
	t.Parallel()
	d := brokenDB(t)
	m := NewManager(d, 1, 1)

	// Prevents the INSERT failure inside insertPlannedRun being
	// reported as a successful enqueue with id 0 — callers start
	// waiting on a run that does not exist.
	if _, err := m.Enqueue("test", "job", nil); err == nil {
		t.Fatal("Enqueue against a broken DB must fail")
	}
}

func TestInsertPlannedRunInheritsPriorityAndDueTime(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// The delayed-retry machinery writes next_run_at through this
	// helper; a lost due time would make workers retry instantly
	// (thundering herd) and a lost priority would put interactive
	// jobs on the slow lane.
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "high",
		Priority: PriorityHigh,
		Task:     func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	due := time.Now().Add(time.Hour).UTC()
	payload := "reload"
	runID, duplicate, err := m.insertPlannedRun(1, &payload, 0, &due)
	if err != nil {
		t.Fatalf("insertPlannedRun: %v", err)
	}
	if duplicate {
		t.Fatal("first insert must not be a duplicate")
	}

	var status string
	var prio int
	var nextRun sql.NullString
	if err := d.QueryRow(
		`SELECT status, priority, next_run_at FROM job_runs WHERE id = ?`, runID,
	).Scan(&status, &prio, &nextRun); err != nil {
		t.Fatalf("read inserted run: %v", err)
	}
	if status != StatusPlanned {
		t.Fatalf("inserted run status = %q, want planned", status)
	}
	if prio != PriorityHigh {
		t.Fatalf("inserted run priority = %d, want inherited %d", prio, PriorityHigh)
	}
	if !nextRun.Valid {
		t.Fatal("inserted run lost its next_run_at due time")
	}
	if got := parseTimePtr(&nextRun.String); got == nil || got.Sub(due) > time.Minute {
		t.Fatalf("next_run_at = %v, want ~%v", got, due)
	}
}

// --- retry ---

func TestRetryRunRejectsUnfinishedRuns(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "ok", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Every guard in one table: each status the user might retry from
	// that must NOT spawn a new run. Retry is only for terminal
	// errored/cancelled rows — anything else would duplicate active
	// work.
	for _, status := range []string{
		// A running run retried "just in case" would double-execute
		// the task; must be rejected.
		StatusRunning,
		// A planned run is already queued; retrying it must fail or
		// the queue fills with copies.
		StatusPlanned,
		// A done run has nothing to redo; the UI hides (or should
		// hide) retry for it, so the API must reject it too.
		StatusDone,
	} {
		runID, err := insertRawRun(t, d, 1, status, "")
		if err != nil {
			t.Fatalf("insert %s run: %v", status, err)
		}
		if _, err := m.RetryRun(runID); err == nil {
			t.Fatalf("RetryRun on %s must fail", status)
		}
		// Planned/running rows share the active-dedupe index key
		// (job_id=1, payload NULL): clear each case so the next
		// insert is not rejected as a duplicate.
		deleteLeak(t, d, runID)
	}
}

func TestRetryRunMissingAndBrokenDB(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Prevents a retry against a deleted run row (history trimmed,
	// race with the janitor) silently succeeding.
	if _, err := m.RetryRun(999); err == nil {
		t.Fatal("RetryRun on a missing run must fail")
	} else if !isNoRows(err) {
		t.Fatalf("RetryRun error %v does not wrap sql.ErrNoRows", err)
	}

	// Prevents schema errors surfacing as a bogus retry id.
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)
	if _, err := bm.RetryRun(1); err == nil {
		t.Fatal("RetryRun on a broken DB must fail")
	}
}

// --- cancel ---

func TestCancelRunRejectsTerminalRuns(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "ok", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Cancel is only meaningful for planned/running rows: cancelling a
	// terminal row must fail instead of bumping the stats ledger a
	// second time (double-counted cancellations would skew the
	// per-definition success rate).
	for _, status := range []string{StatusDone, StatusErrored, StatusCancelled} {
		runID, err := insertRawRun(t, d, 1, status, "")
		if err != nil {
			t.Fatalf("insert %s run: %v", status, err)
		}
		if err := m.CancelRun(runID); err == nil {
			t.Fatalf("CancelRun on %s must fail", status)
		}
		var cancelled int
		if err := d.QueryRow(`SELECT runs_cancelled FROM job_definition_stats WHERE job_id = 1`).Scan(&cancelled); err == nil && cancelled != 0 {
			t.Fatalf("cancel of terminal %s row bumped the ledger to %d", status, cancelled)
		}
	}
}

func TestCancelRunMissingAndBrokenDB(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Prevents cancelling an already-janitor'd run id rewriting history
	// or bumping ledgers.
	if err := m.CancelRun(999); err == nil {
		t.Fatal("CancelRun on a missing run must fail")
	} else if !isNoRows(err) {
		t.Fatalf("CancelRun error %v does not wrap sql.ErrNoRows", err)
	}

	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)
	if err := bm.CancelRun(1); err == nil {
		t.Fatal("CancelRun on a broken DB must fail")
	}
}

func TestCancelRunningRowClampsNegativeDuration(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "ok", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// A row claiming to have started in the future (clock skew, manual
	// import) must not produce a negative duration_ms — the UI graphs
	// it and JSON consumers reject negative ints.
	runID, err := insertRawRun(t, d, 1, StatusRunning, "")
	if err != nil {
		t.Fatalf("insert running run: %v", err)
	}
	if _, err := d.Exec(`UPDATE job_runs SET started_at = ? WHERE id = ?`,
		dbTimestamp(time.Now().Add(time.Hour)), runID); err != nil {
		t.Fatalf("skew started_at: %v", err)
	}

	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	var durationMS sql.NullInt64
	if err := d.QueryRow(`SELECT duration_ms FROM job_runs WHERE id = ?`, runID).Scan(&durationMS); err != nil {
		t.Fatal(err)
	}
	if !durationMS.Valid || durationMS.Int64 != 0 {
		t.Fatalf("duration_ms = %+v, want clamped 0", durationMS)
	}
	var errText string
	if err := d.QueryRow(`SELECT COALESCE(error,'') FROM job_runs WHERE id = ?`, runID).Scan(&errText); err != nil {
		t.Fatal(err)
	}
	// Message keeps operator context for runs cancelled with no task
	// context attached (manual rows).
	if !strings.Contains(errText, "Cancelled by user") {
		t.Fatalf("error note = %q, want the user-cancel note", errText)
	}
	// The exactly-once ledger must record the cancellation.
	var cancelled int
	if err := d.QueryRow(`SELECT runs_cancelled FROM job_definition_stats WHERE job_id = 1`).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 {
		t.Fatalf("runs_cancelled = %d, want 1", cancelled)
	}
}

// --- executing runs whose task was never registered ---

func TestExecuteWithoutRegisteredTaskErrorsRun(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d) // not started: called synchronously

	// A definition row from an older binary (task code removed) must
	// finalize its claimed runs as errored instead of wedging them in
	// "running" until the watchdog acts — and the metadata for such an
	// uncached definition comes from the DB fallback path.
	res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('ghost', 'orphan', '')`)
	if err != nil {
		t.Fatalf("insert ghost definition: %v", err)
	}
	defID, _ := res.LastInsertId()
	payload := `{"id":1}`
	runID, duplicate, err := m.insertPlannedRun(defID, &payload, 0, nil)
	if err != nil || duplicate {
		t.Fatalf("insertPlannedRun: duplicate=%v err=%v", duplicate, err)
	}

	errored := make(chan RunEvent, 1)
	m.SetObserver(func(evt RunEvent) {
		if evt.Type == "errored" {
			errored <- evt
		}
	})

	claim, ok := m.dequeue(false, 1)
	if !ok {
		t.Fatal("dequeue did not claim the orphan run")
	}
	if claim.runID != runID {
		t.Fatalf("claimed run %d, want %d", claim.runID, runID)
	}
	m.execute(claim, false)

	select {
	case evt := <-errored:
		if evt.RunID != runID || evt.PluginID != "ghost" || evt.JobName != "orphan" {
			t.Fatalf("errored event = %+v", evt)
		}
	default:
	}

	status, errText := fetchRun(t, d, runID)
	if status != StatusErrored {
		t.Fatalf("orphan run status = %q, want errored", status)
	}
	if !strings.Contains(errText, "No task registered") {
		t.Fatalf("error note = %q, want the unregistered-task note", errText)
	}
	// The lost definition must still count in the ledger so its rows
	// are visible in the stats view.
	var erroredCount int
	if err := d.QueryRow(`SELECT runs_errored FROM job_definition_stats WHERE job_id = ?`, defID).Scan(&erroredCount); err != nil {
		t.Fatal(err)
	}
	if erroredCount != 1 {
		t.Fatalf("runs_errored = %d, want 1", erroredCount)
	}
}

// --- listing ---

func TestListRunsReturnsFullRunShape(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	done := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "reporter",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			rt.Progress(3, 4, "step 3")
			close(done)
			return "ok", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "reporter", []byte(`{"src":"rss"}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Event-driven wait: the task signals when it reports, so the scan
	// below runs against a fully-populated row. No sleeps.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("task never reported progress")
	}
	if !waitUntil(5*time.Second, func() bool { return statusDone(t, d, runID) }) {
		t.Fatalf("run did not finish")
	}

	// A delayed retry pending in the queue exercises the NextRunAt and
	// pending-count fields.
	//
	// NOTE: definition id 1 is the builtin _system janitor (registered
	// during Start), not the reporter — look the real id up.
	reporterDefID := mustDefinitionID(t, d, "test", "reporter")
	due := time.Now().Add(time.Minute).UTC()
	_, _, err = m.insertPlannedRun(reporterDefID, nil, 1, &due)
	if err != nil {
		t.Fatalf("insert delayed run: %v", err)
	}

	resp, err := m.ListRuns(0) // limit<=0 must default sensibly
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if resp.PendingCount != 1 {
		t.Fatalf("PendingCount = %d, want 1 (the delayed planned run)", resp.PendingCount)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(resp.Runs))
	}

	// Rows come back newest-first: the delayed run leads.
	delayed := resp.Runs[0]
	finished := resp.Runs[1]
	if delayed.NextRunAt == nil {
		t.Fatal("delayed run lost its NextRunAt")
	}
	if delayed.RetryCount != 1 {
		t.Fatalf("delayed retry_count = %d, want 1", delayed.RetryCount)
	}

	if finished.ID != runID {
		t.Fatalf("finished run id = %d, want %d", finished.ID, runID)
	}
	if finished.Status != StatusDone || finished.Result == nil || *finished.Result != "ok" {
		t.Fatalf("finished run = %+v", finished)
	}
	if !strings.Contains(string(finished.Payload), `"src":"rss"`) {
		t.Fatalf("payload lost in listing: %s", finished.Payload)
	}
	if finished.StartedAt == nil || finished.FinishedAt == nil {
		t.Fatalf("timestamps missing: %+v", finished)
	}
	if finished.DurationMS == nil || *finished.DurationMS < 0 {
		t.Fatalf("duration missing: %+v", finished)
	}
	if finished.ProgressDone == nil || *finished.ProgressDone != 3 ||
		finished.ProgressTotal == nil || *finished.ProgressTotal != 4 ||
		finished.ProgressMessage == nil || *finished.ProgressMessage != "step 3" {
		t.Fatalf("progress fields = %+v", finished)
	}
	if finished.PluginID != "test" || finished.JobName != "reporter" {
		t.Fatalf("run identity = %s/%s", finished.PluginID, finished.JobName)
	}
	if finished.WorkerID < 0 {
		t.Fatalf("worker id = %d", finished.WorkerID)
	}
	if finished.CreatedAt.IsZero() {
		t.Fatal("CreatedAt zero")
	}
}

func TestListRunsHonorsLimitAndBrokenDB(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "ok", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := insertRawRun(t, d, 1, StatusDone, ""); err != nil {
			t.Fatalf("insert run %d: %v", i, err)
		}
	}

	// The UI paginates by limit: a limit of 3 must return exactly 3
	// rows, not the full history (which would defeat the cap for busy
	// queues).
	resp, err := m.ListRuns(3)
	if err != nil {
		t.Fatalf("ListRuns(3): %v", err)
	}
	if len(resp.Runs) != 3 {
		t.Fatalf("runs = %d, want 3 (limit cap)", len(resp.Runs))
	}

	// Schema errors must surface as errors, not as an empty list (an
	// empty list renders as "no jobs", hiding the breakage).
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)
	if _, err := bm.ListRuns(10); err == nil {
		t.Fatal("ListRuns on a broken DB must fail")
	}
}

// --- helpers ---

// mustDefinitionID resolves a definition row id by plugin/name; test
// setup ordering (builtins first) makes hardcoded ids brittle.
func mustDefinitionID(t *testing.T, d *sql.DB, pluginID, name string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(
		`SELECT id FROM job_definitions WHERE plugin_id = ? AND name = ?`, pluginID, name,
	).Scan(&id); err != nil {
		t.Fatalf("resolve definition %s/%s: %v", pluginID, name, err)
	}
	return id
}

// isNoRows asserts the package's wrapped not-found errors via the
// standard errors chain — the package wraps with fmt %w everywhere.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// deleteLeak removes a run row created for a negative guard case so the
// unique active-run index stays clean for the next case.
func deleteLeak(t *testing.T, d *sql.DB, runID int64) {
	t.Helper()
	if _, err := d.Exec(`DELETE FROM job_runs WHERE id = ?`, runID); err != nil {
		t.Fatalf("delete run %d: %v", runID, err)
	}
}
