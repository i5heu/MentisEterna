package taskoverview

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// handleQuickSetStatus updates a task's status directly from the overview.
func handleQuickSetStatus(db *sql.DB, params json.RawMessage) (any, error) {
	var p QuickSetStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("task_overview: invalid params: %w", err)
	}
	if p.TaskNoteID <= 0 {
		return nil, fmt.Errorf("task_overview: task_note_id is required")
	}

	validStatuses := map[string]bool{
		"todo":        true,
		"in_progress": true,
		"done":        true,
	}
	if !validStatuses[p.Status] {
		return nil, fmt.Errorf("task_overview: invalid status %q", p.Status)
	}

	// Read current config to preserve other fields.
	var status, completedAt, description, dueDate, timeEst, timeUsed, recurring string
	var difficulty, fun, priority, recurringDays int
	var pendingDoesNotForceDailyInclusion bool
	err := db.QueryRow(
		`SELECT status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, COALESCE(completed_at, ''), COALESCE(pending_does_not_force_daily_inclusion, 0)
		 FROM ct_task_config WHERE note_id = ?`, p.TaskNoteID,
	).Scan(&status, &difficulty, &fun, &priority, &description, &dueDate, &timeEst, &timeUsed, &recurring, &recurringDays, &completedAt, &pendingDoesNotForceDailyInclusion)
	if err != nil {
		if err == sql.ErrNoRows {
			// No config yet — create one.
			now := ""
			if p.Status == "done" {
				now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
			}
			_, err = db.Exec(
				`INSERT INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at, pending_does_not_force_daily_inclusion)
				 VALUES (?, ?, 0, 0, 0, '', '', '', '', 'none', 0, ?, 0)`,
				p.TaskNoteID, p.Status, now,
			)
		} else {
			return nil, fmt.Errorf("task_overview: read task: %w", err)
		}
	} else {
		now := completedAt
		if p.Status == "done" && completedAt == "" {
			now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
		} else if p.Status != "done" {
			now = ""
		}
		_, err = db.Exec(
			`INSERT OR REPLACE INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at, pending_does_not_force_daily_inclusion)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.TaskNoteID, p.Status, difficulty, fun, priority, description, dueDate, timeEst, timeUsed, recurring, recurringDays, now, pendingDoesNotForceDailyInclusion,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("task_overview: update status: %w", err)
	}

	return map[string]any{
		"task_note_id": p.TaskNoteID,
		"status":       p.Status,
	}, nil
}
