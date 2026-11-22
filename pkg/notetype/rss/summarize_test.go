package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// stubSummaryJSON is a canned summarizer reply in the contract's exact shape:
// F/O/Q telegraph lines under the three question keys.
const stubSummaryJSON = `{"content":{"What has changed in reality?":["F: EU parliament approved the AI Act","Q: \"a landmark piece of legislation\" — Commission"],"Why has reality changed?":["F: Trilogue negotiations concluded in December"],"What are the new implications?":["O: enforcement burden falls on deployers — analyst note"]}}`

// stubSummarizerClient returns a canned reply per call (retry-aware: every
// Classify/Summarize call consumes one reply).
type stubSummarizerClient struct {
	replies []string
	err     error
	calls   *int32
	mu      sync.Mutex
}

func (s *stubSummarizerClient) Summarize(system, user string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls != nil {
		*s.calls++
	}
	if s.err != nil {
		return "", s.err
	}
	if len(s.replies) == 0 {
		s.replies = []string{stubSummaryJSON}
	}
	out := s.replies[0]
	s.replies = s.replies[1:]
	return out, nil
}

func (s *stubSummarizerClient) BeginUse() func() { return func() {} }

// stubSummarizer replaces newSummarizer for the test's duration; hermetic —
// no LLM backend dialing. The returned counter tracks Summarize calls.
func stubSummarizer(t *testing.T, replies []string, err error) *int32 {
	t.Helper()
	prev := newSummarizer
	var calls int32
	newSummarizer = func(db *sql.DB, noteID int64) summarizer {
		return &stubSummarizerClient{replies: replies, err: err, calls: &calls}
	}
	t.Cleanup(func() { newSummarizer = prev })
	return &calls
}

// summarizeFixture builds a reader + feed; items are seeded per-case.
func summarizeFixture(t *testing.T) (*Plugin, *db.DB, int64, int64) {
	t.Helper()
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
	return p, d, noteID, feedID
}

// clampedWhatChanged is the exact expectation of the "clamps" case: one
// 300-rune clip + fill of "short" lines up to the section cap.
func clampedWhatChanged() []string {
	out := []string{string([]rune(strings.Repeat("x", summarizeMaxRunesPerLine))) + "…"}
	for i := 1; i < summarizeMaxLinesPerSection; i++ {
		out = append(out, "short")
	}
	return out
}

// canonicalStubSummary converts the canned reply into the canonical stored
// shape (summary_json holds what_changed/why/implications keys, not the
// prompt's question keys) — the shape storeSummary writes.
func canonicalStubSummary(t *testing.T) string {
	t.Helper()
	s, err := parseSummary(stubSummaryJSON)
	if err != nil {
		t.Fatalf("canonicalStubSummary: %v", err)
	}
	js, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("canonicalStubSummary marshal: %v", err)
	}
	return string(js)
}

