package jobs

import (
	"fmt"
	"sync"
	"time"
)

// makeProgress returns the throttled Progress reporter for one run.
//
// Emit gates (first match wins):
//   - never emitted before                        → emit
//   - finished (done >= total)                    → emit (always)
//   - message changed (stage switches etc.)       → emit (always)
//   - ≥ progressHeartbeat since last emit (10 s)  → emit (liveness)
//   - ≥ progressMinInterval (500 ms)              → emit (rate cap)
//
// Everything else is dropped, so chatty tasks (per-item progress) cost
// at most ~2 writes/s per run, and progress_at always stays fresh
// within the heartbeat window. The write is guarded on status:
// once the run is no longer running, progress reports are dropped.
func (m *Manager) makeProgress(runID int64, meta jobDefMeta) func(done, total int, message string) {
	var mu sync.Mutex
	var lastEmit time.Time
	var lastMsg string
	var emitted bool

	return func(done, total int, message string) {
		// Clamp into [0, total].
		if done < 0 {
			done = 0
		}
		if total < 0 {
			total = 0
		}
		if total > 0 && done > total {
			done = total
		}

		now := time.Now()
		mu.Lock()
		finished := total > 0 && done >= total
		msgChanged := message != "" && message != lastMsg
		heartbeat := now.Sub(lastEmit) >= m.progressHeartbeat
		rateOK := now.Sub(lastEmit) >= m.progressMinInterval
		if emitted && !finished && !msgChanged && !heartbeat && !rateOK {
			mu.Unlock()
			return
		}
		emitted = true
		lastEmit = now
		lastMsg = message
		mu.Unlock()

		res, err := m.db.Exec(
			`UPDATE job_runs
			 SET progress_done = ?, progress_total = ?, progress_message = ?,
			     progress_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			 WHERE id = ? AND status = ?`,
			done, total, message, runID, StatusRunning,
		)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return // run is no longer running (cancelled/finished) — drop
			}
		}
		displayMsg := message
		if displayMsg == "" {
			displayMsg = fmt.Sprintf("%d/%d", done, total)
		}
		m.emitEvent(RunEvent{
			Type:            "progress",
			RunID:           runID,
			PluginID:        meta.PluginID,
			JobName:         meta.Name,
			Status:          StatusRunning,
			ProgressDone:    &done,
			ProgressTotal:   &total,
			ProgressMessage: &displayMsg,
		})
	}
}
