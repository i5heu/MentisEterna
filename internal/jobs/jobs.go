// Package jobs provides a robust, observable job system backed by SQLite.
//
// Key features:
//   - Atomic dequeue via UPDATE ... RETURNING (no two workers grab the same job)
//   - Zombie recovery on startup (reset stuck "running" jobs to "planned")
//   - Data retention via built-in janitor (deletes runs older than 30 days)
//   - Job payloads for future user-triggered background work
//   - SQLite WAL + busy timeout for concurrent write resilience
package jobs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
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

// CronJob describes a background task registered by a plugin.
type CronJob struct {
	Name     string
	Schedule string
	Task     func(db *sql.DB, payload []byte) (string, error)
}

// Run represents a single execution of a job definition.
type Run struct {
	ID         int64           `json:"id"`
	PluginID   string          `json:"plugin_id"`
	JobName    string          `json:"job_name"`
	Status     string          `json:"status"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Error      *string         `json:"error,omitempty"`
	Result     *string         `json:"result,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// RunEvent is a lightweight lifecycle notification for a job run.
type RunEvent struct {
	Type     string  `json:"type"`
	RunID    int64   `json:"run_id"`
	PluginID string  `json:"plugin_id"`
	JobName  string  `json:"job_name"`
	Status   string  `json:"status"`
	Result   *string `json:"result,omitempty"`
	Error    *string `json:"error,omitempty"`
}

// ListResponse is the response for GET /jobs.
type ListResponse struct {
	Runs         []Run `json:"runs"`
	PendingCount int   `json:"pending_count"`
}

type jobDefMeta struct {
	ID       int64
	PluginID string
	Name     string
	Schedule string
}

// Manager manages the job system lifecycle.
type Manager struct {
	db      *sql.DB
	workers int
	stop    chan struct{}
	done    chan struct{}

	// tasks maps job_definition.id → task function.
	// defs caches job definition metadata for logging and scheduler startup.
	tasks   map[int64]func(*sql.DB, []byte) (string, error)
	defs    map[int64]jobDefMeta
	tasksMu sync.RWMutex

	observerMu sync.RWMutex
	observer   func(RunEvent)
}

// NewManager creates a new job Manager with the given number of workers.
// It performs zombie recovery (resetting stuck "running" jobs) immediately.
func NewManager(db *sql.DB, workers int) *Manager {
	if workers < 1 {
		workers = 1
	}
	return &Manager{
		db:      db,
		workers: workers,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		tasks:   make(map[int64]func(*sql.DB, []byte) (string, error)),
		defs:    make(map[int64]jobDefMeta),
	}
}

// WorkerCount returns the configured number of concurrent job workers.
func (m *Manager) WorkerCount() int {
	if m == nil {
		return 0
	}
	return m.workers
}

// SetObserver registers a callback for job lifecycle notifications.
func (m *Manager) SetObserver(fn func(RunEvent)) {
	if m == nil {
		return
	}
	m.observerMu.Lock()
	m.observer = fn
	m.observerMu.Unlock()
}

// UpsertDefinitions upserts job_definitions rows from the provided cron jobs.
// Returns a map from plugin_id+name to the definition ID.
func (m *Manager) UpsertDefinitions(pluginID string, jobs []CronJob) error {
	if err := m.upsertDefs(pluginID, jobs); err != nil {
		return err
	}
	return nil
}

// RegisterAdHoc registers a job that is only triggered on-demand via Enqueue
// (not on a cron schedule). The task function is stored in the in-memory map
// and the definition is persisted so Enqueue can find it.
func (m *Manager) RegisterAdHoc(pluginID string, jobs []CronJob) error {
	// Force empty schedule for ad-hoc jobs (no cron ticks).
	for i := range jobs {
		jobs[i].Schedule = ""
	}
	return m.upsertDefs(pluginID, jobs)
}

func (m *Manager) upsertDefs(pluginID string, jobs []CronJob) error {
	for _, job := range jobs {
		_, err := m.db.Exec(
			`INSERT INTO job_definitions (plugin_id, name, schedule)
			 VALUES (?, ?, ?)
			 ON CONFLICT(plugin_id, name) DO UPDATE SET schedule = excluded.schedule`,
			pluginID, job.Name, job.Schedule,
		)
		if err != nil {
			return fmt.Errorf("upsert job definition %s/%s: %w", pluginID, job.Name, err)
		}

		// Always look up the canonical definition ID after the upsert.
		// LastInsertId is not reliable for SQLite ON CONFLICT DO UPDATE paths,
		// and using it can register tasks under the wrong in-memory ID.
		var id int64
		err = m.db.QueryRow(
			`SELECT id FROM job_definitions WHERE plugin_id = ? AND name = ?`,
			pluginID, job.Name,
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("lookup job definition %s/%s: %w", pluginID, job.Name, err)
		}

		m.tasksMu.Lock()
		m.tasks[id] = job.Task
		m.defs[id] = jobDefMeta{ID: id, PluginID: pluginID, Name: job.Name, Schedule: job.Schedule}
		m.tasksMu.Unlock()

		log.Printf("jobs: registered definition %s schedule=%q", formatJobMeta(jobDefMeta{ID: id, PluginID: pluginID, Name: job.Name}), job.Schedule)
	}
	return nil
}

// zombieRecovery resets any job_runs stuck in "running" status back to "planned".
// This handles crashes where a worker died mid-execution.
// Also marks planned runs as errored if their job definition has no registered task
// (leftover runs from before a code fix, or from a removed plugin).
func (m *Manager) zombieRecovery() error {
	// 1. Reset runs stuck in "running" back to "planned".
	res, err := m.db.Exec(
		`UPDATE job_runs
		 SET status = 'planned', started_at = NULL, error = 'Previous server instance crashed'
		 WHERE status = 'running'`,
	)
	if err != nil {
		return fmt.Errorf("zombie recovery: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		log.Printf("jobs: zombie recovery reset %d stuck job(s)", n)
	}

	// 2. Collect the IDs of all currently registered tasks.
	m.tasksMu.RLock()
	registered := make(map[int64]bool, len(m.tasks))
	for id := range m.tasks {
		registered[id] = true
	}
	m.tasksMu.RUnlock()

	// 3. Mark planned runs for unregistered definitions as errored.
	if len(registered) > 0 {
		rows, err := m.db.Query(
			`SELECT jr.id, jr.job_id, COALESCE(jd.plugin_id, ''), COALESCE(jd.name, '')
			 FROM job_runs jr
			 LEFT JOIN job_definitions jd ON jd.id = jr.job_id
			 WHERE jr.status = 'planned'`,
		)
		if err != nil {
			return fmt.Errorf("zombie recovery: list planned: %w", err)
		}
		var orphaned int
		for rows.Next() {
			var runID, jobID int64
			var pluginID, jobName string
			if err := rows.Scan(&runID, &jobID, &pluginID, &jobName); err != nil {
				rows.Close()
				return fmt.Errorf("zombie recovery: scan planned: %w", err)
			}
			if !registered[jobID] {
				meta := jobDefMeta{ID: jobID, PluginID: pluginID, Name: jobName}
				log.Printf("jobs: zombie recovery marking stale planned run=%d %s as errored: no registered task", runID, formatJobMeta(meta))
				m.db.Exec(
					`UPDATE job_runs SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = ? WHERE id = ?`,
					StatusErrored, "No task registered for this job definition", runID,
				)
				orphaned++
			}
		}
		rows.Close()
		if orphaned > 0 {
			log.Printf("jobs: zombie recovery marked %d unregistered planned run(s) as errored", orphaned)
		}
	}

	return nil
}

// registerBuiltinJobs registers the retention janitor and any other system jobs.
func (m *Manager) registerBuiltinJobs() error {
	janitorDef := CronJob{
		Name:     "janitor",
		Schedule: "@daily",
		Task:     janitorTask,
	}
	return m.UpsertDefinitions("_system", []CronJob{janitorDef})
}

func janitorTask(db *sql.DB, _ []byte) (string, error) {
	res, err := db.Exec(
		`DELETE FROM job_runs WHERE created_at < datetime('now', '-30 days')`,
	)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "No old job runs to clean up", nil
	}
	return fmt.Sprintf("Cleaned up %d old job runs", n), nil
}

// Start begins the job system: zombie recovery, scheduler goroutines, and workers.
// It assumes UpsertDefinitions has already been called for all plugins.
func (m *Manager) Start() error {
	if err := m.registerBuiltinJobs(); err != nil {
		return fmt.Errorf("register builtin jobs: %w", err)
	}

	if err := m.zombieRecovery(); err != nil {
		return err
	}

	log.Printf("jobs: starting manager workers=%d registered_definitions=%d", m.workers, m.registeredDefinitionCount())

	// Launch scheduler goroutines for each job definition.
	rows, err := m.db.Query(`SELECT id, schedule FROM job_definitions WHERE enabled = 1`)
	if err != nil {
		return fmt.Errorf("load job definitions: %w", err)
	}
	defer rows.Close()

	var wg sync.WaitGroup
	for rows.Next() {
		var id int64
		var schedule string
		if err := rows.Scan(&id, &schedule); err != nil {
			return fmt.Errorf("scan job definition: %w", err)
		}
		meta := m.lookupDefinition(id)
		// Empty schedule means ad-hoc only (no cron), skip scheduler.
		if schedule == "" {
			log.Printf("jobs: scheduler skipped for ad-hoc definition %s", formatJobMeta(meta))
			continue
		}
		if !m.hasTask(id) {
			log.Printf("jobs: scheduler skipped for unregistered definition %s schedule=%q", formatJobMeta(meta), schedule)
			continue
		}
		wg.Add(1)
		go m.runScheduler(id, schedule, &wg)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Launch worker goroutines.
	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go m.runWorker(i, &wg)
	}

	// When Stop is called, wait for all goroutines to finish.
	go func() {
		wg.Wait()
		close(m.done)
	}()

	return nil
}

// Stop signals all goroutines to stop and waits for them to finish (with a 30s timeout).
func (m *Manager) Stop() {
	close(m.stop)
	select {
	case <-m.done:
		log.Printf("jobs: all workers stopped cleanly")
	case <-time.After(30 * time.Second):
		log.Printf("jobs: timeout waiting for workers to stop")
	}
}

// Enqueue creates a new planned job run and returns the run ID.
// payload can be nil for cron-triggered jobs.
func (m *Manager) Enqueue(pluginID, jobName string, payload []byte) (int64, error) {
	var jobID int64
	err := m.db.QueryRow(
		`SELECT id FROM job_definitions WHERE plugin_id = ? AND name = ?`,
		pluginID, jobName,
	).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("job definition %s/%s not found: %w", pluginID, jobName, err)
	}

	var payloadStr *string
	if payload != nil {
		s := string(payload)
		payloadStr = &s
	}

	res, err := m.db.Exec(
		`INSERT INTO job_runs (job_id, status, payload) VALUES (?, ?, ?)`,
		jobID, StatusPlanned, payloadStr,
	)
	if err != nil {
		return 0, fmt.Errorf("enqueue job run: %w", err)
	}
	id, _ := res.LastInsertId()
	meta := m.lookupDefinition(jobID)
	log.Printf("jobs: enqueued run=%d %s payload_bytes=%d payload_preview=%q", id, formatJobMeta(meta), len(payload), previewText(string(payload), 160))
	m.emitEvent(RunEvent{Type: "enqueued", RunID: id, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusPlanned})
	return id, nil
}

