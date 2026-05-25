package recipe

import (
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/printer"
)

// DefaultPrintWidth is the line width for receipt printing.
// 48 chars on an 80 mm thermal printer at 203.2 dpi (~640 dots across,
// standard Font A at 12 dots/char).  Characters are rendered in BigSize
// (bold + double height, no double width).
const DefaultPrintWidth = 48

// FormatRecipeReceipt formats a recipe into an ESC/POS buffer suitable for
// thermal receipt printing.  All text is BigSize (bold + double height,
// ~1.5× normal) for readability while keeping the full 42-char, 80 mm line.
//
// Other note types can follow this same pattern:
//  1. Create a printer.Buf
//  2. Call b.Init(), set alignment and styles
//  3. Write formatted text with b.Text(), b.Textf(), etc.
//  4. Send to a printer.Printer with printer.SendAndCut()
func FormatRecipeReceipt(payload Payload, title string, body string) *printer.Buf {
	b := new(printer.Buf)
	b.Init()
	b.BigSize()
	w := DefaultPrintWidth // 42

	// Header — centered.
	b.AlignCenter()
	title = printer.TruncateWidth(title, w)
	b.Text(title)
	b.Ln()

	b.AlignLeft()
	b.HLine(w)

	// --- Ingredients ---
	b.Text("Ingredients")
	b.Ln()

	if len(payload.Ingredients) == 0 {
		b.Text("  (none)\n")
	}

	for _, ing := range payload.Ingredients {
		name := "  " + ing.Name
		if strings.TrimSpace(ing.Prepare) != "" {
			name += " (" + strings.TrimSpace(ing.Prepare) + ")"
		}
		right := ""
		if ing.Amount != "" {
			right = ing.Amount
			if ing.Unit != "" {
				right += " " + ing.Unit
			}
		}

		if right != "" {
			// Pad name to fill width minus right side.
			rightWidth := printer.TextWidth(right)
			maxName := w - rightWidth - 1 // -1 for the gap
			if printer.TextWidth(name) > maxName {
				name = printer.TruncateWithEllipsis(name, maxName)
			}
			line := printer.PadRight(name, w-rightWidth)
			b.Text(line + right + "\n")
		} else {
			b.Text(name + "\n")
		}
	}

	b.HLine(w)

	// --- Details ---
	b.Text("Details")
	b.Ln()

	detail := func(label, value string) {
		if value != "" {
			b.Textf("  %s: %s\n", label, value)
		}
	}

	detail("Servings", payload.Servings)
	detail("Attention time", payload.AttentionTime)
	detail("Total time", payload.TotalTime)
	detail("Grams per serving", payload.GramsPerServing)
	detail("Kcal per serving", payload.KcalPerServing)
	detail("Rating", fmt.Sprintf("%d/10", payload.Rating))
	if payload.Freezable {
		b.Text("  Freezable: yes\n")
	}
	if payload.PreCookServings != "" {
		detail("Pre-cook servings", payload.PreCookServings)
	}

	// --- Body (markdown notes) ---
	if strings.TrimSpace(body) != "" {
		b.HLine(w)
		b.Text("Notes")
		b.Ln()
		// Wrap long lines.
		for _, line := range WrapLines(body, w-2) {
			b.Text("  " + line + "\n")
		}
	}

	b.HLine(w)

	// Footer
	b.AlignCenter()
	b.Ln()

	return b
}

// WrapLines splits text into lines no longer than maxWidth.
// Exported for use by the print plugin.
func WrapLines(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		for printer.TextWidth(paragraph) > maxWidth {
			runes := []rune(paragraph)
			cut := maxWidth
			if cut > len(runes) {
				cut = len(runes)
			}
			for i := cut; i > maxWidth/2; i-- {
				if runes[i-1] == ' ' {
					cut = i - 1
					break
				}
			}
			out = append(out, strings.TrimSpace(string(runes[:cut])))
			paragraph = strings.TrimSpace(string(runes[cut:]))
		}
		if paragraph != "" {
			out = append(out, paragraph)
		}
	}
	return out
}

// RecipeTextPrint returns a plain-text rendition of the recipe formatted for
// a thermal receipt.  Matches the BigSize layout used by FormatRecipeReceipt.
// This is useful for:
//   - Preview when the printer is not connected
//   - Testing the formatting logic
//   - Sending to non-ESC/POS printers or logging
func RecipeTextPrint(payload Payload, title string, body string) string {
	w := DefaultPrintWidth // 42
	var sb strings.Builder

	sb.WriteString(CenterPad(title, w))
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	sb.WriteString("Ingredients\n")
	if len(payload.Ingredients) == 0 {
		sb.WriteString("  (none)\n")
	}
	for _, ing := range payload.Ingredients {
		name := "  " + ing.Name
		if strings.TrimSpace(ing.Prepare) != "" {
			name += " (" + strings.TrimSpace(ing.Prepare) + ")"
		}
		right := ""
		if ing.Amount != "" {
			right = ing.Amount
			if ing.Unit != "" {
				right += " " + ing.Unit
			}
		}
		if right != "" {
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
	detailText("Grams per serving", payload.GramsPerServing)
	detailText("Kcal per serving", payload.KcalPerServing)
	detailText("Rating", fmt.Sprintf("%d/10", payload.Rating))
	if payload.Freezable {
		sb.WriteString("  Freezable: yes\n")
	}
	if payload.PreCookServings != "" {
		detailText("Pre-cook servings", payload.PreCookServings)
	}

	// Body.
	if strings.TrimSpace(body) != "" {
		sb.WriteString(strings.Repeat("-", w))
		sb.WriteByte('\n')
		sb.WriteString("Notes\n")
		for _, line := range WrapLines(body, w-2) {
			sb.WriteString("  " + line + "\n")
		}
	}

	sb.WriteString(strings.Repeat("-", w))
	sb.WriteByte('\n')

	return sb.String()
}

// CenterPad centers s in a field of width w.
// Exported for use by the print plugin.
func CenterPad(s string, w int) string {
	return printer.PadCenter(printer.TruncateWidth(s, w), w)
}
