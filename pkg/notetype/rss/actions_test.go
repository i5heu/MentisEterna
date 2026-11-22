package rss

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// enqueueRecorder captures job enqueues made by actions.
type enqueueRecorder struct {
	calls []struct {
		pluginID string
		jobName  string
		payload  []byte
	}
	returnID int64
}

func (r *enqueueRecorder) enqueue(pluginID, jobName string, payload []byte) (int64, error) {
	r.calls = append(r.calls, struct {
		pluginID string
		jobName  string
		payload  []byte
	}{pluginID, jobName, payload})
	r.returnID++
	return r.returnID, nil
}

func installEnqueue(t *testing.T, p *Plugin) *enqueueRecorder {
	t.Helper()
	rec := &enqueueRecorder{}
	p.SetJobEnqueueFunc(rec.enqueue)
	t.Cleanup(func() { p.SetJobEnqueueFunc(nil) })
	return rec
}

// readerFixture creates a reader with one feed and one item; returns p, db, note, feedID, itemID.
func readerFixture(t *testing.T, itemContent string) (*Plugin, *db.DB, int64, int64, int64) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	noteID := plugintest.CreateNote(t, d, "Reader", p)

	tx, _ := d.Begin()
	if err := p.SaveConfig(context.Background(), tx, 0, noteID,
		json.RawMessage(`{"feeds":[{"url":"https://a.example/rss","name":"Feed A","enabled":true}]}`)); err != nil {
		tx.Rollback()
		t.Fatalf("SaveConfig: %v", err)
	}
	tx.Commit()

	var feedID int64
	if err := d.QueryRow(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID).Scan(&feedID); err != nil {
		t.Fatalf("load feed: %v", err)
	}
	res, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, url, author, fetched_at, feed_content, category, summary, quality, tags_json, classify_status)
		 VALUES (?, 'k1', 'The Article Title', 'https://example.com/article', 'An Author', ?, ?, 'Tech', 'A fine summary.', 8, '["a","b"]', 'done')`,
		feedID, nowStr(), itemContent,
	)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID, _ := res.LastInsertId()
	return p, d, noteID, feedID, itemID
}

func TestSaveAsNoteAction(t *testing.T) {
	p, d, noteID, _, itemID := readerFixture(t, "The full article body text.")
	rec := installEnqueue(t, p)

	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "save_as_note",
		json.RawMessage(`{"item_id":`+itoa(itemID)+`}`))
	if err != nil {
		t.Fatalf("save_as_note: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result type: %#v", res)
	}
	newNoteID, _ := m["note_id"].(int64)
	if newNoteID <= 0 {
		t.Fatalf("note_id missing: %#v", m)
	}

	// Note created under the reader with the article as body.
	var title, noteType string
	var parent int64
	if err := d.QueryRow(
		`SELECT title, type, parent_id FROM notes WHERE id = ?`, newNoteID,
	).Scan(&title, &noteType, &parent); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if title != "The Article Title" || noteType != "standard" || parent != noteID {
		t.Errorf("note wrong: title=%q type=%q parent=%d", title, noteType, parent)
	}
	var body string
	if err := d.QueryRow(
		`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, newNoteID,
	).Scan(&body); err != nil {
		t.Fatalf("load body: %v", err)
	}
	for _, want := range []string{"The Article Title", "Feed A", "A fine summary.", "The full article body text.", "Open original"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}

	// Item updated: saved_note_id, state read.
	var savedNote int64
	var state string
	if err := d.QueryRow(
		`SELECT saved_note_id, state FROM ct_rss_items WHERE id = ?`, itemID,
	).Scan(&savedNote, &state); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if savedNote != newNoteID || state != "read" {
		t.Errorf("item not marked: saved=%d state=%q", savedNote, state)
	}

	// Embedding job enqueued for the new note.
	var sawVSS bool
	for _, c := range rec.calls {
		if c.pluginID == "_system" && c.jobName == "vss_index" {
			var pl struct {
				NoteID int64 `json:"note_id"`
			}
			_ = json.Unmarshal(c.payload, &pl)
			if pl.NoteID == newNoteID {
				sawVSS = true
			}
		}
	}
	if !sawVSS {
		t.Errorf("vss_index not enqueued for note %d: %+v", newNoteID, rec.calls)
	}

	// Idempotent: second call points at the same note without creating more.
	res, err = p.HandleAction(context.Background(), d.DB, 0, noteID, "save_as_note",
		json.RawMessage(`{"item_id":`+itoa(itemID)+`}`))
	if err != nil {
		t.Fatalf("second save_as_note: %v", err)
	}
	m2, _ := res.(map[string]any)
	secondID, _ := m2["note_id"].(int64)
	if secondID != newNoteID {
		t.Errorf("second save created %d, want same %d", secondID, newNoteID)
	}
	var noteCount int
	_ = d.QueryRow(`SELECT COUNT(*) FROM notes WHERE parent_id = ?`, noteID).Scan(&noteCount)
	if noteCount != 1 {
		t.Errorf("child notes = %d, want 1", noteCount)
	}
}

