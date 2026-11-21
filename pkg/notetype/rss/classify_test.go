package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

// seqClassifyClient returns canned replies in order (retry-aware).
type seqClassifyClient struct {
	replies []string
	err     error
	n       int
}

func (s *seqClassifyClient) Classify(system, user string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.n >= len(s.replies) {
		return "", errors.New("stub exhausted")
	}
	out := s.replies[s.n]
	s.n++
	return out, nil
}

func (s *seqClassifyClient) BeginUse() func() { return func() {} }

func stubSeqClassifier(t *testing.T, replies []string, err error) {
	t.Helper()
	prev := newClassifier
	newClassifier = func(db *sql.DB, noteID int64) classifier {
		return &seqClassifyClient{replies: replies, err: err}
	}
	t.Cleanup(func() { newClassifier = prev })
}

func TestParseClassification(t *testing.T) {
	// Plain JSON.
	out, err := parseClassification(`{"category":"Tech","summary":"It is a thing.","quality":8,"tags":["go","rss"]}`)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if out.Category != "Tech" || out.Quality != 8 || len(out.Tags) != 2 {
		t.Errorf("plain: %+v", out)
	}

	// Fenced.
	out, err = parseClassification("```json\n{\"category\":\"Fenced\",\"summary\":\"S\",\"quality\":3,\"tags\":[]}\n```")
	if err != nil || out.Category != "Fenced" {
		t.Errorf("fenced: %v %+v", err, out)
	}

	// Prose around the object.
	out, err = parseClassification("Sure! Here you go: {\"category\":\"P\",\"summary\":\"S\",\"quality\":1,\"tags\":[]} hope that helps")
	if err != nil || out.Category != "P" {
		t.Errorf("prose: %v %+v", err, out)
	}

	// Clipping and clamping.
	long := strings.Repeat("x", 300)
	out, err = parseClassification(fmt.Sprintf(`{"category":"%s","summary":"%s","quality":15,"tags":[%s]}`,
		long, long, `"t1","t2","t3","t4","t5","t6","t7","t8","t9","t10","t11","t12"`))
	if err != nil {
		t.Fatalf("clip: %v", err)
	}
	if got := len([]rune(out.Category)); got > 100 {
		t.Errorf("category clip: %d runes", got)
	}
	if out.Quality != 10 {
		t.Errorf("quality clamp: %d", out.Quality)
	}
	if len(out.Tags) != 10 {
		t.Errorf("tags clip: %d", len(out.Tags))
	}
	// Negative quality clamps to zero.
	out, _ = parseClassification(`{"quality":-5}`)
	if out.Quality != 0 {
		t.Errorf("negative quality: %d", out.Quality)
	}

	// Nothing parseable.
	if _, err := parseClassification("I cannot answer that"); err == nil {
		t.Error("prose-only should fail")
	}
}

func TestClassifyOnceRetryThenError(t *testing.T) {
	good := `{"category":"G","summary":"s","quality":5,"tags":[]}`
	bad := "not json at all"

	// Bad then good → retry succeeds.
	stubSeqClassifier(t, []string{bad, good}, nil)
	out, _, err := classifyOnce(&seqClassifyClient{replies: []string{bad, good}}, "sys", "user")
	if err != nil || out.Category != "G" {
		t.Fatalf("retry: err=%v out=%+v", err, out)
	}

	// Bad twice → error.
	stubSeqClassifier(t, nil, nil)
	_, _, err = classifyOnce(&seqClassifyClient{replies: []string{bad, bad}}, "sys", "user")
	if err == nil {
		t.Fatal("double-bad should error")
	}

	// Transport error → error.
	_, _, err = classifyOnce(&seqClassifyClient{err: errors.New("llm down")}, "sys", "user")
	if err == nil || !strings.Contains(err.Error(), "llm down") {
		t.Fatalf("transport error: %v", err)
	}
}

