// Package recipeoverview implements a "recipe_overview" note type that provides
// a dashboard to view all recipes and generate a grocery list for the coming 8 days.
package recipeoverview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "recipe_overview"

type RecipeOverviewPlugin struct{}

func init() {
	notetype.Register(&RecipeOverviewPlugin{})
}

func (p *RecipeOverviewPlugin) ID() string { return pluginID }

func (p *RecipeOverviewPlugin) InitSchema(db *sql.DB) error {
	// The overview itself doesn't need extra tables — it queries ct_recipe_ingredients.
	// But we create a table to store generated grocery lists.
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_recipe_overview_grocery_lists (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id     INTEGER NOT NULL,
			generated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			days        INTEGER NOT NULL DEFAULT 8,
			items_json  TEXT NOT NULL DEFAULT '[]',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_overview_gl_note
			ON ct_recipe_overview_grocery_lists(note_id);
	`)
	return err
}

func (p *RecipeOverviewPlugin) Validate(raw json.RawMessage) error {
	return nil // no custom payload needed
}

func (p *RecipeOverviewPlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
	return nil // nothing to persist beyond the note itself
}

// OverviewData is what the frontend receives when loading this note type.
type OverviewData struct {
	Recipes      []RecipeSummary `json:"recipes"`
	GroceryItems []GroceryItem   `json:"grocery_items"`
}

type RecipeSummary struct {
	NoteID          int64  `json:"note_id"`
	Title           string `json:"title"`
	IngredientCount int    `json:"ingredient_count"`
}

type GroceryItem struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
	Unit   string `json:"unit"`
}

func (p *RecipeOverviewPlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// Find all recipe notes (type='recipe') and summarize them.
	rows, err := db.Query(`
		SELECT n.id, n.title, COUNT(ri.id) AS ingredient_count
		FROM notes n
		LEFT JOIN ct_recipe_ingredients ri ON ri.note_id = n.id
		WHERE n.type = 'recipe'
		GROUP BY n.id
		ORDER BY n.title
	`)
	if err != nil {
		return nil, fmt.Errorf("recipe_overview: load recipes: %w", err)
	}
	defer rows.Close()

	recipes := []RecipeSummary{}
	for rows.Next() {
		var r RecipeSummary
		if err := rows.Scan(&r.NoteID, &r.Title, &r.IngredientCount); err != nil {
			return nil, fmt.Errorf("recipe_overview: scan recipe: %w", err)
		}
		recipes = append(recipes, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load the most recent grocery list for this overview note.
	items := []GroceryItem{}
	var itemsJSON string
	err = db.QueryRow(
		`SELECT items_json FROM ct_recipe_overview_grocery_lists WHERE note_id = ? ORDER BY generated_at DESC LIMIT 1`,
		noteID,
	).Scan(&itemsJSON)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("recipe_overview: load grocery list: %w", err)
	}
	if itemsJSON != "" {
		if err := json.Unmarshal([]byte(itemsJSON), &items); err != nil {
			return nil, fmt.Errorf("recipe_overview: unmarshal grocery items: %w", err)
		}
	}

	return &OverviewData{
		Recipes:      recipes,
		GroceryItems: items,
	}, nil
}

func (p *RecipeOverviewPlugin) UISchema() json.RawMessage {
	// The frontend renders this as a custom dashboard — we provide a minimal
	// schema that tells the frontend to render the overview component.
	schema := json.RawMessage(`[
	{
		"$el": "p",
		"children": "Shows all your recipes and lets you generate a grocery list for the next 8 days."
	}
]`)
	return schema
}

func (p *RecipeOverviewPlugin) CronJobs() []notetype.CronJob {
	// Grocery list generation is triggered on-demand from the frontend,
	// not on a cron schedule. But we could add a weekly auto-generation.
	return nil
}
