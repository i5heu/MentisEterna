package taskoverview

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"
)

func TestNormalizeOverviewConfig_Table(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		input TaskOverviewConfig
		want  TaskOverviewConfig
	}{
		// Verifies invalid counters fall back to defaults without clobbering explicitly supplied weights; failure would make saved user tuning unstable after validation.
		"defaults invalid counters but preserves weights": {
			input: TaskOverviewConfig{
				DailyTaskCount:       0,
				UrgentDueDays:        -3,
				PriorityWeight:       9,
				DueUrgencyWeight:     8,
				DifficultyWeight:     -7,
				FunWeight:            6,
				TimeEstimationWeight: -5,
				FunTimeWeight:        4,
			},
			want: TaskOverviewConfig{
				DailyTaskCount:       defaultOverviewConfig().DailyTaskCount,
				UrgentDueDays:        defaultOverviewConfig().UrgentDueDays,
				PriorityWeight:       9,
				DueUrgencyWeight:     8,
				DifficultyWeight:     -7,
				FunWeight:            6,
				TimeEstimationWeight: -5,
				FunTimeWeight:        4,
			},
		},
		// Verifies an explicit zero-day urgent window is preserved; failure would make it impossible to configure "only overdue/today" style behavior.
		"preserves explicit zero urgent window": {
			input: TaskOverviewConfig{
				DailyTaskCount: 5,
				UrgentDueDays:  0,
			},
			want: TaskOverviewConfig{
				DailyTaskCount: 5,
				UrgentDueDays:  0,
			},
		},
		// Verifies already normalized config stays byte-for-byte stable; failure would create non-idempotent saves and noisy diffs.
		"leaves already normalized config unchanged": {
			input: TaskOverviewConfig{
				DailyTaskCount:       7,
				UrgentDueDays:        2,
				PriorityWeight:       1.5,
				DueUrgencyWeight:     2.5,
				DifficultyWeight:     -3.5,
				FunWeight:            4.5,
				TimeEstimationWeight: -5.5,
				FunTimeWeight:        6.5,
			},
			want: TaskOverviewConfig{
				DailyTaskCount:       7,
				UrgentDueDays:        2,
				PriorityWeight:       1.5,
				DueUrgencyWeight:     2.5,
				DifficultyWeight:     -3.5,
				FunWeight:            4.5,
				TimeEstimationWeight: -5.5,
				FunTimeWeight:        6.5,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := normalizeOverviewConfig(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("normalizeOverviewConfig mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScoreOpenTasks_Table(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		cfg        TaskOverviewConfig
		tasks      []TaskSummary
		wantIDs    []int64
		wantForced map[int64][]string
	}{
		// Verifies done tasks are removed and due-soon tasks outrank pending carry-overs and optional work; failure would surface completed work or bury urgent deadlines.
		"filters done tasks and ranks due soon ahead of pending and optional": {
			cfg: defaultOverviewConfig(),
			tasks: []TaskSummary{
				{NoteID: 99, Title: "done", Status: "done", Priority: 10},
				{NoteID: 2, Title: "pending carry over", Status: "in_progress", Priority: 10},
				{NoteID: 3, Title: "due today", Status: "todo", Priority: 0, DueDate: "2026-07-12"},
				{NoteID: 4, Title: "optional", Status: "todo", Priority: 10},
			},
			wantIDs: []int64{3, 2, 4},
			wantForced: map[int64][]string{
				2: {"pending"},
				3: {"due_soon"},
				4: nil,
			},
		},
		// Verifies optional tasks still sort by score when no force rules apply; failure would make user-defined weights meaningless.
		"orders optional tasks by descending score": {
			cfg: TaskOverviewConfig{
				DailyTaskCount:       3,
				UrgentDueDays:        0,
				PriorityWeight:       2,
				DueUrgencyWeight:     0,
				DifficultyWeight:     -1,
				FunWeight:            1,
				TimeEstimationWeight: 0,
				FunTimeWeight:        0,
			},
			tasks: []TaskSummary{
				{NoteID: 1, Title: "priority wins", Status: "todo", Priority: 3},
				{NoteID: 2, Title: "difficulty loses", Status: "todo", Difficulty: 10},
				{NoteID: 3, Title: "fun helps", Status: "todo", Fun: 4},
			},
			wantIDs: []int64{1, 3, 2},
			wantForced: map[int64][]string{
				1: nil,
				2: nil,
				3: nil,
			},
		},
		// Verifies equal-score tasks break ties by earlier due date, then higher priority, then lower note ID; failure would make ordering flicker unpredictably between refreshes.
		"breaks equal scores by due date then priority then note id": {
			cfg: TaskOverviewConfig{
				DailyTaskCount:       5,
				UrgentDueDays:        0,
				PriorityWeight:       0,
				DueUrgencyWeight:     0,
				DifficultyWeight:     0,
				FunWeight:            0,
				TimeEstimationWeight: 0,
				FunTimeWeight:        0,
			},
			tasks: []TaskSummary{
				{NoteID: 5, Title: "later due", Status: "todo", DueDate: "2026-07-14", Priority: 1},
				{NoteID: 4, Title: "sooner due", Status: "todo", DueDate: "2026-07-13", Priority: 0},
				{NoteID: 3, Title: "higher priority", Status: "todo", Priority: 9},
				{NoteID: 2, Title: "same priority higher id", Status: "todo", Priority: 3},
				{NoteID: 1, Title: "same priority lower id", Status: "todo", Priority: 3},
			},
			wantIDs: []int64{4, 5, 3, 1, 2},
			wantForced: map[int64][]string{
				1: nil,
				2: nil,
				3: nil,
				4: nil,
				5: nil,
			},
		},
		// Verifies overdue work is treated as urgent and receives both the force flag and higher urgency than today; failure would hide already-late tasks behind less critical work.
		"overdue tasks are still due soon and outrank today": {
			cfg: defaultOverviewConfig(),
			tasks: []TaskSummary{
				{NoteID: 10, Title: "due yesterday", Status: "todo", DueDate: "2026-07-11"},
				{NoteID: 11, Title: "due today", Status: "todo", DueDate: "2026-07-12"},
			},
			wantIDs: []int64{10, 11},
			wantForced: map[int64][]string{
				10: {"due_soon"},
				11: {"due_soon"},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := scoreOpenTasks(tc.tasks, tc.cfg, baseNow)
			if diff := cmp.Diff(tc.wantIDs, noteIDs(got)); diff != "" {
				t.Fatalf("scoreOpenTasks order mismatch (-want +got):\n%s", diff)
			}
			for _, task := range got {
				if diff := cmp.Diff(tc.wantForced[task.NoteID], task.GenerationForcedReasons, cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("forced reasons mismatch for note %d (-want +got):\n%s", task.NoteID, diff)
				}
				wantTotal := task.GenerationScoreBreakdown.DueUrgency +
					task.GenerationScoreBreakdown.Priority +
					task.GenerationScoreBreakdown.Difficulty +
					task.GenerationScoreBreakdown.Fun +
					task.GenerationScoreBreakdown.TimeEstimation +
					task.GenerationScoreBreakdown.FunTime
				if !nearlyEqual(task.GenerationScore, wantTotal) {
					t.Fatalf("generation score total mismatch for note %d: got %v want %v", task.NoteID, task.GenerationScore, wantTotal)
				}
			}
		})
	}
}

func TestSelectDailyTasks_Table(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		cfg           TaskOverviewConfig
		countOverride int
		tasks         []TaskSummary
		wantIDs       []int64
	}{
		// Verifies the selection expands beyond the base count when urgent work exceeds capacity; failure would drop exactly the tasks the feature is meant to protect.
		"forced tasks expand result beyond base count": {
			cfg: defaultOverviewConfig(),
			tasks: []TaskSummary{
				{NoteID: 1, Title: "pending", Status: "in_progress"},
				{NoteID: 2, Title: "due today", Status: "todo", DueDate: "2026-07-12"},
				{NoteID: 3, Title: "due tomorrow", Status: "todo", DueDate: "2026-07-13"},
				{NoteID: 4, Title: "due in two days", Status: "todo", DueDate: "2026-07-14"},
				{NoteID: 5, Title: "optional", Status: "todo", Priority: 10},
			},
			wantIDs: []int64{2, 3, 4, 1},
		},
		// Verifies an explicit count override pulls in more optional tasks when there is spare capacity; failure would make the manual refresh count parameter ineffective.
		"count override increases optional capacity": {
			cfg:           defaultOverviewConfig(),
			countOverride: 4,
			tasks: []TaskSummary{
				{NoteID: 10, Title: "highest", Status: "todo", Priority: 10},
				{NoteID: 11, Title: "second", Status: "todo", Priority: 9},
				{NoteID: 12, Title: "third", Status: "todo", Priority: 8},
				{NoteID: 13, Title: "fourth", Status: "todo", Priority: 7},
				{NoteID: 14, Title: "fifth", Status: "todo", Priority: 6},
			},
			wantIDs: []int64{10, 11, 12, 13},
		},
		// Verifies pending opt-out keeps in-progress work from being force-included; failure would make the new task-level escape hatch useless.
		"pending opt out prevents forced inclusion": {
			cfg: defaultOverviewConfig(),
			tasks: []TaskSummary{
				{NoteID: 20, Title: "in progress but skippable", Status: "in_progress", PendingDoesNotForceDailyInclusion: true},
				{NoteID: 21, Title: "priority 10", Status: "todo", Priority: 10},
				{NoteID: 22, Title: "priority 9", Status: "todo", Priority: 9},
				{NoteID: 23, Title: "priority 8", Status: "todo", Priority: 8},
			},
			wantIDs: []int64{21, 22, 23},
		},
		// Verifies empty input stays empty; failure would create phantom tasks or panic on a no-data overview.
		"empty input yields empty selection": {
			cfg:     defaultOverviewConfig(),
			tasks:   nil,
			wantIDs: nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scored := scoreOpenTasks(tc.tasks, tc.cfg, baseNow)
			got := selectDailyTasks(scored, tc.cfg, tc.countOverride)
			if diff := cmp.Diff(tc.wantIDs, noteIDs(got), cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("selectDailyTasks mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAnnotateTasksWithScores_Table(t *testing.T) {
	t.Parallel()

	tasks := []TaskSummary{
		{NoteID: 1, Title: "plain"},
		{NoteID: 2, Title: "scored"},
	}
	scored := []TaskSummary{
		{
			NoteID:                  2,
			Title:                   "scored",
			GenerationScore:         42.5,
			GenerationForcedReasons: []string{"pending"},
			GenerationScoreBreakdown: TaskScoreBreakdown{
				Priority: 10,
				Total:    42.5,
			},
			DueInDays: intPtr(1),
		},
	}

	cases := map[string]struct {
		tasks  []TaskSummary
		scored []TaskSummary
		want   []TaskSummary
	}{
		// Verifies only matching note IDs are enriched while unmatched tasks remain untouched and order is preserved; failure would corrupt the visible task list.
		"enriches matching tasks only": {
			tasks:  tasks,
			scored: scored,
			want: []TaskSummary{
				{NoteID: 1, Title: "plain"},
				{
					NoteID:                  2,
					Title:                   "scored",
					GenerationScore:         42.5,
					GenerationForcedReasons: []string{"pending"},
					GenerationScoreBreakdown: TaskScoreBreakdown{
						Priority: 10,
						Total:    42.5,
					},
					DueInDays: intPtr(1),
				},
			},
		},
		// Verifies empty input returns a non-nil empty slice; failure would make JSON output or append behavior depend on nilness.
		"empty input returns non nil empty slice": {
			tasks:  nil,
			scored: scored,
			want:   []TaskSummary{},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := annotateTasksWithScores(tc.tasks, tc.scored)
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("annotateTasksWithScores mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAnnotateTasksWithScores_CopiesForcedReasons(t *testing.T) {
	t.Parallel()

	scored := []TaskSummary{{
		NoteID:                  7,
		GenerationForcedReasons: []string{"pending"},
	}}
	annotated := annotateTasksWithScores([]TaskSummary{{NoteID: 7}}, scored)
	scored[0].GenerationForcedReasons[0] = "mutated"

	want := []TaskSummary{{
		NoteID:                  7,
		GenerationForcedReasons: []string{"pending"},
	}}
	if diff := cmp.Diff(want, annotated, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("annotateTasksWithScores should copy forced reasons (-want +got):\n%s", diff)
	}
}

func TestParseTimeToMinutes_Rapid(t *testing.T) {
	t.Parallel()

	// Invariant: canonical day/hour/minute encodings must roundtrip to the exact total number of minutes; failure would skew score weighting for time estimates.
	rapid.Check(t, func(rt *rapid.T) {
		days := rapid.IntRange(0, 5).Draw(rt, "days")
		hours := rapid.IntRange(0, 23).Draw(rt, "hours")
		minutes := rapid.IntRange(0, 59).Draw(rt, "minutes")

		encoded := durationString(days, hours, minutes)
		want := days*8*60 + hours*60 + minutes
		got := parseTimeToMinutes(encoded)
		if diff := cmp.Diff(want, got); diff != "" {
			rt.Fatalf("parseTimeToMinutes roundtrip mismatch for %q (-want +got):\n%s", encoded, diff)
		}
	})
}

func TestNormalizeOverviewConfig_Rapid(t *testing.T) {
	t.Parallel()

	// Invariant: normalization must be idempotent and must always produce a positive daily count and non-negative urgent window; failure would make repeated saves drift over time.
	rapid.Check(t, func(rt *rapid.T) {
		cfg := TaskOverviewConfig{
			DailyTaskCount:       rapid.IntRange(-50, 50).Draw(rt, "dailyTaskCount"),
			UrgentDueDays:        rapid.IntRange(-50, 50).Draw(rt, "urgentDueDays"),
			PriorityWeight:       float64(rapid.IntRange(-100, 100).Draw(rt, "priorityWeight")) / 4,
			DueUrgencyWeight:     float64(rapid.IntRange(-100, 100).Draw(rt, "dueUrgencyWeight")) / 4,
			DifficultyWeight:     float64(rapid.IntRange(-100, 100).Draw(rt, "difficultyWeight")) / 4,
			FunWeight:            float64(rapid.IntRange(-100, 100).Draw(rt, "funWeight")) / 4,
			TimeEstimationWeight: float64(rapid.IntRange(-100, 100).Draw(rt, "timeEstimationWeight")) / 4,
			FunTimeWeight:        float64(rapid.IntRange(-100, 100).Draw(rt, "funTimeWeight")) / 4,
		}

		once := normalizeOverviewConfig(cfg)
		twice := normalizeOverviewConfig(once)
		if diff := cmp.Diff(once, twice); diff != "" {
			rt.Fatalf("normalizeOverviewConfig idempotency mismatch (-once +twice):\n%s", diff)
		}
		if once.DailyTaskCount <= 0 {
			rt.Fatalf("daily task count must be positive after normalization, got %d", once.DailyTaskCount)
		}
		if once.UrgentDueDays < 0 {
			rt.Fatalf("urgent due days must be non-negative after normalization, got %d", once.UrgentDueDays)
		}
	})
}

func TestScoreOpenTasks_Rapid(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)

	// Invariant: scoring must exclude done tasks, preserve unique task identity, maintain force-rank-before-score ordering, and keep breakdown totals internally consistent; failure would break both generation and the UI preview.
	rapid.Check(t, func(rt *rapid.T) {
		cfg := TaskOverviewConfig{
			DailyTaskCount:       rapid.IntRange(1, 10).Draw(rt, "dailyTaskCount"),
			UrgentDueDays:        rapid.IntRange(0, 7).Draw(rt, "urgentDueDays"),
			PriorityWeight:       float64(rapid.IntRange(-20, 20).Draw(rt, "priorityWeight")) / 2,
			DueUrgencyWeight:     float64(rapid.IntRange(-20, 20).Draw(rt, "dueUrgencyWeight")) / 2,
			DifficultyWeight:     float64(rapid.IntRange(-20, 20).Draw(rt, "difficultyWeight")) / 2,
			FunWeight:            float64(rapid.IntRange(-20, 20).Draw(rt, "funWeight")) / 2,
			TimeEstimationWeight: float64(rapid.IntRange(-20, 20).Draw(rt, "timeEstimationWeight")) / 2,
			FunTimeWeight:        float64(rapid.IntRange(-20, 20).Draw(rt, "funTimeWeight")) / 2,
		}
		tasks := generatedTasks(rt, baseNow)

		got := scoreOpenTasks(tasks, cfg, baseNow)
		wantLen := 0
		for _, task := range tasks {
			if task.Status != "done" {
				wantLen++
			}
		}
		if diff := cmp.Diff(wantLen, len(got)); diff != "" {
			rt.Fatalf("scoreOpenTasks length mismatch (-want +got):\n%s", diff)
		}

		seen := map[int64]struct{}{}
		prevRank := math.MaxInt
		prevScore := math.Inf(1)
		for idx, task := range got {
			if task.Status == "done" {
				rt.Fatalf("done task leaked into scored output at index %d: %+v", idx, task)
			}
			if _, dup := seen[task.NoteID]; dup {
				rt.Fatalf("duplicate note id %d in scored output", task.NoteID)
			}
			seen[task.NoteID] = struct{}{}

			total := task.GenerationScoreBreakdown.DueUrgency +
				task.GenerationScoreBreakdown.Priority +
				task.GenerationScoreBreakdown.Difficulty +
				task.GenerationScoreBreakdown.Fun +
				task.GenerationScoreBreakdown.TimeEstimation +
				task.GenerationScoreBreakdown.FunTime
			if !nearlyEqual(task.GenerationScore, total) {
				rt.Fatalf("task %d total mismatch: got %v want %v", task.NoteID, task.GenerationScore, total)
			}

			rank := generationForceRank(task)
			if rank > prevRank {
				rt.Fatalf("force rank increased at index %d: prev=%d current=%d", idx, prevRank, rank)
			}
			if rank == prevRank && task.GenerationScore > prevScore && !nearlyEqual(task.GenerationScore, prevScore) {
				rt.Fatalf("score increased within the same force rank at index %d: prev=%v current=%v", idx, prevScore, task.GenerationScore)
			}
			prevRank = rank
			prevScore = task.GenerationScore

			expectedDueSoon := task.DueInDays != nil && *task.DueInDays <= normalizeOverviewConfig(cfg).UrgentDueDays
			if hasReason(task.GenerationForcedReasons, "due_soon") != expectedDueSoon {
				rt.Fatalf("due_soon mismatch for task %d: reasons=%v dueInDays=%v urgentWindow=%d", task.NoteID, task.GenerationForcedReasons, ptrValue(task.DueInDays), normalizeOverviewConfig(cfg).UrgentDueDays)
			}
			expectedPending := task.Status == "in_progress" && !task.PendingDoesNotForceDailyInclusion
			if hasReason(task.GenerationForcedReasons, "pending") != expectedPending {
				rt.Fatalf("pending mismatch for task %d: status=%s optOut=%v reasons=%v", task.NoteID, task.Status, task.PendingDoesNotForceDailyInclusion, task.GenerationForcedReasons)
			}
		}
	})
}

func TestSelectDailyTasks_Rapid(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)

	// Invariant: selection must return exactly the scored prefix implied by forced-task expansion and count override rules; failure would make refreshes non-deterministic and silently drop urgent tasks.
	rapid.Check(t, func(rt *rapid.T) {
		cfg := TaskOverviewConfig{
			DailyTaskCount:       rapid.IntRange(1, 8).Draw(rt, "dailyTaskCount"),
			UrgentDueDays:        rapid.IntRange(0, 7).Draw(rt, "urgentDueDays"),
			PriorityWeight:       float64(rapid.IntRange(-10, 10).Draw(rt, "priorityWeight")),
			DueUrgencyWeight:     float64(rapid.IntRange(-10, 10).Draw(rt, "dueUrgencyWeight")),
			DifficultyWeight:     float64(rapid.IntRange(-10, 10).Draw(rt, "difficultyWeight")),
			FunWeight:            float64(rapid.IntRange(-10, 10).Draw(rt, "funWeight")),
			TimeEstimationWeight: float64(rapid.IntRange(-10, 10).Draw(rt, "timeEstimationWeight")),
			FunTimeWeight:        float64(rapid.IntRange(-10, 10).Draw(rt, "funTimeWeight")),
		}
		countOverride := rapid.IntRange(0, 12).Draw(rt, "countOverride")
		tasks := generatedTasks(rt, baseNow)
		scored := scoreOpenTasks(tasks, cfg, baseNow)

		got := selectDailyTasks(scored, cfg, countOverride)
		forcedCount := 0
		for _, task := range scored {
			if len(task.GenerationForcedReasons) > 0 {
				forcedCount++
			}
		}
		target := normalizeOverviewConfig(cfg).DailyTaskCount
		if countOverride > 0 {
			target = countOverride
		}
		if target < 1 {
			target = 1
		}
		expectedLen := maxInt(target, forcedCount)
		if expectedLen > len(scored) {
			expectedLen = len(scored)
		}
		if diff := cmp.Diff(expectedLen, len(got)); diff != "" {
			rt.Fatalf("selectDailyTasks length mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(noteIDs(scored[:expectedLen]), noteIDs(got), cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("selectDailyTasks prefix mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestAnnotateTasksWithScores_Rapid(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)

	// Invariant: annotation must be idempotent and preserve task ordering/length; failure would make repeated view refreshes continually mutate already-annotated tasks.
	rapid.Check(t, func(rt *rapid.T) {
		tasks := generatedTasks(rt, baseNow)
		scored := scoreOpenTasks(tasks, defaultOverviewConfig(), baseNow)

		once := annotateTasksWithScores(tasks, scored)
		twice := annotateTasksWithScores(once, scored)
		if diff := cmp.Diff(once, twice, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("annotateTasksWithScores idempotency mismatch (-once +twice):\n%s", diff)
		}
		if diff := cmp.Diff(noteIDs(tasks), noteIDs(once), cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("annotateTasksWithScores changed task order (-want +got):\n%s", diff)
		}
	})
}

func generatedTasks(rt *rapid.T, baseNow time.Time) []TaskSummary {
	rt.Helper()

	n := rapid.IntRange(0, 18).Draw(rt, "taskCount")
	tasks := make([]TaskSummary, 0, n)
	for i := 0; i < n; i++ {
		status := rapid.SampledFrom([]string{"todo", "in_progress", "done"}).Draw(rt, fmt.Sprintf("status_%d", i))
		hasDueDate := rapid.Bool().Draw(rt, fmt.Sprintf("hasDueDate_%d", i))
		dueDate := ""
		if hasDueDate {
			dueDate = baseNow.AddDate(0, 0, rapid.IntRange(-7, 10).Draw(rt, fmt.Sprintf("dueOffset_%d", i))).Format("2006-01-02")
		}
		days := rapid.IntRange(0, 2).Draw(rt, fmt.Sprintf("days_%d", i))
		hours := rapid.IntRange(0, 12).Draw(rt, fmt.Sprintf("hours_%d", i))
		minutes := rapid.IntRange(0, 59).Draw(rt, fmt.Sprintf("minutes_%d", i))
		tasks = append(tasks, TaskSummary{
			NoteID:                            int64(i + 1),
			Title:                             fmt.Sprintf("task-%d", i+1),
			Status:                            status,
			Priority:                          rapid.IntRange(0, 10).Draw(rt, fmt.Sprintf("priority_%d", i)),
			Difficulty:                        rapid.IntRange(0, 10).Draw(rt, fmt.Sprintf("difficulty_%d", i)),
			Fun:                               rapid.IntRange(-5, 5).Draw(rt, fmt.Sprintf("fun_%d", i)),
			DueDate:                           dueDate,
			TimeEstimation:                    durationString(days, hours, minutes),
			PendingDoesNotForceDailyInclusion: rapid.Bool().Draw(rt, fmt.Sprintf("pendingOptOut_%d", i)),
		})
	}
	return tasks
}

func noteIDs(tasks []TaskSummary) []int64 {
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.NoteID)
	}
	return ids
}

func durationString(days, hours, minutes int) string {
	if days == 0 && hours == 0 && minutes == 0 {
		return ""
	}
	s := ""
	if days > 0 {
		s += fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		s += fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		s += fmt.Sprintf("%dm", minutes)
	}
	return s
}

func hasReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func ptrValue(v *int) int {
	if v == nil {
		return math.MaxInt32
	}
	return *v
}

func intPtr(v int) *int {
	return &v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
