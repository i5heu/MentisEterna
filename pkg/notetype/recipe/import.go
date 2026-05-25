package recipe

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

type importRecipesJSONParams struct {
	ImportJSON string `json:"import_json"`
}

type importedIngredient struct {
	Name    string `json:"name"`
	Prepare string `json:"prepare"`
	Amount  string `json:"amount"`
	Unit    string `json:"unit"`
}

type importedRecipe struct {
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	Payload     Payload              `json:"custom_data"`
	Ingredients []importedIngredient `json:"ingredients"`
}

type importRecipesJSONResult struct {
	PrimaryNoteID  int64   `json:"primary_note_id"`
	CreatedNoteIDs []int64 `json:"created_note_ids"`
	ImportedCount  int     `json:"imported_count"`
}

func (p *RecipePlugin) importRecipesJSON(ctx context.Context, db *sql.DB, userID int, noteID int64, params json.RawMessage) (any, error) {
	var in importRecipesJSONParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, badImportRequest("recipe import request is invalid JSON")
		}
	}
	if strings.TrimSpace(in.ImportJSON) == "" {
		return nil, badImportRequest("Paste a JSON document to import. Expected a top-level object like {\"recipes\":[...]}.")
	}

	recipes, err := parseImportedRecipes(in.ImportJSON)
	if err != nil {
		return nil, err
	}
	if len(recipes) == 0 {
		return nil, badImportRequest("No recipes were found in the import JSON. Expected a non-empty recipes array.")
	}

	var currentTitle string
	var parentID sql.NullInt64
	if err := db.QueryRow(`SELECT title, parent_id FROM notes WHERE id = ?`, noteID).Scan(&currentTitle, &parentID); err != nil {
		return nil, fmt.Errorf("recipe: load target note: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("recipe: begin import tx: %w", err)
	}
	defer tx.Rollback()

	primary := recipes[0]
	if strings.TrimSpace(primary.Title) == "" {
		primary.Title = currentTitle
	}
	primaryBody := strings.TrimSpace(primary.Body)
	primaryConfig, err := json.Marshal(primary.Payload)
	if err != nil {
		return nil, fmt.Errorf("recipe: marshal primary payload: %w", err)
	}

	if _, err := tx.Exec(`UPDATE notes SET title = ? WHERE id = ?`, primary.Title, noteID); err != nil {
		return nil, fmt.Errorf("recipe: update primary note: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, primaryBody); err != nil {
		return nil, fmt.Errorf("recipe: save primary body: %w", err)
	}
	if err := p.SaveConfig(ctx, tx, userID, noteID, primaryConfig); err != nil {
		return nil, fmt.Errorf("recipe: save primary config: %w", err)
	}

	createdIDs := make([]int64, 0, len(recipes)-1)
	for i := 1; i < len(recipes); i++ {
		r := recipes[i]
		res, err := tx.Exec(`INSERT INTO notes (title, parent_id, type) VALUES (?, ?, ?)`, r.Title, nullableParentID(parentID), pluginID)
		if err != nil {
			return nil, fmt.Errorf("recipe: create recipe %d: %w", i+1, err)
		}
		newNoteID, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, newNoteID, strings.TrimSpace(r.Body)); err != nil {
			return nil, fmt.Errorf("recipe: save recipe %d body: %w", i+1, err)
		}
		cfg, err := json.Marshal(r.Payload)
		if err != nil {
			return nil, fmt.Errorf("recipe: marshal recipe %d payload: %w", i+1, err)
		}
		if err := p.SaveConfig(ctx, tx, userID, newNoteID, cfg); err != nil {
			return nil, fmt.Errorf("recipe: save recipe %d config: %w", i+1, err)
		}
		createdIDs = append(createdIDs, newNoteID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("recipe: commit import: %w", err)
	}

	return importRecipesJSONResult{
		PrimaryNoteID:  noteID,
		CreatedNoteIDs: createdIDs,
		ImportedCount:  len(recipes),
	}, nil
}

func nullableParentID(parentID sql.NullInt64) any {
	if parentID.Valid {
		return parentID.Int64
	}
	return nil
}

func parseImportedRecipes(raw string) ([]importedRecipe, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, helpfulImportJSONError(err)
	}

	recipeVals, ok := doc["recipes"].([]any)
	if !ok || len(recipeVals) == 0 {
		return nil, badImportRequest("Expected a top-level object with a non-empty recipes array, for example {\"recipes\":[{\"title\":\"Tomato Soup\",\"body\":\"Simmer and blend.\",\"ingredients\":[{\"name\":\"Tomatoes\",\"amount\":500,\"unit\":\"g\"}]}]}")
	}

	out := make([]importedRecipe, 0, len(recipeVals))
	for i, rawRecipe := range recipeVals {
		recipeMap, ok := rawRecipe.(map[string]any)
		if !ok {
			return nil, badImportRequest("Recipe %d must be a JSON object.", i+1)
		}

		payload, ingredients, err := payloadFromImportedRecipeMap(recipeMap, i+1)
		if err != nil {
			return nil, err
		}
		recipe := importedRecipe{
			Title:       stringFromAny(recipeMap["title"]),
			Body:        stringFromAny(recipeMap["body"]),
			Payload:     payload,
			Ingredients: ingredients,
		}
		if !importedRecipeHasContent(recipe) {
			return nil, badImportRequest("Recipe %d is empty. Provide at least a title, body, ingredients, or recipe details.", i+1)
		}
		cfg, err := json.Marshal(recipe.Payload)
		if err != nil {
			return nil, fmt.Errorf("recipe: marshal recipe %d payload: %w", i+1, err)
		}
		plugin := &RecipePlugin{}
		if err := plugin.ValidateConfig(cfg); err != nil {
			return nil, badImportRequest("Recipe %d: %v", i+1, err)
		}
		out = append(out, recipe)
	}

	return out, nil
}

