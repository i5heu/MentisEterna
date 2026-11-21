package rss

import (
	"strings"
	"testing"
)

const rssFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
	<title>Test Feed</title>
	<description>A feed for tests</description>
	<item>
		<title>First &amp; foremost</title>
		<link>https://example.com/a1</link>
		<guid isPermaLink="false">guid-a1</guid>
		<pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
		<description>Short &lt;b&gt;description&lt;/b&gt;</description>
		<content:encoded><![CDATA[<p>The <b>full story</b> lives here.</p><script>alert(1)</script>]]></content:encoded>
		<dc:creator>Test Author</dc:creator>
	</item>
	<item>
		<title>Second</title>
		<link>https://example.com/a2</link>
		<guid>guid-a2</guid>
		<pubDate>2026-05-01T12:00:00+02:00</pubDate>
		<description>Just a snippet.</description>
	</item>
</channel>
</rss>`

const atomFixture = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<title>Atom Test</title>
	<entry>
		<title>Atom Entry One</title>
		<link rel="self" href="https://example.com/atom-entry"/>
		<link rel="alternate" href="https://example.com/one-page"/>
		<id>urn:uuid:aaa-1</id>
		<published>2026-05-02T09:30:00Z</published>
		<updated>2026-05-02T10:00:00Z</updated>
		<summary>An atom summary with <i>tags</i>.</summary>
		<content type="html">&lt;p&gt;Atom body with entities: &amp;amp; ok&lt;/p&gt;</content>
		<author><name>Atom Author</name></author>
	</entry>
	<entry>
		<title>Atom Entry Two</title>
		<link href="https://example.com/two-page"/>
		<id>urn:uuid:aaa-2</id>
		<updated>2026-05-03T08:00:00Z</updated>
		<summary>Only a summary here.</summary>
	</entry>
</feed>`

func TestParseRSSFeed(t *testing.T) {
	feed, err := ParseFeed([]byte(rssFixture))
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	if feed.Title != "Test Feed" {
		t.Errorf("title = %q", feed.Title)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(feed.Items))
	}

	a1 := feed.Items[0]
	if a1.Title != "First & foremost" {
		t.Errorf("title = %q", a1.Title)
	}
	if a1.Link != "https://example.com/a1" {
		t.Errorf("link = %q", a1.Link)
	}
	if a1.ItemKey() != "guid-a1" {
		t.Errorf("key = %q", a1.ItemKey())
	}
	if a1.PublishedAt != "2006-01-02T15:04:05Z" {
		t.Errorf("published = %q", a1.PublishedAt)
	}
	if a1.Author != "Test Author" {
		t.Errorf("author = %q", a1.Author)
	}
	// content:encoded wins over description; HTML stripped; script dropped.
	if !strings.Contains(a1.Content, "The full story lives here.") {
		t.Errorf("content = %q", a1.Content)
	}
	if strings.Contains(a1.Content, "alert") || strings.Contains(a1.Content, "<b>") {
		t.Errorf("content contains html/script: %q", a1.Content)
	}

	a2 := feed.Items[1]
	if a2.PublishedAt != "2026-05-01T10:00:00Z" {
		t.Errorf("published+offset = %q", a2.PublishedAt)
	}
	if !strings.Contains(a2.Content, "Just a snippet.") {
		t.Errorf("description fallback content = %q", a2.Content)
	}
}

func TestParseAtomFeed(t *testing.T) {
	feed, err := ParseFeed([]byte(atomFixture))
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(feed.Items))
	}

	e1 := feed.Items[0]
	if e1.Link != "https://example.com/one-page" {
		t.Errorf("link rel=alternate missing: %q", e1.Link)
	}
	if e1.ItemKey() != "urn:uuid:aaa-1" {
		t.Errorf("key = %q", e1.ItemKey())
	}
	if e1.PublishedAt != "2026-05-02T09:30:00Z" {
		t.Errorf("published = %q", e1.PublishedAt)
	}
	if e1.Author != "Atom Author" {
		t.Errorf("author = %q", e1.Author)
	}
	// content wins over summary; entities decoded.
	if !strings.Contains(e1.Content, "Atom body with entities: & ok") {
		t.Errorf("content = %q", e1.Content)
	}

	e2 := feed.Items[1]
	if e2.Link != "https://example.com/two-page" {
		t.Errorf("bare link href missing: %q", e2.Link)
	}
	if e2.PublishedAt != "2026-05-03T08:00:00Z" {
		t.Errorf("updated fallback = %q", e2.PublishedAt)
	}
	if !strings.Contains(e2.Content, "Only a summary here.") {
		t.Errorf("summary fallback = %q", e2.Content)
	}
}

func TestParseFeedNonUTF8(t *testing.T) {
	// ISO-8859-1 XML with umlauts in the title (0xFC u-umlaut, 0xDF sharp s, 0xE4 a-umlaut).
	src := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n" +
		"<rss version=\"2.0\"><channel><title>Gr\xfc\xdfe</title>\n" +
		"<item><title>Oberl\xe4nge</title><guid>g1</guid></item>\n" +
		"</channel></rss>"
	feed, err := ParseFeed([]byte(src))
	if err != nil {
		t.Fatalf("ParseFeed latin1: %v", err)
	}
	if !strings.Contains(feed.Title, "Grüße") {
		t.Errorf("latin1 title not converted: %q", feed.Title)
	}
	if len(feed.Items) != 1 || !strings.Contains(feed.Items[0].Title, "Oberlänge") {
		t.Errorf("latin1 item title not converted: %+v", feed.Items)
	}
}

func TestParseFeedRejectsGarbage(t *testing.T) {
	if _, err := ParseFeed([]byte("this is not xml at all")); err == nil {
		t.Error("garbage should not parse")
	}
	if _, err := ParseFeed(nil); err == nil {
		t.Error("empty should not parse")
	}
}

func TestItemKeyFallbackHash(t *testing.T) {
	a := FeedItem{Title: "Same", Link: "https://example.com/x", PublishedAt: "2026-05-01T00:00:00Z"}
	b := FeedItem{Title: "Same", Link: "https://example.com/x", PublishedAt: "2026-05-01T00:00:00Z"}
	if a.ItemKey() != b.ItemKey() {
		t.Error("identical items should hash identically")
	}
	c := FeedItem{Title: "Different", Link: "https://example.com/x", PublishedAt: "2026-05-01T00:00:00Z"}
	if a.ItemKey() == c.ItemKey() {
		t.Error("different items must hash differently")
	}
	if !strings.HasPrefix(a.ItemKey(), "h:") {
		t.Errorf("hash key prefix missing: %q", a.ItemKey())
	}
	// GUID beats the hash.
	d := FeedItem{GUID: "explicit-guid", Title: "Same", Link: "https://example.com/x"}
	if d.ItemKey() != "explicit-guid" {
		t.Errorf("guid key = %q", d.ItemKey())
	}
}

func TestParseFeedDateVariants(t *testing.T) {
	cases := map[string]string{
		"Mon, 02 Jan 2006 15:04:05 GMT":   "2006-01-02T15:04:05Z",
		"Mon, 02 Jan 2006 15:04:05 -0500": "2006-01-02T20:04:05Z",
		"2006-01-02T15:04:05Z":            "2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.123Z":        "2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05+02:00":       "2006-01-02T13:04:05Z",
		"2006-01-02":                      "2006-01-02T00:00:00Z",
		"not a date at all":               "",
		"":                                "",
	}
	for in, want := range cases {
		if got := parseFeedDate(in); got != want {
			t.Errorf("parseFeedDate(%q) = %q, want %q", in, got, want)
		}
	}
}
