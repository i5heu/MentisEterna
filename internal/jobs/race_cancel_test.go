package jobs

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCancelRunLosesRaceWhenRunFinishesFirst drives the exact
// interleaving the guarded cancel UPDATE exists for: CancelRun has read
// status='running', the "worker" finalizes the run (done) while the
// cancel is parked mid-transaction, and the guarded UPDATE then
// matches nothing. The cancel must lose truthfully, and the losing
// path must not double-bump the stats ledger.
func TestCancelRunLosesRaceWhenRunFinishesFirst(t *testing.T) {
	t.Parallel()
	d, _ := openFileTestDB(t, 3)
	m := newTestManager(t, d) // not started: no worker interferes

	res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('race', 'finisher', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	defID, _ := res.LastInsertId()
	res, err = d.Exec(`INSERT INTO job_runs (job_id, status, started_at)
		VALUES (?, 'running', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, defID)
	if err != nil {
		t.Fatalf("insert running run: %v", err)
	}
	runID, _ := res.LastInsertId()

	// Take the write lock so the cancel parks inside its UPDATE —
	// AFTER its status SELECT has read 'running'.
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin parking tx: %v", err)
	}
	if _, err := tx.Exec(`UPDATE job_definitions SET schedule = schedule WHERE id = ?`, defID); err != nil {
		tx.Rollback()
		t.Fatalf("park write lock: %v", err)
	}

	cancelErr := make(chan error, 1)
	go func() { cancelErr <- m.CancelRun(runID) }()

	// Handshake: both pool connections checked out (InUse==2) means the
	// cancel has passed its status SELECT — which read 'running', since
	// the finish below is still uncommitted — and is parked in its
	// guarded UPDATE behind the write lock. No sleep, no luck involved.
	if !waitUntil(2*time.Second, func() bool {
		st := d.Stats()
		return st.OpenConnections >= 2 && st.InUse == 2
	}) {
		tx.Rollback()
		t.Fatal("cancel never reached its guarded UPDATE")
	}

	// The "worker" wins the race: the row flips to done while the
	// cancel is parked.
	if _, err := tx.Exec(`UPDATE job_runs SET status = 'done',
		finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), result = 'raced ahead' WHERE id = ?`, runID); err != nil {
		tx.Rollback()
		t.Fatalf("finish run inside parking tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit finish: %v", err)
	}

	select {
	case err := <-cancelErr:
		if err == nil {
			t.Fatal("cancel must lose the race with a truthful error, got nil")
		}
		// Truthful loss on either branch: normally the status read won
		// the commit race and the guarded UPDATE matches nothing
		// ("finished before"); under extreme scheduler load the status
		// SELECT can run after the commit and hit the plain status
		// guard instead. Both are honest losses — and both leave the
		// row and ledger untouched (asserted below).
		msg := err.Error()
		if !contains(msg, "finished before") && !contains(msg, "can only cancel") {
			t.Fatalf("unexpected cancel error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not return after the run finished")
	}

	// The row keeps the worker's terminal state, uncorrupted.
	status, errText := fetchRun(t, d, runID)
	if status != StatusDone || errText != "" {
		t.Fatalf("run after lost cancel = %q (%q), want done", status, errText)
	}
	// Exactly-once: the losing cancel bumped no stats.
	var bumped int
	if err := d.QueryRow(
		`SELECT COUNT(*) FROM job_definition_stats WHERE job_id = ? AND runs_total > 0`, defID,
	).Scan(&bumped); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if bumped != 0 {
		t.Fatalf("losing cancel must not bump stats, found %d ledger rows", bumped)
	}
}

// TestConcurrentCancelsProduceExactlyOneWinner runs two CancelRun calls
// against the same running row with both parked past their status
// reads. SQLite serializes the guarded UPDATEs: exactly one cancel
// commits, the loser reports the race truthfully, and the ledger
// counts the cancellation once.
func TestConcurrentCancelsProduceExactlyOneWinner(t *testing.T) {
	t.Parallel()
	d, _ := openFileTestDB(t, 3)
	m := newTestManager(t, d) // not started

	res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('race', 'double', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	defID, _ := res.LastInsertId()
	res, err = d.Exec(`INSERT INTO job_runs (job_id, status, started_at)
		VALUES (?, 'running', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, defID)
	if err != nil {
		t.Fatalf("insert running run: %v", err)
	}
	runID, _ := res.LastInsertId()

	// Park the write lock FIRST, then launch both cancels: each one's
	// status SELECT is guaranteed to read 'running' (the lock only
	// blocks their UPDATEs), so both necessarily enter the guarded
	// UPDATE believing the run is cancellable.
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin parking tx: %v", err)
	}
	if _, err := tx.Exec(`UPDATE job_definitions SET schedule = schedule WHERE id = ?`, defID); err != nil {
		tx.Rollback()
		t.Fatalf("park write lock: %v", err)
	}

	const cancels = 2
	errs := make(chan error, cancels)
	for range cancels {
		go func() { errs <- m.CancelRun(runID) }()
	}

	// All three connections checked out: the parking tx plus both
	// cancels parked in their guarded UPDATEs — both have necessarily
	// completed their status reads.
	if !waitUntil(2*time.Second, func() bool {
		return d.Stats().InUse == 3
	}) {
		tx.Rollback()
		t.Fatalf("both cancels never parked: %+v", d.Stats())
	}

	if err := tx.Commit(); err != nil { // release the write lock
		t.Fatalf("commit parking tx: %v", err)
	}

	var winners, losers int
	for range cancels {
		select {
		case err := <-errs:
			if err == nil {
				winners++
			} else {
				losers++
				// Either guard branch is a truthful loss: the twin
				// committed first, or the row's terminal state was
				// read on a fresh pass.
				msg := err.Error()
				if !contains(msg, "finished before") && !contains(msg, "can only cancel") {
					t.Fatalf("unexpected losing-cancel error: %v", err)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a cancel never returned")
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("expected exactly one winning cancel, got %d winners / %d losers", winners, losers)
	}

	// The run is cancelled once, by the winner, with the winner's note.
	status, errText := fetchRun(t, d, runID)
	if status != StatusCancelled || errText == "" {
		t.Fatalf("run after double cancel = %q (%q), want cancelled with note", status, errText)
	}
	var total, cancelled int
	if err := d.QueryRow(
		`SELECT runs_total, runs_cancelled FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &cancelled); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 1 || cancelled != 1 {
		t.Fatalf("ledger after double cancel = total %d/cancelled %d, want 1/1 — exactly-once violated", total, cancelled)
	}
}

// TestTaskReturningSuccessAfterCancelDoesNotOverrideIt closes the
// worker-side of the race: the task ignores cancellation and returns
// success AFTER CancelRun already committed the terminal state. The
// worker's late finishRun must no-op against the guarded row — the
// cancellation verdict, its error text, and the ledger stay intact,
// and no done event may fire for a run the user cancelled.
func TestTaskReturningSuccessAfterCancelDoesNotOverrideIt(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := newTestManager(t, d)

	// The observer log pins the event discipline of the race.
	var mu sync.Mutex
	seen := map[string]int{}
	m.SetObserver(func(evt RunEvent) {
		mu.Lock()
		seen[evt.Type]++
		mu.Unlock()
	})

	taskStarted := make(chan struct{})
	taskReturned := make(chan struct{})
	release := make(chan struct{})
	if err := m.UpsertDefinitions("race", []CronJob{{
		Name: "succeeder",
		Task: func(ctx context.Context, _ *RunContext) (string, error) {
			close(taskStarted)
			// The task only unblocks once the cancel is committed —
			// the ctx.Done arm fires first (CancelRun commits before
			// cancelling), both arms lead to a SUCCESS return.
			select {
			case <-release:
			case <-ctx.Done():
			}
			close(taskReturned)
			return "late success", nil
		},
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	runID, err := m.Enqueue("race", "succeeder", []byte(`{"k":1}`))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-taskStarted

	// The user cancel wins the terminal transition.
	if err := m.CancelRun(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	close(release) // let the task return its (now stale) success
	<-taskReturned

	// Positive handshake: the worker processed the late return when it
	// emits its own cancelled event (CancelRun's is the first). Only
	// then is the "nothing changed" assertion meaningful — a regressed
	// switch would emit done here and never reach 2.
	if !waitUntil(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["cancelled"] >= 2
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("worker never processed the late return (events: %v)", seen)
	}
	mu.Lock()
	if seen["done"] != 0 || seen["errored"] != 0 {
		mu.Unlock()
		t.Fatalf("late success leaked done/errored events: %v", seen)
	}
	mu.Unlock()

	// The row keeps the cancel's verdict and its exact error text —
	// a winning worker-side finishRun would have overwritten it with
	// its own 'Cancelled' note.
	status, errText := fetchRun(t, d, runID)
	if status != StatusCancelled {
		t.Fatalf("late success overrode the cancel: status %q", status)
	}
	if !contains(errText, "Cancelled by user") {
		t.Fatalf("cancel verdict corrupted by late success: %q", errText)
	}

	// Exactly-once ledger: one cancellation, no done, no errored — and
	// no auto-retry was scheduled for a user-cancelled run.
	defID := mustDefinitionID(t, d, "race", "succeeder")
	var total, cancelled, done, errored int
	if err := d.QueryRow(
		`SELECT runs_total, runs_cancelled, runs_done, runs_errored FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&total, &cancelled, &done, &errored); err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if total != 1 || cancelled != 1 || done != 0 || errored != 0 {
		t.Fatalf("ledger after late success = total %d/cancelled %d/done %d/errored %d, want 1/1/0/0",
			total, cancelled, done, errored)
	}
	var planned int
	if err := d.QueryRow(
		`SELECT COUNT(*) FROM job_runs WHERE job_id = ? AND status = ?`, defID, StatusPlanned,
	).Scan(&planned); err != nil {
		t.Fatalf("planned count: %v", err)
	}
	if planned != 0 {
		t.Fatalf("a cancelled run spawned %d retry run(s)", planned)
	}
}
