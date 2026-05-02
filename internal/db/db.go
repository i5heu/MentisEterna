package db

import (
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("not found")

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	d := &DB{sqlDB}
	if err := d.migrate(); err != nil {
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
		)
	`)
	if err != nil {
		return err
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
