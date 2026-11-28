package jobs

import (
	"fmt"
	"time"
)

// WorkerStats describes the worker pools at a moment in time.
type WorkerStats struct {
	Total     int `json:"total"`      // normal + fast
	Busy      int `json:"busy"`       // workers currently executing a run
	FastTotal int `json:"fast_total"` // dedicated high-priority pool
	FastBusy  int `json:"fast_busy"`  // fast workers executing a run
	Idle      int `json:"idle"`       // Total - Busy
	FastIdle  int `json:"fast_idle"`  // FastTotal - FastBusy
}

// QueueStats is the queue depth by state.
type QueueStats struct {
	Planned int `json:"planned"` // ready to run (or waiting for a slot)
	Running int `json:"running"` // executing now
	Delayed int `json:"delayed"` // planned with a future next_run_at (backoff)
}

// WindowStats aggregates recent activity (last 24h).
type WindowStats struct {
	Done      int   `json:"done"`
	Errored   int   `json:"errored"`
	Started   int   `json:"started"`
	DoneAvgMS int64 `json:"done_avg_ms"`
}

// DefinitionStats is the per-job-definition ledger + live counters.
type DefinitionStats struct {
	ID             int64  `json:"id"`
	PluginID       string `json:"plugin_id"`
	Name           string `json:"job_name"`
	Schedule       string `json:"schedule"`
	Enabled        bool   `json:"enabled"`
	Priority       int    `json:"priority"`
	MaxParallel    int    `json:"max_parallel"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxRetries     int    `json:"max_retries"`

	// Live state.
	Running int `json:"running"`
	Queued  int `json:"queued"`

	// Lifetime ledger (job_definition_stats; survives run pruning).
	RunsTotal       int     `json:"runs_total"`
	RunsDone        int     `json:"runs_done"`
	RunsErrored     int     `json:"runs_errored"`
	RunsCancelled   int     `json:"runs_cancelled"`
	AutoRetries     int     `json:"auto_retries"`
	SuccessRate     float64 `json:"success_rate"` // done / (done+errored)
	TotalDurationMS int64   `json:"total_duration_ms"`
	AvgDurationMS   int64   `json:"avg_duration_ms,omitempty"`
	MinDurationMS   *int64  `json:"min_duration_ms,omitempty"`
	MaxDurationMS   *int64  `json:"max_duration_ms,omitempty"`

	LastFinishedAt *time.Time `json:"last_finished_at,omitempty"`
	LastStatus     *string    `json:"last_status,omitempty"`
	LastResult     *string    `json:"last_result,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
}

// StatsResponse is the payload for GET /jobs/stats.
type StatsResponse struct {
	Workers     WorkerStats       `json:"workers"`
	Queue       QueueStats        `json:"queue"`
	Last24h     WindowStats       `json:"last_24h"`
	Definitions []DefinitionStats `json:"definitions"`
}

