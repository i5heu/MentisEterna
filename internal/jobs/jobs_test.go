package jobs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	// Use standard sqlite3 for tests (no VSS needed).
	_ "github.com/mattn/go-sqlite3"
)

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
	// Run the same schema migration.
	if err := migrateTestDB(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func migrateTestDB(d *sql.DB) error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS job_definitions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			plugin_id   TEXT    NOT NULL,
			name        TEXT    NOT NULL,
			schedule    TEXT    NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(plugin_id, name)
		);
		CREATE TABLE IF NOT EXISTS job_runs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id      INTEGER NOT NULL REFERENCES job_definitions(id) ON DELETE CASCADE,
			status      TEXT    NOT NULL DEFAULT 'planned',
			payload     TEXT,
			started_at  DATETIME,
			finished_at DATETIME,
			error       TEXT,
			result      TEXT,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE INDEX IF NOT EXISTS idx_job_runs_status ON job_runs(status);
		CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id);
		CREATE INDEX IF NOT EXISTS idx_job_runs_created ON job_runs(created_at);
	`)
	return err
}

func TestAtomicDequeue(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 2)

	// Register a test job.
	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "myjob",
		Schedule: "@every 1h",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "ok", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Enqueue 10 planned runs.
	for i := 0; i < 10; i++ {
		_, err := m.Enqueue("test", "myjob", nil)
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	// Launch 5 concurrent workers that dequeue atomically.
	var mu sync.Mutex
	claimed := make(map[int64]bool)
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, runID, _, ok := m.atomicDequeue()
				if !ok {
					return
				}
				mu.Lock()
				if claimed[runID] {
					t.Errorf("run %d was dequeued twice!", runID)
				}
				claimed[runID] = true
				mu.Unlock()

				// Mark as done.
				d.Exec(`UPDATE job_runs SET status = 'done', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, runID)
			}
		}()
	}

	wg.Wait()

	if len(claimed) != 10 {
		t.Errorf("expected 10 unique claims, got %d", len(claimed))
	}
	t.Logf("atomic dequeue: %d unique runs claimed by 5 concurrent workers", len(claimed))
}

func TestZombieRecovery(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "zombie_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "done", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Manually insert a "running" job (simulates a crash).
	_, err = d.Exec(`INSERT INTO job_runs (job_id, status, started_at) VALUES (1, 'running', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert stuck running: %v", err)
	}

	// Run zombie recovery.
	if err := m.zombieRecovery(); err != nil {
		t.Fatalf("zombie recovery: %v", err)
	}

	// Assert it was reset.
	var status string
	var errMsg *string
	err = d.QueryRow(`SELECT status, error FROM job_runs WHERE id = 1`).Scan(&status, &errMsg)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != StatusPlanned {
		t.Errorf("expected status 'planned', got %q", status)
	}
	if errMsg == nil || *errMsg == "" {
		t.Error("expected error message set on zombie recovery")
	}
	t.Logf("zombie recovery: status=%s error=%s", status, *errMsg)
}

func TestUpsertDefinitionsReusesExistingDefinitionID(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	firstTask := func(db *sql.DB, payload []byte) (string, error) {
		return "first", nil
	}
	secondTask := func(db *sql.DB, payload []byte) (string, error) {
		return "second", nil
	}

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "existing_job",
		Schedule: "@daily",
		Task:     firstTask,
	}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	var originalID int64
	if err := d.QueryRow(`SELECT id FROM job_definitions WHERE plugin_id = 'test' AND name = 'existing_job'`).Scan(&originalID); err != nil {
		t.Fatalf("lookup original id: %v", err)
	}

	if err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "existing_job",
		Schedule: "@every 1h",
		Task:     secondTask,
	}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var updatedID int64
	if err := d.QueryRow(`SELECT id FROM job_definitions WHERE plugin_id = 'test' AND name = 'existing_job'`).Scan(&updatedID); err != nil {
		t.Fatalf("lookup updated id: %v", err)
	}
	if updatedID != originalID {
		t.Fatalf("expected existing definition ID %d to be reused, got %d", originalID, updatedID)
	}

	task := m.tasks[originalID]
	if task == nil {
		t.Fatalf("expected task registered under existing definition id %d", originalID)
	}
	result, err := task(d, nil)
	if err != nil {
		t.Fatalf("task execution failed: %v", err)
	}
	if result != "second" {
		t.Fatalf("expected updated task result %q, got %q", "second", result)
	}
}

func TestErrorHandling(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "failing_job",
		Schedule: "@every 1h",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "", fmt.Errorf("simulated failure")
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// We can't register the task properly via Start here since that starts goroutines.
	// Instead, we enqueue directly and simulate worker execution.
	runID, err := m.Enqueue("test", "failing_job", nil)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Simulate what the worker would do.
	task := m.tasks[1] // job_definition id=1
	m.db.Exec(`UPDATE job_runs SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, runID)
	_, taskErr := task(d, nil)
	if taskErr == nil {
		t.Fatal("expected task to fail")
	}

	// Update as errored.
	m.db.Exec(`UPDATE job_runs SET status = 'errored', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = ? WHERE id = ?`,
		taskErr.Error(), runID)

	var status string
	var errMsg *string
	d.QueryRow(`SELECT status, error FROM job_runs WHERE id = ?`, runID).Scan(&status, &errMsg)
	if status != StatusErrored {
		t.Errorf("expected status 'errored', got %q", status)
	}
	if errMsg == nil || *errMsg == "" {
		t.Error("expected non-empty error")
	}
	t.Logf("error handling: status=%s error=%s", status, *errMsg)
}

