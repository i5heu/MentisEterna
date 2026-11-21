package rss

import (
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// ensureUpdates creates the updates table for tests that read note bodies.
func ensureUpdates(t *testing.T, d *db.DB) {
	t.Helper()
	if _, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS updates (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id    INTEGER NOT NULL,
			body       TEXT    NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		)`); err != nil {
		t.Fatalf("create updates: %v", err)
	}
}

// makeNote inserts a typed note with an optional parent and returns its id.
func makeNote(t *testing.T, d *db.DB, title, noteType string, parent int64) int64 {
	t.Helper()
	var parentArg any
	if parent > 0 {
		parentArg = parent
	}
	res, err := d.Exec(`INSERT INTO notes (title, type, parent_id) VALUES (?, ?, ?)`, title, noteType, parentArg)
	if err != nil {
		t.Fatalf("insert note %q: %v", title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("note id: %v", err)
	}
	return id
}

// setNoteBody creates the updates table if needed and writes a note body.
func setNoteBody(t *testing.T, d *db.DB, noteID int64, body string) {
	t.Helper()
	ensureUpdates(t, d)
	if _, err := d.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, body); err != nil {
		t.Fatalf("insert body: %v", err)
	}
}

func TestResolvePromptDescendantNearestWins(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	reader := makeNote(t, d, "Reader", PluginID, 0)
	branch := makeNote(t, d, "Branch", "standard", reader)
	deepPrompt := makeNote(t, d, "Deep prompt", PromptPluginID, branch)
	shallowPrompt := makeNote(t, d, "Shallow prompt", PromptPluginID, reader)

	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.NoteID != shallowPrompt {
		t.Errorf("nearest (shallower) prompt must win: got %d, want %d (deep=%d)", got.NoteID, shallowPrompt, deepPrompt)
	}
}

func TestResolvePromptSubtreeBeatsSibling(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	folder := makeNote(t, d, "Folder", "standard", 0)
	prompt := makeNote(t, d, "Folder prompt", PromptPluginID, folder) // sibling of reader
	reader := makeNote(t, d, "Reader", PluginID, folder)
	ownPrompt := makeNote(t, d, "Own prompt", PromptPluginID, reader) // reader's child

	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.NoteID != ownPrompt {
		t.Errorf("subtree prompt must beat sibling: got %d, want %d (sibling=%d)", got.NoteID, ownPrompt, prompt)
	}
}

func TestResolvePromptUncleLevel(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	root := makeNote(t, d, "Root", "standard", 0)
	unclePrompt := makeNote(t, d, "Uncle prompt", PromptPluginID, root)
	mid := makeNote(t, d, "Mid", "standard", root)
	reader := makeNote(t, d, "Reader", PluginID, mid)

	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.NoteID != unclePrompt {
		t.Errorf("uncle prompt must bind at ancestor level: got %d, want %d", got.NoteID, unclePrompt)
	}
}

func TestResolvePromptNearestAncestorLevelBeatsHigher(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	grandRoot := makeNote(t, d, "GrandRoot", "standard", 0)
	grandPrompt := makeNote(t, d, "Grand prompt", PromptPluginID, grandRoot)

	folder := makeNote(t, d, "Folder", "standard", grandRoot)
	nearPrompt := makeNote(t, d, "Near prompt", PromptPluginID, folder)
	reader := makeNote(t, d, "Reader", PluginID, folder)

	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.NoteID != nearPrompt {
		t.Errorf("nearest ancestor level must win: got %d, want %d (grand=%d)", got.NoteID, nearPrompt, grandPrompt)
	}
}

func TestResolvePromptTieLowestIDAndWarning(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	folder := makeNote(t, d, "Folder", "standard", 0)
	first := makeNote(t, d, "Prompt A", PromptPluginID, folder)
	second := makeNote(t, d, "Prompt B", PromptPluginID, folder)
	reader := makeNote(t, d, "Reader", PluginID, folder)

	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.NoteID != first {
		t.Errorf("tie must pick the lowest id: got %d, want %d (other=%d)", got.NoteID, first, second)
	}
	if !strings.Contains(got.Warning, "multiple RSS Prompt") {
		t.Errorf("tie warning missing: %+v", got)
	}
}

func TestResolvePromptNoneDefaults(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	reader := makeNote(t, d, "Reader", PluginID, 0)
	standard := makeNote(t, d, "Other", "standard", reader) // non-prompt descendants ignored

	_ = standard
	got, err := resolvePromptNote(d.DB, reader)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got.Default || got.NoteID != 0 {
		t.Errorf("expected default prompt, got %+v", got)
	}
}

func TestPromptTextBodyAndEmpty(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)

	reader := makeNote(t, d, "Reader", PluginID, 0)

	// No prompt in scope → default.
	ref, text, err := promptText(d.DB, reader)
	if err != nil {
		t.Fatalf("promptText: %v", err)
	}
	if !ref.Default || text != defaultPrompt {
		t.Errorf("expected default prompt, got ref=%+v", ref)
	}

	// Prompt in scope with body.
	prompt := makeNote(t, d, "Prompt", PromptPluginID, 0) // sibling: reader's parent's other child
	// reader parentless: siblings = parent's children → reader is parentless,
	// so use a child prompt instead.
	_ = prompt
	childPrompt := makeNote(t, d, "Child prompt", PromptPluginID, reader)
	setNoteBody(t, d, childPrompt, "Classify everything as NEWS.")

	ref, text, err = promptText(d.DB, reader)
	if err != nil {
		t.Fatalf("promptText with body: %v", err)
	}
	if ref.NoteID != childPrompt || text != "Classify everything as NEWS." {
		t.Errorf("prompt body not loaded: ref=%+v text=%q", ref, text)
	}

	// Reading the current body = latest updates row wins.
	setNoteBody(t, d, childPrompt, "Classify everything as TECH.")
	_, text, err = promptText(d.DB, reader)
	if err != nil {
		t.Fatalf("promptText second: %v", err)
	}
	if text != "Classify everything as TECH." {
		t.Errorf("latest body not used: %q", text)
	}
}
