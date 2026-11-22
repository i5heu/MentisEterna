package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype/llminferencer"
)

// summarizeTimeBudget bounds how long one refresh's summarize stage spends
// draining the pending-summary queue (multiple batches). It runs after the
// 8-minute evaluation budget inside the same job, so this stays lean.
const summarizeTimeBudget = 6 * time.Minute

// Clamps for model output: prompt asks for ≤ 200 words, but a stray prompt
// override must not be able to bloat the stored summary_json.
const (
	summarizeMaxLinesPerSection = 15
	summarizeMaxRunesPerLine    = 300
)

// defaultSummarizerPrompt is the built-in article-summarization prompt (a
// sibling of the significance-evaluation defaultPrompt). It compresses an
// article into telegraph-style F/O/Q lines grouped under three questions.
// Unlike the evaluation prompt it is not note-resolvable (yet) — it lives
// here as the single constant every reader uses.
const defaultSummarizerPrompt = `Please summarize the provided article in no more than 200 words.

Instructions:

- Use very short, telegraph-style language.
- Label each line with:
- F: for facts.
- O: for opinions, followed by a dash and the source.
- Q: for quotes, followed by a dash and the source.
- Organize the content under these questions:
1. What has changed in reality?
2. Why has reality changed?
3. What are the new implications?
- Use separate lines for each F:, O:, or Q: entry.
- Translate all text to English, except for German text, which should remain untranslated.
- Exclude any source links from the output.

Output Format:

Provide the response in plain text JSON (no markdown), following this schema:

{
"content": {
"What has changed in reality?": [ "string", "string", "..."],
"Why has reality changed?": [ "string", "string", "..."],
"What are the new implications?": [ "string", "string", "..."]
}
}`

// summarizeContract is appended to the prompt so the output stays
// machine-readable: the JSON object ends the reply, nothing may follow it.
const summarizeContract = `

Output contract (binding): Reply with exactly ONE JSON object in the shape below — plain text, no markdown fence, nothing before or after it:
{"content": {"What has changed in reality?": ["..."], "Why has reality changed?": ["..."], "What are the new implications?": ["..."]}}`

// StructuredSummary is the summarizer's parsed output: telegraph-style
// F/O/Q lines grouped under the prompt's three questions. Stored as
// summary_json (canonical keys, never the raw question strings).
type StructuredSummary struct {
	WhatChanged  []string `json:"what_changed"`
	Why          []string `json:"why"`
	Implications []string `json:"implications"`
}

// Empty reports whether the summary carries no lines at all (e.g. stored
// empty for items without article text).
func (s *StructuredSummary) Empty() bool {
	return s == nil || (len(s.WhatChanged) == 0 && len(s.Why) == 0 && len(s.Implications) == 0)
}

// summarizer is the summarize-pipeline seam (mirrors classifier); tests
// replace newSummarizer with a stub.
type summarizer interface {
	Summarize(system, user string) (string, error)
	BeginUse() func()
}

type llmSummarizer struct {
	client *llm.ChatClient
}

func (l llmSummarizer) Summarize(system, user string) (string, error) {
	return l.client.Complete([]llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

// BeginUse holds the backend lease so the model is not idle-stopped mid-run.
func (l llmSummarizer) BeginUse() func() {
	return llm.BeginBackendUse(l.client)
}

// newSummarizer is the production seam for tests. It resolves through the
// app's single llm_inferencer note exactly like newClassifier does, but for
// the "rss_summarize" usage — so summaries can run on a cheaper tier than
// the evaluation. {local: true} pins or no binding fall back to the
// config.toml feature client (small tier by default).
var newSummarizer = func(db *sql.DB, noteID int64) summarizer {
	client, _, err := llminferencer.ResolveChat(db, "rss_summarize")
	if err != nil || client == nil {
		return llmSummarizer{client: llm.NewFeatureChatClient("rss_summarize")}
	}
	return llmSummarizer{client: client}
}

// summarizeUserMessage builds the user message for one article (same shape as
// the evaluation pass; the prompt itself keeps F:/O:/Q: lines link-free).
func summarizeUserMessage(title, feedName, url, content string) string {
	return classifyUserMessage(title, feedName, url, content)
}

// summaryKey normalizes a question key for matching: lowercase, punctuation
// and whitespace collapsed.
func summaryKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			// skip separators
		default:
			// punctuation and everything else
		}
	}
	return b.String()
}

