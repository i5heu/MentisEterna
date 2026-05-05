package db

import (
	"crypto/rand"
	"crypto/sha512"
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

// SetAdminPassword stores the SHA-512 hex digest of plaintext for 'admin'.
func (d *DB) SetAdminPassword(plaintext string) error {
	sum := sha512.Sum512([]byte(plaintext))
	hash := fmt.Sprintf("%x", sum)
	_, err := d.Exec(`
		INSERT INTO auth (username, password_hash) VALUES ('admin', ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`,
		hash,
	)
	return err
}

// CheckPassword returns true if plaintext matches the stored SHA-512 hash for username.
func (d *DB) CheckPassword(username, plaintext string) (bool, error) {
	var stored string
	err := d.QueryRow(`SELECT password_hash FROM auth WHERE username = ?`, username).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sum := sha512.Sum512([]byte(plaintext))
	return fmt.Sprintf("%x", sum) == stored, nil
}

// CreateSession generates a secure random token, persists it, and returns it with its expiry.
func (d *DB) CreateSession(username string) (token string, expiresAt time.Time, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate token: %w", err)
	}
	token = hex.EncodeToString(raw)
	expiresAt = time.Now().UTC().Add(24 * time.Hour)

	_, err = d.Exec(
		`INSERT INTO sessions (token, username, expires_at) VALUES (?, ?, ?)`,
		token, username, expiresAt.Format(time.RFC3339),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("insert session: %w", err)
	}
	return token, expiresAt, nil
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
