// Package recipeoverview provides action handlers for generating and managing
// grocery lists.
package recipeoverview

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/internal/server"
)

func init() {
	server.RegisterPluginActionHandler("recipe_overview", handleAction)
}

func handleAction(db *sql.DB, noteID int64, action string, params json.RawMessage) (any, error) {
	switch action {
	case "generate_grocery_list":
		return generateGroceryList(db, noteID, params)
	case "delete_grocery_list":
		return deleteGroceryList(db, params)
	case "list_grocery_lists":
		return listGroceryLists(db, noteID)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

// generateGroceryListParams is the JSON body for the generate action.
type generateGroceryListParams struct {
	RecipeIDs []int64 `json:"recipe_ids"`
	NumDays   int     `json:"num_days"`
	NumPeople int     `json:"num_people"`
}

// generateGroceryList collects ingredients from the specified recipe notes,
// multiplies amounts by num_people/num_days (approximately), deduplicates by
// name+unit, stores the result, and records which recipes were used.
func generateGroceryList(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p generateGroceryListParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}
	if p.NumDays <= 0 {
		p.NumDays = 8
	}
	if p.NumPeople <= 0 {
		p.NumPeople = 1
	}
	if len(p.RecipeIDs) == 0 {
		return nil, fmt.Errorf("at least one recipe must be selected")
	}

	// Build IN clause with positional parameters.
	placeholders := make([]string, len(p.RecipeIDs))
	args := make([]any, len(p.RecipeIDs))
	for i, id := range p.RecipeIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT ri.name, ri.amount, ri.unit
		FROM ct_recipe_ingredients ri
		JOIN notes n ON n.id = ri.note_id
		WHERE n.type = 'recipe' AND n.id IN (%s)
		ORDER BY ri.name, ri.unit
	`, strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
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
			existing.Amount = mergeAmounts(existing.Amount, amount)
			aggregated[key] = existing
		} else {
			aggregated[key] = item{Name: name, Amount: amount, Unit: unit}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Apply days/people scaling factor (crude: multiply numeric amounts).
	// This is a simple heuristic — amounts like "2" become "2*3*8=48".
	totalServings := p.NumDays * p.NumPeople
	scaled := make([]item, 0, len(aggregated))
	for _, it := range aggregated {
		it.Amount = scaleAmount(it.Amount, totalServings)
		scaled = append(scaled, it)
	}
	sort.Slice(scaled, func(i, j int) bool {
		return scaled[i].Name < scaled[j].Name
	})

	itemsJSON, err := json.Marshal(scaled)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}

	// Insert the grocery list and its recipe associations in a transaction.
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO ct_recipe_overview_grocery_lists (note_id, num_days, num_people, items_json) VALUES (?, ?, ?, ?)`,
		noteID, p.NumDays, p.NumPeople, string(itemsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("insert grocery list: %w", err)
	}

	listID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get list id: %w", err)
	}

	for _, rid := range p.RecipeIDs {
		if _, err := tx.Exec(
			`INSERT INTO ct_recipe_overview_grocery_list_recipes (grocery_list_id, recipe_note_id) VALUES (?, ?)`,
			listID, rid,
		); err != nil {
			return nil, fmt.Errorf("insert list-recipe link: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// Convert to GroceryItem slice.
	groceryItems := make([]GroceryItem, len(scaled))
	for i, it := range scaled {
		groceryItems[i] = GroceryItem{Name: it.Name, Amount: it.Amount, Unit: it.Unit}
	}

	// Return the full grocery list object so the frontend can display it.
	return map[string]any{
		"grocery_list": GroceryList{
			ID:        listID,
			NumDays:   p.NumDays,
			NumPeople: p.NumPeople,
			RecipeIDs: p.RecipeIDs,
			Items:     groceryItems,
		},
	}, nil
}

// deleteGroceryListParams is the JSON body for the delete action.
type deleteGroceryListParams struct {
	ListID int64 `json:"list_id"`
}

// deleteGroceryList deletes a grocery list and its recipe associations.
func deleteGroceryList(db *sql.DB, params json.RawMessage) (any, error) {
	var p deleteGroceryListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.ListID <= 0 {
		return nil, fmt.Errorf("list_id is required")
	}

	// CASCADE handles the junction table, so a single DELETE suffices.
	_, err := db.Exec(`DELETE FROM ct_recipe_overview_grocery_lists WHERE id = ?`, p.ListID)
	if err != nil {
		return nil, fmt.Errorf("delete grocery list: %w", err)
	}
	return map[string]any{"deleted": true}, nil
}

// listGroceryLists returns all past grocery lists for this overview note.
func listGroceryLists(db *sql.DB, noteID int64) (any, error) {
	lists := []GroceryList{}

	rows, err := db.Query(`
		SELECT id, generated_at, num_days, num_people, items_json
		FROM ct_recipe_overview_grocery_lists
		WHERE note_id = ?
		ORDER BY generated_at DESC
	`, noteID)
	if err != nil {
		return nil, fmt.Errorf("query grocery lists: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var gl GroceryList
		var itemsJSON string
		if err := rows.Scan(&gl.ID, &gl.GeneratedAt, &gl.NumDays, &gl.NumPeople, &itemsJSON); err != nil {
			return nil, fmt.Errorf("scan grocery list: %w", err)
		}
		if itemsJSON != "" {
			if err := json.Unmarshal([]byte(itemsJSON), &gl.Items); err != nil {
				return nil, fmt.Errorf("unmarshal items: %w", err)
			}
		}

		// Load recipe IDs.
		rrows, err := db.Query(`
			SELECT recipe_note_id FROM ct_recipe_overview_grocery_list_recipes
			WHERE grocery_list_id = ?
			ORDER BY recipe_note_id
		`, gl.ID)
		if err != nil {
			return nil, fmt.Errorf("query list recipes: %w", err)
		}
		gl.RecipeIDs = []int64{}
		for rrows.Next() {
			var rid int64
			if err := rrows.Scan(&rid); err != nil {
				rrows.Close()
				return nil, fmt.Errorf("scan list recipe: %w", err)
			}
			gl.RecipeIDs = append(gl.RecipeIDs, rid)
		}
		rrows.Close()

		lists = append(lists, gl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return map[string]any{"lists": lists}, nil
}

// mergeAmounts combines two amount strings (e.g. "2" + "3" → "5" or
// "2 cups" + "3 cups" → "5 cups").
func mergeAmounts(a, b string) string {
	// Try numeric merge first.
	aNum, aUnit := splitAmount(a)
	bNum, bUnit := splitAmount(b)
	if aNum >= 0 && bNum >= 0 && aUnit == bUnit {
		return formatAmount(aNum+bNum, aUnit)
	}
	// Fallback: concatenate.
	if a != "" && b != "" {
		return a + " + " + b
	}
	if b != "" {
		return b
	}
	return a
}

// splitAmount tries to parse a numeric prefix and trailing unit from a string.
func splitAmount(s string) (float64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1, ""
	}
	// Find where the number stops.
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == ',') {
		i++
	}
	if i == 0 {
		return -1, ""
	}
	numStr := strings.ReplaceAll(s[:i], ",", ".")
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return -1, ""
	}
	unit := strings.TrimSpace(s[i:])
	return num, unit
}

// formatAmount formats a numeric amount with optional unit.
func formatAmount(num float64, unit string) string {
	s := strconv.FormatFloat(num, 'f', -1, 64)
	if unit != "" {
		s += " " + unit
	}
	return s
}

// scaleAmount multiplies a numeric amount by a factor.
func scaleAmount(amount string, factor int) string {
	num, unit := splitAmount(amount)
	if num < 0 {
		return amount // non-numeric, leave as-is
	}
	return formatAmount(num*float64(factor), unit)
}
