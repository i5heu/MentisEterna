package rss

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestRSSPlugin(t *testing.T) {
	plugintest.Run(t, notetype.Registry[PluginID], plugintest.TestData{
		ValidPayload:   `{"feeds":[{"url":"https://example.com/feed.xml","name":"Example","enabled":true}],"interval_minutes":60,"auto_classify":true,"fetch_full_content":true,"max_items_per_feed":100,"retention_days":90,"reject_threshold":5}`,
		InvalidPayload: `{"feeds":[{"url":"ftp://example.com/feed"}]}`,
	})
}

func TestRSSPromptPlugin(t *testing.T) {
	plugintest.Run(t, notetype.Registry[PromptPluginID], plugintest.TestData{})
}

// --- Config validation ---

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "empty default", cfg: defaultConfig()},
		{
			name: "valid feeds",
			cfg: Config{
				Feeds:            []Feed{{URL: "https://a.example/rss", Name: "A", Enabled: true}, {URL: "http://b.example/rss"}},
				IntervalMinutes:  30,
				AutoClassify:     true,
				FetchFullContent: true,
				MaxItemsPerFeed:  50,
				RetentionDays:    60,
				RejectThreshold:  8,
			},
		},
		{
			name:    "non-http scheme",
			cfg:     Config{Feeds: []Feed{{URL: "ftp://example.com/feed"}}},
			wantErr: "http(s)",
		},
		{
			name:    "garbage url",
			cfg:     Config{Feeds: []Feed{{URL: "::not a url::"}}},
			wantErr: "http(s)",
		},
		{
			name:    "duplicate url",
			cfg:     Config{Feeds: []Feed{{URL: "https://example.com/feed"}, {URL: "https://example.com/feed/"}}},
			wantErr: "duplicate",
		},
		{
			name:    "interval too large",
			cfg:     Config{IntervalMinutes: maxIntervalMins + 1},
			wantErr: "interval_minutes",
		},
		{
			name:    "threshold negative",
			cfg:     Config{RejectThreshold: -1},
			wantErr: "reject_threshold",
		},
		{
			name:    "too many feeds",
			cfg:     Config{Feeds: make([]Feed, maxFeedsPerNote+1)},
			wantErr: "too many feeds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
		})
	}
}

// --- Config save/load + feed sync ---

