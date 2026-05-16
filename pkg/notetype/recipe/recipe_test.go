package recipe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRecipePlugin(t *testing.T) {
	plugintest.Run(t, &RecipePlugin{}, plugintest.TestData{
		ValidPayload:   `{"ingredients":[{"name":"Flour","amount":"2","unit":"g"},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","freezable":true,"pre_cook_servings":"8"}`,
		InvalidPayload: `{"ingredients":[{"name":"","amount":"2","unit":"g"}]}`,
	})
}

func TestRecipeTextPrint(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Flour","amount":"2","unit":"g"},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","freezable":true,"pre_cook_servings":"8"}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := RecipeTextPrint(payload, "My Cake")

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
	if !strings.Contains(out, "Pre-cook servings") {
		t.Error("missing Pre-cook servings detail")
	}
	if !strings.Contains(out, "8") {
		t.Error("missing pre-cook servings value")
	}

	t.Logf("Text output:\n%s", out)
}

func TestFormatRecipeReceipt(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Sugar","amount":"100","unit":"g"}],"servings":"2","attention_time":"","total_time":"30m","grams_per_serving":"","kcal_per_serving":"","freezable":false,"pre_cook_servings":""}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	buf := formatRecipeReceipt(payload, "Cookies")
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
