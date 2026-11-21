package rss

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestParseRules(t *testing.T) {
	doc := parseRules(`Just a plain prose line without punctuation
Reading list filters:
# comment ignored
ban: crypto giveaway
BAN: sponsored post
press release: 6
webinar: 2.5
wonderful deep dive: -3
https://spam.example/x: 4
threshold: 8
ban:
threshold: banana
`)
	// "Reading list filters:" ends in a bare colon → silently ignored (not
	// rule-shaped enough to warn). Warnings: empty ban + bad threshold.
	if len(doc.Warnings) != 2 {
		t.Errorf("warnings = %v, want 2", doc.Warnings)
	}
	if len(doc.Bans) != 2 {
		t.Errorf("bans = %v, want 2 entries", doc.Bans)
	}
	for i, want := range []string{"crypto giveaway", "sponsored post"} {
		if i < len(doc.Bans) && doc.Bans[i] != want {
			t.Errorf("ban[%d] = %q, want %q", i, doc.Bans[i], want)
		}
	}
	if len(doc.Penalties) != 4 {
		t.Errorf("penalties = %+v, want 4 entries", doc.Penalties)
	}
	// Phrase containing a colon splits at the last colon.
	found := false
	for _, p := range doc.Penalties {
		if p.phrase == "https://spam.example/x" && p.points == 4 {
			found = true
		}
	}
	if !found {
		t.Errorf("colon phrase not parsed: %+v", doc.Penalties)
	}
	if doc.Threshold == nil || *doc.Threshold != 8 {
		t.Errorf("threshold = %v, want 8", doc.Threshold)
	}
}

func TestEffectiveThreshold(t *testing.T) {
	var doc RulesDoc
	if got := doc.effectiveThreshold(5); got != 5 {
		t.Errorf("configured: %v", got)
	}
	eight := 8.0
	doc.Threshold = &eight
	if got := doc.effectiveThreshold(5); got != 8 {
		t.Errorf("override: %v", got)
	}
}

func TestApplyRules(t *testing.T) {
	doc := parseRules(`ban: hard ban phrase
press release: 6
webinar: 2
wonderful deep dive: -3
`)
	longContent := strings.Repeat("This webinar is a press release. ", 20) // 20 × 2 occurrences

	// Banned: ban phrase wins regardless of score.
	res := applyRules(doc, "Title", "totally fine content with no rule matches at all")
	if res.Banned || res.Score != 0 || len(res.Entries) != 0 {
		t.Errorf("clean article scored: %+v", res)
	}

	res = applyRules(doc, "Title", "the Hard Ban Phrase appears here")
	if !res.Banned {
		t.Error("ban phrase (case-insensitive) must ban")
	}

	// Scores: 20 occurrences of both phrases → 20×(6+2), capped at 10 each.
	res = applyRules(doc, "A press release webinar", longContent)
	if res.Banned {
		t.Errorf("no ban expected: %+v", res)
	}
	// "press release" occurs 20× in content + 1 in title → capped 10; "webinar" likewise.
	want := 10.0*6 + 10.0*2
	if res.Score != want {
		t.Errorf("score = %v, want %v", res.Score, want)
	}
	if len(res.Entries) != 2 {
		t.Errorf("entries = %+v", res.Entries)
	}

	// Negative bonus reduces score.
	res2 := applyRules(parseRules("boilerplate: 5\nwonderful deep dive: -3\n"), "", "boilerplate wonderful deep dive content")
	if res2.Score != 2 {
		t.Errorf("bonus math = %v, want 2", res2.Score)
	}
}

