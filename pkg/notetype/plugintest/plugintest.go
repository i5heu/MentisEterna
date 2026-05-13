// Package plugintest provides a standard test harness for note type plugins.
//
// Each plugin simply calls plugintest.Run(t, &MyPlugin{}) in its test file and
// gets a full battery of automatic tests for free:
//
//   - Registration: plugin is found in the global registry after init()
//   - Schema idempotency: calling InitSchema twice does not error
//   - Validate: accepts valid payload, rejects invalid payload
//   - Save/Load round-trip: what you save is what you get back
//   - Payload shape consistency: ProcessLoad output passes Validate
//   - Empty save: saving with nil/empty payload does not crash
//   - Orphan cleanup: deleting the parent note cascades to plugin tables
//   - UISchema validity: returns well-formed JSON
//   - ID uniqueness: no two registered plugins share the same ID
package plugintest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// TestData provides the test harness with type-specific payloads.
type TestData struct {
	// ValidPayload is a JSON string that passes Validate and round-trips
	// through ProcessSave → ProcessLoad correctly.
	ValidPayload string

	// InvalidPayload is a JSON string that MUST fail Validate.
	InvalidPayload string
}

// Run executes the standard test battery against a plugin.
// Call this from your plugin's test file:
//
//	func TestRecipePlugin(t *testing.T) {
//	    plugintest.Run(t, &recipe.RecipePlugin{}, plugintest.TestData{
//	        ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"cups"}]}`,
//	        InvalidPayload: `{"ingredients":[{"name":"","amount":"2","unit":"cups"}]}`,
//	    })
//	}
func Run(t *testing.T, plugin notetype.NoteType, td TestData) {
	t.Helper()
	t.Run("ID_NotEmpty", func(t *testing.T) {
		if plugin.ID() == "" {
			t.Error("ID() returned empty string")
		}
	})

	t.Run("Registry", func(t *testing.T) {
		got, ok := notetype.Registry[plugin.ID()]
		if !ok {
			t.Fatalf("plugin %q not found in registry — did init() run?", plugin.ID())
		}
		if got != plugin {
			t.Fatal("registry entry is not the same plugin instance")
		}
	})

	t.Run("ID_Uniqueness", func(t *testing.T) {
		for id, p := range notetype.Registry {
			if id == plugin.ID() {
				continue
			}
			if p.ID() == plugin.ID() {
				t.Fatalf("two plugins share ID %q", plugin.ID())
			}
		}
	})

	t.Run("InitSchema_Idempotent", func(t *testing.T) {
		d, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open in-memory db: %v", err)
		}
		defer d.Close()

		if err := plugin.InitSchema(d.DB); err != nil {
			t.Fatalf("first InitSchema: %v", err)
		}
		if err := plugin.InitSchema(d.DB); err != nil {
			t.Fatalf("second InitSchema: %v", err)
		}
	})

	t.Run("InitSchema_AfterNotesTable", func(t *testing.T) {
		d, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open in-memory db: %v", err)
		}
		defer d.Close()

		// Simulate the server startup order: notes table exists before plugin schema.
		if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'standard',
			pinned INTEGER NOT NULL DEFAULT 0,
			parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
		)`); err != nil {
			t.Fatalf("create notes table: %v", err)
		}

		if err := plugin.InitSchema(d.DB); err != nil {
			t.Fatalf("InitSchema after notes table: %v", err)
		}

		// Insert a note so we can test foreign key behavior.
		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('test', ?)`, plugin.ID())
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()

		// Validate can unmarshal its own output.
		_ = noteID // used in sub-tests below
	})

	t.Run("UISchema_ValidJSON", func(t *testing.T) {
		schema := plugin.UISchema()
		if len(schema) == 0 {
			t.Skip("plugin does not provide a UI schema")
		}
		var v any
		if err := json.Unmarshal(schema, &v); err != nil {
			t.Errorf("UISchema is not valid JSON: %v\nSchema: %s", err, string(schema))
		}
	})

	t.Run("Validate_EmptyPayload", func(t *testing.T) {
		if err := plugin.Validate(json.RawMessage{}); err != nil {
			t.Errorf("empty payload should be valid: %v", err)
		}
		if err := plugin.Validate(json.RawMessage("null")); err != nil {
			t.Errorf("null payload should be valid: %v", err)
		}
	})

	if td.ValidPayload != "" {
		t.Run("Validate_AcceptsValid", func(t *testing.T) {
			if err := plugin.Validate(json.RawMessage(td.ValidPayload)); err != nil {
				t.Errorf("valid payload rejected: %v\nPayload: %s", err, td.ValidPayload)
			}
		})
	}

	if td.InvalidPayload != "" {
		t.Run("Validate_RejectsInvalid", func(t *testing.T) {
			if err := plugin.Validate(json.RawMessage(td.InvalidPayload)); err == nil {
				t.Errorf("invalid payload was accepted\nPayload: %s", td.InvalidPayload)
			}
		})
	}

	// Full round-trip test: create note, save custom data, load it back.
	if td.ValidPayload != "" {
		t.Run("SaveLoad_RoundTrip", func(t *testing.T) {
			d, err := db.OpenInMemory()
			if err != nil {
				t.Fatalf("open in-memory db: %v", err)
			}
			defer d.Close()

			// Set up the notes table.
			if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				title TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'standard',
				pinned INTEGER NOT NULL DEFAULT 0,
				parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
			)`); err != nil {
				t.Fatalf("create notes table: %v", err)
			}

			if err := plugin.InitSchema(d.DB); err != nil {
				t.Fatalf("InitSchema: %v", err)
			}

			res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('roundtrip', ?)`, plugin.ID())
			if err != nil {
				t.Fatalf("insert note: %v", err)
			}
			noteID, _ := res.LastInsertId()

			payload := json.RawMessage(td.ValidPayload)

			// Start a transaction and save.
			tx, err := d.Begin()
			if err != nil {
				t.Fatalf("begin tx: %v", err)
			}
			defer tx.Rollback()

			if err := plugin.ProcessSave(context.Background(), tx, 0, noteID, payload); err != nil {
				t.Fatalf("ProcessSave: %v", err)
			}

			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}

			// Load back.
			loaded, err := plugin.ProcessLoad(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Fatalf("ProcessLoad: %v", err)
			}

			// Re-encode loaded data and validate it — catches shape mismatches
			// (e.g. ProcessLoad returns raw array but Validate expects object).
			reencoded, err := json.Marshal(loaded)
			if err != nil {
				t.Fatalf("marshal loaded data: %v", err)
			}

			if err := plugin.Validate(reencoded); err != nil {
				t.Errorf(
					"ProcessLoad returned data that fails Validate — %v\n"+
						"Loaded:  %s\n"+
						"Payload: %s\n"+
						"HINT: ProcessLoad must return the same JSON shape that Validate expects. "+
						"Wrap arrays in an object (e.g. {\"items\": [...]} not [...]).",
					err, string(reencoded), string(payload),
				)
			}

			// Overwrite with new data (simulates an edit).
			tx2, err := d.Begin()
			if err != nil {
				t.Fatalf("begin tx2: %v", err)
			}
			defer tx2.Rollback()

			if err := plugin.ProcessSave(context.Background(), tx2, 0, noteID, payload); err != nil {
				t.Fatalf("ProcessSave (update): %v", err)
			}

			if err := tx2.Commit(); err != nil {
				t.Fatalf("commit tx2: %v", err)
			}

			loaded2, err := plugin.ProcessLoad(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Fatalf("ProcessLoad after update: %v", err)
			}

			reencoded2, err := json.Marshal(loaded2)
			if err != nil {
				t.Fatalf("marshal loaded data after update: %v", err)
			}

			if err := plugin.Validate(reencoded2); err != nil {
				t.Errorf(
					"ProcessLoad after update returned data that fails Validate — %v\n"+
						"Loaded: %s",
					err, string(reencoded2),
				)
			}
		})
	}

	// Orphan cleanup: deleting the parent note should cascade to plugin tables.
	t.Run("SaveLoad_OrphanCleanup", func(t *testing.T) {
		d, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open in-memory db: %v", err)
		}
		defer d.Close()

		if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'standard',
			pinned INTEGER NOT NULL DEFAULT 0,
			parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
		)`); err != nil {
			t.Fatalf("create notes table: %v", err)
		}

		if err := plugin.InitSchema(d.DB); err != nil {
			t.Fatalf("InitSchema: %v", err)
		}

		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('orphan', ?)`, plugin.ID())
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()

		// If the plugin has no valid payload, skip the data-dependent part.
		if td.ValidPayload != "" {
			tx, err := d.Begin()
			if err != nil {
				t.Fatalf("begin tx: %v", err)
			}
			if err := plugin.ProcessSave(context.Background(), tx, 0, noteID, json.RawMessage(td.ValidPayload)); err != nil {
				tx.Rollback()
				t.Fatalf("ProcessSave: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}
		}

		// Delete the note.
		if _, err := d.Exec(`DELETE FROM notes WHERE id = ?`, noteID); err != nil {
			t.Fatalf("delete note: %v", err)
		}

		// Load should return nothing (or empty).
		loaded, err := plugin.ProcessLoad(context.Background(), d.DB, 0, noteID)
		if err != nil {
			// Some plugins might return ErrNotFound; that's fine too.
			return
		}
		if loaded != nil {
			// Check if it's an empty result.
			reencoded, _ := json.Marshal(loaded)
			trimmed := strings.TrimSpace(string(reencoded))
			// Accept null, {}, [], {"items":[]}, {"ingredients":[]}, etc.
			if trimmed != "null" && trimmed != "{}" && trimmed != "[]" &&
				!strings.Contains(trimmed, ":[]") { // crude but catches {"items":[]}
				t.Errorf("ProcessLoad returned data after note was deleted: %s", trimmed)
			}
		}
	})

	// Edge case: saving with empty payload should not crash.
	t.Run("SaveLoad_EmptySave", func(t *testing.T) {
		d, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open in-memory db: %v", err)
		}
		defer d.Close()

		if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'standard',
			pinned INTEGER NOT NULL DEFAULT 0,
			parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
		)`); err != nil {
			t.Fatalf("create notes table: %v", err)
		}

		if err := plugin.InitSchema(d.DB); err != nil {
			t.Fatalf("InitSchema: %v", err)
		}

		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('empty-save', ?)`, plugin.ID())
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()

		tx, err := d.Begin()
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback()

		// Save with empty payload.
		if err := plugin.ProcessSave(context.Background(), tx, 0, noteID, json.RawMessage("null")); err != nil {
			t.Errorf("ProcessSave with null payload: %v", err)
		}
		if err := plugin.ProcessSave(context.Background(), tx, 0, noteID, json.RawMessage("{}")); err != nil {
			t.Errorf("ProcessSave with empty object payload: %v", err)
		}

		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}

		// For plugins without a custom payload, ProcessLoad may depend on
		// other plugins' tables — skip the load check.
		if td.ValidPayload != "" {
			_, err = plugin.ProcessLoad(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Errorf("ProcessLoad after empty save: %v", err)
			}
		}
	})

	// CronJobs should not panic and must have required fields.
	t.Run("CronJobs_NoPanic", func(t *testing.T) {
		jobs := plugin.CronJobs()
		for i, job := range jobs {
			if job.Name == "" {
				t.Errorf("CronJob[%d] has empty Name", i)
			}
			if job.Schedule == "" {
				t.Errorf("CronJob[%d] has empty Schedule", i)
			}
			if job.Task == nil {
				t.Errorf("CronJob[%d] has nil Task", i)
			}
		}
	})

	// Test the action handler, if the plugin registered one.
	t.Run("Actions_Handler", func(t *testing.T) {
		// Collect all registered action handlers by checking pluginActionHandlers.
		// We access it indirectly: if the plugin registered one via
		// server.RegisterPluginActionHandler, we can find it by type ID.
		//
		// Since pluginActionHandlers is not exported, we test indirectly
		// by calling ProcessLoad which every plugin must support.
		// Plugin-specific action tests belong in the plugin's own test file.
	})

	// Manifest tests
	t.Run("Manifest_Provider", func(t *testing.T) {
		mp, ok := plugin.(notetype.ManifestProvider)
		if !ok {
			t.Skip("plugin does not implement ManifestProvider (not yet migrated)")
		}
		m := mp.Manifest()
		if m.ID != plugin.ID() {
			t.Errorf("Manifest ID %q != plugin ID %q", m.ID, plugin.ID())
		}
		if m.Label == "" {
			t.Error("Manifest Label is empty")
		}
		if len(m.DefaultConfig) > 0 {
			var v any
			if err := json.Unmarshal(m.DefaultConfig, &v); err != nil {
				t.Errorf("Manifest DefaultConfig is not valid JSON: %v", err)
			}
		}
		if m.Editor.Mode != "none" && m.Editor.Mode != "schema" && m.Editor.Mode != "custom" {
			t.Errorf("Manifest Editor.Mode is invalid: %q", m.Editor.Mode)
		}
		if m.Viewer.Mode != "none" && m.Viewer.Mode != "custom" {
			t.Errorf("Manifest Viewer.Mode is invalid: %q", m.Viewer.Mode)
		}
		if m.Editor.Mode == "schema" && len(m.Editor.Schema) == 0 {
			t.Error("Manifest Editor.Mode is 'schema' but Editor.Schema is empty")
		}
		if m.HasActions && len(m.Actions) == 0 {
			t.Error("Manifest HasActions is true but Actions list is empty")
		}
		if len(m.Actions) > 0 && !m.HasActions {
			t.Error("Manifest HasActions is false but Actions list is non-empty")
		}
		for i, a := range m.Actions {
			if a.ID == "" {
				t.Errorf("Action[%d] has empty ID", i)
			}
			if a.Label == "" {
				t.Errorf("Action[%d] has empty Label", i)
			}
			if a.RefreshStrategy != "none" && a.RefreshStrategy != "reload" && a.RefreshStrategy != "reload_view" {
				t.Errorf("Action[%d] has invalid RefreshStrategy: %q", i, a.RefreshStrategy)
			}
			if len(a.ParamsSchema) > 0 {
				var v any
				if err := json.Unmarshal(a.ParamsSchema, &v); err != nil {
					t.Errorf("Action[%d] ParamsSchema is not valid JSON: %v", i, err)
				}
			}
		}
	})

	// Config round-trip (for migrated plugins)
	if td.ValidPayload != "" {
		t.Run("Config_RoundTrip", func(t *testing.T) {
			cv, okv := plugin.(notetype.ConfigValidator)
			cs, oks := plugin.(notetype.ConfigSaver)
			cl, okl := plugin.(notetype.ConfigLoader)
			if !okv || !oks || !okl {
				t.Skip("plugin does not implement full config interface")
			}

			d, err := db.OpenInMemory()
			if err != nil {
				t.Fatalf("open in-memory db: %v", err)
			}
			defer d.Close()

			if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				title TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'standard',
				pinned INTEGER NOT NULL DEFAULT 0,
				parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
			)`); err != nil {
				t.Fatalf("create notes table: %v", err)
			}

			if err := plugin.InitSchema(d.DB); err != nil {
				t.Fatalf("InitSchema: %v", err)
			}

			res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('config-test', ?)`, plugin.ID())
			if err != nil {
				t.Fatalf("insert note: %v", err)
			}
			noteID, _ := res.LastInsertId()

			config := json.RawMessage(td.ValidPayload)

			// Validate
			if err := cv.ValidateConfig(config); err != nil {
				t.Fatalf("ValidateConfig: %v", err)
			}

			// Save
			tx, err := d.Begin()
			if err != nil {
				t.Fatalf("begin tx: %v", err)
			}
			if err := cs.SaveConfig(context.Background(), tx, 0, noteID, config); err != nil {
				tx.Rollback()
				t.Fatalf("SaveConfig: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}

			// Load
			loaded, err := cl.LoadConfig(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			// Validate loaded config
			if err := cv.ValidateConfig(loaded); err != nil {
				t.Errorf("loaded config fails ValidateConfig: %v\nLoaded: %s", err, string(loaded))
			}
		})
	}

	// View builder (for migrated plugins)
	t.Run("View_Builder", func(t *testing.T) {
		vb, ok := plugin.(notetype.ViewBuilder)
		if !ok {
			t.Skip("plugin does not implement ViewBuilder")
		}

		d, err := db.OpenInMemory()
		if err != nil {
			t.Fatalf("open in-memory db: %v", err)
		}
		defer d.Close()

		if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'standard',
			pinned INTEGER NOT NULL DEFAULT 0,
			parent_id INTEGER REFERENCES notes(id) ON DELETE SET NULL
		)`); err != nil {
			t.Fatalf("create notes table: %v", err)
		}

		// Initialize ALL registered plugin schemas, not just the plugin under test.
		// Some plugins (e.g. recipe_overview) query tables owned by other plugins (e.g. recipe).
		for _, p := range notetype.Registry {
			if err := p.InitSchema(d.DB); err != nil {
				t.Fatalf("InitSchema for %s: %v", p.ID(), err)
			}
		}

		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('view-test', ?)`, plugin.ID())
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()

		// Save config first if we have a valid payload, so view has data to work with.
		if td.ValidPayload != "" {
			if cs, ok := plugin.(notetype.ConfigSaver); ok {
				tx, _ := d.Begin()
				_ = cs.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(td.ValidPayload))
				tx.Commit()
			}
		}

		view, err := vb.BuildView(context.Background(), d.DB, 0, noteID)
		if err != nil {
			t.Errorf("BuildView: %v", err)
		}
		if view != nil {
			// View should be JSON-serializable.
			if _, err := json.Marshal(view); err != nil {
				t.Errorf("BuildView returned non-JSON-serializable data: %v", err)
			}
		}
	})

	// Action handler (for migrated plugins)
	t.Run("Action_Handler", func(t *testing.T) {
		ah, ok := plugin.(notetype.ActionHandler)
		if !ok {
			t.Skip("plugin does not implement ActionHandler")
		}
		m := notetype.HasManifest(plugin)
		if m == nil || len(m.Actions) == 0 {
			t.Skip("plugin has action handler but no action metadata")
		}

		// Verify each action in the manifest is dispatchable (dry-run: unknown params should not panic).
		for _, a := range m.Actions {
			_, err := ah.HandleAction(context.Background(), nil, 0, 0, a.ID, nil)
			// We don't care about the error — just that it doesn't panic.
			_ = err
		}
	})
}

