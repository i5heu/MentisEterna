package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	// Use standard sqlite3 for the pre-migration fixture (no VSS needed).
	_ "github.com/mattn/go-sqlite3"
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

// TestJobDedupeMigration verifies that opening a pre-migration database
// collapses duplicate active job runs (same job_id + payload) to the oldest
// row and installs the unique partial index.
func TestJobDedupeMigration(t *testing.T) {
	// Build a pre-migration database with duplicate active runs (old schema,
	// no unique index).
	prePath := filepath.Join(t.TempDir(), "pre.db")
	pre, err := sql.Open("sqlite3", prePath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open pre db: %v", err)
	}
	oldSchema := `
		CREATE TABLE job_definitions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			plugin_id   TEXT    NOT NULL,
			name        TEXT    NOT NULL,
			schedule    TEXT    NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(plugin_id, name)
		);
		CREATE TABLE job_runs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id      INTEGER NOT NULL REFERENCES job_definitions(id) ON DELETE CASCADE,
			status      TEXT    NOT NULL DEFAULT 'planned',
			payload     TEXT,
			started_at  DATETIME,
			finished_at DATETIME,
			error       TEXT,
			result      TEXT,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
	`
	if _, err := pre.Exec(oldSchema); err != nil {
		pre.Close()
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := pre.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('test', 'dedupe_job', '')`); err != nil {
		pre.Close()
		t.Fatalf("insert definition: %v", err)
	}
	// 3 planned rows with payload A (ids 1-3), 2 running rows with payload B
	// (ids 4-5), 1 planned NULL-payload row (id 6), 1 done row with payload A
	// (id 7).
	for i := range 3 {
		if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status, payload) VALUES (1, 'planned', 'A')`); err != nil {
			pre.Close()
			t.Fatalf("insert planned A %d: %v", i, err)
		}
	}
	for i := range 2 {
		if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status, payload) VALUES (1, 'running', 'B')`); err != nil {
			pre.Close()
			t.Fatalf("insert running B %d: %v", i, err)
		}
	}
	if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status) VALUES (1, 'planned')`); err != nil {
		pre.Close()
		t.Fatalf("insert NULL-payload row: %v", err)
	}
	if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status, payload) VALUES (1, 'done', 'A')`); err != nil {
		pre.Close()
		t.Fatalf("insert done row: %v", err)
	}
	pre.Close()

	// db.Open runs the migration (dedupe cleanup + unique index) on open.
	d, err := Open(prePath)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	defer d.Close()

	// Payload A: exactly one active row, the MIN(id) of the originals (id 1).
	var count, id int64
	if err := d.QueryRow(`SELECT COUNT(*), MIN(id) FROM job_runs WHERE status IN ('planned','running') AND payload = 'A'`).Scan(&count, &id); err != nil {
		t.Fatalf("query A: %v", err)
	}
	if count != 1 || id != 1 {
		t.Errorf("payload A: expected 1 active row with id 1, got count=%d id=%d", count, id)
	}
	// Payload B: exactly one active row, the MIN(id) of the originals (id 4).
	if err := d.QueryRow(`SELECT COUNT(*), MIN(id) FROM job_runs WHERE status IN ('planned','running') AND payload = 'B'`).Scan(&count, &id); err != nil {
		t.Fatalf("query B: %v", err)
	}
	if count != 1 || id != 4 {
		t.Errorf("payload B: expected 1 active row with id 4, got count=%d id=%d", count, id)
	}
	// NULL-payload planned row survives untouched.
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE status = 'planned' AND payload IS NULL`).Scan(&count); err != nil {
		t.Fatalf("query NULL: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 NULL-payload planned row, got %d", count)
	}
	// Done row with payload A survives (outside the index scope).
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE status = 'done' AND payload = 'A'`).Scan(&count); err != nil {
		t.Fatalf("query done: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 done row with payload A, got %d", count)
	}
	// Unique partial index exists.
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_job_runs_active_unique'`).Scan(&count); err != nil {
		t.Fatalf("query index: %v", err)
	}
	if count != 1 {
		t.Errorf("expected unique index idx_job_runs_active_unique, got %d", count)
	}
}

