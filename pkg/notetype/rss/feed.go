package rss

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

// FeedItem is one article parsed from a feed.
type FeedItem struct {
	Title       string
	Link        string
	GUID        string
	PublishedAt string // normalized UTC ("2006-01-02T15:04:05Z"), "" if unknown
	Content     string // best content (RSS content:encoded > description; Atom content > summary), HTML stripped
	Author      string
}

// ItemKey returns the dedup key: GUID, or a hash of link|title|published.
func (fi FeedItem) ItemKey() string {
	if s := strings.TrimSpace(fi.GUID); s != "" {
		return s
	}
	sum := sha256.Sum256([]byte(fi.Link + "|" + fi.Title + "|" + fi.PublishedAt))
	return "h:" + hex.EncodeToString(sum[:16])
}

// FeedDocument is a parsed feed document.
type FeedDocument struct {
	Title       string
	Description string
	Items       []FeedItem
}

// --- XML shapes ---

type rssRoot struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	Content     string `xml:"encoded"` // content:encoded (namespaced)
	Author      string `xml:"creator"` // dc:creator (namespaced)
}

type atomRoot struct {
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	ID        string     `xml:"id"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// ParseFeed parses an RSS 2.0 or Atom 1.0 document (XML), returning items
// with text content (HTML stripped) and normalized dates.
func ParseFeed(data []byte) (*FeedDocument, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("rss: empty feed document")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if len(data) > feedParseLimit {
		data = data[:feedParseLimit]
	}

	head := leadingTag(data)
	switch {
	case strings.HasPrefix(head, "feed"):
		return parseAtom(data)
	case strings.HasPrefix(head, "rss"), strings.HasPrefix(head, "channel"):
		return parseRSS(data)
	default:
		// No recognizable root — try RSS first as the most common format,
		// and fail if nothing parses.
		f, err := parseRSS(data)
		if err == nil {
			return f, nil
		}
		return nil, fmt.Errorf("rss: unrecognized feed format (root <%s>): %w", head, err)
	}
}

const feedParseLimit = 4 << 20 // 4 MiB of XML is far beyond any real feed

// timeFormat is the canonical UTC timestamp layout used across plugin tables.
const timeFormat = "2006-01-02T15:04:05Z"

// decodeXML unmarshals an XML document, supporting non-UTF-8 encodings via
// the x/net/html/charset label reader (feeds in the wild declare latin-1
// and friends).
func decodeXML(data []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel
	return dec.Decode(v)
}

func parseRSS(data []byte) (*FeedDocument, error) {
	var root rssRoot
	if err := decodeXML(data, &root); err != nil {
		return nil, fmt.Errorf("rss: parse RSS: %w", err)
	}
	feed := &FeedDocument{
		Title:       strings.TrimSpace(root.Channel.Title),
		Description: strings.TrimSpace(root.Channel.Description),
	}
	for _, it := range root.Channel.Items {
		content := it.Content
		if strings.TrimSpace(content) == "" {
			content = it.Description
		}
		feed.Items = append(feed.Items, FeedItem{
			Title:       strings.TrimSpace(it.Title),
			Link:        strings.TrimSpace(it.Link),
			GUID:        it.GUID,
			PublishedAt: parseFeedDate(it.PubDate),
			Content:     htmlToText(content),
			Author:      strings.TrimSpace(it.Author),
		})
	}
	return feed, nil
}

func parseAtom(data []byte) (*FeedDocument, error) {
	var root atomRoot
	if err := decodeXML(data, &root); err != nil {
		return nil, fmt.Errorf("rss: parse Atom: %w", err)
	}
	feed := &FeedDocument{Title: strings.TrimSpace(root.Title)}
	for _, e := range root.Entries {
		link := ""
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				if l.Href != "" {
					link = strings.TrimSpace(l.Href)
					if l.Rel == "alternate" {
						break
					}
				}
			}
		}
		content := e.Content
		if strings.TrimSpace(content) == "" {
			content = e.Summary
		}
		published := parseFeedDate(e.Published)
		if published == "" {
			published = parseFeedDate(e.Updated)
		}
		feed.Items = append(feed.Items, FeedItem{
			Title:       strings.TrimSpace(e.Title),
			Link:        link,
			GUID:        e.ID,
			PublishedAt: published,
			Content:     htmlToText(content),
			Author:      strings.TrimSpace(e.Author.Name),
		})
	}
	return feed, nil
}

// leadingTag returns the name of the first element tag in the document
// (after the XML declaration), lowercase, "" when absent.
func leadingTag(data []byte) string {
	s := string(data)
	if i := strings.Index(s, "?>"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.IndexByte(s, '<'); i < 0 {
		return ""
	} else {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, " \t\r\n>/?"); i >= 0 {
		return strings.ToLower(s[:i])
	}
	return strings.ToLower(s)
}

// --- Feed date parsing ---

var feedDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
	"2006-01-02T15:04:05Z0700",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// parseFeedDate normalizes a feed date to UTC "2006-01-02T15:04:05Z"
// ("" when unparseable or empty).
func parseFeedDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range feedDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format(timeFormat)
		}
	}
	return ""
}

// feedDescription returns a short plain-text snippet of the feed item
// content for UI display.
func feedSnippet(content string, maxRunes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	r := []rune(content)
	if len(r) <= maxRunes {
		return content
	}
	return string(r[:maxRunes]) + "…"
}
