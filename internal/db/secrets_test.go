package db

import (
	"path/filepath"
	"testing"
)

func openSecretsTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSecretsRoundTrip(t *testing.T) {
	d := openSecretsTestDB(t)

	// Missing key reads as empty, not an error.
	if got, err := d.GetSecret("missing"); err != nil || got != "" {
		t.Fatalf("GetSecret(missing) = %q, %v; want \"\", nil", got, err)
	}

	if err := d.SetSecret("k", "v1"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if got, err := d.GetSecret("k"); err != nil || got != "v1" {
		t.Fatalf("GetSecret = %q, %v; want v1", got, err)
	}

	// Set overwrites.
	if err := d.SetSecret("k", "v2"); err != nil {
		t.Fatalf("SetSecret overwrite: %v", err)
	}
	if got, err := d.GetSecret("k"); err != nil || got != "v2" {
		t.Fatalf("GetSecret after overwrite = %q, %v; want v2", got, err)
	}

	// IfAbsent keeps the stored value.
	if created, err := d.SetSecretIfAbsent("k", "v3"); err != nil || created {
		t.Fatalf("SetSecretIfAbsent(existing) = %v, %v; want false, nil", created, err)
	}
	if got, err := d.GetSecret("k"); err != nil || got != "v2" {
		t.Fatalf("GetSecret after SetSecretIfAbsent = %q, %v; want v2", got, err)
	}

	// IfAbsent on a fresh key stores.
	if created, err := d.SetSecretIfAbsent("fresh", "new"); err != nil || !created {
		t.Fatalf("SetSecretIfAbsent(fresh) = %v, %v; want true, nil", created, err)
	}
	if got, err := d.GetSecret("fresh"); err != nil || got != "new" {
		t.Fatalf("GetSecret(fresh) = %q, %v; want new", got, err)
	}
}

func TestSecretsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")

	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if created, err := d.SetSecretIfAbsent("tunnel", "secret-value"); err != nil || !created {
		t.Fatalf("SetSecretIfAbsent = %v, %v", created, err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	if got, err := d2.GetSecret("tunnel"); err != nil || got != "secret-value" {
		t.Fatalf("GetSecret after reopen = %q, %v; want secret-value", got, err)
	}
}