// TestParseSummary validates the reply parser: the contract shape, loose
// framing (fences, prose), key normalization, clamps, and rejection of
// reply shapes that carry no usable lines. Table-driven for randomized order.
func TestParseSummary(t *testing.T) {
	cases := map[string]struct {
		raw     string
		want    StructuredSummary
		wantErr bool
	}{ // The contract shape verbatim: three exact question keys.
		"canonical": {
			raw:  `{"content":{"What has changed in reality?":["F: a"],"Why has reality changed?":["F: b"],"What are the new implications?":["F: c"]}}`,
			want: StructuredSummary{WhatChanged: []string{"F: a"}, Why: []string{"F: b"}, Implications: []string{"F: c"}},
		},
		// Failure mode prevented: fenced replies (common model behavior)
		// were silently rejected by strict JSON decoding.
		"fenced": {
			raw:  "```json\n{\"content\":{\"What has changed in reality?\":[\"F: a\"]}}\n```",
			want: StructuredSummary{WhatChanged: []string{"F: a"}, Why: []string{}, Implications: []string{}},
		},
		// Failure mode prevented: preamble text before the object must not
		// break decoding (loose unmarshal extracts the object).
		"prose_preamble": {
			raw:  "Here you go:\n{\"content\":{\"What are the new implications?\":[\"F: x\"]}}",
			want: StructuredSummary{WhatChanged: []string{}, Why: []string{}, Implications: []string{"F: x"}},
		},
		// Failure mode prevented: a reply missing the "content" wrapper must
		// be recovered instead of producing an empty summary.
		"missing_wrapper": {
			raw:  `{"What has changed in reality?":["F: a"],"Why has reality changed?":["F: b"]}`,
			want: StructuredSummary{WhatChanged: []string{"F: a"}, Why: []string{"F: b"}, Implications: []string{}},
		},
		// Failure mode prevented: rephrased/case-shifted keys with lost
		// punctuation must still land in the right section.
		"fuzzy_keys": {
			raw:  `{"content":{"what has changed in reality":["F: a"],"WHY HAS REALITY CHANGED":["F: b"],"What are the new implications":["F: c"]}}`,
			want: StructuredSummary{WhatChanged: []string{"F: a"}, Why: []string{"F: b"}, Implications: []string{"F: c"}},
		},
		// Failure mode prevented: an off-script model key must be dropped
		// without discarding the recognized sections.
		"unknown_keys_dropped": {
			raw:  `{"content":{"Who is involved?":["F: noise"],"Why has reality changed?":["F: b"]}}`,
			want: StructuredSummary{WhatChanged: []string{}, Why: []string{"F: b"}, Implications: []string{}},
		},
		// Failure mode prevented: blank lines must never become digest
		// entries (they render as stray bullets in the UI).
		"empty_lines_filtered": {
			raw:  `{"content":{"What has changed in reality?":["F: a","","  ","F: b"]}}`,
			want: StructuredSummary{WhatChanged: []string{"F: a", "F: b"}, Why: []string{}, Implications: []string{}},
		},
		// Failure mode prevented: oversized lines/counts from a rogue prompt
		// must be clamped before hitting summary_json.
		"clamps": {
			raw: fmt.Sprintf(`{"content":{"What has changed in reality?":[%s]}}`,
				`"`+strings.Repeat("x", summarizeMaxRunesPerLine+50)+`"`+strings.Repeat(`,"short"`, summarizeMaxLinesPerSection+4)),
			want: StructuredSummary{
				WhatChanged:  clampedWhatChanged(),
				Why:          []string{},
				Implications: []string{},
			},
		},
		// Failure mode prevented: a reply with no lines at all is an error,
		// never a silently stored empty summary.
		"empty_object": {
			raw:     `{"content":{}}`,
			wantErr: true,
		},
		// Failure mode prevented: non-JSON garbage must error, not zero.
		"not_json": {
			raw:     `the article is about goats`,
			wantErr: true,
		},
		// Failure mode prevented: a wrapper with non-array values must be a
		// parse error, not coerced into lines.
		"wrong_shape": {
			raw:     `{"content":{"What has changed in reality?":"F: a string not an array"}}`,
			wantErr: true,
		},
	}
	for name, tc := range cases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			got, err := parseSummary(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSummary(%q) = nil error, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSummary(%q): %v", tc.raw, err)
			}
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("parseSummary mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParseSummaryRoundtrip is a property-based check of the summary
// roundtrip invariant: a StructuredSummary encoded as the contract reply
// parses back to itself. Guards the store→load cycle (summary_json written
// by storeSummary is what the views/manifests read back) plus parse
// idempotency (parse(parse⁻¹(...)) stability for any in-clamp summary).
func TestParseSummaryRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lineGen := rapid.StringMatching(`^[FfOoQq]:[A-Za-z0-9 ,.;"'—-]{1,80}\.?$`)
		what := rapid.SliceOf(lineGen).Draw(t, "what")
		why := rapid.SliceOf(lineGen).Draw(t, "why")
		imp := rapid.SliceOf(lineGen).Draw(t, "implications")

		// In-clamp inputs, normalized the way the parser will: the invariant
		// is about STORED data (post-trim) surviving re-serialization.
		s := StructuredSummary{
			WhatChanged:  clipSummaryLines(what),
			Why:          clipSummaryLines(why),
			Implications: clipSummaryLines(imp),
		}
		if s.Empty() {
			// Nothing to assert for the empty shape: the empty summary is
			// legal only as the stored-no-content marker, not as a reply.
			return
		}
		raw, err := json.Marshal(map[string]any{"content": map[string]any{
			"What has changed in reality?":   s.WhatChanged,
			"Why has reality changed?":       s.Why,
			"What are the new implications?": s.Implications,
		}})
		if err != nil {
			t.Fatalf("marshal reply: %v", err)
		}
		got, err := parseSummary(string(raw))
		if err != nil {
			t.Fatalf("parseSummary(reply): %v (summary %+v)", err, s)
		}
		// Roundtrip invariant: no line lost, none invented, none reordered.
		if diff := cmp.Diff(s, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("roundtrip mismatch (-stored +parsed):\n%s", diff)
		}
		// Idempotency invariant: parseSummary is stable under re-encoding of
		// its own output — storeSummary's JSON is reparsable as-is.
		js, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal stored: %v", err)
		}
		if back := parseSummaryJSON(string(js)); back == nil || cmp.Diff(got, *back) != "" {
			t.Fatalf("summary_json roundtrip broken: %+v vs %+v", got, back)
		}
	})
}

