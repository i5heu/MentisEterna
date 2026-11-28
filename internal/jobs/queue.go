package jobs

import (
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// --- Run queue API ---

// insertPlannedRun inserts a planned run for jobID with the given
// payload, retry count, and optional due time (delayed auto-retries).
// The run inherits the definition's priority. If an identical run (same
// job_id + NON-NULL payload) is already planned or running, it returns
// that run's ID with duplicate=true instead of inserting — the unique
// partial index idx_job_runs_active_unique makes this race-free. NULL
// payloads are exempt by SQLite semantics (NULLs are distinct in
// unique indexes, so multiple active NULL-payload rows are legal by
// design — the scheduler prevents cron pile-up with its own active-run
// check); a NULL-payload insert therefore always inserts.
func (m *Manager) insertPlannedRun(jobID int64, payload *string, retryCount int, nextRunAt *time.Time) (runID int64, duplicate bool, err error) {
	var nextRunStr any
	if nextRunAt != nil {
		nextRunStr = dbTimestamp(*nextRunAt)
	}
	res, err := m.db.Exec(
		`INSERT INTO job_runs (job_id, status, payload, retry_count, next_run_at, priority)
		 SELECT ?, ?, ?, ?, ?, priority FROM job_definitions WHERE id = ?`,
		jobID, StatusPlanned, payload, retryCount, nextRunStr, jobID,
	)
	if err == nil {
		id, _ := res.LastInsertId()
		return id, false, nil
	}
	// The unique index rejected the insert: an identical active run
	// exists. `payload IS ?` matches both NULL and TEXT payloads.
	var existing int64
	if lookupErr := m.db.QueryRow(
		`SELECT id FROM job_runs WHERE job_id = ? AND payload IS ? AND status IN (?, ?) LIMIT 1`,
		jobID, payload, StatusPlanned, StatusRunning,
	).Scan(&existing); lookupErr == nil {
		return existing, true, nil
	}
	return 0, false, err
}

// Enqueue creates a new planned job run and returns the run ID.
// payload can be nil for cron-triggered jobs. Workers are woken
// immediately; an identical active run with the same NON-NULL payload
// dedupes to its ID (NULL-payload runs are exempt — see
// insertPlannedRun).
func (m *Manager) Enqueue(pluginID, jobName string, payload []byte) (int64, error) {
	var jobID int64
	err := m.db.QueryRow(
		`SELECT id FROM job_definitions WHERE plugin_id = ? AND name = ?`,
		pluginID, jobName,
	).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("job definition %s/%s not found: %w", pluginID, jobName, err)
	}

	payloadStr := (*string)(nil)
	if payload != nil {
		s := string(payload)
		payloadStr = &s
	}

	id, duplicate, err := m.insertPlannedRun(jobID, payloadStr, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("enqueue job run: %w", err)
	}
	meta := m.lookupDefinition(jobID)
	if duplicate {
		log.Printf("jobs: enqueue skipped run=%d %s (identical active run exists)", id, formatJobMeta(meta))
		return id, nil
	}
	log.Printf("jobs: enqueued run=%d %s payload_bytes=%d payload_preview=%q",
		id, formatJobMeta(meta), len(payload), previewText(string(payload), 160))
	m.emitEvent(RunEvent{Type: "enqueued", RunID: id, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusPlanned})
	m.kick()
	return id, nil
}

// RetryRun re-queues an errored or cancelled run by inserting a new
// planned row carrying one more retry than the run it replaces.
func (m *Manager) RetryRun(runID int64) (int64, error) {
	var jobID int64
	var status string
	var payload *string
	var retryCount int
	err := m.db.QueryRow(
		`SELECT job_id, status, payload, retry_count FROM job_runs WHERE id = ?`, runID,
	).Scan(&jobID, &status, &payload, &retryCount)
	if err != nil {
		return 0, fmt.Errorf("retry: %w", err)
	}
	if status != StatusErrored && status != StatusCancelled {
		return 0, fmt.Errorf("retry: run %d has status %q, can only retry errored or cancelled runs", runID, status)
	}

	id, duplicate, err := m.insertPlannedRun(jobID, payload, retryCount+1, nil)
	if err != nil {
		return 0, fmt.Errorf("retry insert: %w", err)
	}
	meta := m.lookupDefinition(jobID)
	if duplicate {
		log.Printf("jobs: retry skipped run=%d (identical active run=%d exists)", runID, id)
		return id, nil
	}
	log.Printf("jobs: retried run=%d as run=%d %s payload_bytes=%d payload_preview=%q",
		runID, id, formatJobMeta(meta), len(derefString(payload)), previewText(derefString(payload), 160))
	m.emitEvent(RunEvent{Type: "retried", RunID: id, PluginID: meta.PluginID, JobName: meta.Name,
		Status: StatusPlanned, Attempt: retryCount + 2})
	m.kick()
	return id, nil
}

