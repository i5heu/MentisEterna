package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// withPrivateHosts flips the SSRF test seam on for the test's duration
// (httptest listeners bind 127.0.0.1).
func withPrivateHosts(t *testing.T) {
	t.Helper()
	prev := allowPrivateHosts
	allowPrivateHosts = true
	t.Cleanup(func() { allowPrivateHosts = prev })
}

// stubClassifier replaces newClassifier with a canned-response stub for the
// test's duration; keeps test runs hermetic (no LLM backend dialing).
func stubClassifier(t *testing.T, raw string) {
	t.Helper()
	prev := newClassifier
	newClassifier = func(db *sql.DB, noteID int64) classifier {
		return stubClassifyClient{raw: raw}
	}
	t.Cleanup(func() { newClassifier = prev })
}

// stubClassifyClient is a deterministic classifier for tests.
type stubClassifyClient struct {
	raw string
}

func (s stubClassifyClient) Classify(system, user string) (string, error) {
	return s.raw, nil
}

func (s stubClassifyClient) BeginUse() func() { return func() {} }

// stubClassifyJSON is a canned evaluation reply matching the output
// contract: significance 0.7 positive, personally relevant, ~2.16 billion
// humans affected, plus a reflection paragraph (stored in the reflection
// column — the evaluation never writes a summary; the summarizer pass owns
// summary_json).
const stubClassifyJSON = `{"evaluationReflectionParagraph":"A significant global development with broad impact.","potentialFutureDirectlyAffectedHumansInMillions":120.5,"potentialFutureIndirectlyAffectedHumansInMillions":2000.0,"alreadyDirectlyAffectedHumansInMillions":3.2,"alreadyIndirectlyAffectedHumansInMillions":40.0,"significance":0.7,"negativity":0.0,"personalRelevance":0.6,"tags":["world","policy"]}`

