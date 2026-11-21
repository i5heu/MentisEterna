package rss

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// readAllLimited reads at most limit bytes from r.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}

// skipTextTags elements never contribute to article text.
var skipTextTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"iframe": true, "svg": true, "form": true, "button": true, "select": true,
	"nav": true, "aside": true, "header": true, "footer": true,
}

// blockTags introduce line breaks in the rendered text.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "pre": true, "table": true, "tr": true, "section": true,
	"article": true, "hr": true, "dl": true, "dd": true, "dt": true,
}

// candidateTags are the block containers readability-lite considers.
var candidateTags = map[string]bool{
	"div": true, "section": true, "article": true, "main": true, "td": true, "body": true,
}

// htmlToText converts an HTML fragment or document to plain text: skipped
// elements are dropped, block tags break lines, entities are decoded, and
// output collapses to paragraphs capped at defaultFeedLength runes.
func htmlToText(fragment string) string {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" || !strings.Contains(fragment, "<") {
		return fragment
	}
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return clipRunes(fragment, defaultFeedLength)
	}
	var b strings.Builder
	writeNodeText(&b, doc)
	return collapseParagraphs(b.String(), defaultFeedLength)
}

// writeNodeText DFS-writes an element tree as text. Text nodes are written
// raw (whitespace collapses later); only block-level elements break lines,
// so inline markup like <b> stays inside the sentence.
func writeNodeText(b *strings.Builder, n *html.Node) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeNodeText(b, c)
		}
		return
	}
	if skipTextTags[n.Data] {
		return
	}
	if blockTags[n.Data] {
		b.WriteString("\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeNodeText(b, c)
	}
	if blockTags[n.Data] {
		b.WriteString("\n")
	}
}

// nodeTextLen returns the combined text length of a subtree (skipping
// non-text elements).
func nodeTextLen(n *html.Node) int {
	var count int
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			count += len(strings.TrimSpace(node.Data))
			return
		}
		if node.Type != html.ElementNode {
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			return
		}
		if skipTextTags[node.Data] {
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return count
}

// nodeTextLenInLinks returns the combined text length within <a> elements.
func nodeTextLenInLinks(n *html.Node) int {
	var count int
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inLink bool) {
		if node.Type == html.TextNode {
			if inLink {
				count += len(strings.TrimSpace(node.Data))
			}
			return
		}
		if node.Type != html.ElementNode {
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inLink)
			}
			return
		}
		if skipTextTags[node.Data] {
			return
		}
		isLink := inLink || node.Data == "a"
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c, isLink)
		}
	}
	walk(n, false)
	return count
}

// extractArticle extracts readable article text from a full HTML page.
// Preference: <article>, then <main>, then [role=main], then readability-lite
// scoring (text length × link density) over block candidates. Returns
// (text, true) when readable content was found.
func extractArticle(raw []byte) (string, bool) {
	if len(raw) == 0 || !bytes.Contains(bytes.ToLower(raw), []byte("<body")) {
		return "", false
	}
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	body := findTag(doc, "body")
	if body == nil {
		return "", false
	}

	content := findFirstText(body, "article", 250)
	if content == nil {
		content = findFirstText(body, "main", 250)
	}
	if content == nil {
		content = findRoleMain(body, 250)
	}
	if content == nil {
		content = readabilityBest(body)
	}
	if content == nil {
		return "", false
	}

	var b strings.Builder
	writeNodeText(&b, content)
	text := collapseParagraphs(b.String(), defaultFeedLength)
	if text == "" {
		return "", false
	}
	return text, true
}

// findTag returns the first descendant element node with the given tag.
func findTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findTag(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// findFirstText returns the first descendant element with the given tag whose
// subtree holds at least minLen bytes of text.
func findFirstText(n *html.Node, tag string, minLen int) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag && nodeTextLen(n) >= minLen {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstText(c, tag, minLen); found != nil {
			return found
		}
	}
	return nil
}

// findRoleMain returns the first element with role="main" attribute holding
// at least minLen bytes of text.
func findRoleMain(n *html.Node, minLen int) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == "role" && a.Val == "main" && nodeTextLen(n) >= minLen {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findRoleMain(c, minLen); found != nil {
			return found
		}
	}
	return nil
}

// readabilityBest scores candidates by text length × (1 - link density) and
// returns the best element with at least minimum readable text.
func readabilityBest(root *html.Node) *html.Node {
	type scored struct {
		node  *html.Node
		score float64
	}
	var candidates []scored
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && candidateTags[n.Data] {
			t := nodeTextLen(n)
			if t >= 250 {
				links := nodeTextLenInLinks(n)
				ld := 0.0
				if t > 0 {
					ld = float64(links) / float64(t)
				}
				candidates = append(candidates, scored{n, float64(t) * (1 - ld) * (1 - ld)})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	var best *scored
	for i := range candidates {
		if best == nil || candidates[i].score > best.score {
			best = &candidates[i]
		}
	}
	if best == nil {
		return nil
	}
	return best.node
}

// collapseParagraphs trims lines, drops empties, joins with blank lines, and
// caps the result at maxRunes runes.
func collapseParagraphs(s string, maxRunes int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	joined := strings.Join(out, "\n\n")
	return clipRunes(joined, maxRunes)
}

// clipRunes caps a string to maxRunes runes without splitting runes.
func clipRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "\n…"
}
