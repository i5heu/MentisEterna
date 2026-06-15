package taskoverview

import (
	"database/sql"
	"fmt"
	"time"
)

// --- Task loading ---

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

// --- Statistics ---

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
func sumTimeUsed(tasks []TaskSummary) string {
	totalMinutes := 0
	for _, t := range tasks {
		if t.TimeUsed == "" {
			continue
		}
		totalMinutes += parseTimeToMinutes(t.TimeUsed)
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

// --- Time parsing ---

// parseTimeToMinutes parses strings like "2h", "30m", "1h30m", "1d" into total minutes.
func parseTimeToMinutes(s string) int {
	s = trimSpace(s)
	if s == "" {
		return 0
	}
	total := 0

	// Days: "1d" = 8 * 60 = 480 minutes
	for i := 0; i < len(s); i++ {
		if s[i] == 'd' {
			total += parseLeadingInt(s[:i]) * 8 * 60
			s = s[i+1:]
			break
		}
	}

	// Hours
	for i := 0; i < len(s); i++ {
		if s[i] == 'h' {
			total += parseLeadingInt(s[:i]) * 60
			s = s[i+1:]
			break
		}
	}

	// Minutes
	for i := 0; i < len(s); i++ {
		if s[i] == 'm' {
			total += parseLeadingInt(s[:i])
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
