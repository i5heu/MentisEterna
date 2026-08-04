package task

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestTaskPlugin(t *testing.T) {
	plugintest.Run(t, &TaskPlugin{}, plugintest.TestData{
		ValidPayload:   `{"status":"in_progress","difficulty":5,"fun":3,"priority":7,"description":"Write unit tests","due_date":"2025-01-15","time_estimation":"2h","time_used":"30m","recurring":"weekly","recurring_days":0,"completed_at":"","pending_does_not_force_daily_inclusion":true,"subtasks":[{"label":"Write unit tests","checked":false,"description":"Cover the happy path"},{"label":"Update docs","checked":true,"description":"Refresh README"}]}`,
		InvalidPayload: `{"status":"invalid","difficulty":999,"fun":999}`,
	})
}

func TestSubTaskPersistence(t *testing.T) {
	d := plugintest.DB(t, &TaskPlugin{})
	noteID := plugintest.CreateNote(t, d, "Task with subtasks", &TaskPlugin{})
	save := func(t *testing.T, cfgJSON string) {
		t.Helper()
		tx, err := d.Begin()
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		if err := (&TaskPlugin{}).SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(cfgJSON)); err != nil {
			tx.Rollback()
			t.Fatalf("SaveConfig: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	load := func(t *testing.T) TaskConfig {
		t.Helper()
		raw, err := (&TaskPlugin{}).LoadConfig(context.Background(), d.DB, 0, noteID)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		var cfg TaskConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("unmarshal loaded config: %v", err)
		}
		return cfg
	}

	t.Run("Insert", func(t *testing.T) {
		save(t, `{"status":"todo","subtasks":[{"label":"First","checked":false,"description":"Do the thing"},{"label":"Second","checked":true}]}`)
		cfg := load(t)
		if len(cfg.SubTasks) != 2 {
			t.Fatalf("expected 2 subtasks, got %d: %+v", len(cfg.SubTasks), cfg.SubTasks)
		}
		if cfg.SubTasks[0].Label != "First" {
			t.Errorf("subtask[0].Label = %q, want %q", cfg.SubTasks[0].Label, "First")
		}
		if cfg.SubTasks[0].Description != "Do the thing" {
			t.Errorf("subtask[0].Description = %q, want %q", cfg.SubTasks[0].Description, "Do the thing")
		}
		if cfg.SubTasks[1].Description != "" {
			t.Errorf("subtask[1].Description = %q, want empty", cfg.SubTasks[1].Description)
		}
		if !cfg.SubTasks[1].Checked {
			t.Errorf("subtask[1].Checked = false, want true")
		}
	})

	t.Run("Replace", func(t *testing.T) {
		save(t, `{"status":"todo","subtasks":[{"label":"Only","checked":false}]}`)
		cfg := load(t)
		if len(cfg.SubTasks) != 1 {
			t.Fatalf("expected 1 subtask after re-save, got %d: %+v", len(cfg.SubTasks), cfg.SubTasks)
		}
		if cfg.SubTasks[0].Label != "Only" {
			t.Errorf("subtask[0].Label = %q, want %q", cfg.SubTasks[0].Label, "Only")
		}
	})

	t.Run("EmptyLabelSkipped", func(t *testing.T) {
		save(t, `{"status":"todo","subtasks":[{"label":"   ","checked":false},{"label":"Kept","checked":false}]}`)
		cfg := load(t)
		if len(cfg.SubTasks) != 1 {
			t.Fatalf("expected 1 subtask (empty skipped), got %d: %+v", len(cfg.SubTasks), cfg.SubTasks)
		}
		if cfg.SubTasks[0].Label != "Kept" {
			t.Errorf("subtask[0].Label = %q, want %q", cfg.SubTasks[0].Label, "Kept")
		}
	})
}

// TestToggleSubtaskAction verifies the view-mode checkbox persistence path:
// toggling a subtask via the action updates only that row in the table.
func TestToggleSubtaskAction(t *testing.T) {
	d := plugintest.DB(t, &TaskPlugin{})
	noteID := plugintest.CreateNote(t, d, "Task with subtasks", &TaskPlugin{})
	p := &TaskPlugin{}

	saveCfg := `{"status":"todo","subtasks":[{"label":"One","checked":false},{"label":"Two","checked":true}]}`
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(saveCfg)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Grab the server-assigned subtask ids.
	raw, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var cfg TaskConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if len(cfg.SubTasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(cfg.SubTasks))
	}
	firstID := cfg.SubTasks[0].ID

	t.Run("ChecksUnchecked", func(t *testing.T) {
		res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "toggle_subtask", json.RawMessage(`{"subtask_id":`+strconv.FormatInt(firstID, 10)+`,"checked":true}`))
		if err != nil {
			t.Fatalf("HandleAction toggle on: %v", err)
		}
		if res.(map[string]any)["checked"] != true {
			t.Errorf("action response checked = %v, want true", res)
		}
		raw, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		var after TaskConfig
		if err := json.Unmarshal(raw, &after); err != nil {
			t.Fatalf("unmarshal after: %v", err)
		}
		if !after.SubTasks[0].Checked {
			t.Error("subtask[0] should be checked after toggle")
		}
		if !after.SubTasks[1].Checked {
			t.Error("subtask[1] must be untouched by the toggle")
		}
	})

	t.Run("UnchecksChecked", func(t *testing.T) {
		secondID := func() int64 {
			raw, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			var cfg TaskConfig
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			return cfg.SubTasks[1].ID
		}()
		if _, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "toggle_subtask", json.RawMessage(`{"subtask_id":`+strconv.FormatInt(secondID, 10)+`,"checked":false}`)); err != nil {
			t.Fatalf("HandleAction toggle off: %v", err)
		}
		raw, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		var after TaskConfig
		if err := json.Unmarshal(raw, &after); err != nil {
			t.Fatalf("unmarshal after: %v", err)
		}
		if after.SubTasks[1].Checked {
			t.Error("subtask[1] should be unchecked after toggle")
		}
	})

	t.Run("UnknownSubtaskErrors", func(t *testing.T) {
		_, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "toggle_subtask", json.RawMessage(`{"subtask_id":99999,"checked":true}`))
		if err == nil {
			t.Fatal("expected error for unknown subtask id")
		}
	})
}

// TestTaskPlugin_MigratesLegacySubtasksTable verifies that InitSchema adds the
// description column to a pre-existing ct_task_subtasks table (created before
// the column existed).
func TestTaskPlugin_MigratesLegacySubtasksTable(t *testing.T) {
	d := plugintest.DB(t, &TaskPlugin{})

	// Simulate a legacy database: drop the new-style table and recreate it
	// without the description column.
	if _, err := d.Exec(`DROP TABLE IF EXISTS ct_task_subtasks`); err != nil {
		t.Fatalf("drop subtasks table: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TABLE ct_task_subtasks (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id INTEGER NOT NULL,
			label   TEXT    NOT NULL DEFAULT '',
			checked INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		)
	`); err != nil {
		t.Fatalf("create legacy subtasks table: %v", err)
	}

	if err := (&TaskPlugin{}).InitSchema(d.DB); err != nil {
		t.Fatalf("InitSchema after legacy table: %v", err)
	}

	rows, err := d.Query(`PRAGMA table_info(ct_task_subtasks)`)
	if err != nil {
		t.Fatalf("pragma subtasks table info: %v", err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan subtasks table info: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate subtasks table info: %v", err)
	}
	if !cols["description"] {
		t.Fatal("description column missing after legacy migration")
	}
}
