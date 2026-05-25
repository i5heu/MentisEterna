package recipe

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRecipePlugin(t *testing.T) {
	plugintest.Run(t, &RecipePlugin{}, plugintest.TestData{
		ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"g","non_metric_amount":"1","non_metric_unit":"cup","metric_validated":true},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","freezable":true,"pre_cook_servings":"8"}`,
		InvalidPayload: `{"ingredients":[{"name":"","amount":"2","unit":"g"}]}`,
	})
}

func TestValidateConfigAllowsMilligrams(t *testing.T) {
	plugin := &RecipePlugin{}
	err := plugin.ValidateConfig(json.RawMessage(`{"ingredients":[{"name":"Salt","amount":"500","unit":"mg"}]}`))
	if err != nil {
		t.Fatalf("expected mg unit to be valid, got: %v", err)
	}
}

func TestValidateConfigAllowsNonMetricFields(t *testing.T) {
	plugin := &RecipePlugin{}
	err := plugin.ValidateConfig(json.RawMessage(`{"ingredients":[{"name":"Sugar","amount":"200","unit":"g","non_metric_amount":"1","non_metric_unit":"cup","metric_validated":true}]}`))
	if err != nil {
		t.Fatalf("expected non-metric fields to be valid, got: %v", err)
	}
}

func TestRecipeTextPrint(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Flour","amount":"2","unit":"g"},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","freezable":true,"pre_cook_servings":"8"}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := RecipeTextPrint(payload, "My Cake", "Preheat oven to 180C.\nMix ingredients well.")

	// Basic structure checks.
	if !strings.Contains(out, "My Cake") {
		t.Error("missing title")
	}
	if !strings.Contains(out, "Flour") {
		t.Error("missing ingredient Flour")
	}
	if !strings.Contains(out, "Eggs") {
		t.Error("missing ingredient Eggs")
	}
	if !strings.Contains(out, "2 g") {
		t.Error("missing amount+unit for Flour")
	}
	if !strings.Contains(out, "3 pcs") {
		t.Error("missing amount+unit for Eggs")
	}
	if !strings.Contains(out, "Servings") {
		t.Error("missing Servings detail")
	}
	if !strings.Contains(out, "4") {
		t.Error("missing servings value")
	}
	if !strings.Contains(out, "Freezable: yes") {
		t.Error("missing Freezable detail")
	}
	if !strings.Contains(out, "Pre-cook") {
		t.Error("missing Pre-cook detail")
	}
	if !strings.Contains(out, "8") {
		t.Error("missing pre-cook value")
	}
	if !strings.Contains(out, "Notes") {
		t.Error("missing Notes section")
	}
	if !strings.Contains(out, "Preheat oven") {
		t.Error("missing body text")
	}

	t.Logf("Text output:\n%s", out)
}

func TestFormatRecipeReceipt(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Sugar","amount":"100","unit":"g"}],"servings":"2","attention_time":"","total_time":"30m","grams_per_serving":"","kcal_per_serving":"","freezable":false,"pre_cook_servings":""}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	buf := FormatRecipeReceipt(payload, "Cookies", "Bake at 180C for 12 minutes.")
	b := buf.Bytes()
	if len(b) == 0 {
		t.Fatal("empty buffer")
	}

	// Should start with ESC @ (init).
	if b[0] != 0x1B || b[1] != '@' {
		t.Error("buffer should start with ESC @")
	}

	// Should contain the title text.
	s := string(b)
	if !strings.Contains(s, "Cookies") {
		t.Error("missing title in buffer")
	}
	if !strings.Contains(s, "Sugar") {
		t.Error("missing ingredient")
	}

	t.Logf("Buffer length: %d bytes", len(b))
}