func TestRefreshActionEnqueuesJob(t *testing.T) {
	p, d, noteID, _, _ := readerFixture(t, "content")
	rec := installEnqueue(t, p)

	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "refresh", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("refresh action: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result: %#v", res)
	}
	if runID, _ := m["run_id"].(int64); runID <= 0 {
		t.Errorf("run_id = %v", m["run_id"])
	}
	if len(rec.calls) != 1 {
		t.Fatalf("enqueue calls = %d, want 1", len(rec.calls))
	}
	c := rec.calls[0]
	if c.pluginID != PluginID || c.jobName != JobRefreshFeeds {
		t.Errorf("wrong job: %s/%s", c.pluginID, c.jobName)
	}
	var pl struct {
		NoteID int64 `json:"note_id"`
		Force  bool  `json:"force"`
	}
	if err := json.Unmarshal(c.payload, &pl); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if pl.NoteID != noteID || !pl.Force {
		t.Errorf("payload wrong: %+v (want note %d force)", pl, noteID)
	}
}

func mustGetItem(t *testing.T, p *Plugin, d *db.DB, noteID, itemID int64) *ArticleDetail {
	t.Helper()
	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "get_item",
		json.RawMessage(`{"item_id":`+itoa(itemID)+`}`))
	if err != nil {
		t.Fatalf("get_item %d: %v", itemID, err)
	}
	it, ok := res.(*ArticleDetail)
	if !ok {
		t.Fatalf("get_item result type: %#v", res)
	}
	return it
}

func TestGetItemAction(t *testing.T) {
	p, d, noteID, _, itemID := readerFixture(t, "Feed snippet text.")

	// Extracted content present → preferred over the feed snippet.
	if _, err := d.Exec(
		`UPDATE ct_rss_items SET full_content = 'The extracted article body.',
		full_status = 'fetched', published_at = '2026-06-01T00:00:00Z' WHERE id = ?`,
		itemID,
	); err != nil {
		t.Fatalf("update item: %v", err)
	}

	it := mustGetItem(t, p, d, noteID, itemID)
	if it.Content != "The extracted article body." || it.ContentSource != "extracted" {
		t.Errorf("content = %q source = %q, want extracted body", it.Content, it.ContentSource)
	}
	if it.Title != "The Article Title" || it.FeedName != "Feed A" ||
		it.Author != "An Author" || it.URL != "https://example.com/article" {
		t.Errorf("meta wrong: %+v", it)
	}
	if it.Category != "Tech" || it.Summary != "A fine summary." || it.Quality != 8 {
		t.Errorf("classification wrong: %+v", it)
	}
	if len(it.Tags) != 2 || it.Tags[0] != "a" || it.Tags[1] != "b" {
		t.Errorf("tags = %#v", it.Tags)
	}
	if it.ClassifyStatus != "done" || it.State != "new" || it.FullStatus != "fetched" {
		t.Errorf("status wrong: classify=%q state=%q full=%q", it.ClassifyStatus, it.State, it.FullStatus)
	}
	if it.PublishedAt != "2026-06-01T00:00:00Z" || it.FetchedAt == "" {
		t.Errorf("dates wrong: published=%q fetched=%q", it.PublishedAt, it.FetchedAt)
	}
	if it.Banned || it.SavedNoteID != 0 || it.ID != itemID {
		t.Errorf("flags wrong: %+v", it)
	}
	// penalty must round-trip as valid JSON.
	var penalty []map[string]any
	if err := json.Unmarshal(it.Penalty, &penalty); err != nil {
		t.Errorf("penalty not valid JSON: %v", err)
	}

	// No extracted content → falls back to the feed-provided snippet.
	if _, err := d.Exec(`UPDATE ct_rss_items SET full_content = '', full_status = 'failed' WHERE id = ?`, itemID); err != nil {
		t.Fatalf("clear full_content: %v", err)
	}
	if it := mustGetItem(t, p, d, noteID, itemID); it.Content != "Feed snippet text." || it.ContentSource != "feed" {
		t.Errorf("fallback content = %q source = %q", it.Content, it.ContentSource)
	}

	// No content at all → empty content and source: the UI shows the
	// external link instead.
	if _, err := d.Exec(`UPDATE ct_rss_items SET feed_content = '' WHERE id = ?`, itemID); err != nil {
		t.Fatalf("clear feed_content: %v", err)
	}
	if it := mustGetItem(t, p, d, noteID, itemID); it.Content != "" || it.ContentSource != "" {
		t.Errorf("empty content = %q source = %q", it.Content, it.ContentSource)
	}

	// Rejected items stay viewable (shown dimmed, but readable).
	if _, err := d.Exec(`UPDATE ct_rss_items SET banned = 1, state = 'rejected' WHERE id = ?`, itemID); err != nil {
		t.Fatalf("reject item: %v", err)
	}
	if it := mustGetItem(t, p, d, noteID, itemID); !it.Banned || it.State != "rejected" {
		t.Errorf("rejected item flags: %+v", it)
	}
}

