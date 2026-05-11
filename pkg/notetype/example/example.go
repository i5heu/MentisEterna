// Package example implements a minimal "example" note type to demonstrate
// the notetype.NoteType interface. Use this as a starting point for new plugins.
package example

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

func init() {
	notetype.Register(&ExamplePlugin{})
}

type ExamplePlugin struct{}

func (p *ExamplePlugin) ID() string { return "example" }

func (p *ExamplePlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_example_items (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id INTEGER NOT NULL,
			label   TEXT    NOT NULL DEFAULT '',
			checked INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_example_items_note ON ct_example_items(note_id);
	`)
	return err
}

type ExampleItem struct {
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Checked bool   `json:"checked"`
}

type ExamplePayload struct {
	Items []ExampleItem `json:"items"`
}

func (p *ExamplePlugin) Validate(raw json.RawMessage) error {
	return nil // accept anything
}

func (p *ExamplePlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
	var payload ExamplePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ct_example_items WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	for i, item := range payload.Items {
		checked := 0
		if item.Checked {
			checked = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO ct_example_items (note_id, label, checked, id) VALUES (?, ?, ?, ?)`,
			noteID, item.Label, checked, i,
		); err != nil {
			return err
		}
	}
	return nil
}

func (p *ExamplePlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	rows, err := db.Query(`SELECT id, label, checked FROM ct_example_items WHERE note_id = ? ORDER BY id`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ExampleItem{}
	for rows.Next() {
		var item ExampleItem
		var checked int
		if err := rows.Scan(&item.ID, &item.Label, &checked); err != nil {
			return nil, err
		}
		item.Checked = checked != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return the same Payload shape that Validate/ProcessSave expect.
	return ExamplePayload{Items: items}, nil
}

func (p *ExamplePlugin) UISchema() json.RawMessage {
	return json.RawMessage(`[
		{
			"$formkit": "list",
			"name": "items",
			"children": [
				{"$formkit": "text", "name": "label", "label": "Item"},
				{"$formkit": "checkbox", "name": "checked", "label": "Done"}
			]
		}
	]`)
}

func (p *ExamplePlugin) CronJobs() []notetype.CronJob {
	return nil
}
