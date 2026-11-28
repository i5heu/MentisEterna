package jobs

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"
)

// --- Definition registration ---

// UpsertDefinitions upserts job_definitions rows from the provided cron
// jobs (schedule + execution metadata) and registers the task
// functions. Called for every plugin at server startup.
func (m *Manager) UpsertDefinitions(pluginID string, jobs []CronJob) error {
	return m.upsertDefs(pluginID, jobs)
}

// RegisterAdHoc registers a job that is only triggered on-demand via
// Enqueue (not on a cron schedule). The task function is stored in the
// in-memory map and the definition is persisted so Enqueue can find it.
func (m *Manager) RegisterAdHoc(pluginID string, jobs []CronJob) error {
	// Force empty schedule for ad-hoc jobs (no cron ticks).
	for i := range jobs {
		jobs[i].Schedule = ""
	}
	return m.upsertDefs(pluginID, jobs)
}

// upsertDefs persists one row per job and wires the in-memory task
// table. Metadata (schedule, priority, max_parallel, timeout,
// max_retries) always follows the registered values — code is the
// source of truth; the DB row makes it observable. Sub-second timeouts
// round up to 1 s (the scheduler/watchdog granularity is whole
// seconds).
func (m *Manager) upsertDefs(pluginID string, jobs []CronJob) error {
	for _, job := range jobs {
		maxParallel := job.MaxParallel
		if maxParallel < 1 {
			maxParallel = 1
		}
		timeoutSeconds := 0
		if job.Timeout > 0 {
			timeoutSeconds = int(math.Ceil(job.Timeout.Seconds()))
		}
		_, err := m.db.Exec(
			`INSERT INTO job_definitions (plugin_id, name, schedule, priority, max_parallel, timeout_seconds, max_retries)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(plugin_id, name) DO UPDATE SET
			   schedule = excluded.schedule,
			   priority = excluded.priority,
			   max_parallel = excluded.max_parallel,
			   timeout_seconds = excluded.timeout_seconds,
			   max_retries = excluded.max_retries`,
			pluginID, job.Name, job.Schedule, job.Priority, maxParallel,
			timeoutSeconds, job.MaxRetries,
		)
		if err != nil {
			return fmt.Errorf("upsert job definition %s/%s: %w", pluginID, job.Name, err)
		}

		// Always look up the canonical definition ID after the upsert.
		// LastInsertId is not reliable for SQLite ON CONFLICT DO UPDATE
		// paths, and using it can register tasks under the wrong ID.
		var id int64
		var storedTimeoutSeconds int
		err = m.db.QueryRow(
			`SELECT id, timeout_seconds FROM job_definitions WHERE plugin_id = ? AND name = ?`,
			pluginID, job.Name,
		).Scan(&id, &storedTimeoutSeconds)
		if err != nil {
			return fmt.Errorf("lookup job definition %s/%s: %w", pluginID, job.Name, err)
		}

		meta := jobDefMeta{
			ID:          id,
			PluginID:    pluginID,
			Name:        job.Name,
			Schedule:    job.Schedule,
			Priority:    job.Priority,
			MaxParallel: maxParallel,
			Timeout:     time.Duration(storedTimeoutSeconds) * time.Second,
			MaxRetries:  job.MaxRetries,
		}

		m.tasksMu.Lock()
		m.tasks[id] = job.Task
		m.defs[id] = meta
		m.tasksMu.Unlock()

		log.Printf("jobs: registered definition %s schedule=%q priority=%d max_parallel=%d timeout=%s max_retries=%d",
			formatJobMeta(meta), job.Schedule, meta.Priority, meta.MaxParallel, meta.Timeout, meta.MaxRetries)
	}
	return nil
}

// registerBuiltinJobs registers the retention janitor and any other
// system jobs.
func (m *Manager) registerBuiltinJobs() error {
	return m.UpsertDefinitions("_system", []CronJob{{
		Name:     "janitor",
		Schedule: "@daily",
		Task:     janitorTask,
	}})
}