// Stats builds the full job-system snapshot.
func (m *Manager) Stats() (*StatsResponse, error) {
	resp := &StatsResponse{Definitions: []DefinitionStats{}}

	// Worker pools: live state from the active map.
	m.activeMu.Lock()
	resp.Workers = WorkerStats{
		Total:     m.workers + m.fastWorker,
		FastTotal: m.fastWorker,
	}
	for _, ar := range m.active {
		if ar.fast {
			resp.Workers.FastBusy++
		} else {
			resp.Workers.Busy++
		}
	}
	m.activeMu.Unlock()
	resp.Workers.Idle = resp.Workers.Total - resp.Workers.Busy - resp.Workers.FastBusy
	resp.Workers.FastIdle = resp.Workers.FastTotal - resp.Workers.FastBusy

	// Queue depth.
	if err := m.db.QueryRow(
		`SELECT
		   SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
		   SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
		   SUM(CASE WHEN status = ? AND next_run_at IS NOT NULL
		              AND next_run_at > strftime('%Y-%m-%dT%H:%M:%fZ','now') THEN 1 ELSE 0 END)
		 FROM job_runs`,
		StatusPlanned, StatusRunning, StatusPlanned,
	).Scan(&resp.Queue.Planned, &resp.Queue.Running, &resp.Queue.Delayed); err != nil {
		return nil, fmt.Errorf("job queue stats: %w", err)
	}

	// 24h window.
	rows, err := m.db.Query(
		`SELECT status, COUNT(*), COALESCE(AVG(duration_ms), 0)
		 FROM job_runs
		 WHERE created_at >= datetime('now', '-1 day') AND status IN (?, ?)
		 GROUP BY status`,
		StatusDone, StatusErrored,
	)
	if err != nil {
		return nil, fmt.Errorf("job window stats: %w", err)
	}
	for rows.Next() {
		var status string
		var count int
		var avg int64
		if err := rows.Scan(&status, &count, &avg); err != nil {
			rows.Close()
			return nil, fmt.Errorf("job window stats scan: %w", err)
		}
		switch status {
		case StatusDone:
			resp.Last24h.Done = count
			resp.Last24h.DoneAvgMS = avg
		case StatusErrored:
			resp.Last24h.Errored = count
		}
	}
	rows.Close()

	// Per-definition ledger + live counters.
	drows, err := m.db.Query(
		`SELECT jd.id, jd.plugin_id, jd.name, jd.schedule, jd.enabled,
		        COALESCE(jd.priority, 0), COALESCE(jd.max_parallel, 1),
		        COALESCE(jd.timeout_seconds, 0), COALESCE(jd.max_retries, 0),
		        COALESCE(s.runs_total, 0), COALESCE(s.runs_done, 0),
		        COALESCE(s.runs_errored, 0), COALESCE(s.runs_cancelled, 0),
		        COALESCE(s.auto_retries, 0),
		        COALESCE(s.total_duration_ms, 0),
		        s.min_duration_ms, s.max_duration_ms,
		        s.last_finished_at, s.last_status, s.last_result, s.last_error,
		        (SELECT COUNT(*) FROM job_runs r WHERE r.job_id = jd.id AND r.status = 'running'),
		        (SELECT COUNT(*) FROM job_runs r WHERE r.job_id = jd.id AND r.status = 'planned')
		 FROM job_definitions jd
		 LEFT JOIN job_definition_stats s ON s.job_id = jd.id
		 ORDER BY jd.plugin_id, jd.name`,
	)
	if err != nil {
		return nil, fmt.Errorf("job definition stats: %w", err)
	}
	defer drows.Close()
	for drows.Next() {
		var ds DefinitionStats
		var lastFinished, lastStatus, lastResult, lastError *string
		var minDur, maxDur *int64
		if err := drows.Scan(
			&ds.ID, &ds.PluginID, &ds.Name, &ds.Schedule, &ds.Enabled,
			&ds.Priority, &ds.MaxParallel, &ds.TimeoutSeconds, &ds.MaxRetries,
			&ds.RunsTotal, &ds.RunsDone, &ds.RunsErrored, &ds.RunsCancelled,
			&ds.AutoRetries, &ds.TotalDurationMS,
			&minDur, &maxDur,
			&lastFinished, &lastStatus, &lastResult, &lastError,
			&ds.Running, &ds.Queued,
		); err != nil {
			return nil, fmt.Errorf("job definition stats scan: %w", err)
		}
		ds.MinDurationMS = minDur
		ds.MaxDurationMS = maxDur
		ds.LastFinishedAt = parseTimePtr(lastFinished)
		ds.LastStatus = lastStatus
		ds.LastResult = lastResult
		ds.LastError = lastError
		if attempted := ds.RunsDone + ds.RunsErrored; attempted > 0 {
			ds.SuccessRate = float64(ds.RunsDone) / float64(attempted)
		}
		if ds.RunsDone > 0 {
			ds.AvgDurationMS = ds.TotalDurationMS / int64(ds.RunsDone)
		}
		resp.Definitions = append(resp.Definitions, ds)
	}
	if err := drows.Err(); err != nil {
		return nil, fmt.Errorf("job definition stats iterate: %w", err)
	}
	resp.Last24h.Started = resp.Last24h.Done + resp.Last24h.Errored

	return resp, nil
}
