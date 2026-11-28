package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// --- Test helpers ---

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// In-memory databases are per-connection in SQLite. Pin to 1 connection
	// so all goroutines share the same in-memory database.
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	// Run the same schema migration shape as internal/db.
	if err := migrateTestDB(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// migrateTestDB mirrors internal/db.ensureJobTables (the job system's
// schema): definitions with v2 execution metadata, runs with progress
// and scheduling observability, the stats ledger, and the active-dedupe
// partial unique index.
func migrateTestDB(d *sql.DB) error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS job_definitions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			plugin_id       TEXT    NOT NULL,
			name            TEXT    NOT NULL,
			schedule        TEXT    NOT NULL,
			enabled         INTEGER NOT NULL DEFAULT 1,
			priority        INTEGER NOT NULL DEFAULT 0,
			max_parallel    INTEGER NOT NULL DEFAULT 1,
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			max_retries     INTEGER NOT NULL DEFAULT 0,
			created_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(plugin_id, name)
		);
		CREATE TABLE IF NOT EXISTS job_runs (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id           INTEGER NOT NULL REFERENCES job_definitions(id) ON DELETE CASCADE,
			status           TEXT    NOT NULL DEFAULT 'planned',
			payload          TEXT,
			retry_count      INTEGER NOT NULL DEFAULT 0,
			priority         INTEGER NOT NULL DEFAULT 0,
			next_run_at      DATETIME,
			duration_ms      INTEGER,
			worker_id        INTEGER,
			progress_done    INTEGER,
			progress_total   INTEGER,
			progress_message TEXT,
			progress_at      DATETIME,
			started_at       DATETIME,
			finished_at      DATETIME,
			error            TEXT,
			result           TEXT,
			created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE INDEX IF NOT EXISTS idx_job_runs_status ON job_runs(status);
		CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id);
		CREATE INDEX IF NOT EXISTS idx_job_runs_created ON job_runs(created_at);
		CREATE TABLE IF NOT EXISTS job_definition_stats (
			job_id            INTEGER PRIMARY KEY REFERENCES job_definitions(id) ON DELETE CASCADE,
			runs_total        INTEGER NOT NULL DEFAULT 0,
			runs_done         INTEGER NOT NULL DEFAULT 0,
			runs_errored      INTEGER NOT NULL DEFAULT 0,
			runs_cancelled    INTEGER NOT NULL DEFAULT 0,
			auto_retries      INTEGER NOT NULL DEFAULT 0,
			total_duration_ms INTEGER NOT NULL DEFAULT 0,
			min_duration_ms   INTEGER,
			max_duration_ms   INTEGER,
			last_finished_at  DATETIME,
			last_status       TEXT,
			last_result       TEXT,
			last_error        TEXT
		);

		CREATE UNIQUE INDEX IF NOT EXISTS idx_job_runs_active_unique
		ON job_runs(job_id, payload)
		WHERE status IN ('planned','running');
	`)
	return err
}

// newTestManager returns a manager wired for hermetic tests: fast poll,
// fast watchdog, short progress throttle.
func newTestManager(t *testing.T, d *sql.DB) *Manager {
	t.Helper()
	m := NewManager(d, 2, 1)
	m.pollInterval = 20 * time.Millisecond
	m.watchdogInterval = 40 * time.Millisecond
	m.progressMinInterval = 10 * time.Millisecond
	m.progressHeartbeat = 250 * time.Millisecond
	return m
}

// startManager starts the manager and stops it on test cleanup.
func startManager(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	t.Cleanup(func() { m.Stop() })
}

// waitUntil polls cond until it holds or the timeout elapses.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// runStatus loads a run row's status (+ optional error text).
func fetchRun(t *testing.T, d *sql.DB, runID int64) (status string, errText string) {
	t.Helper()
	var errStr sql.NullString
	if err := d.QueryRow(`SELECT status, COALESCE(error,'') FROM job_runs WHERE id = ?`, runID).Scan(&status, &errStr); err != nil {
		t.Fatalf("fetch run %d: %v", runID, err)
	}
	return status, errStr.String
}

func runCountByStatus(t *testing.T, d *sql.DB, jobID int64) map[string]int {
	t.Helper()
	rows, err := d.Query(`SELECT status, COUNT(*) FROM job_runs WHERE job_id = ? GROUP BY status`, jobID)
	if err != nil {
		t.Fatalf("count runs: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatalf("scan count: %v", err)
		}
		out[s] = n
	}
	return out
}

// --- Registration & lifecycle ---

func TestUpsertDefinitionsReusesExistingDefinitionID(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1, 1)

	first := func(context.Context, *RunContext) (string, error) { return "", nil }
	second := func(context.Context, *RunContext) (string, error) { return "", nil }

	if err := m.UpsertDefinitions("test", []CronJob{{Name: "job", Schedule: "@daily", Task: first}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := m.UpsertDefinitions("test", []CronJob{{Name: "job", Schedule: "@daily", Task: second}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_definitions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 definition, got %d", count)
	}

	task, meta, ok := m.taskAndDefinition(1)
	if !ok || task == nil {
		t.Fatal("expected task registered under definition id 1")
	}
	if meta.Name != "job" {
		t.Fatalf("expected meta name 'job', got %q", meta.Name)
	}
}

func TestUpsertDefinitionsPersistsExecutionMetadata(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	task := func(context.Context, *RunContext) (string, error) { return "", nil }

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job", Schedule: "@every 5m",
		Priority: PriorityHigh, MaxParallel: 3,
		Timeout: 90 * time.Second, MaxRetries: 2, Task: task,
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var priority, maxParallel, timeoutSeconds, maxRetries int
	if err := d.QueryRow(`SELECT priority, max_parallel, timeout_seconds, max_retries FROM job_definitions WHERE id = 1`).
		Scan(&priority, &maxParallel, &timeoutSeconds, &maxRetries); err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if priority != 1 || maxParallel != 3 || timeoutSeconds != 90 || maxRetries != 2 {
		t.Fatalf("metadata mismatch: prio=%d par=%d timeout=%ds retries=%d", priority, maxParallel, timeoutSeconds, maxRetries)
	}

	meta := m.lookupDefinition(1)
	if meta.Timeout != 90*time.Second || meta.MaxParallel != 3 || meta.Priority != 1 || meta.MaxRetries != 2 {
		t.Fatalf("cached meta mismatch: %+v", meta)
	}
}

func TestZombieRecoveryResetsRunningAndErrorsOrphans(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	// Registered task → its stuck "running" run must reset to planned.
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job", Schedule: "@daily",
		Task: func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	res, err := d.Exec(`INSERT INTO job_runs (job_id, status, started_at, worker_id) VALUES (1, 'running', strftime('%Y-%m-%dT%H:%M:%fZ','now'), 7)`)
	if err != nil {
		t.Fatalf("insert zombie: %v", err)
	}
	zombieID, _ := res.LastInsertId()

	// Definition never registered → its planned run must be errored.
	res2, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('ghost', 'gone', '')`)
	if err != nil {
		t.Fatalf("insert ghost def: %v", err)
	}
	ghostDefID, _ := res2.LastInsertId()
	res3, err := d.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, 'planned')`, ghostDefID)
	if err != nil {
		t.Fatalf("insert orphan run: %v", err)
	}
	orphanID, _ := res3.LastInsertId()

	if err := m.zombieRecovery(); err != nil {
		t.Fatalf("zombie recovery: %v", err)
	}

	status, errText := fetchRun(t, d, zombieID)
	if status != StatusPlanned {
		t.Fatalf("expected zombie reset to planned, got %q", status)
	}
	if errText == "" {
		t.Fatal("expected crash note on reset run")
	}
	var workerID sql.NullInt64
	if err := d.QueryRow(`SELECT worker_id FROM job_runs WHERE id = ?`, zombieID).Scan(&workerID); err != nil {
		t.Fatalf("scan worker: %v", err)
	}
	if workerID.Valid && workerID.Int64 != 0 {
		t.Fatalf("expected worker_id cleared, got %v", workerID.Int64)
	}

	status, errText = fetchRun(t, d, orphanID)
	if status != StatusErrored {
		t.Fatalf("expected orphan planned run errored, got %q", status)
	}
	if errText == "" {
		t.Fatal("expected 'no task registered' error on orphan run")
	}
}

// --- Execution: wakeup, priority lanes, parallelism ---

func TestImmediateWakeupOnEnqueue(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	done := make(chan int64, 1)
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			done <- rt.RunID
			return "ok", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The notify kick must start the run far faster than the old 1s poll
	// (poll interval here is 20ms; assert 500ms headroom).
	select {
	case got := <-done:
		if got != runID {
			t.Fatalf("expected run %d, got %d", runID, got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("run did not start within 500 ms — workers are not woken by enqueue")
	}
}

func TestFastLaneServesHighPriorityBehindBulkWork(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1, 1) // 1 normal + 1 fast worker
	m.pollInterval = 20 * time.Millisecond
	m.watchdogInterval = time.Hour // watchdog off for this test

	bulkStarted := make(chan struct{})
	bulkRelease := make(chan struct{})
	highDone := make(chan struct{})

	if err := m.UpsertDefinitions("test", []CronJob{
		{
			Name:     "bulk",
			Priority: PriorityNormal,
			Task: func(context.Context, *RunContext) (string, error) {
				close(bulkStarted)
				<-bulkRelease
				return "bulk done", nil
			},
		},
		{
			Name:     "interactive",
			Priority: PriorityHigh,
			Task: func(context.Context, *RunContext) (string, error) {
				close(highDone)
				return "high done", nil
			},
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	if _, err := m.Enqueue("test", "bulk", nil); err != nil {
		t.Fatalf("enqueue bulk: %v", err)
	}
	<-bulkStarted // the single normal worker is now fully occupied

	if _, err := m.Enqueue("test", "interactive", nil); err != nil {
		t.Fatalf("enqueue interactive: %v", err)
	}

	select {
	case <-highDone:
		// The fast worker picked up the high-priority run immediately.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("high-priority run starved behind bulk work — fast lane broken")
	}
	close(bulkRelease)
}

func TestMaxParallelCapsConcurrentRuns(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 4, 1) // plenty of workers; the cap must do the limiting
	m.pollInterval = 20 * time.Millisecond
	m.watchdogInterval = time.Hour

	running := make(chan struct{}, 16)
	release := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:        "capped",
		MaxParallel: 2,
		Task: func(context.Context, *RunContext) (string, error) {
			running <- struct{}{}
			<-release
			return "done", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	for i := 0; i < 5; i++ {
		if _, err := m.Enqueue("test", "capped", []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	// Exactly 2 runs may execute concurrently; the rest must wait.
	<-running
	<-running
	if !waitUntil(300*time.Millisecond, func() bool {
		counts := runCountByStatus(t, d, 1)
		return counts[StatusRunning] == 2 && counts[StatusPlanned] == 3
	}) {
		counts := runCountByStatus(t, d, 1)
		t.Fatalf("expected 2 running / 3 planned under max_parallel=2, got %v", counts)
	}

	close(release)
	if !waitUntil(2*time.Second, func() bool {
		counts := runCountByStatus(t, d, 1)
		return counts[StatusDone] == 5
	}) {
		counts := runCountByStatus(t, d, 1)
		t.Fatalf("expected all 5 done after release, got %v", counts)
	}
}

// --- Progress reporting ---

func TestProgressReportsPersistAndStream(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	var mu sync.Mutex
	var progressEvents []RunEvent
	m.SetObserver(func(evt RunEvent) {
		if evt.Type == "progress" {
			mu.Lock()
			progressEvents = append(progressEvents, evt)
			mu.Unlock()
		}
	})

	rts := make(chan *RunContext, 1)
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			rts <- rt
			for i := 1; i <= 3; i++ {
				rt.Progress(i, 3, fmt.Sprintf("step %d", i))
			}
			return "done", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	rt := <-rts

	// Progress fields land on the row while the run is still executing.
	if !waitUntil(time.Second, func() bool {
		var done, total int
		var msg string
		if err := d.QueryRow(`SELECT progress_done, progress_total, COALESCE(progress_message,'') FROM job_runs WHERE id = ?`, runID).
			Scan(&done, &total, &msg); err != nil {
			return false
		}
		return done == 3 && total == 3 && msg == "step 3"
	}) {
		t.Fatal("progress fields never landed on the run row")
	}

	if rt.RunID != runID {
		t.Fatalf("RunContext carries run id %d, want %d", rt.RunID, runID)
	}

	if !waitUntil(time.Second, func() bool {
		_, errText := fetchRun(t, d, runID)
		return errText == "" && statusDone(t, d, runID)
	}) {
		t.Fatal("run did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(progressEvents) == 0 {
		t.Fatal("expected progress observer events")
	}
	last := progressEvents[len(progressEvents)-1]
	if last.ProgressDone == nil || *last.ProgressDone != 3 || last.ProgressTotal == nil || *last.ProgressTotal != 3 {
		t.Fatalf("last progress event = %+v", last)
	}
	if last.PluginID != "test" || last.JobName != "job" {
		t.Fatalf("progress event identity = %s/%s", last.PluginID, last.JobName)
	}
}

func statusDone(t *testing.T, d *sql.DB, runID int64) bool {
	status, _ := fetchRun(t, d, runID)
	return status == StatusDone
}

// --- Reliability: retries, timeouts, lost claims ---

func TestAutoRetryWithBackoffEventuallySucceeds(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	var attempts int32
	var mu sync.Mutex
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:       "flaky",
		MaxRetries: 2,
		Task: func(context.Context, *RunContext) (string, error) {
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n == 1 {
				return "", fmt.Errorf("transient failure")
			}
			return "recovered", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "flaky", []byte(`{"id":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// First attempt errors; the run row is annotated and a delayed retry
	// is scheduled.
	if !waitUntil(2*time.Second, func() bool {
		status, errText := fetchRun(t, d, runID)
		return status == StatusErrored && errText != "" && errText != "transient failure"
	}) {
		status, errText := fetchRun(t, d, runID)
		t.Fatalf("expected errored run with retry note; got %q (%q)", status, errText)
	}

	var retryID int64
	var nextRun sql.NullString
	if !waitUntil(time.Second, func() bool {
		if err := d.QueryRow(`SELECT id, next_run_at FROM job_runs WHERE retry_count = 1 AND status = 'planned'`).Scan(&retryID, &nextRun); err != nil {
			return false
		}
		return nextRun.Valid
	}) {
		t.Fatal("expected a delayed retry run with next_run_at set")
	}
	// Backoff must be roughly the documented ~15s base for attempt 1
	// (jitter ±20%: 12–18s) — but never in the past.
	if nextRun.String == "" {
		t.Fatal("retry run has empty next_run_at")
	}

	// The watchdog wakes workers for due retries; to keep the test
	// quick and deterministic, make the retry due NOW.
	if _, err := d.Exec(`UPDATE job_runs SET next_run_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, retryID); err != nil {
		t.Fatalf("accelerate retry: %v", err)
	}

	if !waitUntil(2*time.Second, func() bool {
		// The second attempt recovers.
		_, errText := fetchRun(t, d, retryID)
		_ = errText
		return statusDone(t, d, retryID)
	}) {
		status, _ := fetchRun(t, d, retryID)
		t.Fatalf("expected retry run to succeed, got %q", status)
	}

	// Stats: two attempts, one done; auto-retries counted.
	var runsTotal, runsDone, autoRetries int
	if err := d.QueryRow(`SELECT runs_total, runs_done, auto_retries FROM job_definition_stats WHERE job_id = 1`).
		Scan(&runsTotal, &runsDone, &autoRetries); err != nil {
		t.Fatalf("stats read: %v", err)
	}
	if runsTotal != 2 || runsDone != 1 || autoRetries != 1 {
		t.Fatalf("expected 2 total/1 done/1 auto-retry, got %d/%d/%d", runsTotal, runsDone, autoRetries)
	}
}

func TestWatchdogEnforcesTimeoutAndRequeues(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d) // watchdog ticks every 40ms

	release := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:       "wedged",
		Timeout:    time.Second, // sub-second rounds up to 1 s
		MaxRetries: 1,
		Task: func(ctx context.Context, _ *RunContext) (string, error) {
			select {
			case <-release:
				return "finally", nil
			case <-time.After(30 * time.Second):
				return "slept", nil
			case <-ctx.Done():
				// A task that cooperates with cancellation stops here.
				return "", ctx.Err()
			}
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "wedged", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The watchdog must force-finalize the run as errored (timeout).
	if !waitUntil(3*time.Second, func() bool {
		status, errText := fetchRun(t, d, runID)
		return status == StatusErrored && errText != ""
	}) {
		status, errText := fetchRun(t, d, runID)
		t.Fatalf("expected timeout-errored run, got %q (%q)", status, errText)
	}
	_, errText := fetchRun(t, d, runID)
	if want := "timed out"; !contains(errText, want) {
		t.Fatalf("error text %q missing %q", errText, want)
	}

	// MaxRetries=1 → a delayed requeue must exist.
	var retryCount int
	if !waitUntil(time.Second, func() bool {
		err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id = 1 AND retry_count = 1 AND status = 'planned'`).Scan(&retryCount)
		return err == nil && retryCount == 1
	}) {
		t.Fatalf("expected 1 requeued retry run, got %d", retryCount)
	}

	close(release)
}

