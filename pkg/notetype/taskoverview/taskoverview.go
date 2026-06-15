// Package taskoverview implements a "task_overview" note type that provides
// a dashboard to view all tasks, filter by status, due date, etc., and
// get 3 random daily tasks.
package taskoverview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
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

// TaskSummary is a lightweight task representation for the dashboard.
type TaskSummary struct {
	NoteID         int64  `json:"note_id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Priority       int    `json:"priority"`
	Difficulty     int    `json:"difficulty"`
	Fun            int    `json:"fun"`
	DueDate        string `json:"due_date"`
	TimeEstimation string `json:"time_estimation"`
	TimeUsed       string `json:"time_used"`
	Recurring      string `json:"recurring"`
	CompletedAt    string `json:"completed_at"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	Body           string `json:"body"`
}

// DailyHistoryEntry groups the tasks that were assigned at a particular generation.
type DailyHistoryEntry struct {
	GeneratedAt string        `json:"generated_at"`
	Tasks       []TaskSummary `json:"tasks"`
}

// OverviewData is the view returned to the frontend.
type OverviewData struct {
	Tasks        []TaskSummary       `json:"tasks"`
	DailyTasks   []TaskSummary       `json:"daily_tasks"`
	DailyHistory []DailyHistoryEntry `json:"daily_history"`
	Stats        TaskStats           `json:"stats"`
}

// TaskStats holds aggregate statistics.
type TaskStats struct {
	Total         int     `json:"total"`
	Todo          int     `json:"todo"`
	InProgress    int     `json:"in_progress"`
	Done          int     `json:"done"`
	AvgPriority   float64 `json:"avg_priority"`
	AvgDifficulty float64 `json:"avg_difficulty"`
	AvgFun        float64 `json:"avg_fun"`
	Overdue       int     `json:"overdue"`
	TotalTimeUsed string  `json:"total_time_used"` // human-readable sum
}

