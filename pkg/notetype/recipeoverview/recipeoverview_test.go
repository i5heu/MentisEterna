package recipeoverview

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRecipeOverviewPlugin(t *testing.T) {
	// recipe_overview doesn't have a custom payload — ProcessSave does nothing.
	plugintest.Run(t, &RecipeOverviewPlugin{}, plugintest.TestData{})
}
