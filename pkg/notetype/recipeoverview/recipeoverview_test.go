package recipeoverview

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"github.com/i5heu/MentisEterna/pkg/notetype/recipe"
)

func TestRecipeOverviewPlugin(t *testing.T) {
	plugintest.Run(t, &RecipeOverviewPlugin{}, plugintest.TestData{})
}

func TestBuildView_WithRealRecipes(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	note1 := plugintest.CreateNote(t, d, "Chocolate Cake", recipePlugin)
	note2 := plugintest.CreateNote(t, d, "Tomato Soup", recipePlugin)

	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Flour', '2', 'g', 0)`, note1)
	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Sugar', '1', 'g', 1)`, note1)
	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Tomatoes', '5', 'ml', 0)`, note2)
	d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, rating) VALUES (?, 8), (?, 3)`, note1, note2)
	expectedBodyThumb := fmt.Sprintf("/file/%d/%d", note1, 101)
	d.DB.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, note1, fmt.Sprintf("![Cake](%s)\nStep 1", expectedBodyThumb))

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})

	plugin := &RecipeOverviewPlugin{}
	result, err := plugin.BuildView(context.Background(), d.DB, 0, overviewNote)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	data, ok := result.(*OverviewData)
	if !ok {
		t.Fatalf("expected *OverviewData, got %T", result)
	}

	if len(data.Recipes) == 0 {
		t.Error("expected at least 1 recipe, got 0")
	}
	if len(data.Recipes) != 2 {
		t.Errorf("expected 2 recipes, got %d", len(data.Recipes))
	}

	var cake *RecipeSummary
	for i := range data.Recipes {
		if data.Recipes[i].NoteID == note1 {
			cake = &data.Recipes[i]
			break
		}
	}
	if cake == nil {
		t.Fatalf("expected recipe %d in overview", note1)
	}
	if cake.ThumbnailURL != expectedBodyThumb {
		t.Fatalf("expected markdown thumbnail URL %q, got %q", expectedBodyThumb, cake.ThumbnailURL)
	}
	if cake.Rating != 8 {
		t.Fatalf("expected cake rating 8, got %d", cake.Rating)
	}
}

func TestBuildView_UsesImageAttachmentFallbackWhenBodyHasNoImage(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	noteID := plugintest.CreateNote(t, d, "Veggie Curry", recipePlugin)
	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Carrot', '2', 'pcs', 0)`, noteID)
	d.DB.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, noteID, `No inline images here.`)

	res, err := d.DB.Exec(`
		INSERT INTO files (original_note_id, storage_key, filename, mime_type, size_bytes, plaintext_sha256, ciphertext_sha256, aes_key, aes_nonce)
		VALUES (?, 'recipe-thumb', 'thumb.jpg', 'image/jpeg', 1234, 'aa', 'bb', x'0001', x'0002')
	`, noteID)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()
	if _, err := d.DB.Exec(`INSERT INTO files_refs (note_id, file_id, ref_kind) VALUES (?, ?, 'attachment')`, noteID, fileID); err != nil {
		t.Fatalf("insert file ref: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})

	plugin := &RecipeOverviewPlugin{}
	result, err := plugin.BuildView(context.Background(), d.DB, 0, overviewNote)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	data, ok := result.(*OverviewData)
	if !ok {
		t.Fatalf("expected *OverviewData, got %T", result)
	}

	if len(data.Recipes) != 1 {
		t.Fatalf("expected 1 recipe, got %d", len(data.Recipes))
	}
	expectedFallbackThumb := fmt.Sprintf("/file/%d/%d", noteID, fileID)
	if data.Recipes[0].ThumbnailURL != expectedFallbackThumb {
		t.Fatalf("expected fallback thumbnail URL %q, got %q", expectedFallbackThumb, data.Recipes[0].ThumbnailURL)
	}
}

func TestBuildView_MarksPantryRecipeAndExcludesItFromUnvalidIngredients(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	pantryNote := plugintest.CreateNote(t, d, "Pantry Staples", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Salt', '', '', '1', 'tablespoon', 0, 0)`, pantryNote); err != nil {
		t.Fatalf("insert pantry ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT OR IGNORE INTO tags (name) VALUES ('pantry')`); err != nil {
		t.Fatalf("insert pantry tag: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO tags_refs (note_id, tag_id) SELECT ?, id FROM tags WHERE name = 'pantry'`, pantryNote); err != nil {
		t.Fatalf("attach pantry tag: %v", err)
	}

	normalNote := plugintest.CreateNote(t, d, "Bread", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Flour', '', '', '1', 'cup', 0, 0)`, normalNote); err != nil {
		t.Fatalf("insert normal ingredient: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	plugin := &RecipeOverviewPlugin{}
	result, err := plugin.BuildView(context.Background(), d.DB, 0, overviewNote)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	data, ok := result.(*OverviewData)
	if !ok {
		t.Fatalf("expected *OverviewData, got %T", result)
	}

	var pantrySummary *RecipeSummary
	for i := range data.Recipes {
		if data.Recipes[i].NoteID == pantryNote {
			pantrySummary = &data.Recipes[i]
			break
		}
	}
	if pantrySummary == nil {
		t.Fatalf("expected pantry recipe %d in overview", pantryNote)
	}
	if !pantrySummary.IsPantry {
		t.Fatalf("expected pantry recipe to be marked as pantry: %+v", pantrySummary)
	}

	for _, item := range data.UnvalidIngredients {
		if item.RecipeNoteID == pantryNote {
			t.Fatalf("expected pantry ingredients to be excluded from unvalid list, got %+v", item)
		}
	}
	foundNormal := false
	for _, item := range data.UnvalidIngredients {
		if item.RecipeNoteID == normalNote {
			foundNormal = true
		}
	}
	if !foundNormal {
		t.Fatal("expected normal recipe ingredient to remain in unvalid list")
	}
}

func TestNormalizeMetricAmountSupportsMilligrams(t *testing.T) {
	tests := []struct {
		name       string
		amount     string
		unit       string
		wantAmount string
		wantUnit   string
	}{
		{name: "mg stays mg", amount: "500", unit: "mg", wantAmount: "500", wantUnit: "mg"},
		{name: "mg to g", amount: "1500", unit: "mg", wantAmount: "1.5", wantUnit: "g"},
		{name: "mg to kg", amount: "1500000", unit: "mg", wantAmount: "1.5", wantUnit: "kg"},
		{name: "fractional g to mg", amount: "0.5", unit: "g", wantAmount: "500", wantUnit: "mg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAmount, gotUnit := normalizeMetricAmount(tt.amount, tt.unit)
			if gotAmount != tt.wantAmount || gotUnit != tt.wantUnit {
				t.Fatalf("normalizeMetricAmount(%q, %q) = (%q, %q), want (%q, %q)", tt.amount, tt.unit, gotAmount, gotUnit, tt.wantAmount, tt.wantUnit)
			}
		})
	}
}

func TestGenerateGroceryListMergesMilligramsAndGrams(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	note1 := plugintest.CreateNote(t, d, "Vitamin Mix A", recipePlugin)
	note2 := plugintest.CreateNote(t, d, "Vitamin Mix B", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Vitamin C', '500', 'mg', '1', 'teaspoon', 1, 0)`, note1); err != nil {
		t.Fatalf("insert ingredient 1: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Vitamin C', '0.5', 'g', '0.5', 'teaspoon', 1, 0)`, note2); err != nil {
		t.Fatalf("insert ingredient 2: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1'), (?, '1')`, note1, note2); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	params := json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d,%d],"num_people":1}`, note1, note2))
	result, err := generateGroceryList(d.DB, overviewNote, params)
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}
	if len(gl.Items) != 1 {
		t.Fatalf("expected 1 grocery item, got %d", len(gl.Items))
	}
	if gl.Items[0].Name != "Vitamin C" || gl.Items[0].Amount != "1" || gl.Items[0].Unit != "g" {
		t.Fatalf("expected merged Vitamin C amount 1 g, got %+v", gl.Items[0])
	}
}

func TestGenerateGroceryListPrefersNonMetricWhenNotValidated(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	noteID := plugintest.CreateNote(t, d, "Spice Mix", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Paprika', '5', 'g', '1', 'tablespoon', 0, 0)`, noteID); err != nil {
		t.Fatalf("insert ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1')`, noteID); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	params := json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d],"num_people":1}`, noteID))
	result, err := generateGroceryList(d.DB, overviewNote, params)
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}
	if len(gl.Items) != 1 {
		t.Fatalf("expected 1 grocery item, got %d", len(gl.Items))
	}
	if gl.Items[0].Name != "Paprika" || gl.Items[0].Amount != "1" || gl.Items[0].Unit != "tablespoon" {
		t.Fatalf("expected unvalidated ingredient to use non-metric values, got %+v", gl.Items[0])
	}
}

func TestGenerateGroceryListUsesNonMetricWhenOnlyNonMetricExists(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	noteID := plugintest.CreateNote(t, d, "Sauce", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Soy Sauce', '', '', '2', 'tablespoon', 0, 0)`, noteID); err != nil {
		t.Fatalf("insert ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1')`, noteID); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	params := json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d],"num_people":1}`, noteID))
	result, err := generateGroceryList(d.DB, overviewNote, params)
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}
	if len(gl.Items) != 1 {
		t.Fatalf("expected 1 grocery item, got %d", len(gl.Items))
	}
	if gl.Items[0].Name != "Soy Sauce" || gl.Items[0].Amount != "2" || gl.Items[0].Unit != "tablespoon" {
		t.Fatalf("expected non-metric-only ingredient to use non-metric values, got %+v", gl.Items[0])
	}
}

func TestGenerateGroceryListIgnoresIngredientPrepare(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	noteID := plugintest.CreateNote(t, d, "Salad", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, prepare, amount, unit, metric_validated, sort_order) VALUES (?, 'Onion', 'finely chopped', '1', 'pcs', 1, 0)`, noteID); err != nil {
		t.Fatalf("insert ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1')`, noteID); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	params := json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d],"num_people":1}`, noteID))
	result, err := generateGroceryList(d.DB, overviewNote, params)
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}
	if len(gl.Items) != 1 {
		t.Fatalf("expected 1 grocery item, got %d", len(gl.Items))
	}
	if gl.Items[0].Name != "Onion" {
		t.Fatalf("expected grocery list to omit prepare text, got %+v", gl.Items[0])
	}
}

func TestGenerateGroceryListIncludesAndOrdersCategories(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	noteID := plugintest.CreateNote(t, d, "Shop", recipePlugin)
	if _, err := d.DB.Exec(`
		INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, metric_validated, grocery_category, sort_order)
		VALUES
			(?, 'Zucchini', '1', 'pcs', 1, 'vegetables', 0),
			(?, 'Carrot', '2', 'pcs', 1, 'vegetables', 1),
			(?, 'Milk', '1', 'l', 1, 'dairy', 2)
	`, noteID, noteID, noteID); err != nil {
		t.Fatalf("insert ingredients: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1')`, noteID); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	params := json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d],"num_people":1}`, noteID))
	result, err := generateGroceryList(d.DB, overviewNote, params)
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}

	if len(gl.Items) != 3 {
		t.Fatalf("expected 3 grocery items, got %d", len(gl.Items))
	}
	if gl.Items[0].Category != "vegetables" || gl.Items[0].Name != "Carrot" {
		t.Fatalf("unexpected first grocery item: %+v", gl.Items[0])
	}
	if gl.Items[1].Category != "vegetables" || gl.Items[1].Name != "Zucchini" {
		t.Fatalf("unexpected second grocery item: %+v", gl.Items[1])
	}
	if gl.Items[2].Category != "dairy" || gl.Items[2].Name != "Milk" {
		t.Fatalf("unexpected third grocery item: %+v", gl.Items[2])
	}
}

func TestUpdateGroceryListPersistsManualEdits(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	recipeNote := plugintest.CreateNote(t, d, "Pasta", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, metric_validated, sort_order) VALUES (?, 'Pasta', '200', 'g', 1, 0)`, recipeNote); err != nil {
		t.Fatalf("insert ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_meta (note_id, servings) VALUES (?, '1')`, recipeNote); err != nil {
		t.Fatalf("insert meta: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	generated, err := generateGroceryList(d.DB, overviewNote, json.RawMessage(fmt.Sprintf(`{"recipe_ids":[%d],"num_people":1}`, recipeNote)))
	if err != nil {
		t.Fatalf("generateGroceryList: %v", err)
	}

	payload, ok := generated.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", generated)
	}
	glRaw, ok := payload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in result")
	}
	encoded, err := json.Marshal(glRaw)
	if err != nil {
		t.Fatalf("marshal grocery list: %v", err)
	}
	var gl GroceryList
	if err := json.Unmarshal(encoded, &gl); err != nil {
		t.Fatalf("unmarshal grocery list: %v", err)
	}

	updated, err := updateGroceryList(d.DB, overviewNote, json.RawMessage(fmt.Sprintf(`{"list_id":%d,"items":[{"name":"Pasta","amount":"250","unit":"g"},{"name":"Olive Oil","amount":"1","unit":"bottle"},{"name":"","amount":"","unit":""}]}`, gl.ID)))
	if err != nil {
		t.Fatalf("updateGroceryList: %v", err)
	}

	updatedPayload, ok := updated.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", updated)
	}
	updatedRaw, ok := updatedPayload["grocery_list"]
	if !ok {
		t.Fatalf("expected grocery_list in update result")
	}
	updatedEncoded, err := json.Marshal(updatedRaw)
	if err != nil {
		t.Fatalf("marshal updated grocery list: %v", err)
	}
	var updatedList GroceryList
	if err := json.Unmarshal(updatedEncoded, &updatedList); err != nil {
		t.Fatalf("unmarshal updated grocery list: %v", err)
	}

	if len(updatedList.Items) != 2 {
		t.Fatalf("expected 2 items after update, got %d", len(updatedList.Items))
	}
	if updatedList.Items[0].Name != "Olive Oil" || updatedList.Items[0].Amount != "1" || updatedList.Items[0].Unit != "bottle" || updatedList.Items[0].Category != "other" {
		t.Fatalf("unexpected first item after update: %+v", updatedList.Items[0])
	}
	if updatedList.Items[1].Name != "Pasta" || updatedList.Items[1].Amount != "250" || updatedList.Items[1].Unit != "g" || updatedList.Items[1].Category != "other" {
		t.Fatalf("unexpected second item after update: %+v", updatedList.Items[1])
	}

	stored, err := loadGroceryListByID(d.DB, overviewNote, gl.ID)
	if err != nil {
		t.Fatalf("loadGroceryListByID: %v", err)
	}
	if len(stored.Items) != 2 || stored.Items[0].Name != "Olive Oil" || stored.Items[1].Name != "Pasta" {
		t.Fatalf("expected stored grocery list to match manual edits, got %+v", stored.Items)
	}
}

