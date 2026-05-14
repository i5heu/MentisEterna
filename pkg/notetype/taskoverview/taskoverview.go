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
	// No dedicated tables needed — this is a read-only dashboard.
	return nil
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

// OverviewData is the view returned to the frontend.
type OverviewData struct {
	Tasks      []TaskSummary `json:"tasks"`
	DailyTasks []TaskSummary `json:"daily_tasks"`
	Stats      TaskStats     `json:"stats"`
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
	return nil
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
				Description:     "Get 3 random non-done tasks for today's focus",
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
	dailyTasks := pickRandomTasks(tasks, 3)

	return &OverviewData{
		Tasks:      tasks,
		DailyTasks: dailyTasks,
		Stats:      stats,
	}, nil
}

func (p *TaskOverviewPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "daily_tasks":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return handleDailyTasks(db, params)
	case "quick_set_status":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return handleQuickSetStatus(db, params)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
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
	// Filter to non-done tasks.
	var candidates []TaskSummary
	for _, t := range tasks {
		if t.Status != "done" {
			candidates = append(candidates, t)
		}
	}

	if len(candidates) == 0 {
		return []TaskSummary{}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	if count > len(candidates) {
		count = len(candidates)
	}
	return candidates[:count]
}

func handleDailyTasks(db *sql.DB, params json.RawMessage) (any, error) {
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
