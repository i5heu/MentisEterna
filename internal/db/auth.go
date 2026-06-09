package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// HasAdminPassword reports whether the 'admin' row exists and has a non-empty hash.
func (d *DB) HasAdminPassword() (bool, error) {
	var hash string
	err := d.QueryRow(`SELECT password_hash FROM auth WHERE username = 'admin'`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hash != "", nil
}

// SetAdminPassword stores a modern password hash for 'admin'.
func (d *DB) SetAdminPassword(plaintext string) error {
	hash, err := hashPassword(plaintext)
	if err != nil {
		return err
	}
	_, err = d.Exec(`
		INSERT INTO auth (username, password_hash) VALUES ('admin', ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`,
		hash,
	)
	return err
}

// CheckPassword returns true if plaintext matches the stored password hash for username.
// Legacy SHA-512 hashes are upgraded in-place after a successful login.
func (d *DB) CheckPassword(username, plaintext string) (bool, error) {
	var stored string
	err := d.QueryRow(`SELECT password_hash FROM auth WHERE username = ?`, username).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ok, legacy, err := verifyPasswordHash(stored, plaintext)
	if err != nil {
		return false, err
	}
	if ok && legacy {
		if err := d.rehashPassword(username, plaintext); err != nil {
			return false, err
		}
	}
	return ok, nil
}

func (d *DB) rehashPassword(username, plaintext string) error {
	hash, err := hashPassword(plaintext)
	if err != nil {
		return fmt.Errorf("rehash password: %w", err)
	}
	_, err = d.Exec(`UPDATE auth SET password_hash = ? WHERE username = ?`, hash, username)
	return err
}

// CreateSession generates a secure random token, rotates prior sessions for the
// user, persists the new session, and returns it with its expiry.
func (d *DB) CreateSession(username string) (token string, expiresAt time.Time, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate token: %w", err)
	}
	token = hex.EncodeToString(raw)
	expiresAt = time.Now().UTC().Add(24 * time.Hour)

	tx, err := d.Begin()
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`DELETE FROM sessions WHERE username = ?`, username); err != nil {
		return "", time.Time{}, fmt.Errorf("delete prior sessions: %w", err)
	}
	if _, err = tx.Exec(
		`INSERT INTO sessions (token, username, expires_at) VALUES (?, ?, ?)`,
		token, username, expiresAt.Format(time.RFC3339),
	); err != nil {
		return "", time.Time{}, fmt.Errorf("insert session: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (d *DB) DeleteSession(token string) error {
	res, err := d.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete session rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUserID returns the id for a given username.
func (d *DB) GetUserID(username string) (int64, error) {
	var id int64
	err := d.QueryRow(`SELECT id FROM auth WHERE username = ?`, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// GetUsernameByID returns the username for a given auth.id.
func (d *DB) GetUsernameByID(id int64) (string, error) {
	var username string
	err := d.QueryRow(`SELECT username FROM auth WHERE id = ?`, id).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

// ValidateSession looks up the token and returns the username if it is valid and not expired.
func (d *DB) ValidateSession(token string) (username string, err error) {
	var expiresAt string
	err = d.QueryRow(
		`SELECT username, expires_at FROM sessions WHERE token = ?`, token,
	).Scan(&username, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "", fmt.Errorf("parse expires_at: %w", err)
	}
	if time.Now().UTC().After(exp) {
		// Clean up expired token opportunistically.
		_, _ = d.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return "", ErrNotFound
	}
	return username, nil
}