func payloadFromImportedRecipeMap(recipeMap map[string]any, index int) (Payload, []importedIngredient, error) {
	rating, err := ratingFromAny(recipeMap["rating"])
	if err != nil {
		return Payload{}, nil, badImportRequest("Recipe %d: %v", index, err)
	}

	ingVals, ok := recipeMap["ingredients"]
	if !ok || ingVals == nil {
		return Payload{
			Ingredients:     []IngredientRow{},
			Servings:        stringFromAny(recipeMap["servings"]),
			AttentionTime:   stringFromAny(recipeMap["attention_time"]),
			TotalTime:       stringFromAny(recipeMap["total_time"]),
			GramsPerServing: stringFromAny(recipeMap["grams_per_serving"]),
			KcalPerServing:  stringFromAny(recipeMap["kcal_per_serving"]),
			Rating:          rating,
			Freezable:       boolFromAny(recipeMap["freezable"]),
			PreCookServings: stringFromAny(recipeMap["pre_cook_servings"]),
		}, []importedIngredient{}, nil
	}

	ingArr, ok := ingVals.([]any)
	if !ok {
		return Payload{}, nil, badImportRequest("Recipe %d: ingredients must be an array.", index)
	}

	rows := make([]IngredientRow, 0, len(ingArr))
	imported := make([]importedIngredient, 0, len(ingArr))
	for j, rawIngredient := range ingArr {
		ingMap, ok := rawIngredient.(map[string]any)
		if !ok {
			return Payload{}, nil, badImportRequest("Recipe %d: ingredient %d must be an object.", index, j+1)
		}
		name := stringFromAny(ingMap["name"])
		if strings.TrimSpace(name) == "" {
			return Payload{}, nil, badImportRequest("Recipe %d: ingredient %d is missing a name.", index, j+1)
		}
		prepare := stringFromAny(ingMap["prepare"])
		amount := stringFromAny(ingMap["amount"])
		unit := stringFromAny(ingMap["unit"])
		nonMetricAmount := stringFromAny(ingMap["non_metric_amount"])
		nonMetricUnit := stringFromAny(ingMap["non_metric_unit"])
		metricValidated := boolFromAny(ingMap["metric_validated"])
		rows = append(rows, IngredientRow{
			Name:            name,
			Prepare:         prepare,
			Amount:          amount,
			Unit:            unit,
			NonMetricAmount: nonMetricAmount,
			NonMetricUnit:   nonMetricUnit,
			MetricValidated: metricValidated,
		})
		imported = append(imported, importedIngredient{Name: name, Prepare: prepare, Amount: amount, Unit: unit})
	}

	return Payload{
		Ingredients:     rows,
		Servings:        stringFromAny(recipeMap["servings"]),
		AttentionTime:   stringFromAny(recipeMap["attention_time"]),
		TotalTime:       stringFromAny(recipeMap["total_time"]),
		GramsPerServing: stringFromAny(recipeMap["grams_per_serving"]),
		KcalPerServing:  stringFromAny(recipeMap["kcal_per_serving"]),
		Rating:          rating,
		Freezable:       boolFromAny(recipeMap["freezable"]),
		PreCookServings: stringFromAny(recipeMap["pre_cook_servings"]),
	}, imported, nil
}

