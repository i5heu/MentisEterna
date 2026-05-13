package recipeoverview

import (
	"context"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"github.com/i5heu/MentisEterna/pkg/notetype/recipe"
)

func TestRecipeOverviewPlugin(t *testing.T) {
	plugintest.Run(t, &RecipeOverviewPlugin{}, plugintest.TestData{})
}

func TestProcessLoad_WithRealRecipes(t *testing.T) {
	d := plugintest.DB(t, &RecipeOverviewPlugin{})
	defer d.Close()

	recipePlugin := &recipe.RecipePlugin{}
	if err := recipePlugin.InitSchema(d.DB); err != nil {
		t.Fatalf("recipe InitSchema: %v", err)
	}

	note1 := plugintest.CreateNote(t, d, "Chocolate Cake", recipePlugin)
	note2 := plugintest.CreateNote(t, d, "Tomato Soup", recipePlugin)

	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Flour', '2', 'cups', 0)`, note1)
	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Sugar', '1', 'cup', 1)`, note1)
	d.DB.Exec(`INSERT INTO ct_recipe_ingredients (note_id, name, amount, unit, sort_order) VALUES (?, 'Tomatoes', '5', 'pieces', 0)`, note2)

	overviewNote := plugintest.CreateNote(t, d, "Weekly Overview", &RecipeOverviewPlugin{})

	plugin := &RecipeOverviewPlugin{}
	result, err := plugin.ProcessLoad(context.Background(), d.DB, 0, overviewNote)
	if err != nil {
		t.Fatalf("ProcessLoad: %v", err)
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
}