// classifyFixture builds a reader + feed + two pending items.
func classifyFixture(t *testing.T) (*Plugin, *db.DB, int64, []int64) {
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
	var ids []int64
	for i, key := range []string{"i-a", "i-b"} {
		res, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at, feed_content)
			 VALUES (?, ?, ?, '', ?, ?)`, feedID, key, fmt.Sprintf("Item %d", i+1), nowStr(), fmt.Sprintf("Content number %d.", i+1),
		)
		if err != nil {
			t.Fatalf("insert item: %v", err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	return p, d, noteID, ids
}

func TestClassifyPendingItemsStoresResults(t *testing.T) {
	stubClassifier(t, stubClassifyJSON)
	p, d, noteID, ids := classifyFixture(t)

	done := classifyPendingItems(d.DB, noteID, Config{AutoClassify: true})
	if done != 2 {
		t.Fatalf("classified = %d, want 2", done)
	}
	for _, id := range ids {
		var status, category string
		var quality int
		var tags string
		if err := d.QueryRow(
			`SELECT classify_status, category, quality, tags_json FROM ct_rss_items WHERE id = ?`, id,
		).Scan(&status, &category, &quality, &tags); err != nil {
			t.Fatalf("load item %d: %v", id, err)
		}
		if status != "done" || category != "Technology" || quality != 7 || tags != `["one","two"]` {
			t.Errorf("item %d: status=%q category=%q quality=%d tags=%q", id, status, category, quality, tags)
		}
	}

	// Second run: nothing pending.
	if done := classifyPendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 0 {
		t.Errorf("second run classified %d, want 0", done)
	}

	// auto_classify disabled → no-op.
	_ = p
	if _, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content) VALUES ((SELECT id FROM ct_rss_feeds WHERE note_id = ?), 'i-c', 'Third', ?, 'C.')`,
		noteID, nowStr(),
	); err != nil {
		t.Fatalf("insert third: %v", err)
	}
	if done := classifyPendingItems(d.DB, noteID, Config{AutoClassify: false}); done != 0 {
		t.Errorf("disabled classify still ran: %d", done)
	}
	var status string
	_ = d.QueryRow(`SELECT classify_status FROM ct_rss_items WHERE item_key = 'i-c'`).Scan(&status)
	if status != "pending" {
		t.Errorf("item i-c tampered: %q", status)
	}
}

func TestClassifyPendingItemsRecordsErrors(t *testing.T) {
	stubSeqClassifier(t, nil, errors.New("llm unreachable"))
	_, d, noteID, _ := classifyFixture(t)

	done := classifyPendingItems(d.DB, noteID, Config{AutoClassify: true})
	if done != 2 {
		t.Fatalf("errored items count = %d, want 2", done)
	}
	var status, msg string
	if err := d.QueryRow(
		`SELECT classify_status, classify_error FROM ct_rss_items WHERE item_key = 'i-a'`,
	).Scan(&status, &msg); err != nil {
		t.Fatalf("load: %v", err)
	}
	if status != "error" || !strings.Contains(msg, "llm unreachable") {
		t.Errorf("error not recorded: status=%q msg=%q", status, msg)
	}
}

func TestClassifyPendingItemsUsesScopedPrompt(t *testing.T) {
	stubClassifier(t, stubClassifyJSON)
	_, d, noteID, ids := classifyFixture(t)

	prompt := makeNote(t, d, "My prompt", PromptPluginID, 0)
	setNoteBody(t, d, prompt, "Prompt text.")

	// Sibling prompt: reader is parentless → parent's children = none; use
	// subtree binding instead by placing it under the reader.
	childPrompt := makeNote(t, d, "Child prompt", PromptPluginID, noteID)
	_ = prompt

	_ = classifyPendingItems(d.DB, noteID, Config{AutoClassify: true})
	var stored int64
	if err := d.QueryRow(`SELECT prompt_note_id FROM ct_rss_items WHERE id = ?`, ids[0]).Scan(&stored); err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored != childPrompt {
		t.Errorf("prompt_note_id = %d, want %d", stored, childPrompt)
	}
}

func TestReclassifyAction(t *testing.T) {
	stubClassifier(t, stubClassifyJSON)
	p, d, noteID, ids := classifyFixture(t)

	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "reclassify",
		jsonRaw(fmt.Sprintf(`{"item_id":%d}`, ids[0])))
	if err != nil {
		t.Fatalf("HandleAction reclassify: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["category"] != "Technology" {
		t.Fatalf("reclassify result: %#v", res)
	}
	var status string
	if err := d.QueryRow(`SELECT classify_status FROM ct_rss_items WHERE id = ?`, ids[0]).Scan(&status); err != nil {
		t.Fatalf("load: %v", err)
	}
	if status != "done" {
		t.Errorf("classify_status = %q", status)
	}

	// Failing classifier → action reports the error; item marked errored.
	stubSeqClassifier(t, nil, errors.New("classifier offline"))
	if _, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "reclassify",
		jsonRaw(fmt.Sprintf(`{"item_id":%d}`, ids[1]))); err == nil {
		t.Fatal("expected reclassify error")
	}
	var st2, msg string
	if err := d.QueryRow(`SELECT classify_status, classify_error FROM ct_rss_items WHERE id = ?`, ids[1]).Scan(&st2, &msg); err != nil {
		t.Fatalf("load second: %v", err)
	}
	if st2 != "error" || !strings.Contains(msg, "classifier offline") {
		t.Errorf("failure not recorded: %q %q", st2, msg)
	}
}

