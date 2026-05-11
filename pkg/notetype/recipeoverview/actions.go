// Package recipeoverview provides an action handler for "generate_grocery_list"
// that collects ingredients from all recipe notes.
package recipeoverview

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/i5heu/MentisEterna/internal/server"
)

func init() {
	server.RegisterPluginActionHandler("recipe_overview", handleAction)
}

func handleAction(db *sql.DB, noteID int64, action string, params json.RawMessage) (any, error) {
	switch action {
	case "generate_grocery_list":
		return generateGroceryList(db, noteID)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

// generateGroceryList collects all ingredients from all recipe notes,
// deduplicates by name+unit, and stores the result in ct_recipe_overview_grocery_lists.
func generateGroceryList(db *sql.DB, noteID int64) (any, error) {
	rows, err := db.Query(`
		SELECT ri.name, ri.amount, ri.unit
		FROM ct_recipe_ingredients ri
		JOIN notes n ON n.id = ri.note_id
		WHERE n.type = 'recipe'
		ORDER BY ri.name, ri.unit
	`)
	if err != nil {
		return nil, fmt.Errorf("query ingredients: %w", err)
	}
	defer rows.Close()

	// Aggregate: group by name+unit.
	type item struct {
		Name   string `json:"name"`
		Amount string `json:"amount"`
		Unit   string `json:"unit"`
	}
	aggregated := map[string]item{}
	for rows.Next() {
		var name, amount, unit string
		if err := rows.Scan(&name, &amount, &unit); err != nil {
			return nil, fmt.Errorf("scan ingredient: %w", err)
		}
		key := name + "|" + unit
		if existing, ok := aggregated[key]; ok {
			// Simple concatenation approach — a real implementation would parse
			// amounts numerically. For now we just note "multiple recipes".
			if existing.Amount != "" && amount != "" {
				existing.Amount = existing.Amount + " + " + amount
			} else if amount != "" {
				existing.Amount = amount
			}
			aggregated[key] = existing
		} else {
			aggregated[key] = item{Name: name, Amount: amount, Unit: unit}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := make([]item, 0, len(aggregated))
	for _, v := range aggregated {
		items = append(items, v)
	}

	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}

	// Store the generated list.
	_, err = db.Exec(
		`INSERT INTO ct_recipe_overview_grocery_lists (note_id, days, items_json) VALUES (?, 8, ?)`,
		noteID, string(itemsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("insert grocery list: %w", err)
	}

	return map[string]any{
		"items": items,
	}, nil
}