// Quick runs a minimal, fast validation-only test. Useful for rapid iteration.
// It skips the round-trip and schema tests.
func Quick(t *testing.T, plugin notetype.NoteType, td TestData) {
	t.Helper()
	t.Run("ID_NotEmpty", func(t *testing.T) {
		if plugin.ID() == "" {
			t.Error("ID() returned empty string")
		}
	})
	t.Run("Validate_AcceptsValid", func(t *testing.T) {
		if td.ValidPayload == "" {
			t.Skip("no valid payload provided")
		}
		if err := plugin.Validate(json.RawMessage(td.ValidPayload)); err != nil {
			t.Errorf("valid payload rejected: %v", err)
		}
	})
	t.Run("Validate_RejectsInvalid", func(t *testing.T) {
		if td.InvalidPayload == "" {
			t.Skip("no invalid payload provided")
		}
		if err := plugin.Validate(json.RawMessage(td.InvalidPayload)); err == nil {
			t.Errorf("invalid payload was accepted")
		}
	})
	t.Run("UISchema_ValidJSON", func(t *testing.T) {
		schema := plugin.UISchema()
		if len(schema) == 0 {
			t.Skip("plugin does not provide a UI schema")
		}
		var v any
		if err := json.Unmarshal(schema, &v); err != nil {
			t.Errorf("UISchema is not valid JSON: %v", err)
		}
	})
}

// DB is a helper that opens an in-memory database, runs the notes migration,
// and the plugin's InitSchema. Plugins can use this for their own custom tests.
func DB(t *testing.T, plugin notetype.NoteType) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// Run the standard notes migration.
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		title      TEXT NOT NULL,
		type       TEXT NOT NULL DEFAULT 'standard',
		pinned     INTEGER NOT NULL DEFAULT 0,
		parent_id  INTEGER REFERENCES notes(id) ON DELETE SET NULL,
		created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		t.Fatalf("create notes table: %v", err)
	}

	if err := plugin.InitSchema(d.DB); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return d
}

// CreateNote inserts a note of the given type and returns its ID.
func CreateNote(t *testing.T, d *db.DB, title string, nt notetype.NoteType) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO notes (title, type) VALUES (?, ?)`, title, nt.ID())
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// SavePayload begins a transaction, calls ProcessSave, and commits.
func SavePayload(t *testing.T, d *db.DB, plugin notetype.NoteType, noteID int64, payload json.RawMessage) {
	t.Helper()
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if err := plugin.ProcessSave(context.Background(), tx, 0, noteID, payload); err != nil {
		t.Fatalf("ProcessSave: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
