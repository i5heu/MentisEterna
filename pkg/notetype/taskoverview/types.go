package taskoverview

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

// QuickSetStatusParams is the JSON body for the quick_set_status action.
type QuickSetStatusParams struct {
	TaskNoteID int64  `json:"task_note_id"`
	Status     string `json:"status"`
}
