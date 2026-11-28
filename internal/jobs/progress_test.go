package jobs

import (
	"testing"
	"time"
)

// seedRunningRun inserts a definition and a run row in the given status
// and returns (defID, runID) — progress tests need rows the manager's
// Update can target without starting any worker.
func seedRunningRun(t *testing.T, m *Manager, status string) (int64, int64) {
	t.Helper()
	res, err := m.db.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('t', 'p', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	defID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	res, err = m.db.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, ?)`, defID, status)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return defID, runID
}

func TestProgressClampsValues(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	_, runID := seedRunningRun(t, m, StatusRunning)

	events := make(chan RunEvent, 16)
	m.SetObserver(func(evt RunEvent) { events <- evt })

	// Gates wide open: this test only cares about value clamping.
	m.progressMinInterval = 0
	m.progressHeartbeat = 0

	prog := m.makeProgress(runID, jobDefMeta{ID: 1, PluginID: "t", Name: "p"})

	// Prevents over-reporting (done > total, e.g. a task counting
	// batches while also counting items) producing progress bars over
	// 100% in the UI.
	prog(5, 3, "over")
	// Prevents negative progress (an item dequeued mid-run, a task
	// computing done by subtraction) surfacing as -33% in the UI.
	prog(-2, -2, "clamp")
	// Prevents empty messages rendering as blank rows in the job
	// queue: the event must carry a synthesized display string.
	prog(0, 0, "")

	var done, total int
	var msg string
	if err := d.QueryRow(
		`SELECT progress_done, progress_total, COALESCE(progress_message,'')
		 FROM job_runs WHERE id = ?`, runID,
	).Scan(&done, &total, &msg); err != nil {
		t.Fatal(err)
	}
	if done != 0 || total != 0 {
		t.Fatalf("stored progress = %d/%d, want clamped 0/0", done, total)
	}

	// All three emissions are observer-visible (clamping never
	// suppresses).
	if len(events) != 3 {
		t.Fatalf("observer saw %d events, want 3", len(events))
	}
	evt := <-events
	if evt.ProgressDone == nil || *evt.ProgressDone != 3 || evt.ProgressTotal == nil || *evt.ProgressTotal != 3 {
		t.Fatalf("first event = %+v, want clamped 3/3", evt)
	}
	evt = <-events
	if evt.ProgressDone == nil || *evt.ProgressDone != 0 || evt.ProgressTotal == nil || *evt.ProgressTotal != 0 {
		t.Fatalf("second event = %+v, want clamped 0/0", evt)
	}
	if evt.ProgressMessage == nil || *evt.ProgressMessage != "clamp" {
		t.Fatalf("second event message = %v, want %q kept verbatim", evt.ProgressMessage, "clamp")
	}
	evt = <-events
	// The synthesized fallback for empty messages (the UI would show
	// a bare bar otherwise).
	if evt.ProgressMessage == nil || *evt.ProgressMessage != "0/0" {
		t.Fatalf("third event message = %v, want the 0/0 fallback", evt.ProgressMessage)
	}
}

func TestProgressThrottleRatesAndFavorsChanges(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	_, runID := seedRunningRun(t, m, StatusRunning)

	events := make(chan RunEvent, 16)
	m.SetObserver(func(evt RunEvent) { events <- evt })

	// Rate cap longer than the whole test, heartbeat too: the only
	// paths through the gate are finished and message changes.
	m.progressMinInterval = time.Hour
	m.progressHeartbeat = time.Hour

	prog := m.makeProgress(runID, jobDefMeta{ID: 1, PluginID: "t", Name: "p"})
	prog(2, 5, "step")  // first emission always passes
	prog(3, 5, "step")  // same msg, unfinished, no elapsed gates → dropped
	prog(4, 5, "stage") // message change → always emitted
	prog(5, 5, "stage") // finished → always emitted
	prog(5, 5, "stage") // finished again → still emitted (completion is never dropped)

	if len(events) != 4 {
		t.Fatalf("observer saw %d events, want 4 (chatty repeats must drop)", len(events))
	}

	// The dropped call must never reach the DB either — the run row is
	// what ListRuns (the job queue UI) shows after reloads.
	var done int
	if err := d.QueryRow(`SELECT progress_done FROM job_runs WHERE id = ?`, runID).Scan(&done); err != nil {
		t.Fatal(err)
	}
	// The last WRITE belongs to the "finished" call (5); the dropped
	// call was the second (3) — order of writes: 2 → 4 → 5.
	if done != 5 {
		t.Fatalf("stored progress_done = %d, want 5 (drops must not write)", done)
	}
}

func TestProgressDroppedOnceRunLeavesRunning(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	_, runID := seedRunningRun(t, m, StatusDone) // finalized, not running

	events := make(chan RunEvent, 8)
	m.SetObserver(func(evt RunEvent) { events <- evt })
	m.progressMinInterval = 0
	m.progressHeartbeat = 0

	// Prevents a task's trailing goroutine overwriting a terminal row
	// (done/errored/cancelled) with stale progress — it would resurrect
	// the run's counters in the UI and confuse duration math.
	prog := m.makeProgress(runID, jobDefMeta{ID: 1, PluginID: "t", Name: "p"})
	prog(1, 2, "late report")

	if len(events) != 0 {
		t.Fatalf("observer saw %+v events, want none for a finalized run", len(events))
	}
	var n int
	// No progress fields may be written to the finalized row.
	if err := d.QueryRow(`SELECT progress_done IS NULL FROM job_runs WHERE id = ?`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("progress write leaked onto a finalized run row")
	}
}
