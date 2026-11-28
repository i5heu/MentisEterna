// Package jobs provides a robust, observable, multithreaded job system
// backed by SQLite.
//
// Layout of this package:
//
//	jobs.go       public types (CronJob, RunContext, Run, RunEvent), the
//	              Manager, its lifecycle (Start/Stop) and the observer bus
//	definitions.go  definition registration, the builtin janitor, zombie
//	              recovery, and the definition lookup cache
//	queue.go       enqueue / retry / cancel / list — the run queue API
//	scheduler.go   per-definition cron tickers (never pile up ticks)
//	worker.go      worker lanes, the atomic dequeue, and task execution
//	finalize.go    guarded terminal transitions + stats ledger bumps,
//	              automatic retries with backoff
//	progress.go    the throttled per-run progress reporter
//	watchdog.go    timeouts, lost-claim cleanup, delayed-retry wakeups
//	stats.go       the /jobs/stats snapshot (worker, queue, per-definition)
//
// Architecture (v2):
//
//	enqueue ──┐                                  ┌─ normal workers (all priorities)
//	          ├── job_runs (planned) ── wake ────┤
//	scheduler ─┘        via notify channel       └─ fast workers   (priority ≥ 1 only —
//	                                                 interactive jobs never starve
//	                                                 behind slow bulk work)
//
// Key properties:
//
//   - Event-driven wakeup: enqueues and scheduler ticks kick a notify
//     channel; workers start new runs essentially instantly and fall
//     back to a slow poll (2 s) that picks up delayed retries.
//   - Priority dequeue: priority DESC, created_at ASC; per-definition
//     max_parallel caps how many runs of the same job may run at once.
//   - Automatic retries: failed runs are re-queued with exponential
//     backoff + jitter (next_run_at), up to the definition's max
//     retries; cancellation-induced errors are never auto-retried.
//   - Watchdog: enforces per-definition timeouts and marks runs whose
//     claim was lost (running rows not tracked by this manager) as
//     errored — wedged runs never stay "running" forever.
//   - Progress: tasks report (done, total, message) through
//     RunContext.Progress; the manager throttles DB writes and observer
//     events; progress_at doubles as the run's heartbeat.
//   - Stats: the job_definition_stats ledger aggregates every finish
//     (success rate, durations, last result) and survives the 30-day
//     run-retention janitor.
//   - Exactly-once terminal transition: every terminal status write is a
//     single guarded UPDATE (WHERE status IN ('running','planned')) in
//     one transaction with its stats bump — whichever path wins a race
//     (worker, cancel, timeout, watchdog), the run is finalized and
//     counted exactly once.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Status values for job runs.
const (
	StatusPlanned   = "planned"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusErrored   = "errored"
	StatusCancelled = "cancelled"
)

// Priority lanes. High-priority (interactive) jobs get a dedicated fast
// worker pool so bulk work can never starve them.
const (
	PriorityLow    = -1
	PriorityNormal = 0
	PriorityHigh   = 1
)

// TaskFunc is the signature every job task implements. The context is
// cancelled when the run is cancelled, times out, or the server shuts
// down; well-behaved tasks check it between steps. RunContext carries
// the DB handle, payload, identity, and the Progress reporter.
type TaskFunc func(ctx context.Context, rt *RunContext) (string, error)

// CronJob describes a background task registered by a plugin or the
// server.
type CronJob struct {
	// Name is the unique job name within the registering plugin.
	Name string
	// Schedule: "@every 15m", "@hourly", "@daily"; empty = ad-hoc only
	// (triggered exclusively via Enqueue).
	Schedule string
	// Priority: PriorityHigh jobs run on the dedicated fast lane so
	// interactive work (titles, tags, embeddings) never queues behind
	// bulk jobs. Default PriorityNormal.
	Priority int
	// MaxParallel caps concurrently running runs of this definition
	// (<=0 → 1).
	MaxParallel int
	// Timeout is a hard wall-clock cap per run; 0 = none. The watchdog
	// force-finalizes (and retries, if retries remain) expired runs.
	Timeout time.Duration
	// MaxRetries is the number of automatic retries (with backoff)
	// after an error; 0 = none (manual retry stays available).
	MaxRetries int
	// Task is the function executed for each run.
	Task TaskFunc
}