func TestConfigSaveLoadRoundTripAndFeedSync(t *testing.T) {
	p := &Plugin{}
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "My Reader", p)

	save := func(raw string) {
		t.Helper()
		tx, err := d.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(raw)); err != nil {
			tx.Rollback()
			t.Fatalf("SaveConfig: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	// Initial config: feeds A (enabled) and B (disabled).
	save(`{"feeds":[
		{"url":"https://a.example/rss","name":"A","enabled":true},
		{"url":"https://b.example/rss","name":"B","enabled":false}
	],"interval_minutes":45,"auto_classify":true,"fetch_full_content":true,
	"max_items_per_feed":80,"retention_days":30,"reject_threshold":4}`)

	// Simulate fetch metadata + items on both feeds.
	var feedA, feedB int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE url = 'https://a.example/rss'`).Scan(&feedA); err != nil {
		t.Fatalf("load feed A: %v", err)
	}
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE url = 'https://b.example/rss'`).Scan(&feedB); err != nil {
		t.Fatalf("load feed B: %v", err)
	}
	if _, err := d.Exec(
		`UPDATE ct_rss_feeds SET etag = '"etag-a"', last_fetch_at = '2026-05-01T10:00:00Z', last_status = 'ok' WHERE id = ?`, feedA,
	); err != nil {
		t.Fatalf("set meta: %v", err)
	}
	for _, feed := range []int64{feedA, feedB} {
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at, state) VALUES (?, 'k1', 'Item 1', '', '2026-05-01T10:00:00Z', 'new')`,
			feed,
		); err != nil {
			t.Fatalf("insert item on feed %d: %v", feed, err)
		}
	}

	// Second save: A renamed (same URL — metadata must survive), B dropped,
	// C added. A's URL also gets a trailing slash variant to test trimming.
	save(`{"feeds":[
		{"url":"https://a.example/rss/","name":"A prime","enabled":true},
		{"url":"https://c.example/rss","name":"C","enabled":true}
	],"auto_classify":false,"reject_threshold":7}`)

	// LoadConfig round-trips and validates.
	loadedRaw, err := p.LoadConfig(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(loadedRaw, &cfg); err != nil {
		t.Fatalf("unmarshal loaded: %v", err)
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("loaded config does not validate: %v", err)
	}
	if len(cfg.Feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d: %+v", len(cfg.Feeds), cfg.Feeds)
	}
	if cfg.Feeds[0].Name != "A prime" || cfg.Feeds[0].URL != "https://a.example/rss" || !cfg.Feeds[0].Enabled {
		t.Errorf("feed A mismatch: %+v", cfg.Feeds[0])
	}
	if cfg.AutoClassify {
		t.Error("auto_classify should be false after second save (got true)")
	}
	if cfg.RejectThreshold != 7 || cfg.IntervalMinutes != 60 {
		t.Errorf("settings mismatch: threshold=%g interval=%d", cfg.RejectThreshold, cfg.IntervalMinutes)
	}

	// Feed A: same row (metadata + items survive a rename / URL trim).
	var etag, status string
	var itemsA int
	if err := d.QueryRow(`SELECT etag, last_status FROM ct_rss_feeds WHERE id = ?`, feedA).Scan(&etag, &status); err != nil {
		t.Fatalf("load feed A after: %v", err)
	}
	if etag != `"etag-a"` || status != "ok" {
		t.Errorf("feed A metadata lost: etag=%q status=%q", etag, status)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE feed_id = ?`, feedA).Scan(&itemsA); err != nil {
		t.Fatalf("count items A: %v", err)
	}
	if itemsA != 1 {
		t.Errorf("feed A items should survive config save, got %d", itemsA)
	}

	// Feed B: dropped — row and items cascade away.
	var feedBCount, itemsB int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_feeds WHERE id = ?`, feedB).Scan(&feedBCount); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE feed_id = ?`, feedB).Scan(&itemsB); err != nil {
		t.Fatalf("count items B: %v", err)
	}
	if feedBCount != 0 || itemsB != 0 {
		t.Errorf("dropped feed B should be gone with its items: rows=%d items=%d", feedBCount, itemsB)
	}

	// LoadConfig on a note without config: defaults.
	other := plugintest.CreateNote(t, d, "Other", p)
	raw, err := p.LoadConfig(context.Background(), d.DB, 0, other)
	if err != nil {
		t.Fatalf("LoadConfig defaults: %v", err)
	}
	var def Config
	if err := json.Unmarshal(raw, &def); err != nil {
		t.Fatalf("unmarshal defaults: %v", err)
	}
	if def.IntervalMinutes != defaultInterval || !def.AutoClassify || def.RejectThreshold != defaultThreshold {
		t.Errorf("defaults mismatch: %+v", def)
	}
}

// --- Cascading delete ---

func TestNoteDeleteCascadesFeedsAndItems(t *testing.T) {
	p := &Plugin{}
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(`{"feeds":[{"url":"https://x.example/rss","name":"X","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at) VALUES (?, 'k', 'T', '', '2026-05-01T10:00:00Z')`,
		feedID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	if _, err := d.Exec(`DELETE FROM notes WHERE id = ?`, noteID); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	for _, table := range []string{"ct_rss_config", "ct_rss_feeds", "ct_rss_items"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s should be empty after note delete, got %d rows", table, n)
		}
	}
}

// --- Actions ---

func TestSetItemStateAction(t *testing.T) {
	p := &Plugin{}
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(`{"feeds":[{"url":"https://a.example/rss","name":"A","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	res, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at, state) VALUES (?, 'k', 'T', '', '2026-05-01T10:00:00Z', 'new')`,
		feedID,
	)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID, _ := res.LastInsertId()

	// Items of another note must not be reachable.
	other := plugintest.CreateNote(t, d, "Other", p)
	if _, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at, state) VALUES (?, 'k2', 'T2', '', '2026-05-01T10:00:00Z', 'new')`,
		feedID,
	); err != nil {
		t.Fatalf("insert item 2: %v", err)
	}

	if _, err := actionSetItemState(d.DB, noteID, json.RawMessage(`{"item_id":`+itoa(itemID)+`,"state":"starred"}`)); err != nil {
		t.Fatalf("set state: %v", err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM ct_rss_items WHERE id = ?`, itemID).Scan(&state); err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state != "starred" {
		t.Errorf("state = %q, want starred", state)
	}

	// Cross-note access rejected.
	if _, err := actionSetItemState(d.DB, other, json.RawMessage(`{"item_id":`+itoa(itemID)+`,"state":"read"}`)); err == nil {
		t.Error("cross-note set state should fail")
	}
	// Invalid state rejected.
	if _, err := actionSetItemState(d.DB, noteID, json.RawMessage(`{"item_id":999999,"state":"archived"}`)); err == nil {
		t.Error("invalid state should fail")
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