// TestBucketSummaryKeyProperty: the question-key matcher must stay stable
// under the cosmetic mutations models produce (case, whitespace, lost
// question mark) while never bucketing off-script keys into a section.
func TestBucketSummaryKeyProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		padding := rapid.StringOf(rapid.SampledFrom([]rune(" \t_?-"))).Draw(t, "pad")
		upper := rapid.Bool().Draw(t, "upper")
		mut := func(s string) string {
			if upper {
				s = strings.ToUpper(s)
			}
			return padding + s + padding
		}
		if b := bucketSummaryKey(mut("What has changed in reality?")); b != "what_changed" {
			t.Fatalf("what-changed mutation bucketed to %q", b)
		}
		if b := bucketSummaryKey(mut("Why has reality changed?")); b != "why" {
			t.Fatalf("why mutation bucketed to %q", b)
		}
		if b := bucketSummaryKey(mut("What are the new implications?")); b != "implications" {
			t.Fatalf("implications mutation bucketed to %q", b)
		}
	})
}

// TestSummarizePendingItemsDrain covers the drain queue: LLM call per item,
// done/error statuses, the legacy ” exemption (never auto-drained), and
// the AutoClassify gate. Table-driven where the invariant is per-case.
func TestSummarizePendingItemsDrain(t *testing.T) {
	t.Run("stores structured summaries", func(t *testing.T) {
		calls := stubSummarizer(t, nil, nil)
		_, d, noteID, feedID := summarizeFixture(t)
		for _, key := range []string{"s-a", "s-b"} {
			if _, err := d.Exec(
				`INSERT INTO ct_rss_items (feed_id, item_key, title, url, fetched_at, feed_content, summary_status)
				 VALUES (?, ?, ?, 'https://x', ?, 'Article body.', 'pending')`,
				feedID, key, key, nowStr(),
			); err != nil {
				t.Fatalf("insert %s: %v", key, err)
			}
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 2 {
			t.Fatalf("summarized = %d, want 2", done)
		}
		if *calls != 2 {
			t.Errorf("stub calls = %d, want 2 (one LLM call per article)", *calls)
		}
		for _, key := range []string{"s-a", "s-b"} {
			var status, js string
			if err := d.QueryRow(
				`SELECT summary_status, summary_json FROM ct_rss_items WHERE item_key = ?`, key,
			).Scan(&status, &js); err != nil {
				t.Fatalf("load %s: %v", key, err)
			}
			if status != "done" {
				t.Errorf("%s status = %q, want done", key, status)
			}
			s := parseSummaryJSON(js)
			if s == nil || len(s.WhatChanged) != 2 || len(s.Why) != 1 || len(s.Implications) != 1 {
				t.Errorf("%s summary_json = %q, want canned stub content", key, js)
			}
		}
		// Second run: queue empty.
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 0 {
			t.Errorf("second drain processed %d, want 0", done)
		}
	})

	t.Run("auto_classify off is a no-op", func(t *testing.T) {
		calls := stubSummarizer(t, nil, nil)
		_, d, noteID, feedID := summarizeFixture(t)
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, summary_status)
			 VALUES (?, 's-off', 'x', ?, 'body', 'pending')`, feedID, nowStr(),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: false}); done != 0 {
			t.Errorf("disabled drain processed %d, want 0", done)
		}
		if *calls != 0 {
			t.Errorf("LLM called with auto_classify off: %d calls", *calls)
		}
		var status string
		_ = d.QueryRow(`SELECT summary_status FROM ct_rss_items WHERE item_key = 's-off'`).Scan(&status)
		if status != "pending" {
			t.Errorf("status tampered under disabled drain: %q", status)
		}
	})

	t.Run("contentless item completes without an LLM call", func(t *testing.T) {
		calls := stubSummarizer(t, nil, nil)
		_, d, noteID, feedID := summarizeFixture(t)
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, summary_status)
			 VALUES (?, 's-empty', 'x', ?, '', 'pending')`, feedID, nowStr(),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 1 {
			t.Fatalf("contentless drain = %d, want 1 processed", done)
		}
		if *calls != 0 {
			t.Errorf("LLM called for a contentless item: %d calls", *calls)
		}
		var status, js string
		if err := d.QueryRow(
			`SELECT summary_status, summary_json FROM ct_rss_items WHERE item_key = 's-empty'`,
		).Scan(&status, &js); err != nil {
			t.Fatal(err)
		}
		if status != "done" {
			t.Errorf("contentless status = %q, want done (queue must not block)", status)
		}
		if s := parseSummaryJSON(js); s != nil {
			t.Errorf("contentless summary_json = %q, must stay empty-shaped", js)
		}
	})

	t.Run("model failure marks error and keeps the queue moving", func(t *testing.T) {
		// Two invalid replies per item (initial + corrective retry) → error.
		calls := stubSummarizer(t, []string{"garbage", "also garbage", "garbage", "also garbage"}, nil)
		_, d, noteID, feedID := summarizeFixture(t)
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, summary_status)
			 VALUES (?, 's-bad', 'x', ?, 'body', 'pending')`, feedID, nowStr(),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 1 {
			t.Fatalf("drain = %d, want 1", done)
		}
		if *calls != 2 {
			t.Errorf("calls = %d, want 2 (initial + one corrective retry)", *calls)
		}
		var status, summaryErr string
		if err := d.QueryRow(
			`SELECT summary_status, summary_error FROM ct_rss_items WHERE item_key = 's-bad'`,
		).Scan(&status, &summaryErr); err != nil {
			t.Fatal(err)
		}
		if status != "error" || summaryErr == "" {
			t.Errorf("error state = (%q, %q), want recorded error", status, summaryErr)
		}
		// The next drain retries the item (status pending again via action).
		if _, err := d.Exec(`UPDATE ct_rss_items SET summary_status = 'pending' WHERE item_key = 's-bad'`); err != nil {
			t.Fatal(err)
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 1 {
			t.Errorf("retry drain = %d, want 1", done)
		}
	})

	t.Run("legacy empty-status rows are never auto-drained", func(t *testing.T) {
		calls := stubSummarizer(t, nil, nil)
		_, d, noteID, feedID := summarizeFixture(t)
		// Legacy shape: summary_status '' (the ALTER default for rows that
		// predate the summarizer). A plain refresh must NOT summarize them.
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content)
			 VALUES (?, 's-legacy', 'x', ?, 'body')`, feedID, nowStr(),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if done := summarizePendingItems(d.DB, noteID, Config{AutoClassify: true}); done != 0 {
			t.Errorf("legacy rows drained: %d, want 0 (explicit opt-in only)", done)
		}
		if *calls != 0 {
			t.Errorf("LLM called for legacy row: %d calls", *calls)
		}
	})
}

