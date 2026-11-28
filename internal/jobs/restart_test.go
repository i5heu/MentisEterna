package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// openFileTestDBAt opens a WAL-mode file-backed database with the jobs
// schema and a bounded connection pool. Unlike openTestDB's pinned
// single :memory: connection, this allows a parked transaction and
// genuinely concurrent statements — the restart and race tests need a
// real file and parallel connections. Reopening the same path is how a
// "new server process" is simulated.
func openFileTestDBAt(t testing.TB, path string, maxOpenConns int) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open file db: %v", err)
	}
	d.SetMaxOpenConns(maxOpenConns)
	t.Cleanup(func() { _ = d.Close() })
	if err := migrateTestDB(d); err != nil {
		t.Fatalf("migrate file db: %v", err)
	}
	return d
}

// openFileTestDB opens a fresh file-backed test database in a temp dir
// and returns the handle plus its path (for reopen-as-new-process).
func openFileTestDB(t testing.TB, maxOpenConns int) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	return openFileTestDBAt(t, path, maxOpenConns), path
}

// TestRestartCycleOverFileDatabase walks the full graceful-restart
// cycle across two manager instances on the same database file:
// run in flight → Stop (cancels it) → handle closed → file reopened →
// definitions re-registered → queued run executes to completion, and
// the stats ledger carries over.
func TestRestartCycleOverFileDatabase(t *testing.T) {
	t.Parallel()
	d1, path := openFileTestDB(t, 2)

	m1 := newTestManager(t, d1)
	taskStarted := make(chan struct{})
	release := make(chan struct{})
	if err := m1.UpsertDefinitions("restart", []CronJob{{
		Name: "cycle",
		Task: func(ctx context.Context, _ *RunContext) (string, error) {
			close(taskStarted)
			select {
			case <-release:
			case <-ctx.Done(): // graceful Stop cancels here
			}
			return "interrupted", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	defID := mustDefinitionID(t, d1, "restart", "cycle")

	if err := m1.Start(); err != nil {
		t.Fatalf("start m1: %v", err)
	}

	run1, err := m1.Enqueue("restart", "cycle", []byte(`{"n":1}`))
	if err != nil {
		t.Fatalf("enqueue run1: %v", err)
	}
	<-taskStarted

	// A second queued run must survive the restart untouched (blocked
	// behind max_parallel=1 while the first run holds the slot).
	run2, err := m1.Enqueue("restart", "cycle", []byte(`{"n":2}`))
	if err != nil {
		t.Fatalf("enqueue run2: %v", err)
	}
	if status, _ := fetchRun(t, d1, run2); status != StatusPlanned {
		t.Fatalf("run2 should be queued while run1 runs, got %q", status)
	}

	// Graceful shutdown: run1 finalizes as cancelled, workers exit
	// without draining run2 — exactly what a server stop leaves behind.
	m1.Stop()
	if !waitUntil(2*time.Second, func() bool {
		status, _ := fetchRun(t, d1, run1)
		return status == StatusCancelled
	}) {
		status, _ := fetchRun(t, d1, run1)
		t.Fatalf("run1 not cancelled by graceful stop, got %q", status)
	}
	// Simulate process death: the handle goes away, the queue lives on
	// in the file (double Close is a no-op with the cleanup).
	_ = d1.Close()

	// A "new process" opens the same file.
	d2 := openFileTestDBAt(t, path, 2)
	if status, _ := fetchRun(t, d2, run1); status != StatusCancelled {
		t.Fatalf("run1 did not survive restart as cancelled, got %q", status)
	}
	if status, _ := fetchRun(t, d2, run2); status != StatusPlanned {
		t.Fatalf("run2 did not survive restart as planned, got %q", status)
	}

	m2 := newTestManager(t, d2)
	gotPayload := make(chan []byte, 1)
	if err := m2.UpsertDefinitions("restart", []CronJob{{
		Name: "cycle", // same plugin/name → the persisted definition row is reused
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			gotPayload <- rt.Payload
			return "restarted", nil
		},
	}}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if got := mustDefinitionID(t, d2, "restart", "cycle"); got != defID {
		t.Fatalf("restart must reuse the persisted definition id %d, got %d", defID, got)
	}
	startManager(t, m2)

	// The queued run executes under the new process, with its payload.
	if !waitUntil(2*time.Second, func() bool { return statusDone(t, d2, run2) }) {
		status, _ := fetchRun(t, d2, run2)
		t.Fatalf("queued run did not continue after restart, got %q", status)
	}
	select {
	case p := <-gotPayload:
		if string(p) != `{"n":2}` {
			t.Fatalf("restarted run got payload %q, want %q", p, `{"n":2}`)
		}
	default:
		t.Fatal("restarted run never handed its payload to the task")
	}

	// Ledger continuity across the restart: one cancelled (m1), one
	// done (m2) — the aggregate history survives the process boundary.
	var total, done, cancelled int
	if err := d2.QueryRow(
		`SELECT runs_total, runs_done, runs_cancelled FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &done, &cancelled); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 2 || done != 1 || cancelled != 1 {
		t.Fatalf("ledger after restart = total %d/done %d/cancelled %d, want 2/1/1", total, done, cancelled)
	}
}

// TestHardCrashRecoveryOverFileDatabase simulates a kill -9: a run
// stuck mid-flight (status running, worker_id set) plus a queued run
// are left in the file. The next manager's startup zombie recovery
// resets the stuck row and BOTH runs execute to completion — over a
// real file DB, through the full Start path (recovery → dequeue →
// task → finalize), not a direct zombieRecovery call.
func TestHardCrashRecoveryOverFileDatabase(t *testing.T) {
	t.Parallel()
	d, _ := openFileTestDB(t, 2)

	// Crash leftovers, written directly: a definition, a run claimed
	// by a worker that died (worker_id=7), and a queued NULL-payload
	// run — the cron-shaped row the unique active index allows.
	res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('crash', 'worker', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	defID, _ := res.LastInsertId()
	res, err = d.Exec(`INSERT INTO job_runs (job_id, status, started_at, worker_id)
		VALUES (?, 'running', strftime('%Y-%m-%dT%H:%M:%fZ','now'), 7)`, defID)
	if err != nil {
		t.Fatalf("insert crashed run: %v", err)
	}
	crashedID, _ := res.LastInsertId()
	res, err = d.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, 'planned')`, defID)
	if err != nil {
		t.Fatalf("insert queued run: %v", err)
	}
	queuedID, _ := res.LastInsertId()

	m := newTestManager(t, d)
	invocations := make(chan int64, 2)
	if err := m.UpsertDefinitions("crash", []CronJob{{
		Name: "worker",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			invocations <- rt.RunID
			return "recovered", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	bothDone := func() bool {
		a, _ := fetchRun(t, d, crashedID)
		b, _ := fetchRun(t, d, queuedID)
		return a == StatusDone && b == StatusDone
	}
	if !waitUntil(2*time.Second, bothDone) {
		a, aErr := fetchRun(t, d, crashedID)
		b, _ := fetchRun(t, d, queuedID)
		t.Fatalf("recovery did not complete both runs: crashed=%q (%q) queued=%q", a, aErr, b)
	}

	// The stuck row really re-executed (not just status-flipped): both
	// run ids passed through the task, the crash note is cleared by the
	// success, and the dead worker_id was replaced by a live claim.
	seen := map[int64]bool{}
	for range 2 {
		select {
		case id := <-invocations:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("expected 2 task invocations, saw %v", seen)
		}
	}
	if !seen[crashedID] || !seen[queuedID] {
		t.Fatalf("task invocations %v must include both the recovered run %d and the queued run %d", seen, crashedID, queuedID)
	}
	_, errText := fetchRun(t, d, crashedID)
	if errText != "" {
		t.Fatalf("recovered run must clear the crash note on success, got %q", errText)
	}
	var workerID sql.NullInt64
	if err := d.QueryRow(`SELECT worker_id FROM job_runs WHERE id = ?`, crashedID).Scan(&workerID); err != nil {
		t.Fatalf("scan worker: %v", err)
	}
	if workerID.Int64 == 7 {
		t.Fatal("recovered run still carries the dead worker's id — it was never re-claimed")
	}

	// The ledger counted both recovered executions exactly once.
	var total, done int
	if err := d.QueryRow(
		`SELECT runs_total, runs_done FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &done); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 2 || done != 2 {
		t.Fatalf("ledger after crash recovery = %d/%d, want 2/2", total, done)
	}
}
