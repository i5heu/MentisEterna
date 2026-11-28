package jobs

import (
	"context"
	"testing"
	"time"
)

// --- Stats snapshot ---

func TestStatsOnEmptyDatabase(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 2, 1)

	// A fresh install (or a database whose runs the janitor just
	// pruned) must return a well-formed zero snapshot. SQLite's SUM
	// over zero rows is NULL — an unguarded Scan turns that into an
	// error, which would make /jobs/stats 500 on day one.
	resp, err := m.Stats()
	if err != nil {
		t.Fatalf("Stats on an empty DB must not fail: %v", err)
	}
	if resp.Queue.Planned != 0 || resp.Queue.Running != 0 || resp.Queue.Delayed != 0 {
		t.Fatalf("queue stats = %+v, want all zeros", resp.Queue)
	}
	if resp.Last24h != (WindowStats{}) {
		t.Fatalf("window stats = %+v, want zero", resp.Last24h)
	}
	if resp.Workers.Total != 3 || resp.Workers.Busy != 0 || resp.Workers.Idle != 3 ||
		resp.Workers.FastTotal != 1 || resp.Workers.FastBusy != 0 || resp.Workers.FastIdle != 1 {
		t.Fatalf("worker stats = %+v, want 3 idle incl. 1 fast", resp.Workers)
	}
	if resp.Definitions == nil {
		t.Fatal("Definitions must be a non-nil empty slice (JSON renders [] not null)")
	}
}

