package recipe

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/printer"
)

// DefaultPrintWidth is the line width for receipt printing.
// 48 chars on an 80 mm thermal printer at 203.2 dpi (~640 dots across,
// standard Font A at 12 dots/char).  Characters are rendered in BigSize
// (bold + double height, no double width).
const DefaultPrintWidth = 48

var (
	markdownImagePattern   = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]*)\)`)
	markdownLinkPattern    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]*)\)`)
	markdownHeadingPattern = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	markdownTaskPattern    = regexp.MustCompile(`^(\s*)[-+*]\s+\[([ xX])\]\s+(.*)$`)
	markdownBulletPattern  = regexp.MustCompile(`^(\s*)[-+*]\s+(.*)$`)
	markdownOrderedPattern = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	markdownRulePattern    = regexp.MustCompile(`^\s*([-*_]\s*){3,}$`)
)

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
	return formatRecipeReceipt(payload, title, body, nil)
}

// FormatRecipeReceiptWithImages formats a recipe and prints markdown images
// as actual ESC/POS bit images when imagePrinter can resolve them.
func FormatRecipeReceiptWithImages(payload Payload, title string, body string, imagePrinter func(*printer.Buf, int64) error) *printer.Buf {
	return formatRecipeReceipt(payload, title, body, imagePrinter)
}

func formatRecipeReceipt(payload Payload, title string, body string, imagePrinter func(*printer.Buf, int64) error) *printer.Buf {
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
		WriteMarkdownToReceipt(b, body, w-2, imagePrinter)
	}

	b.HLine(w)

	// Footer
	b.AlignCenter()
	b.Ln()

	return b
}

// WrapLines splits plain text into lines no longer than maxWidth.
// Exported for use by the print plugin.
func WrapLines(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		out = append(out, wrapWithPrefixes("", "", paragraph, maxWidth)...)
	}
	return out
}

// FormatMarkdownForPrint converts markdown into readable plain text while
// preserving paragraphs and common list structure for receipt printing.
func FormatMarkdownForPrint(markdown string, maxWidth int) []string {
	return MarkdownPrintLines(markdown, maxWidth)
}

func markdownLineToPrint(rawLine string, maxWidth int, inCodeFence *bool) ([]string, bool) {
	line := strings.TrimRight(rawLine, " \t")
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
		*inCodeFence = !*inCodeFence
		return nil, false
	}
	if *inCodeFence {
		if trimmed == "" {
			return []string{""}, false
		}
		return []string{line}, false
	}
	if trimmed == "" {
		return []string{""}, false
	}
	if markdownRulePattern.MatchString(trimmed) {
		return []string{strings.Repeat("-", maxWidth)}, false
	}
	if matches := markdownHeadingPattern.FindStringSubmatch(line); len(matches) == 3 {
		text := cleanMarkdownInline(matches[2])
		if text == "" {
			return nil, false
		}
		return []string{text}, true
	}
	if matches := markdownTaskPattern.FindStringSubmatch(line); len(matches) == 4 {
		marker := "☐ "
		if strings.EqualFold(matches[2], "x") {
			marker = "☑ "
		}
		return []string{matches[1] + marker + cleanMarkdownInline(matches[3])}, false
	}
	if matches := markdownBulletPattern.FindStringSubmatch(line); len(matches) == 3 {
		return []string{matches[1] + "• " + cleanMarkdownInline(matches[2])}, false
	}
	if matches := markdownOrderedPattern.FindStringSubmatch(line); len(matches) == 4 {
		return []string{matches[1] + matches[2] + ". " + cleanMarkdownInline(matches[3])}, false
	}
	if strings.HasPrefix(trimmed, ">") {
		quoted := strings.TrimSpace(strings.TrimLeft(trimmed, ">"))
		if quoted == "" {
			return []string{""}, false
		}
		return []string{"| " + cleanMarkdownInline(quoted)}, false
	}
	text := cleanMarkdownInline(line)
	if text == "" {
		return nil, false
	}
	return []string{text}, false
}

func cleanMarkdownInline(line string) string {
	line = markdownImagePattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := markdownImagePattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return markdownImagePlaceholder("")
		}
		return markdownImagePlaceholder(parts[1])
	})
	line = markdownLinkPattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := markdownLinkPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		text := strings.TrimSpace(parts[1])
		url := strings.TrimSpace(parts[2])
		if text != "" {
			return text
		}
		return url
	})
	line = strings.NewReplacer(
		"**", "",
		"__", "",
		"~~", "",
		"`", "",
		"*", "",
		"_", "",
	).Replace(line)
	line = strings.TrimSpace(line)
	line = strings.Join(strings.Fields(line), " ")
	return line
}

func wrapMarkdownLine(line string, maxWidth int) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return []string{""}
	}
	if strings.HasPrefix(trimmed, "• ") {
		return wrapWithPrefixes("• ", "  ", strings.TrimSpace(strings.TrimPrefix(trimmed, "• ")), maxWidth)
	}
	if strings.HasPrefix(trimmed, "☐ ") {
		return wrapWithPrefixes("☐ ", "  ", strings.TrimSpace(strings.TrimPrefix(trimmed, "☐ ")), maxWidth)
	}
	if strings.HasPrefix(trimmed, "☑ ") {
		return wrapWithPrefixes("☑ ", "  ", strings.TrimSpace(strings.TrimPrefix(trimmed, "☑ ")), maxWidth)
	}
	if strings.HasPrefix(trimmed, "| ") {
		return wrapWithPrefixes("| ", "  ", strings.TrimSpace(strings.TrimPrefix(trimmed, "| ")), maxWidth)
	}
	if matches := markdownOrderedPattern.FindStringSubmatch(trimmed); len(matches) == 4 {
		prefix := matches[2] + ". "
		return wrapWithPrefixes(prefix, strings.Repeat(" ", printer.TextWidth(prefix)), strings.TrimSpace(matches[3]), maxWidth)
	}
	return wrapWithPrefixes("", "", trimmed, maxWidth)
}

func wrapWithPrefixes(prefix string, continuationPrefix string, text string, maxWidth int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{strings.TrimRight(prefix, " ")}
	}
	var out []string
	currentPrefix := prefix
	remaining := text
	for remaining != "" {
		available := maxWidth - printer.TextWidth(currentPrefix)
		if available <= 0 {
			out = append(out, currentPrefix+remaining)
			break
		}
		if printer.TextWidth(remaining) <= available {
			out = append(out, currentPrefix+remaining)
			break
		}
		cut := wrapCutIndex(remaining, available)
		part := strings.TrimSpace(remaining[:cut])
		if part == "" {
			part = strings.TrimSpace(remaining)
			out = append(out, currentPrefix+part)
			break
		}
		out = append(out, currentPrefix+part)
		remaining = strings.TrimSpace(remaining[cut:])
		currentPrefix = continuationPrefix
	}
	return out
}

func wrapCutIndex(text string, maxWidth int) int {
	lastSpace := -1
	lastRuneEnd := 0
	width := 0
	for idx, r := range text {
		runeWidth := printer.TextWidth(string(r))
		next := idx + len(string(r))
		if width+runeWidth > maxWidth {
			if lastSpace > 0 {
				return lastSpace
			}
			if lastRuneEnd > 0 {
				return lastRuneEnd
			}
			return next
		}
		width += runeWidth
		if r == ' ' {
			lastSpace = idx
		}
		lastRuneEnd = next
	}
	return len(text)
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
		for _, line := range FormatMarkdownForPrint(body, w-2) {
			if line == "" {
				sb.WriteByte('\n')
				continue
			}
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
