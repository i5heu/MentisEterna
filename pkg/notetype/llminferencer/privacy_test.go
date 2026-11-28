package llminferencer

import (
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/llm"
)

func TestNoteIsHighPrivate(t *testing.T) {
	db := testDB(t)
	note := createNote(t, db, "Secrets", "standard", 0)

	ok, err := NoteIsHighPrivate(db, note)
	if err != nil {
		t.Fatalf("NoteIsHighPrivate: %v", err)
	}
	if ok {
		t.Fatal("new notes are not highly private by default")
	}

	if _, err := db.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, note); err != nil {
		t.Fatalf("flag note: %v", err)
	}
	ok, err = NoteIsHighPrivate(db, note)
	if err != nil {
		t.Fatalf("NoteIsHighPrivate: %v", err)
	}
	if !ok {
		t.Fatal("flagged note must report highly private")
	}

	// Missing notes and id 0 are not private (and never error).
	if ok, err := NoteIsHighPrivate(db, 99999); err != nil || ok {
		t.Fatalf("missing note: ok=%v err=%v", ok, err)
	}
	if ok, err := NoteIsHighPrivate(db, 0); err != nil || ok {
		t.Fatalf("id 0: ok=%v err=%v", ok, err)
	}
}

func TestAnyNoteHighPrivate(t *testing.T) {
	db := testDB(t)
	a := createNote(t, db, "Public", "standard", 0)
	b := createNote(t, db, "Private", "standard", 0)
	if _, err := db.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, b); err != nil {
		t.Fatalf("flag note: %v", err)
	}

	if ok, err := AnyNoteHighPrivate(db, a); err != nil || ok {
		t.Fatalf("public only: ok=%v err=%v", ok, err)
	}
	if ok, err := AnyNoteHighPrivate(db, a, b); err != nil || !ok {
		t.Fatalf("mixed set must be private: ok=%v err=%v", ok, err)
	}
	if ok, err := AnyNoteHighPrivate(db, 0); err != nil || ok {
		t.Fatalf("id 0 only: ok=%v err=%v", ok, err)
	}
	if ok, err := AnyNoteHighPrivate(db); err != nil || ok {
		t.Fatalf("empty set: ok=%v err=%v", ok, err)
	}
}

// A remote binding must be downgraded to the config.toml local client when
// any of the notes feeding the prompt is marked highly private.
func TestResolveChatForNotesDowngradesRemote(t *testing.T) {
	db := testDB(t)
	inferencer := createNote(t, db, "Inferencer", PluginID, 0)
	saveCfg(t, db, inferencer, validPayload) // default_tier middle → binds "chat" remotely
	public := createNote(t, db, "Public", "standard", 0)

	// No private notes involved: the remote binding wins.
	client, res, err := ResolveChatForNotes(db, "chat", public, 0)
	if err != nil {
		t.Fatalf("ResolveChatForNotes: %v", err)
	}
	if res.Source != SourceNote || client.Source != llm.ChatSourceNote {
		t.Fatalf("public notes must keep the remote resolution: %+v", res)
	}

	// A private note in the prompt downgrades to the local LLM.
	private := createNote(t, db, "Private", "standard", 0)
	if _, err := db.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, private); err != nil {
		t.Fatalf("flag note: %v", err)
	}
	client, res, err = ResolveChatForNotes(db, "chat", public, private)
	if err != nil {
		t.Fatalf("ResolveChatForNotes: %v", err)
	}
	if res.Source != SourceConfig || client.Source != llm.ChatSourceConfig {
		t.Fatalf("private note must downgrade to config: %+v", res)
	}
	if client.Model != llm.FeatureModel("chat") || client.BaseURL != llm.FeatureBaseURL("chat") {
		t.Errorf("downgraded client must be the chat feature client: %q %q", client.BaseURL, client.Model)
	}
	if !strings.Contains(res.Warning, "highly private") {
		t.Errorf("downgrade warning missing: %q", res.Warning)
	}
}

// An explicit {local: true} pin is local already — a private note must not
// disturb it.
func TestResolveChatForNotesKeepsLocalPin(t *testing.T) {
	db := testDB(t)
	inferencer := createNote(t, db, "Inferencer", PluginID, 0)
	saveCfg(t, db, inferencer, `{
		"provider": "openrouter",
		"api_key": "sk-test-123",
		"default_tier": "middle",
		"tiers": {"middle": {"model": "prov/mid"}},
		"usages": {"chat": {"local": true}}
	}`)
	private := createNote(t, db, "Private", "standard", 0)
	if _, err := db.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, private); err != nil {
		t.Fatalf("flag note: %v", err)
	}

	client, res, err := ResolveChatForNotes(db, "chat", private)
	if err != nil {
		t.Fatalf("ResolveChatForNotes: %v", err)
	}
	if res.Source != SourceLocal || client.Source != llm.ChatSourceLocal {
		t.Fatalf("local pin must survive private notes: %+v", res)
	}
}

// Without an inferencer note the resolution is the config fallback already —
// private notes change nothing.
func TestResolveChatForNotesWithoutInferencer(t *testing.T) {
	db := testDB(t)
	private := createNote(t, db, "Private", "standard", 0)
	if _, err := db.Exec(`UPDATE notes SET high_private = 1 WHERE id = ?`, private); err != nil {
		t.Fatalf("flag note: %v", err)
	}

	client, res, err := ResolveChatForNotes(db, "chat", private)
	if err != nil {
		t.Fatalf("ResolveChatForNotes: %v", err)
	}
	if res.Source != SourceConfig || client.Source != llm.ChatSourceConfig {
		t.Fatalf("config fallback must survive private notes: %+v", res)
	}
}
