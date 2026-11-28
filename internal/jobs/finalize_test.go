package jobs

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// --- exactly-once terminal transitions ---

func TestFinishRunIsExactlyOnce(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	defID, runID := seedRunningRun(t, m, StatusRunning)

	meta := jobDefMeta{ID: defID, PluginID: "t", Name: "p"}

	// The winner writes the terminal state.
	if !m.finishRun(runID, defID, meta, StatusDone, "ok", "", 120) {
		t.Fatal("finishRun on a running row must win")
	}
	// A second finalize (cancel racing the worker, watchdog racing the
	// cancel) must no-op: the stats ledger counts every run once.
	if m.finishRun(runID, defID, meta, StatusErrored, "", "late error", 999) {
		t.Fatal("finishRun on a terminal row must lose (return false)")
	}
	if m.finishRun(runID, defID, meta, StatusCancelled, "", "", 999) {
		t.Fatal("finishRun on a terminal row must lose (return false)")
	}

	// The row keeps the first writer's state, not the last.
	var status, result string
	var duration int64
	if err := d.QueryRow(`SELECT status, COALESCE(result,''), COALESCE(duration_ms,0) FROM job_runs WHERE id = ?`, runID).
		Scan(&status, &result, &duration); err != nil {
		t.Fatal(err)
	}
	if status != StatusDone || result != "ok" || duration != 120 {
		t.Fatalf("run row = %s/%q/%d, want done/ok/120", status, result, duration)
	}

	// The ledger must show exactly one fold: done=1, errored=0,
	// cancelled=0 — a double bump here inflates success rates in the
	// stats view and skews the 24h window.
	var total, done, errored, cancelled int
	if err := d.QueryRow(
		`SELECT runs_total, runs_done, runs_errored, runs_cancelled FROM job_definition_stats WHERE job_id = ?`,
		defID).Scan(&total, &done, &errored, &cancelled); err != nil {
		t.Fatal(err)
	}
	if total != 1 || done != 1 || errored != 0 || cancelled != 0 {
		t.Fatalf("ledger = %d/%d/%d/%d, want 1/1/0/0", total, done, errored, cancelled)
	}
}

func TestFinishRunFromPlannedState(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	defID, runID := seedRunningRun(t, m, StatusPlanned)

	// A planned run cancelled before a worker ever claimed it (the
	// CancelRun fast path can be bypassed by the watchdog's lost-claim
	// sweep) still finalizes cleanly from 'planned'.
	if !m.finishRun(runID, defID, jobDefMeta{ID: defID}, StatusCancelled, "", "Cancelled", 0) {
		t.Fatal("finishRun on a planned row must win")
	}
	status, errText := fetchRun(t, d, runID)
	if status != StatusCancelled || errText != "Cancelled" {
		t.Fatalf("run = %s/%q, want cancelled/Cancelled", status, errText)
	}
}

func TestFinishRunSurvivesBrokenDB(t *testing.T) {
	t.Parallel()
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)

	// Prevents a failed finalize PANICKING the worker: begin errors
	// must return false so the caller can log and move on.
	if bm.finishRun(1, 1, jobDefMeta{ID: 1}, StatusDone, "r", "", 1) {
		t.Fatal("finishRun on a broken DB must report failure (false)")
	}
}

// --- the stats ledger ---

// ledgerRow is the independently-scanned shape of job_definition_stats
// used to compare against an expected model.
type ledgerRow struct {
	RunsTotal     int64
	RunsDone      int64
	RunsErrored   int64
	RunsCancelled int64
	TotalDur      int64
	MinDur        *int64
	MaxDur        *int64
	LastStatus    *string
	LastResult    *string
	LastError     *string
}

func loadLedger(m *Manager, defID int64) (ledgerRow, error) {
	var r ledgerRow
	err := m.db.QueryRow(
		`SELECT runs_total, runs_done, runs_errored, runs_cancelled,
		        total_duration_ms, min_duration_ms, max_duration_ms,
		        last_status, last_result, last_error
		 FROM job_definition_stats WHERE job_id = ?`, defID,
	).Scan(&r.RunsTotal, &r.RunsDone, &r.RunsErrored, &r.RunsCancelled,
		&r.TotalDur, &r.MinDur, &r.MaxDur, &r.LastStatus, &r.LastResult, &r.LastError)
	if err != nil {
		return r, err
	}
	return r, nil
}

