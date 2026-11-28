package jobs

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// finishRun writes the terminal status of a run and folds it into the
// stats ledger — both in one transaction. The UPDATE is guarded by the
// current status (running or planned): if a concurrent
// cancel/timeout/watchdog already finalized the row, this call is a
// no-op returning false, and the stats are bumped exactly once (by the
// winner). This is the exactly-once terminal transition of the system;
// every path that ends a run must go through here (or CancelRun's
// inline equivalent).
func (m *Manager) finishRun(runID, defID int64, meta jobDefMeta, status, result, errMsg string, durationMS int64) bool {
	now := dbTimestamp(time.Now())
	tx, err := m.db.Begin()
	if err != nil {
		log.Printf("jobs: finish run=%d begin: %v", runID, err)
		return false
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE job_runs
		 SET status = ?, finished_at = ?, result = ?, error = ?, duration_ms = ?
		 WHERE id = ? AND status IN (?, ?)`,
		status, now, result, errMsg, durationMS, runID, StatusRunning, StatusPlanned,
	)
	if err != nil {
		log.Printf("jobs: finish run=%d update: %v", runID, err)
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false // row was already finalized elsewhere
	}
	if err := bumpStatsTx(tx, defID, status, durationMS, result, errMsg, now); err != nil {
		log.Printf("jobs: stats bump run=%d: %v", runID, err)
	}
	if err := tx.Commit(); err != nil {
		log.Printf("jobs: finish run=%d commit: %v", runID, err)
		return false
	}
	return true
}

// bumpStatsTx folds one terminal run into the per-definition stats
// ledger (job_definition_stats). Duration only counts when > 0; empty
// result/error strings are stored as NULL so stale values don't stick
// around after a later success.
func bumpStatsTx(tx *sql.Tx, defID int64, status string, durationMS int64, result, errMsg, finishedAt string) error {
	var done, errored, cancelled int
	switch status {
	case StatusDone:
		done = 1
	case StatusErrored:
		errored = 1
	case StatusCancelled:
		cancelled = 1
	}
	var duration, resultVal, errVal any
	if durationMS > 0 {
		duration = durationMS
	}
	if result != "" {
		resultVal = result
	}
	if errMsg != "" {
		errVal = errMsg
	}
	_, err := tx.Exec(
		`INSERT INTO job_definition_stats
		   (job_id, runs_total, runs_done, runs_errored, runs_cancelled,
		    auto_retries, total_duration_ms, min_duration_ms, max_duration_ms,
		    last_finished_at, last_status, last_result, last_error)
		 VALUES (?, 1, ?, ?, ?, 0, COALESCE(?, 0), ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id) DO UPDATE SET
		   runs_total = job_definition_stats.runs_total + 1,
		   runs_done = job_definition_stats.runs_done + excluded.runs_done,
		   runs_errored = job_definition_stats.runs_errored + excluded.runs_errored,
		   runs_cancelled = job_definition_stats.runs_cancelled + excluded.runs_cancelled,
		   total_duration_ms = job_definition_stats.total_duration_ms + COALESCE(excluded.total_duration_ms, 0),
		   min_duration_ms = CASE
		     WHEN job_definition_stats.min_duration_ms IS NULL THEN excluded.min_duration_ms
		     WHEN excluded.min_duration_ms IS NULL THEN job_definition_stats.min_duration_ms
		     WHEN excluded.min_duration_ms < job_definition_stats.min_duration_ms THEN excluded.min_duration_ms
		     ELSE job_definition_stats.min_duration_ms END,
		   max_duration_ms = CASE
		     WHEN job_definition_stats.max_duration_ms IS NULL THEN excluded.max_duration_ms
		     WHEN excluded.max_duration_ms IS NULL THEN job_definition_stats.max_duration_ms
		     WHEN excluded.max_duration_ms > job_definition_stats.max_duration_ms THEN excluded.max_duration_ms
		     ELSE job_definition_stats.max_duration_ms END,
		   last_finished_at = excluded.last_finished_at,
		   last_status = excluded.last_status,
		   last_result = excluded.last_result,
		   last_error = excluded.last_error`,
		defID, done, errored, cancelled,
		duration, duration, duration,
		finishedAt, status, resultVal, errVal,
	)
	return err
}

// retryBackoff returns the delay before attempt n of a run: 15 s
// doubled per attempt, capped at 10 minutes, with ±20 % jitter so
// concurrent failures don't retry in lockstep.
func retryBackoff(attempt int) time.Duration {
	base := 15 * time.Second
	d := base << (attempt - 1)
	if d <= 0 || d > 10*time.Minute {
		d = 10 * time.Minute
	}
	jitter := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(d) * jitter)
}

// autoRetry re-queues a just-errored run when retries remain, with
// exponential backoff + jitter (due time stored in next_run_at — the
// watchdog wakes workers when it elapses). The errored run stays
// visible in the history: its error is annotated with the retry note.
func (m *Manager) autoRetry(claim dqClaim, meta jobDefMeta, errStr string) {
	if meta.MaxRetries <= 0 || claim.retryCount >= meta.MaxRetries {
		return
	}
	backoff := retryBackoff(claim.retryCount + 1)
	note := fmt.Sprintf("auto-retry %d/%d scheduled in ~%s",
		claim.retryCount+1, meta.MaxRetries, backoff.Round(time.Second))
	log.Printf("jobs: run=%d %s errored (%s); %s",
		claim.runID, formatJobMeta(meta), previewText(errStr, 160), note)

	payloadStr := (*string)(nil)
	if len(claim.payload) > 0 {
		s := string(claim.payload)
		payloadStr = &s
	}
	id, duplicate, err := m.insertPlannedRun(claim.defID, payloadStr, claim.retryCount+1,
		timePtr(time.Now().Add(backoff)))
	if err != nil {
		log.Printf("jobs: auto-retry insert run=%d: %v", claim.runID, err)
		return
	}
	if duplicate {
		return
	}
	log.Printf("jobs: auto-retry of run=%d is run=%d %s attempt=%d due-in=%s",
		claim.runID, id, formatJobMeta(meta), claim.retryCount+2, backoff.Round(time.Millisecond))

	// Annotate the errored run so the history explains itself, and
	// count the auto-retry in the ledger.
	m.db.Exec(`UPDATE job_runs SET error = COALESCE(error,'') || ' [' || ? || ']' WHERE id = ?`, note, claim.runID)
	m.db.Exec(`UPDATE job_definition_stats SET auto_retries = COALESCE(auto_retries, 0) + 1 WHERE job_id = ?`, claim.defID)
	m.emitEvent(RunEvent{
		Type:     "retried",
		RunID:    id,
		PluginID: meta.PluginID,
		JobName:  meta.Name,
		Status:   StatusPlanned,
		Attempt:  claim.retryCount + 2,
		Error:    ptrStr(note),
	})
}
