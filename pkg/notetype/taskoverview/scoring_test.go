package taskoverview

import (
	"testing"
	"time"
)

func TestSelectDailyTasks_ForceIncludesDueSoonAndPending(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	cfg := TaskOverviewConfig{
		DailyTaskCount:       3,
		UrgentDueDays:        3,
		PriorityWeight:       4,
		DueUrgencyWeight:     6,
		DifficultyWeight:     -1,
		FunWeight:            0.5,
		TimeEstimationWeight: -0.25,
		FunTimeWeight:        0.1,
	}

	tasks := []TaskSummary{
		{NoteID: 1, Title: "In progress carry-over", Status: "in_progress", Priority: 1},
		{NoteID: 2, Title: "Due tomorrow", Status: "todo", Priority: 1, DueDate: "2026-07-13"},
		{NoteID: 3, Title: "Due in two days", Status: "todo", Priority: 2, DueDate: "2026-07-14"},
		{NoteID: 4, Title: "Due today", Status: "todo", Priority: 0, DueDate: "2026-07-12"},
		{NoteID: 5, Title: "Highest optional score", Status: "todo", Priority: 10, Fun: 5},
		{NoteID: 6, Title: "Done task", Status: "done", Priority: 10},
	}

	scored := scoreOpenTasks(tasks, cfg, now)
	picked := selectDailyTasks(scored, cfg, 0)

	if got, want := len(picked), 4; got != want {
		t.Fatalf("picked len = %d, want %d", got, want)
	}

	gotIDs := []int64{picked[0].NoteID, picked[1].NoteID, picked[2].NoteID, picked[3].NoteID}
	wantIDs := []int64{4, 2, 3, 1}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("picked[%d] = %d, want %d (full order: %v)", i, gotIDs[i], wantIDs[i], gotIDs)
		}
	}
}

func TestSelectDailyTasks_RespectsPendingOptOut(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	cfg := defaultOverviewConfig()

	tasks := []TaskSummary{
		{
			NoteID:                            1,
			Title:                             "In progress but skippable",
			Status:                            "in_progress",
			Priority:                          0,
			PendingDoesNotForceDailyInclusion: true,
		},
		{NoteID: 2, Title: "High priority task", Status: "todo", Priority: 10},
		{NoteID: 3, Title: "Another high priority task", Status: "todo", Priority: 9},
		{NoteID: 4, Title: "Third high priority task", Status: "todo", Priority: 8},
	}

	scored := scoreOpenTasks(tasks, cfg, now)
	picked := selectDailyTasks(scored, cfg, 0)

	if got, want := len(picked), 3; got != want {
		t.Fatalf("picked len = %d, want %d", got, want)
	}
	for _, task := range picked {
		if task.NoteID == 1 {
			t.Fatalf("task 1 should not have been force-included: %+v", picked)
		}
	}
}