// feedServer serves a two-item feed (one rich item, one snippet that needs
// full-content fetch) with ETag/304 handling, plus an article page.
func feedServer(t *testing.T) *httptest.Server {
	t.Helper()
	etag := `"feed-v1"`
	mux := http.NewServeMux()
	var ownURL string // captured after httptest.NewServer; handlers read it late

	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		long := strings.Repeat("Rich body paragraph with details. ", 25)
		feedXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel><title>Test Feed Title</title>
<item>
  <title>Rich Article</title>
  <link>%s/article</link>
  <guid>rich-1</guid>
  <pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
  <content:encoded><![CDATA[<p>%s</p>]]></content:encoded>
</item>
<item>
  <title>Snippet Article</title>
  <link>%s/article</link>
  <guid>snippet-2</guid>
  <description>Short snippet.</description>
</item>
</channel></rss>`, ownURL, long, ownURL)
		_, _ = w.Write([]byte(feedXML))
	})
	mux.HandleFunc("/article", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		body := `<html><body><article><h1>Article Headline</h1>` +
			`<p>` + strings.Repeat("Fetched article paragraph text. ", 40) + `</p></article></body></html>`
		_, _ = w.Write([]byte(body))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ownURL = srv.URL
	return srv
}

func TestRefreshNoteEndToEnd(t *testing.T) {
	withPrivateHosts(t)
	stubClassifier(t, stubClassifyJSON)
	stubSummarizer(t, nil, nil) // hermetic: the refresh also drains the summary queue
	srv := feedServer(t)

	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "My Reader", p)
	setNoteBody(t, d, noteID, "") // refreshNote reads the note body for rules

	feedURL := srv.URL + "/feed.xml"
	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(
		`{"feeds":[{"url":"`+feedURL+`","name":"","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	// First refresh: both items are new; the snippet needs full-text fetch.
	summary, err := refreshNote(d.DB, noteID, true)
	if err != nil {
		t.Fatalf("refreshNote: %v (%s)", err, summary)
	}
	if !strings.Contains(summary, "1 ok") {
		t.Errorf("summary = %q", summary)
	}
	if !strings.Contains(summary, "+2 new") || !strings.Contains(summary, "1 full-text") {
		t.Errorf("summary missing counts: %q", summary)
	}
	if !strings.Contains(summary, "2 classified") {
		t.Errorf("summary missing classification: %q", summary)
	}
	// Items were classified with the stub's canned result.
	var cs string
	var sig float64
	var eval int
	if err := d.QueryRow(`SELECT classify_status, significance, evaluated FROM ct_rss_items WHERE item_key = 'rich-1'`).Scan(&cs, &sig, &eval); err != nil {
		t.Fatalf("load classified item: %v", err)
	}
	if cs != "done" || sig != 0.7 || eval != 1 {
		t.Errorf("evaluation not stored: status=%q significance=%v evaluated=%d", cs, sig, eval)
	}
	var points, rel float64
	var itemSummary string
	if err := d.QueryRow(`SELECT points, personal_relevance, summary FROM ct_rss_items WHERE item_key = 'rich-1'`).Scan(&points, &rel, &itemSummary); err != nil {
		t.Fatalf("load evaluated item: %v", err)
	}
	if points < 7.0 || points > 7.6 {
		t.Errorf("points = %v, want ≈7.35", points)
	}
	if rel != 0.6 {
		t.Errorf("personal_relevance = %v, want 0.6", rel)
	}
	// The evaluation must NOT write a summary anymore: the old column stays
	// empty (the telegraph digest is the only summary — see the assert below).
	if itemSummary != "" {
		t.Errorf("old summary leaked from the evaluation pipeline = %q, want empty", itemSummary)
	}
	if !strings.Contains(summary, "2 summarized") {
		t.Errorf("summary missing summarization: %q", summary)
	}
	// The summarize stage ran in the same refresh: both items carry a
	// done structured summary from the stub.
	var sumStatus, sumJSON string
	if err := d.QueryRow(
		`SELECT summary_status, summary_json FROM ct_rss_items WHERE item_key = 'rich-1'`,
	).Scan(&sumStatus, &sumJSON); err != nil {
		t.Fatalf("load summarized item: %v", err)
	}
	if sumStatus != "done" {
		t.Errorf("rich-1 summary_status = %q, want done (refresh must drain)", sumStatus)
	}
	if s := parseSummaryJSON(sumJSON); s == nil || len(s.WhatChanged) == 0 {
		t.Errorf("rich-1 summary_json = %q, want the canned stub digest", sumJSON)
	}

	// Items stored with expected content.
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items`).Scan(&count); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if count != 2 {
		t.Fatalf("items = %d, want 2", count)
	}
	var richFeed, richFull, richStatus string
	if err := d.QueryRow(
		`SELECT feed_content, full_content, full_status FROM ct_rss_items WHERE item_key = 'rich-1'`,
	).Scan(&richFeed, &richFull, &richStatus); err != nil {
		t.Fatalf("load rich item: %v", err)
	}
	if !strings.Contains(richFeed, "Rich body paragraph") {
		t.Errorf("rich feed content: %q", richFeed)
	}
	if richStatus != "none" || richFull != "" {
		t.Errorf("rich item should not need full fetch: status=%q content=%q", richStatus, richFull)
	}
	var snipFeed, snipFull, snipStatus string
	if err := d.QueryRow(
		`SELECT feed_content, full_content, full_status FROM ct_rss_items WHERE item_key = 'snippet-2'`,
	).Scan(&snipFeed, &snipFull, &snipStatus); err != nil {
		t.Fatalf("load snippet item: %v", err)
	}
	if snipStatus != "fetched" || !strings.Contains(snipFull, "Article Headline") {
		t.Errorf("snippet full fetch wrong: status=%q content=%q", snipStatus, snipFull)
	}

	// Feed name auto-filled from <title>.
	var feedName string
	if err := d.QueryRow(`SELECT name FROM ct_rss_feeds`).Scan(&feedName); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	if feedName != "Test Feed Title" {
		t.Errorf("feed name auto-fill = %q", feedName)
	}

	// Feed meta recorded.
	var status string
	var etagStore string
	if err := d.QueryRow(`SELECT last_status, etag FROM ct_rss_feeds`).Scan(&status, &etagStore); err != nil {
		t.Fatalf("load feed status: %v", err)
	}
	if status != "ok" || etagStore != `"feed-v1"` {
		t.Errorf("feed meta: status=%q etag=%q", status, etagStore)
	}

	// Second refresh: ETag match → 304 Not Modified, no duplicate items.
	summary, err = refreshNote(d.DB, noteID, true)
	if err != nil {
		t.Fatalf("second refreshNote: %v (%s)", err, summary)
	}
	if !strings.Contains(summary, "1 unchanged") {
		t.Errorf("second summary = %q", summary)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items`).Scan(&count); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if count != 2 {
		t.Errorf("items after second refresh = %d, want 2 (dedup)", count)
	}

	// Freshness: not forced and within interval → skipped.
	summary, err = refreshNote(d.DB, noteID, false)
	if err != nil {
		t.Fatalf("fresh refreshNote: %v (%s)", err, summary)
	}
	if !strings.Contains(summary, "1 fresh-skipped") {
		t.Errorf("fresh summary = %q", summary)
	}
}

func TestRefreshNoteSSRFGuard(t *testing.T) {
	// allowPrivateHosts stays FALSE here: the guard must refuse the
	// 127.0.0.1 feed URL and record the error on the feed row.
	srv := feedServer(t)

	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Guarded Reader", p)
	setNoteBody(t, d, noteID, "")

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(
		`{"feeds":[{"url":"`+srv.URL+`/feed.xml","name":"","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	summary, err := refreshNote(d.DB, noteID, true)
	if err != nil {
		t.Fatalf("refreshNote: %v", err)
	}
	if !strings.Contains(summary, "1 error") {
		t.Errorf("summary = %q", summary)
	}
	var lastErr, lastStatus string
	if err := d.QueryRow(`SELECT COALESCE(last_error,''), last_status FROM ct_rss_feeds`).Scan(&lastErr, &lastStatus); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	if lastStatus != "error" || !strings.Contains(lastErr, "refuse dial") {
		t.Errorf("feed error not recorded: status=%q err=%q", lastStatus, lastErr)
	}
}

func TestRetentionSweep(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(
		`{"feeds":[{"url":"https://example.com/rss","name":"F","enabled":true}],"retention_days":7,"max_items_per_feed":10}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds`).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}

	old := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(timeFormat)
	insertItem := func(key, fetchedAt, state string, saved int64) {
		t.Helper()
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, state, saved_note_id)
			 VALUES (?, ?, ?, ?, ?, ?)`, feedID, key, key, fetchedAt, state, saved,
		); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	insertItem("expired", old, "read", 0)
	insertItem("expired-star", old, "starred", 0)
	insertItem("expired-saved", old, "read", 42)
	now := nowStr()
	for i := 0; i < 12; i++ {
		insertItem(fmt.Sprintf("trim-%02d", i), now, "new", 0)
	}

	// Use the persisted config (retention 7 days, 10 items/feed).
	cfg, err := loadConfigRow(d.DB, noteID)
	if err != nil {
		t.Fatalf("loadConfigRow: %v", err)
	}
	swept, err := runRetention(d.DB, noteID, cfg)
	if err != nil {
		t.Fatalf("runRetention: %v", err)
	}
	// 1 expired regular + 2 trim-overflow beyond the 10-item cap.
	if swept != 3 {
		t.Errorf("swept = %d, want 3", swept)
	}

	var remaining int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	// 10 newest + starred + saved survive = 12.
	if remaining != 12 {
		t.Errorf("remaining = %d, want 12", remaining)
	}
	var starAlive, savedAlive int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE item_key = 'expired-star'`).Scan(&starAlive); err != nil {
		t.Fatalf("count star: %v", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE item_key = 'expired-saved'`).Scan(&savedAlive); err != nil {
		t.Fatalf("count saved: %v", err)
	}
	if starAlive != 1 || savedAlive != 1 {
		t.Errorf("starred/saved items must survive retention: star=%d saved=%d", starAlive, savedAlive)
	}
}
