package rss

import (
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	in := `<p>Hello <b>world</b> &amp; friends.</p><script>var x = 1;</script><p>Second paragraph.</p>`
	out := htmlToText(in)
	if !strings.Contains(out, "Hello world &amp") {
		if !strings.Contains(out, "Hello world") {
			t.Errorf("text missing: %q", out)
		}
	}
	if !strings.Contains(out, "friends.") {
		t.Errorf("text missing: %q", out)
	}
	if strings.Contains(out, "var x") || strings.Contains(out, "<b>") || strings.Contains(out, "&amp;") {
		t.Errorf("html/script not stripped: %q", out)
	}
	if !strings.Contains(out, "Second paragraph.") {
		t.Errorf("paragraph missing: %q", out)
	}
}

func TestHTMLToTextPlain(t *testing.T) {
	if got := htmlToText("no tags here"); got != "no tags here" {
		t.Errorf("plain text altered: %q", got)
	}
	if got := htmlToText("   "); got != "" {
		t.Errorf("whitespace: %q", got)
	}
}

const articleHTML = `<!DOCTYPE html>
<html><head><title>Page Title</title><script>function evil(){}</script>
<style>.x{color:red}</style></head>
<body>
<nav><a href="/">Home</a> <a href="/x">Menu link menu text</a></nav>
<article>
<h1>The Real Article Headline</h1>
<p>This is the first paragraph of the article body and it contains plenty of
plain text so the text length threshold is comfortably exceeded by far.</p>
<p>The second paragraph adds more content and keeps the link density low which
makes this container score highly in readability terms for the extractor.</p>
<p>A third paragraph to be safe about minimum lengths in all code paths.</p>
</article>
<footer>Copyright Site</footer>
</body></html>`

func TestExtractArticlePrefersArticleTag(t *testing.T) {
	text, ok := extractArticle([]byte(articleHTML))
	if !ok {
		t.Fatal("extraction failed")
	}
	if !strings.Contains(text, "The Real Article Headline") {
		t.Errorf("headline missing: %q", text)
	}
	if !strings.Contains(text, "first paragraph") || !strings.Contains(text, "third paragraph") {
		t.Errorf("paragraphs missing: %q", text)
	}
	if strings.Contains(text, "Menu link") || strings.Contains(text, "Copyright Site") {
		t.Errorf("nav/footer leaked: %q", text)
	}
	if strings.Contains(text, "evil") || strings.Contains(text, "color:red") {
		t.Errorf("script/style leaked: %q", text)
	}
}

func TestExtractArticleMainAndFallback(t *testing.T) {
	mainHTML := `<html><body><div><p>` + strings.Repeat("Word ", 100) + `</p></div>
	<main id="content"><p>` + strings.Repeat("Main text ", 120) + `</p></main></body></html>`
	text, ok := extractArticle([]byte(mainHTML))
	if !ok || !strings.Contains(text, "Main text") {
		t.Errorf("main extraction failed: ok=%v text=%q", ok, text)
	}

	// Readability-lite fallback: the link-heavy sidebar must lose to the
	// text-heavy div (all within <p> siblings, no article/main).
	noSemanticHTML := `<html><body>
	<div class="wrap">
		<div class="sidebar"><a href="/l1">link one text</a> <a href="/l2">link two text</a> <a href="/l3">link three text</a> <a href="/l4">link four text</a> <a href="/l5">link five text</a> <a href="/l6">link six text</a></div>
		<div class="content"><p>` + strings.Repeat("Content paragraph sentence here. ", 30) + `</p></div>
	</div></body></html>`
	text, ok = extractArticle([]byte(noSemanticHTML))
	if !ok {
		t.Fatal("fallback extraction failed")
	}
	if !strings.Contains(text, "Content paragraph") || strings.Contains(text, "link one text") {
		t.Errorf("readability picked the wrong container: %q", text)
	}
}

func TestExtractArticleRejectsBarrenPages(t *testing.T) {
	if _, ok := extractArticle([]byte("")); ok {
		t.Error("empty page should not extract")
	}
	bare := `<html><body><p>tiny</p></body></html>`
	if _, ok := extractArticle([]byte(bare)); ok {
		t.Error("page below threshold should not extract")
	}
}

func TestClipRunes(t *testing.T) {
	s := strings.Repeat("a", defaultFeedLength+10)
	got := clipRunes(s, defaultFeedLength)
	if len([]rune(got)) > defaultFeedLength+5 {
		t.Errorf("clip exceeded cap: %d runes", len([]rune(got)))
	}
	if s2 := clipRunes("short", defaultFeedLength); s2 != "short" {
		t.Errorf("short string altered: %q", s2)
	}
}
