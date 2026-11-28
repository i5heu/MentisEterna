package jobs

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// dqClaim is a running run claimed by a worker via dequeue.
type dqClaim struct {
	runID      int64
	defID      int64
	payload    []byte
	retryCount int
	workerID   int
}

// dequeue claims the next eligible planned run for the given lane.
//
// fast=true restricts candidates to PriorityHigh runs: the fast lane
// exists so interactive jobs are never stuck behind bulk work. Eligible
// means: planned, next_run_at due (delayed retries), definition
// enabled, and the definition below its max_parallel cap.
//
// The claim is ONE atomic UPDATE ... RETURNING: SQLite serializes
// writers, so the eligibility check (including the per-definition
// running count) and the status flip happen in a single statement —
// two workers can never both pass the max_parallel gate. A lane
// contention (both workers aimed at the same row) simply retries with
// a fresh candidate.
func (m *Manager) dequeue(fast bool, workerID int) (dqClaim, bool) {
	prioFilter := ``
	if fast {
		prioFilter = ` AND jd.priority >= 1`
	}
	for attempt := 0; attempt < 4; attempt++ {
		rows, err := m.db.Query(
			`UPDATE job_runs
			 SET status = ?, started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
			     progress_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), worker_id = ?
			 WHERE id = (
			   SELECT jr.id
			   FROM job_runs jr
			   JOIN job_definitions jd ON jd.id = jr.job_id
			   WHERE jr.status = ?`+prioFilter+`
			     AND jd.enabled = 1
			     AND (jr.next_run_at IS NULL OR jr.next_run_at <= strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			     AND (jd.max_parallel <= 0 OR (
			           SELECT COUNT(*) FROM job_runs r
			           WHERE r.job_id = jr.job_id AND r.status = ?
			         ) < jd.max_parallel)
			   ORDER BY jr.priority DESC, jr.created_at ASC, jr.id ASC
			   LIMIT 1
			 )
			 AND status = ?
			 RETURNING id, job_id, COALESCE(payload, ''), retry_count`,
			StatusRunning, workerID, StatusPlanned, StatusRunning, StatusPlanned,
		)
		if err != nil {
			log.Printf("jobs: dequeue claim: %v", err)
			return dqClaim{}, false
		}
		var c dqClaim
		var payloadStr string
		if rows.Next() {
			if err := rows.Scan(&c.runID, &c.defID, &payloadStr, &c.retryCount); err != nil {
				rows.Close()
				log.Printf("jobs: dequeue scan: %v", err)
				return dqClaim{}, false
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Printf("jobs: dequeue iterate: %v", err)
			return dqClaim{}, false
		}
		if c.runID == 0 {
			// Either the queue is empty or another worker claimed the
			// top candidate between statements — retry with fresh view.
			continue
		}
		c.payload = []byte(payloadStr)
		c.workerID = workerID
		return c, true
	}
	return dqClaim{}, false
}

// runWorker is a persistent worker goroutine. Fast workers only serve
// high-priority runs; normal workers serve everything, highest
// priority first. Idle workers sleep on the notify channel and wake
// immediately when work arrives; the fallback poll (2 s) covers
// delayed retries and any missed wakeup.
func (m *Manager) runWorker(id int, fast bool, wg *sync.WaitGroup) {
	defer wg.Done()
	lane := "normal"
	if fast {
		lane = "fast"
	}
	log.Printf("jobs: worker=%d started lane=%s", id, lane)

	for {
		select {
		case <-m.stop:
			log.Printf("jobs: worker=%d stopping", id)
			return
		default:
		}

		claim, ok := m.dequeue(fast, id)
		if !ok {
			select {
			case <-m.stop:
				return
			case <-m.notify:
			case <-time.After(m.pollInterval):
			}
			continue
		}
		m.execute(claim, fast)
		m.kick() // a slot and maybe more work freed up — wake a sibling
	}
}

// execute runs one claimed job run to completion (or timeout/cancel).
// It owns the run's active-tracking entry and the task context; every
// terminal transition goes through the guarded finishRun.
func (m *Manager) execute(claim dqClaim, fast bool) {
	task, meta, hasTask := m.taskAndDefinition(claim.defID)
	meta = orLookupDefinition(m, meta, claim.defID)
	log.Printf("jobs: worker=%d claimed run=%d %s attempt=%d payload_bytes=%d payload_preview=%q",
		claim.workerID, claim.runID, formatJobMeta(meta), claim.retryCount+1,
		len(claim.payload), previewText(string(claim.payload), 160))
	m.emitEvent(RunEvent{
		Type:     "running",
		RunID:    claim.runID,
		PluginID: meta.PluginID,
		JobName:  meta.Name,
		Status:   StatusRunning,
		Attempt:  claim.retryCount + 1,
		WorkerID: claim.workerID,
	})

	if !hasTask || task == nil {
		log.Printf("jobs: worker=%d no task registered run=%d %s", claim.workerID, claim.runID, formatJobMeta(meta))
		m.finishRun(claim.runID, claim.defID, meta, StatusErrored, "", "No task registered for this job definition", 0)
		m.emitEvent(RunEvent{Type: "errored", RunID: claim.runID, PluginID: meta.PluginID,
			JobName: meta.Name, Status: StatusErrored, Error: ptrStr("No task registered for this job definition")})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ar := &activeRun{
		runID:    claim.runID,
		defID:    claim.defID,
		workerID: claim.workerID,
		fast:     fast,
		started:  time.Now(),
		cancel:   cancel,
	}
	m.activeMu.Lock()
	m.active[claim.runID] = ar
	m.activeMu.Unlock()
	defer func() {
		cancel()
		m.activeMu.Lock()
		delete(m.active, claim.runID)
		m.activeMu.Unlock()
	}()

	rt := &RunContext{
		DB:       m.db,
		RunID:    claim.runID,
		PluginID: meta.PluginID,
		JobName:  meta.Name,
		Payload:  claim.payload,
		Progress: m.makeProgress(claim.runID, meta),
	}

	started := time.Now()
	result, taskErr := runTaskWithRecover(ctx, task, rt, claim, meta)
	durationMS := time.Since(started).Milliseconds()
	cancelled := ctx.Err() != nil
	timedOut := ar.timedOut.Load()

	switch {
	case timedOut:
		// The watchdog owns this terminal transition: it finalize-errors
		// the row (with the real timeout message), requeues per the
		// retry policy, and emits the event. The task already returned
		// because the watchdog cancelled its context — nothing to write
		// here (and writing would race the watchdog's message).
		log.Printf("jobs: worker=%d timed-out run=%d %s", claim.workerID, claim.runID, formatJobMeta(meta))
		return
	case cancelled && taskErr == nil:
		// Cancelled (user or shutdown): if CancelRun already flipped
		// the row its transaction bumped the stats and this finishRun
		// no-ops; otherwise (shutdown) it wins here. Either way the
		// run is finalized and counted exactly once.
		log.Printf("jobs: worker=%d cancelled run=%d %s duration=%s",
			claim.workerID, claim.runID, formatJobMeta(meta), time.Since(started))
		m.finishRun(claim.runID, claim.defID, meta, StatusCancelled, "", "Cancelled", durationMS)
		m.emitEvent(RunEvent{Type: "cancelled", RunID: claim.runID, PluginID: meta.PluginID,
			JobName: meta.Name, Status: StatusCancelled, DurationMS: ptrInt64(durationMS), Error: ptrStr("Cancelled")})
		return
	case taskErr != nil:
		errStr := taskErr.Error()
		log.Printf("jobs: worker=%d errored run=%d %s duration=%s error=%q",
			claim.workerID, claim.runID, formatJobMeta(meta), time.Since(started), previewText(errStr, 240))
		won := m.finishRun(claim.runID, claim.defID, meta, StatusErrored, "", errStr, durationMS)
		if won {
			m.emitEvent(RunEvent{Type: "errored", RunID: claim.runID, PluginID: meta.PluginID,
				JobName: meta.Name, Status: StatusErrored, Error: &errStr,
				DurationMS: ptrInt64(durationMS), Attempt: claim.retryCount + 1})
			// Cancellation-induced errors are never auto-retried (the
			// user asked for it to stop; timeouts are requeued by the
			// watchdog, which owns that decision).
			if !cancelled {
				m.autoRetry(claim, meta, errStr)
			}
		}
		return
	default:
		log.Printf("jobs: worker=%d done run=%d %s duration=%s result=%q",
			claim.workerID, claim.runID, formatJobMeta(meta), time.Since(started), previewText(result, 240))
		m.finishRun(claim.runID, claim.defID, meta, StatusDone, result, "", durationMS)
		m.emitEvent(RunEvent{Type: "done", RunID: claim.runID, PluginID: meta.PluginID,
			JobName: meta.Name, Status: StatusDone, Result: &result,
			DurationMS: ptrInt64(durationMS), Attempt: claim.retryCount + 1})
	}
}

// runTaskWithRecover invokes the task, converting a panic into an error
// so a broken task can never take down a worker.
func runTaskWithRecover(ctx context.Context, task TaskFunc, rt *RunContext, claim dqClaim, meta jobDefMeta) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			log.Printf("jobs: worker=%d panicked run=%d %s panic=%q",
				claim.workerID, claim.runID, formatJobMeta(meta), previewText(fmt.Sprintf("%v", r), 240))
		}
	}()
	return task(ctx, rt)
}

// orLookupDefinition falls back to a DB lookup when the cached metadata
// is missing (definition without a registered task).
func orLookupDefinition(m *Manager, meta jobDefMeta, defID int64) jobDefMeta {
	if meta.ID == 0 {
		return m.lookupDefinition(defID)
	}
	return meta
}
