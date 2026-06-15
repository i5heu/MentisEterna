package taskoverview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "task_overview"

func init() {
	notetype.Register(&TaskOverviewPlugin{})
}

type TaskOverviewPlugin struct{}

func (p *TaskOverviewPlugin) ID() string { return pluginID }

func (p *TaskOverviewPlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_taskoverview_daily (
			overview_note_id INTEGER NOT NULL,
			task_note_id     INTEGER NOT NULL,
			assigned_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			FOREIGN KEY (overview_note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (task_note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_taskoverview_daily_ov
			ON ct_taskoverview_daily(overview_note_id, assigned_at);
	`)
	return err
}

func (p *TaskOverviewPlugin) CronJobs() []notetype.CronJob {
	return []notetype.CronJob{
		{
			Name:     "regenerate_daily_tasks",
			Schedule: "@daily",
			Task:     regenerateAllDailyTasks,
		},
	}
}

func (p *TaskOverviewPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "task_overview",
		Label:         "Task Overview",
		Description:   "Dashboard to view and filter all tasks, with daily random task selection",
		Category:      "Productivity",
		SortOrder:     410,
		DefaultConfig: json.RawMessage(`{}`),
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "daily_tasks",
				Label:           "Get Daily Tasks",
				Description:     "Get 3 tasks for today's focus (in-progress tasks prioritized)",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Daily tasks selected",
			},
			{
				ID:              "quick_set_status",
				Label:           "Quick Set Status",
				Description:     "Quickly change a task's status from the overview",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"task_note_id":{"type":"integer"},"status":{"type":"string","enum":["todo","in_progress","done"]}},"required":["task_note_id","status"]}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Task status updated",
			},
		},
		HasConfig:  false,
		HasView:    true,
		HasActions: true,
	}
}

func (p *TaskOverviewPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}

	stats := computeStats(tasks)

	// Load persisted daily tasks, regenerating if none exist yet.
	dailyTasks, err := loadDailyTasks(db, noteID)
	if err != nil {
		return nil, err
	}
	if len(dailyTasks) == 0 {
		dailyTasks = pickRandomTasks(tasks, 3)
		_ = storeDailyTasks(db, noteID, dailyTasks) // best-effort
	}

	dailyHistory, err := loadDailyHistory(db, noteID)
	if err != nil {
		return nil, err
	}

	return &OverviewData{
		Tasks:        tasks,
		DailyTasks:   dailyTasks,
		DailyHistory: dailyHistory,
		Stats:        stats,
	}, nil
}

func (p *TaskOverviewPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "daily_tasks":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return handleDailyTasks(db, noteID, params)
	case "quick_set_status":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return handleQuickSetStatus(db, params)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}
