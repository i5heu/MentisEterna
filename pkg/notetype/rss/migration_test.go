package rss

import (
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// TestInitSchemaUpgradesOldItemsTable builds the pre-evaluation items table
// (the layout shipped before the significance columns), inserts a row, and
// verifies InitSchema upgrades it in place while preserving data.
func TestInitSchemaUpgradesOldItemsTable(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	if _, err := d.Exec(`DROP TABLE ct_rss_items`); err != nil {
		t.Fatalf("drop items: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TABLE ct_rss_items (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id         INTEGER NOT NULL,
			item_key        TEXT    NOT NULL,
			title           TEXT    NOT NULL,
			url             TEXT    NOT NULL DEFAULT '',
			author          TEXT    NOT NULL DEFAULT '',
			published_at    TEXT    NOT NULL DEFAULT '',
			fetched_at      TEXT    NOT NULL,
			prompt_note_id  INTEGER NOT NULL DEFAULT 0,
			feed_content    TEXT    NOT NULL DEFAULT '',
			full_content    TEXT    NOT NULL DEFAULT '',
			full_status    TEXT    NOT NULL DEFAULT 'none',
			category        TEXT    NOT NULL DEFAULT '',
			summary         TEXT    NOT NULL DEFAULT '',
			tags_json       TEXT    NOT NULL DEFAULT '[]',
			quality         INTEGER NOT NULL DEFAULT 0,
			classify_status TEXT    NOT NULL DEFAULT 'pending',
			classify_error  TEXT    NOT NULL DEFAULT '',
			penalty_score   REAL    NOT NULL DEFAULT 0,
			penalty_json    TEXT    NOT NULL DEFAULT '[]',
			banned          INTEGER NOT NULL DEFAULT 0,
			state           TEXT    NOT NULL DEFAULT 'new',
			saved_note_id   INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(feed_id) REFERENCES ct_rss_feeds(id) ON DELETE CASCADE,
			UNIQUE(feed_id, item_key)
		)`); err != nil {
		t.Fatalf("create v1 items table: %v", err)
	}
	var feedID int64
	if _, err := d.Exec(
		`INSERT INTO ct_rss_feeds (note_id, url, name) VALUES (?, 'https://a.example/rss', 'A')`, noteID,
	); err != nil {
		t.Fatalf("insert feed: %v", err)
	}
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, category, classify_status)
		 VALUES (?, 'legacy', 'Old article', '2026-01-01T00:00:00Z', 'Tech', 'done')`, feedID,
	); err != nil {
		t.Fatalf("insert legacy item: %v", err)
	}

	// The upgrade path: InitSchema must add the evaluation columns.
	if err := p.InitSchema(d.DB); err != nil {
		t.Fatalf("InitSchema upgrade: %v", err)
	}

	cols := map[string]bool{}
	rows, err := d.Query(`PRAGMA table_info(ct_rss_items)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltVal any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, want := range []string{"significance", "negativity", "personal_relevance",
		"affected_now_direct", "affected_now_indirect", "affected_future_direct",
		"affected_future_indirect", "reflection", "points", "evaluated",
		"summary_json", "summary_status", "summary_error"} {
		if !cols[want] {
			t.Errorf("missing column after upgrade: %s", want)
		}
	}

	// Legacy row survives with unevaluated defaults → exempt from min_points.
	// The summarizer columns default to '' (status unknown-to-the-feature):
	// legacy rows are never auto-summarized — only the explicit
	// "Summarize missing" action queues them.
	var sig, points float64
	var evaluated int
	var sumStatus, sumJSON string
	var title string
	if err := d.QueryRow(
		`SELECT title, significance, points, evaluated, summary_status, summary_json FROM ct_rss_items WHERE item_key = 'legacy'`,
	).Scan(&title, &sig, &points, &evaluated, &sumStatus, &sumJSON); err != nil {
		t.Fatalf("load legacy item: %v", err)
	}
	if title != "Old article" || sig != 0 || points != 0 || evaluated != 0 {
		t.Errorf("legacy item corrupted: %q %v %v %d", title, sig, points, evaluated)
	}
	if sumStatus != "" || sumJSON != "" {
		t.Errorf("legacy summary state = (%q, %q), want ('', '') — no surprise draining", sumStatus, sumJSON)
	}

	// The upgraded table keeps working: a full classify store round-trip
	// and a summary store round-trip.
	if err := storeClassification(d.DB, 1, 0, Classification{
		EvaluationReflectionParagraph: "Upgraded row.",
		Significance:                  0.5,
		Tags:                          []string{"x"},
	}, defaultScoreWeights(), 0); err != nil {
		t.Errorf("store after upgrade: %v", err)
	}
	if err := storeSummary(d.DB, 1, StructuredSummary{WhatChanged: []string{"F: works"}}); err != nil {
		t.Errorf("store summary after upgrade: %v", err)
	}
}
