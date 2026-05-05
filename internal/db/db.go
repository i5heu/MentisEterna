package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	gosqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	extPath := os.Getenv("VSS_EXT_PATH")
	if extPath == "" {
		extPath = findExtPath()
	}
	vectorLib := filepath.Join(extPath, "vector0")
	vssLib := filepath.Join(extPath, "vss0")

	sql.Register("sqlite3-vss", &gosqlite3.SQLiteDriver{
		ConnectHook: func(conn *gosqlite3.SQLiteConn) error {
			if err := conn.LoadExtension(vectorLib, "sqlite3_vector_init"); err != nil {
				return fmt.Errorf("load vector0: %w", err)
			}
			if err := conn.LoadExtension(vssLib, "sqlite3_vss_init"); err != nil {
				return fmt.Errorf("load vss0: %w", err)
			}
			return nil
		},
	})
}

// findExtPath locates the directory containing vector0.so and vss0.so.
// It checks VSS_EXT_PATH env, then searches from CWD up to the module root.
func findExtPath() string {
	// Walk up from the current working directory looking for "lib/vector0.so".
	dir, err := os.Getwd()
	if err != nil {
		return "lib"
	}
	for {
		candidate := filepath.Join(dir, "lib", "vector0.so")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Join(dir, "lib")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "lib"
}

var ErrNotFound = errors.New("not found")

type DB struct {
	*sql.DB
	vssAvailable bool
}

func (d *DB) VSSAvailable() bool { return d.vssAvailable }

func Open(path string) (*DB, error) {
	d, err := openWithDriver("sqlite3-vss", path+"?_journal_mode=WAL&_foreign_keys=on", true)
	if err != nil {
		log.Printf("VSS extensions not available, falling back to standard SQLite: %v", err)
		d, err = openWithDriver("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on", false)
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
	d, err := openWithDriver("sqlite3-vss", ":memory:?_foreign_keys=on", true)
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
	return d.migrateNotes()
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
		)
	`)
	if err != nil {
		return err
	}

	// Create the VSS virtual table if extensions are loaded.
	// sqlite-vss requires the embedding dimension, which for Qwen3-Embedding-4B is 2560.
	if d.vssAvailable {
		if err := d.ensureVSSDimension(2560); err != nil {
			return fmt.Errorf("vss_notes: %w", err)
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

	return nil
}

// ensureVSSDimension ensures the vss_notes virtual table uses the expected
// embedding dimension. If the table doesn't exist it creates it. If it exists
// with a different dimension it drops and recreates it (embeddings need to be
// regenerated via backfill). Otherwise it leaves existing embeddings intact.
func (d *DB) ensureVSSDimension(expectedDim int) error {
	// Check whether vss_notes already exists.
	var name string
	err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='vss_notes'`).Scan(&name)
	if err != nil {
		// Table doesn't exist yet — create it.
		if _, createErr := d.Exec(`
			CREATE VIRTUAL TABLE IF NOT EXISTS vss_notes USING vss0(
				body_embedding(` + fmt.Sprintf("%d", expectedDim) + `)
			)
		`); createErr != nil {
			return fmt.Errorf("create vss_notes: %w", createErr)
		}
		return nil
	}

	// Table exists — probe its dimension by inserting a dummy row.
	// Build a JSON array of expectedDim zeros.
	parts := make([]string, expectedDim)
	for i := range parts {
		parts[i] = "0"
	}
	dummyJSON := "[" + strings.Join(parts, ",") + "]"

	// Try inserting a test row. vss0 validates dimension on insert.
	testRowID := int64(-1) // negative rowid to avoid collision with real notes.
	_, insertErr := d.Exec(`INSERT INTO vss_notes(rowid, body_embedding) VALUES (?, ?)`, testRowID, dummyJSON)
	if insertErr != nil {
		// Dimension mismatch detected. Drop and recreate.
		log.Printf("vss_notes dimension mismatch detected, recreating table (run backfill to regenerate embeddings)")
		if _, dropErr := d.Exec(`DROP TABLE IF EXISTS vss_notes`); dropErr != nil {
			return fmt.Errorf("drop old vss_notes: %w", dropErr)
		}
		if _, createErr := d.Exec(`
			CREATE VIRTUAL TABLE vss_notes USING vss0(
				body_embedding(` + fmt.Sprintf("%d", expectedDim) + `)
			)
		`); createErr != nil {
			return fmt.Errorf("recreate vss_notes: %w", createErr)
		}
		return nil
	}

	// Clean up the test row.
	_, _ = d.Exec(`DELETE FROM vss_notes WHERE rowid = ?`, testRowID)
	return nil
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
