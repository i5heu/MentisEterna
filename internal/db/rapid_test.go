package db

import (
	"testing"

	"pgregory.net/rapid"
)

// openDB opens a fresh database in the outer test's temp dir.
// Must be called with the outer *testing.T so that Cleanup and TempDir work correctly
// even when invoked inside rapid.Check callbacks.
func openDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenInMemory()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestPropPasswordRoundTrip: any non-empty password that is set must be accepted on check.
func TestPropPasswordRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		d := openDB(t)
		pw := rapid.StringMatching(`[a-zA-Z0-9!@#$%]{1,80}`).Draw(rt, "password")
		if err := d.SetAdminPassword(pw); err != nil {
			rt.Fatalf("SetAdminPassword: %v", err)
		}
		ok, err := d.CheckPassword("admin", pw)
		if err != nil {
			rt.Fatalf("CheckPassword: %v", err)
		}
		if !ok {
			rt.Fatalf("correct password %q not accepted", pw)
		}
	})
}

// TestPropWrongPasswordNeverMatches: a string different from the stored password must never validate.
func TestPropWrongPasswordNeverMatches(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		d := openDB(t)
		pw := rapid.StringMatching(`[a-z]{4,40}`).Draw(rt, "password")
		if err := d.SetAdminPassword(pw); err != nil {
			rt.Fatalf("SetAdminPassword: %v", err)
		}
		// pw contains only a-z, so appending a digit always yields a distinct string.
		wrong := pw + "1"
		ok, err := d.CheckPassword("admin", wrong)
		if err != nil {
			rt.Fatalf("CheckPassword: %v", err)
		}
		if ok {
			rt.Fatalf("wrong password %q accepted for password %q", wrong, pw)
		}
	})
}

// TestPropSessionsAlwaysUnique: every session token must be distinct from all previously issued ones.
func TestPropSessionsAlwaysUnique(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		d := openDB(t)
		n := rapid.IntRange(2, 12).Draw(rt, "count")
		seen := make(map[string]struct{}, n)
		for i := 0; i < n; i++ {
			tok, _, err := d.CreateSession("admin")
			if err != nil {
				rt.Fatalf("CreateSession %d: %v", i, err)
			}
			if _, dup := seen[tok]; dup {
				rt.Fatalf("duplicate token after %d sessions", i+1)
			}
			seen[tok] = struct{}{}
		}
	})
}

// TestPropDeleteNoteCascadesToUpdates: deleting a note row must cascade-delete all its updates.
func TestPropDeleteNoteCascadesToUpdates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		d := openDB(t)
		updateCount := rapid.IntRange(0, 10).Draw(rt, "updates")

		res, err := d.Exec(`INSERT INTO notes (title) VALUES (?)`, "prop-note")
		if err != nil {
			rt.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()

		for i := 0; i < updateCount; i++ {
			if _, err := d.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, "body"); err != nil {
				rt.Fatalf("insert update %d: %v", i, err)
			}
		}

		if _, err := d.Exec(`DELETE FROM notes WHERE id = ?`, noteID); err != nil {
			rt.Fatalf("delete note: %v", err)
		}

		var remaining int
		if err := d.QueryRow(`SELECT COUNT(*) FROM updates WHERE note_id = ?`, noteID).Scan(&remaining); err != nil {
			rt.Fatalf("count updates: %v", err)
		}
		if remaining != 0 {
			rt.Fatalf("expected 0 updates after note delete, got %d (had %d before)", remaining, updateCount)
		}
	})
}

// TestPropDeleteParentSetsChildParentToNull: deleting a parent note must set the child's
// parent_id to NULL (ON DELETE SET NULL), not delete the child.
func TestPropDeleteParentSetsChildParentToNull(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		d := openDB(t)
		grandChildren := rapid.IntRange(0, 5).Draw(rt, "grandChildren")

		parentRes, err := d.Exec(`INSERT INTO notes (title) VALUES (?)`, "parent")
		if err != nil {
			rt.Fatalf("insert parent: %v", err)
		}
		parentID, _ := parentRes.LastInsertId()

		childRes, err := d.Exec(`INSERT INTO notes (title, parent_id) VALUES (?, ?)`, "child", parentID)
		if err != nil {
			rt.Fatalf("insert child: %v", err)
		}
		childID, _ := childRes.LastInsertId()

		// Optional deeper nesting to exercise multiple levels.
		currentParent := childID
		for i := 0; i < grandChildren; i++ {
			r, err := d.Exec(`INSERT INTO notes (title, parent_id) VALUES (?, ?)`, "grand", currentParent)
			if err != nil {
				rt.Fatalf("insert grandchild %d: %v", i, err)
			}
			currentParent, _ = r.LastInsertId()
		}

		if _, err := d.Exec(`DELETE FROM notes WHERE id = ?`, parentID); err != nil {
			rt.Fatalf("delete parent: %v", err)
		}

		// The direct child must still exist.
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM notes WHERE id = ?`, childID).Scan(&count); err != nil {
			rt.Fatalf("count child: %v", err)
		}
		if count != 1 {
			rt.Fatalf("child note must survive parent deletion, count=%d", count)
		}

		// Its parent_id must now be NULL.
		var parentIDAfter *int64
		if err := d.QueryRow(`SELECT parent_id FROM notes WHERE id = ?`, childID).Scan(&parentIDAfter); err != nil {
			rt.Fatalf("scan parent_id: %v", err)
		}
		if parentIDAfter != nil {
			rt.Fatalf("child parent_id should be NULL after parent delete, got %d", *parentIDAfter)
		}
	})
}