// TestBumpStatsLedgerMatchesModel is the core ledger property: after any
// sequence of terminal folds, the row equals a trivially-computed
// aggregate (counts, min/max over positive durations, sum, last-fold
// fields). Catches aggregation drift in the big ON CONFLICT UPDATE.
func TestBumpStatsLedgerMatchesModel(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// *rapid.T does not satisfy testing.TB, so this property
		// builds its own throwaway DB per iteration (deferred close
		// scopes to each closure call = each iteration).
		d, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
		if err != nil {
			rt.Fatal(err)
		}
		defer d.Close()
		d.SetMaxOpenConns(1)
		if err := migrateTestDB(d); err != nil {
			rt.Fatal(err)
		}
		m := NewManager(d, 1, 1)

		res, err := d.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('t', 'p', '')`)
		if err != nil {
			rt.Fatal(err)
		}
		defID, err := res.LastInsertId()
		if err != nil {
			rt.Fatal(err)
		}

		type fold struct {
			status   string
			duration int64
			result   string
			errText  string
		}
		folds := rapid.SliceOf(rapid.Custom(func(r *rapid.T) fold {
			statuses := []string{StatusDone, StatusErrored, StatusCancelled}
			f := fold{
				status:   statuses[rapid.IntRange(0, 2).Draw(r, "status")],
				duration: int64(rapid.IntRange(0, 5000).Draw(r, "duration")),
				result:   "",
				errText:  "",
			}
			// Results only on done, errors only on failures: the
			// ledger must store NULL for empty, and a later empty
			// fold must clear a stale earlier value.
			if f.status == StatusDone && rapid.Bool().Draw(r, "hasResult") {
				f.result = "res-" + strconv.Itoa(rapid.IntRange(0, 9).Draw(r, "i"))
			}
			if f.status == StatusErrored && rapid.Bool().Draw(r, "hasErr") {
				f.errText = "boom-" + strconv.Itoa(rapid.IntRange(0, 9).Draw(r, "e"))
			}
			return f
		})).Draw(rt, "folds")

		// Independent expected model.
		var exp ledgerRow
		var minDur, maxDur *int64
		for _, f := range folds {
			exp.RunsTotal++
			switch f.status {
			case StatusDone:
				exp.RunsDone++
			case StatusErrored:
				exp.RunsErrored++
			case StatusCancelled:
				exp.RunsCancelled++
			}
			if f.duration > 0 {
				exp.TotalDur += f.duration
				if minDur == nil || f.duration < *minDur {
					v := f.duration
					minDur = &v
				}
				if maxDur == nil || f.duration > *maxDur {
					v := f.duration
					maxDur = &v
				}
			}
			exp.LastStatus = &f.status
			exp.LastResult = nil
			if f.result != "" {
				exp.LastResult = &f.result
			}
			exp.LastError = nil
			if f.errText != "" {
				exp.LastError = &f.errText
			}
		}
		exp.MinDur, exp.MaxDur = minDur, maxDur

		// Apply the folds through the real transaction path.
		for _, f := range folds {
			tx, err := m.db.Begin()
			if err != nil {
				rt.Fatal(err)
			}
			if err := bumpStatsTx(tx, defID, f.status, f.duration, f.result, f.errText,
				dbTimestamp(time.Now())); err != nil {
				tx.Rollback()
				rt.Fatalf("bump: %v", err)
			}
			if err := tx.Commit(); err != nil {
				rt.Fatal(err)
			}
		}

		if len(folds) == 0 {
			// No folds → no row at all: the ledger must not carry
			// phantom zero rows for never-run definitions.
			var n int
			if err := d.QueryRow(`SELECT COUNT(*) FROM job_definition_stats`).Scan(&n); err != nil {
				rt.Fatal(err)
			}
			if n != 0 {
				rt.Fatalf("ledger created %d rows with zero folds", n)
			}
			return
		}

		got, err := loadLedger(m, defID)
		if err != nil {
			rt.Fatalf("load ledger: %v", err)
		}
		if diff := cmp.Diff(exp, got); diff != "" {
			rt.Fatalf("ledger mismatch after %d folds (-expected +got):\n%s", len(folds), diff)
		}
	})
}

// --- retry backoff ---

// TestRetryBackoffStaysInJitteredBounds pins the retry schedule: the
// base doubles per attempt from 15s, is capped at 10m, survives the
// shift-overflow region, and the ±20% jitter never escapes
// [0.8·expected, 1.2·expected].
func TestRetryBackoffStaysInJitteredBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		attempt := rapid.IntRange(1, 40).Draw(rt, "attempt")

		base := 15 * time.Second << (attempt - 1)
		expected := base
		if expected <= 0 || expected > 10*time.Minute {
			expected = 10 * time.Minute
		}

		got := retryBackoff(attempt)
		if got < time.Duration(0.8*float64(expected)) || got > time.Duration(1.2*float64(expected)) {
			rt.Fatalf("retryBackoff(%d) = %v, want within [0.8·%v, 1.2·%v]",
				attempt, got, expected, expected)
		}
	})

	// Spot-check the documented anchors: attempt 1 ≈ 15s (a faster
	// first retry would hammer a restarting dependency; a slower one
	// delays user-visible work).
	if d := retryBackoff(1); d < 12*time.Second || d > 18*time.Second {
		t.Fatalf("retryBackoff(1) = %v, want ~15s ±20%%", d)
	}
}

// --- automatic retry policy ---

func TestAutoRetryRespectsBudget(t *testing.T) {
	t.Parallel()

	// The retry budget is the only knob protecting a failing dependency
	// (a down LLM backend, a hung feed) from infinite retry pressure.
	cases := map[string]struct {
		maxRetries int
		retryCount int
		wantRetry  bool
	}{
		// A definition with retries disabled (default) must never
		// auto-requeue — manual retry stays available instead.
		"retries disabled": {maxRetries: 0, retryCount: 0, wantRetry: false},
		// Once every configured retry is spent the run stays
		// terminal; otherwise a poison job loops forever.
		"budget exhausted": {maxRetries: 2, retryCount: 2, wantRetry: false},
		// The common case: first failure with budget left.
		"first failure": {maxRetries: 2, retryCount: 0, wantRetry: true},
		// Mid-sequence failure still has one more attempt coming.
		"penultimate failure": {maxRetries: 2, retryCount: 1, wantRetry: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := openTestDB(t)
			m := NewManager(d, 1, 1)
			defID := mustRegisterDef(t, m)
			runID := insertRunForAuto(t, m, defID, tc.retryCount, StatusErrored)

			meta := jobDefMeta{ID: defID, PluginID: "t", Name: "p", MaxRetries: tc.maxRetries}
			m.autoRetry(dqClaim{runID: runID, defID: defID, payload: []byte(`{}`), retryCount: tc.retryCount}, meta, "boom")

			var retries int
			if err := d.QueryRow(
				`SELECT COUNT(*) FROM job_runs WHERE retry_count = ? AND status = ?`,
				tc.retryCount+1, StatusPlanned).Scan(&retries); err != nil {
				t.Fatal(err)
			}
			if (retries == 1) != tc.wantRetry {
				t.Fatalf("retry rows = %d, wantRetry = %v", retries, tc.wantRetry)
			}
		})
	}
}

func TestAutoRetrySchedulesAnnotatedAttempt(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	defID := mustRegisterDef(t, m)
	// Mirror the real sequence: the run is claimed, executes, fails,
	// and is finalized as errored (which creates the ledger row the
	// auto-retry bump updates).
	runID := insertRunForAuto(t, m, defID, 0, StatusPlanned)
	if !m.finishRun(runID, defID, jobDefMeta{ID: defID, PluginID: "t", Name: "p"}, StatusErrored, "", "boom", 10) {
		t.Fatal("finishRun must finalize the errored run")
	}

	events := make(chan RunEvent, 4)
	m.SetObserver(func(evt RunEvent) {
		if evt.Type == "retried" {
			events <- evt
		}
	})

	meta := jobDefMeta{ID: defID, PluginID: "t", Name: "p", MaxRetries: 3}
	m.autoRetry(dqClaim{runID: runID, defID: defID, payload: []byte(`{"note":1}`), retryCount: 0}, meta, "boom")

	var retryID int64
	var retryCount int
	var nextRun *string
	if err := d.QueryRow(
		`SELECT id, retry_count, next_run_at FROM job_runs WHERE retry_count = 1 AND status = ?`,
		StatusPlanned).Scan(&retryID, &retryCount, &nextRun); err != nil {
		t.Fatalf("scheduled retry run not found: %v", err)
	}
	if retryID == runID || retryCount != 1 {
		t.Fatalf("retry run = id %d attempt %d, want a NEW row with attempt 1", retryID, retryCount)
	}
	if nextRun == nil {
		t.Fatal("retry run must be delayed via next_run_at (never instantly due)")
	}

	// The errored run's note must explain the follow-up attempt so the
	// history row in the UI is self-describing.
	var note string
	if err := d.QueryRow(`SELECT COALESCE(error,'') FROM job_runs WHERE id = ?`, runID).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "auto-retry 1/3") {
		t.Fatalf("errored run note = %q, want the auto-retry 1/3 annotation", note)
	}

	// The ledger counts the automatic retry.
	var autoRetries int
	if err := d.QueryRow(`SELECT COALESCE(auto_retries, 0) FROM job_definition_stats WHERE job_id = ?`, defID).Scan(&autoRetries); err != nil {
		t.Fatal(err)
	}
	if autoRetries != 1 {
		t.Fatalf("auto_retries = %d, want 1", autoRetries)
	}

	// The observer tells the UI immediately (attempt numbering is
	// human 1-based in events).
	select {
	case evt := <-events:
		if evt.Attempt != 2 {
			t.Fatalf("retried event Attempt = %d, want 2", evt.Attempt)
		}
	default:
		t.Fatal("auto-retry emitted no retried event")
	}
}

func TestAutoRetrySkipsWhenDuplicateActive(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)
	defID := mustRegisterDef(t, m)

	// An identical active run already queued (a cron tick racing the
	// failure) must not double-schedule: the unique index dedupes and
	// autoRetry must return before bumping the ledger.
	payload := `{"note":1}`
	if _, _, err := m.insertPlannedRun(defID, &payload, 1, nil); err != nil {
		t.Fatal(err)
	}

	meta := jobDefMeta{ID: defID, PluginID: "t", Name: "p", MaxRetries: 3}
	m.autoRetry(dqClaim{runID: 1, defID: defID, payload: []byte(payload), retryCount: 0}, meta, "boom")

	var n, autoRetries int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runs = %d, want 1 (duplicate must not insert)", n)
	}
	if err := d.QueryRow(`SELECT COALESCE(auto_retries, 0) FROM job_definition_stats WHERE job_id = ?`, defID).Scan(&autoRetries); err == nil && autoRetries != 0 {
		t.Fatalf("auto_retries = %d, want 0 on duplicate skip", autoRetries)
	}
}

func TestAutoRetrySurvivesBrokenDB(t *testing.T) {
	t.Parallel()
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)

	// Prevents a scheduling failure crashing the worker that already
	// errored once: the insert error must be swallowed and logged.
	meta := jobDefMeta{ID: 1, PluginID: "t", Name: "p", MaxRetries: 3}
	bm.autoRetry(dqClaim{runID: 1, defID: 1, payload: []byte(`{}`), retryCount: 0}, meta, "boom")
}

func mustRegisterDef(t testing.TB, m *Manager) int64 {
	t.Helper()
	res, err := m.db.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('t', 'p', '')`)
	if err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// insertRunForAuto inserts a run row in the given status with a fixed
// payload — the status a failing worker leaves behind (errored) or the
// pre-claim state for tests that drive finishRun themselves (planned).
func insertRunForAuto(t *testing.T, m *Manager, defID int64, retryCount int, status string) int64 {
	t.Helper()
	res, err := m.db.Exec(
		`INSERT INTO job_runs (job_id, status, payload, retry_count) VALUES (?, ?, '{}', ?)`,
		defID, status, retryCount)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
