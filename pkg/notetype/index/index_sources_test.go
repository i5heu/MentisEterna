package index

import (
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
)

func TestBuildIndexDifferentiatesUserAndAutoTags(t *testing.T) {
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	insertNote := func(title string) int64 {
		t.Helper()
		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES (?, 'standard')`, title)
		if err != nil {
			t.Fatalf("insert note %q: %v", title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last insert id for %q: %v", title, err)
		}
		return id
	}
	insertTag := func(name string) int64 {
		t.Helper()
		res, err := d.Exec(`INSERT INTO tags (name) VALUES (?)`, name)
		if err != nil {
			t.Fatalf("insert tag %q: %v", name, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last insert id for tag %q: %v", name, err)
		}
		return id
	}

	noteUser := insertNote("User only")
	noteAuto := insertNote("Auto only")
	noteMixed := insertNote("Mixed")
	tagID := insertTag("alpha")

	if _, err := d.Exec(`INSERT INTO tags_refs (note_id, tag_id) VALUES (?, ?), (?, ?)`, noteUser, tagID, noteMixed, tagID); err != nil {
		t.Fatalf("insert user tag refs: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO auto_tags_refs (note_id, tag_id) VALUES (?, ?), (?, ?)`, noteAuto, tagID, noteMixed, tagID); err != nil {
		t.Fatalf("insert auto tag refs: %v", err)
	}

	entries, err := buildIndex(d.DB, noteUser, Payload{Mode: "global"})
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.Tag != "alpha" {
		t.Fatalf("entry tag = %q, want alpha", entry.Tag)
	}
	if entry.Source != "mixed" {
		t.Fatalf("entry source = %q, want mixed", entry.Source)
	}
	if entry.Count != 3 {
		t.Fatalf("entry count = %d, want 3", entry.Count)
	}
	if entry.UserCount != 2 {
		t.Fatalf("entry user_count = %d, want 2", entry.UserCount)
	}
	if entry.AutoCount != 2 {
		t.Fatalf("entry auto_count = %d, want 2", entry.AutoCount)
	}

	sources := map[int64]string{}
	for _, note := range entry.Notes {
		sources[note.NoteID] = note.Source
	}
	if sources[noteUser] != "user" {
		t.Fatalf("user note source = %q, want user", sources[noteUser])
	}
	if sources[noteAuto] != "auto" {
		t.Fatalf("auto note source = %q, want auto", sources[noteAuto])
	}
	if sources[noteMixed] != "mixed" {
		t.Fatalf("mixed note source = %q, want mixed", sources[noteMixed])
	}
}