// DailyTaskParams is the JSON body for the daily_tasks action.
type DailyTaskParams struct {
	Count int `json:"count"` // number of random tasks to return (default 3)
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

// --- Daily task persistence ---

// regenerateAllDailyTasks is the cron job that repicks daily tasks
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
	for _, noteID := range noteIDs {
		picked := pickRandomTasks(tasks, 3)
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
		       COALESCE(tc.completed_at, '')
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
		ORDER BY d.assigned_at ASC
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
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt); err != nil {
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
	for _, t := range tasks {
		if _, err := db.Exec(
			`INSERT INTO ct_taskoverview_daily (overview_note_id, task_note_id, assigned_at) VALUES (?, ?, ?)`,
			overviewNoteID, t.NoteID, now,
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
		       COALESCE(tc.completed_at, '')
		FROM ct_taskoverview_daily d
		JOIN notes n ON n.id = d.task_note_id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		LEFT JOIN ct_task_config tc ON tc.note_id = n.id
		WHERE d.overview_note_id = ? AND d.assigned_at != ?
		ORDER BY d.assigned_at DESC, n.title ASC
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
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt); err != nil {
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

// --- Helpers ---

func loadAllTasks(db *sql.DB) ([]TaskSummary, error) {
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
		       COALESCE(tc.completed_at, '')
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		LEFT JOIN ct_task_config tc ON tc.note_id = n.id
		WHERE n.type = 'task'
		ORDER BY tc.priority DESC, tc.due_date ASC, n.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("task_overview: load tasks: %w", err)
	}
	defer rows.Close()

	var tasks []TaskSummary
	for rows.Next() {
		var t TaskSummary
		if err := rows.Scan(&t.NoteID, &t.Title, &t.CreatedAt, &t.Body, &t.UpdatedAt,
			&t.Status, &t.Priority, &t.Difficulty, &t.Fun,
			&t.DueDate, &t.TimeEstimation, &t.TimeUsed, &t.Recurring, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("task_overview: scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []TaskSummary{}
	}
	return tasks, nil
}

func computeStats(tasks []TaskSummary) TaskStats {
	s := TaskStats{Total: len(tasks)}
	var totalPriority, totalDiff, totalFun int
	now := time.Now().Format("2006-01-02")

	for _, t := range tasks {
		switch t.Status {
		case "todo":
			s.Todo++
		case "in_progress":
			s.InProgress++
		case "done":
			s.Done++
		}
		totalPriority += t.Priority
		totalDiff += t.Difficulty
		totalFun += t.Fun

		if t.DueDate != "" && t.Status != "done" && t.DueDate < now {
			s.Overdue++
		}
	}

	if s.Total > 0 {
		s.AvgPriority = float64(totalPriority) / float64(s.Total)
		s.AvgDifficulty = float64(totalDiff) / float64(s.Total)
		s.AvgFun = float64(totalFun) / float64(s.Total)
	}

	s.TotalTimeUsed = sumTimeUsed(tasks)
	return s
}

// sumTimeUsed sums all time_used values and returns a human-readable string.
// This is a rough approximation — it just concatenates for display.
func sumTimeUsed(tasks []TaskSummary) string {
	totalMinutes := 0
	for _, t := range tasks {
		if t.TimeUsed == "" {
			continue
		}
		minutes := parseTimeToMinutes(t.TimeUsed)
		totalMinutes += minutes
	}
	if totalMinutes == 0 {
		return "0h"
	}
	hours := totalMinutes / 60
	mins := totalMinutes % 60
	if hours > 0 && mins > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", mins)
}

// parseTimeToMinutes parses strings like "2h", "30m", "1h30m", "1d" into total minutes.
func parseTimeToMinutes(s string) int {
	s = trimSpace(s)
	if s == "" {
		return 0
	}
	total := 0

	// Handle days: "1d" = 8 * 60 = 480 minutes
	for i := 0; i < len(s); i++ {
		if s[i] == 'd' {
			num := parseLeadingInt(s[:i])
			total += num * 8 * 60
			s = s[i+1:]
			break
		}
	}

	// Handle hours
	for i := 0; i < len(s); i++ {
		if s[i] == 'h' {
			num := parseLeadingInt(s[:i])
			total += num * 60
			s = s[i+1:]
			break
		}
	}

	// Handle minutes
	for i := 0; i < len(s); i++ {
		if s[i] == 'm' {
			num := parseLeadingInt(s[:i])
			total += num
			break
		}
	}

	return total
}

func parseLeadingInt(s string) int {
	s = trimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

func pickRandomTasks(tasks []TaskSummary, count int) []TaskSummary {
	var inProgress, candidates []TaskSummary
	for _, t := range tasks {
		switch t.Status {
		case "in_progress":
			inProgress = append(inProgress, t)
		case "done":
			// skip
		default:
			candidates = append(candidates, t)
		}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(inProgress), func(i, j int) { inProgress[i], inProgress[j] = inProgress[j], inProgress[i] })
	rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	result := make([]TaskSummary, 0, count)
	result = append(result, inProgress...)
	result = append(result, candidates...)
	if len(result) > count {
		result = result[:count]
	}
	return result
}

func handleDailyTasks(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p DailyTaskParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("task_overview: invalid params: %w", err)
		}
	}
	if p.Count <= 0 {
		p.Count = 3
	}

	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}

	picked := pickRandomTasks(tasks, p.Count)

	// Persist so BuildView returns the same set.
	if err := storeDailyTasks(db, noteID, picked); err != nil {
		// Non-fatal: still return the picked tasks even if persist fails.
		fmt.Printf("task_overview: failed to store daily tasks for note %d: %v\n", noteID, err)
	}

	return map[string]any{
		"daily_tasks": picked,
	}, nil
}

// QuickSetStatusParams is the JSON body for the quick_set_status action.
type QuickSetStatusParams struct {
	TaskNoteID int64  `json:"task_note_id"`
	Status     string `json:"status"`
}

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
	err := db.QueryRow(
		`SELECT status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, COALESCE(completed_at, '')
		 FROM ct_task_config WHERE note_id = ?`, p.TaskNoteID,
	).Scan(&status, &difficulty, &fun, &priority, &description, &dueDate, &timeEst, &timeUsed, &recurring, &recurringDays, &completedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			// No config yet — create one.
			now := ""
			if p.Status == "done" {
				now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
			}
			_, err = db.Exec(
				`INSERT INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at)
				 VALUES (?, ?, 0, 0, 0, '', '', '', '', 'none', 0, ?)`,
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
			`INSERT OR REPLACE INTO ct_task_config (note_id, status, difficulty, fun, priority, description, due_date, time_estimation, time_used, recurring, recurring_days, completed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.TaskNoteID, p.Status, difficulty, fun, priority, description, dueDate, timeEst, timeUsed, recurring, recurringDays, now,
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