// bucketSummaryKey maps a raw reply key to a StructuredSummary section
// ("" = unrecognized, dropped). Matching is fuzzy because models
// occasionally rephrase or lose the trailing question mark.
func bucketSummaryKey(k string) string {
	n := summaryKey(k)
	switch {
	case strings.Contains(n, "whathaschanged"):
		return "what_changed"
	case strings.Contains(n, "why"):
		return "why"
	case strings.Contains(n, "implication") || strings.Contains(n, "whatare"):
		return "implications"
	default:
		return ""
	}
}

// clipSummaryLines trims, drops empties, and clamps line count/length.
func clipSummaryLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, line := range in {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > summarizeMaxRunesPerLine {
			line = string([]rune(line)[:summarizeMaxRunesPerLine]) + "…"
		}
		out = append(out, line)
		if len(out) >= summarizeMaxLinesPerSection {
			break
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// parseSummary decodes the summarizer reply into the three canonical
// sections. Tolerant of markdown fences and prose around the object (loose
// unmarshal), of a missing "content" wrapper (top-level question keys), and
// of rephrased question keys — unknown keys are dropped, never fatal.
func parseSummary(raw string) (StructuredSummary, error) {
	var wrapped struct {
		Content map[string][]string `json:"content"`
	}
	err := llm.UnmarshalLooseJSONObject(raw, &wrapped)
	if err != nil || len(wrapped.Content) == 0 {
		// Defensive: reply without the "content" wrapper — treat the whole
		// object as the question map.
		var direct map[string][]string
		if err2 := llm.UnmarshalLooseJSONObject(raw, &direct); err2 != nil {
			if err == nil {
				err = err2
			}
			return StructuredSummary{}, fmt.Errorf("rss: summarize: parse: %w", err)
		}
		wrapped.Content = direct
	}

	var out StructuredSummary
	for k, lines := range wrapped.Content {
		switch bucketSummaryKey(k) {
		case "what_changed":
			out.WhatChanged = clipSummaryLines(lines)
		case "why":
			out.Why = clipSummaryLines(lines)
		case "implications":
			out.Implications = clipSummaryLines(lines)
		}
	}
	if out.WhatChanged == nil {
		out.WhatChanged = []string{}
	}
	if out.Why == nil {
		out.Why = []string{}
	}
	if out.Implications == nil {
		out.Implications = []string{}
	}
	if out.Empty() {
		return StructuredSummary{}, fmt.Errorf("rss: summarize: reply carried no summary lines")
	}
	return out, nil
}

// storeSummary persists a successful summary. A summary_json of "" (empty
// struct — items without article text) is a valid "done" state.
func storeSummary(db *sql.DB, itemID int64, s StructuredSummary) error {
	js, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("rss: summarize: encode item %d: %w", itemID, err)
	}
	if _, err := db.Exec(
		`UPDATE ct_rss_items
		 SET summary_json = ?, summary_status = 'done', summary_error = ''
		 WHERE id = ?`, string(js), itemID,
	); err != nil {
		return fmt.Errorf("rss: summarize: store item %d: %w", itemID, err)
	}
	return nil
}

// parseSummaryJSON decodes a stored summary_json column into a
// StructuredSummary (nil when absent or malformed).
func parseSummaryJSON(raw string) *StructuredSummary {
	if raw == "" {
		return nil
	}
	var s StructuredSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Empty() {
		return nil
	}
	return &s
}

// summarizeOnce runs one summarization attempt; invalid output is retried
// once with the parse error appended (mirrors classifyOnce).
func summarizeOnce(s summarizer, system, user string) (StructuredSummary, string, error) {
	raw, err := s.Summarize(system, user)
	if err != nil {
		return StructuredSummary{}, "", err
	}
	out, perr := parseSummary(raw)
	if perr != nil {
		retrySystem := system + fmt.Sprintf("\n\nYour previous reply was invalid: %v. Reply again correctly.", perr)
		raw2, err := s.Summarize(retrySystem, user)
		if err != nil {
			return StructuredSummary{}, raw, err
		}
		out, perr = parseSummary(raw2)
		if perr != nil {
			return StructuredSummary{}, raw2, fmt.Errorf("invalid model output: %w", perr)
		}
		return out, raw2, nil
	}
	return out, raw, nil
}

// itemForSummarize is a pending summary work row.
type itemForSummarize struct {
	ID      int64
	Title   string
	URL     string
	Content string
}

// loadPendingSummaries selects up to classifyBatchSize items awaiting
// summarization for a note, oldest first.
func loadPendingSummaries(db *sql.DB, noteID int64) ([]itemForSummarize, error) {
	rows, err := db.Query(`
		SELECT i.id, i.title, i.url, COALESCE(NULLIF(i.full_content,''), i.feed_content, '')
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ? AND i.summary_status = 'pending'
		ORDER BY i.id
		LIMIT ?`, noteID, classifyBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: summarize: load pending: %w", err)
	}
	defer rows.Close()
	var items []itemForSummarize
	for rows.Next() {
		var it itemForSummarize
		if err := rows.Scan(&it.ID, &it.Title, &it.URL, &it.Content); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// summarizeProgressCount returns how many items are pending summarization
// (the progress total at drain start).
func summarizeProgressCount(db *sql.DB, noteID int64) int {
	var n int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ? AND i.summary_status = 'pending'`, noteID,
	).Scan(&n)
	return n
}

// summarizePendingItems drains the pending-summary queue for a reader, one
// LLM call per article, in batches with its own time budget. Items without
// any stored content complete instantly with an empty summary (status done)
// so they cannot block the queue. Per-item progress streams to the UI under
// the "summarize" stage. Returns the number processed.
func summarizePendingItems(db *sql.DB, noteID int64, cfg Config) int {
	if !cfg.AutoClassify {
		return 0
	}
	total := summarizeProgressCount(db, noteID)
	if total == 0 {
		return 0
	}

	sum := newSummarizer(db, noteID)
	release := sum.BeginUse()
	defer release()

	system := defaultSummarizerPrompt + summarizeContract
	reportEvaluationProgress(noteID, progressStageSummarize, 0, total, false)

	processed := 0
	drained := false
	deadline := time.Now().Add(summarizeTimeBudget)
	for {
		items, err := loadPendingSummaries(db, noteID)
		if err != nil || len(items) == 0 {
			drained = err == nil
			break
		}
		for _, it := range items {
			var serr error
			if strings.TrimSpace(it.Content) == "" {
				// Nothing to summarize — finish instantly instead of
				// burning an LLM call (and blocking the queue forever).
				serr = storeSummary(db, it.ID, StructuredSummary{WhatChanged: []string{}, Why: []string{}, Implications: []string{}})
			} else {
				var s StructuredSummary
				s, _, serr = summarizeOnce(sum, system, summarizeUserMessage(it.Title, "", it.URL, it.Content))
				if serr == nil {
					serr = storeSummary(db, it.ID, s)
				}
			}
			if serr != nil {
				msg := serr.Error()
				if len(msg) > 500 {
					msg = msg[:500]
				}
				_, _ = db.Exec(
					`UPDATE ct_rss_items SET summary_status = 'error', summary_error = ? WHERE id = ?`,
					msg, it.ID,
				)
			}
			processed++
			reportEvaluationProgress(noteID, progressStageSummarize, processed, total, false)
		}
		if len(items) < classifyBatchSize {
			drained = true
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	reportEvaluationProgress(noteID, progressStageSummarize, processed, total, drained)
	return processed
}