func TestClassifyUserMessageCapsContent(t *testing.T) {
	long := strings.Repeat("x", classifyMaxRunes+5000)
	msg := classifyUserMessage("Title", "Feed", "https://x", long)
	if got := len([]rune(msg)); got > classifyMaxRunes+2000 {
		t.Errorf("user message not capped: %d runes", got)
	}
}

// TestClassifierResolvesThroughInferencerNote verifies the production
// newClassifier seam: a nearby llm_inferencer note binding rss_classify must
// yield that note's model/endpoint/key, and without one the config.toml
// feature client applies. No network is dialed — client construction only.
func TestClassifierResolvesThroughInferencerNote(t *testing.T) {
	p := notetype.Registry[PluginID].(*Plugin)
	d := plugintest.DB(t, p)
	if err := notetype.Registry["llm_inferencer"].InitSchema(d.DB); err != nil {
		t.Fatalf("llm_inferencer schema: %v", err)
	}

	// Fallback first: no inferencer notes anywhere.
	cls := newClassifier(d.DB, 0)
	ll, ok := cls.(llmClassifier)
	if !ok {
		t.Fatalf("production classifier has unexpected type %T", cls)
	}
	if ll.client.Model != llm.FeatureModel("rss_classify") {
		t.Fatalf("fallback model = %q, want config feature model", ll.client.Model)
	}

	// A sibling inferencer note binds rss_classify.
	folder := createTestNote(t, d, "News", "standard", 0)
	reader := createTestNote(t, d, "Reader", PluginID, folder)
	createTestNote(t, d, "Other reader", PluginID, folder)
	sibling, err := d.Exec(`INSERT INTO notes (title, type, parent_id) VALUES ('Inferencer', 'llm_inferencer', ?)`, folder)
	if err != nil {
		t.Fatalf("insert inferencer note: %v", err)
	}
	siblingID, _ := sibling.LastInsertId()
	saveInferencerCfg(t, d, siblingID, `{
		"provider": "compatible",
		"base_url": "http://127.0.0.1:9",
		"api_key": "sk-rss",
		"default_tier": "small",
		"tiers": {"small": {"model": "prov/small"}},
		"usages": {"rss_classify": {"tier": "small"}}
	}`)

	cls = newClassifier(d.DB, reader)
	ll, ok = cls.(llmClassifier)
	if !ok {
		t.Fatalf("classifier type %T", cls)
	}
	if ll.client.Model != "prov/small" {
		t.Errorf("resolved model = %q, want prov/small", ll.client.Model)
	}
	if ll.client.BaseURL != "http://127.0.0.1:9" {
		t.Errorf("resolved base URL = %q", ll.client.BaseURL)
	}
	if ll.client.APIKey != "sk-rss" {
		t.Errorf("stored API key not applied")
	}
}

// createTestNote inserts a note in the rss tests (NULL parent for root).
func createTestNote(t *testing.T, d *db.DB, title, noteType string, parent int64) int64 {
	t.Helper()
	var res sql.Result
	var err error
	if parent == 0 {
		res, err = d.Exec(`INSERT INTO notes (title, type, parent_id) VALUES (?, ?, NULL)`, title, noteType)
	} else {
		res, err = d.Exec(`INSERT INTO notes (title, type, parent_id) VALUES (?, ?, ?)`, title, noteType, parent)
	}
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// saveInferencerCfg saves an llm_inferencer note's config inside a tx.
func saveInferencerCfg(t *testing.T, d *db.DB, noteID int64, payload string) {
	t.Helper()
	saver := notetype.Registry["llm_inferencer"].(notetype.ConfigSaver)
	tx, err := d.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := saver.SaveConfig(context.Background(), tx, 0, noteID, jsonRaw(payload)); err != nil {
		tx.Rollback()
		t.Fatalf("save inferencer config: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