// CancelRun cancels a planned or running job run. The terminal write
// commits FIRST (guarded by the status read at the top of this call,
// so exactly one finalize path wins and the stats are bumped once);
// only then is the task's context cancelled — the worker's own final
// update would hit `WHERE status IN (...)` and no-op. If the run
// finished in between, a truthful error is returned.
func (m *Manager) CancelRun(runID int64) error {
	var jobID int64
	var status string
	var startedAt *string
	err := m.db.QueryRow(
		`SELECT job_id, status, started_at FROM job_runs WHERE id = ?`, runID,
	).Scan(&jobID, &status, &startedAt)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if status != StatusPlanned && status != StatusRunning {
		return fmt.Errorf("cancel: run %d has status %q, can only cancel planned or running runs", runID, status)
	}
	meta := m.lookupDefinition(jobID)

	var durationMS int64
	if st := parseTimePtr(startedAt); st != nil && status == StatusRunning {
		durationMS = time.Since(*st).Milliseconds()
		if durationMS < 0 {
			durationMS = 0
		}
	}

	// 1. Atomically flip the row to cancelled (+ stats ledger bump).
	now := dbTimestamp(time.Now())
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	res, err := tx.Exec(
		`UPDATE job_runs
		 SET status = ?, finished_at = ?, duration_ms = ?, error = 'Cancelled by user'
		 WHERE id = ? AND status = ?`,
		StatusCancelled, now, durationMS, runID, status,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("cancel: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return fmt.Errorf("cancel: run %d finished before the cancel took effect", runID)
	}
	if err := bumpStatsTx(tx, jobID, StatusCancelled, durationMS, "", "", now); err != nil {
		log.Printf("jobs: cancel stats bump run=%d: %v", runID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}

	// 2. Stop the task (if any) — its own finalize no-ops against the
	// committed row.
	m.activeMu.Lock()
	if ar, ok := m.active[runID]; ok {
		ar.cancel()
	}
	m.activeMu.Unlock()

	log.Printf("jobs: cancelled %s run=%d %s", status, runID, formatJobMeta(meta))
	m.emitEvent(RunEvent{Type: "cancelled", RunID: runID, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusCancelled})
	return nil
}

// ListRuns returns the most recent job runs with the global pending
// count. Runs carry live progress (done/total/message), duration, and
// the delayed-retry due time when present.
func (m *Manager) ListRuns(limit int) (*ListResponse, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := m.db.Query(
		`SELECT jr.id, jd.plugin_id, jd.name, jr.status, jr.priority, jr.retry_count,
		        COALESCE(jr.worker_id, 0), jr.payload, jr.started_at, jr.finished_at, jr.next_run_at,
		        jr.duration_ms, jr.progress_done, jr.progress_total, jr.progress_message,
		        jr.error, jr.result, jr.created_at
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
		var startedAt, finishedAt, nextRunAt, errStr, resultStr, payloadStr, progressMsg *string
		var durationMS *int64
		var progressDone, progressTotal *int
		if err := rows.Scan(&r.ID, &pluginID, &jobName, &status, &r.Priority, &r.RetryCount,
			&r.WorkerID, &payloadStr, &startedAt, &finishedAt, &nextRunAt,
			&durationMS, &progressDone, &progressTotal, &progressMsg,
			&errStr, &resultStr, &r.CreatedAt); err != nil {
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
		r.NextRunAt = parseTimePtr(nextRunAt)
		r.DurationMS = durationMS
		r.ProgressDone = progressDone
		r.ProgressTotal = progressTotal
		r.ProgressMessage = progressMsg
		r.Error = errStr
		r.Result = resultStr
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job runs: %w", err)
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
