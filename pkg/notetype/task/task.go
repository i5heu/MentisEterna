// Package task implements a "task" note type for managing personal tasks
// with priority, difficulty, fun, due date, time estimation, time tracking,
// and recurrence options.
package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "task"

func init() {
	notetype.Register(&TaskPlugin{})
}

type TaskPlugin struct {
	// enqueue is injected by the server via notetype.JobEnqueuer and used to
	// schedule background jobs from actions (e.g. generate_subtasks).
	enqueue func(pluginID, jobName string, payload []byte) (int64, error)
}

func (p *TaskPlugin) ID() string { return pluginID }

// SetJobEnqueueFunc implements notetype.JobEnqueuer.
func (p *TaskPlugin) SetJobEnqueueFunc(fn func(pluginID, jobName string, payload []byte) (int64, error)) {
	p.enqueue = fn
}

func (p *TaskPlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_task_config (
			note_id                                    INTEGER PRIMARY KEY,
			status                                     TEXT    NOT NULL DEFAULT 'todo',
			difficulty                                 INTEGER NOT NULL DEFAULT 0,
			fun                                        INTEGER NOT NULL DEFAULT 0,
			priority                                   INTEGER NOT NULL DEFAULT 0,
			description                                TEXT    NOT NULL DEFAULT '',
			due_date                                   TEXT    NOT NULL DEFAULT '',
			time_estimation                            TEXT    NOT NULL DEFAULT '',
			time_used                                  TEXT    NOT NULL DEFAULT '',
			recurring                                  TEXT    NOT NULL DEFAULT 'none',
			recurring_days                             INTEGER NOT NULL DEFAULT 0,
			completed_at                               TEXT    NOT NULL DEFAULT '',
			pending_does_not_force_daily_inclusion     INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_task_config_status
			ON ct_task_config(status);
		CREATE INDEX IF NOT EXISTS idx_ct_task_config_due
			ON ct_task_config(due_date);
		CREATE INDEX IF NOT EXISTS idx_ct_task_config_priority
			ON ct_task_config(priority);
		CREATE TABLE IF NOT EXISTS ct_task_subtasks (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id     INTEGER NOT NULL,
			label       TEXT    NOT NULL DEFAULT '',
			description TEXT    NOT NULL DEFAULT '',
			checked     INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_task_subtasks_note ON ct_task_subtasks(note_id);
	`)
	if err != nil {
		return err
	}
	db.Exec(`ALTER TABLE ct_task_config ADD COLUMN pending_does_not_force_daily_inclusion INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE ct_task_subtasks ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
	return nil
}

// TaskConfig is the JSON structure for task configuration.
type TaskConfig struct {
	Status                            string `json:"status"`          // "todo", "in_progress", "done"
	Difficulty                        int    `json:"difficulty"`      // 0 to 10
	Fun                               int    `json:"fun"`             // -5 to 5
	Priority                          int    `json:"priority"`        // 0 to 10
	Description                       string `json:"description"`     // detailed task description
	DueDate                           string `json:"due_date"`        // ISO 8601 date or empty
	TimeEstimation                    string `json:"time_estimation"` // e.g. "2h", "30m", "1d"
	TimeUsed                          string `json:"time_used"`       // e.g. "1h30m"
	Recurring                         string `json:"recurring"`       // "none", "daily", "weekly", "monthly", "custom"
	RecurringDays                     int    `json:"recurring_days"`  // custom interval in days
	CompletedAt                       string    `json:"completed_at"`    // ISO 8601 timestamp
	PendingDoesNotForceDailyInclusion bool      `json:"pending_does_not_force_daily_inclusion"`
	SubTasks                          []SubTask `json:"subtasks"`        // checklist items
}

// SubTask is a single checklist item within a task.
type SubTask struct {
	ID          int64  `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Checked     bool   `json:"checked"`
}

// SetStatusParams is the JSON body for the set_status action.
type SetStatusParams struct {
	Status string `json:"status"` // "todo", "in_progress", "done"
}

// ToggleSubtaskParams is the JSON body for the toggle_subtask action.
type ToggleSubtaskParams struct {
	SubtaskID int64 `json:"subtask_id"`
	Checked   bool  `json:"checked"`
}

var validStatuses = map[string]bool{
	"todo":        true,
	"in_progress": true,
	"done":        true,
}

var validRecurring = map[string]bool{
	"none":    true,
	"daily":   true,
	"weekly":  true,
	"monthly": true,
	"custom":  true,
}

func (p *TaskPlugin) CronJobs() []notetype.CronJob {
	return nil
}

func (p *TaskPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "task",
		Label:         "Task",
		Description:   "A task with priority, difficulty, fun, due date, time tracking, and recurrence",
		Category:      "Productivity",
		SortOrder:     400,
		DefaultConfig: json.RawMessage(`{"status":"todo","difficulty":0,"fun":0,"priority":0,"description":"","due_date":"","time_estimation":"","time_used":"","recurring":"none","recurring_days":0,"completed_at":"","pending_does_not_force_daily_inclusion":false,"subtasks":[]}`),
		Editor: notetype.EditorMeta{
			Mode: "custom",
			Schema: json.RawMessage(`[
	{
		"$formkit": "select",
		"name": "status",
		"label": "Status",
		"options": [
			{"label": "To Do", "value": "todo"},
			{"label": "In Progress", "value": "in_progress"},
			{"label": "Done", "value": "done"}
		]
	},
	{
		"$formkit": "range",
		"name": "priority",
		"label": "Priority",
		"min": "0",
		"max": "10",
		"step": "1",
		"help": "0 = low, 10 = critical"
	},
	{
		"$formkit": "range",
		"name": "difficulty",
		"label": "Difficulty",
		"min": "0",
		"max": "10",
		"step": "1",
		"help": "0 = trivial, 10 = extremely hard"
	},
	{
		"$formkit": "range",
		"name": "fun",
		"label": "Fun",
		"min": "-5",
		"max": "5",
		"step": "1",
		"help": "-5 = dreadful, 0 = neutral, 5 = amazing"
	},
	{
		"$formkit": "checkbox",
		"name": "pending_does_not_force_daily_inclusion",
		"label": "Don't auto-include this task in Daily Tasks while it is In Progress"
	},
	{
		"$formkit": "textarea",
		"name": "description",
		"label": "Description",
		"rows": "4"
	},
	{
		"$formkit": "date",
		"name": "due_date",
		"label": "Due Date"
	},
	{
		"$formkit": "text",
		"name": "time_estimation",
		"label": "Time Estimation",
		"help": "e.g. 2h, 30m, 1d"
	},
	{
		"$formkit": "text",
		"name": "time_used",
		"label": "Time Used",
		"help": "e.g. 1h30m"
	},
	{
		"$formkit": "select",
		"name": "recurring",
		"label": "Recurring",
		"options": [
			{"label": "None", "value": "none"},
			{"label": "Daily", "value": "daily"},
			{"label": "Weekly", "value": "weekly"},
			{"label": "Monthly", "value": "monthly"},
			{"label": "Custom (days)", "value": "custom"}
		]
	},
	{
		"$formkit": "number",
		"name": "recurring_days",
		"label": "Recurring Interval (days)",
		"min": "1",
		"help": "Only used when Recurring is set to 'custom'"
	}
]`),
		},
		Viewer: notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "set_status",
				Label:           "Set Status",
				Description:     "Quickly change the task status",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["todo","in_progress","done"]}},"required":["status"]}`),
				Dangerous:       false,
				RefreshStrategy: "reload",
				SuccessMessage:  "Status updated",
			},
			{
				ID:              "toggle_subtask",
				Label:           "Toggle Subtask",
				Description:     "Check or uncheck a subtask from the task view",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"subtask_id":{"type":"integer"},"checked":{"type":"boolean"}},"required":["subtask_id","checked"]}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Subtask updated",
			},
			{
				ID:              "generate_subtasks",
				Label:           "Generate Subtasks",
				Description:     "Ask the LLM to plan additional subtasks from the task description (runs as a background job)",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Dangerous:       false,
				RefreshStrategy: "none", // the note changes only when the job finishes, which notifies separately
				SuccessMessage:  "Subtask generation started",
			},
		},
		HasConfig:  true,
		HasView:    false,
		HasActions: true,
	}
}