// janitorTask prunes job_runs older than 30 days. The per-definition
// stats ledger (job_definition_stats) is not pruned — it is the
// long-lived aggregate history.
func janitorTask(_ context.Context, rt *RunContext) (string, error) {
	res, err := rt.DB.Exec(
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

// --- Definition lookups ---

// registeredDefinitionCount is used for the startup log line.
func (m *Manager) registeredDefinitionCount() int {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	return len(m.defs)
}

// hasTask reports whether a definition has a registered task function.
func (m *Manager) hasTask(jobID int64) bool {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	_, ok := m.tasks[jobID]
	return ok
}

// taskAndDefinition returns the registered task and cached metadata for
// a definition.
func (m *Manager) taskAndDefinition(jobID int64) (TaskFunc, jobDefMeta, bool) {
	m.tasksMu.RLock()
	defer m.tasksMu.RUnlock()
	task, ok := m.tasks[jobID]
	meta := m.defs[jobID]
	return task, meta, ok
}

// lookupDefinition returns metadata for a definition: from the cache
// when registered, otherwise from the DB (and without caching — rows
// without a registered task are transient by definition).
func (m *Manager) lookupDefinition(jobID int64) jobDefMeta {
	m.tasksMu.RLock()
	meta, ok := m.defs[jobID]
	m.tasksMu.RUnlock()
	if ok {
		return meta
	}

	meta.ID = jobID
	var timeoutSeconds int
	_ = m.db.QueryRow(
		`SELECT plugin_id, name, schedule,
		        COALESCE(priority, 0), COALESCE(max_parallel, 1),
		        COALESCE(timeout_seconds, 0), COALESCE(max_retries, 0)
		 FROM job_definitions WHERE id = ?`,
		jobID,
	).Scan(&meta.PluginID, &meta.Name, &meta.Schedule, &meta.Priority,
		&meta.MaxParallel, &timeoutSeconds, &meta.MaxRetries)
	meta.Timeout = time.Duration(timeoutSeconds) * time.Second
	return meta
}

// --- Crash recovery ---

// zombieRecovery runs once at Start: it resets any job_runs stuck in
// "running" back to "planned" (a previous server instance crashed
// mid-run) and marks planned runs as errored whose definition has no
// registered task (leftovers from a removed plugin, or runs enqueued
// before a code fix).
func (m *Manager) zombieRecovery() error {
	// 1. Reset runs stuck in "running" back to "planned".
	res, err := m.db.Exec(
		`UPDATE job_runs
		 SET status = ?, started_at = NULL, progress_at = NULL, worker_id = NULL,
		     error = 'Previous server instance crashed'
		 WHERE status = ?`,
		StatusPlanned, StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("zombie recovery: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("jobs: zombie recovery reset %d stuck job(s)", n)
	}

	// 2. Collect the IDs of all currently registered tasks.
	m.tasksMu.RLock()
	registered := make(map[int64]bool, len(m.tasks))
	for id := range m.tasks {
		registered[id] = true
	}
	m.tasksMu.RUnlock()
	if len(registered) == 0 {
		return nil
	}

	// 3. Mark planned runs for unregistered definitions as errored.
	// Two-phase (collect IDs first, update after the cursor closes):
	// keeps nested Exec-while-Rows-open off the connection pool, which
	// deadlocks single-connection (in-memory) databases.
	type orphanRun struct {
		id     int64
		jobID  int64
		plugin string
		name   string
	}
	rows, err := m.db.Query(
		`SELECT jr.id, jr.job_id, COALESCE(jd.plugin_id, ''), COALESCE(jd.name, '')
		 FROM job_runs jr
		 LEFT JOIN job_definitions jd ON jd.id = jr.job_id
		 WHERE jr.status = ?`,
		StatusPlanned,
	)
	if err != nil {
		return fmt.Errorf("zombie recovery: list planned: %w", err)
	}
	var orphans []orphanRun
	for rows.Next() {
		var o orphanRun
		if err := rows.Scan(&o.id, &o.jobID, &o.plugin, &o.name); err != nil {
			rows.Close()
			return fmt.Errorf("zombie recovery: scan planned: %w", err)
		}
		if !registered[o.jobID] {
			orphans = append(orphans, o)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("zombie recovery: iterate planned: %w", err)
	}
	for _, o := range orphans {
		meta := jobDefMeta{ID: o.jobID, PluginID: o.plugin, Name: o.name}
		log.Printf("jobs: zombie recovery marking stale planned run=%d %s as errored: no registered task", o.id, formatJobMeta(meta))
		if _, err := m.db.Exec(
			`UPDATE job_runs SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), error = ? WHERE id = ?`,
			StatusErrored, "No task registered for this job definition", o.id,
		); err != nil {
			log.Printf("jobs: zombie recovery: mark run=%d errored: %v", o.id, err)
		}
	}
	if len(orphans) > 0 {
		log.Printf("jobs: zombie recovery marked %d unregistered planned run(s) as errored", len(orphans))
	}

	return nil
}

// --- Observer bus ---

// emitEvent delivers a lifecycle event to the registered observer, if
// any. Errors in the observer never propagate into job execution.
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

// kick wakes one idle worker (non-blocking; the buffer of 1 collapses
// bursts into a single wakeup) so freshly enqueued runs start
// immediately instead of waiting for the fallback poll.
func (m *Manager) kick() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// fmtError is a tiny helper keeping wrap sites uniform.
func fmtError(what string, err error) error {
	return fmt.Errorf("%s: %w", what, err)
}
