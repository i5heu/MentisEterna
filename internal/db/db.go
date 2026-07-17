package db

/*
#cgo linux LDFLAGS: -ldl
#include <dlfcn.h>

static const char* mentis_load_global_libm(void) {
#if defined(__linux__)
	dlerror();
	if (dlopen("libm.so.6", RTLD_NOW | RTLD_GLOBAL) != NULL) {
		return NULL;
	}
	dlerror();
	if (dlopen("libm.so", RTLD_NOW | RTLD_GLOBAL) != NULL) {
		return NULL;
	}
	return dlerror();
#else
	return NULL;
#endif
}
*/
import "C"

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	internaltags "github.com/i5heu/MentisEterna/internal/tags"
	gosqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	extPath := os.Getenv("VEC_EXT_PATH")
	if extPath == "" {
		extPath = os.Getenv("VSS_EXT_PATH")
	}
	if extPath == "" {
		extPath = findExtPath()
	}
	vecLib := filepath.Join(extPath, "vec0")

	sql.Register("sqlite3-vec", &gosqlite3.SQLiteDriver{
		ConnectHook: func(conn *gosqlite3.SQLiteConn) error {
			if err := ensureVecRuntimeDeps(); err != nil {
				return err
			}
			if err := conn.LoadExtension(vecLib, "sqlite3_vec_init"); err != nil {
				return fmt.Errorf("load vec0: %w", err)
			}
			return nil
		},
	})
}

var vecRuntimeDepsOnce sync.Once
var vecRuntimeDepsErr error

func ensureVecRuntimeDeps() error {
	vecRuntimeDepsOnce.Do(func() {
		if msg := C.mentis_load_global_libm(); msg != nil {
			vecRuntimeDepsErr = fmt.Errorf("load libm for vec0: %s", C.GoString(msg))
		}
	})
	return vecRuntimeDepsErr
}

