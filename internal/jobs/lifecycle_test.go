package jobs

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
)

// brokenDB returns a SQLite handle without the job tables: every
// statement the job system issues fails with sqlite3.Error ("no such
// table"). Deterministic fault injection for the package's error paths
// without mutating any shared DB.
func brokenDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open broken db: %v", err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	return d
}

// --- NewManager defaults ---

func TestNewManagerFloorsWorkerCounts(t *testing.T) {
	t.Parallel()
	d := brokenDB(t)

	// Prevents a zero/negative config ([jobs] workers = 0 in
	// config.toml) producing a manager with no workers at all — that
	// would silently enqueue runs that never execute.
	m := NewManager(d, 0, -1)
	if m.WorkerCount() != 1 {
		t.Fatalf("WorkerCount() = %d, want floor 1", m.WorkerCount())
	}
	if m.FastWorkerCount() != 1 {
		t.Fatalf("FastWorkerCount() = %d, want floor 1", m.FastWorkerCount())
	}
}

func TestNewManagerKeepsConfiguredLanes(t *testing.T) {
	t.Parallel()
	d := brokenDB(t)

	// Prevents the fast lane being silently dropped when it was
	// explicitly configured — interactive jobs would lose their
	// dedicated pool and queue behind bulk work again.
	m := NewManager(d, 4, 2)
	if m.WorkerCount() != 4 || m.FastWorkerCount() != 2 {
		t.Fatalf("lane sizes = %d/%d, want 4/2", m.WorkerCount(), m.FastWorkerCount())
	}
}

func TestNilManagerMethodGuards(t *testing.T) {
	t.Parallel()

	var m *Manager
	// Prevents a nil Manager (e.g. a test-mode server that never
	// constructs the job system) panicking the whole request path when
	// the UI asks for counters, registers an observer, or a stray
	// event emission runs.
	if m.WorkerCount() != 0 {
		t.Fatal("WorkerCount on nil manager must return 0")
	}
	if m.FastWorkerCount() != 0 {
		t.Fatal("FastWorkerCount on nil manager must return 0")
	}
	m.SetObserver(func(RunEvent) {})
	m.emitEvent(RunEvent{Type: "done"})
}

func TestSetObserverWithoutOverlappingCalls(t *testing.T) {
	t.Parallel()
	d := brokenDB(t)
	m := NewManager(d, 1, 1)

	got := make(chan string, 4)
	// Prevents an observer swap mid-stream either losing events or
	// racing: the manager must hand the latest observer snapshot to
	// emitEvent while holding only a read lock.
	m.SetObserver(func(e RunEvent) { got <- e.Type })
	m.emitEvent(RunEvent{Type: "running"})
	// Swapping to a second observer mid-flight must not deadlock.
	m.SetObserver(func(e RunEvent) { got <- "second:" + e.Type })
	m.emitEvent(RunEvent{Type: "done"})

	select {
	case evt := <-got:
		if evt != "running" {
			t.Fatalf("first event routed to %q", evt)
		}
	default:
		t.Fatal("emitEvent delivered nothing to the first observer")
	}
	select {
	case evt := <-got:
		if evt != "second:done" {
			t.Fatalf("second event routed as %q", evt)
		}
	default:
		t.Fatal("emitEvent delivered nothing to the second observer")
	}
}

// --- Builtin janitor ---

