package recipe

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRecipePlugin(t *testing.T) {
	plugintest.Run(t, &RecipePlugin{}, plugintest.TestData{
		ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"cups"},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","freezable":true}`,
		InvalidPayload: `{"ingredients":[{"name":"","amount":"2","unit":"cups"}]}`,
	})
}