// RunContext is handed to every task: identity + inputs + progress. It
// is safe to keep and use from goroutines the task spawns.
type RunContext struct {
	// DB is the app database.
	DB *sql.DB
	// RunID is the job_runs row id of this execution.
	RunID int64
	// PluginID and JobName identify the definition.
	PluginID string
	JobName  string
	// Payload is the opaque run payload (nil for cron runs).
	Payload []byte
	// Progress reports live progress for this run. The manager
	// throttles writes (~2/s per run plus a 10 s heartbeat); it never
	// blocks and swallows its own errors.
	Progress func(done, total int, message string)
}

// Run represents a single execution of a job definition.
type Run struct {
	ID              int64           `json:"id"`
	PluginID        string          `json:"plugin_id"`
	JobName         string          `json:"job_name"`
	Status          string          `json:"status"`
	Priority        int             `json:"priority"`
	RetryCount      int             `json:"retry_count"`
	WorkerID        int             `json:"worker_id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	DurationMS      *int64          `json:"duration_ms,omitempty"`
	ProgressDone    *int            `json:"progress_done,omitempty"`
	ProgressTotal   *int            `json:"progress_total,omitempty"`
	ProgressMessage *string         `json:"progress_message,omitempty"`
	Error           *string         `json:"error,omitempty"`
	Result          *string         `json:"result,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// RunEvent is a lifecycle notification for a job run, delivered to the
// observer registered via SetObserver. Type is one of: enqueued,
// running, progress, done, errored, cancelled, retried.
type RunEvent struct {
	Type            string  `json:"type"`
	RunID           int64   `json:"run_id"`
	PluginID        string  `json:"plugin_id"`
	JobName         string  `json:"job_name"`
	Status          string  `json:"status"`
	Attempt         int     `json:"attempt,omitempty"`
	WorkerID        int     `json:"worker_id,omitempty"`
	Result          *string `json:"result,omitempty"`
	Error           *string `json:"error,omitempty"`
	DurationMS      *int64  `json:"duration_ms,omitempty"`
	ProgressDone    *int    `json:"progress_done,omitempty"`
	ProgressTotal   *int    `json:"progress_total,omitempty"`
	ProgressMessage *string `json:"progress_message,omitempty"`
}

// ListResponse is the response for GET /jobs.
type ListResponse struct {
	Runs         []Run `json:"runs"`
	PendingCount int   `json:"pending_count"`
}

// activeRun tracks a run claimed by one of this manager's workers: the
// watchdog uses it to detect lost claims, CancelRun to cancel the task
// context, and timeouts are measured from started. timedOut is set by
// the watchdog BEFORE cancelling, so the worker's finalize path knows
// the watchdog owns the terminal write (and the row is recorded as a
// timeout, not a bare "context canceled").
type activeRun struct {
	runID    int64
	defID    int64
	workerID int
	fast     bool
	started  time.Time
	cancel   context.CancelFunc
	timedOut atomic.Bool
}

// jobDefMeta caches definition metadata for logging, dequeue, and
// retry decisions without a DB round-trip.
type jobDefMeta struct {
	ID          int64
	PluginID    string
	Name        string
	Schedule    string
	Priority    int
	MaxParallel int
	Timeout     time.Duration
	MaxRetries  int
}

// Manager owns the job system lifecycle: definitions, schedulers, the
// watchdog, and the worker lanes. All public methods are safe for
// concurrent use.
type Manager struct {
	db         *sql.DB
	workers    int
	fastWorker int

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// notify wakes idle workers immediately after an enqueue or a
	// scheduler tick (buffered 1, non-blocking kick).
	notify chan struct{}

	// pollInterval is the fallback poll that also picks up delayed
	// retries; tests shrink it.
	pollInterval time.Duration

	// watchdogInterval bounds how quickly timeouts are enforced and
	// lost claims cleared; tests shrink it.
	watchdogInterval time.Duration

	// tasks maps job_definition.id → task function; defs caches the
	// same definitions' metadata.
	tasks   map[int64]TaskFunc
	defs    map[int64]jobDefMeta
	tasksMu sync.RWMutex

	// active tracks runs claimed by this manager's workers.
	active   map[int64]*activeRun
	activeMu sync.Mutex

	observerMu sync.RWMutex
	observer   func(RunEvent)

	// progressMinInterval and progressHeartbeat bound the progress
	// reporter; tests shrink them.
	progressMinInterval time.Duration
	progressHeartbeat   time.Duration
}

// NewManager creates a new job Manager. workers run at every priority;
// fastWorkers is a dedicated pool that only serves PriorityHigh runs,
// so interactive jobs never wait behind slow bulk work. Start performs
// zombie recovery (resetting stuck "running" jobs).
func NewManager(db *sql.DB, workers, fastWorkers int) *Manager {
	if workers < 1 {
		workers = 1
	}
	if fastWorkers < 1 {
		fastWorkers = 1
	}
	return &Manager{
		db:                  db,
		workers:             workers,
		fastWorker:          fastWorkers,
		stop:                make(chan struct{}),
		done:                make(chan struct{}),
		notify:              make(chan struct{}, 1),
		pollInterval:        2 * time.Second,
		watchdogInterval:    10 * time.Second,
		progressMinInterval: 500 * time.Millisecond,
		progressHeartbeat:   10 * time.Second,
		tasks:               make(map[int64]TaskFunc),
		defs:                make(map[int64]jobDefMeta),
		active:              make(map[int64]*activeRun),
	}
}

// WorkerCount returns the configured number of normal job workers.
func (m *Manager) WorkerCount() int {
	if m == nil {
		return 0
	}
	return m.workers
}

// FastWorkerCount returns the size of the dedicated high-priority pool.
func (m *Manager) FastWorkerCount() int {
	if m == nil {
		return 0
	}
	return m.fastWorker
}

// SetObserver registers a callback for job lifecycle notifications.
// The callback must be safe for concurrent use and must not block.
func (m *Manager) SetObserver(fn func(RunEvent)) {
	if m == nil {
		return
	}
	m.observerMu.Lock()
	m.observer = fn
	m.observerMu.Unlock()
}

// Start begins the job system: builtin jobs, zombie recovery, scheduler
// goroutines, the watchdog, and the worker lanes. It assumes
// UpsertDefinitions has already been called for all plugins and system
// job sets.
func (m *Manager) Start() error {
	if err := m.registerBuiltinJobs(); err != nil {
		return fmtError("register builtin jobs", err)
	}

	if err := m.zombieRecovery(); err != nil {
		return err
	}

	log.Printf("jobs: starting manager workers=%d fast_workers=%d registered_definitions=%d",
		m.workers, m.fastWorker, m.registeredDefinitionCount())

	// Launch a scheduler goroutine for every enabled cron definition.
	// Two-phase (collect rows first, then process): keeps queries off
	// the connection while the cursor is open — nested
	// query-while-Rows-here deadlocks single-connection databases.
	rows, err := m.db.Query(`SELECT id, schedule FROM job_definitions WHERE enabled = 1`)
	if err != nil {
		return fmtError("load job definitions", err)
	}
	type schedDef struct {
		id       int64
		schedule string
	}
	var cronDefs []schedDef
	for rows.Next() {
		var sd schedDef
		if err := rows.Scan(&sd.id, &sd.schedule); err != nil {
			rows.Close()
			return fmtError("scan job definition", err)
		}
		cronDefs = append(cronDefs, sd)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmtError("iterate job definitions", err)
	}

	var wg sync.WaitGroup
	for _, sd := range cronDefs {
		// Empty schedule means ad-hoc only (no cron), skip scheduler.
		if sd.schedule == "" {
			continue
		}
		if !m.hasTask(sd.id) {
			log.Printf("jobs: scheduler skipped for unregistered definition def_id=%d schedule=%q", sd.id, sd.schedule)
			continue
		}
		wg.Add(1)
		go m.runScheduler(sd.id, sd.schedule, &wg)
	}

	// Watchdog: timeouts, lost claims, delayed-retry wakeups.
	wg.Add(1)
	go m.runWatchdog(&wg)

	// Worker lanes: normal workers serve everything (high priority
	// first); fast workers only serve PriorityHigh runs.
	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go m.runWorker(i, false, &wg)
	}
	for i := 0; i < m.fastWorker; i++ {
		wg.Add(1)
		go m.runWorker(1000+i, true, &wg)
	}

	go func() {
		wg.Wait()
		close(m.done)
	}()

	return nil
}

// Stop signals all goroutines to stop, cancels the contexts of running
// tasks, and waits for workers to finish (with a 30 s timeout). Tasks
// that observe their context finalize as cancelled; leftover "running"
// rows are reset to planned by the next startup's zombie recovery. Stop
// is idempotent.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)

		// Cancel running tasks so wedged tasks let go promptly; the
		// workers then finalize the runs as cancelled (no auto-retry —
		// cron jobs re-enqueue on their next tick anyway).
		m.activeMu.Lock()
		for _, ar := range m.active {
			ar.cancel()
		}
		m.activeMu.Unlock()
		m.kick()

		select {
		case <-m.done:
			log.Printf("jobs: all workers stopped cleanly")
		case <-time.After(30 * time.Second):
			log.Printf("jobs: timeout waiting for workers to stop")
		}
	})
}