// TestJobSystemV2Migration verifies that a pre-v2 job system (no
// priority/max_parallel/timeout/max_retries on definitions, no
// progress/scheduling/duration columns on runs, no stats ledger) is
// migrated on open: columns are added, the job_definition_stats table
// is created and backfilled from existing run history exactly once.
func TestJobSystemV2Migration(t *testing.T) {
	prePath := filepath.Join(t.TempDir(), "pre-v2.db")
	pre, err := sql.Open("sqlite3", prePath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open pre db: %v", err)
	}
	// v1 schema: legacy definitions and runs tables.
	if _, err := pre.Exec(`
		CREATE TABLE job_definitions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			plugin_id   TEXT    NOT NULL,
			name        TEXT    NOT NULL,
			schedule    TEXT    NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(plugin_id, name)
		);
		CREATE TABLE job_runs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id      INTEGER NOT NULL REFERENCES job_definitions(id) ON DELETE CASCADE,
			status      TEXT    NOT NULL DEFAULT 'planned',
			payload     TEXT,
			retry_count INTEGER NOT NULL DEFAULT 0,
			started_at  DATETIME,
			finished_at DATETIME,
			error       TEXT,
			result      TEXT,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
	`); err != nil {
		pre.Close()
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := pre.Exec(`INSERT INTO job_definitions (plugin_id, name, schedule) VALUES ('test', 'job', '@daily')`); err != nil {
		pre.Close()
		t.Fatalf("insert definition: %v", err)
	}
	// History: 2 done, 1 errored, 1 planned.
	for range 2 {
		if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status, result) VALUES (1, 'done', 'ok')`); err != nil {
			pre.Close()
			t.Fatalf("insert done: %v", err)
		}
	}
	if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status, error) VALUES (1, 'errored', 'boom')`); err != nil {
		pre.Close()
		t.Fatalf("insert errored: %v", err)
	}
	if _, err := pre.Exec(`INSERT INTO job_runs (job_id, status) VALUES (1, 'planned')`); err != nil {
		pre.Close()
		t.Fatalf("insert planned: %v", err)
	}
	pre.Close()

	// Open runs the v2 migration.
	d, err := Open(prePath)
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	defer d.Close()

	// New run columns exist and default correctly.
	var prio int
	var nextRun, dur, progDone, progTotal, worker sql.NullInt64
	if err := d.QueryRow(`SELECT priority, next_run_at, duration_ms, progress_done, progress_total, worker_id FROM job_runs WHERE status = 'planned'`).
		Scan(&prio, &nextRun, &dur, &progDone, &progTotal, &worker); err != nil {
		t.Fatalf("scan v2 run row: %v", err)
	}
	if prio != 0 || nextRun.Valid || dur.Valid || worker.Valid {
		t.Fatalf("unexpected v2 defaults: prio=%d next=%v dur=%v worker=%v", prio, nextRun, dur, worker)
	}
	// New definition columns exist and default correctly.
	var defPrio, defParallel, defTimeout, defRetries int
	if err := d.QueryRow(`SELECT priority, max_parallel, timeout_seconds, max_retries FROM job_definitions WHERE id = 1`).
		Scan(&defPrio, &defParallel, &defTimeout, &defRetries); err != nil {
		t.Fatalf("scan v2 definition row: %v", err)
	}
	if defPrio != 0 || defParallel < 1 || defTimeout != 0 || defRetries != 0 {
		t.Fatalf("unexpected v2 defaults: prio=%d par=%d timeout=%d retries=%d", defPrio, defParallel, defTimeout, defRetries)
	}

	// Ledger backfilled from existing history: 4 total, 2 done, 1 errored,
	// last status from the most recent run, NULL durations (v1 rows had
	// none).
	var runsTotal, runsDone, runsErrored int
	var lastStatus string
	var totalDur sql.NullInt64
	if err := d.QueryRow(`SELECT runs_total, runs_done, runs_errored, last_status, total_duration_ms FROM job_definition_stats WHERE job_id = 1`).
		Scan(&runsTotal, &runsDone, &runsErrored, &lastStatus, &totalDur); err != nil {
		t.Fatalf("scan stats ledger: %v", err)
	}
	if runsTotal != 4 || runsDone != 2 || runsErrored != 1 {
		t.Fatalf("ledger counts = %d/%d/%d", runsTotal, runsDone, runsErrored)
	}
	if totalDur.Valid && totalDur.Int64 != 0 {
		t.Fatalf("v1 rows have no durations; total_duration_ms = %v", totalDur)
	}
	// The "last" run is the most recent TERMINAL one (the errored row,
	// id 3) — never a planned one.
	if lastStatus != "errored" {
		t.Fatalf("last_status = %q, want %q", lastStatus, "errored")
	}

	// Reopening must not double-count the backfill.
	d.Close()
	d2, err := Open(prePath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	if err := d2.QueryRow(`SELECT runs_total, runs_done, runs_errored FROM job_definition_stats WHERE job_id = 1`).
		Scan(&runsTotal, &runsDone, &runsErrored); err != nil {
		t.Fatalf("scan ledger on reopen: %v", err)
	}
	if runsTotal != 4 || runsDone != 2 || runsErrored != 1 {
		t.Fatalf("backfill not idempotent: %d/%d/%d", runsTotal, runsDone, runsErrored)
	}
}
