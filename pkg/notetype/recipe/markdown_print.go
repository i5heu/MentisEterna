package recipe

import (
	"strconv"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/printer"
)

type MarkdownPrintBlockKind string

const (
	MarkdownPrintBlockText  MarkdownPrintBlockKind = "text"
	MarkdownPrintBlockBlank MarkdownPrintBlockKind = "blank"
	MarkdownPrintBlockImage MarkdownPrintBlockKind = "image"
)

type MarkdownPrintBlock struct {
	Kind   MarkdownPrintBlockKind
	Text   string
	Alt    string
	FileID int64
}

func MarkdownPrintBlocks(markdown string, maxWidth int) []MarkdownPrintBlock {
	if maxWidth <= 0 {
		return nil
	}

	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.ReplaceAll(markdown, "\r", "\n")

	var out []MarkdownPrintBlock
	inCodeFence := false
	for _, rawLine := range strings.Split(markdown, "\n") {
		blocks, blankAfter := markdownLineToBlocks(rawLine, maxWidth, &inCodeFence)
		if len(blocks) == 0 {
			appendBlankMarkdownBlock(&out)
			continue
		}
		for _, block := range blocks {
			if block.Kind == MarkdownPrintBlockBlank {
				appendBlankMarkdownBlock(&out)
				continue
			}
			out = append(out, block)
		}
		if blankAfter {
			appendBlankMarkdownBlock(&out)
		}
	}

	for len(out) > 0 && out[0].Kind == MarkdownPrintBlockBlank {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1].Kind == MarkdownPrintBlockBlank {
		out = out[:len(out)-1]
	}
	return out
}

func MarkdownPrintLines(markdown string, maxWidth int) []string {
	return markdownBlocksToLines(MarkdownPrintBlocks(markdown, maxWidth))
}

func WriteMarkdownToReceipt(b *printer.Buf, markdown string, maxWidth int, imagePrinter func(*printer.Buf, int64) error) {
	for _, block := range MarkdownPrintBlocks(markdown, maxWidth) {
		switch block.Kind {
		case MarkdownPrintBlockBlank:
			b.Ln()
		case MarkdownPrintBlockImage:
			printed := false
			if imagePrinter != nil && block.FileID > 0 {
				b.AlignCenter()
				if err := imagePrinter(b, block.FileID); err == nil {
					printed = true
				}
				b.AlignLeft()
			}
			if printed {
				b.Ln()
				continue
			}
			b.Text("  " + markdownImagePlaceholder(block.Alt) + "\n")
		case MarkdownPrintBlockText:
			b.Text("  " + block.Text + "\n")
		}
	}
}

func markdownLineToBlocks(rawLine string, maxWidth int, inCodeFence *bool) ([]MarkdownPrintBlock, bool) {
	line := strings.TrimRight(rawLine, " \t")
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
		*inCodeFence = !*inCodeFence
		return nil, false
	}
	if *inCodeFence {
		if trimmed == "" {
			return []MarkdownPrintBlock{{Kind: MarkdownPrintBlockBlank}}, false
		}
		return wrapTextBlocks(line, maxWidth), false
	}
	if blocks, ok := markdownImageBlocks(trimmed); ok {
		return blocks, false
	}

	lines, blankAfter := markdownLineToPrint(rawLine, maxWidth, inCodeFence)
	var blocks []MarkdownPrintBlock
	for _, line := range lines {
		if line == "" {
			appendBlankMarkdownBlock(&blocks)
			continue
		}
		blocks = append(blocks, wrapTextBlocks(line, maxWidth)...)
	}
	return blocks, blankAfter
}

func wrapTextBlocks(line string, maxWidth int) []MarkdownPrintBlock {
	wrapped := wrapMarkdownLine(line, maxWidth)
	blocks := make([]MarkdownPrintBlock, 0, len(wrapped))
	for _, part := range wrapped {
		if part == "" {
			appendBlankMarkdownBlock(&blocks)
			continue
		}
		blocks = append(blocks, MarkdownPrintBlock{Kind: MarkdownPrintBlockText, Text: part})
	}
	return blocks
}

func markdownImageBlocks(trimmed string) ([]MarkdownPrintBlock, bool) {
	matches := markdownImagePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return nil, false
	}
	leftover := strings.TrimSpace(markdownImagePattern.ReplaceAllString(trimmed, ""))
	if leftover != "" {
		return nil, false
	}
	blocks := make([]MarkdownPrintBlock, 0, len(matches))
	for _, match := range matches {
		alt := ""
		if len(match) > 1 {
			alt = strings.TrimSpace(match[1])
		}
		fileID, ok := extractFileIDFromMarkdownURL(match[2])
		blocks = append(blocks, MarkdownPrintBlock{
			Kind:   MarkdownPrintBlockImage,
			Alt:    alt,
			FileID: fileID,
		})
		if !ok {
			blocks[len(blocks)-1].FileID = 0
		}
	}
	return blocks, true
}

func markdownBlocksToLines(blocks []MarkdownPrintBlock) []string {
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Kind {
		case MarkdownPrintBlockBlank:
			lines = append(lines, "")
		case MarkdownPrintBlockImage:
			lines = append(lines, markdownImagePlaceholder(block.Alt))
		case MarkdownPrintBlockText:
			lines = append(lines, block.Text)
		}
	}
	return lines
}

func appendBlankMarkdownBlock(blocks *[]MarkdownPrintBlock) {
	if len(*blocks) > 0 && (*blocks)[len(*blocks)-1].Kind == MarkdownPrintBlockBlank {
		return
	}
	*blocks = append(*blocks, MarkdownPrintBlock{Kind: MarkdownPrintBlockBlank})
}

func markdownImagePlaceholder(alt string) string {
	alt = strings.TrimSpace(alt)
	if alt == "" {
		return "[Image]"
	}
	return "[Image: " + alt + "]"
}

func extractFileIDFromMarkdownURL(rawURL string) (int64, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if idx := strings.IndexAny(rawURL, " \t\n"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	rawURL = strings.Trim(rawURL, "<>")
	idx := strings.Index(rawURL, "/file/")
	if idx < 0 {
		return 0, false
	}
	path := rawURL[idx+len("/file/"):]
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		return 0, false
	}
	filePart := parts[1]
	end := 0
	for end < len(filePart) && filePart[end] >= '0' && filePart[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	fileID, err := strconv.ParseInt(filePart[:end], 10, 64)
	if err != nil || fileID <= 0 {
		return 0, false
	}
	return fileID, true
}
