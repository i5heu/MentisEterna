package jobs

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// runWatchdog is the safety net while the server runs (zombie recovery
// covers crashes at startup). It ticks at m.watchdogInterval:
//
//   - enforceTimeouts: cancels and finalizes runs that exceeded their
//     definition's timeout, requeueing them per the retry policy;
//   - clearLostClaims: marks running rows that no worker of this
//     manager tracks as errored (manual DB edits, lost claims, bugs);
//   - wakeDueRetries: kicks workers when delayed auto-retries become
//     due, so they start without waiting for the fallback poll.
func (m *Manager) runWatchdog(wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(m.watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
		}
		m.enforceTimeouts()
		m.clearLostClaims()
		m.wakeDueRetries()
	}
}

// enforceTimeouts cancels running tasks whose definition timeout has
// expired, finalizes their rows (the worker's own final update then
// no-ops thanks to the status guard), and requeues them if the retry
// policy allows.
func (m *Manager) enforceTimeouts() {
	type victim struct {
		runID int64
		ar    *activeRun
		meta  jobDefMeta
		over  time.Duration
	}
	var victims []victim

	now := time.Now()
	m.activeMu.Lock()
	for _, ar := range m.active {
		meta := m.lookupDefinition(ar.defID)
		if meta.Timeout <= 0 {
			continue
		}
		if over := now.Sub(ar.started); over > meta.Timeout {
			victims = append(victims, victim{runID: ar.runID, ar: ar, meta: meta, over: over})
			ar.timedOut.Store(true) // before cancel: the worker reads it when the task returns
			ar.cancel()
		}
	}
	m.activeMu.Unlock()

	for _, v := range victims {
		errMsg := fmt.Sprintf("timed out after %s (limit %s)", v.over.Round(time.Second), v.meta.Timeout)
		log.Printf("jobs: watchdog timeout run=%d %s %s", v.runID, formatJobMeta(v.meta), errMsg)

		// Recover the original payload and attempt so the requeue is a
		// faithful retry of the same work.
		var payloadStr *string
		var retryCount int
		_ = m.db.QueryRow(
			`SELECT payload, retry_count FROM job_runs WHERE id = ?`, v.runID,
		).Scan(&payloadStr, &retryCount)
		var payload []byte
		if payloadStr != nil {
			payload = []byte(*payloadStr)
		}

		m.finishRun(v.runID, v.ar.defID, v.meta, StatusErrored, "", errMsg, v.meta.Timeout.Milliseconds())
		m.emitEvent(RunEvent{Type: "errored", RunID: v.runID, PluginID: v.meta.PluginID,
			JobName: v.meta.Name, Status: StatusErrored, Error: &errMsg})
		m.autoRetry(dqClaim{runID: v.runID, defID: v.ar.defID, payload: payload, retryCount: retryCount}, v.meta, errMsg)
	}
}

// clearLostClaims marks running rows that no worker of this manager
// tracks as errored, so a wedged/inconsistent row can never count
// against a definition's max_parallel forever.
func (m *Manager) clearLostClaims() {
	rows, err := m.db.Query(`SELECT id, job_id FROM job_runs WHERE status = ?`, StatusRunning)
	if err != nil {
		return
	}
	type lostRun struct {
		runID int64
		defID int64
	}
	var candidates []lostRun
	for rows.Next() {
		var l lostRun
		if err := rows.Scan(&l.runID, &l.defID); err != nil {
			continue
		}
		candidates = append(candidates, l)
	}
	rows.Close()
	if len(candidates) == 0 {
		return
	}

	m.activeMu.Lock()
	tracked := make(map[int64]bool, len(m.active))
	for id := range m.active {
		tracked[id] = true
	}
	m.activeMu.Unlock()

	for _, l := range candidates {
		if tracked[l.runID] {
			continue
		}
		meta := m.lookupDefinition(l.defID)
		errMsg := "run lost its worker (crashed claim); cleared by watchdog"
		log.Printf("jobs: watchdog clearing run=%d %s", l.runID, formatJobMeta(meta))
		m.finishRun(l.runID, l.defID, meta, StatusErrored, "", errMsg, 0)
		m.emitEvent(RunEvent{Type: "errored", RunID: l.runID, PluginID: meta.PluginID,
			JobName: meta.Name, Status: StatusErrored, Error: ptrStr(errMsg)})
	}
}

// wakeDueRetries kicks workers when delayed retry runs become due, so
// they start without waiting for the fallback poll.
func (m *Manager) wakeDueRetries() {
	var n int
	if err := m.db.QueryRow(
		`SELECT COUNT(*) FROM job_runs
		 WHERE status = ? AND next_run_at IS NOT NULL AND next_run_at <= strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		StatusPlanned,
	).Scan(&n); err != nil {
		return
	}
	if n > 0 {
		m.kick()
	}
}
