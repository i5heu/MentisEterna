package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype/llminferencer"
)

// classifyMaxRunes caps the article text sent to the classifier.
const classifyMaxRunes = 10_000

// Classification is the structured result a prompt must produce.
type Classification struct {
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	Quality  int      `json:"quality"`
	Tags     []string `json:"tags"`
}

// defaultPrompt classifies articles when no rss_prompt note is in scope.
const defaultPrompt = `You are a classification service for a personal news reading pipeline.

Read the article text provided and judge it:
- category: one short topical label (for example: Technology, Science, Business, Politics, Culture, Health, Sports, World, Opinion).
- summary: one or two plain-text sentences describing what the article actually says.
- quality: an integer 0 (worthless, spammy, or clickbait) to 10 (substantive, informative article).
- tags: zero to three short topical keywords.

Summarize only what the text says. Be skeptical; do not invent facts.`

// classifyContract is appended to every prompt (user-defined or default) so
// the output stays machine-readable.
const classifyContract = `

Return ONLY one JSON object in exactly this shape, with no markdown, no code fence, and no other text:
{"category": "<short label>", "summary": "<1-2 sentences>", "quality": <0-10 integer>, "tags": ["<label>", ...]}`

// classifier is the seam between the pipeline and the LLM backend; tests
// replace newClassifier with a stub.
type classifier interface {
	Classify(system, user string) (string, error)
	BeginUse() func()
}

type llmClassifier struct {
	client *llm.ChatClient
}

