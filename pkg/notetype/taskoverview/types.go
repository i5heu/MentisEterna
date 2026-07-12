package taskoverview

// TaskOverviewConfig controls score-based daily task generation.
type TaskOverviewConfig struct {
	DailyTaskCount       int     `json:"daily_task_count"`
	UrgentDueDays        int     `json:"urgent_due_days"`
	PriorityWeight       float64 `json:"priority_weight"`
	DueUrgencyWeight     float64 `json:"due_urgency_weight"`
	DifficultyWeight     float64 `json:"difficulty_weight"`
	FunWeight            float64 `json:"fun_weight"`
	TimeEstimationWeight float64 `json:"time_estimation_weight"`
	FunTimeWeight        float64 `json:"fun_time_weight"`
}

// TaskScoreBreakdown exposes how a generation score was computed.
type TaskScoreBreakdown struct {
	DueUrgency     float64 `json:"due_urgency"`
	Priority       float64 `json:"priority"`
	Difficulty     float64 `json:"difficulty"`
	Fun            float64 `json:"fun"`
	TimeEstimation float64 `json:"time_estimation"`
	FunTime        float64 `json:"fun_time"`
	EstimatedHours float64 `json:"estimated_hours"`
	Total          float64 `json:"total"`
}

// TaskSummary is a lightweight task representation for the dashboard.
type TaskSummary struct {
	NoteID                            int64              `json:"note_id"`
	Title                             string             `json:"title"`
	Status                            string             `json:"status"`
	Priority                          int                `json:"priority"`
	Difficulty                        int                `json:"difficulty"`
	Fun                               int                `json:"fun"`
	DueDate                           string             `json:"due_date"`
	TimeEstimation                    string             `json:"time_estimation"`
	TimeUsed                          string             `json:"time_used"`
	Recurring                         string             `json:"recurring"`
	CompletedAt                       string             `json:"completed_at"`
	CreatedAt                         string             `json:"created_at"`
	UpdatedAt                         string             `json:"updated_at"`
	Body                              string             `json:"body"`
	PendingDoesNotForceDailyInclusion bool               `json:"pending_does_not_force_daily_inclusion"`
	DueInDays                         *int               `json:"due_in_days,omitempty"`
	GenerationScore                   float64            `json:"generation_score"`
	GenerationForcedReasons           []string           `json:"generation_forced_reasons,omitempty"`
	GenerationScoreBreakdown          TaskScoreBreakdown `json:"generation_score_breakdown"`
}

// DailyHistoryEntry groups the tasks that were assigned at a particular generation.
type DailyHistoryEntry struct {
	GeneratedAt string        `json:"generated_at"`
	Tasks       []TaskSummary `json:"tasks"`
}

// OverviewData is the view returned to the frontend.
type OverviewData struct {
	Tasks           []TaskSummary       `json:"tasks"`
	ScoredOpenTasks []TaskSummary       `json:"scored_open_tasks"`
	DailyTasks      []TaskSummary       `json:"daily_tasks"`
	DailyHistory    []DailyHistoryEntry `json:"daily_history"`
	Stats           TaskStats           `json:"stats"`
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
	Count int `json:"count"` // optional override for the base number of daily tasks
}

// QuickSetStatusParams is the JSON body for the quick_set_status action.
type QuickSetStatusParams struct {
	TaskNoteID int64  `json:"task_note_id"`
	Status     string `json:"status"`
}
