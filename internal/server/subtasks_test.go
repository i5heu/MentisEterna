package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// stubSubTaskGenerator is a deterministic SubTaskGenerator for tests. It also
// implements Generator so it can be passed as the chat client to New().
type stubSubTaskGenerator struct {
	title      string
	suggestion llm.SubTaskSuggestion
	err        error
	lastInput  llm.SubTaskGenerationInput
}

func (g *stubSubTaskGenerator) GenerateTitle(_ string) (string, error) {
	return g.title, nil
}

func (g *stubSubTaskGenerator) GenerateSubTasks(input llm.SubTaskGenerationInput) (llm.SubTaskSuggestion, error) {
	g.lastInput = input
	return g.suggestion, g.err
}

// TestGenerateSubTasksTaskAppendsAndBroadcasts verifies the job task:
// generated subtasks are appended (blank labels skipped, duplicates of
// existing rows not re-added), the task context is passed to the generator,
// and a notes.changed broadcast with reason "subtasks_generated" is sent.
func TestGenerateSubTasksTaskAppendsAndBroadcasts(t *testing.T) {
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	gen := &stubSubTaskGenerator{title: "Title"}
	s := New(d, ":0", nil, gen, nil, nil)
	if err := notetype.Registry["task"].InitSchema(d.DB); err != nil {
		t.Fatalf("init task schema: %v", err)
	}

	note := helperCreateNote(t, s, "Ship feature", "body context", nil)
	if _, err := d.DB.Exec(`UPDATE notes SET type = 'task' WHERE id = ?`, note.ID); err != nil {
		t.Fatalf("set note type: %v", err)
	}

	// Save an initial task config with one existing subtask.
	plugin := notetype.Registry["task"]
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := plugin.(notetype.ConfigSaver).SaveConfig(context.Background(), tx, 0, note.ID,
		json.RawMessage(`{"status":"todo","description":"Make it work","subtasks":[{"label":"Duplicate","checked":false}]}`),
	); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	token := createTestSession(t, s)
	httpServer, wsURL := newLiveTestHTTPServer(t, s)
	conn, _, err := dialLiveWebSocket(t, wsURL, token, httpServer.URL)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	requireLiveMessageType(t, conn, liveTypeReady)

	gen.suggestion = llm.SubTaskSuggestion{Subtasks: []llm.SubTaskItem{
		{Label: "Write tests"},
		{Label: "   "},
		{Label: "Duplicate"},
		{Label: "Ship"},
	}}

	result, err := s.generateSubTasksTask(d.DB, []byte(fmt.Sprintf(`{"note_id":%d}`, note.ID)))
	if err != nil {
		t.Fatalf("generateSubTasksTask: %v", err)
	}
	if want := fmt.Sprintf("Generated 2 subtasks for note %d", note.ID); result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}

	// The generator received the full task context.
	if gen.lastInput.Title != "Ship feature" {
		t.Errorf("Title = %q, want %q", gen.lastInput.Title, "Ship feature")
	}
	if gen.lastInput.Description != "Make it work" {
		t.Errorf("Description = %q, want %q", gen.lastInput.Description, "Make it work")
	}
	if gen.lastInput.Body != "body context" {
		t.Errorf("Body = %q, want %q", gen.lastInput.Body, "body context")
	}
	if len(gen.lastInput.ExistingSubtasks) != 1 || gen.lastInput.ExistingSubtasks[0] != "duplicate" {
		t.Errorf("ExistingSubtasks = %v, want [duplicate]", gen.lastInput.ExistingSubtasks)
	}

	// The table holds the existing row plus the two appended ones, all unchecked.
	rows, err := d.DB.Query(`SELECT label, checked FROM ct_task_subtasks WHERE note_id = ? ORDER BY id`, note.ID)
	if err != nil {
		t.Fatalf("query subtasks: %v", err)
	}
	defer rows.Close()
	type row struct {
		label   string
		checked int
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.label, &r.checked); err != nil {
			t.Fatalf("scan subtask: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate subtasks: %v", err)
	}
	want := []row{{label: "Duplicate", checked: 0}, {label: "Write tests", checked: 0}, {label: "Ship", checked: 0}}
	if len(got) != len(want) {
		t.Fatalf("subtask rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subtask rows = %+v, want %+v", got, want)
		}
	}

	msg := requireLiveMessageType(t, conn, liveTypeNotesChange)
	if msg.Reason != "subtasks_generated" {
		t.Fatalf("reason = %q, want %q", msg.Reason, "subtasks_generated")
	}
	assertNoteIDsEqual(t, msg.NoteIDs, note.ID)
}

func TestGenerateSubTasksTaskMissingNote(t *testing.T) {
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	gen := &stubSubTaskGenerator{title: "Title"}
	s := New(d, ":0", nil, gen, nil, nil)

	result, err := s.generateSubTasksTask(d.DB, []byte(`{"note_id":999999}`))
	if err != nil {
		t.Fatalf("generateSubTasksTask: %v", err)
	}
	if want := "Skipped subtask generation for missing note 999999"; result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}