func (l llmClassifier) Classify(system, user string) (string, error) {
	return l.client.Complete([]llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

// BeginUse holds the backend lease so the model is not idle-stopped mid-run.
func (l llmClassifier) BeginUse() func() {
	return llm.BeginBackendUse(l.client)
}

// newClassifier is the production seam for tests. The classifier resolves
// through the note hierarchy: the nearest llm_inferencer note binding the
// "rss_classify" usage (OpenRouter / Mistral / any OpenAI-compatible
// provider, with the note-stored API key) wins; with no binding the
// config.toml feature/tier system client applies. On resolver failure the
// config client is used so a broken inferencer note degrades gracefully
// rather than blocking classification.
var newClassifier = func(db *sql.DB, noteID int64) classifier {
	client, _, err := llminferencer.ResolveChat(db, noteID, "rss_classify")
	if err != nil || client == nil {
		return llmClassifier{client: llm.NewRSSClassifierClient()}
	}
	return llmClassifier{client: client}
}

// classifyUserMessage builds the user message for one article.
func classifyUserMessage(title, feedName, url, content string) string {
	if content == "" {
		content = "(no content available)"
	}
	if utf8.RuneCountInString(content) > classifyMaxRunes {
		content = string([]rune(content)[:classifyMaxRunes]) + "…"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Article title: %s\n", title)
	if feedName != "" {
		fmt.Fprintf(&b, "Feed: %s\n", feedName)
	}
	if url != "" {
		fmt.Fprintf(&b, "URL: %s\n", url)
	}
	b.WriteString("\n")
	b.WriteString(content)
	return b.String()
}

// parseClassification decodes the LLM reply, clipping fields into sane
// bounds so bad prompts cannot poison the schema.
func parseClassification(raw string) (Classification, error) {
	var out Classification
	if err := llm.UnmarshalLooseJSONObject(raw, &out); err != nil {
		return Classification{}, err
	}
	out.Category = strings.TrimSpace(out.Category)
	if utf8.RuneCountInString(out.Category) > 100 {
		out.Category = string([]rune(out.Category)[:100])
	}
	out.Summary = strings.TrimSpace(out.Summary)
	if utf8.RuneCountInString(out.Summary) > 1200 {
		out.Summary = string([]rune(out.Summary)[:1200])
	}
	if out.Quality < 0 {
		out.Quality = 0
	} else if out.Quality > 10 {
		out.Quality = 10
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	if len(out.Tags) > 10 {
		out.Tags = out.Tags[:10]
	}
	for i, tag := range out.Tags {
		tag = strings.TrimSpace(tag)
		if utf8.RuneCountInString(tag) > 60 {
			tag = string([]rune(tag)[:60])
		}
		out.Tags[i] = tag
	}
	return out, nil
}

// classifyOnce runs one classification attempt with the given system prompt;
// invalid output is retried once with the parse error appended.
func classifyOnce(c classifier, system, user string) (Classification, string, error) {
	raw, err := c.Classify(system, user)
	if err != nil {
		return Classification{}, "", err
	}
	out, perr := parseClassification(raw)
	if perr != nil {
		// One corrective retry.
		retrySystem := system + fmt.Sprintf("\n\nYour previous reply was invalid: %v. Reply again correctly.", perr)
		raw2, err := c.Classify(retrySystem, user)
		if err != nil {
			return Classification{}, raw, err
		}
		out, perr = parseClassification(raw2)
		if perr != nil {
			return Classification{}, raw2, fmt.Errorf("invalid model output: %w", perr)
		}
		return out, raw2, nil
	}
	return out, raw, nil
}

// itemForClassify is a pending classification work row.
type itemForClassify struct {
	ID      int64
	Title   string
	URL     string
	Content string // full_content || feed_content
}

// loadPendingItems selects up to classifyBatchSize pending items for a note,
// oldest first.
func loadPendingItems(db *sql.DB, noteID int64) ([]itemForClassify, error) {
	rows, err := db.Query(`
		SELECT i.id, i.title, i.url, COALESCE(NULLIF(i.full_content,''), i.feed_content, '')
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ? AND i.classify_status = 'pending'
		ORDER BY i.id
		LIMIT ?`, noteID, classifyBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: classify: load pending: %w", err)
	}
	defer rows.Close()
	var items []itemForClassify
	for rows.Next() {
		var it itemForClassify
		if err := rows.Scan(&it.ID, &it.Title, &it.URL, &it.Content); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// storeClassification persists a successful classification.
func storeClassification(db *sql.DB, itemID, promptNoteID int64, c Classification) error {
	tagsJSON, err := json.Marshal(c.Tags)
	if err != nil || len(tagsJSON) == 0 {
		tagsJSON = []byte("[]")
	}
	_, err = db.Exec(
		`UPDATE ct_rss_items
		 SET category = ?, summary = ?, tags_json = ?, quality = ?,
		     classify_status = 'done', classify_error = '', prompt_note_id = ?
		 WHERE id = ?`,
		c.Category, c.Summary, string(tagsJSON), c.Quality, promptNoteID, itemID,
	)
	if err != nil {
		return fmt.Errorf("rss: classify: store item %d: %w", itemID, err)
	}
	return nil
}

// classifyPendingItems processes pending items for a reader note and returns
// the number classified (done or errored).
func classifyPendingItems(db *sql.DB, noteID int64, cfg Config) int {
	if !cfg.AutoClassify {
		return 0
	}
	promptRef, prompt, err := promptText(db, noteID)
	if err != nil {
		return 0
	}
	items, err := loadPendingItems(db, noteID)
	if err != nil || len(items) == 0 {
		return 0
	}

	cls := newClassifier(db, noteID)
	release := cls.BeginUse()
	defer release()

	system := prompt + classifyContract
	processed := 0
	for _, it := range items {
		user := classifyUserMessage(it.Title, "", it.URL, it.Content)
		classification, _, err := classifyOnce(cls, system, user)
		if err != nil {
			// Distinguish model failure from config failure: keep the item
			// visible with an error; the user can reclassify manually.
			msg := err.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			_, _ = db.Exec(
				`UPDATE ct_rss_items SET classify_status = 'error', classify_error = ? WHERE id = ?`,
				msg, it.ID,
			)
			processed++
			continue
		}
		if err := storeClassification(db, it.ID, promptRef.NoteID, classification); err != nil {
			_, _ = db.Exec(
				`UPDATE ct_rss_items SET classify_status = 'error', classify_error = ? WHERE id = ?`,
				err.Error(), it.ID,
			)
		}
		processed++
	}
	return processed
}
