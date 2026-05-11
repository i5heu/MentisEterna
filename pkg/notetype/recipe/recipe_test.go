package recipe

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRecipePlugin(t *testing.T) {
	plugintest.Run(t, &RecipePlugin{}, plugintest.TestData{
		ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"cups"},{"name":"Eggs","amount":"3","unit":"pcs"}]}`,
		InvalidPayload: `{"ingredients":[{"name":"","amount":"2","unit":"cups"}]}`,
	})
}
