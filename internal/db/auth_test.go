package db

import (
	"crypto/sha512"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHasAdminPasswordEmpty(t *testing.T) {
	d := openTestDB(t)
	has, err := d.HasAdminPassword()
	if err != nil {
		t.Fatalf("HasAdminPassword: %v", err)
	}
	if has {
		t.Error("expected no admin password in empty DB")
	}
}

func TestSetAdminPassword(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAdminPassword("secret"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
	has, err := d.HasAdminPassword()
	if err != nil {
		t.Fatalf("HasAdminPassword: %v", err)
	}
	if !has {
		t.Error("expected admin password to exist after SetAdminPassword")
	}

	var stored string
	if err := d.QueryRow(`SELECT password_hash FROM auth WHERE username = 'admin'`).Scan(&stored); err != nil {
		t.Fatalf("read stored hash: %v", err)
	}
	if !strings.HasPrefix(stored, "$argon2id$") {
		t.Fatalf("expected argon2id hash, got %q", stored)
	}
}

func TestSetAdminPasswordOverwrite(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAdminPassword("first"); err != nil {
		t.Fatalf("SetAdminPassword first: %v", err)
	}
	if err := d.SetAdminPassword("second"); err != nil {
		t.Fatalf("SetAdminPassword second: %v", err)
	}
	ok, err := d.CheckPassword("admin", "second")
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !ok {
		t.Error("expected updated password to match")
	}
}

func TestCheckPasswordCorrect(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAdminPassword("mypass"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
	ok, err := d.CheckPassword("admin", "mypass")
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !ok {
		t.Error("expected correct password to match")
	}
}

func TestCheckPasswordWrong(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAdminPassword("mypass"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
	ok, err := d.CheckPassword("admin", "wrongpass")
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if ok {
		t.Error("expected wrong password to not match")
	}
}

func TestCheckPasswordUnknownUser(t *testing.T) {
	d := openTestDB(t)
	ok, err := d.CheckPassword("nobody", "pass")
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if ok {
		t.Error("expected false for unknown user")
	}
}

func TestCreateSession(t *testing.T) {
	d := openTestDB(t)
	token, expiresAt, err := d.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Error("expected future expiry")
	}
}

func TestCreateSessionUnique(t *testing.T) {
	d := openTestDB(t)
	t1, _, err := d.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	t2, _, err := d.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if t1 == t2 {
		t.Error("expected unique tokens for consecutive sessions")
	}

	if _, err := d.ValidateSession(t1); err != ErrNotFound {
		t.Fatalf("expected prior session to be revoked, got %v", err)
	}
}

func TestValidateSessionValid(t *testing.T) {
	d := openTestDB(t)
	token, _, err := d.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	username, err := d.ValidateSession(token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if username != "admin" {
		t.Errorf("expected admin, got %q", username)
	}
}

func TestValidateSessionInvalid(t *testing.T) {
	d := openTestDB(t)
	_, err := d.ValidateSession("nosuchtoken")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateSessionExpired(t *testing.T) {
	d := openTestDB(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO sessions (token, username, expires_at) VALUES ('exp-tok', 'admin', ?)`, past); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	_, err := d.ValidateSession("exp-tok")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for expired session, got %v", err)
	}
}

func TestValidateSessionExpiredCleansUp(t *testing.T) {
	d := openTestDB(t)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO sessions (token, username, expires_at) VALUES ('cleanup-tok', 'admin', ?)`, past); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	_, _ = d.ValidateSession("cleanup-tok")

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = 'cleanup-tok'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("expected expired session to be deleted after validation")
	}
}

func TestCheckPasswordUpgradesLegacySHA512Hash(t *testing.T) {
	d := openTestDB(t)
	legacySum := sha512.Sum512([]byte("legacy-pass"))
	legacyHash := fmt.Sprintf("%x", legacySum)
	if _, err := d.Exec(`INSERT INTO auth (username, password_hash) VALUES ('admin', ?)`, legacyHash); err != nil {
		t.Fatalf("insert legacy hash: %v", err)
	}

	ok, err := d.CheckPassword("admin", "legacy-pass")
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected legacy password to validate")
	}

	var stored string
	if err := d.QueryRow(`SELECT password_hash FROM auth WHERE username = 'admin'`).Scan(&stored); err != nil {
		t.Fatalf("read upgraded hash: %v", err)
	}
	if !strings.HasPrefix(stored, "$argon2id$") {
		t.Fatalf("expected legacy hash to be upgraded, got %q", stored)
	}
}

func TestDeleteSession(t *testing.T) {
	d := openTestDB(t)
	token, _, err := d.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := d.DeleteSession(token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := d.ValidateSession(token); err != ErrNotFound {
		t.Fatalf("expected deleted session to be gone, got %v", err)
	}
}
