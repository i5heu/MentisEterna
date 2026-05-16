package recipe

import (
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/printer"
)

// DefaultPrintWidth is the line width for receipt printing.
// 32 chars at normal size → 16 chars at double size.
const DefaultPrintWidth = 32

// FormatRecipeReceipt formats a recipe into an ESC/POS buffer suitable for
// thermal receipt printing. It produces a nicely styled receipt with:
//
//   - Double-size centered title
//   - Horizontal rule
//   - Ingredient list (left-aligned, name on the left, amount+unit on the right)
//   - Horizontal rule
//   - Detail fields (servings, timing, nutrition, freezable, pre-cook)
//   - Footer line feeds + partial cut
//
// Other note types can follow this same pattern:
//  1. Create a printer.Buf
//  2. Call b.Init(), set alignment and styles
//  3. Write formatted text with b.Text(), b.Textf(), etc.
//  4. Send to a printer.Printer with printer.SendAndCut()
func FormatRecipeReceipt(payload Payload, title string) *printer.Buf {
	b := new(printer.Buf)
	b.Init()
	w := DefaultPrintWidth

	// Header — centered, double sized
	b.AlignCenter()
	b.DoubleSize()
	maxTitle := w / 2 // double size halves available width
	if len(title) > maxTitle {
		title = title[:maxTitle]
	}
	b.Text(title)
	b.Ln()
	b.NormalSize()

	b.AlignLeft()
	b.DoubleHLine(w)

	// --- Ingredients ---
	b.Bold(true)
	b.Text("Ingredients")
	b.Bold(false)
	b.Ln()

	if len(payload.Ingredients) == 0 {
		b.Text("  (none)\n")
	}

	for _, ing := range payload.Ingredients {
		bullet := "  " + ing.Name
		right := ""
		if ing.Amount != "" {
			right = ing.Amount
			if ing.Unit != "" {
				right += " " + ing.Unit
			}
		}

		if right != "" {
			// Left-pad bullet to fill the line width minus the right side.
			maxName := w - len(right) - 1 // -1 for the gap
			if len(bullet) > maxName {
				if maxName > 5 {
					bullet = bullet[:maxName-1] + "\u2026" // ellipsis
				} else {
					bullet = bullet[:maxName]
				}
			}
			line := printer.PadRight(bullet, w-len(right))
			b.Text(line + right + "\n")
		} else {
			b.Text(bullet + "\n")
		}
	}

	b.HLine(w)

	// --- Details ---
	b.Bold(true)
	b.Text("Details")
	b.Bold(false)
	b.Ln()

	detail := func(label, value string) {
		if value != "" {
			b.Textf("  %s: %s\n", label, value)
		}
	}

	detail("Servings", payload.Servings)
	detail("Attention time", payload.AttentionTime)
	detail("Total time", payload.TotalTime)
	detail("Grams/serving", payload.GramsPerServing)
	detail("Kcal/serving", payload.KcalPerServing)
	if payload.Freezable {
		b.Text("  Freezable: yes\n")
	}
	if payload.PreCookServings != "" {
		detail("Pre-cook servings", payload.PreCookServings)
	}

	b.HLine(w)

	// Footer
	b.AlignCenter()
	b.Ln()

	return b
}

// RecipeTextPrint returns a plain-text rendition of the recipe formatted for
// a thermal receipt. This is useful for:
//   - Preview when the printer is not connected
//   - Testing the formatting logic
//   - Sending to non-ESC/POS printers or logging
func RecipeTextPrint(payload Payload, title string) string {
	w := DefaultPrintWidth
	var sb strings.Builder

	sb.WriteString(centerPad(title, w))
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("=", w))
	sb.WriteByte('\n')

	sb.WriteString("Ingredients\n")
	if len(payload.Ingredients) == 0 {
		sb.WriteString("  (none)\n")
	}
	for _, ing := range payload.Ingredients {
		bullet := "  " + ing.Name
		right := ""
		if ing.Amount != "" {
			right = ing.Amount
			if ing.Unit != "" {
				right += " " + ing.Unit
			}
		}
		if right != "" {
			maxName := w - len(right) - 1
			if len(bullet) > maxName {
				if maxName > 5 {
					bullet = bullet[:maxName-1] + "\u2026"
				} else {
					bullet = bullet[:maxName]
				}
			}
			line := printer.PadRight(bullet, w-len(right))
			sb.WriteString(line + right + "\n")
		} else {
			sb.WriteString(bullet + "\n")
		}
	}

	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	sb.WriteString("Details\n")
	detailText := func(label, value string) {
		if value != "" {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", label, value))
		}
	}
	detailText("Servings", payload.Servings)
	detailText("Attention time", payload.AttentionTime)
	detailText("Total time", payload.TotalTime)
	detailText("Grams/serving", payload.GramsPerServing)
	detailText("Kcal/serving", payload.KcalPerServing)
	if payload.Freezable {
		sb.WriteString("  Freezable: yes\n")
	}
	if payload.PreCookServings != "" {
		detailText("Pre-cook servings", payload.PreCookServings)
	}

	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	return sb.String()
}

// centerPad centers s in a field of width w.
func centerPad(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	left := (w - len(s)) / 2
	return strings.Repeat(" ", left) + s
}