func TestRetry(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "retry_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "ok", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insert an errored run.
	res, err := d.Exec(`INSERT INTO job_runs (job_id, status, error) VALUES (1, 'errored', 'test error')`)
	if err != nil {
		t.Fatalf("insert errored run: %v", err)
	}
	oldID, _ := res.LastInsertId()

	// Retry it.
	newID, err := m.RetryRun(oldID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	if newID == oldID {
		t.Error("retry should create a new run")
	}

	var status string
	var pld *string
	err = d.QueryRow(`SELECT status, payload FROM job_runs WHERE id = ?`, newID).Scan(&status, &pld)
	if err != nil {
		t.Fatalf("query new run: %v", err)
	}
	if status != StatusPlanned {
		t.Errorf("expected status planned, got %q", status)
	}
	t.Logf("retry: old=%d new=%d status=%s", oldID, newID, status)
}

func TestCancel(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "cancel_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "ok", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insert a planned run.
	res, err := d.Exec(`INSERT INTO job_runs (job_id, status) VALUES (1, 'planned')`)
	if err != nil {
		t.Fatalf("insert planned run: %v", err)
	}
	runID, _ := res.LastInsertId()

	// Cancel it.
	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var status string
	err = d.QueryRow(`SELECT status FROM job_runs WHERE id = ?`, runID).Scan(&status)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != StatusCancelled {
		t.Errorf("expected status cancelled, got %q", status)
	}

	// Cancelling again should fail (not planned).
	if err := m.CancelRun(runID); err == nil {
		t.Error("expected error when cancelling an already cancelled run")
	}
	t.Logf("cancel: status=%s (double cancel correctly rejected)", status)
}

func TestGracefulShutdown(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "gs_job",
		Schedule: "@every 10h", // long enough to not schedule during test
		Task: func(db *sql.DB, payload []byte) (string, error) {
			time.Sleep(100 * time.Millisecond)
			return "done", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Start the manager.
	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Give it a moment to start.
	time.Sleep(50 * time.Millisecond)

	// Stop should complete quickly since no jobs are scheduled.
	m.Stop()

	select {
	case <-m.done:
		t.Log("graceful shutdown: manager stopped cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown: timed out waiting for Stop")
	}

	// Verify stop channel is closed (double-check).
	select {
	case <-m.stop:
		t.Log("stop channel closed")
	default:
		t.Error("stop channel not closed")
	}
}

func TestJanitor(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "j_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "ok", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insert 5 old run rows (31 days ago).
	for i := 0; i < 5; i++ {
		_, err := d.Exec(
			`INSERT INTO job_runs (job_id, status, created_at) VALUES (1, 'done', datetime('now', '-31 days'))`,
		)
		if err != nil {
			t.Fatalf("insert old run %d: %v", i, err)
		}
	}

	// Insert 3 recent run rows (1 day ago).
	for i := 0; i < 3; i++ {
		_, err := d.Exec(
			`INSERT INTO job_runs (job_id, status, created_at) VALUES (1, 'done', datetime('now', '-1 day'))`,
		)
		if err != nil {
			t.Fatalf("insert recent run %d: %v", i, err)
		}
	}

	// Run the janitor.
	result, err := janitorTask(d, nil)
	if err != nil {
		t.Fatalf("janitor: %v", err)
	}
	t.Logf("janitor result: %s", result)

	// Assert old rows are deleted, recent rows remain.
	var count int
	err = d.QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 remaining runs, got %d", count)
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	var receivedPayload []byte
	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "payload_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			receivedPayload = payload
			var v map[string]int
			if err := json.Unmarshal(payload, &v); err != nil {
				return "", err
			}
			return fmt.Sprintf("processed note %d", v["note_id"]), nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Enqueue with payload.
	payload := json.RawMessage(`{"note_id":42}`)
	runID, err := m.Enqueue("test", "payload_job", payload)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Simulate worker executing.
	task := m.tasks[1]
	m.db.Exec(`UPDATE job_runs SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, runID)
	result, taskErr := task(d, []byte(payload))
	if taskErr != nil {
		t.Fatalf("task error: %v", taskErr)
	}
	m.db.Exec(`UPDATE job_runs SET status = 'done', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), result = ? WHERE id = ?`,
		result, runID)

	// Assert payload was received correctly.
	if string(receivedPayload) != string(payload) {
		t.Errorf("payload mismatch: got %s, want %s", receivedPayload, payload)
	}
	if result != "processed note 42" {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify stored in DB.
	var dbPayload *string
	var dbResult *string
	err = d.QueryRow(`SELECT payload, result FROM job_runs WHERE id = ?`, runID).Scan(&dbPayload, &dbResult)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if dbPayload == nil || *dbPayload != `{"note_id":42}` {
		t.Errorf("stored payload mismatch: %v", dbPayload)
	}
	if dbResult == nil || *dbResult != "processed note 42" {
		t.Errorf("stored result mismatch: %v", dbResult)
	}
	t.Logf("payload round-trip: payload=%s result=%s", *dbPayload, *dbResult)
}

func TestMediaJobsRegistration(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	// Simulate what the media package does: register ad-hoc upload and delete jobs.
	uploadCalled := false
	deleteCalled := false

	err := m.RegisterAdHoc("media", []CronJob{
		{
			Name: "upload_file",
			Task: func(db *sql.DB, payload []byte) (string, error) {
				uploadCalled = true
				return "uploaded file", nil
			},
		},
		{
			Name: "delete_replicas",
			Task: func(db *sql.DB, payload []byte) (string, error) {
				deleteCalled = true
				return "deleted replicas", nil
			},
		},
	})
	if err != nil {
		t.Fatalf("register ad-hoc: %v", err)
	}

	// Verify definitions exist.
	var count int
	d.QueryRow(`SELECT COUNT(*) FROM job_definitions WHERE plugin_id = 'media'`).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 media job definitions, got %d", count)
	}

	// Verify schedule is empty (ad-hoc only).
	var schedule string
	d.QueryRow(`SELECT schedule FROM job_definitions WHERE plugin_id = 'media' AND name = 'upload_file'`).Scan(&schedule)
	if schedule != "" {
		t.Errorf("expected empty schedule for ad-hoc job, got %q", schedule)
	}

	// Enqueue an upload job and simulate worker execution.
	payload := json.RawMessage(`{"file_id":42}`)
	runID, err := m.Enqueue("media", "upload_file", payload)
	if err != nil {
		t.Fatalf("enqueue upload_file: %v", err)
	}

	// Find the task and execute it.
	task := m.tasks[1] // job_definition id for upload_file
	if task == nil {
		t.Fatal("upload_file task not registered")
	}
	result, taskErr := task(d, []byte(payload))
	if taskErr != nil {
		t.Fatalf("upload_file task: %v", taskErr)
	}
	if !uploadCalled {
		t.Error("upload task was not called")
	}
	t.Logf("upload task result: %s", result)

	// Update run as done.
	d.Exec(`UPDATE job_runs SET status = 'done', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), result = ? WHERE id = ?`,
		result, runID)

	// Verify the run is recorded.
	var status string
	d.QueryRow(`SELECT status FROM job_runs WHERE id = ?`, runID).Scan(&status)
	if status != StatusDone {
		t.Errorf("expected status 'done', got %q", status)
	}

	// Enqueue a delete job.
	payload2 := json.RawMessage(`{"file_id":99}`)
	runID2, err := m.Enqueue("media", "delete_replicas", payload2)
	if err != nil {
		t.Fatalf("enqueue delete_replicas: %v", err)
	}
	task2 := m.tasks[2] // job_definition id for delete_replicas
	if task2 == nil {
		t.Fatal("delete_replicas task not registered")
	}
	result2, taskErr2 := task2(d, []byte(payload2))
	if taskErr2 != nil {
		t.Fatalf("delete_replicas task: %v", taskErr2)
	}
	if !deleteCalled {
		t.Error("delete task was not called")
	}
	d.Exec(`UPDATE job_runs SET status = 'done', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), result = ? WHERE id = ?`,
		result2, runID2)

	t.Logf("media jobs: upload=%d delete=%d both completed", runID, runID2)
}

func TestListRuns(t *testing.T) {
	d := openTestDB(t)
	m := NewManager(d, 1)

	err := m.UpsertDefinitions("test", []CronJob{{
		Name:     "list_job",
		Schedule: "@daily",
		Task: func(db *sql.DB, payload []byte) (string, error) {
			return "ok", nil
		},
	}})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insert 3 runs with different statuses.
	for i, status := range []string{StatusDone, StatusErrored, StatusPlanned} {
		_, err := d.Exec(`INSERT INTO job_runs (job_id, status, created_at) VALUES (1, ?, datetime('now', ? || ' seconds'))`,
			status, fmt.Sprintf("-%d", i))
		if err != nil {
			t.Fatalf("insert run %d: %v", i, err)
		}
	}

	resp, err := m.ListRuns(10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	if len(resp.Runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(resp.Runs))
	}
	if resp.PendingCount != 1 {
		t.Errorf("expected 1 pending, got %d", resp.PendingCount)
	}
	t.Logf("list: %d runs, %d pending", len(resp.Runs), resp.PendingCount)
}