func TestStatsCountsWindowQueueAndLedger(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 2, 1)

	// One definition; terminal folds seeded through the real
	// bumpStatsTx path so the ledger matches production shape.
	defID := mustRegisterDef(t, m)
	fold := func(status string, duration int64, result, errText string) {
		t.Helper()
		tx, err := d.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := bumpStatsTx(tx, defID, status, duration, result, errText, dbTimestamp(time.Now())); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	fold(StatusDone, 100, "ok", "")
	fold(StatusDone, 900, "great", "")
	fold(StatusErrored, 700, "", "boom")

	// Window rows are independent of the ledger: seed runs created
	// within the last 24h plus queue states.
	if _, err := insertRawRun(t, d, defID, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE job_runs SET duration_ms = 200 WHERE id = (SELECT MAX(id) FROM job_runs)`); err != nil {
		t.Fatal(err)
	}
	if _, err := insertRawRun(t, d, defID, StatusErrored, ""); err != nil {
		t.Fatal(err)
	}
	// A running row and a delayed planned row fill the queue counters.
	if _, err := insertRawRun(t, d, defID, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour).UTC()
	if _, _, err := m.insertPlannedRun(defID, nil, 0, &due); err != nil {
		t.Fatal(err)
	}

	resp, err := m.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	// 24h window: the two freshly-created terminal runs count, with
	// the average duration taken from the done one.
	if resp.Last24h.Done != 1 || resp.Last24h.Errored != 1 || resp.Last24h.Started != 2 {
		t.Fatalf("window = %+v, want 1 done / 1 errored / 2 started", resp.Last24h)
	}
	if resp.Last24h.DoneAvgMS != 200 {
		t.Fatalf("done_avg_ms = %d, want 200", resp.Last24h.DoneAvgMS)
	}

	// Queue depth: one planned (delayed — due in the future) plus one
	// running.
	if resp.Queue.Planned != 1 || resp.Queue.Delayed != 1 || resp.Queue.Running != 1 {
		t.Fatalf("queue = %+v, want planned=1 delayed=1 running=1", resp.Queue)
	}

	// Per-definition ledger: 3 folds → 2 done + 1 errored, durations
	// [100, 900] for done (avg 500), last fold errored with NULL result
	// (the earlier "great" result must not stick around).
	if len(resp.Definitions) != 1 {
		t.Fatalf("definitions = %d, want 1", len(resp.Definitions))
	}
	ds := resp.Definitions[0]
	if ds.RunsTotal != 3 || ds.RunsDone != 2 || ds.RunsErrored != 1 || ds.RunsCancelled != 0 {
		t.Fatalf("definition ledger = %+v", ds)
	}
	if ds.SuccessRate < 0.66 || ds.SuccessRate > 0.67 {
		t.Fatalf("success_rate = %v, want 2/3", ds.SuccessRate)
	}
	// The ledger's duration sum covers ALL folds (100 + 900 + 700), so
	// the average is over done+errored attempts: 1700/3.
	if ds.AvgDurationMS != 566 || ds.MinDurationMS == nil || *ds.MinDurationMS != 100 ||
		ds.MaxDurationMS == nil || *ds.MaxDurationMS != 900 {
		t.Fatalf("durations avg=%d min=%v max=%v, want 566/100/900", ds.AvgDurationMS, ds.MinDurationMS, ds.MaxDurationMS)
	}
	if ds.LastStatus == nil || *ds.LastStatus != StatusErrored {
		t.Fatalf("last_status = %v, want errored", ds.LastStatus)
	}
	if ds.LastResult != nil {
		t.Fatalf("last_result = %v, want nil (NULL must clear stale results)", *ds.LastResult)
	}
	if ds.LastError == nil || *ds.LastError != "boom" {
		t.Fatalf("last_error = %v, want boom", ds.LastError)
	}
	if ds.LastFinishedAt == nil {
		t.Fatal("last_finished_at must be set from the ledger")
	}
	// Live counters: the definition shows its running/queued work.
	if ds.Running != 1 || ds.Queued != 1 {
		t.Fatalf("live counters running=%d queued=%d, want 1/1", ds.Running, ds.Queued)
	}
	if ds.PluginID != "t" || ds.Name != "p" {
		t.Fatalf("definition identity = %s/%s", ds.PluginID, ds.Name)
	}
}

func TestStatsSplitsBusyWorkersByLane(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	// Two normal workers, two fast. Normal workers serve every
	// priority, so the fast lane only engages under saturation: both
	// normal workers are parked on bulk work while the interactive job
	// still runs — it must land on a fast worker.
	m := newTestManager(t, d)
	m.workers = 2
	m.fastWorker = 2

	started := make(chan string, 3)
	release := make(chan struct{})
	track := func(name string) func(context.Context, *RunContext) (string, error) {
		return func(ctx context.Context, _ *RunContext) (string, error) {
			started <- name
			select {
			case <-release:
				return name, nil
			case <-ctx.Done():
				// Must observe the shutdown cancel so Stop does not
				// wait out its 30 s timeout in this test.
				return name, nil
			}
		}
	}
	if err := m.UpsertDefinitions("test", []CronJob{
		// Fast-lane task: blocking until released.
		{Name: "interactive", Priority: PriorityHigh, Task: track("interactive")},
		// Normal-lane tasks: also blocking.
		{Name: "bulk-a", Task: track("bulk-a")},
		{Name: "bulk-b", Task: track("bulk-b")},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	startManager(t, m)

	// Order matters: normal workers serve every priority (high first),
	// so the fast lane only engages once both normal workers are
	// already busy. Park them on bulk work first — only then can the
	// interactive job provably land on a fast worker.
	for _, job := range []string{"bulk-a", "bulk-b"} {
		if _, err := m.Enqueue("test", job, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Event-driven: wait until both bulk tasks are claimed.
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("bulk task %d never started", i+1)
		}
	}
	if _, err := m.Enqueue("test", "interactive", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-started:
		if name != "interactive" {
			t.Fatalf("unexpected task %q started", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interactive task never started on the fast lane")
	}

	resp, err := m.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// Both normal workers busy with bulk work and the interactive job
	// running anyway proves the fast pool engaged: Busy=2, FastBusy=1.
	if resp.Workers.Busy != 2 || resp.Workers.FastBusy != 1 {
		t.Fatalf("busy split = %+v, want 2 normal + 1 fast busy", resp.Workers)
	}
	if resp.Workers.Idle != 1 || resp.Workers.FastIdle != 1 {
		t.Fatalf("idle split = %+v, want 1 normal + 1 fast idle", resp.Workers)
	}

	// A saturated queue must not block the stats endpoint: it answered
	// while all tasks are wedged.
	close(release)
}

func TestStatsSurfacesSchemaErrors(t *testing.T) {
	t.Parallel()
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)

	// The options view renders /jobs/stats as a page header — a schema
	// error must surface as an error (toast), not as a silently empty
	// dashboard.
	if _, err := bm.Stats(); err == nil {
		t.Fatal("Stats on a broken DB must fail")
	}
}

// --- watchdog: delayed-retry wakeups ---

func TestWakeDueRetriesKicksIdleWorkers(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	m := NewManager(d, 1, 1)

	defID := mustRegisterDef(t, m)
	// A delayed auto-retry whose due time just passed: the watchdog's
	// sweep must kick an idle worker so the retry starts without
	// waiting for the (slow) fallback poll.
	past := time.Now().Add(-time.Minute).UTC()
	if _, _, err := m.insertPlannedRun(defID, nil, 1, &past); err != nil {
		t.Fatal(err)
	}

	m.wakeDueRetries()
	select {
	case <-m.notify:
	default:
		t.Fatal("wakeDueRetries did not kick the workers for a due retry")
	}

	// Nothing due → no kick (the notify buffer must stay empty: a
	// spurious kick per tick would waste worker wakeups). Fresh DB so
	// the first manager's due row cannot leak into this check.
	d2 := openTestDB(t)
	m2 := NewManager(d2, 1, 1)
	defID2 := mustRegisterDef(t, m2)
	_, _, err := m2.insertPlannedRun(defID2, nil, 1,
		timePtr(time.Now().Add(time.Hour).UTC()))
	if err != nil {
		t.Fatal(err)
	}
	m2.wakeDueRetries()
	select {
	case <-m2.notify:
		t.Fatal("wakeDueRetries kicked workers with nothing due")
	default:
	}
}

func TestWakeDueRetriesSurvivesBrokenDB(t *testing.T) {
	t.Parallel()
	bd := brokenDB(t)
	bm := NewManager(bd, 1, 1)

	// The watchdog tick must never panic on schema errors — it would
	// take the whole manager goroutine down.
	bm.wakeDueRetries()
}