func TestBuildView_IncludesUnvalidIngredientsAcrossRecipes(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	note1 := plugintest.CreateNote(t, d, "Spices", recipePlugin)
	note2 := plugintest.CreateNote(t, d, "Baking", recipePlugin)
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Paprika', '5', 'g', '1', 'tablespoon', 0, 0)`, note1); err != nil {
		t.Fatalf("insert unvalidated ingredient: %v", err)
	}
	if _, err := d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, non_metric_amount, non_metric_unit, metric_validated, sort_order) VALUES (?, 'Flour', '', '', '1', 'cup', 0, 0)`, note2); err != nil {
		t.Fatalf("insert missing metric ingredient: %v", err)
	}

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})
	plugin := &RecipeOverviewPlugin{}
	result, err := plugin.BuildView(context.Background(), d.DB, 0, overviewNote)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	data, ok := result.(*OverviewData)
	if !ok {
		t.Fatalf("expected *OverviewData, got %T", result)
	}
	if len(data.UnvalidIngredients) != 2 {
		t.Fatalf("expected 2 unvalid ingredients, got %d", len(data.UnvalidIngredients))
	}

	issues := map[string]string{}
	for _, item := range data.UnvalidIngredients {
		issues[item.IngredientName] = item.IssueType
	}
	if issues["Paprika"] != "not_validated" {
		t.Fatalf("expected Paprika to be not_validated, got %q", issues["Paprika"])
	}
	if issues["Flour"] != "missing_metric" {
		t.Fatalf("expected Flour to be missing_metric, got %q", issues["Flour"])
	}
}
