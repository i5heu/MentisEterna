package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// refreshFeedsTask is the cron job task. Two invocation modes:
//
//   - cron-scheduled: payload nil/empty → refresh every due feed of every
//     reader note (per-feed interval_minutes since last fetch),
//   - action-enqueued: payload {"note_id":N,"force":true} → refresh all feeds
//     of that one reader regardless of interval.
//
// The full pipeline lands with the fetch subsystem (refresh.go); this minimal
// version only reports due readers so the job definition is registered early.
func (p *Plugin) refreshFeedsTask(db *sql.DB, payload []byte) (string, error) {
	noteID, force := int64(0), false
	if len(payload) > 0 {
		var pl struct {
			NoteID int64 `json:"note_id"`
			Force  bool  `json:"force"`
		}
		if err := json.Unmarshal(payload, &pl); err != nil {
			return "", fmt.Errorf("rss: invalid refresh payload: %w", err)
		}
		noteID, force = pl.NoteID, pl.Force
	}

	if noteID > 0 {
		return refreshNote(db, noteID, force)
	}

	now := time.Now().UTC()
	var due, total int
	rows, err := db.Query(`SELECT note_id FROM ct_rss_config`)
	if err != nil {
		return "", fmt.Errorf("rss: list readers: %w", err)
	}
	defer rows.Close()
	var noteIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("rss: scan reader: %w", err)
		}
		noteIDs = append(noteIDs, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("rss: iterate readers: %w", err)
	}
	for _, id := range noteIDs {
		cfg, err := loadConfigRow(db, id)
		if err != nil {
			return "", err
		}
		total++
		if refreshDue(db, id, cfg, now) {
			due++
		}
	}
	return fmt.Sprintf("rss: %d reader(s), %d due (fetch pipeline not yet wired)", total, due), nil
}

// refreshDue reports whether any enabled feed of the reader is due. The real
// check uses feeds.last_fetch_at; without the fetch pipeline every feed with
// pending work counts as due.
func refreshDue(db *sql.DB, noteID int64, cfg Config, now time.Time) bool {
	rows, err := db.Query(
		`SELECT last_fetch_at, enabled FROM ct_rss_feeds WHERE note_id = ?`, noteID,
	)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var lastFetch sql.NullString
		var enabled int
		if err := rows.Scan(&lastFetch, &enabled); err != nil {
			return false
		}
		if enabled == 0 {
			continue
		}
		if !lastFetch.Valid {
			return true
		}
		t, err := time.Parse("2006-01-02T15:04:05.000Z", lastFetch.String)
		if err != nil {
			t, err = time.Parse(timeFormat, lastFetch.String)
		}
		if err != nil || now.Sub(t) >= time.Duration(cfg.IntervalMinutes)*time.Minute {
			return true
		}
	}
	return false
}

const timeFormat = "2006-01-02T15:04:05Z"

// refreshNote refreshes all feeds of a single reader note. The real fetch
// pipeline lands in refresh.go.
func refreshNote(db *sql.DB, noteID int64, force bool) (string, error) {
	_ = force // full-force fetching arrives with the fetch pipeline
	var enabled int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM ct_rss_feeds WHERE note_id = ? AND enabled = 1`,
		noteID,
	).Scan(&enabled); err != nil {
		return "", fmt.Errorf("rss: count feeds: %w", err)
	}
	return fmt.Sprintf("rss: reader %d: %d enabled feed(s) (fetch pipeline not yet wired)", noteID, enabled), nil
}