func importedRecipeHasContent(recipe importedRecipe) bool {
	if strings.TrimSpace(recipe.Title) != "" || strings.TrimSpace(recipe.Body) != "" ||
		strings.TrimSpace(recipe.Payload.Servings) != "" ||
		strings.TrimSpace(recipe.Payload.AttentionTime) != "" ||
		strings.TrimSpace(recipe.Payload.TotalTime) != "" ||
		strings.TrimSpace(recipe.Payload.GramsPerServing) != "" ||
		strings.TrimSpace(recipe.Payload.KcalPerServing) != "" ||
		recipe.Payload.Rating > 0 ||
		recipe.Payload.Freezable ||
		strings.TrimSpace(recipe.Payload.PreCookServings) != "" {
		return true
	}

	for _, ing := range recipe.Payload.Ingredients {
		if strings.TrimSpace(ing.Name) != "" ||
			strings.TrimSpace(ing.Prepare) != "" ||
			strings.TrimSpace(ing.Amount) != "" ||
			strings.TrimSpace(ing.Unit) != "" ||
			strings.TrimSpace(ing.NonMetricAmount) != "" ||
			strings.TrimSpace(ing.NonMetricUnit) != "" ||
			ing.MetricValidated {
			return true
		}
	}
	return false
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		if math.Trunc(x) == x {
			return fmt.Sprintf("%.0f", x)
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func ratingFromAny(v any) (int, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, fmt.Errorf("rating must be an integer between 0 and 10")
		}
		return n, nil
	case float64:
		if math.Trunc(x) != x {
			return 0, fmt.Errorf("rating must be an integer between 0 and 10")
		}
		return int(x), nil
	default:
		return 0, fmt.Errorf("rating must be an integer between 0 and 10")
	}
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		x = strings.TrimSpace(strings.ToLower(x))
		return x == "true" || x == "1" || x == "yes"
	case float64:
		return x != 0
	default:
		return false
	}
}

func helpfulImportJSONError(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return badImportRequest("Import JSON is invalid near character %d. Expected a document like {\"recipes\":[{\"title\":\"Tomato Soup\",\"body\":\"Simmer and blend.\",\"ingredients\":[{\"name\":\"Tomatoes\",\"amount\":500,\"unit\":\"g\"}]}]}", syntaxErr.Offset)
	}

	if errors.Is(err, io.EOF) {
		return badImportRequest("Import JSON is empty. Paste a document like {\"recipes\":[{\"title\":\"Tomato Soup\",\"ingredients\":[{\"name\":\"Tomatoes\",\"amount\":500,\"unit\":\"g\"}]}]}")
	}

	return badImportRequest("Import JSON is not valid JSON: %v. Expected a top-level object with a recipes array.", err)
}

func badImportRequest(format string, args ...any) error {
	return &notetype.BadRequestError{Message: fmt.Sprintf(format, args...)}
}
