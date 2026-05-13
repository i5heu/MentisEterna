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
	Ingredients     []IngredientRow `json:"ingredients"`
	Servings        string          `json:"servings"`
	AttentionTime   string          `json:"attention_time"`
	TotalTime       string          `json:"total_time"`
	GramsPerServing string          `json:"grams_per_serving"`
	KcalPerServing  string          `json:"kcal_per_serving"`
	Freezable       bool            `json:"freezable"`
	PreCookServings string          `json:"pre_cook_servings"`
}

type RecipePlugin struct{}

func init() {
	notetype.Register(&RecipePlugin{})
}

func (p *RecipePlugin) ID() string { return pluginID }

func (p *RecipePlugin) InitSchema(db *sql.DB) error {
	var err error
	if _, err := db.Exec(`
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
	`); err != nil {
		return err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_recipe_meta (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id           INTEGER NOT NULL UNIQUE,
			servings          TEXT    NOT NULL DEFAULT '',
			attention_time    TEXT    NOT NULL DEFAULT '',
			total_time        TEXT    NOT NULL DEFAULT '',
			grams_per_serving TEXT    NOT NULL DEFAULT '',
			kcal_per_serving  TEXT    NOT NULL DEFAULT '',
			freezable         INTEGER NOT NULL DEFAULT 0,
			pre_cook_servings TEXT    NOT NULL DEFAULT '',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_meta_note
			ON ct_recipe_meta(note_id);
	`)
	if err != nil {
		return err
	}
	// Migration: add pre_cook_servings column to databases created before it existed.
	db.Exec(`ALTER TABLE ct_recipe_meta ADD COLUMN pre_cook_servings TEXT NOT NULL DEFAULT ''`)
	return nil
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

	// Upsert recipe metadata.
	freezableInt := 0
	if payload.Freezable {
		freezableInt = 1
	}
	if _, err := tx.Exec(`
		INSERT INTO ct_recipe_meta (note_id, servings, attention_time, total_time, grams_per_serving, kcal_per_serving, freezable, pre_cook_servings)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			servings          = excluded.servings,
			attention_time    = excluded.attention_time,
			total_time        = excluded.total_time,
			grams_per_serving = excluded.grams_per_serving,
			kcal_per_serving  = excluded.kcal_per_serving,
			freezable         = excluded.freezable,
			pre_cook_servings = excluded.pre_cook_servings`,
		noteID,
		strings.TrimSpace(payload.Servings),
		strings.TrimSpace(payload.AttentionTime),
		strings.TrimSpace(payload.TotalTime),
		strings.TrimSpace(payload.GramsPerServing),
		strings.TrimSpace(payload.KcalPerServing),
		freezableInt,
		strings.TrimSpace(payload.PreCookServings),
	); err != nil {
		return fmt.Errorf("recipe: upsert meta: %w", err)
	}

	return nil
}

func (p *RecipePlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// Load ingredients.
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

	// Load metadata.
	payload := Payload{Ingredients: ingredients}
	var freezableInt int
	err = db.QueryRow(
		`SELECT servings, attention_time, total_time, grams_per_serving, kcal_per_serving, freezable, pre_cook_servings
		 FROM ct_recipe_meta WHERE note_id = ?`,
		noteID,
	).Scan(
		&payload.Servings,
		&payload.AttentionTime,
		&payload.TotalTime,
		&payload.GramsPerServing,
		&payload.KcalPerServing,
		&freezableInt,
		&payload.PreCookServings,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("recipe: load meta: %w", err)
	}
	payload.Freezable = freezableInt != 0

	return payload, nil
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
	},
	{
		"$el": "h3",
		"children": "Details"
	},
	{
		"$formkit": "text",
		"name": "servings",
		"label": "Servings"
	},
	{
		"$formkit": "text",
		"name": "attention_time",
		"label": "Attention Time"
	},
	{
		"$formkit": "text",
		"name": "total_time",
		"label": "Total Time"
	},
	{
		"$formkit": "text",
		"name": "grams_per_serving",
		"label": "Grams per Serving"
	},
	{
		"$formkit": "text",
		"name": "kcal_per_serving",
		"label": "Kcal per Serving"
	},
	{
		"$formkit": "checkbox",
		"name": "freezable",
		"label": "Freezable"
	},
	{
		"$el": "h3",
		"children": "Meal Prep (freezable only)"
	},
	{
		"$formkit": "text",
		"name": "pre_cook_servings",
		"label": "Pre-Cook Servings"
	}
]`)
	return schema
}

func (p *RecipePlugin) CronJobs() []notetype.CronJob {
	return nil // no background jobs for individual recipes
}

// --- New interfaces ---

func (p *RecipePlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "recipe",
		Label:         "Recipe",
		Description:   "A recipe with ingredients, servings, and timing info",
		Category:      "Cooking",
		SortOrder:     200,
		DefaultConfig: json.RawMessage(`{"ingredients":[],"servings":"","attention_time":"","total_time":"","grams_per_serving":"","kcal_per_serving":"","freezable":false,"pre_cook_servings":""}`),
		Editor:        notetype.EditorMeta{Mode: "custom", Schema: p.UISchema()},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		HasConfig:     true,
		HasView:       false,
		HasActions:    false,
	}
}

func (p *RecipePlugin) ValidateConfig(payload json.RawMessage) error {
	return p.Validate(payload)
}

func (p *RecipePlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	return p.ProcessSave(ctx, tx, userID, noteID, config)
}

func (p *RecipePlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	result, err := p.ProcessLoad(ctx, db, userID, noteID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(result)
}
