package recipeoverview

import (
	"context"
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