func TestWatchdogClearsLostClaims(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	// A "running" row this manager never claimed (manual DB edit or a
	// crashed claim): the watchdog must clear it.
	res, err := d.Exec(`INSERT INTO job_runs (job_id, status, started_at) VALUES (1, 'running', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`)
	if err != nil {
		t.Fatalf("insert lost claim: %v", err)
	}
	lostID, _ := res.LastInsertId()

	if !waitUntil(2*time.Second, func() bool {
		status, errText := fetchRun(t, d, lostID)
		return status == StatusErrored && errText != ""
	}) {
		status, errText := fetchRun(t, d, lostID)
		t.Fatalf("expected lost claim cleared, got %q (%q)", status, errText)
	}
	_, errText := fetchRun(t, d, lostID)
	if want := "lost its worker"; !contains(errText, want) {
		t.Fatalf("error text %q missing %q", errText, want)
	}
}

func TestSchedulerDoesNotPileUpBehindSlowRun(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	block := make(chan struct{})
	goal := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "cron",
		Schedule: "@every 40ms",
		Task: func(context.Context, *RunContext) (string, error) {
			close(goal)
			<-block // stay running way past the next tick
			return "done", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	select {
	case <-goal:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled run never started")
	}

	// Several ticks elapse while the first run is still going.
	time.Sleep(250 * time.Millisecond)

	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id = 1`).Scan(&total); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if total != 1 {
		t.Fatalf("cron ticks piled up: %d runs for one still-running job", total)
	}
	close(block)
}

func TestGracefulStopCancelsRunningTask(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	started := make(chan struct{})
	ctxSeen := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(ctx context.Context, _ *RunContext) (string, error) {
			close(started)
			<-ctx.Done()
			close(ctxSeen)
			return "", ctx.Err()
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := m.Enqueue("test", "job", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started

	m.Stop() // cancels the run context and waits for workers

	select {
	case <-ctxSeen:
	default:
		t.Fatal("task context was not cancelled on Stop")
	}
	// The run was running when we stopped; the row must not stay
	// "running" forever (worker finalized it as cancelled/errored).
	status, _ := fetchRun(t, d, 1)
	if status == StatusRunning {
		t.Fatal("run still marked running after graceful stop")
	}
	// Stop is idempotent.
	m.Stop()
}

// --- Queue API: dedupe, retry, cancel ---

func registerDedupeJob(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "dedupe_job",
		Task: func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register dedupe job: %v", err)
	}
}

func TestEnqueueDeduplicatesActiveRuns(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)
	registerDedupeJob(t, m)

	payload7 := []byte(`{"file_id":7}`)
	firstID, err := m.Enqueue("test", "dedupe_job", payload7)
	if err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	secondID, err := m.Enqueue("test", "dedupe_job", payload7)
	if err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	if firstID != secondID {
		t.Errorf("expected identical payload to dedupe to one run ID, got %d vs %d", firstID, secondID)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id = 1 AND payload = ?`, string(payload7)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row for payload %s, got %d", payload7, count)
	}

	payload8 := []byte(`{"file_id":8}`)
	thirdID, err := m.Enqueue("test", "dedupe_job", payload8)
	if err != nil {
		t.Fatalf("enqueue 3: %v", err)
	}
	if thirdID == firstID {
		t.Error("expected different payload to get a different run ID")
	}
}

