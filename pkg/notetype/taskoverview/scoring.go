package taskoverview

import (
	"math"
	"sort"
	"time"
)

func defaultOverviewConfig() TaskOverviewConfig {
	return TaskOverviewConfig{
		DailyTaskCount:       3,
		UrgentDueDays:        3,
		PriorityWeight:       4,
		DueUrgencyWeight:     6,
		DifficultyWeight:     -1,
		FunWeight:            0.75,
		TimeEstimationWeight: -0.5,
		FunTimeWeight:        0.1,
	}
}

func scoreOpenTasks(tasks []TaskSummary, cfg TaskOverviewConfig, now time.Time) []TaskSummary {
	cfg = normalizeOverviewConfig(cfg)
	startOfToday := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)

	scored := make([]TaskSummary, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == "done" {
			continue
		}

		t := task
		minutes := parseTimeToMinutes(t.TimeEstimation)
		hours := float64(minutes) / 60.0
		dueInDays := computeDueInDays(t.DueDate, startOfToday)
		forced := forcedGenerationReasons(t, cfg, dueInDays)

		dueUrgencyUnits := computeDueUrgencyUnits(cfg, dueInDays)
		breakdown := TaskScoreBreakdown{
			DueUrgency:     dueUrgencyUnits * cfg.DueUrgencyWeight,
			Priority:       float64(t.Priority) * cfg.PriorityWeight,
			Difficulty:     float64(t.Difficulty) * cfg.DifficultyWeight,
			Fun:            float64(t.Fun) * cfg.FunWeight,
			TimeEstimation: hours * cfg.TimeEstimationWeight,
			FunTime:        hours * float64(t.Fun) * cfg.FunTimeWeight,
			EstimatedHours: hours,
		}
		breakdown.Total = breakdown.DueUrgency + breakdown.Priority + breakdown.Difficulty + breakdown.Fun + breakdown.TimeEstimation + breakdown.FunTime

		t.DueInDays = dueInDays
		t.GenerationForcedReasons = forced
		t.GenerationScoreBreakdown = breakdown
		t.GenerationScore = breakdown.Total
		scored = append(scored, t)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		left, right := scored[i], scored[j]
		if leftRank, rightRank := generationForceRank(left), generationForceRank(right); leftRank != rightRank {
			return leftRank > rightRank
		}
		if !nearlyEqual(left.GenerationScore, right.GenerationScore) {
			return left.GenerationScore > right.GenerationScore
		}
		if leftDue, rightDue := sortableDueDays(left.DueInDays), sortableDueDays(right.DueInDays); leftDue != rightDue {
			return leftDue < rightDue
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return left.NoteID < right.NoteID
	})

	return scored
}

func selectDailyTasks(scoredOpenTasks []TaskSummary, cfg TaskOverviewConfig, countOverride int) []TaskSummary {
	cfg = normalizeOverviewConfig(cfg)
	target := cfg.DailyTaskCount
	if countOverride > 0 {
		target = countOverride
	}
	if target < 1 {
		target = 1
	}

	forced := make([]TaskSummary, 0, len(scoredOpenTasks))
	optional := make([]TaskSummary, 0, len(scoredOpenTasks))
	for _, task := range scoredOpenTasks {
		if len(task.GenerationForcedReasons) > 0 {
			forced = append(forced, task)
		} else {
			optional = append(optional, task)
		}
	}

	resultSize := target
	if len(forced) > resultSize {
		resultSize = len(forced)
	}

	result := make([]TaskSummary, 0, resultSize)
	result = append(result, forced...)
	remaining := resultSize - len(result)
	if remaining > len(optional) {
		remaining = len(optional)
	}
	if remaining > 0 {
		result = append(result, optional[:remaining]...)
	}
	return result
}

func annotateTasksWithScores(tasks []TaskSummary, scoredOpenTasks []TaskSummary) []TaskSummary {
	if len(tasks) == 0 {
		return []TaskSummary{}
	}
	scoreByID := make(map[int64]TaskSummary, len(scoredOpenTasks))
	for _, task := range scoredOpenTasks {
		scoreByID[task.NoteID] = task
	}
	annotated := make([]TaskSummary, 0, len(tasks))
	for _, task := range tasks {
		if scored, ok := scoreByID[task.NoteID]; ok {
			task.DueInDays = scored.DueInDays
			task.GenerationScore = scored.GenerationScore
			task.GenerationForcedReasons = append([]string(nil), scored.GenerationForcedReasons...)
			task.GenerationScoreBreakdown = scored.GenerationScoreBreakdown
		}
		annotated = append(annotated, task)
	}
	return annotated
}

func normalizeOverviewConfig(cfg TaskOverviewConfig) TaskOverviewConfig {
	defaults := defaultOverviewConfig()
	if cfg.DailyTaskCount <= 0 {
		cfg.DailyTaskCount = defaults.DailyTaskCount
	}
	if cfg.UrgentDueDays < 0 {
		cfg.UrgentDueDays = defaults.UrgentDueDays
	}
	return cfg
}

func forcedGenerationReasons(task TaskSummary, cfg TaskOverviewConfig, dueInDays *int) []string {
	reasons := make([]string, 0, 2)
	if dueInDays != nil && *dueInDays <= cfg.UrgentDueDays {
		reasons = append(reasons, "due_soon")
	}
	if task.Status == "in_progress" && !task.PendingDoesNotForceDailyInclusion {
		reasons = append(reasons, "pending")
	}
	return reasons
}

func computeDueInDays(dueDate string, startOfToday time.Time) *int {
	if dueDate == "" {
		return nil
	}
	due, err := time.Parse("2006-01-02", dueDate)
	if err != nil {
		return nil
	}
	days := int(due.Sub(startOfToday).Hours() / 24)
	return &days
}

func computeDueUrgencyUnits(cfg TaskOverviewConfig, dueInDays *int) float64 {
	if dueInDays == nil {
		return 0
	}
	if *dueInDays > cfg.UrgentDueDays {
		return 0
	}
	return float64(cfg.UrgentDueDays + 1 - *dueInDays)
}

func generationForceRank(task TaskSummary) int {
	hasDueSoon := false
	hasPending := false
	for _, reason := range task.GenerationForcedReasons {
		switch reason {
		case "due_soon":
			hasDueSoon = true
		case "pending":
			hasPending = true
		}
	}
	switch {
	case hasDueSoon:
		return 2
	case hasPending:
		return 1
	default:
		return 0
	}
}

func sortableDueDays(dueInDays *int) int {
	if dueInDays == nil {
		return math.MaxInt32
	}
	return *dueInDays
}

func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.000001
}
