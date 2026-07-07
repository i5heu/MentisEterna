package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/i5heu/MentisEterna/internal/llm"
	recipeplugin "github.com/i5heu/MentisEterna/pkg/notetype/recipe"
)

type recipeImportActionResult struct {
	PrimaryNoteID  int64   `json:"primary_note_id"`
	CreatedNoteIDs []int64 `json:"created_note_ids"`
}

func (s *Server) recipeCategoryWorkerCount() int {
	return envOrInt("RECIPE_CATEGORY_WORKERS", 10)
}

func (s *Server) classifyRecipeIngredientsForNotes(noteIDs ...int64) {
	if s == nil || s.db == nil || s.llm == nil || len(noteIDs) == 0 {
		return
	}

	ids := append([]int64(nil), noteIDs...)
	dbConn := s.db.DB
	embedder := s.llm
	workers := s.recipeCategoryWorkerCount()

	go func() {
		release := llm.BeginBackendUse(embedder)
		defer release()
		if err := recipeplugin.CategorizeIngredientsForNotesWithWorkers(context.Background(), dbConn, embedder, ids, workers); err != nil {
			log.Printf("recipe: categorize ingredients for notes %v: %v", ids, err)
		}
	}()
}

func (s *Server) maybePostProcessRecipeAction(noteType string, actionID string, noteID int64, result any) {
	if s == nil || s.db == nil || s.llm == nil {
		return
	}
	if noteType != "recipe" || actionID != "import_recipes_json" {
		return
	}

	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	var out recipeImportActionResult
	if err := json.Unmarshal(data, &out); err != nil {
		return
	}
	ids := []int64{noteID}
	if out.PrimaryNoteID > 0 {
		ids[0] = out.PrimaryNoteID
	}
	ids = append(ids, out.CreatedNoteIDs...)
	s.classifyRecipeIngredientsForNotes(ids...)
}

func (s *Server) classifyAllRecipeIngredients(ctx context.Context, dbConn *sql.DB) (int, int, error) {
	if s == nil || dbConn == nil || s.llm == nil {
		return 0, 0, nil
	}

	rows, err := dbConn.QueryContext(ctx, `SELECT id FROM notes WHERE type = 'recipe' ORDER BY id ASC`)
	if err != nil {
		return 0, 0, fmt.Errorf("load recipe note ids: %w", err)
	}
	defer rows.Close()

	noteIDs := make([]int64, 0)
	for rows.Next() {
		var noteID int64
		if err := rows.Scan(&noteID); err != nil {
			return 0, 0, fmt.Errorf("scan recipe note id: %w", err)
		}
		noteIDs = append(noteIDs, noteID)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	var ingredientCount int
	if err := dbConn.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM ct_recipe_ingredients ri
		JOIN notes n ON n.id = ri.note_id
		WHERE n.type = 'recipe'
	`).Scan(&ingredientCount); err != nil {
		return 0, 0, fmt.Errorf("count recipe ingredients: %w", err)
	}

	release := llm.BeginBackendUse(s.llm)
	defer release()

	const batchSize = 200
	for start := 0; start < len(noteIDs); start += batchSize {
		end := start + batchSize
		if end > len(noteIDs) {
			end = len(noteIDs)
		}
		if err := recipeplugin.CategorizeIngredientsForNotesWithWorkers(ctx, dbConn, s.llm, noteIDs[start:end], s.recipeCategoryWorkerCount()); err != nil {
			return len(noteIDs), ingredientCount, err
		}
	}

	return len(noteIDs), ingredientCount, nil
}

func (s *Server) recalculateRecipeIngredientCategoriesTask(dbConn *sql.DB, _ []byte) (string, error) {
	recipeCount, ingredientCount, err := s.classifyAllRecipeIngredients(context.Background(), dbConn)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Recalculated grocery categories for %d ingredients across %d recipe notes", ingredientCount, recipeCount), nil
}