func TestGetItemActionIsolationAndErrors(t *testing.T) {
	p, d, noteID, _, _ := readerFixture(t, "content")

	// A second reader with its own feed and item: must not be reachable
	// through the first reader's note.
	note2 := plugintest.CreateNote(t, d, "Reader 2", p)
	res, err := d.Exec(
		`INSERT INTO ct_rss_feeds (note_id, url, name) VALUES (?, 'https://b.example/rss', 'Feed B')`,
		note2,
	)
	if err != nil {
		t.Fatalf("insert feed 2: %v", err)
	}
	feed2, _ := res.LastInsertId()
	res, err = d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at) VALUES (?, 'other', 'Other reader item', ?)`,
		feed2, nowStr(),
	)
	if err != nil {
		t.Fatalf("insert item 2: %v", err)
	}
	item2, _ := res.LastInsertId()

	if _, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "get_item",
		json.RawMessage(`{"item_id":`+itoa(item2)+`}`)); err == nil {
		t.Errorf("get_item reached across readers: item %d via note %d", item2, noteID)
	}
	// ...but the owning reader can read it.
	if it := mustGetItem(t, p, d, note2, item2); it.Title != "Other reader item" || it.FeedName != "Feed B" {
		t.Errorf("owning reader got wrong item: %+v", it)
	}

	// Unknown item id.
	if _, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "get_item",
		json.RawMessage(`{"item_id":999999}`)); err == nil {
		t.Errorf("get_item unknown id: expected error")
	}
	// Invalid / missing item_id.
	for _, bad := range []string{`{}`, `{"item_id":0}`, `not-json`} {
		if _, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "get_item",
			json.RawMessage(bad)); err == nil {
			t.Errorf("get_item params %q: expected error", bad)
		}
	}
}

func TestHandleActionReapplyRulesDispatch(t *testing.T) {
	p, d, noteID, _, itemID := readerFixture(t, "a webinar about webinars")

	// Rules are typed into the reader's body; reapply must flow through the
	// HandleAction dispatch (the manifest declares this route).
	setNoteBody(t, d, noteID, "webinar: 6\nthreshold: 8\n")

	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "reapply_rules",
		json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("reapply_rules dispatch: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["applied"] != 1 {
		t.Fatalf("reapply_rules result: %#v", res)
	}
	if th, _ := m["threshold"].(float64); th != 8 {
		t.Errorf("threshold = %v, want 8", m["threshold"])
	}
	var score float64
	if err := d.QueryRow(`SELECT penalty_score FROM ct_rss_items WHERE id = ?`, itemID).Scan(&score); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if score != 12 { // "webinar" × 2 occurrences × 6 points
		t.Errorf("penalty_score = %v, want 12", score)
	}
}