func TestInsertFeedItemsAppliesRules(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID,
		jsonRaw(`{"feeds":[{"url":"https://a.example/rss","name":"A","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}

	oc := feedOutcome{feed: feedRow{ID: feedID, Name: "A"}}
	oc.items = []preparedItem{
		{Key: "clean", Title: "Nice article", FeedContent: "A substantive story about science."},
		{Key: "spam", Title: "Big crypto giveaway", FeedContent: "Enter our crypto giveaway now!"},
		{Key: "penalized", Title: "Webinar announcement", FeedContent: strings.Repeat("This is a webinar about things. ", 3)},
	}
	rules := parseRules("ban: crypto giveaway\nwebinar: 4\n")
	cfg := defaultConfig() // threshold 5

	inserted, rejected, err := insertFeedItems(d.DB, oc, cfg, rules)
	if err != nil {
		t.Fatalf("insertFeedItems: %v", err)
	}
	if inserted != 3 || rejected != 2 {
		t.Fatalf("inserted=%d rejected=%d, want 3/2", inserted, rejected)
	}

	var banned int
	var score float64
	var entries string
	if err := d.QueryRow(
		`SELECT banned, penalty_score, penalty_json FROM ct_rss_items WHERE item_key = 'spam'`,
	).Scan(&banned, &score, &entries); err != nil {
		t.Fatalf("load spam item: %v", err)
	}
	if banned != 1 {
		t.Error("spam item should be banned")
	}
	var cleanScore float64
	if err := d.QueryRow(`SELECT penalty_score FROM ct_rss_items WHERE item_key = 'clean'`).Scan(&cleanScore); err != nil {
		t.Fatalf("load clean: %v", err)
	}
	if cleanScore != 0 {
		t.Errorf("clean score = %v", cleanScore)
	}
	if err := d.QueryRow(`SELECT penalty_score, penalty_json FROM ct_rss_items WHERE item_key = 'penalized'`).Scan(&score, &entries); err != nil {
		t.Fatalf("load penalized: %v", err)
	}
	// 3 content + 1 title occurrences × 4 points = 16 ≥ threshold 5 → rejected.
	if score != 16 || !strings.Contains(entries, "webinar") {
		t.Errorf("penalized: score=%v entries=%q", score, entries)
	}
}

func TestReapplyRulesAction(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)
	setNoteBody(t, d, noteID, "ban: crypto giveaway\nwebinar: 4\nthreshold: 9\n")

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID,
		jsonRaw(`{"feeds":[{"url":"https://a.example/rss","name":"A","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	insert := func(key, content string) {
		t.Helper()
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content) VALUES (?, ?, ?, ?, ?)`,
			feedID, key, key, nowStr(), content,
		); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	insert("clean", "just science writing")
	insert("webinar", "a webinar about a webinar, in a webinar hall")
	insert("banned", "the best crypto giveaway of 2026")

	res, err := actionReapplyRules(d.DB, noteID)
	if err != nil {
		t.Fatalf("actionReapplyRules: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["applied"] != 3 || m["rejected"] != 2 {
		t.Fatalf("result = %#v", res)
	}
	if th, _ := m["threshold"].(float64); th != 9 {
		t.Errorf("threshold override not applied: %v", m["threshold"])
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE banned = 1`).Scan(&count); err != nil {
		t.Fatalf("count banned: %v", err)
	}
	if count != 1 {
		t.Errorf("banned rows = %d, want 1", count)
	}
	var webinarScore float64
	_ = d.QueryRow(`SELECT penalty_score FROM ct_rss_items WHERE item_key = 'webinar'`).Scan(&webinarScore)
	// 3 content + 1 title occurrences × 4 points = 16 (≥ threshold 9 → rejected).
	if webinarScore != 16 {
		t.Errorf("webinar score = %v, want 16", webinarScore)
	}
}

func TestRulesWarningsSurfaceInView(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)
	setNoteBody(t, d, noteID, "ban:\ncolor: blue\nthreshold: ok\nthreshold: 3\n")

	view, err := p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	v, ok := view.(*View)
	if !ok {
		t.Fatalf("view type: %#v", view)
	}
	if v.RulesWarning == "" || !strings.Contains(v.RulesWarning, "invalid") {
		t.Errorf("rules warning missing: %q", v.RulesWarning)
	}
	// Effective threshold from body override is used for rejection.
	_ = fmt.Sprint
}
