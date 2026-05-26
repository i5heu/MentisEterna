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
	"regexp"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/recipe"
)

const pluginID = "recipe_overview"

var firstMarkdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+(?:\s+[^)]*)?)\)`)

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

// OverviewData is what the frontend receives when loading this note type.
type OverviewData struct {
	Recipes            []RecipeSummary     `json:"recipes"`
	GroceryLists       []GroceryList       `json:"grocery_lists"`
	UnvalidIngredients []UnvalidIngredient `json:"unvalid_ingredients"`
}

type RecipeSummary struct {
	NoteID          int64  `json:"note_id"`
	Title           string `json:"title"`
	IngredientCount int    `json:"ingredient_count"`
	InRecentList    bool   `json:"in_recent_list"` // true if this recipe appeared in any grocery list in the last 3 weeks
	Rating          int    `json:"rating"`
	Freezable       bool   `json:"freezable"`
	PreCookServings string `json:"pre_cook_servings"`
	IsPantry        bool   `json:"is_pantry"`
	ThumbnailURL    string `json:"thumbnail_url,omitempty"`
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
	Category string `json:"category,omitempty"`
	Name     string `json:"name"`
	Amount   string `json:"amount"`
	Unit     string `json:"unit"`
}

type UnvalidIngredient struct {
	RecipeNoteID    int64  `json:"recipe_note_id"`
	RecipeTitle     string `json:"recipe_title"`
	IngredientID    int64  `json:"ingredient_id"`
	IngredientName  string `json:"ingredient_name"`
	Amount          string `json:"amount"`
	Unit            string `json:"unit"`
	NonMetricAmount string `json:"non_metric_amount"`
	NonMetricUnit   string `json:"non_metric_unit"`
	MetricValidated bool   `json:"metric_validated"`
	IssueType       string `json:"issue_type"`
}

func extractFirstMarkdownImageURL(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	match := firstMarkdownImagePattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	url := strings.TrimSpace(match[1])
	if idx := strings.IndexAny(url, " \t\n"); idx >= 0 {
		url = url[:idx]
	}
	return strings.Trim(url, "<>")
}

func (p *RecipeOverviewPlugin) CronJobs() []notetype.CronJob {
	return nil
}

// --- New interfaces ---

func (p *RecipeOverviewPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "recipe_overview",
		Label:         "Recipe Overview",
		Description:   "Dashboard to view recipes and generate grocery lists",
		Category:      "Cooking",
		SortOrder:     250,
		DefaultConfig: json.RawMessage(`{}`),
		Editor:        notetype.EditorMeta{Mode: "custom"},
		Viewer:        notetype.ViewerMeta{Mode: "custom"},
		Actions: []notetype.ActionMeta{
			{
				ID:              "generate_grocery_list",
				Label:           "Generate Grocery List",
				Description:     "Generate a grocery list from selected recipes",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"recipe_ids":{"type":"array","items":{"type":"integer"}},"num_days":{"type":"integer"},"num_people":{"type":"integer"}}}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Grocery list generated",
			},
			{
				ID:              "delete_grocery_list",
				Label:           "Delete Grocery List",
				Description:     "Delete a grocery list",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"list_id":{"type":"integer"}},"required":["list_id"]}`),
				Dangerous:       true,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Grocery list deleted",
			},
			{
				ID:              "print_grocery_list",
				Label:           "Print Grocery List",
				Description:     "Format and print the latest grocery list on the thermal receipt printer",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"list_id":{"type":"integer"}},"required":["list_id"]}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Grocery list printed",
			},
			{
				ID:              "update_grocery_list",
				Label:           "Update Grocery List",
				Description:     "Update grocery list items after manual edits",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"list_id":{"type":"integer"},"items":{"type":"array","items":{"type":"object","properties":{"category":{"type":"string"},"name":{"type":"string"},"amount":{"type":"string"},"unit":{"type":"string"}},"required":["name"]}}},"required":["list_id","items"]}`),
				Dangerous:       false,
				RefreshStrategy: "reload_view",
				SuccessMessage:  "Grocery list updated",
			},
		},
		HasConfig:  false,
		HasView:    true,
		HasActions: true,
	}
}

func (p *RecipeOverviewPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// 1. Find all recipe notes and summarize them, flagging those in recent lists.
	rows, err := db.Query(`
		SELECT
			n.id,
			n.title,
			(
				SELECT COUNT(*)
				FROM ct_recipe_ingredients ri
				WHERE ri.note_id = n.id
			) AS ingredient_count,
			EXISTS(
				SELECT 1 FROM ct_recipe_overview_grocery_list_recipes glr
				JOIN ct_recipe_overview_grocery_lists gl ON gl.id = glr.grocery_list_id
				WHERE glr.recipe_note_id = n.id
				AND gl.generated_at >= datetime('now', '-21 days')
			) AS in_recent_list,
			COALESCE(rm.rating, 0) AS rating,
			COALESCE(rm.freezable, 0) AS freezable,
			COALESCE(rm.pre_cook_servings, '') AS pre_cook_servings,
			EXISTS(
				SELECT 1
				FROM tags_refs tr
				JOIN tags t ON t.id = tr.tag_id
				WHERE tr.note_id = n.id AND lower(t.name) = 'pantry'
			) AS is_pantry,
			COALESCE(u.body, '') AS body,
			COALESCE((
				SELECT printf('/file/%d/%d', n.id, f.id)
				FROM files f
				JOIN files_refs fr ON fr.file_id = f.id
				WHERE fr.note_id = n.id
				  AND f.deleted_at IS NULL
				  AND f.mime_type LIKE 'image/%'
				ORDER BY
					CASE fr.ref_kind WHEN 'inline' THEN 0 ELSE 1 END,
					fr.created_at,
					f.id
				LIMIT 1
			), '') AS fallback_thumbnail_url
		FROM notes n
		LEFT JOIN ct_recipe_meta rm ON rm.note_id = n.id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.type = 'recipe'
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
		var body string
		var fallbackThumbnailURL string
		if err := rows.Scan(&r.NoteID, &r.Title, &r.IngredientCount, &r.InRecentList, &r.Rating, &freezableInt, &r.PreCookServings, &r.IsPantry, &body, &fallbackThumbnailURL); err != nil {
			return nil, fmt.Errorf("recipe_overview: scan recipe: %w", err)
		}
		r.Freezable = freezableInt != 0
		r.ThumbnailURL = extractFirstMarkdownImageURL(body)
		if r.ThumbnailURL == "" {
			r.ThumbnailURL = fallbackThumbnailURL
		}
		recipes = append(recipes, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. Load ingredients that still need validation work across all recipes.
	ingredientRows, err := db.Query(`
		SELECT n.id, n.title, ri.id, ri.name, ri.amount, ri.unit,
			COALESCE(ri.non_metric_amount, ''),
			COALESCE(ri.non_metric_unit, ''),
			COALESCE(ri.metric_validated, 0)
		FROM notes n
		JOIN ct_recipe_ingredients ri ON ri.note_id = n.id
		WHERE n.type = 'recipe'
		  AND NOT EXISTS(
				SELECT 1
				FROM tags_refs tr
				JOIN tags t ON t.id = tr.tag_id
				WHERE tr.note_id = n.id AND lower(t.name) = 'pantry'
		  )
		ORDER BY n.title, ri.sort_order, ri.id
	`)
	if err != nil {
		return nil, fmt.Errorf("recipe_overview: load unvalid ingredients: %w", err)
	}
	defer ingredientRows.Close()

	unvalidIngredients := []UnvalidIngredient{}
	for ingredientRows.Next() {
		var item UnvalidIngredient
		var metricValidatedInt int
		if err := ingredientRows.Scan(&item.RecipeNoteID, &item.RecipeTitle, &item.IngredientID, &item.IngredientName, &item.Amount, &item.Unit, &item.NonMetricAmount, &item.NonMetricUnit, &metricValidatedInt); err != nil {
			return nil, fmt.Errorf("recipe_overview: scan unvalid ingredient: %w", err)
		}
		item.MetricValidated = metricValidatedInt != 0

		hasMetric := strings.TrimSpace(item.Amount) != "" && strings.TrimSpace(item.Unit) != ""
		hasNonMetric := strings.TrimSpace(item.NonMetricAmount) != "" && strings.TrimSpace(item.NonMetricUnit) != ""
		switch {
		case !hasMetric:
			item.IssueType = "missing_metric"
			unvalidIngredients = append(unvalidIngredients, item)
		case hasNonMetric && !item.MetricValidated:
			item.IssueType = "not_validated"
			unvalidIngredients = append(unvalidIngredients, item)
		}
	}
	if err := ingredientRows.Err(); err != nil {
		return nil, err
	}

	// 3. Load all past grocery lists for this overview note (newest first).
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
			for i := range gl.Items {
				gl.Items[i].Category = recipe.NormalizeIngredientCategory(gl.Items[i].Category)
			}
			sortGroceryItems(gl.Items)
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
		Recipes:            recipes,
		GroceryLists:       lists,
		UnvalidIngredients: unvalidIngredients,
	}, nil
}

func (p *RecipeOverviewPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "generate_grocery_list":
		return generateGroceryList(db, noteID, params)
	case "delete_grocery_list":
		return deleteGroceryList(db, params)
	case "list_grocery_lists":
		return listGroceryLists(db, noteID)
	case "print_grocery_list":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return printGroceryListAction(db, params)
	case "update_grocery_list":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return updateGroceryList(db, noteID, params)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}