// ListRuns returns recent job runs (last N) with the pending count.
func (m *Manager) ListRuns(limit int) (*ListResponse, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := m.db.Query(
		`SELECT jr.id, jd.plugin_id, jd.name, jr.status, jr.payload,
		        jr.started_at, jr.finished_at, jr.error, jr.result, jr.created_at
		 FROM job_runs jr
		 JOIN job_definitions jd ON jd.id = jr.job_id
		 ORDER BY jr.created_at DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list job runs: %w", err)
	}
	defer rows.Close()

	runs := make([]Run, 0)
	for rows.Next() {
		var r Run
		var pluginID, jobName, status string
		var startedAt, finishedAt, errStr, resultStr, payloadStr *string
		if err := rows.Scan(&r.ID, &pluginID, &jobName, &status, &payloadStr,
			&startedAt, &finishedAt, &errStr, &resultStr, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan job run: %w", err)
		}
		r.PluginID = pluginID
		r.JobName = jobName
		r.Status = status
		if payloadStr != nil {
			r.Payload = json.RawMessage(*payloadStr)
		}
		r.StartedAt = parseTimePtr(startedAt)
		r.FinishedAt = parseTimePtr(finishedAt)
		if errStr != nil {
			r.Error = errStr
		}
		if resultStr != nil {
			r.Result = resultStr
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var pendingCount int
	err = m.db.QueryRow(
		`SELECT COUNT(*) FROM job_runs WHERE status IN (?, ?)`,
		StatusPlanned, StatusRunning,
	).Scan(&pendingCount)
	if err != nil {
		return nil, fmt.Errorf("count pending jobs: %w", err)
	}

	return &ListResponse{Runs: runs, PendingCount: pendingCount}, nil
}

// RetryRun re-queues an errored or cancelled run by inserting a new planned row.
func (m *Manager) RetryRun(runID int64) (int64, error) {
	var jobID int64
	var status string
	var payload *string
	err := m.db.QueryRow(
		`SELECT job_id, status, payload FROM job_runs WHERE id = ?`, runID,
	).Scan(&jobID, &status, &payload)
	if err != nil {
		return 0, fmt.Errorf("retry: %w", err)
	}
	if status != StatusErrored && status != StatusCancelled {
		return 0, fmt.Errorf("retry: run %d has status %q, can only retry errored or cancelled runs", runID, status)
	}

	res, err := m.db.Exec(
		`INSERT INTO job_runs (job_id, status, payload) VALUES (?, ?, ?)`,
		jobID, StatusPlanned, payload,
	)
	if err != nil {
		return 0, fmt.Errorf("retry insert: %w", err)
	}
	id, _ := res.LastInsertId()
	meta := m.lookupDefinition(jobID)
	log.Printf("jobs: retried run=%d as run=%d %s payload_bytes=%d payload_preview=%q", runID, id, formatJobMeta(meta), len(derefString(payload)), previewText(derefString(payload), 160))
	m.emitEvent(RunEvent{Type: "retried", RunID: id, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusPlanned})
	return id, nil
}

// CancelRun cancels a planned (not yet running) job run.
func (m *Manager) CancelRun(runID int64) error {
	res, err := m.db.Exec(
		`UPDATE job_runs
		 SET status = 'cancelled', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND status = 'planned'`,
		runID,
	)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cancel: run %d not found or not in planned status", runID)
	}
	var jobID int64
	_ = m.db.QueryRow(`SELECT job_id FROM job_runs WHERE id = ?`, runID).Scan(&jobID)
	meta := m.lookupDefinition(jobID)
	log.Printf("jobs: cancelled run=%d %s", runID, formatJobMeta(meta))
	m.emitEvent(RunEvent{Type: "cancelled", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusCancelled})
	return nil
}

// runScheduler inserts a planned job_run row at each tick for a given definition.
func (m *Manager) runScheduler(defID int64, schedule string, wg *sync.WaitGroup) {
	defer wg.Done()

	meta := m.lookupDefinition(defID)
	d, ok := parseSimpleSchedule(schedule)
	if !ok {
		log.Printf("jobs: scheduler unsupported %s schedule=%q", formatJobMeta(meta), schedule)
		return
	}

	log.Printf("jobs: scheduler started %s schedule=%q", formatJobMeta(meta), schedule)

	for {
		// Calculate next tick.
		var sleep time.Duration
		if strings.HasPrefix(schedule, "@every ") {
			sleep = d
		} else {
			now := time.Now()
			next := now.Truncate(d).Add(d)
			if next.Before(now) || next.Equal(now) {
				next = next.Add(d)
			}
			sleep = next.Sub(now)
		}

		select {
		case <-m.stop:
			log.Printf("jobs: scheduler stopping %s", formatJobMeta(meta))
			return
		case <-time.After(sleep):
		}

		// Check if definition still exists and is enabled.
		var enabled bool
		err := m.db.QueryRow(`SELECT enabled FROM job_definitions WHERE id = ?`, defID).Scan(&enabled)
		if err != nil {
			log.Printf("jobs: scheduler lookup failed %s: %v", formatJobMeta(meta), err)
			continue
		}
		if !enabled {
			log.Printf("jobs: scheduler saw disabled definition %s", formatJobMeta(meta))
			continue
		}

		res, err := m.db.Exec(`INSERT INTO job_runs (job_id, status) VALUES (?, ?)`, defID, StatusPlanned)
		if err != nil {
			log.Printf("jobs: scheduler enqueue failed %s: %v", formatJobMeta(meta), err)
			continue
		}
		runID, _ := res.LastInsertId()
		log.Printf("jobs: scheduler enqueued run=%d %s", runID, formatJobMeta(meta))
		m.emitEvent(RunEvent{Type: "enqueued", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusPlanned})
	}
}

// runWorker is a persistent worker goroutine that polls for planned jobs.
func (m *Manager) runWorker(id int, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Printf("jobs: worker=%d started", id)

	for {
		select {
		case <-m.stop:
			log.Printf("jobs: worker=%d stopping", id)
			return
		default:
		}

		// Atomic dequeue: UPDATE ... RETURNING (Kleppmann, 2017).
		// go-sqlite3 doesn't support RETURNING, so we use a two-step approach
		// wrapped in an immediate transaction for atomicity.
		jobID, runID, payload, ok := m.atomicDequeue()
		if !ok {
			// No jobs available, sleep before polling again.
			select {
			case <-m.stop:
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		task, meta, hasTask := m.taskAndDefinition(jobID)
		if meta.ID == 0 {
			meta = m.lookupDefinition(jobID)
		}
		log.Printf("jobs: worker=%d claimed run=%d %s payload_bytes=%d payload_preview=%q", id, runID, formatJobMeta(meta), len(payload), previewText(string(payload), 160))
		m.emitEvent(RunEvent{Type: "running", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusRunning})

		if !hasTask || task == nil {
			log.Printf("jobs: worker=%d no task registered run=%d %s", id, runID, formatJobMeta(meta))
			m.db.Exec(
				`UPDATE job_runs SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = ? WHERE id = ?`,
				StatusErrored, "No task registered for this job definition", runID,
			)
			errMsg := "No task registered for this job definition"
			m.emitEvent(RunEvent{Type: "errored", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusErrored, Error: &errMsg})
			continue
		}

		// Execute the task with panic recovery.
		func() {
			started := time.Now()
			defer func() {
				if r := recover(); r != nil {
					errMsg := fmt.Sprintf("panic: %v", r)
					log.Printf("jobs: worker=%d panicked run=%d %s duration=%s panic=%q", id, runID, formatJobMeta(meta), time.Since(started), previewText(errMsg, 240))
					m.db.Exec(
						`UPDATE job_runs SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = ? WHERE id = ?`,
						StatusErrored, errMsg, runID,
					)
					m.emitEvent(RunEvent{Type: "errored", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusErrored, Error: &errMsg})
				}
			}()

			result, taskErr := task(m.db, payload)
			now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			duration := time.Since(started)
			if taskErr != nil {
				errStr := taskErr.Error()
				log.Printf("jobs: worker=%d errored run=%d %s duration=%s error=%q", id, runID, formatJobMeta(meta), duration, previewText(errStr, 240))
				m.db.Exec(
					`UPDATE job_runs SET status = ?, finished_at = ?, error = ? WHERE id = ?`,
					StatusErrored, now, errStr, runID,
				)
				m.emitEvent(RunEvent{Type: "errored", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusErrored, Error: &errStr})
			} else {
				log.Printf("jobs: worker=%d done run=%d %s duration=%s result=%q", id, runID, formatJobMeta(meta), duration, previewText(result, 240))
				m.db.Exec(
					`UPDATE job_runs SET status = ?, finished_at = ?, result = ? WHERE id = ?`,
					StatusDone, now, result, runID,
				)
				m.emitEvent(RunEvent{Type: "done", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusDone, Result: &result})
			}
		}()
	}
}

// atomicDequeue atomically claims the next planned job run.
// Uses a compare-and-swap (CAS) pattern: SELECT a candidate, then
// UPDATE WHERE id=? AND status='planned'. SQLite serializes writes,
// so only one worker's UPDATE succeeds — the loser retries (Kleppmann, 2017).
func (m *Manager) atomicDequeue() (jobID, runID int64, payload []byte, ok bool) {
	// Retry up to 3 times to handle CAS conflicts.
	for attempt := 0; attempt < 3; attempt++ {
		// Step 1: Pick a candidate (non-locking read).
		var pld *string
		err := m.db.QueryRow(
			`SELECT id, job_id, payload FROM job_runs
			 WHERE status = ?
			 ORDER BY created_at ASC
			 LIMIT 1`,
			StatusPlanned,
		).Scan(&runID, &jobID, &pld)
		if err != nil {
			return 0, 0, nil, false
		}

		// Step 2: Atomically claim it. The WHERE status = 'planned' guard ensures
		// that if another worker already claimed it, RowsAffected will be 0.
		res, err := m.db.Exec(
			`UPDATE job_runs
			 SET status = ?, started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			 WHERE id = ? AND status = ?`,
			StatusRunning, runID, StatusPlanned,
		)
		if err != nil {
			log.Printf("jobs: atomic dequeue: update: %v", err)
			continue
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// Another worker claimed this row. Try the next one.
			continue
		}

		if pld != nil {
			payload = []byte(*pld)
		}
		return jobID, runID, payload, true
	}
	return 0, 0, nil, false
}

// --- Helpers ---

func (m *Manager) registeredDefinitionCount() int {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	return len(m.defs)
}

func (m *Manager) hasTask(jobID int64) bool {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	_, ok := m.tasks[jobID]
	return ok
}

func (m *Manager) taskAndDefinition(jobID int64) (func(*sql.DB, []byte) (string, error), jobDefMeta, bool) {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	task, ok := m.tasks[jobID]
	meta := m.defs[jobID]
	return task, meta, ok
}

func (m *Manager) emitEvent(evt RunEvent) {
	if m == nil {
		return
	}
	m.observerMu.RLock()
	observer := m.observer
	m.observerMu.RUnlock()
	if observer != nil {
		observer(evt)
	}
}

func (m *Manager) lookupDefinition(jobID int64) jobDefMeta {
	m.tasksMu.RLock()
	meta, ok := m.defs[jobID]
	m.tasksMu.RUnlock()
	if ok {
		return meta
	}

	meta.ID = jobID
	_ = m.db.QueryRow(
		`SELECT plugin_id, name, schedule FROM job_definitions WHERE id = ?`,
		jobID,
	).Scan(&meta.PluginID, &meta.Name, &meta.Schedule)
	return meta
}

func formatJobMeta(meta jobDefMeta) string {
	if meta.ID == 0 && meta.PluginID == "" && meta.Name == "" {
		return "def_id=0"
	}
	if meta.PluginID == "" && meta.Name == "" {
		return fmt.Sprintf("def_id=%d", meta.ID)
	}
	return fmt.Sprintf("job=%s/%s def_id=%d", meta.PluginID, meta.Name, meta.ID)
}

func previewText(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseTimePtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", *s)
	if err != nil {
		// Try with fractional seconds.
		t, err = time.Parse("2006-01-02T15:04:05.000Z", *s)
		if err != nil {
			return nil
		}
	}
	return &t
}

// parseSimpleSchedule parses the same schedule formats as the old scheduler.
func parseSimpleSchedule(schedule string) (time.Duration, bool) {
	switch {
	case strings.HasPrefix(schedule, "@every "):
		d, err := time.ParseDuration(strings.TrimPrefix(schedule, "@every "))
		if err != nil {
			return 0, false
		}
		return d, true
	case schedule == "@daily":
		return 24 * time.Hour, true
	case schedule == "@hourly":
		return time.Hour, true
	default:
		return 0, false
	}
}