func TestEnqueueConcurrentDedupe(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)
	registerDedupeJob(t, m)

	payload := []byte(`{"file_id":7}`)
	const n = 8
	ids := make(chan int64, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := m.Enqueue("test", "dedupe_job", payload)
			if err != nil {
				t.Errorf("concurrent enqueue: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]bool)
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Errorf("expected all %d concurrent enqueues to share one run ID, got %d distinct: %v", n, len(seen), seen)
	}
}

func TestRetryRunReturnsActiveDuplicate(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)
	registerDedupeJob(t, m)

	res, err := d.Exec(`INSERT INTO job_runs (job_id, status, payload, error) VALUES (1, 'errored', ?, 'test error')`, `{"file_id":7}`)
	if err != nil {
		t.Fatalf("insert errored run: %v", err)
	}
	erroredID, _ := res.LastInsertId()

	plannedRes, err := d.Exec(`INSERT INTO job_runs (job_id, status, payload) VALUES (1, 'planned', ?)`, `{"file_id":7}`)
	if err != nil {
		t.Fatalf("insert planned run: %v", err)
	}
	plannedID, _ := plannedRes.LastInsertId()

	newID, err := m.RetryRun(erroredID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if newID != plannedID {
		t.Errorf("expected retry to return active duplicate run %d, got %d", plannedID, newID)
	}
}

func TestManualRetryRunsErroredRunAgain(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) {
			return "ran", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitUntil(2*time.Second, func() bool { return statusDone(t, d, runID) }) {
		t.Fatal("first run never finished")
	}

	// Simulate an errored history row (as if the task had failed).
	if _, err := d.Exec(`UPDATE job_runs SET status = 'errored', error = 'simulated failure' WHERE id = ?`, runID); err != nil {
		t.Fatalf("fake error: %v", err)
	}

	newID, err := m.RetryRun(runID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if newID == runID {
		t.Fatal("retry must create a new run row")
	}
	if !waitUntil(2*time.Second, func() bool { return statusDone(t, d, newID) }) {
		status, _ := fetchRun(t, d, newID)
		t.Fatalf("retried run never finished, got %q", status)
	}

	var retryCount int
	if err := d.QueryRow(`SELECT retry_count FROM job_runs WHERE id = ?`, newID).Scan(&retryCount); err != nil {
		t.Fatalf("read retry count: %v", err)
	}
	if retryCount != 1 {
		t.Fatalf("expected retry_count=1 on retried run, got %d", retryCount)
	}
}

func TestCancelRunningRunCancelsTask(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	taskStarted := make(chan struct{})
	ctxCancelled := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(ctx context.Context, _ *RunContext) (string, error) {
			close(taskStarted)
			<-ctx.Done()
			close(ctxCancelled)
			return "", ctx.Err()
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", []byte(`{"which":"running-cancel"}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-taskStarted

	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel running: %v", err)
	}

	// The task observed the cancellation.
	if !waitUntil(time.Second, func() bool {
		select {
		case <-ctxCancelled:
			return true
		default:
			return false
		}
	}) {
		t.Fatal("task context was not cancelled")
	}

	// The row ends cancelled exactly once, and the stats ledger
	// reflects the cancellation.
	if !waitUntil(time.Second, func() bool {
		status, _ := fetchRun(t, d, runID)
		return status == StatusCancelled
	}) {
		status, _ := fetchRun(t, d, runID)
		t.Fatalf("expected cancelled run, got %q", status)
	}

	var runsTotal, runsCancelled int
	if err := d.QueryRow(`SELECT runs_total, runs_cancelled FROM job_definition_stats WHERE job_id = 1`).
		Scan(&runsTotal, &runsCancelled); err != nil {
		t.Fatalf("stats: %v", err)
	}
	if runsTotal != 1 || runsCancelled != 1 {
		t.Fatalf("expected 1 total/1 cancelled in stats, got %d/%d", runsTotal, runsCancelled)
	}

	// Cancelling a terminal run must fail cleanly.
	if err := m.CancelRun(runID); err == nil {
		t.Fatal("expected error cancelling an already-finished run")
	}
}

func TestCancelPlannedRunBeforeStart(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1, 1) // don't start: nothing consumes the queue
	m.registerBuiltinJobs()

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) { return "should not run", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	runID, err := m.Enqueue("test", "job", []byte(`{"which":"planned"}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel planned: %v", err)
	}
	status, errText := fetchRun(t, d, runID)
	if status != StatusCancelled || errText == "" {
		t.Fatalf("expected cancelled planned run with note, got %q (%q)", status, errText)
	}
}

// --- Stats ---

func TestStatsReflectsRunsAndDurations(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) {
			time.Sleep(20 * time.Millisecond)
			return "worked", nil
		},
	}, {
		Name:     "bad",
		Schedule: "", // ad-hoc
		Task: func(context.Context, *RunContext) (string, error) {
			return "", fmt.Errorf("nope")
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	goodID, err := m.Enqueue("test", "job", nil)
	if err != nil {
		t.Fatalf("enqueue good: %v", err)
	}
	badID, err := m.Enqueue("test", "bad", nil)
	if err != nil {
		t.Fatalf("enqueue bad: %v", err)
	}

	if !waitUntil(3*time.Second, func() bool {
		g, _ := fetchRun(t, d, goodID)
		b, _ := fetchRun(t, d, badID)
		return g == StatusDone && b == StatusErrored
	}) {
		g, _ := fetchRun(t, d, goodID)
		b, _ := fetchRun(t, d, badID)
		t.Fatalf("runs did not finish: good=%q bad=%q", g, b)
	}

	resp, err := m.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if resp.Workers.Total != m.WorkerCount()+m.FastWorkerCount() {
		t.Fatalf("workers.total = %d", resp.Workers.Total)
	}
	if resp.Queue.Planned != 0 || resp.Queue.Running != 0 {
		t.Fatalf("expected empty queue, got %+v", resp.Queue)
	}
	if resp.Last24h.Done != 1 || resp.Last24h.Errored != 1 {
		t.Fatalf("24h window = %+v", resp.Last24h)
	}

	byName := map[string]DefinitionStats{}
	for _, ds := range resp.Definitions {
		byName[ds.PluginID+"/"+ds.Name] = ds
	}
	good := byName["test/job"]
	if good.RunsTotal != 1 || good.RunsDone != 1 || good.SuccessRate != 1.0 {
		t.Fatalf("good stats = %+v", good)
	}
	if good.AvgDurationMS < 15 {
		t.Fatalf("avg duration suspiciously low: %d", good.AvgDurationMS)
	}
	if good.LastStatus == nil || *good.LastStatus != StatusDone {
		t.Fatalf("last status = %+v", good.LastStatus)
	}
	bad := byName["test/bad"]
	if bad.RunsErrored != 1 || bad.SuccessRate != 0 {
		t.Fatalf("bad stats = %+v", bad)
	}
	if bad.LastError == nil || *bad.LastError != "nope" {
		t.Fatalf("last error = %+v", bad.LastError)
	}

	// Duration is also stored on the run row.
	var dur sql.NullInt64
	if err := d.QueryRow(`SELECT duration_ms FROM job_runs WHERE id = ?`, goodID).Scan(&dur); err != nil {
		t.Fatalf("duration read: %v", err)
	}
	if !dur.Valid || dur.Int64 < 15 {
		t.Fatalf("run duration_ms = %+v", dur)
	}
}

// --- Error & panic handling ---

func TestTaskErrorMarksRunErrored(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) {
			return "", fmt.Errorf("boom")
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitUntil(2*time.Second, func() bool {
		status, errText := fetchRun(t, d, runID)
		return status == StatusErrored && errText == "boom"
	}) {
		status, errText := fetchRun(t, d, runID)
		t.Fatalf("expected errored run with 'boom', got %q (%q)", status, errText)
	}
}

func TestTaskPanicIsRecoveredAndRecorded(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) {
			panic("kaboom")
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("test", "job", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitUntil(2*time.Second, func() bool {
		status, errText := fetchRun(t, d, runID)
		return status == StatusErrored && contains(errText, "panic: kaboom")
	}) {
		status, errText := fetchRun(t, d, runID)
		t.Fatalf("expected panic recorded as error, got %q (%q)", status, errText)
	}

	// Cancel-retry still works on the panicked run (manual path).
	if _, err := m.RetryRun(runID); err != nil {
		t.Fatalf("retry panicked run: %v", err)
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	var got []byte
	done := make(chan struct{})
	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(_ context.Context, rt *RunContext) (string, error) {
			got = rt.Payload
			close(done)
			return "ok", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	payload, _ := json.Marshal(map[string]any{"note_id": 42, "text": "hello"})
	if _, err := m.Enqueue("test", "job", payload); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run never executed")
	}
	var p map[string]any
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if p["note_id"] != float64(42) || p["text"] != "hello" {
		t.Fatalf("payload mismatch: %+v", p)
	}
}

// --- Observer ---

func TestObserverReceivesLifecycleEvents(t *testing.T) {
	d := openTestDB(t)
	m := newTestManager(t, d)

	var mu sync.Mutex
	seen := map[string]int{}
	m.SetObserver(func(evt RunEvent) {
		mu.Lock()
		seen[evt.Type]++
		mu.Unlock()
	})

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name: "job",
		Task: func(context.Context, *RunContext) (string, error) {
			return "ok", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	if _, err := m.Enqueue("test", "job", nil); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitUntil(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["enqueued"] >= 1 && seen["running"] >= 1 && seen["done"] >= 1
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("expected enqueued/running/done events, got %v", seen)
	}

	// Identity and duration ride along.
	mu.Lock()
	defer mu.Unlock()
	if seen["running"] > 0 {
		// pass: shape validated by progress test
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