func TestImportRecipesJSONAction(t *testing.T) {
	d := plugintest.DB(t, &RecipePlugin{})
	defer d.Close()

	parentRes, err := d.Exec(`INSERT INTO notes (title, type) VALUES ('Meals', 'standard')`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parentID, _ := parentRes.LastInsertId()

	noteID := plugintest.CreateNote(t, d, "Existing Recipe", &RecipePlugin{})
	if _, err := d.Exec(`UPDATE notes SET parent_id = ? WHERE id = ?`, parentID, noteID); err != nil {
		t.Fatalf("set parent: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, `Old body`); err != nil {
		t.Fatalf("insert old body: %v", err)
	}

	importDoc, err := json.Marshal(map[string]any{
		"recipes": []map[string]any{
			{
				"title": "Imported Chili",
				"body":  "Simmer slowly.",
				"ingredients": []map[string]any{
					{"name": "Beans", "amount": 2, "unit": "pcs", "non_metric_amount": 1, "non_metric_unit": "cup", "metric_validated": true},
				},
				"servings": 4,
			},
			{
				"title": "Imported Soup",
				"body":  "Blend and serve.",
				"ingredients": []map[string]any{
					{"name": "Tomatoes", "amount": 500, "unit": "g"},
				},
				"total_time": "25m",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal import doc: %v", err)
	}
	params, err := json.Marshal(map[string]any{"import_json": string(importDoc)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	plugin := &RecipePlugin{}
	result, err := plugin.HandleAction(context.Background(), d.DB, 0, noteID, "import_recipes_json", params)
	if err != nil {
		t.Fatalf("HandleAction import_recipes_json: %v", err)
	}
	out, ok := result.(importRecipesJSONResult)
	if !ok {
		t.Fatalf("expected importRecipesJSONResult, got %T", result)
	}
	if out.PrimaryNoteID != noteID {
		t.Fatalf("expected primary note id %d, got %d", noteID, out.PrimaryNoteID)
	}
	if out.ImportedCount != 2 {
		t.Fatalf("expected imported count 2, got %d", out.ImportedCount)
	}
	if len(out.CreatedNoteIDs) != 1 {
		t.Fatalf("expected 1 created note id, got %d", len(out.CreatedNoteIDs))
	}

	var title string
	var latestBody string
	if err := d.QueryRow(`SELECT title FROM notes WHERE id = ?`, noteID).Scan(&title); err != nil {
		t.Fatalf("query updated title: %v", err)
	}
	if err := d.QueryRow(`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID).Scan(&latestBody); err != nil {
		t.Fatalf("query updated body: %v", err)
	}
	if title != "Imported Chili" {
		t.Fatalf("expected updated title %q, got %q", "Imported Chili", title)
	}
	if latestBody != "Simmer slowly." {
		t.Fatalf("expected updated body %q, got %q", "Simmer slowly.", latestBody)
	}

	var ingredientCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_recipe_ingredients WHERE note_id = ?`, noteID).Scan(&ingredientCount); err != nil {
		t.Fatalf("query primary ingredient count: %v", err)
	}
	if ingredientCount != 1 {
		t.Fatalf("expected 1 primary ingredient, got %d", ingredientCount)
	}

	var nonMetricAmount, nonMetricUnit string
	var metricValidated int
	if err := d.QueryRow(`SELECT non_metric_amount, non_metric_unit, metric_validated FROM ct_recipe_ingredients WHERE note_id = ? LIMIT 1`, noteID).Scan(&nonMetricAmount, &nonMetricUnit, &metricValidated); err != nil {
		t.Fatalf("query primary ingredient extended fields: %v", err)
	}
	if nonMetricAmount != "1" || nonMetricUnit != "cup" || metricValidated != 1 {
		t.Fatalf("expected imported non-metric fields to persist, got amount=%q unit=%q validated=%d", nonMetricAmount, nonMetricUnit, metricValidated)
	}

	createdID := out.CreatedNoteIDs[0]
	var createdTitle string
	var createdParentID int64
	if err := d.QueryRow(`SELECT title, parent_id FROM notes WHERE id = ?`, createdID).Scan(&createdTitle, &createdParentID); err != nil {
		t.Fatalf("query created note: %v", err)
	}
	if createdTitle != "Imported Soup" {
		t.Fatalf("expected created title %q, got %q", "Imported Soup", createdTitle)
	}
	if createdParentID != parentID {
		t.Fatalf("expected created parent id %d, got %d", parentID, createdParentID)
	}
}
