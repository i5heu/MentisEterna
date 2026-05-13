// Package recipeoverview implements a "recipe_overview" note type that provides
// a dashboard to view all recipes, select which ones to include, configure days
// and people, and generate a grocery list. Past grocery lists are kept and can
// be deleted. Recipes that appeared in a grocery list within the last 3 weeks
// are flagged.
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
	// Table for generated grocery lists — now includes num_people.
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_recipe_overview_grocery_lists (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id      INTEGER NOT NULL,
			generated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			num_days     INTEGER NOT NULL DEFAULT 8,
			num_people   INTEGER NOT NULL DEFAULT 1,
			items_json   TEXT NOT NULL DEFAULT '[]',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_overview_gl_note
			ON ct_recipe_overview_grocery_lists(note_id);
	`)
	if err != nil {
		return err
	}

	// Migrations for databases created before these columns existed.
	// Old schema: (id, note_id, generated_at, days, items_json)
	// New schema: (id, note_id, generated_at, num_days, num_people, items_json)
	// Ignore errors — columns may already exist or be renamed.
	db.Exec(`ALTER TABLE ct_recipe_overview_grocery_lists RENAME COLUMN days TO num_days`)
	db.Exec(`ALTER TABLE ct_recipe_overview_grocery_lists ADD COLUMN num_days INTEGER NOT NULL DEFAULT 8`)
	db.Exec(`ALTER TABLE ct_recipe_overview_grocery_lists ADD COLUMN num_people INTEGER NOT NULL DEFAULT 1`)

	// Junction table: which recipes are in which grocery list.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_recipe_overview_grocery_list_recipes (
			grocery_list_id INTEGER NOT NULL,
			recipe_note_id  INTEGER NOT NULL,
			PRIMARY KEY(grocery_list_id, recipe_note_id),
			FOREIGN KEY(grocery_list_id) REFERENCES ct_recipe_overview_grocery_lists(id) ON DELETE CASCADE,
			FOREIGN KEY(recipe_note_id) REFERENCES notes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_overview_glr_list
			ON ct_recipe_overview_grocery_list_recipes(grocery_list_id);
		CREATE INDEX IF NOT EXISTS idx_ct_recipe_overview_glr_recipe
			ON ct_recipe_overview_grocery_list_recipes(recipe_note_id);
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
	GroceryLists []GroceryList   `json:"grocery_lists"`
}

type RecipeSummary struct {
	NoteID          int64  `json:"note_id"`
	Title           string `json:"title"`
	IngredientCount int    `json:"ingredient_count"`
	InRecentList    bool   `json:"in_recent_list"` // true if this recipe appeared in any grocery list in the last 3 weeks
	Freezable       bool   `json:"freezable"`
	PreCookServings string `json:"pre_cook_servings"`
}

type GroceryList struct {
	ID          int64         `json:"id"`
	GeneratedAt string        `json:"generated_at"`
	NumDays     int           `json:"num_days"`
	NumPeople   int           `json:"num_people"`
	RecipeIDs   []int64       `json:"recipe_ids"`
	RecipeNames []string      `json:"recipe_names"`
	Items       []GroceryItem `json:"items"`
}

type GroceryItem struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
	Unit   string `json:"unit"`
}

func (p *RecipeOverviewPlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// 1. Find all recipe notes and summarize them, flagging those in recent lists.
	rows, err := db.Query(`
		SELECT n.id, n.title, COUNT(ri.id) AS ingredient_count,
			EXISTS(
				SELECT 1 FROM ct_recipe_overview_grocery_list_recipes glr
				JOIN ct_recipe_overview_grocery_lists gl ON gl.id = glr.grocery_list_id
				WHERE glr.recipe_note_id = n.id
				AND gl.generated_at >= datetime('now', '-21 days')
			) AS in_recent_list,
			COALESCE(rm.freezable, 0) AS freezable,
			COALESCE(rm.pre_cook_servings, '') AS pre_cook_servings
		FROM notes n
		LEFT JOIN ct_recipe_ingredients ri ON ri.note_id = n.id
		LEFT JOIN ct_recipe_meta rm ON rm.note_id = n.id
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
		var freezableInt int
		if err := rows.Scan(&r.NoteID, &r.Title, &r.IngredientCount, &r.InRecentList, &freezableInt, &r.PreCookServings); err != nil {
			return nil, fmt.Errorf("recipe_overview: scan recipe: %w", err)
		}
		r.Freezable = freezableInt != 0
		recipes = append(recipes, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. Load all past grocery lists for this overview note (newest first).
	listRows, err := db.Query(`
		SELECT id, generated_at, num_days, num_people, items_json
		FROM ct_recipe_overview_grocery_lists
		WHERE note_id = ?
		ORDER BY generated_at DESC
	`, noteID)
	if err != nil {
		return nil, fmt.Errorf("recipe_overview: load grocery lists: %w", err)
	}
	defer listRows.Close()

	lists := []GroceryList{}
	for listRows.Next() {
		var gl GroceryList
		var itemsJSON string
		if err := listRows.Scan(&gl.ID, &gl.GeneratedAt, &gl.NumDays, &gl.NumPeople, &itemsJSON); err != nil {
			return nil, fmt.Errorf("recipe_overview: scan grocery list: %w", err)
		}
		if itemsJSON != "" {
			if err := json.Unmarshal([]byte(itemsJSON), &gl.Items); err != nil {
				return nil, fmt.Errorf("recipe_overview: unmarshal grocery items: %w", err)
			}
		}

		// Load associated recipe IDs and titles for this grocery list.
		recipeRows, err := db.Query(`
			SELECT glr.recipe_note_id, n.title
			FROM ct_recipe_overview_grocery_list_recipes glr
			JOIN notes n ON n.id = glr.recipe_note_id
			WHERE glr.grocery_list_id = ?
			ORDER BY glr.recipe_note_id
		`, gl.ID)
		if err != nil {
			return nil, fmt.Errorf("recipe_overview: load list recipes: %w", err)
		}
		gl.RecipeIDs = []int64{}
		gl.RecipeNames = []string{}
		for recipeRows.Next() {
			var rid int64
			var title string
			if err := recipeRows.Scan(&rid, &title); err != nil {
				recipeRows.Close()
				return nil, fmt.Errorf("recipe_overview: scan list recipe: %w", err)
			}
			gl.RecipeIDs = append(gl.RecipeIDs, rid)
			gl.RecipeNames = append(gl.RecipeNames, title)
		}
		recipeRows.Close()

		lists = append(lists, gl)
	}
	if err := listRows.Err(); err != nil {
		return nil, err
	}

	return &OverviewData{
		Recipes:      recipes,
		GroceryLists: lists,
	}, nil
}

func (p *RecipeOverviewPlugin) UISchema() json.RawMessage {
	// The frontend renders this as a custom dashboard.
	schema := json.RawMessage(`[
	{
		"$el": "p",
		"children": "Shows all your recipes and lets you generate a grocery list."
	}
]`)
	return schema
}

func (p *RecipeOverviewPlugin) CronJobs() []notetype.CronJob {
	return nil
}
