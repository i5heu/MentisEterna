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

	"github.com/i5heu/MentisEterna/pkg/printer"
)

// handleAction is now implemented directly on RecipeOverviewPlugin via the
// notetype.ActionHandler interface (HandleAction method). These helper
// functions are called by HandleAction.

// generateGroceryListParams is the JSON body for the generate action.
type generateGroceryListParams struct {
	RecipeIDs        []int64 `json:"recipe_ids"`
	PreCookRecipeIDs []int64 `json:"pre_cook_recipe_ids"`
	NumDays          int     `json:"num_days"`
	NumPeople        int     `json:"num_people"`
}

// generateGroceryList collects ingredients from the selected recipes, scales
// each recipe's ingredient amounts by (num_people / recipe_servings), and
// deduplicates by name+unit across all recipes.  Days is ignored — every
// recipe is used exactly once.
//
// Recipes in pre_cook_recipe_ids use their pre_cook_servings value instead
// of the people-based scaling factor. This allows batch-cooking freezable
// recipes at a fixed serving size independent of the current head count.
func generateGroceryList(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p generateGroceryListParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}
	if p.NumPeople <= 0 {
		p.NumPeople = 1
	}
	if len(p.RecipeIDs) == 0 && len(p.PreCookRecipeIDs) == 0 {
		return nil, fmt.Errorf("at least one recipe must be selected")
	}

	// Collect all recipe IDs into a single deduplicated set for fetching.
	// pre_cook_recipe_ids is a subset of recipe_ids; each recipe appears once.
	idSet := make(map[int64]bool, len(p.RecipeIDs))
	for _, id := range p.RecipeIDs {
		idSet[id] = true
	}
	for _, id := range p.PreCookRecipeIDs {
		idSet[id] = true
	}
	allIDs := make([]int64, 0, len(idSet))
	for id := range idSet {
		allIDs = append(allIDs, id)
	}

	// Build a set for O(1) lookup.
	preCookSet := make(map[int64]bool, len(p.PreCookRecipeIDs))
	for _, id := range p.PreCookRecipeIDs {
		preCookSet[id] = true
	}

	// Fetch title, servings, and pre_cook_servings for every selected recipe.
	type recipeInfo struct {
		Title           string
		Servings        float64
		PreCookServings float64
	}
	recipes := map[int64]recipeInfo{}
	recipeNames := make([]string, 0, len(p.RecipeIDs)+len(p.PreCookRecipeIDs))

	for _, rid := range allIDs {
		var title string
		var servingsStr sql.NullString
		var preCookStr sql.NullString
		err := db.QueryRow(`
			SELECT n.title, rm.servings, rm.pre_cook_servings
			FROM notes n
			LEFT JOIN ct_recipe_meta rm ON rm.note_id = n.id
			WHERE n.id = ?`, rid,
		).Scan(&title, &servingsStr, &preCookStr)
		if err != nil {
			return nil, fmt.Errorf("recipe %d: %w", rid, err)
		}
		recipeNames = append(recipeNames, title)

		servings := 1.0 // default: 1 serving → no scaling
		if servingsStr.Valid && servingsStr.String != "" {
			if s, err := strconv.ParseFloat(strings.ReplaceAll(servingsStr.String, ",", "."), 64); err == nil && s > 0 {
				servings = s
			}
		}

		preCook := 0.0
		if preCookStr.Valid && preCookStr.String != "" {
			if s, err := strconv.ParseFloat(strings.ReplaceAll(preCookStr.String, ",", "."), 64); err == nil && s > 0 {
				preCook = s
			}
		}

		recipes[rid] = recipeInfo{Title: title, Servings: servings, PreCookServings: preCook}
	}

	// Build IN clause with positional parameters.
	placeholders := make([]string, len(allIDs))
	args := make([]any, len(allIDs))
	for i, id := range allIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT ri.note_id, ri.name, ri.amount, ri.unit, COALESCE(ri.non_metric_amount, ''), COALESCE(ri.non_metric_unit, '')
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

	// Aggregate: group by name+unit, scaling each ingredient by
	// (num_people / recipe_servings) before merging.
	type item struct {
		Name      string `json:"name"`
		Amount    string `json:"amount"`
		Unit      string `json:"unit"`
		NonMetric string `json:"non_metric,omitempty"`
	}
	aggregated := map[string]item{}
	for rows.Next() {
		var rid int64
		var name, amount, unit, nonMetricAmount, nonMetricUnit string
		if err := rows.Scan(&rid, &name, &amount, &unit, &nonMetricAmount, &nonMetricUnit); err != nil {
			return nil, fmt.Errorf("scan ingredient: %w", err)
		}

		// Scale: pre-cook recipes use pre_cook_servings, others use people / recipe_servings.
		info := recipes[rid]
		var factor float64
		if preCookSet[rid] && info.PreCookServings > 0 {
			factor = info.PreCookServings / info.Servings
		} else {
			factor = float64(p.NumPeople) / info.Servings
		}
		amount = scaleAmountFloat(amount, factor)
		amount, unit = canonicalMetricAmount(amount, unit)
		nonMetricAmount = scaleAmountFloat(nonMetricAmount, factor)
		nonMetric := formatOptionalAmount(nonMetricAmount, nonMetricUnit)

		key := name + "|" + unit
		if existing, ok := aggregated[key]; ok {
			existing.Amount = mergeAmounts(existing.Amount, amount)
			existing.NonMetric = mergeAmounts(existing.NonMetric, nonMetric)
			aggregated[key] = existing
		} else {
			aggregated[key] = item{Name: name, Amount: amount, Unit: unit, NonMetric: nonMetric}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	scaled := make([]item, 0, len(aggregated))
	for _, it := range aggregated {
		it.Amount, it.Unit = normalizeMetricAmount(it.Amount, it.Unit)
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

	// Store pre_cook_recipe_ids in a new column (or as JSON in a text field).
	// For backward compatibility, we store this info in a new column.
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

	for _, rid := range allIDs {
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
		groceryItems[i] = GroceryItem{Name: it.Name, Amount: it.Amount, Unit: it.Unit, NonMetric: it.NonMetric}
	}

	// Return the full grocery list object so the frontend can display it.
	return map[string]any{
		"grocery_list": GroceryList{
			ID:          listID,
			NumDays:     p.NumDays,
			NumPeople:   p.NumPeople,
			RecipeIDs:   allIDs,
			RecipeNames: recipeNames,
			Items:       groceryItems,
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

func formatOptionalAmount(amount string, unit string) string {
	amount = strings.TrimSpace(amount)
	unit = strings.TrimSpace(unit)
	if amount == "" && unit == "" {
		return ""
	}
	if amount == "" {
		return unit
	}
	if unit == "" {
		return amount
	}
	return amount + " " + unit
}

// canonicalMetricAmount converts compatible metric units to a shared base unit
// before aggregation so values like 500 mg and 0.5 g merge correctly.
// - mass units are canonicalized to mg
// - volume units are canonicalized to ml
func canonicalMetricAmount(amount string, unit string) (string, string) {
	num, u := splitAmount(amount)
	if num < 0 || u != "" {
		return amount, unit
	}

	switch unit {
	case "mg":
		return formatAmount(num, ""), "mg"
	case "g":
		return formatAmount(num*1000, ""), "mg"
	case "kg":
		return formatAmount(num*1000*1000, ""), "mg"
	case "ml":
		return formatAmount(num, ""), "ml"
	case "l":
		return formatAmount(num*1000, ""), "ml"
	default:
		return amount, unit
	}
}

// normalizeMetricAmount normalizes amounts to the best metric unit.
// - mg >= 1,000,000 → convert to kg
// - mg >= 1000 → convert to g
// - g >= 1000 → convert to kg
// - g > 0 and < 1 → convert to mg
// - kg > 0 and < 1 → convert to g
// - ml >= 1000 → convert to l
// - l > 0 and < 1 → convert to ml
// Otherwise returns the amount and unit as-is.
func normalizeMetricAmount(amount string, unit string) (string, string) {
	num, u := splitAmount(amount)
	if num < 0 || u != "" {
		return amount, unit // non-numeric or already has embedded unit
	}

	switch unit {
	case "mg":
		if num >= 1000*1000 {
			return formatAmount(num/(1000*1000), ""), "kg"
		}
		if num >= 1000 {
			return formatAmount(num/1000, ""), "g"
		}
	case "g":
		if num >= 1000 {
			return formatAmount(num/1000, ""), "kg"
		}
		if num > 0 && num < 1 {
			return formatAmount(num*1000, ""), "mg"
		}
	case "kg":
		if num > 0 && num < 1 {
			return formatAmount(num*1000, ""), "g"
		}
	case "ml":
		if num >= 1000 {
			return formatAmount(num/1000, ""), "l"
		}
	case "l":
		if num > 0 && num < 1 {
			return formatAmount(num*1000, ""), "ml"
		}
	}
	return amount, unit
}

// scaleAmountFloat multiplies a numeric amount by a float64 factor
// (e.g. 0.5 when people=1 and recipe serves 2).
func scaleAmountFloat(amount string, factor float64) string {
	num, unit := splitAmount(amount)
	if num < 0 {
		return amount // non-numeric, leave as-is
	}
	return formatAmount(num*factor, unit)
}

// --- Print action ---

// printGroceryListParams is the JSON body for the print_grocery_list action.
type printGroceryListParams struct {
	ListID int64 `json:"list_id"`
}

// printGroceryListAction loads a grocery list from the DB, formats it for
// the thermal printer, and sends it to the device.
func printGroceryListAction(db *sql.DB, params json.RawMessage) (any, error) {
	var p printGroceryListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.ListID <= 0 {
		return nil, fmt.Errorf("list_id is required")
	}

	// Load the grocery list.
	var gl GroceryList
	var itemsJSON string
	err := db.QueryRow(`
		SELECT id, generated_at, num_days, num_people, items_json
		FROM ct_recipe_overview_grocery_lists WHERE id = ?`, p.ListID,
	).Scan(&gl.ID, &gl.GeneratedAt, &gl.NumDays, &gl.NumPeople, &itemsJSON)
	if err != nil {
		return nil, fmt.Errorf("grocery list %d: %w", p.ListID, err)
	}

	if itemsJSON != "" {
		if err := json.Unmarshal([]byte(itemsJSON), &gl.Items); err != nil {
			return nil, fmt.Errorf("unmarshal items: %w", err)
		}
	}

	// Load recipe names.
	recipeRows, err := db.Query(`
		SELECT n.title
		FROM ct_recipe_overview_grocery_list_recipes glr
		JOIN notes n ON n.id = glr.recipe_note_id
		WHERE glr.grocery_list_id = ?
		ORDER BY glr.recipe_note_id`, p.ListID,
	)
	if err != nil {
		return nil, fmt.Errorf("load recipes: %w", err)
	}
	defer recipeRows.Close()
	for recipeRows.Next() {
		var title string
		if err := recipeRows.Scan(&title); err != nil {
			return nil, fmt.Errorf("scan recipe name: %w", err)
		}
		gl.RecipeNames = append(gl.RecipeNames, title)
		gl.RecipeIDs = append(gl.RecipeIDs, 0) // placeholder
	}
	if err := recipeRows.Err(); err != nil {
		return nil, err
	}

	// Format.
	buf := FormatGroceryListReceipt(gl)

	// Connect to printer.
	prDev, err := printer.FindPrinter()
	if err != nil {
		preview := FormatGroceryListText(gl)
		return map[string]any{
			"printed": false,
			"preview": preview,
			"error":   err.Error(),
		}, nil
	}

	if err := printer.SendAndCut(prDev, buf); err != nil {
		return nil, fmt.Errorf("send to printer: %w", err)
	}

	return map[string]any{"printed": true}, nil
}