func (p *TaskPlugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var cfg TaskConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("task: invalid payload: %w", err)
	}
	if cfg.Status != "" && !validStatuses[cfg.Status] {
		return fmt.Errorf("task: invalid status %q (valid: todo, in_progress, done)", cfg.Status)
	}
	if cfg.Difficulty < 0 || cfg.Difficulty > 10 {
		return fmt.Errorf("task: difficulty must be 0-10, got %d", cfg.Difficulty)
	}
	if cfg.Fun < -5 || cfg.Fun > 5 {
		return fmt.Errorf("task: fun must be -5 to 5, got %d", cfg.Fun)
	}
	if cfg.Priority < 0 || cfg.Priority > 10 {
		return fmt.Errorf("task: priority must be 0-10, got %d", cfg.Priority)
	}
	if cfg.Recurring != "" && !validRecurring[cfg.Recurring] {
		return fmt.Errorf("task: invalid recurring %q", cfg.Recurring)
	}
	if cfg.Recurring == "custom" && cfg.RecurringDays < 1 {
		return fmt.Errorf("task: recurring_days must be >= 1 when recurring is 'custom'")
	}
	return nil
}

func (p *TaskPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	var cfg TaskConfig
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("task: unmarshal config: %w", err)
		}
	}

	// DELETE old config first (upsert pattern).
	if _, err := tx.Exec(`DELETE FROM ct_task_config WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("task: delete old config: %w", err)
	}

	// Determine completed_at: set to now if status is done and no prior value.
	var completedAtSQL string
	var completedAtArg interface{}
	if cfg.Status == "done" && cfg.CompletedAt == "" {
		completedAtSQL = "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')"
	} else if cfg.CompletedAt != "" {
		completedAtSQL = "?"
		completedAtArg = cfg.CompletedAt
	} else {
		completedAtSQL = "''"
	}

	query := fmt.Sprintf(
		`INSERT INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at, pending_does_not_force_daily_inclusion)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, %s, ?)`, completedAtSQL)

	args := []interface{}{
		noteID, cfg.Status, cfg.Difficulty, cfg.Fun, cfg.Priority,
		strings.TrimSpace(cfg.Description), strings.TrimSpace(cfg.DueDate),
		strings.TrimSpace(cfg.TimeEstimation), strings.TrimSpace(cfg.TimeUsed),
		cfg.Recurring, cfg.RecurringDays, cfg.PendingDoesNotForceDailyInclusion,
	}
	if completedAtArg != nil {
		args = append(args, completedAtArg)
	}

	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("task: insert config: %w", err)
	}

	// DELETE old subtasks first (upsert pattern).
	if _, err := tx.Exec(`DELETE FROM ct_task_subtasks WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("task: delete old subtasks: %w", err)
	}
	for _, st := range cfg.SubTasks {
		label := strings.TrimSpace(st.Label)
		if label == "" {
			continue
		}
		checked := 0
		if st.Checked {
			checked = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO ct_task_subtasks (note_id, label, checked, description) VALUES (?, ?, ?, ?)`,
			noteID, label, checked, strings.TrimSpace(st.Description),
		); err != nil {
			return fmt.Errorf("task: insert subtask: %w", err)
		}
	}

	return nil
}

func (p *TaskPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	var cfg TaskConfig
	err := db.QueryRow(
		`SELECT status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, COALESCE(completed_at, ''), COALESCE(pending_does_not_force_daily_inclusion, 0)
		 FROM ct_task_config WHERE note_id = ?`,
		noteID,
	).Scan(&cfg.Status, &cfg.Difficulty, &cfg.Fun, &cfg.Priority,
		&cfg.Description, &cfg.DueDate, &cfg.TimeEstimation, &cfg.TimeUsed,
		&cfg.Recurring, &cfg.RecurringDays, &cfg.CompletedAt, &cfg.PendingDoesNotForceDailyInclusion)
	if err == sql.ErrNoRows {
		defaultCfg := TaskConfig{Status: "todo", SubTasks: []SubTask{}}
		return json.Marshal(defaultCfg)
	} else if err != nil {
		return nil, fmt.Errorf("task: load config: %w", err)
	}

	rows, err := db.Query(`SELECT id, label, checked, description FROM ct_task_subtasks WHERE note_id = ? ORDER BY id`, noteID)
	if err != nil {
		return nil, fmt.Errorf("task: load subtasks: %w", err)
	}
	defer rows.Close()
	subtasks := []SubTask{}
	for rows.Next() {
		var st SubTask
		var checked int
		if err := rows.Scan(&st.ID, &st.Label, &checked, &st.Description); err != nil {
			return nil, fmt.Errorf("task: scan subtask: %w", err)
		}
		st.Checked = checked != 0
		subtasks = append(subtasks, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task: subtask rows: %w", err)
	}
	cfg.SubTasks = subtasks
	return json.Marshal(cfg)
}

// HandleAction implements notetype.ActionHandler.
func (p *TaskPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "set_status":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return setTaskStatus(db, noteID, params)
	case "toggle_subtask":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return toggleSubtask(db, noteID, params)
	case "generate_subtasks":
		if p.enqueue == nil {
			return nil, fmt.Errorf("task: subtask generation is not configured on this server")
		}
		payload, err := json.Marshal(map[string]any{"note_id": noteID})
		if err != nil {
			return nil, fmt.Errorf("task: marshal subtask payload: %w", err)
		}
		runID, err := p.enqueue("_system", "generate_subtasks", payload)
		if err != nil {
			return nil, fmt.Errorf("task: enqueue subtask generation: %w", err)
		}
		return map[string]any{"job_run_id": runID, "status": "planned"}, nil
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}

// toggleSubtask flips the checked state of a single subtask in place,
// without touching the rest of the task config. Used by the view-mode
// checkbox so clicks persist immediately.
func toggleSubtask(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p ToggleSubtaskParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("task: invalid params: %w", err)
	}
	if p.SubtaskID <= 0 {
		return nil, fmt.Errorf("task: subtask_id is required")
	}
	checked := 0
	if p.Checked {
		checked = 1
	}
	res, err := db.Exec(
		`UPDATE ct_task_subtasks SET checked = ? WHERE id = ? AND note_id = ?`,
		checked, p.SubtaskID, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("task: update subtask: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("task: subtask rows affected: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("task: subtask %d not found for note %d", p.SubtaskID, noteID)
	}
	return map[string]any{
		"subtask_id": p.SubtaskID,
		"checked":    p.Checked,
	}, nil
}

// setTaskStatus updates only the status field (and completed_at if done)
// without touching any other task fields.
func setTaskStatus(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p SetStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("task: invalid params: %w", err)
	}
	if !validStatuses[p.Status] {
		return nil, fmt.Errorf("task: invalid status %q (valid: todo, in_progress, done)", p.Status)
	}

	// Read current config to preserve all fields.
	var cfg TaskConfig
	err := db.QueryRow(
		`SELECT status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, COALESCE(completed_at, ''), COALESCE(pending_does_not_force_daily_inclusion, 0)
	 FROM ct_task_config WHERE note_id = ?`, noteID,
	).Scan(&cfg.Status, &cfg.Difficulty, &cfg.Fun, &cfg.Priority,
		&cfg.Description, &cfg.DueDate, &cfg.TimeEstimation, &cfg.TimeUsed,
		&cfg.Recurring, &cfg.RecurringDays, &cfg.CompletedAt, &cfg.PendingDoesNotForceDailyInclusion)
	if err != nil {
		if err == sql.ErrNoRows {
			// No config yet — use defaults.
			cfg = TaskConfig{Status: "todo"}
		} else {
			return nil, fmt.Errorf("task: read config: %w", err)
		}
	}

	cfg.Status = p.Status
	if p.Status == "done" && cfg.CompletedAt == "" {
		cfg.CompletedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	_, err = db.Exec(
		`INSERT OR REPLACE INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at, pending_does_not_force_daily_inclusion)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		noteID, cfg.Status, cfg.Difficulty, cfg.Fun, cfg.Priority,
		cfg.Description, cfg.DueDate, cfg.TimeEstimation, cfg.TimeUsed,
		cfg.Recurring, cfg.RecurringDays, cfg.CompletedAt, cfg.PendingDoesNotForceDailyInclusion,
	)
	if err != nil {
		return nil, fmt.Errorf("task: update status: %w", err)
	}

	cfgJSON, _ := json.Marshal(cfg)
	return map[string]any{
		"status":       p.Status,
		"config":       json.RawMessage(cfgJSON),
		"completed_at": cfg.CompletedAt,
	}, nil
}
