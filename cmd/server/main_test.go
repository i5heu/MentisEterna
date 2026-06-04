package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequireExistingDBUnlessCreate(t *testing.T) {
	t.Run("allows existing db", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mentis.db")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("write db file: %v", err)
		}
		if err := requireExistingDBUnlessCreate(path, false); err != nil {
			t.Fatalf("expected existing db to be allowed, got %v", err)
		}
	})

	t.Run("rejects missing db without create flag", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.db")
		err := requireExistingDBUnlessCreate(path, false)
		if err == nil {
			t.Fatal("expected missing db to be rejected")
		}
	})

	t.Run("allows missing db with create flag", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.db")
		if err := requireExistingDBUnlessCreate(path, true); err != nil {
			t.Fatalf("expected create flag to allow missing db, got %v", err)
		}
	})

	t.Run("allows in-memory db paths", func(t *testing.T) {
		for _, path := range []string{":memory:", "file:mentis?mode=memory&cache=shared"} {
			if err := requireExistingDBUnlessCreate(path, false); err != nil {
				t.Fatalf("expected in-memory path %q to be allowed, got %v", path, err)
			}
		}
	})
}
