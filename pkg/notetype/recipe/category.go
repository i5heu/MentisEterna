package recipe

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
)

const OtherIngredientCategory = "other"

var ingredientCategoryList = []string{
	"vegetables",
	"fruit",
	"meat",
	"dairy",
	"fish",
	"chilled & deli",
	"frozen",
	"spices",
	"beverages",
	"household",
	OtherIngredientCategory,
}

var ingredientCategoryIndex = func() map[string]int {
	out := make(map[string]int, len(ingredientCategoryList))
	for i, category := range ingredientCategoryList {
		out[category] = i
	}
	return out
}()

type IngredientCategoryEmbedder interface {
	GenerateEmbedding(text string) ([]float64, error)
}

type categorizedIngredient struct {
	ID   int64
	Name string
}

type categoryEmbedding struct {
	Category string
	Vector   []float64
}

func IngredientCategoryList() []string {
	out := make([]string, len(ingredientCategoryList))
	copy(out, ingredientCategoryList)
	return out
}

func NormalizeIngredientCategory(category string) string {
	category = strings.TrimSpace(strings.ToLower(category))
	if _, ok := ingredientCategoryIndex[category]; ok {
		return category
	}
	return OtherIngredientCategory
}

func IngredientCategorySortIndex(category string) int {
	category = NormalizeIngredientCategory(category)
	if idx, ok := ingredientCategoryIndex[category]; ok {
		return idx
	}
	return ingredientCategoryIndex[OtherIngredientCategory]
}

func CategorizeIngredientsForNotes(ctx context.Context, db *sql.DB, embedder IngredientCategoryEmbedder, noteIDs []int64) error {
	return CategorizeIngredientsForNotesWithWorkers(ctx, db, embedder, noteIDs, 1)
}

func CategorizeIngredientsForNotesWithWorkers(ctx context.Context, db *sql.DB, embedder IngredientCategoryEmbedder, noteIDs []int64, workers int) error {
	if db == nil || embedder == nil || len(noteIDs) == 0 {
		return nil
	}

	ids := dedupeNoteIDs(noteIDs)
	if len(ids) == 0 {
		return nil
	}

	ingredients, err := loadIngredientsForCategorization(ctx, db, ids)
	if err != nil {
		return err
	}
	if len(ingredients) == 0 {
		return nil
	}

	categoryEmbeddings, err := buildCategoryEmbeddings(embedder)
	if err != nil {
		return fmt.Errorf("recipe: build category embeddings: %w", err)
	}

	categoryByName := categorizeIngredientNames(ctx, embedder, ingredients, categoryEmbeddings, workers)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recipe: begin ingredient categorization tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE ct_recipe_ingredients SET grocery_category = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("recipe: prepare ingredient categorization update: %w", err)
	}
	defer stmt.Close()

	for _, ingredient := range ingredients {
		name := normalizeIngredientText(ingredient.Name)
		if name == "" {
			continue
		}
		category, ok := categoryByName[name]
		if !ok {
			continue
		}

		if _, err := stmt.ExecContext(ctx, category, ingredient.ID); err != nil {
			return fmt.Errorf("recipe: update ingredient %d category: %w", ingredient.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recipe: commit ingredient categorization: %w", err)
	}
	return nil
}

func dedupeNoteIDs(noteIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(noteIDs))
	out := make([]int64, 0, len(noteIDs))
	for _, id := range noteIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func loadIngredientsForCategorization(ctx context.Context, db *sql.DB, noteIDs []int64) ([]categorizedIngredient, error) {
	placeholders := make([]string, len(noteIDs))
	args := make([]any, 0, len(noteIDs))
	for i, id := range noteIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := `
		SELECT id, name
		FROM ct_recipe_ingredients
		WHERE note_id IN (` + strings.Join(placeholders, ",") + `)
		  AND COALESCE(grocery_category_manual, 0) = 0
		ORDER BY note_id, sort_order, id
	`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recipe: load ingredients for categorization: %w", err)
	}
	defer rows.Close()

	out := make([]categorizedIngredient, 0)
	for rows.Next() {
		var ingredient categorizedIngredient
		if err := rows.Scan(&ingredient.ID, &ingredient.Name); err != nil {
			return nil, fmt.Errorf("recipe: scan ingredient for categorization: %w", err)
		}
		out = append(out, ingredient)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildCategoryEmbeddings(embedder IngredientCategoryEmbedder) ([]categoryEmbedding, error) {
	out := make([]categoryEmbedding, 0, len(ingredientCategoryList))
	for _, category := range ingredientCategoryList {
		vec, err := embedder.GenerateEmbedding(category)
		if err != nil {
			return nil, err
		}
		out = append(out, categoryEmbedding{Category: category, Vector: vec})
	}
	return out, nil
}

func categorizeIngredientNames(ctx context.Context, embedder IngredientCategoryEmbedder, ingredients []categorizedIngredient, categoryEmbeddings []categoryEmbedding, workers int) map[string]string {
	uniqueNames := make(map[string]struct{}, len(ingredients))
	for _, ingredient := range ingredients {
		name := normalizeIngredientText(ingredient.Name)
		if name == "" {
			continue
		}
		uniqueNames[name] = struct{}{}
	}
	if len(uniqueNames) == 0 {
		return map[string]string{}
	}

	if workers < 1 {
		workers = 1
	}
	if workers > len(uniqueNames) {
		workers = len(uniqueNames)
	}

	type result struct {
		name     string
		category string
		ok       bool
	}

	jobs := make(chan string)
	results := make(chan result, len(uniqueNames))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				if ctx.Err() != nil {
					return
				}
				vec, err := embedder.GenerateEmbedding(name)
				if err != nil {
					results <- result{name: name, ok: false}
					continue
				}
				results <- result{name: name, category: bestIngredientCategory(vec, categoryEmbeddings), ok: true}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for name := range uniqueNames {
			select {
			case <-ctx.Done():
				return
			case jobs <- name:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	categoryByName := make(map[string]string, len(uniqueNames))
	for res := range results {
		if !res.ok {
			continue
		}
		categoryByName[res.name] = res.category
	}
	return categoryByName
}

func normalizeIngredientText(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
}

func bestIngredientCategory(ingredientVector []float64, categories []categoryEmbedding) string {
	bestCategory := OtherIngredientCategory
	bestScore := math.Inf(-1)
	for _, category := range categories {
		score := cosineSimilarity(ingredientVector, category.Vector)
		if score > bestScore {
			bestScore = score
			bestCategory = category.Category
		}
	}
	return NormalizeIngredientCategory(bestCategory)
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return math.Inf(-1)
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return math.Inf(-1)
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
