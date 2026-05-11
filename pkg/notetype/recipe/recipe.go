// Package recipe implements a "recipe" note type that stores a recipe as
// multiple ingredient rows (name, amount, unit) in its own table.
package recipe

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "recipe"

// IngredientRow represents a single ingredient in a recipe.
type IngredientRow struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Amount string `json:"amount"` // stored as string to support "1-2", "to taste", etc.
	Unit   string `json:"unit"`
}

// Payload is the JSON structure the frontend sends for a recipe note.
type Payload struct {
	Ingredients []IngredientRow `json:"ingredients"`
}

type RecipePlugin struct{}

func init() {
	notetype.Register(&RecipePlugin{})
}

func (p *RecipePlugin) ID() string { return pluginID }

func (p *RecipePlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_recipe_ingredients (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id    INTEGER NOT NULL,
			name       TEXT    NOT NULL,
			amount     TEXT    NOT NULL DEFAULT '',
			unit       TEXT    NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_ingredients_note
			ON ct_recipe_ingredients(note_id);
	`)
	return err
}

func (p *RecipePlugin) Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil // optional
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("recipe: invalid payload: %w", err)
	}
	for i, ing := range payload.Ingredients {
		if strings.TrimSpace(ing.Name) == "" {
			return fmt.Errorf("recipe: ingredient %d: name is required", i+1)
		}
	}
	return nil
}

func (p *RecipePlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("recipe: unmarshal payload: %w", err)
	}

	// Delete old ingredients and insert new ones (simpler than diffing).
	if _, err := tx.Exec(`DELETE FROM ct_recipe_ingredients WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("recipe: delete old ingredients: %w", err)
	}

	for i, ing := range payload.Ingredients {
		if _, err := tx.Exec(
			`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, ?, ?, ?, ?)`,
			noteID, strings.TrimSpace(ing.Name), strings.TrimSpace(ing.Amount), strings.TrimSpace(ing.Unit), i,
		); err != nil {
			return fmt.Errorf("recipe: insert ingredient %d: %w", i, err)
		}
	}
	return nil
}

func (p *RecipePlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	rows, err := db.Query(
		`SELECT id, name, amount, unit FROM ct_recipe_ingredients WHERE note_id = ? ORDER BY sort_order`,
		noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("recipe: load ingredients: %w", err)
	}
	defer rows.Close()

	ingredients := []IngredientRow{}
	for rows.Next() {
		var ing IngredientRow
		if err := rows.Scan(&ing.ID, &ing.Name, &ing.Amount, &ing.Unit); err != nil {
			return nil, fmt.Errorf("recipe: scan ingredient: %w", err)
		}
		ingredients = append(ingredients, ing)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return the same Payload shape that Validate/ProcessSave expect.
	return Payload{Ingredients: ingredients}, nil
}

func (p *RecipePlugin) UISchema() json.RawMessage {
	schema := json.RawMessage(`[
	{
		"$el": "h3",
		"children": "Ingredients"
	},
	{
		"$formkit": "list",
		"name": "ingredients",
		"children": [
			{
				"$formkit": "text",
				"name": "name",
				"label": "Name",
				"validation": "required"
			},
			{
				"$formkit": "text",
				"name": "amount",
				"label": "Amount"
			},
			{
				"$formkit": "text",
				"name": "unit",
				"label": "Unit"
			}
		]
	}
]`)
	return schema
}

func (p *RecipePlugin) CronJobs() []notetype.CronJob {
	return nil // no background jobs for individual recipes
}