// TestSummarizeProgressStages verifies the staged progress stream: the
// summarization drain labels its messages with stage "summarize" so the UI
// bar can say "Summarizing" instead of "Evaluating".
func TestSummarizeProgressStages(t *testing.T) {
	calls := stubSummarizer(t, nil, nil)
	p, d, noteID, feedID := summarizeFixture(t)
	for i := 0; i < 2; i++ {
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, summary_status)
			 VALUES (?, ?, 'x', ?, 'body', 'pending')`, feedID, fmt.Sprintf("s-p%d", i), nowStr(),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	var mu sync.Mutex
	var payloads []map[string]any
	p.SetProgressFunc(func(payload json.RawMessage) {
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			return
		}
		mu.Lock()
		payloads = append(payloads, m)
		mu.Unlock()
	})
	t.Cleanup(func() { p.SetProgressFunc(nil) })

	summarizePendingItems(d.DB, noteID, Config{AutoClassify: true})
	if *calls != 2 {
		t.Fatalf("calls = %d", *calls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) < 3 {
		t.Fatalf("progress payloads = %d, want ≥3 (start + per-item + finish)", len(payloads))
	}
	for i, m := range payloads {
		if m["stage"] != progressStageSummarize {
			t.Errorf("payload[%d].stage = %v, want %q", i, m["stage"], progressStageSummarize)
		}
	}
	if payloads[0]["done"] != float64(0) || payloads[0]["total"] != float64(2) {
		t.Errorf("start payload = %v, want done=0 total=2", payloads[0])
	}
	last := payloads[len(payloads)-1]
	if last["finished"] != true || last["done"] != float64(2) {
		t.Errorf("finish payload = %v, want finished done=2", last)
	}
	// Deterministic monotonicity: done never decreases within a stage.
	var prev float64
	for _, m := range payloads {
		done, _ := m["done"].(float64)
		if done < prev {
			t.Fatalf("progress went backwards: %v after %v", done, prev)
		}
		prev = done
	}
}

// TestSummarizeItemAction exercises the per-article action through the
// action dispatcher: stores the summary, updates the open modal payload,
// and rejects items without content.
func TestSummarizeItemAction(t *testing.T) {
	t.Run("stores and returns the summary", func(t *testing.T) {
		calls := stubSummarizer(t, nil, nil)
		p, d, noteID, feedID := summarizeFixture(t)
		res, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content)
			 VALUES (?, 'a1', 'Title', ?, 'Body text.')`, feedID, nowStr(),
		)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		itemID, _ := res.LastInsertId()

		out, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "summarize_item",
			jsonRaw(fmt.Sprintf(`{"item_id": %d}`, itemID)))
		if err != nil {
			t.Fatalf("summarize_item: %v", err)
		}
		m, ok := out.(map[string]any)
		if !ok || m["item_id"] != itemID {
			t.Fatalf("result = %#v", out)
		}
		s, ok := m["structured_summary"].(*StructuredSummary)
		if !ok || s == nil || len(s.WhatChanged) != 2 {
			t.Fatalf("structured_summary = %#v, want canned stub content", m["structured_summary"])
		}
		if *calls != 1 {
			t.Errorf("calls = %d, want 1", *calls)
		}
		var status string
		if err := d.QueryRow(
			`SELECT summary_status FROM ct_rss_items WHERE id = ?`, itemID,
		).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "done" {
			t.Errorf("stored status = %q, want done", status)
		}
	})

	t.Run("rejects contentless and unknown items", func(t *testing.T) {
		_ = stubSummarizer(t, nil, nil)
		p, d, noteID, feedID := summarizeFixture(t)
		res, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content)
			 VALUES (?, 'a2', 'Empty', ?, '')`, feedID, nowStr(),
		)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		emptyID, _ := res.LastInsertId()

		for name, params := range map[string]string{
			// Failure mode prevented: a contentless item must fail loudly
			// instead of burning an LLM call on "(no content available)".
			"contentless": fmt.Sprintf(`{"item_id": %d}`, emptyID),
			// Failure mode prevented: cross-reader item ids must not resolve.
			"unknown_item": `{"item_id": 999999}`,
			// Failure mode prevented: parameter validation must reject
			// garbage before any LLM call.
			"missing_param": `{}`,
		} {
			_, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "summarize_item", jsonRaw(params))
			if err == nil {
				t.Errorf("%s: expected error", name)
				continue
			}
			var bad *notetype.BadRequestError
			if !errors.As(err, &bad) {
				t.Errorf("%s: err = %v, want BadRequestError", name, err)
			}
		}
	})
}

// TestSummarizeMissingAction: the settings button's backend — queue exactly
// the rows without a stored summary (” legacy, error, pending), keep the
// done ones, and schedule the drain job.
func TestSummarizeMissingAction(t *testing.T) {
	p, d, noteID, feedID := summarizeFixture(t)
	rec := installEnqueue(t, p)

	seed := func(key, status, js string) {
		t.Helper()
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, summary_status, summary_json)
			 VALUES (?, ?, ?, ?, 'body', ?, ?)`, feedID, key, key, nowStr(), status, js,
		); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	canonical := canonicalStubSummary(t)
	// Fixture default shape: status '' (legacy row).
	seed("legacy", "", "")
	// Already summarized → must not be requeued.
	seed("done-row", "done", canonical)
	// Errored run → requeue.
	seed("errored", "error", "")
	// Freshly pending → idempotent requeue.
	seed("pending-row", "pending", "")

	res, err := p.HandleAction(context.Background(), d.DB, 0, noteID, "summarize_missing", jsonRaw(`{}`))
	if err != nil {
		t.Fatalf("summarize_missing: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok || m["reset"] != int64(3) {
		t.Fatalf("result = %#v, want reset=3 (legacy, errored, pending — not done-row)", res)
	}

	var pending, done int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE summary_status = 'pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM ct_rss_items WHERE summary_status = 'done' AND summary_json != ''`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if pending != 3 || done != 1 {
		t.Errorf("queue = (%d pending, %d done), want (3, 1)", pending, done)
	}

	// Existing summary kept verbatim: no overwrite by the queue action.
	var js string
	if err := d.QueryRow(`SELECT summary_json FROM ct_rss_items WHERE item_key = 'done-row'`).Scan(&js); err != nil {
		t.Fatal(err)
	}
	if js != canonical {
		t.Errorf("done-row summary_json overwritten: %q", js)
	}

	// A refresh job is scheduled to drain the queue.
	if len(rec.calls) != 1 || rec.calls[0].pluginID != PluginID || rec.calls[0].jobName != JobRefreshFeeds {
		t.Fatalf("enqueue calls = %+v, want one refresh_feeds job", rec.calls)
	}
}

