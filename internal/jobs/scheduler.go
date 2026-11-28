package jobs

import (
	"log"
	"strings"
	"sync"
	"time"
)

// runScheduler inserts a planned job_run row at each tick for one cron
// definition. Ticks never pile up: if an active run for the definition
// already exists (e.g. the previous @every 15m run is still going), the
// tick is skipped — a slow job can never spawn a queue of itself.
func (m *Manager) runScheduler(defID int64, schedule string, wg *sync.WaitGroup) {
	defer wg.Done()

	meta := m.lookupDefinition(defID)
	interval, ok := parseSimpleSchedule(schedule)
	if !ok {
		log.Printf("jobs: scheduler unsupported %s schedule=%q", formatJobMeta(meta), schedule)
		return
	}

	log.Printf("jobs: scheduler started %s schedule=%q", formatJobMeta(meta), schedule)

	for {
		select {
		case <-m.stop:
			log.Printf("jobs: scheduler stopping %s", formatJobMeta(meta))
			return
		case <-time.After(nextTickDelay(schedule, interval)):
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

		// Skip if an active run for this definition already exists —
		// slow runs must not make cron ticks pile up.
		var activeCount int
		if err := m.db.QueryRow(
			`SELECT COUNT(*) FROM job_runs WHERE job_id = ? AND status IN (?, ?)`,
			defID, StatusPlanned, StatusRunning,
		).Scan(&activeCount); err != nil {
			log.Printf("jobs: scheduler active check failed %s: %v", formatJobMeta(meta), err)
			continue
		}
		if activeCount > 0 {
			log.Printf("jobs: scheduler skipped tick for %s (%d active run(s) exist)", formatJobMeta(meta), activeCount)
			continue
		}

		id, duplicate, err := m.insertPlannedRun(defID, nil, 0, nil)
		if err != nil {
			log.Printf("jobs: scheduler enqueue failed %s: %v", formatJobMeta(meta), err)
			continue
		}
		if duplicate {
			continue
		}
		log.Printf("jobs: scheduler enqueued run=%d %s", id, formatJobMeta(meta))
		m.emitEvent(RunEvent{Type: "enqueued", RunID: id, PluginID: meta.PluginID, JobName: meta.Name, Status: StatusPlanned})
		m.kick()
	}
}

// nextTickDelay computes the sleep until the next tick: "@every" sleeps
// the fixed interval; calendar-style schedules (hour/day) sleep until
// the next whole boundary.
func nextTickDelay(schedule string, interval time.Duration) time.Duration {
	if strings.HasPrefix(schedule, "@every ") {
		return interval
	}
	now := time.Now()
	next := now.Truncate(interval).Add(interval)
	if !next.After(now) {
		next = next.Add(interval)
	}
	return next.Sub(now)
}

// parseSimpleSchedule parses the supported schedule formats:
// "@every <duration>", "@hourly", "@daily".
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
