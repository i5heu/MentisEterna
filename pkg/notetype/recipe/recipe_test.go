package recipe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
	"github.com/i5heu/MentisEterna/pkg/printer"
)

func TestRecipePlugin(t *testing.T) {
	plugintest.Run(t, &RecipePlugin{}, plugintest.TestData{
		ValidPayload:   `{"ingredients":[{"name":"Flour","prepare":"sifted","amount":"2","unit":"g","non_metric_amount":"1","non_metric_unit":"cup","metric_validated":true},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","rating":7,"freezable":true,"pre_cook_servings":"8"}`,
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
	err := plugin.ValidateConfig(json.RawMessage(`{"ingredients":[{"name":"Sugar","amount":"200","unit":"g","non_metric_amount":"1","non_metric_unit":"cup","metric_validated":true}],"rating":6}`))
	if err != nil {
		t.Fatalf("expected non-metric fields to be valid, got: %v", err)
	}
}

func TestValidateConfigRejectsOutOfRangeRating(t *testing.T) {
	plugin := &RecipePlugin{}
	err := plugin.ValidateConfig(json.RawMessage(`{"ingredients":[{"name":"Sugar"}],"rating":11}`))
	if err == nil {
		t.Fatal("expected out-of-range rating to be rejected")
	}
}

type categoryTestEmbedder struct {
	mu        sync.Mutex
	vectors   map[string][]float64
	active    int32
	maxActive int32
	delay     time.Duration
}

func newCategoryTestEmbedder(delay time.Duration) *categoryTestEmbedder {
	categories := IngredientCategoryList()
	vectors := make(map[string][]float64, len(categories)+8)
	for idx, category := range categories {
		vectors[category] = categoryVector(len(categories), idx)
	}
	vectors["carrot"] = categoryVector(len(categories), categoryIndex(categories, "vegetables"))
	vectors["milk"] = categoryVector(len(categories), categoryIndex(categories, "dairy"))
	vectors["paprika"] = categoryVector(len(categories), categoryIndex(categories, "spices"))
	vectors["salmon"] = categoryVector(len(categories), categoryIndex(categories, "fish"))
	return &categoryTestEmbedder{vectors: vectors, delay: delay}
}

func (e *categoryTestEmbedder) GenerateEmbedding(text string) ([]float64, error) {
	current := atomic.AddInt32(&e.active, 1)
	defer atomic.AddInt32(&e.active, -1)
	for {
		max := atomic.LoadInt32(&e.maxActive)
		if current <= max || atomic.CompareAndSwapInt32(&e.maxActive, max, current) {
			break
		}
	}
	if e.delay > 0 {
		time.Sleep(e.delay)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	vec, ok := e.vectors[strings.ToLower(strings.TrimSpace(text))]
	if !ok {
		return nil, fmt.Errorf("missing test vector for %q", text)
	}
	out := make([]float64, len(vec))
	copy(out, vec)
	return out, nil
}

func categoryVector(size int, index int) []float64 {
	vec := make([]float64, size)
	if index >= 0 && index < size {
		vec[index] = 1
	}
	return vec
}

func categoryIndex(categories []string, target string) int {
	for i, category := range categories {
		if category == target {
			return i
		}
	}
	return -1
}

func TestCategorizeIngredientsForNotesWithWorkersStoresCategories(t *testing.T) {
	d := plugintest.DB(t, &RecipePlugin{})
	defer d.Close()

	noteID := plugintest.CreateNote(t, d, "Ingredients", &RecipePlugin{})
	if _, err := d.Exec(`
		INSERT INTO ct_recipe_ingredients (note_id, name, sort_order)
		VALUES (?, 'Carrot', 0), (?, 'Milk', 1), (?, 'Paprika', 2)
	`, noteID, noteID, noteID); err != nil {
		t.Fatalf("insert ingredients: %v", err)
	}

	embedder := newCategoryTestEmbedder(0)
	if err := CategorizeIngredientsForNotesWithWorkers(context.Background(), d.DB, embedder, []int64{noteID}, 3); err != nil {
		t.Fatalf("CategorizeIngredientsForNotesWithWorkers: %v", err)
	}

	rows, err := d.Query(`SELECT name, grocery_category FROM ct_recipe_ingredients WHERE note_id = ? ORDER BY sort_order`, noteID)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, category string
		if err := rows.Scan(&name, &category); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = category
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if got["Carrot"] != "vegetables" {
		t.Fatalf("Carrot category = %q, want vegetables", got["Carrot"])
	}
	if got["Milk"] != "dairy" {
		t.Fatalf("Milk category = %q, want dairy", got["Milk"])
	}
	if got["Paprika"] != "spices" {
		t.Fatalf("Paprika category = %q, want spices", got["Paprika"])
	}
}

func TestCategorizeIngredientsForNotesWithWorkersRunsInParallel(t *testing.T) {
	d := plugintest.DB(t, &RecipePlugin{})
	defer d.Close()

	noteID := plugintest.CreateNote(t, d, "Parallel", &RecipePlugin{})
	if _, err := d.Exec(`
		INSERT INTO ct_recipe_ingredients (note_id, name, sort_order)
		VALUES (?, 'Carrot', 0), (?, 'Milk', 1), (?, 'Paprika', 2), (?, 'Salmon', 3)
	`, noteID, noteID, noteID, noteID); err != nil {
		t.Fatalf("insert ingredients: %v", err)
	}

	embedder := newCategoryTestEmbedder(20 * time.Millisecond)
	if err := CategorizeIngredientsForNotesWithWorkers(context.Background(), d.DB, embedder, []int64{noteID}, 4); err != nil {
		t.Fatalf("CategorizeIngredientsForNotesWithWorkers: %v", err)
	}
	if got := atomic.LoadInt32(&embedder.maxActive); got < 2 {
		t.Fatalf("expected parallel embedding calls, maxActive = %d", got)
	}
}

func TestRecipeTextPrint(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Flour","prepare":"sifted","amount":"2","unit":"g"},{"name":"Eggs","amount":"3","unit":"pcs"}],"servings":"4","attention_time":"30m","total_time":"1h","grams_per_serving":"250","kcal_per_serving":"420","rating":8,"freezable":true,"pre_cook_servings":"8"}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := RecipeTextPrint(payload, "My Cake", "Preheat oven to 180C.\n\n![Cake slice](/file/1/2)\n\n- Mix ingredients well.")

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
	if !strings.Contains(out, "Flour (sifted)") {
		t.Error("missing ingredient preparation for Flour")
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
	if !strings.Contains(out, "Rating: 8/10") {
		t.Error("missing rating detail")
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
	if strings.Contains(out, "![") {
		t.Error("raw markdown image tag should not be printed")
	}
	if !strings.Contains(out, "[Image: Cake slice]") {
		t.Error("image placeholder should be readable")
	}
	if !strings.Contains(out, "  Preheat oven to 180C.\n\n  [Image: Cake slice]") {
		t.Error("paragraph breaks should be preserved in printed notes")
	}
	if !strings.Contains(out, "• Mix ingredients well.") {
		t.Error("markdown list items should be rendered readably")
	}

	t.Logf("Text output:\n%s", out)
}

func TestFormatMarkdownForPrintPreservesParagraphsAndCleansImages(t *testing.T) {
	body := "# Method\n\nFirst paragraph.\n\n![Plated dish](/file/1/2)\n\n- Mix well\n1. Bake\n[See details](https://example.com)"
	lines := FormatMarkdownForPrint(body, 40)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "![") {
		t.Fatalf("expected markdown image tags to be removed, got: %q", joined)
	}
	if strings.Contains(joined, "# Method") {
		t.Fatalf("expected markdown heading markers to be removed, got: %q", joined)
	}
	if !strings.Contains(joined, "Method\n\nFirst paragraph.") {
		t.Fatalf("expected paragraph separation to be preserved, got: %q", joined)
	}
	if !strings.Contains(joined, "[Image: Plated dish]") {
		t.Fatalf("expected image placeholder to be readable, got: %q", joined)
	}
	if !strings.Contains(joined, "• Mix well") {
		t.Fatalf("expected bullet formatting, got: %q", joined)
	}
	if !strings.Contains(joined, "1. Bake") {
		t.Fatalf("expected ordered list formatting, got: %q", joined)
	}
	if !strings.Contains(joined, "See details") {
		t.Fatalf("expected markdown links to keep link text, got: %q", joined)
	}
}

func TestMarkdownPrintBlocksExtractImageFileID(t *testing.T) {
	body := "Intro\n\n![Cake slice](/file/12/34)\n\nOutro"
	blocks := MarkdownPrintBlocks(body, 40)

	var foundImage bool
	for _, block := range blocks {
		if block.Kind == MarkdownPrintBlockImage {
			foundImage = true
			if block.FileID != 34 {
				t.Fatalf("expected file id 34, got %d", block.FileID)
			}
			if block.Alt != "Cake slice" {
				t.Fatalf("expected alt text %q, got %q", "Cake slice", block.Alt)
			}
		}
	}
	if !foundImage {
		t.Fatal("expected image block to be extracted")
	}
}

func TestFormatRecipeReceiptWithImagesUsesImageCallback(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Sugar","amount":"100","unit":"g"}],"rating":5}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var printedFileID int64
	buf := FormatRecipeReceiptWithImages(payload, "Cookies", "![Cookie](/file/1/22)", func(b *printer.Buf, fileID int64) error {
		printedFileID = fileID
		b.Text("[img]")
		return nil
	})
	s := string(buf.Bytes())
	if printedFileID != 22 {
		t.Fatalf("expected callback file id 22, got %d", printedFileID)
	}
	if !strings.Contains(s, "[img]") {
		t.Fatal("expected image callback output to be present in receipt buffer")
	}
	if strings.Contains(s, "[Image: Cookie]") {
		t.Fatal("expected placeholder not to be used when image callback succeeds")
	}
}

func TestFormatRecipeReceipt(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal([]byte(`{"ingredients":[{"name":"Sugar","prepare":"finely ground","amount":"100","unit":"g"}],"servings":"2","attention_time":"","total_time":"30m","grams_per_serving":"","kcal_per_serving":"","rating":5,"freezable":false,"pre_cook_servings":""}`), &payload); err != nil {
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
	if !strings.Contains(s, "finely ground") {
		t.Error("missing ingredient preparation")
	}
	if !strings.Contains(s, "5/10") {
		t.Error("missing rating")
	}

	t.Logf("Buffer length: %d bytes", len(b))
}

func TestImportRecipesJSONRejectsNonIntegerRating(t *testing.T) {
	d := plugintest.DB(t, &RecipePlugin{})
	defer d.Close()

	noteID := plugintest.CreateNote(t, d, "Existing Recipe", &RecipePlugin{})
	params, err := json.Marshal(map[string]any{
		"import_json": `{"recipes":[{"title":"Bad Rating","rating":7.5}]}`,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	plugin := &RecipePlugin{}
	_, err = plugin.HandleAction(context.Background(), d.DB, 0, noteID, "import_recipes_json", params)
	if err == nil {
		t.Fatal("expected non-integer rating import to fail")
	}
	if !strings.Contains(err.Error(), "rating") {
		t.Fatalf("expected rating error, got: %v", err)
	}
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
					{"name": "Beans", "prepare": "drained", "amount": 2, "unit": "pcs", "non_metric_amount": 1, "non_metric_unit": "cup", "metric_validated": true},
				},
				"servings": 4,
				"rating":   9,
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

	var prepare, nonMetricAmount, nonMetricUnit string
	var metricValidated int
	if err := d.QueryRow(`SELECT prepare, non_metric_amount, non_metric_unit, metric_validated FROM ct_recipe_ingredients WHERE note_id = ? LIMIT 1`, noteID).Scan(&prepare, &nonMetricAmount, &nonMetricUnit, &metricValidated); err != nil {
		t.Fatalf("query primary ingredient extended fields: %v", err)
	}
	if prepare != "drained" || nonMetricAmount != "1" || nonMetricUnit != "cup" || metricValidated != 1 {
		t.Fatalf("expected imported ingredient fields to persist, got prepare=%q amount=%q unit=%q validated=%d", prepare, nonMetricAmount, nonMetricUnit, metricValidated)
	}

	var rating int
	if err := d.QueryRow(`SELECT rating FROM ct_recipe_meta WHERE note_id = ?`, noteID).Scan(&rating); err != nil {
		t.Fatalf("query primary rating: %v", err)
	}
	if rating != 9 {
		t.Fatalf("expected imported rating 9, got %d", rating)
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
