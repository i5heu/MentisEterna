package jobs

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLateSuccessAfterTimeoutKeepsWatchdogVerdict pins the reverse
// watchdog/task ordering: the timeout fires (watchdog sets timedOut,
// finalizes the row as errored, requeues), and only THEN the task
// returns — with a SUCCESS. The worker must honor the timedOut flag,
// discard the stale success, and leave the watchdog's verdict, its
// single requeue, and the ledger untouched. A regressed switch order
// would emit a done event and corrupt the exactly-once counting.
func TestLateSuccessAfterTimeoutKeepsWatchdogVerdict(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)
	m.watchdogInterval = 20 * time.Millisecond // fires soon after the 1s limit

	// The observer log pins the event discipline of the race: exactly
	// one errored event (the watchdog's), no done event ever.
	var mu sync.Mutex
	seen := map[string]int{}
	m.SetObserver(func(evt RunEvent) {
		mu.Lock()
		seen[evt.Type]++
		mu.Unlock()
	})

	taskReturned := make(chan struct{})
	if err := m.UpsertDefinitions("dawdle", []CronJob{{
		Name:       "dawdler",
		Timeout:    time.Second, // sub-second rounds up to 1 s
		MaxRetries: 1,
		Task: func(context.Context, *RunContext) (string, error) {
			// The task ignores cancellation and keeps "working" well
			// past the limit, then returns success. The sleep is the
			// scenario under test (wall-clock timeout), not a sync
			// mechanism: the watchdog fires at ~1.02 s, the task
			// returns at ~1.8 s — a 780 ms margin.
			<-time.After(1800 * time.Millisecond)
			close(taskReturned)
			return "late success", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("dawdle", "dawdler", []byte(`{"k":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The watchdog owns the terminal transition while the task still runs.
	if !waitUntil(3*time.Second, func() bool {
		status, errText := fetchRun(t, d, runID)
		return status == StatusErrored && contains(errText, "timed out")
	}) {
		status, errText := fetchRun(t, d, runID)
		t.Fatalf("watchdog never finalized the run: %q (%q)", status, errText)
	}

	// MaxRetries=1 → exactly one requeued retry, due in the future.
	var retried, dueInFuture int
	if err := d.QueryRow(
		`SELECT COUNT(*),
		        SUM(CASE WHEN next_run_at > strftime('%Y-%m-%dT%H:%M:%fZ','now') THEN 1 ELSE 0 END)
		 FROM job_runs WHERE job_id = (SELECT id FROM job_definitions WHERE plugin_id = 'dawdle' AND name = 'dawdler')
		   AND retry_count = 1 AND status = ?`, StatusPlanned,
	).Scan(&retried, &dueInFuture); err != nil {
		t.Fatalf("retry count: %v", err)
	}
	if retried != 1 || dueInFuture != 1 {
		t.Fatalf("expected exactly 1 future retry, got %d rows (%d future)", retried, dueInFuture)
	}

	// Now the task returns its stale success — and nothing may change.
	select {
	case <-taskReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("task never returned")
	}

	// Grace window for the worker to (wrongly) react to the late
	// return; the assertion is that it does not. 300 ms is two orders
	// of magnitude above the worker's own turnaround.
	time.Sleep(300 * time.Millisecond)

	status, errText := fetchRun(t, d, runID)
	if status != StatusErrored || !contains(errText, "timed out") {
		t.Fatalf("late success overrode the watchdog verdict: %q (%q)", status, errText)
	}
	var resultStr *string
	if err := d.QueryRow(`SELECT result FROM job_runs WHERE id = ?`, runID).Scan(&resultStr); err != nil {
		t.Fatalf("result read: %v", err)
	}
	if resultStr != nil && *resultStr == "late success" {
		t.Fatalf("stale success leaked into the run row: %q", *resultStr)
	}

	// Event discipline: the watchdog's single errored event, never done.
	mu.Lock()
	defer mu.Unlock()
	if seen["errored"] != 1 {
		t.Fatalf("expected exactly 1 errored event, got %d (all: %v)", seen["errored"], seen)
	}
	if seen["done"] != 0 {
		t.Fatalf("late success emitted a done event: %v", seen)
	}

	// Ledger: one errored attempt, one auto-retry — the late success
	// added nothing.
	defID := mustDefinitionID(t, d, "dawdle", "dawdler")
	var total, errored, done, autoRetries int
	if err := d.QueryRow(
		`SELECT runs_total, runs_errored, runs_done, auto_retries FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &errored, &done, &autoRetries); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 1 || errored != 1 || done != 0 || autoRetries != 1 {
		t.Fatalf("ledger after late success = total %d/errored %d/done %d/retries %d, want 1/1/0/1",
			total, errored, done, autoRetries)
	}
}
