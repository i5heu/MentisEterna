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

// --- OCR tests ---

func TestFilesOCRSchema(t *testing.T) {
	d := openTestDB(t)

	cols, err := d.tableColumns("files_ocr")
	if err != nil {
		t.Fatalf("tableColumns files_ocr: %v", err)
	}

	required := []string{"file_id", "ocr_text", "model", "created_at", "updated_at", "error"}
	for _, c := range required {
		if !cols[c] {
			t.Errorf("files_ocr missing column: %s", c)
		}
	}
}

func TestFilesOCRForeignKey(t *testing.T) {
	d := openTestDB(t)

	// Inserting OCR for non-existent file should fail (FK constraint)
	_, err := d.Exec(`INSERT INTO files_ocr (file_id, ocr_text, model) VALUES (99999, '', 'test')`)
	if err == nil {
		t.Error("expected foreign key error when file_id doesn't exist")
	}
}

func TestFilesOCRInsertAndQuery(t *testing.T) {
	d := openTestDB(t)

	// Create a file record first
	res, err := d.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (NULL, 'test-key', 'test.png', 'image/png', 100,
		        'aa', 'bb', x'0001', x'0002')
	`)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Insert OCR result
	_, err = d.Exec(`INSERT INTO files_ocr (file_id, ocr_text, model) VALUES (?, ?, ?)`,
		fileID, "Hello World", "glm-ocr:latest")
	if err != nil {
		t.Fatalf("insert ocr: %v", err)
	}

	// Query it back
	var ocrText, model, errorMsg string
	err = d.QueryRow(`SELECT ocr_text, model, COALESCE(error, '') FROM files_ocr WHERE file_id = ?`, fileID).
		Scan(&ocrText, &model, &errorMsg)
	if err != nil {
		t.Fatalf("query ocr: %v", err)
	}
	if ocrText != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", ocrText)
	}
	if model != "glm-ocr:latest" {
		t.Errorf("expected 'glm-ocr:latest', got %q", model)
	}

	// Update OCR result
	_, err = d.Exec(`UPDATE files_ocr SET ocr_text = ?, model = ?, error = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE file_id = ?`,
		"Updated Text", "new-model", fileID)
	if err != nil {
		t.Fatalf("update ocr: %v", err)
	}

	// Verify update
	err = d.QueryRow(`SELECT ocr_text, model FROM files_ocr WHERE file_id = ?`, fileID).
		Scan(&ocrText, &model)
	if err != nil {
		t.Fatalf("query updated ocr: %v", err)
	}
	if ocrText != "Updated Text" {
		t.Errorf("expected 'Updated Text', got %q", ocrText)
	}

	// Delete file should cascade to OCR
	_, err = d.Exec(`DELETE FROM files WHERE id = ?`, fileID)
	if err != nil {
		t.Fatalf("delete file: %v", err)
	}

	// OCR row should be gone
	var count int
	d.QueryRow(`SELECT COUNT(*) FROM files_ocr WHERE file_id = ?`, fileID).Scan(&count)
	if count != 0 {
		t.Error("expected OCR row to be cascade-deleted with file")
	}
}

func TestFilesOCRErrorColumn(t *testing.T) {
	d := openTestDB(t)

	// Create a file record first
	res, err := d.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes,
		                   plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (NULL, 'test-key-err', 'test.jpg', 'image/jpeg', 200,
		        'cc', 'dd', x'0003', x'0004')
	`)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Insert with error
	_, err = d.Exec(`INSERT INTO files_ocr (file_id, ocr_text, model, error) VALUES (?, ?, ?, ?)`,
		fileID, "", "glm-ocr:latest", "OCR failed: timeout")
	if err != nil {
		t.Fatalf("insert ocr with error: %v", err)
	}

	var ocrText, errorMsg string
	d.QueryRow(`SELECT COALESCE(ocr_text, ''), COALESCE(error, '') FROM files_ocr WHERE file_id = ?`, fileID).
		Scan(&ocrText, &errorMsg)
	if ocrText != "" {
		t.Error("expected empty ocr_text on error")
	}
	if errorMsg != "OCR failed: timeout" {
		t.Errorf("expected error message, got %q", errorMsg)
	}
}
