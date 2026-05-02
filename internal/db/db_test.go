package db

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpen(t *testing.T) {
	d := openTestDB(t)
	if err := d.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestTableColumnsNotes(t *testing.T) {
	d := openTestDB(t)
	cols, err := d.tableColumns("notes")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	for _, col := range []string{"id", "title", "parent_id", "created_at"} {
		if !cols[col] {
			t.Errorf("missing column %q in notes", col)
		}
	}
	if cols["body"] {
		t.Error("column body should have been migrated out of notes")
	}
	if cols["updated_at"] {
		t.Error("column updated_at should have been migrated out of notes")
	}
}

func TestTableColumnsUpdates(t *testing.T) {
	d := openTestDB(t)
	cols, err := d.tableColumns("updates")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	for _, col := range []string{"id", "note_id", "body", "created_at"} {
		if !cols[col] {
			t.Errorf("missing column %q in updates", col)
		}
	}
}

func TestTableColumnsNonexistent(t *testing.T) {
	d := openTestDB(t)
	cols, err := d.tableColumns("nonexistent")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("expected empty map, got %v", cols)
	}
}
