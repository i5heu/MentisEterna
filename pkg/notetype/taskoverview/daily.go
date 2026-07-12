package taskoverview

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// --- Daily task generation & persistence ---

var dailyGenerationSeq atomic.Uint64

var nextDailyGenerationID = func(assignedAt string) string {
	return fmt.Sprintf("%s#%020d", assignedAt, dailyGenerationSeq.Add(1))
}

type latestDailyGeneration struct {
	generationID string
	assignedAt   string
}

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
// for a given overview note.
func loadDailyTasks(db *sql.DB, overviewNoteID int64) ([]TaskSummary, error) {
	latest, err := findLatestDailyGeneration(db, overviewNoteID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return []TaskSummary{}, nil
	}

	query := `
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
		WHERE d.overview_note_id = ? AND %s
		ORDER BY d.position ASC, d.assigned_at ASC, n.id ASC
	`

	filter := "d.generation_id = ?"
	args := []any{overviewNoteID, latest.generationID}
	if latest.generationID == "" {
		filter = "d.generation_id = '' AND d.assigned_at = ?"
		args = []any{overviewNoteID, latest.assignedAt}
	}

	rows, err := db.Query(fmt.Sprintf(query, filter), args...)
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

	if tasks == nil {
		return []TaskSummary{}, nil
	}
	return tasks, nil
}

func findLatestDailyGeneration(db *sql.DB, overviewNoteID int64) (*latestDailyGeneration, error) {
	var latest latestDailyGeneration
	err := db.QueryRow(`
		SELECT generation_id, assigned_at
		FROM ct_taskoverview_daily
		WHERE overview_note_id = ?
		ORDER BY assigned_at DESC, generation_id DESC, position DESC, task_note_id DESC
		LIMIT 1
	`, overviewNoteID).Scan(&latest.generationID, &latest.assignedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("task_overview: latest daily generation: %w", err)
	}
	return &latest, nil
}

// storeDailyTasks persists daily task assignments for an overview note.
// Each call creates a new generation and stores all rows atomically.
func storeDailyTasks(db *sql.DB, overviewNoteID int64, tasks []TaskSummary) (err error) {
	assignedAt := nowUTC().Format("2006-01-02T15:04:05Z")
	generationID := nextDailyGenerationID(assignedAt)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("task_overview: begin daily generation tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for idx, t := range tasks {
		if _, err = tx.Exec(
			`INSERT INTO ct_taskoverview_daily (overview_note_id, generation_id, task_note_id, assigned_at, position) VALUES (?, ?, ?, ?, ?)`,
			overviewNoteID, generationID, t.NoteID, assignedAt, idx,
		); err != nil {
			return fmt.Errorf("task_overview: insert daily task: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("task_overview: commit daily generation tx: %w", err)
	}
	return nil
}

// loadDailyHistory returns all past generations grouped by generation,
// newest first, excluding the most recent generation (which is current).
func loadDailyHistory(db *sql.DB, overviewNoteID int64) ([]DailyHistoryEntry, error) {
	latest, err := findLatestDailyGeneration(db, overviewNoteID)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return []DailyHistoryEntry{}, nil
	}

	exclusion := "(d.generation_id != ? OR d.generation_id = '')"
	args := []any{overviewNoteID, latest.generationID}
	if latest.generationID == "" {
		exclusion = "NOT (d.generation_id = '' AND d.assigned_at = ?)"
		args = []any{overviewNoteID, latest.assignedAt}
	}

	rows, err := db.Query(fmt.Sprintf(`
		SELECT d.generation_id, d.assigned_at, n.id, n.title, n.created_at,
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
		WHERE d.overview_note_id = ? AND %s
		ORDER BY d.assigned_at DESC, d.generation_id DESC, d.position ASC, n.title ASC
	`, exclusion), args...)
	if err != nil {
		return nil, fmt.Errorf("task_overview: load history: %w", err)
	}
	defer rows.Close()

	type row struct {
		generationKey string
		assignedAt    string
		task          TaskSummary
	}
	var all []row
	for rows.Next() {
		var generationID, assignedAt string
		var t TaskSummary
		if err := rows.Scan(&generationID, &assignedAt, &t.NoteID, &t.Title, &t.CreatedAt, &t.Body, &t.UpdatedAt,
			&t.Status, &t.Priority, &t.Difficulty, &t.Fun,
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt,
			&t.PendingDoesNotForceDailyInclusion); err != nil {
			return nil, fmt.Errorf("task_overview: scan history: %w", err)
		}
		generationKey := generationID
		if generationKey == "" {
			generationKey = assignedAt
		}
		all = append(all, row{generationKey: generationKey, assignedAt: assignedAt, task: t})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(all) == 0 {
		return []DailyHistoryEntry{}, nil
	}

	var result []DailyHistoryEntry
	var cur *DailyHistoryEntry
	var curKey string
	for _, r := range all {
		if cur == nil || curKey != r.generationKey {
			result = append(result, DailyHistoryEntry{
				GeneratedAt: r.assignedAt,
				Tasks:       []TaskSummary{r.task},
			})
			cur = &result[len(result)-1]
			curKey = r.generationKey
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
