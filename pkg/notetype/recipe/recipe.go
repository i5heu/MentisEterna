// Package recipe implements a "recipe" note type that stores a recipe as
// multiple ingredient rows (name, amount, unit) in its own table.
package recipe

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/printer"
)

const pluginID = "recipe"

var validUnits = map[string]bool{
	"g":   true,
	"kg":  true,
	"ml":  true,
	"l":   true,
	"pcs": true,
}

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

func (p *RecipePlugin) CronJobs() []notetype.CronJob {
	return nil // no background jobs for individual recipes
}

func (p *RecipePlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "recipe",
		Label:         "Recipe",
		Description:   "A recipe with ingredients, servings, and timing info",
		Category:      "Cooking",
		SortOrder:     200,
		DefaultConfig: json.RawMessage(`{"ingredients":[],"servings":"","attention_time":"","total_time":"","grams_per_serving":"","kcal_per_serving":"","freezable":false,"pre_cook_servings":""}`),
		Editor: notetype.EditorMeta{Mode: "custom", Schema: json.RawMessage(`[
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
]`)},
		Viewer:     notetype.ViewerMeta{Mode: "custom"},
		HasConfig:  true,
		HasView:    false,
		HasActions: true,
		Actions: []notetype.ActionMeta{
			{
				ID:              "print_recipe",
				Label:           "Print Recipe",
				Description:     "Format and print this recipe on the thermal receipt printer",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"device_path":{"type":"string","description":"Optional path to the printer device (e.g. /dev/usb/lp0)"}},"additionalProperties":false}`),
				Dangerous:       false,
				RefreshStrategy: "none",
				SuccessMessage:  "Recipe printed",
			},
			{
				ID:              "import_recipes_json",
				Label:           "Import Recipes JSON",
				Description:     "Replace this recipe with the first imported recipe and create additional recipe notes for the remaining items",
				ParamsSchema:    json.RawMessage(`{"type":"object","properties":{"import_json":{"type":"string","description":"JSON document containing a top-level recipes array"}},"required":["import_json"],"additionalProperties":false}`),
				Dangerous:       false,
				RefreshStrategy: "reload",
				SuccessMessage:  "Recipes imported",
			},
		},
	}
}

func (p *RecipePlugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil // optional
	}
	var pld Payload
	if err := json.Unmarshal(payload, &pld); err != nil {
		return fmt.Errorf("recipe: invalid payload: %w", err)
	}
	for i, ing := range pld.Ingredients {
		if strings.TrimSpace(ing.Name) == "" {
			return fmt.Errorf("recipe: ingredient %d: name is required", i+1)
		}
		unit := strings.TrimSpace(ing.Unit)
		if !validUnits[unit] {
			return fmt.Errorf("recipe: ingredient %d: unit %q is not a valid metric unit (use g, kg, ml, l, pcs)", i+1, unit)
		}
		if strings.Contains(ing.Amount, ",") {
			return fmt.Errorf("recipe: ingredient %d: amount must use dot as decimal separator, not comma", i+1)
		}
	}
	return nil
}

func (p *RecipePlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	if len(config) == 0 {
		return nil
	}
	var payload Payload
	if err := json.Unmarshal(config, &payload); err != nil {
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

// HandleAction implements the notetype.ActionHandler interface.
func (p *RecipePlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	switch actionID {
	case "print_recipe":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return p.printRecipe(ctx, db, userID, noteID, params)
	case "import_recipes_json":
		if db == nil {
			return nil, fmt.Errorf("no database available")
		}
		return p.importRecipesJSON(ctx, db, userID, noteID, params)
	default:
		return nil, fmt.Errorf("%w: %s", notetype.ErrUnknownAction, actionID)
	}
}

// printRecipeParams is the JSON body for the print_recipe action.
type printRecipeParams struct {
	DevicePath string `json:"device_path"`
}

// printRecipe loads the recipe config + note title, formats it for the
// thermal printer, and sends it to the device.
func (p *RecipePlugin) printRecipe(ctx context.Context, db *sql.DB, userID int, noteID int64, params json.RawMessage) (any, error) {
	var pr printRecipeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &pr); err != nil {
			return nil, fmt.Errorf("recipe: invalid print params: %w", err)
		}
	}

	// Load the recipe payload.
	config, err := p.LoadConfig(ctx, db, userID, noteID)
	if err != nil {
		return nil, fmt.Errorf("recipe: load config for print: %w", err)
	}

	var payload Payload
	if err := json.Unmarshal(config, &payload); err != nil {
		return nil, fmt.Errorf("recipe: unmarshal config for print: %w", err)
	}

	// Get the note title and latest body.
	var title, body string
	if err := db.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		return nil, fmt.Errorf("recipe: get title for print: %w", err)
	}
	// Latest body from the updates table.
	db.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID).Scan(&body)

	// Build the ESC/POS buffer.
	buf := FormatRecipeReceipt(payload, title, body)

	// Connect to the printer.
	var prDev printer.Printer
	if pr.DevicePath != "" {
		prDev, err = printer.NewFilePrinter(pr.DevicePath)
	} else {
		prDev, err = printer.FindPrinter()
	}
	if err != nil {
		// If printer isn't available, return a plain-text preview instead.
		preview := RecipeTextPrint(payload, title, body)
		log.Printf("printer not available (%v), returning preview", err)
		return map[string]any{
			"printed": false,
			"preview": preview,
			"error":   err.Error(),
		}, nil
	}

	// Send and cut.
	if err := printer.SendAndCut(prDev, buf); err != nil {
		return nil, fmt.Errorf("recipe: send to printer: %w", err)
	}

	return map[string]any{
		"printed": true,
		"device":  pr.DevicePath,
	}, nil
}

func (p *RecipePlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
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

	result, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("recipe: marshal payload: %w", err)
	}
	return json.RawMessage(result), nil
}
