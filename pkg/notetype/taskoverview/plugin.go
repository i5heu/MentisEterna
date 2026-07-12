package taskoverview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
			generation_id    TEXT    NOT NULL DEFAULT '',
			task_note_id     INTEGER NOT NULL,
			assigned_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			position         INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (overview_note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY (task_note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS ct_taskoverview_config (
			note_id                 INTEGER PRIMARY KEY,
			daily_task_count        INTEGER NOT NULL DEFAULT 3,
			urgent_due_days         INTEGER NOT NULL DEFAULT 3,
			priority_weight         REAL    NOT NULL DEFAULT 4,
			due_urgency_weight      REAL    NOT NULL DEFAULT 6,
			difficulty_weight       REAL    NOT NULL DEFAULT -1,
			fun_weight              REAL    NOT NULL DEFAULT 0.75,
			time_estimation_weight  REAL    NOT NULL DEFAULT -0.5,
			fun_time_weight         REAL    NOT NULL DEFAULT 0.1,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
	`)
	if err != nil {
		return err
	}

	// Best-effort migrations for older databases.
	db.Exec(`ALTER TABLE ct_taskoverview_daily ADD COLUMN generation_id TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE ct_taskoverview_daily ADD COLUMN position INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN daily_task_count INTEGER NOT NULL DEFAULT 3`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN urgent_due_days INTEGER NOT NULL DEFAULT 3`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN priority_weight REAL NOT NULL DEFAULT 4`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN due_urgency_weight REAL NOT NULL DEFAULT 6`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN difficulty_weight REAL NOT NULL DEFAULT -1`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN fun_weight REAL NOT NULL DEFAULT 0.75`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN time_estimation_weight REAL NOT NULL DEFAULT -0.5`)
	db.Exec(`ALTER TABLE ct_taskoverview_config ADD COLUMN fun_time_weight REAL NOT NULL DEFAULT 0.1`)

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_ct_taskoverview_daily_ov ON ct_taskoverview_daily(overview_note_id, assigned_at)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_ct_taskoverview_daily_generation ON ct_taskoverview_daily(overview_note_id, generation_id)`); err != nil {
		return err
	}
	return nil
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
	defaultConfig, _ := json.Marshal(defaultOverviewConfig())
	return notetype.Manifest{
		ID:            pluginID,
		Label:         "Task Overview",
		Description:   "Dashboard to score tasks and generate daily focus tasks from due dates, priority, difficulty, fun, and time estimates",
		Category:      "Productivity",
		SortOrder:     410,
		DefaultConfig: defaultConfig,
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "daily_tasks",
				Label:           "Generate Daily Tasks",
				Description:     "Generate scored daily tasks using due dates, priorities, and task effort",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Daily tasks generated",
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
		HasConfig:  true,
		HasView:    true,
		HasActions: true,
	}
}

func (p *TaskOverviewPlugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}

	cfg := defaultOverviewConfig()
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("task_overview: invalid payload: %w", err)
	}
	if cfg.DailyTaskCount < 1 || cfg.DailyTaskCount > 50 {
		return fmt.Errorf("task_overview: daily_task_count must be between 1 and 50")
	}
	if cfg.UrgentDueDays < 0 || cfg.UrgentDueDays > 30 {
		return fmt.Errorf("task_overview: urgent_due_days must be between 0 and 30")
	}
	for name, value := range map[string]float64{
		"priority_weight":        cfg.PriorityWeight,
		"due_urgency_weight":     cfg.DueUrgencyWeight,
		"difficulty_weight":      cfg.DifficultyWeight,
		"fun_weight":             cfg.FunWeight,
		"time_estimation_weight": cfg.TimeEstimationWeight,
		"fun_time_weight":        cfg.FunTimeWeight,
	} {
		if value < -100 || value > 100 {
			return fmt.Errorf("task_overview: %s must be between -100 and 100", name)
		}
	}
	return nil
}

func (p *TaskOverviewPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	cfg := defaultOverviewConfig()
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return fmt.Errorf("task_overview: unmarshal config: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM ct_taskoverview_config WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("task_overview: delete old config: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO ct_taskoverview_config (
			note_id, daily_task_count, urgent_due_days, priority_weight, due_urgency_weight,
			difficulty_weight, fun_weight, time_estimation_weight, fun_time_weight
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		noteID,
		cfg.DailyTaskCount,
		cfg.UrgentDueDays,
		cfg.PriorityWeight,
		cfg.DueUrgencyWeight,
		cfg.DifficultyWeight,
		cfg.FunWeight,
		cfg.TimeEstimationWeight,
		cfg.FunTimeWeight,
	); err != nil {
		return fmt.Errorf("task_overview: insert config: %w", err)
	}
	return nil
}

func (p *TaskOverviewPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	cfg, err := loadOverviewConfig(db, noteID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}

func loadOverviewConfig(db *sql.DB, noteID int64) (TaskOverviewConfig, error) {
	cfg := defaultOverviewConfig()
	err := db.QueryRow(
		`SELECT daily_task_count, urgent_due_days, priority_weight, due_urgency_weight,
		        difficulty_weight, fun_weight, time_estimation_weight, fun_time_weight
		 FROM ct_taskoverview_config WHERE note_id = ?`,
		noteID,
	).Scan(
		&cfg.DailyTaskCount,
		&cfg.UrgentDueDays,
		&cfg.PriorityWeight,
		&cfg.DueUrgencyWeight,
		&cfg.DifficultyWeight,
		&cfg.FunWeight,
		&cfg.TimeEstimationWeight,
		&cfg.FunTimeWeight,
	)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return TaskOverviewConfig{}, fmt.Errorf("task_overview: load config: %w", err)
	}
	return normalizeOverviewConfig(cfg), nil
}

func (p *TaskOverviewPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	cfg, err := loadOverviewConfig(db, noteID)
	if err != nil {
		return nil, err
	}

	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}
	scoredOpenTasks := scoreOpenTasks(tasks, cfg, nowUTC())
	stats := computeStats(tasks)

	// Load persisted daily tasks, regenerating if none exist yet.
	dailyTasks, err := loadDailyTasks(db, noteID)
	if err != nil {
		return nil, err
	}
	if len(dailyTasks) == 0 {
		dailyTasks = selectDailyTasks(scoredOpenTasks, cfg, 0)
		_ = storeDailyTasks(db, noteID, dailyTasks) // best-effort
	} else {
		dailyTasks = annotateTasksWithScores(dailyTasks, scoredOpenTasks)
	}

	dailyHistory, err := loadDailyHistory(db, noteID)
	if err != nil {
		return nil, err
	}

	return &OverviewData{
		Tasks:           tasks,
		ScoredOpenTasks: scoredOpenTasks,
		DailyTasks:      dailyTasks,
		DailyHistory:    dailyHistory,
		Stats:           stats,
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

var nowUTC = func() time.Time {
	return time.Now().UTC()
}
