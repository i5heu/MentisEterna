package recipeoverview

import (
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype/recipe"
	"github.com/i5heu/MentisEterna/pkg/printer"
)

const groceryPrintWidth = 48

// FormatGroceryListReceipt formats a grocery list into an ESC/POS buffer.
func FormatGroceryListReceipt(gl GroceryList) *printer.Buf {
	b := new(printer.Buf)
	b.Init()
	b.FontA()
	b.BigSize()
	w := groceryPrintWidth

	// Header.
	b.AlignCenter()
	title := printer.TruncateWidth("Grocery List", w)
	b.Text(title)
	b.Ln()

	b.FontB()
	b.BigSize()
	b.AlignLeft()
	b.HLine(w)

	// Config line.
	b.Textf("  %d people", gl.NumPeople)
	if gl.NumDays > 0 {
		b.Textf(", %d days", gl.NumDays)
	}
	b.Ln()
	b.Ln()

	// Recipe names.
	if len(gl.RecipeNames) > 0 {
		b.Text("  Recipes:")
		b.Ln()
		for _, name := range gl.RecipeNames {
			line := printer.TruncateWithEllipsis("    - "+name, w-2)
			b.Text(line)
			b.Ln()
		}
		b.Ln()
	}

	// Items.
	b.Text("  Items")
	b.Ln()
	b.HLine(w)

	if len(gl.Items) == 0 {
		b.Text("  (none)\n")
	}

	for _, group := range groupGroceryItems(gl.Items) {
		b.FontA()
		b.BigSize()
		b.Text(formatCategoryHeading(group.Category))
		b.Ln()
		b.FontB()
		b.BigSize()
		b.Bold(true)
		for i, it := range group.Items {
			if i > 0 && i%3 == 0 {
				b.SpacerLine(w)
			}

			right := it.Amount
			if it.Unit != "" {
				right += " " + it.Unit
			}
			name := " " + it.Name

			if right != "" && strings.TrimSpace(right) != "" {
				rightWidth := printer.TextWidth(right)
				maxName := w - rightWidth - 1
				if printer.TextWidth(name) > maxName {
					name = printer.TruncateWithEllipsis(name, maxName)
				}
				line := printer.PadRight(name, w-rightWidth)
				b.Text(line + right + "\n")
			} else {
				b.Text(name + "\n")
			}
		}
		b.Bold(false)
		b.Ln()
	}

	b.HLine(w)
	b.AlignCenter()
	b.Ln()

	return b
}

// FormatGroceryListText returns a plain-text preview of a grocery list.
func FormatGroceryListText(gl GroceryList) string {
	w := groceryPrintWidth
	var sb strings.Builder

	sb.WriteString(recipe.CenterPad("Grocery List", w))
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	sb.WriteString(fmt.Sprintf("  %d people", gl.NumPeople))
	if gl.NumDays > 0 {
		sb.WriteString(fmt.Sprintf(", %d days", gl.NumDays))
	}
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if len(gl.RecipeNames) > 0 {
		sb.WriteString("  Recipes:\n")
		for _, name := range gl.RecipeNames {
			line := printer.TruncateWithEllipsis("    - "+name, w-2)
			sb.WriteString(line + "\n")
		}
		sb.WriteByte('\n')
	}

	sb.WriteString("  Items\n")
	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	if len(gl.Items) == 0 {
		sb.WriteString("  (none)\n")
	}

	for _, group := range groupGroceryItems(gl.Items) {
		sb.WriteString(formatCategoryHeading(group.Category) + "\n")
		for i, it := range group.Items {
			if i > 0 && i%3 == 0 {
				sb.WriteString(strings.Repeat("-", w))
				sb.WriteByte('\n')
			}
			right := it.Amount
			if it.Unit != "" {
				right += " " + it.Unit
			}
			name := " " + it.Name

			if right != "" && strings.TrimSpace(right) != "" {
				rightWidth := printer.TextWidth(right)
				maxName := w - rightWidth - 1
				if printer.TextWidth(name) > maxName {
					name = printer.TruncateWithEllipsis(name, maxName)
				}
				line := printer.PadRight(name, w-rightWidth)
				sb.WriteString(line + right + "\n")
			} else {
				sb.WriteString(name + "\n")
			}
		}
		sb.WriteByte('\n')
	}

	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	return sb.String()
}

func formatCategoryHeading(category string) string {
	switch recipe.NormalizeIngredientCategory(category) {
	case "vegetables":
		return "Vegetables"
	case "fruit":
		return "Fruit"
	case "meat":
		return "Meat"
	case "dairy":
		return "Dairy"
	case "fish":
		return "Fish"
	case "chilled & deli":
		return "Chilled & Deli"
	case "frozen":
		return "Frozen"
	case "spices":
		return "Spices"
	case "beverages":
		return "Beverages"
	case "household":
		return "Household"
	default:
		return "Other"
	}
}
