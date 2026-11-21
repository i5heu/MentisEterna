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
