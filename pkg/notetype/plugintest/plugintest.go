// Package plugintest provides a standard test harness for note type plugins.
//
// Each plugin simply calls plugintest.Run(t, &MyPlugin{}) in its test file and
// gets a full battery of automatic tests for free:
//
//   - Registration: plugin is found in the global registry after init()
//   - Schema idempotency: calling InitSchema twice does not error
//   - Manifest: self-consistent metadata (label, editor, viewer, actions)
//   - Config round-trip: save/load/validate via ConfigValidator/ConfigSaver/ConfigLoader
//   - View builder: BuildView returns JSON-serializable data
//   - Action handler: declared actions are dispatchable
//   - ID uniqueness: no two registered plugins share the same ID
package plugintest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// TestData provides the test harness with type-specific payloads.
type TestData struct {
	// ValidPayload is a JSON string that passes config validation and is used
	// for the Config_RoundTrip and View_Builder sub-tests.
	ValidPayload string

	// InvalidPayload is a JSON string that MUST fail config validation.
	// Note: as of the new explicit model migration, this field is retained for
	// backward compatibility but no standard sub-test consumes it directly.
	// Plugins may use it in their own tests.
	InvalidPayload string
}

// Run executes the standard test battery against a plugin.
// Call this from your plugin's test file:
//
//	func TestRecipePlugin(t *testing.T) {
//	    plugintest.Run(t, &recipe.RecipePlugin{}, plugintest.TestData{
//	        ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"cups"}]}`,
//	    })
//	}
func Run(t *testing.T, plugin notetype.Plugin, td TestData) {
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

		// Insert a note to verify foreign key compatibility.
		res, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('test', ?)`, plugin.ID())
		if err != nil {
			t.Fatalf("insert note: %v", err)
		}
		noteID, _ := res.LastInsertId()
		_ = noteID
	})

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

	// Manifest — every plugin now exposes Manifest() directly via the Plugin interface.
	t.Run("Manifest", func(t *testing.T) {
		m := plugin.Manifest()
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

	// Config round-trip (for plugins that implement ConfigValidator/ConfigSaver/ConfigLoader)
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

	// View builder (for plugins that implement ViewBuilder)
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

	// Action handler (for plugins that implement ActionHandler)
	t.Run("Action_Handler", func(t *testing.T) {
		ah, ok := plugin.(notetype.ActionHandler)
		if !ok {
			t.Skip("plugin does not implement ActionHandler")
		}
		m := plugin.Manifest()
		if len(m.Actions) == 0 {
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
// It only checks ID validity, Registry presence, and Manifest consistency.
func Quick(t *testing.T, plugin notetype.Plugin) {
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
	t.Run("Manifest", func(t *testing.T) {
		m := plugin.Manifest()
		if m.ID != plugin.ID() {
			t.Errorf("Manifest ID %q != plugin ID %q", m.ID, plugin.ID())
		}
		if m.Label == "" {
			t.Error("Manifest Label is empty")
		}
	})
}

// DB is a helper that opens an in-memory database, runs the notes migration,
// and the plugin's InitSchema. Plugins can use this for their own custom tests.
func DB(t *testing.T, plugin notetype.Plugin) *db.DB {
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
func CreateNote(t *testing.T, d *db.DB, title string, plugin notetype.Plugin) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO notes (title, type) VALUES (?, ?)`, title, plugin.ID())
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}
