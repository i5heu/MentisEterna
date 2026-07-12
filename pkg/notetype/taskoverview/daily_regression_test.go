package taskoverview

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	internaldb "github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"github.com/i5heu/MentisEterna/pkg/notetype/task"
)

func TestLoadDailyTasks_DoesNotMergeRapidGenerationsWithSameTimestamp(t *testing.T) {
	d, overviewPlugin, taskPlugin := newDailyRegressionDB(t)
	overviewNoteID := plugintest.CreateNote(t, d, "Overview", overviewPlugin)
	firstIDs := createTaskNotes(t, d, taskPlugin, 3, "First")
	secondIDs := createTaskNotes(t, d, taskPlugin, 3, "Second")

	fixedNow := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	restoreNow := nowUTC
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() { nowUTC = restoreNow })

	restoreGenerationID := nextDailyGenerationID
	generationIDs := map[int]string{
		// Forces the first rapid click to persist one batch identity; failure would let multiple batches collapse into one generation key.
		1: "2026-07-12T12:00:00Z#00000000000000000001",
		// Forces the second rapid click to use a distinct batch identity despite the same timestamp; failure would reproduce the doubled-task bug.
		2: "2026-07-12T12:00:00Z#00000000000000000002",
	}
	generationCall := 0
	nextDailyGenerationID = func(_ string) string {
		generationCall++
		return generationIDs[generationCall]
	}
	t.Cleanup(func() { nextDailyGenerationID = restoreGenerationID })

	if err := storeDailyTasks(d.DB, overviewNoteID, taskSummariesForIDs(firstIDs)); err != nil {
		t.Fatalf("store first generation: %v", err)
	}
	if err := storeDailyTasks(d.DB, overviewNoteID, taskSummariesForIDs(secondIDs)); err != nil {
		t.Fatalf("store second generation: %v", err)
	}

	got, err := loadDailyTasks(d.DB, overviewNoteID)
	if err != nil {
		t.Fatalf("loadDailyTasks: %v", err)
	}

	wantIDs := secondIDs
	if diff := cmp.Diff(wantIDs, noteIDs(got), cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("latest generation should not merge rapid clicks (-want +got):\n%s", diff)
	}
}

func TestLoadDailyHistory_DoesNotCollapseRapidGenerationsWithSameTimestamp(t *testing.T) {
	d, overviewPlugin, taskPlugin := newDailyRegressionDB(t)
	overviewNoteID := plugintest.CreateNote(t, d, "Overview", overviewPlugin)
	firstIDs := createTaskNotes(t, d, taskPlugin, 2, "First")
	secondIDs := createTaskNotes(t, d, taskPlugin, 2, "Second")

	fixedNow := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	restoreNow := nowUTC
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() { nowUTC = restoreNow })

	restoreGenerationID := nextDailyGenerationID
	generationIDs := map[int]string{
		// Captures the older rapid generation under one batch key so history can distinguish it later; failure would merge multiple runs into one history entry.
		1: "2026-07-12T12:00:00Z#00000000000000000001",
		// Captures the newer rapid generation under a second batch key with the same timestamp; failure would make history hide the earlier run.
		2: "2026-07-12T12:00:00Z#00000000000000000002",
	}
	generationCall := 0
	nextDailyGenerationID = func(_ string) string {
		generationCall++
		return generationIDs[generationCall]
	}
	t.Cleanup(func() { nextDailyGenerationID = restoreGenerationID })

	if err := storeDailyTasks(d.DB, overviewNoteID, taskSummariesForIDs(firstIDs)); err != nil {
		t.Fatalf("store first generation: %v", err)
	}
	if err := storeDailyTasks(d.DB, overviewNoteID, taskSummariesForIDs(secondIDs)); err != nil {
		t.Fatalf("store second generation: %v", err)
	}

	got, err := loadDailyHistory(d.DB, overviewNoteID)
	if err != nil {
		t.Fatalf("loadDailyHistory: %v", err)
	}

	want := []DailyHistoryEntry{{
		GeneratedAt: "2026-07-12T12:00:00Z",
		Tasks:       taskSummariesForIDs(firstIDs),
	}}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(TaskSummary{}, "Title", "CreatedAt", "UpdatedAt", "Body", "Status", "Priority", "Difficulty", "Fun", "DueDate", "TimeEstimation", "TimeUsed", "Recurring", "CompletedAt", "PendingDoesNotForceDailyInclusion", "DueInDays", "GenerationScore", "GenerationForcedReasons", "GenerationScoreBreakdown")); diff != "" {
		t.Fatalf("history should keep rapid generations separate (-want +got):\n%s", diff)
	}
}

func newDailyRegressionDB(t *testing.T) (*internaldb.DB, *TaskOverviewPlugin, *task.TaskPlugin) {
	t.Helper()
	overviewPlugin := &TaskOverviewPlugin{}
	taskPlugin := &task.TaskPlugin{}
	d := plugintest.DB(t, overviewPlugin)
	if err := taskPlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("task InitSchema: %v", err)
	}
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS updates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		note_id INTEGER NOT NULL,
		body TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatalf("create updates table: %v", err)
	}
	return d, overviewPlugin, taskPlugin
}

func createTaskNotes(t *testing.T, d *internaldb.DB, taskPlugin *task.TaskPlugin, count int, prefix string) []int64 {
	t.Helper()
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		ids = append(ids, plugintest.CreateNote(t, d, prefix, taskPlugin))
	}
	return ids
}

func taskSummariesForIDs(ids []int64) []TaskSummary {
	tasks := make([]TaskSummary, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, TaskSummary{NoteID: id})
	}
	return tasks
}
