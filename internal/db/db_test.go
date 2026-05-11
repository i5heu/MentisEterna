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

func TestMediaTablesExist(t *testing.T) {
	d := openTestDB(t)
	for _, table := range []string{"files", "file_s3", "files_refs"} {
		var name string
		err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
}

func TestFileRefsCascadeOnNoteDelete(t *testing.T) {
	d := openTestDB(t)
	// Create a note
	res, err := d.Exec(`INSERT INTO notes (title) VALUES ('test')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	// Insert a file
	res, err = d.Exec(`INSERT INTO files (storage_key, filename, mime_type, size_bytes, ciphertext_sha256, aes_key, aes_nonce) VALUES ('key1', 'test.pdf', 'application/pdf', 100, 'sha', X'00', X'00')`)
	if err != nil {
		t.Fatal(err)
	}
	fileID, _ := res.LastInsertId()

	// Insert a ref
	_, err = d.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, noteID, fileID)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the note
	_, err = d.Exec(`DELETE FROM notes WHERE id = ?`, noteID)
	if err != nil {
		t.Fatal(err)
	}

	// Ref should be gone
	var count int
	err = d.QueryRow(`SELECT COUNT(*) FROM files_refs WHERE file_id = ?`, fileID).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 refs after note delete, got %d", count)
	}
}

func TestFilesOriginalNoteUsesSetNull(t *testing.T) {
	d := openTestDB(t)
	// Create a note
	res, err := d.Exec(`INSERT INTO notes (title) VALUES ('test')`)
	if err != nil {
		t.Fatal(err)
	}
	noteID, _ := res.LastInsertId()

	// Insert a file with original_note_id set
	res, err = d.Exec(`INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes, ciphertext_sha256, aes_key, aes_nonce) VALUES (?, 'key2', 'test.pdf', 'application/pdf', 100, 'sha', X'00', X'00')`, noteID)
	if err != nil {
		t.Fatal(err)
	}
	fileID, _ := res.LastInsertId()

	// Delete the note
	_, err = d.Exec(`DELETE FROM notes WHERE id = ?`, noteID)
	if err != nil {
		t.Fatal(err)
	}

	// File should still exist with original_note_id NULL
	var originalNoteID *int64
	err = d.QueryRow(`SELECT original_note_id FROM files WHERE id = ?`, fileID).Scan(&originalNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if originalNoteID != nil {
		t.Errorf("expected original_note_id to be NULL after note delete, got %v", *originalNoteID)
	}
}
