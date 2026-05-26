package recipe

import (
	"math"
	"strconv"
	"strings"
)

func ScalePayloadForServings(payload Payload, displayServings string) Payload {
	baseServings, ok := parseRecipeNumericValue(payload.Servings)
	if !ok || baseServings <= 0 {
		return payload
	}

	targetServings, ok := parseRecipeNumericValue(displayServings)
	if !ok || targetServings <= 0 {
		return payload
	}

	scaled := payload
	scaled.Servings = formatRecipeNumericValue(targetServings)
	factor := targetServings / baseServings
	if nearlyEqualFloat64(factor, 1) {
		return scaled
	}

	scaled.Ingredients = make([]IngredientRow, len(payload.Ingredients))
	for i, ingredient := range payload.Ingredients {
		next := ingredient
		next.Amount = scaleRecipeAmountString(ingredient.Amount, factor)
		next.NonMetricAmount = scaleRecipeAmountString(ingredient.NonMetricAmount, factor)
		scaled.Ingredients[i] = next
	}

	return scaled
}

func parseRecipeNumericValue(value string) (float64, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	if !isRecipeNumericString(trimmed) {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(trimmed, ",", "."), 64)
	if err != nil || !isFinitePositive(parsed) {
		return 0, false
	}
	return parsed, true
}

func scaleRecipeAmountString(amount string, factor float64) string {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" || !isFinitePositive(factor) {
		return trimmed
	}
	parsed, ok := parseRecipeNumericValue(trimmed)
	if !ok {
		return trimmed
	}
	return formatRecipeNumericValue(parsed * factor)
}

func formatRecipeNumericValue(value float64) string {
	if !isFinitePositive(value) {
		return ""
	}
	if nearlyEqualFloat64(value, math.Round(value)) {
		return strconv.FormatInt(int64(math.Round(value)), 10)
	}
	formatted := strconv.FormatFloat(value, 'f', 6, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

func isRecipeNumericString(value string) bool {
	if value == "" {
		return false
	}
	hasDigit := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.' || r == ',':
		default:
			return false
		}
	}
	return hasDigit
}

func nearlyEqualFloat64(left float64, right float64) bool {
	return math.Abs(left-right) < 1e-9
}

func isFinitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