func TestJanitorPrunesOnlyOldRuns(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// The ledger (job_definition_stats) must survive pruning — it is
	// the only long-lived history; only job_runs rows older than 30
	// days disappear.
	if err := m.UpsertDefinitions("test", []CronJob{{Name: "job", Task: func(context.Context, *RunContext) (string, error) { return "", nil }}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	old, err := insertRawRun(t, d, 1, StatusDone, "2020-01-01T00:00:00.000Z")
	if err != nil {
		t.Fatalf("insert old run: %v", err)
	}
	fresh, err := insertRawRun(t, d, 1, StatusDone, "") // now
	if err != nil {
		t.Fatalf("insert fresh run: %v", err)
	}
	if _, err := insertStatsRow(t, d, 1); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	// Direct invocation keeps this deterministic (no scheduler wait):
	// the janitor is a plain task function.
	res, err := janitorTask(context.Background(), &RunContext{DB: d})
	if err != nil {
		t.Fatalf("janitorTask: %v", err)
	}
	if !strings.Contains(res, "Cleaned up 1") {
		t.Fatalf("janitor result = %q, want a 1-run cleanup report", res)
	}

	// The old run must be gone, the fresh one kept.
	for id, wantGone := range map[int64]bool{old: true, fresh: false} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if (n == 0) != wantGone {
			t.Fatalf("run %d: gone=%v, want gone=%v", id, n == 0, wantGone)
		}
	}
	// The ledger row must survive the prune (fresh installs rely on it
	// accumulating beyond the 30-day run horizon).
	var ledger int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_definition_stats WHERE job_id = 1`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("ledger rows after janitor = %d, want 1 (never pruned)", ledger)
	}

	// A second pass with nothing to do must report the no-op message
	// (the UI shows the result string verbatim).
	res, err = janitorTask(context.Background(), &RunContext{DB: d})
	if err != nil {
		t.Fatalf("second janitorTask: %v", err)
	}
	if !strings.Contains(res, "No old job runs") {
		t.Fatalf("second janitor result = %q, want the no-op message", res)
	}
}

func TestJanitorSurfacesDBErrors(t *testing.T) {
	t.Parallel()

	// Prevents a failing DELETE being swallowed into a success result
	// (the run row would claim "done" while the prune silently never
	// happens).
	d := brokenDB(t)
	if _, err := janitorTask(context.Background(), &RunContext{DB: d}); err == nil {
		t.Fatal("janitorTask on a broken DB must return an error")
	}
}

// --- Start error paths ---

func TestStartReportsRegistrationFailure(t *testing.T) {
	t.Parallel()
	d := brokenDB(t)
	m := NewManager(d, 1, 1)

	// Prevents Start succeeding (and the server booting) against a DB
	// where the job tables are missing/corrupt: the failure must be a
	// wrapped sqlite error, not a log line, so operators see it.
	err := m.Start()
	if err == nil {
		t.Fatal("Start on a schema-less DB must fail")
	}
	var sqlErr sqlite3.Error
	if !asSQLiteError(err, &sqlErr) {
		t.Fatalf("Start error %v does not wrap a sqlite error", err)
	}
	if !strings.Contains(err.Error(), "register builtin jobs") {
		t.Fatalf("Start error %v does not name the failing step", err)
	}
}

func TestStartReportsZombieRecoveryFailure(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	if _, err := d.Exec(`DROP TABLE job_runs`); err != nil {
		t.Fatalf("drop job_runs: %v", err)
	}
	m := newTestManager(t, d)

	// Definitions upsert fine but the runs table is gone: zombie
	// recovery must stop Start with the wrapped sqlite error instead
	// of continuing into third-loop goroutines that would fail on
	// every query.
	err := m.Start()
	if err == nil {
		t.Fatal("Start without job_runs must fail")
	}
	var sqlErr sqlite3.Error
	if !asSQLiteError(err, &sqlErr) {
		t.Fatalf("Start error %v does not wrap a sqlite error", err)
	}
	if !strings.Contains(err.Error(), "zombie recovery") {
		t.Fatalf("Start error %v does not name the failing step", err)
	}
}

func TestStartSkipsSchedulerForUnregisteredDefinition(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// A definition left in the DB by an older binary (plugin removed,
	// or its jobs package no longer registering the task) must not
	// fight the recovery pass: Start launches no scheduler for it and
	// still boots.
	if err := m.UpsertDefinitions("ghost", []CronJob{{
		Name:     "vanished",
		Schedule: "@every 10ms",
		Task:     func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	m.tasksMu.Lock()
	delete(m.tasks, 1)
	delete(m.defs, 1)
	m.tasksMu.Unlock()

	startManager(t, m) // asserts Start returns nil

	// The orphaned definition gets no scheduler: after several would-be
	// tick intervals no run exists for it.
	deadline := time.After(150 * time.Millisecond)
	for {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id = 1`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			t.Fatal("unregistered definition must not be scheduled; a run appeared")
		}
		select {
		case <-deadline:
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// --- Stop ---

func TestStopIsIdempotent(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)
	startManager(t, m)

	// Prevents a double Stop (test cleanup + explicit server shutdown)
	// panicking on a closed channel — the second call must be a no-op
	// and return promptly.
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("first Stop did not return")
	}
	m.Stop() // second call: must not block or panic
}

// --- helpers ---

// insertRawRun inserts a finished/planned run row with an explicit
// created_at ("" = now) for touch-ups directly in SQL.
func insertRawRun(t *testing.T, d *sql.DB, jobID int64, status, createdAt string) (int64, error) {
	t.Helper()
	created := "strftime('%Y-%m-%dT%H:%M:%fZ','now')"
	if createdAt != "" {
		created = "'" + createdAt + "'"
	}
	res, err := d.Exec(`INSERT INTO job_runs (job_id, status, created_at) VALUES (?, ?, `+created+`)`, jobID, status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// insertStatsRow seeds a minimal ledger row so tests can pin the
// "ledger survives the janitor" behavior.
func insertStatsRow(t *testing.T, d *sql.DB, jobID int64) (int64, error) {
	t.Helper()
	res, err := d.Exec(
		`INSERT INTO job_definition_stats (job_id, runs_total, runs_done) VALUES (?, 1, 1)`, jobID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// asSQLiteError unwraps fmt-wrapped sqlite failures without tying the
// tests to the driver's concrete error text.
func asSQLiteError(err error, target *sqlite3.Error) bool {
	for err != nil {
		if se, ok := err.(sqlite3.Error); ok {
			*target = se
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