// TestViewSummaryCountsAndDigest: the view folds summary state into the
// counts (drives the Settings buttons + hint lines) and exposes the parsed
// digest per item (drives the card rendering).
func TestViewSummaryCountsAndDigest(t *testing.T) {
	p, d, noteID, feedID := summarizeFixture(t)
	seed := func(key, status, js string) {
		t.Helper()
		if _, err := d.Exec(
			`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content, classify_status, summary_status, summary_json)
			 VALUES (?, ?, ?, ?, 'body', 'done', ?, ?)`, feedID, key, key, nowStr(), status, js,
		); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	seed("v-done", "done", canonicalStubSummary(t))
	seed("v-legacy", "", "")
	seed("v-pending", "pending", "")
	seed("v-errored", "error", "")
	seed("v-disabled", "disabled", "")

	view, err := p.BuildView(context.Background(), d.DB, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	v, ok := view.(*View)
	if !ok {
		t.Fatalf("view type = %T", view)
	}
	// Invariants: summarized+unsummarized partition the non-disabled rows;
	// pending_summarize counts only the queued ones.
	if v.Counts.Summarized != 1 {
		t.Errorf("counts.summarized = %d, want 1", v.Counts.Summarized)
	}
	if v.Counts.Unsummarized != 3 { // legacy + pending + errored
		t.Errorf("counts.unsummarized = %d, want 3", v.Counts.Unsummarized)
	}
	if v.Counts.PendingSummarize != 1 {
		t.Errorf("counts.pending_summarize = %d, want 1", v.Counts.PendingSummarize)
	}

	byKey := map[string]*ItemView{}
	for i := range v.Items {
		byKey[v.Items[i].Title] = &v.Items[i]
	}
	if it := byKey["v-done"]; it == nil || it.StructuredSummary == nil || len(it.StructuredSummary.WhatChanged) != 2 {
		t.Errorf("v-done digest = %+v, want parsed stub summary", it)
	}
	if it := byKey["v-legacy"]; it == nil || it.StructuredSummary != nil || it.SummaryStatus != "" {
		t.Errorf("v-legacy digest = %+v, want nil digest and legacy status", it)
	}
}

// TestGetItemReturnsSummaryFields: the article modal's payload must carry
// the digest + status so the modal can render it and the Summarize button
// can pick the right title/hint.
func TestGetItemReturnsSummaryFields(t *testing.T) {
	_, d, noteID, feedID := summarizeFixture(t)
	res, err := d.Exec(
		`INSERT INTO ct_rss_items (feed_id, item_key, title, fetched_at, feed_content,
		 classify_status, summary_status, summary_json)
		 VALUES (?, 'g1', 'Title', ?, 'Body.', 'done', 'done', ?)`, feedID, nowStr(),
		`{"what_changed":["F: a"],"why":["F: b"],"implications":[]}`,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	itemID, _ := res.LastInsertId()

	out, err := actionGetItem(d.DB, noteID, jsonRaw(fmt.Sprintf(`{"item_id": %d}`, itemID)))
	if err != nil {
		t.Fatalf("get_item: %v", err)
	}
	it, ok := out.(*ArticleDetail)
	if !ok {
		t.Fatalf("get_item type = %T", out)
	}
	if it.SummaryStatus != "done" || it.StructuredSummary == nil ||
		len(it.StructuredSummary.WhatChanged) != 1 || len(it.StructuredSummary.Why) != 1 {
		t.Errorf("get_item summary fields = (%q, %+v)", it.SummaryStatus, it.StructuredSummary)
	}
}

// TestSaveAsNoteBodyContainsDigest: saved notes embed the telegraph
// summary as markdown (searchable + visible in the note body).
func TestSaveAsNoteBodyContainsDigest(t *testing.T) {
	js := `{"what_changed":["F: EU approved the AI Act"],"why":["Q: \"landmark\" — Parliament"],"implications":["F: fines up to 7% of turnover"]}`
	body := saveAsNoteBody(&itemForSave{
		Title:       "AI Act passes",
		URL:         "https://example.com/ai-act",
		FeedName:    "EU Wire",
		SummaryJSON: js,
		FullContent: "The full text.",
	})
	for _, want := range []string{
		"**Summary**", "*What has changed in reality?*", "F: EU approved the AI Act",
		"Why has reality changed?", "F: fines up to 7% of turnover", "The full text.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("note body missing %q:\n%s", want, body)
		}
	}
}
