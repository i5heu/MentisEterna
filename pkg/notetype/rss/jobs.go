package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// refreshFeedsTask is the cron job task (definition "rss/refresh_feeds").
// Two invocation modes:
//
//   - cron-scheduled: payload nil/empty → refresh every due reader (per-feed
//     interval_minutes freshness),
//   - action-enqueued: payload {"note_id":N,"force":true} → force-refresh all
//     enabled feeds of that one reader.
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
		noteID, force = pl.NoteID, pl.Force || pl.NoteID > 0
	}

	// Force mode: one reader, everything refreshed.
	if noteID > 0 {
		return refreshNote(db, noteID, force)
	}

	// Cron mode: iterate readers, refresh those with due feeds.
	now := time.Now().UTC()
	rows, err := db.Query(`SELECT note_id FROM ct_rss_config`)
	if err != nil {
		return "", fmt.Errorf("rss: list readers: %w", err)
	}
	var noteIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", fmt.Errorf("rss: scan reader: %w", err)
		}
		noteIDs = append(noteIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("rss: iterate readers: %w", err)
	}
	rows.Close()

	summaries := make([]string, 0, len(noteIDs))
	for _, id := range noteIDs {
		cfg, err := loadConfigRow(db, id)
		if err != nil {
			summaries = append(summaries, fmt.Sprintf("reader %d: %v", id, err))
			continue
		}
		if !refreshDue(db, id, cfg, now) {
			continue
		}
		summary, err := refreshNote(db, id, false)
		if err != nil {
			return "", err
		}
		summaries = append(summaries, fmt.Sprintf("note %d: %s", id, summary))
	}
	if len(summaries) == 0 {
		return "no readers due", nil
	}
	total := 0
	if err := db.QueryRow(`SELECT COUNT(*) FROM ct_rss_config`).Scan(&total); err != nil {
		return "", fmt.Errorf("rss: count readers: %w", err)
	}
	return fmt.Sprintf("%d/%d reader(s): %s", len(summaries), total, joinNonEmpty("; ", summaries)), nil
}

func joinNonEmpty(sep string, parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}

// refreshDue reports whether any enabled feed of the reader is due for a
// fetch under its interval (or has never been fetched).
func refreshDue(db *sql.DB, noteID int64, cfg Config, now time.Time) bool {
	rows, err := db.Query(
		`SELECT COALESCE(last_fetch_at, ''), enabled FROM ct_rss_feeds WHERE note_id = ?`, noteID,
	)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var lastFetch string
		var enabled int
		if err := rows.Scan(&lastFetch, &enabled); err != nil {
			return false
		}
		if enabled == 0 {
			continue
		}
		if lastFetch == "" {
			return true
		}
		if t := parseDBTime(lastFetch); t.IsZero() {
			return true
		} else if now.Sub(t) >= time.Duration(cfg.IntervalMinutes)*time.Minute {
			return true
		}
	}
	return false
}
