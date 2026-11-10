package db

import (
	"database/sql"
	"errors"
)

// ensureSecretsTable creates the generic key/value secret store used for
// machine-generated credentials (e.g. the auto-generated MCP tunnel token).
func (d *DB) ensureSecretsTable() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS secrets (
			key        TEXT NOT NULL PRIMARY KEY,
			value      TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
	`)
	return err
}

// GetSecret returns the stored value for key, or "" when the key is absent.
func (d *DB) GetSecret(key string) (string, error) {
	var value string
	err := d.QueryRow(`SELECT value FROM secrets WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSecret replaces the value for key.
func (d *DB) SetSecret(key, value string) error {
	_, err := d.Exec(`
		INSERT INTO secrets (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// SetSecretIfAbsent stores value under key only when no value exists yet. It
// returns created=false when the key is already present (and leaves the
// stored value untouched), so concurrent initializations converge on the
// first write.
func (d *DB) SetSecretIfAbsent(key, value string) (bool, error) {
	res, err := d.Exec(`INSERT OR IGNORE INTO secrets (key, value) VALUES (?, ?)`, key, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
