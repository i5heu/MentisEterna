package jobs

import (
	"context"
	"testing"
)

// TestQueueSurvivesMissingRunsTable covers the fault-propagation paths
// of the queue primitives: with job_definitions intact but job_runs
// gone (mid-migration, corrupted DB), every entry point must return
// errors instead of panicking or reporting success with run id 0.
func TestQueueSurvivesMissingRunsTable(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	if _, err := d.Exec(`DROP TABLE job_runs`); err != nil {
		t.Fatalf("drop job_runs: %v", err)
	}
	m := NewManager(d, 2, 1)

	// Register a definition first so Enqueue gets past its definition
	// lookup and fails at the INSERT (the path under test).
	if err := m.UpsertDefinitions("p", []CronJob{{
		Name: "j",
		Task: func(context.Context, *RunContext) (string, error) { return "", nil },
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Prevents dequeue logging a claim and returning a zero-value
	// claim a worker would then execute against a broken schema.
	if _, ok := m.dequeue(false, 0); ok {
		t.Fatal("dequeue on a missing runs table must not claim")
	}
	if _, ok := m.dequeue(true, 0); ok {
		t.Fatal("fast-lane dequeue on a missing runs table must not claim")
	}

	// Prevents Enqueue reporting a successful enqueue whose INSERT
	// failed underneath — callers would wait on run id 0 forever.
	if _, err := m.Enqueue("p", "j", nil); err == nil {
		t.Fatal("Enqueue on a missing runs table must fail")
	}
}

func TestFinishRunSurvivesAbortedUpdate(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	defID, runID := seedRunningRun(t, m, StatusRunning)

	// A BEFORE UPDATE trigger that RAISEs simulates any low-level
	// write failure (locked row, disk error, trigger guard): the
	// exactly-once finalize must return false and never panic, and
	// the row must stay unfinalized so a later retry can win.
	if _, err := d.Exec(
		`CREATE TRIGGER block_finalize BEFORE UPDATE ON job_runs
		 BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if m.finishRun(runID, defID, jobDefMeta{ID: defID}, StatusDone, "r", "", 1) {
		t.Fatal("finishRun must report failure when the UPDATE is aborted")
	}

	// The ledger must not have been double-bumped by later attempts
	// (the failed attempt rolled back).
	if _, err := d.Exec(`DROP TRIGGER block_finalize`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if !m.finishRun(runID, defID, jobDefMeta{ID: defID}, StatusDone, "r", "", 1) {
		t.Fatal("finishRun must win once the obstruction clears")
	}
	var total int
	if err := d.QueryRow(`SELECT runs_total FROM job_definition_stats WHERE job_id = ?`, defID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("runs_total = %d after a failed + a winning attempt, want 1", total)
	}
}