// findExtPath locates the directory containing vec0.
// It checks VEC_EXT_PATH, then legacy VSS_EXT_PATH, then searches from CWD up to the module root.
func findExtPath() string {
	dir, err := os.Getwd()
	if err != nil {
		return "lib"
	}
	for {
		candidateDir := filepath.Join(dir, "lib")
		if hasVecExtension(candidateDir) {
			return candidateDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "lib"
}

func hasVecExtension(dir string) bool {
	for _, name := range []string{"vec0.so", "vec0.dylib", "vec0.dll", "vec0"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

var ErrNotFound = errors.New("not found")

type DB struct {
	*sql.DB
	vssAvailable bool
}

func (d *DB) VSSAvailable() bool { return d.vssAvailable }

func Open(path string) (*DB, error) {
	d, err := openWithDriver("sqlite3-vec", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", true)
	if err != nil {
		log.Printf("sqlite-vec extension not available, falling back to standard SQLite: %v", err)
		d, err = openWithDriver("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", false)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
	}
	return d, nil
}

// OpenInMemory opens a fresh SQLite database entirely in RAM.
// This is significantly faster than opening on-disk databases.
// The connection pool is pinned to 1 so all operations share the same in-memory database.
func OpenInMemory() (*DB, error) {
	d, err := openWithDriver("sqlite3-vec", ":memory:?_foreign_keys=on", true)
	if err != nil {
		d, err = openWithDriver("sqlite3", ":memory:?_foreign_keys=on", false)
		if err != nil {
			return nil, fmt.Errorf("open sqlite in-memory: %w", err)
		}
	}
	d.SetMaxOpenConns(1)
	return d, nil
}

func openWithDriver(driverName, dsn string, useVSS bool) (*DB, error) {
	sqlDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	d := &DB{DB: sqlDB, vssAvailable: useVSS}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *DB) migrate() error {
	if err := d.ensureAuthTables(); err != nil {
		return err
	}
	if err := d.ensureJobTables(); err != nil {
		return err
	}
	if err := d.migrateNotes(); err != nil {
		return err
	}
	if err := d.ensureMediaTables(); err != nil {
		return err
	}
	if err := d.ensureUploadSessions(); err != nil {
		return err
	}
	if err := d.ensureOCRTables(); err != nil {
		return err
	}
	if err := d.ensureSTTTables(); err != nil {
		return err
	}
	if err := d.migrateTags(); err != nil {
		return err
	}
	return d.ensureNotesFTS()
}

func (d *DB) ensureAuthTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS auth (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT    NOT NULL UNIQUE,
			password_hash TEXT    NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT     NOT NULL PRIMARY KEY,
			username   TEXT     NOT NULL,
			expires_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS webauthn_credentials (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id           INTEGER NOT NULL,
			credential_id     BLOB    NOT NULL UNIQUE,
			public_key        BLOB    NOT NULL,
			attestation_type  TEXT    NOT NULL DEFAULT '',
			attestation_format TEXT   NOT NULL DEFAULT '',
			flags             INTEGER NOT NULL DEFAULT 0,
			sign_count        INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(user_id) REFERENCES auth(id) ON DELETE CASCADE
		);
	`)

	// Migrate existing webauthn_credentials tables that may lack the new columns.
	cols, cerr := d.tableColumns("webauthn_credentials")
	if cerr == nil {
		if !cols["flags"] {
			_, _ = d.Exec(`ALTER TABLE webauthn_credentials ADD COLUMN flags INTEGER NOT NULL DEFAULT 0`)
		}
		if !cols["attestation_format"] {
			_, _ = d.Exec(`ALTER TABLE webauthn_credentials ADD COLUMN attestation_format TEXT NOT NULL DEFAULT ''`)
		}
	}

	return err
}

func (d *DB) ensureJobTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS job_definitions (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			plugin_id   TEXT    NOT NULL,
			name        TEXT    NOT NULL,
			schedule    TEXT    NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			UNIQUE(plugin_id, name)
		);

		CREATE TABLE IF NOT EXISTS job_runs (
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

		CREATE INDEX IF NOT EXISTS idx_job_runs_status ON job_runs(status);
		CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id);
		CREATE INDEX IF NOT EXISTS idx_job_runs_created ON job_runs(created_at);
	`)
	return err
}

func (d *DB) migrateNotes() error {
	cols, err := d.tableColumns("notes")
	if err != nil {
		return err
	}

	if len(cols) == 0 {
		_, err = d.Exec(`
			CREATE TABLE notes (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				title      TEXT    NOT NULL,
				parent_id  INTEGER REFERENCES notes(id) ON DELETE SET NULL,
				created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			)
		`)
		if err != nil {
			return err
		}
	}

	_, err = d.Exec(`
		CREATE TABLE IF NOT EXISTS updates (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id    INTEGER NOT NULL,
			body       TEXT    NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS note_search_chunks (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id    INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			field      TEXT    NOT NULL,
			ordinal    INTEGER NOT NULL DEFAULT 0,
			content    TEXT    NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);

		CREATE INDEX IF NOT EXISTS idx_note_search_chunks_note_id ON note_search_chunks(note_id);
		CREATE INDEX IF NOT EXISTS idx_note_search_chunks_field ON note_search_chunks(field);
	`)
	if err != nil {
		return err
	}

	// Create the vector-search virtual tables if sqlite-vec is available.
	// The embedding dimension for the current embedding model is 2560.
	if d.vssAvailable {
		if err := d.ensureVSSDimension(2560); err != nil {
			return fmt.Errorf("vss_notes: %w", err)
		}
		if err := d.ensureVSSNoteSearchDimension(2560); err != nil {
			return fmt.Errorf("vss_note_search: %w", err)
		}
		if err := d.ensureVSSFilesOCR(2560); err != nil {
			return fmt.Errorf("vss_files_ocr: %w", err)
		}
		if err := d.ensureVSSFilesSTT(2560); err != nil {
			return fmt.Errorf("vss_files_stt: %w", err)
		}
	}

	// Re-read columns in case table was just created above
	cols, err = d.tableColumns("notes")
	if err != nil {
		return err
	}

	if cols["body"] {
		if _, err = d.Exec(`
			INSERT INTO updates (note_id, body, created_at)
			SELECT id, body, created_at FROM notes WHERE body != ''
		`); err != nil {
			return err
		}
		if _, err = d.Exec(`ALTER TABLE notes DROP COLUMN body`); err != nil {
			return err
		}
	}

	if cols["updated_at"] {
		if _, err = d.Exec(`ALTER TABLE notes DROP COLUMN updated_at`); err != nil {
			return err
		}
	}

	if !cols["parent_id"] {
		if _, err = d.Exec(`ALTER TABLE notes ADD COLUMN parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL`); err != nil {
			return err
		}
	}

	if !cols["type"] {
		if _, err = d.Exec(`ALTER TABLE notes ADD COLUMN type TEXT NOT NULL DEFAULT 'standard'`); err != nil {
			return err
		}
	}

	if !cols["pinned"] {
		if _, err = d.Exec(`ALTER TABLE notes ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	return nil
}

func (d *DB) ensureMediaTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			id                     INTEGER PRIMARY KEY AUTOINCREMENT,
			original_note_id       INTEGER REFERENCES notes(id) ON DELETE SET NULL,
			pending_inline_note_id INTEGER REFERENCES notes(id) ON DELETE CASCADE,
			pending_inline_at      DATETIME,
			storage_key            TEXT    NOT NULL UNIQUE,
			filename               TEXT    NOT NULL,
			mime_type              TEXT    NOT NULL,
			size_bytes             INTEGER NOT NULL,
			plaintext_sha256       TEXT,
			ciphertext_sha256      TEXT    NOT NULL,
			aes_key                BLOB    NOT NULL,
			aes_nonce              BLOB    NOT NULL,
			created_at             DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			deleted_at             DATETIME
		);

		CREATE TABLE IF NOT EXISTS file_s3 (
			file_id           INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			endpoint_id       TEXT    NOT NULL,
			state             TEXT    NOT NULL,
			remote_key        TEXT    NOT NULL,
			etag              TEXT,
			ciphertext_size   INTEGER,
			last_error        TEXT,
			retry_count       INTEGER NOT NULL DEFAULT 0,
			last_attempt_at   DATETIME,
			last_success_at   DATETIME,
			next_retry_at     DATETIME,
			updated_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			PRIMARY KEY (file_id, endpoint_id)
		);

		CREATE TABLE IF NOT EXISTS files_refs (
			note_id      INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			ref_kind     TEXT    NOT NULL,
			created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			PRIMARY KEY (note_id, file_id, ref_kind)
		);

		CREATE INDEX IF NOT EXISTS idx_files_pending_inline_note_id ON files(pending_inline_note_id);
		CREATE INDEX IF NOT EXISTS idx_files_pending_inline_at ON files(pending_inline_at);
		CREATE INDEX IF NOT EXISTS idx_files_deleted_at ON files(deleted_at);
		CREATE INDEX IF NOT EXISTS idx_file_s3_next_retry ON file_s3(state, next_retry_at);
		CREATE INDEX IF NOT EXISTS idx_files_refs_note_id ON files_refs(note_id);
		CREATE INDEX IF NOT EXISTS idx_files_refs_file_id ON files_refs(file_id);
	`)
	return err
}

func (d *DB) migrateTags() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS tags (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT    NOT NULL UNIQUE
		);

		CREATE TABLE IF NOT EXISTS tags_refs (
			note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			PRIMARY KEY (note_id, tag_id)
		);

		CREATE TABLE IF NOT EXISTS auto_tags_refs (
			note_id    INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			PRIMARY KEY (note_id, tag_id)
		);

		CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
		CREATE INDEX IF NOT EXISTS idx_tags_refs_note_id ON tags_refs(note_id);
		CREATE INDEX IF NOT EXISTS idx_tags_refs_tag_id ON tags_refs(tag_id);
		CREATE INDEX IF NOT EXISTS idx_auto_tags_refs_note_id ON auto_tags_refs(note_id);
		CREATE INDEX IF NOT EXISTS idx_auto_tags_refs_tag_id ON auto_tags_refs(tag_id);
	`)
	if err != nil {
		return err
	}
	return d.normalizeTagNames()
}

func (d *DB) normalizeTagNames() error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type row struct {
		id   int64
		name string
	}
	rows, err := tx.Query(`SELECT id, name FROM tags ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	groups := make(map[string][]row)
	var orderedNames []string
	var blankIDs []int64
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			return err
		}
		normalized := internaltags.NormalizeName(r.name)
		if normalized == "" {
			blankIDs = append(blankIDs, r.id)
			continue
		}
		if _, exists := groups[normalized]; !exists {
			orderedNames = append(orderedNames, normalized)
		}
		groups[normalized] = append(groups[normalized], r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, id := range blankIDs {
		if err := reassignMergedTagRefs(tx, "tags_refs", id, 0); err != nil {
			return err
		}
		if err := reassignMergedTagRefs(tx, "auto_tags_refs", id, 0); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id = ?`, id); err != nil {
			return err
		}
	}

	for _, normalized := range orderedNames {
		group := groups[normalized]
		canonical := group[0]
		for _, dup := range group[1:] {
			if err := reassignMergedTagRefs(tx, "tags_refs", dup.id, canonical.id); err != nil {
				return err
			}
			if err := reassignMergedTagRefs(tx, "auto_tags_refs", dup.id, canonical.id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM tags WHERE id = ?`, dup.id); err != nil {
				return err
			}
		}
		if canonical.name != normalized {
			if _, err := tx.Exec(`UPDATE tags SET name = ? WHERE id = ?`, normalized, canonical.id); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func reassignMergedTagRefs(tx *sql.Tx, table string, fromTagID, toTagID int64) error {
	if fromTagID <= 0 {
		return nil
	}
	if toTagID > 0 && toTagID != fromTagID {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO `+table+` (note_id, tag_id) SELECT note_id, ? FROM `+table+` WHERE tag_id = ?`,
			toTagID,
			fromTagID,
		); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`DELETE FROM `+table+` WHERE tag_id = ?`, fromTagID)
	return err
}

// ensureNotesFTS creates the FTS4 full-text index over note titles and tag
// names. The index is kept in sync with the `notes` and `tags_refs` tables
// via triggers so that title edits, tag attachments/detachments, and tag
// renames propagate automatically.
//
// FTS4 does not support true fuzzy matching, but the queries constructed in
// the search package combine prefix tokens (`word*`), OR'd phrase queries, and
// post-filter ranking with the existing subsequence scorer to produce robust
// results that tolerate typos, partial words, and multi-word queries.
func (d *DB) ensureNotesFTS() error {
	// Create the FTS4 table if missing. We use `IF NOT EXISTS` so existing
	// databases are upgraded in place without reindexing on every startup.
	if _, err := d.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts4(
			note_id UNINDEXED,
			title,
			tags,
			tokenize = unicode61
		);
	`); err != nil {
		return fmt.Errorf("create notes_fts: %w", err)
	}

	// Backfill any notes that are missing from the index (e.g. legacy DBs
	// upgraded after the FTS table was introduced, or rows that slipped past
	// a trigger due to a schema migration gap).
	if _, err := d.Exec(`
		INSERT INTO notes_fts(note_id, title, tags)
		SELECT n.id,
		       COALESCE(n.title, ''),
		       COALESCE((
		         SELECT GROUP_CONCAT(tag_name, ' ')
		         FROM (
		           SELECT DISTINCT t.name AS tag_name
		           FROM tags_refs tr
		           JOIN tags t ON t.id = tr.tag_id
		           WHERE tr.note_id = n.id
		           UNION
		           SELECT DISTINCT t.name AS tag_name
		           FROM auto_tags_refs atr
		           JOIN tags t ON t.id = atr.tag_id
		           WHERE atr.note_id = n.id
		           ORDER BY tag_name
		         ) all_tags
		       ), '')
		FROM notes n
		WHERE NOT EXISTS (SELECT 1 FROM notes_fts f WHERE f.note_id = n.id);
	`); err != nil {
		return fmt.Errorf("backfill notes_fts: %w", err)
	}

	// Triggers keep the FTS index in sync with the source tables.
	// Notes: title changes.
	for _, stmt := range []string{
		`CREATE TRIGGER IF NOT EXISTS notes_fts_ai AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(note_id, title, tags) VALUES (new.id, COALESCE(new.title, ''), '');
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_ad AFTER DELETE ON notes BEGIN
			DELETE FROM notes_fts WHERE note_id = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_au AFTER UPDATE OF title ON notes BEGIN
			UPDATE notes_fts SET title = COALESCE(new.title, '') WHERE note_id = new.id;
		END`,
		// Tags: when a manual or auto-tag is attached/detached we rebuild the tags
		// column for the affected note(s) so the FTS row reflects the current set.
		`CREATE TRIGGER IF NOT EXISTS notes_fts_tags_ai AFTER INSERT ON tags_refs BEGIN
			UPDATE notes_fts
			SET tags = COALESCE((
			  SELECT GROUP_CONCAT(tag_name, ' ')
			  FROM (
			    SELECT DISTINCT t.name AS tag_name
			    FROM tags_refs tr
			    JOIN tags t ON t.id = tr.tag_id
			    WHERE tr.note_id = new.note_id
			    UNION
			    SELECT DISTINCT t.name AS tag_name
			    FROM auto_tags_refs atr
			    JOIN tags t ON t.id = atr.tag_id
			    WHERE atr.note_id = new.note_id
			    ORDER BY tag_name
			  ) all_tags
			), '')
			WHERE note_id = new.note_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_tags_ad AFTER DELETE ON tags_refs BEGIN
			UPDATE notes_fts
			SET tags = COALESCE((
			  SELECT GROUP_CONCAT(tag_name, ' ')
			  FROM (
			    SELECT DISTINCT t.name AS tag_name
			    FROM tags_refs tr
			    JOIN tags t ON t.id = tr.tag_id
			    WHERE tr.note_id = old.note_id
			    UNION
			    SELECT DISTINCT t.name AS tag_name
			    FROM auto_tags_refs atr
			    JOIN tags t ON t.id = atr.tag_id
			    WHERE atr.note_id = old.note_id
			    ORDER BY tag_name
			  ) all_tags
			), '')
			WHERE note_id = old.note_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_auto_tags_ai AFTER INSERT ON auto_tags_refs BEGIN
			UPDATE notes_fts
			SET tags = COALESCE((
			  SELECT GROUP_CONCAT(tag_name, ' ')
			  FROM (
			    SELECT DISTINCT t.name AS tag_name
			    FROM tags_refs tr
			    JOIN tags t ON t.id = tr.tag_id
			    WHERE tr.note_id = new.note_id
			    UNION
			    SELECT DISTINCT t.name AS tag_name
			    FROM auto_tags_refs atr
			    JOIN tags t ON t.id = atr.tag_id
			    WHERE atr.note_id = new.note_id
			    ORDER BY tag_name
			  ) all_tags
			), '')
			WHERE note_id = new.note_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_fts_auto_tags_ad AFTER DELETE ON auto_tags_refs BEGIN
			UPDATE notes_fts
			SET tags = COALESCE((
			  SELECT GROUP_CONCAT(tag_name, ' ')
			  FROM (
			    SELECT DISTINCT t.name AS tag_name
			    FROM tags_refs tr
			    JOIN tags t ON t.id = tr.tag_id
			    WHERE tr.note_id = old.note_id
			    UNION
			    SELECT DISTINCT t.name AS tag_name
			    FROM auto_tags_refs atr
			    JOIN tags t ON t.id = atr.tag_id
			    WHERE atr.note_id = old.note_id
			    ORDER BY tag_name
			  ) all_tags
			), '')
			WHERE note_id = old.note_id;
		END`,
		// If a tag is renamed, refresh every note that uses it.
		`CREATE TRIGGER IF NOT EXISTS notes_fts_tags_au AFTER UPDATE OF name ON tags BEGIN
			UPDATE notes_fts
			SET tags = COALESCE((
			  SELECT GROUP_CONCAT(tag_name, ' ')
			  FROM (
			    SELECT DISTINCT t2.name AS tag_name
			    FROM tags_refs tr2
			    JOIN tags t2 ON t2.id = tr2.tag_id
			    WHERE tr2.note_id = refs.note_id
			    UNION
			    SELECT DISTINCT t2.name AS tag_name
			    FROM auto_tags_refs atr2
			    JOIN tags t2 ON t2.id = atr2.tag_id
			    WHERE atr2.note_id = refs.note_id
			    ORDER BY tag_name
			  ) all_tags
			), '')
			FROM (
			  SELECT note_id FROM tags_refs WHERE tag_id = new.id
			  UNION
			  SELECT note_id FROM auto_tags_refs WHERE tag_id = new.id
			) refs
			WHERE notes_fts.note_id = refs.note_id;
		END`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("create notes_fts trigger: %w", err)
		}
	}
	return nil
}

func (d *DB) ensureUploadSessions() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS upload_sessions (
			upload_id   TEXT PRIMARY KEY,
			note_id     INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			filename    TEXT NOT NULL,
			mime_type   TEXT NOT NULL DEFAULT '',
			total_size  INTEGER NOT NULL,
			chunk_size  INTEGER NOT NULL,
			total_chunks INTEGER NOT NULL,
			file_sha256 TEXT,
			inline      INTEGER NOT NULL DEFAULT 0,
			chunks_done TEXT NOT NULL DEFAULT '[]',
			status      TEXT NOT NULL DEFAULT 'uploading',
			finish_result TEXT DEFAULT NULL,
			placeholder_token TEXT DEFAULT NULL,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			expires_at  DATETIME NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	// Migration: add columns that may not exist in older DBs.
	cols, err := d.tableColumns("upload_sessions")
	if err != nil {
		return err
	}
	if !cols["status"] {
		_, err = d.Exec(`ALTER TABLE upload_sessions ADD COLUMN status TEXT NOT NULL DEFAULT 'uploading'`)
		if err != nil {
			return err
		}
	}
	if !cols["finish_result"] {
		_, err = d.Exec(`ALTER TABLE upload_sessions ADD COLUMN finish_result TEXT DEFAULT NULL`)
		if err != nil {
			return err
		}
	}
	if !cols["placeholder_token"] {
		_, err = d.Exec(`ALTER TABLE upload_sessions ADD COLUMN placeholder_token TEXT DEFAULT NULL`)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) ensureOCRTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS files_ocr (
			file_id    INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
			ocr_text   TEXT    NOT NULL DEFAULT '',
			model      TEXT    NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			error      TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_files_ocr_file_id ON files_ocr(file_id);
	`)
	return err
}

// ensureVSSDimension ensures the vss_notes virtual table uses the expected
// sqlite-vec schema and embedding dimension. If the table is still using the
// legacy sqlite-vss module or an incompatible dimension/metric, it is dropped
// and recreated (embeddings then need to be regenerated via backfill).
func (d *DB) ensureVSSDimension(expectedDim int) error {
	return d.ensureVecTable("vss_notes", "body_embedding", expectedDim)
}

// ensureVSSNoteSearchDimension ensures the vss_note_search virtual table exists
// with the expected sqlite-vec schema and embedding dimension. rowid =
// note_search_chunks.id for paragraph/title/path/tag embeddings.
func (d *DB) ensureVSSNoteSearchDimension(expectedDim int) error {
	return d.ensureVecTable("vss_note_search", "embedding", expectedDim)
}

// ensureVSSFilesOCR ensures the vss_files_ocr virtual table exists with the
// expected sqlite-vec schema and embedding dimension. rowid = files.id for
// direct file lookup.
func (d *DB) ensureVSSFilesOCR(expectedDim int) error {
	return d.ensureVecTable("vss_files_ocr", "ocr_embedding", expectedDim)
}

func (d *DB) tableColumns(table string) (map[string]bool, error) {
	rows, err := d.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, nil
}

// ensureSTTTables ensures the files_stt table exists.
func (d *DB) ensureSTTTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS files_stt (
			file_id    INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
			stt_text   TEXT    NOT NULL DEFAULT '',
			model      TEXT    NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			error      TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_files_stt_file_id ON files_stt(file_id);
	`)
	return err
}

// ensureVSSFilesSTT ensures the vss_files_stt virtual table exists with the
// expected sqlite-vec schema and embedding dimension. rowid = files.id for
// direct file lookup.
func (d *DB) ensureVSSFilesSTT(expectedDim int) error {
	return d.ensureVecTable("vss_files_stt", "stt_embedding", expectedDim)
}

func (d *DB) ensureVecTable(tableName, columnName string, expectedDim int) error {
	var tableSQL sql.NullString
	err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name = ?`, tableName).Scan(&tableSQL)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return d.createVecTable(tableName, columnName, expectedDim)
	case err != nil:
		return fmt.Errorf("inspect %s: %w", tableName, err)
	}

	if needsVecTableRecreate(tableSQL.String, columnName, expectedDim) {
		log.Printf("%s uses legacy or incompatible vector schema; recreating table", tableName)
		return d.recreateVecTable(tableName, columnName, expectedDim)
	}

	testRowID := int64(-1)
	_, _ = d.Exec(`DELETE FROM `+tableName+` WHERE rowid = ?`, testRowID)
	if _, err := d.Exec(
		`INSERT INTO `+tableName+`(rowid, `+columnName+`) VALUES (?, ?)`,
		testRowID,
		zeroVectorJSON(expectedDim),
	); err != nil {
		log.Printf("%s rejected the expected embedding shape; recreating table", tableName)
		return d.recreateVecTable(tableName, columnName, expectedDim)
	}
	_, _ = d.Exec(`DELETE FROM `+tableName+` WHERE rowid = ?`, testRowID)
	return nil
}

func (d *DB) recreateVecTable(tableName, columnName string, expectedDim int) error {
	if _, err := d.Exec(`DROP TABLE IF EXISTS ` + tableName); err != nil {
		return fmt.Errorf("drop %s: %w", tableName, err)
	}
	return d.createVecTable(tableName, columnName, expectedDim)
}

func (d *DB) createVecTable(tableName, columnName string, expectedDim int) error {
	if _, err := d.Exec(`
		CREATE VIRTUAL TABLE ` + tableName + ` USING vec0(
			` + columnName + ` float[` + fmt.Sprintf("%d", expectedDim) + `] distance_metric=cosine
		)
	`); err != nil {
		return fmt.Errorf("create %s: %w", tableName, err)
	}
	return nil
}

func needsVecTableRecreate(tableSQL, columnName string, expectedDim int) bool {
	normalized := normalizeSQL(tableSQL)
	expectedColumn := strings.ToLower(columnName) + ` float[` + fmt.Sprintf("%d", expectedDim) + `]`
	return normalized == "" ||
		!strings.Contains(normalized, `using vec0`) ||
		!strings.Contains(normalized, expectedColumn) ||
		!strings.Contains(normalized, `distance_metric=cosine`)
}

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func zeroVectorJSON(dim int) string {
	parts := make([]string, dim)
	for i := range parts {
		parts[i] = "0"
	}
	return "[" + strings.Join(parts, ",") + "]"
}
