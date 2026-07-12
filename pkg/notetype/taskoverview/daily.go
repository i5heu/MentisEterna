package taskoverview

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// --- Daily task generation & persistence ---

// regenerateAllDailyTasks is the cron job that regenerates daily tasks
// for every task_overview note at midnight UTC.
func regenerateAllDailyTasks(db *sql.DB, payload []byte) (string, error) {
	tasks, err := loadAllTasks(db)
	if err != nil {
		return "", fmt.Errorf("load tasks: %w", err)
	}

	rows, err := db.Query(`SELECT id FROM notes WHERE type = 'task_overview'`)
	if err != nil {
		return "", fmt.Errorf("query overview notes: %w", err)
	}
	defer rows.Close()

	var noteIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		noteIDs = append(noteIDs, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	regenCount := 0
	now := nowUTC()
	for _, noteID := range noteIDs {
		cfg, err := loadOverviewConfig(db, noteID)
		if err != nil {
			return "", fmt.Errorf("load config for note %d: %w", noteID, err)
		}
		scoredOpenTasks := scoreOpenTasks(tasks, cfg, now)
		picked := selectDailyTasks(scoredOpenTasks, cfg, 0)
		if err := storeDailyTasks(db, noteID, picked); err != nil {
			return "", fmt.Errorf("store daily for note %d: %w", noteID, err)
		}
		regenCount++
	}

	return fmt.Sprintf("regenerated daily tasks for %d task_overview notes", regenCount), nil
}

// loadDailyTasks returns the most recent generation of daily tasks
// for a given overview note (the set with the highest assigned_at).
func loadDailyTasks(db *sql.DB, overviewNoteID int64) ([]TaskSummary, error) {
	rows, err := db.Query(`
		SELECT n.id, n.title, n.created_at,
		       COALESCE(u.body, '') AS body,
		       COALESCE(u.created_at, n.created_at) AS updated_at,
		       COALESCE(tc.status, 'todo'),
		       COALESCE(tc.priority, 0),
		       COALESCE(tc.difficulty, 0),
		       COALESCE(tc.fun, 0),
		       COALESCE(tc.due_date, ''),
		       COALESCE(tc.time_estimation, ''),
		       COALESCE(tc.time_used, ''),
		       COALESCE(tc.recurring, 'none'),
		       COALESCE(tc.completed_at, ''),
		       COALESCE(tc.pending_does_not_force_daily_inclusion, 0)
		FROM ct_taskoverview_daily d
		JOIN notes n ON n.id = d.task_note_id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		LEFT JOIN ct_task_config tc ON tc.note_id = n.id
		WHERE d.overview_note_id = ?
		AND d.assigned_at = (
			SELECT MAX(assigned_at) FROM ct_taskoverview_daily WHERE overview_note_id = ?
		)
		ORDER BY d.position ASC, d.assigned_at ASC, n.id ASC
	`, overviewNoteID, overviewNoteID)
	if err != nil {
		return nil, fmt.Errorf("task_overview: load daily: %w", err)
	}
	defer rows.Close()

	var tasks []TaskSummary
	for rows.Next() {
		var t TaskSummary
		if err := rows.Scan(&t.NoteID, &t.Title, &t.CreatedAt, &t.Body, &t.UpdatedAt,
			&t.Status, &t.Priority, &t.Difficulty, &t.Fun,
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt,
			&t.PendingDoesNotForceDailyInclusion); err != nil {
			return nil, fmt.Errorf("task_overview: scan daily: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
}

// storeDailyTasks persists daily task assignments for an overview note.
// Each call creates a new generation — all tasks share the same assigned_at timestamp.
func storeDailyTasks(db *sql.DB, overviewNoteID int64, tasks []TaskSummary) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	for idx, t := range tasks {
		if _, err := db.Exec(
			`INSERT INTO ct_taskoverview_daily (overview_note_id, task_note_id, assigned_at, position) VALUES (?, ?, ?, ?)`,
			overviewNoteID, t.NoteID, now, idx,
		); err != nil {
			return err
		}
	}
	return nil
}

// loadDailyHistory returns all past generations grouped by assigned_at,
// newest first, excluding the most recent generation (which is current).
func loadDailyHistory(db *sql.DB, overviewNoteID int64) ([]DailyHistoryEntry, error) {
	// Find the most recent generation timestamp.
	var latest string
	err := db.QueryRow(
		`SELECT MAX(assigned_at) FROM ct_taskoverview_daily WHERE overview_note_id = ?`,
		overviewNoteID,
	).Scan(&latest)
	if err != nil || latest == "" {
		return []DailyHistoryEntry{}, nil // no generations yet
	}

	// Load all rows except the most recent generation.
	rows, err := db.Query(`
		SELECT d.assigned_at, n.id, n.title, n.created_at,
		       COALESCE(u.body, '') AS body,
		       COALESCE(u.created_at, n.created_at) AS updated_at,
		       COALESCE(tc.status, 'todo'),
		       COALESCE(tc.priority, 0),
		       COALESCE(tc.difficulty, 0),
		       COALESCE(tc.fun, 0),
		       COALESCE(tc.due_date, ''),
		       COALESCE(tc.time_estimation, ''),
		       COALESCE(tc.time_used, ''),
		       COALESCE(tc.recurring, 'none'),
		       COALESCE(tc.completed_at, ''),
		       COALESCE(tc.pending_does_not_force_daily_inclusion, 0)
		FROM ct_taskoverview_daily d
		JOIN notes n ON n.id = d.task_note_id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		LEFT JOIN ct_task_config tc ON tc.note_id = n.id
		WHERE d.overview_note_id = ? AND d.assigned_at != ?
		ORDER BY d.assigned_at DESC, d.position ASC, n.title ASC
	`, overviewNoteID, latest)
	if err != nil {
		return nil, fmt.Errorf("task_overview: load history: %w", err)
	}
	defer rows.Close()

	// Group tasks by assigned_at in order (descending = newest first).
	type row struct {
		assignedAt string
		task       TaskSummary
	}
	var all []row
	for rows.Next() {
		var assignedAt string
		var t TaskSummary
		if err := rows.Scan(&assignedAt, &t.NoteID, &t.Title, &t.CreatedAt, &t.Body, &t.UpdatedAt,
			&t.Status, &t.Priority, &t.Difficulty, &t.Fun,
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt,
			&t.PendingDoesNotForceDailyInclusion); err != nil {
			return nil, fmt.Errorf("task_overview: scan history: %w", err)
		}
		all = append(all, row{assignedAt, t})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(all) == 0 {
		return []DailyHistoryEntry{}, nil
	}

	// Group consecutive rows with the same assigned_at.
	var result []DailyHistoryEntry
	var cur *DailyHistoryEntry
	for _, r := range all {
		if cur == nil || cur.GeneratedAt != r.assignedAt {
			result = append(result, DailyHistoryEntry{
				GeneratedAt: r.assignedAt,
				Tasks:       []TaskSummary{r.task},
			})
			cur = &result[len(result)-1]
		} else {
			cur.Tasks = append(cur.Tasks, r.task)
		}
	}
	return result, nil
}

// handleDailyTasks handles the "daily_tasks" action.
func handleDailyTasks(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p DailyTaskParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("task_overview: invalid params: %w", err)
		}
	}

	cfg, err := loadOverviewConfig(db, noteID)
	if err != nil {
		return nil, err
	}

	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}

	scoredOpenTasks := scoreOpenTasks(tasks, cfg, nowUTC())
	picked := selectDailyTasks(scoredOpenTasks, cfg, p.Count)

	// Persist so BuildView returns the same set.
	if err := storeDailyTasks(db, noteID, picked); err != nil {
		// Non-fatal: still return the picked tasks even if persist fails.
		fmt.Printf("task_overview: failed to store daily tasks for note %d: %v\n", noteID, err)
	}

	return map[string]any{
		"daily_tasks":       picked,
		"scored_open_tasks": scoredOpenTasks,
	}, nil
}
