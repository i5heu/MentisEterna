package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// actionReclassify synchronously re-runs the LLM evaluation for one article
// using the currently-scoped prompt.
func (p *Plugin) actionReclassify(ctx context.Context, db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var pr struct {
		ItemID int64 `json:"item_id"`
	}
	if err := json.Unmarshal(params, &pr); err != nil {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: invalid params: %v", err)}
	}
	if pr.ItemID <= 0 {
		return nil, &notetype.BadRequestError{Message: "rss: item_id is required"}
	}

	var it itemForClassify
	var feedName string
	err := db.QueryRow(`
		SELECT i.id, i.title, i.url, COALESCE(NULLIF(i.full_content,''), i.feed_content, ''),
		       i.penalty_score, COALESCE(NULLIF(f.name,''), '')
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE i.id = ? AND f.note_id = ?
	`, pr.ItemID, noteID).Scan(&it.ID, &it.Title, &it.URL, &it.Content, &it.Penalty, &feedName)
	if err == sql.ErrNoRows {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: item %d not found in note %d", pr.ItemID, noteID)}
	} else if err != nil {
		return nil, fmt.Errorf("rss: load item: %w", err)
	}

	promptRef, prompt, err := promptText(db, noteID)
	if err != nil {
		return nil, err
	}

	cls := newClassifier(db, noteID)
	release := cls.BeginUse()
	defer release()

	if ctx.Err() != nil {
		return nil, &notetype.BadRequestError{Message: "rss: context cancelled"}
	}
	classification, _, err := classifyOnce(cls, prompt+classifyContract, classifyUserMessage(it.Title, feedName, it.URL, it.Content))
	if err != nil {
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		_, _ = db.Exec(
			`UPDATE ct_rss_items SET classify_status = 'error', classify_error = ? WHERE id = ?`,
			msg, it.ID,
		)
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: evaluation failed: %v", err)}
	}
	weights := classificationWeights(db, noteID)
	if err := storeClassification(db, it.ID, promptRef.NoteID, classification, weights, it.Penalty); err != nil {
		return nil, err
	}
	points := computePoints(classification.metrics(), weights, it.Penalty)
	return map[string]any{
		"item_id":            it.ID,
		"summary":            classification.Summary,
		"reflection":         classification.EvaluationReflectionParagraph,
		"significance":       classification.Significance,
		"negativity":         classification.Negativity,
		"personal_relevance": classification.PersonalRelevance,
		"points":             points,
		"tags":               classification.Tags,
	}, nil
}

// HandleAction implements notetype.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) {
	if db == nil {
		return nil, &notetype.BadRequestError{Message: "rss: database not available"}
	}
	switch actionID {
	case "refresh":
		return p.actionRefresh(noteID)
	case "save_as_note":
		return p.actionSaveAsNote(db, noteID, params)
	case "set_item_state":
		return actionSetItemState(db, noteID, params)
	case "get_item":
		return actionGetItem(db, noteID, params)
	case "reclassify":
		return p.actionReclassify(ctx, db, noteID, params)
	case "reclassify_all":
		return p.actionReclassifyAll(db, noteID)
	case "evaluate_missing":
		return p.actionEvaluateMissing(db, noteID)
	case "reapply_rules":
		return actionReapplyRules(db, noteID)
	default:
		return nil, notetype.ErrUnknownAction
	}
}

// queueEvaluationRun schedules the drain refresh for a reader after an
// evaluation reset (reclassify_all / evaluate_missing), best effort: the
// job is what actually runs the queue, a failed enqueue must not fail the
// action (the items stay pending; the next cron tick drains them).
func (p *Plugin) queueEvaluationRun(noteID int64, out map[string]any) {
	if p.enqueue == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"note_id": noteID, "force": true})
	if err != nil {
		return
	}
	runID, err := p.enqueue(PluginID, JobRefreshFeeds, payload)
	if err == nil {
		out["run_id"] = runID
	}
}

// actionReclassifyAll queues every article of the reader for a fresh LLM
// evaluation (classify_status → pending) and schedules a refresh job, whose
// classify stage drains the queue with the currently scoped prompt.
// Existing scores are overwritten.
func (p *Plugin) actionReclassifyAll(db *sql.DB, noteID int64) (any, error) {
	res, err := db.Exec(`
		UPDATE ct_rss_items SET classify_status = 'pending', classify_error = ''
		WHERE feed_id IN (SELECT id FROM ct_rss_feeds WHERE note_id = ?)`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: queue re-evaluation: %w", err)
	}
	reset, _ := res.RowsAffected()

	out := map[string]any{"reset": reset, "note_id": noteID}
	p.queueEvaluationRun(noteID, out)
	return out, nil
}

// actionEvaluateMissing queues ONLY the articles that have no evaluation
// (evaluated = 0 — e.g. the legacy rows classified before the significance
// evaluation, plus pending/errored ones) and schedules the drain refresh.
// Articles that already carry an evaluation keep their scores untouched.
func (p *Plugin) actionEvaluateMissing(db *sql.DB, noteID int64) (any, error) {
	res, err := db.Exec(`
		UPDATE ct_rss_items SET classify_status = 'pending', classify_error = ''
		WHERE feed_id IN (SELECT id FROM ct_rss_feeds WHERE note_id = ?) AND evaluated = 0`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: queue missing evaluation: %w", err)
	}
	reset, _ := res.RowsAffected()

	out := map[string]any{"reset": reset, "note_id": noteID}
	p.queueEvaluationRun(noteID, out)
	return out, nil
}

// ArticleDetail is the get_item action response: one article with its
// content for inline reading. Content is deliberately NOT part of the view
// payload (200 articles × up to 200 KB would dwarf it) — the card fetches
// it on demand through this action.
type ArticleDetail struct {
	ID                     int64           `json:"item_id"`
	FeedID                 int64           `json:"feed_id"`
	FeedName               string          `json:"feed_name"`
	Title                  string          `json:"title"`
	URL                    string          `json:"url"`
	Author                 string          `json:"author"`
	PublishedAt            string          `json:"published_at"`
	FetchedAt              string          `json:"fetched_at"`
	Content                string          `json:"content"`
	ContentSource          string          `json:"content_source"` // extracted | feed | "" (none stored)
	FullStatus             string          `json:"full_status"`    // none|fetched|failed|too_big
	Category               string          `json:"category"`
	Summary                string          `json:"summary"`
	Tags                   []string        `json:"tags"`
	Quality                int             `json:"quality"`
	Significance           float64         `json:"significance"`
	Negativity             float64         `json:"negativity"`
	PersonalRelevance      float64         `json:"personal_relevance"`
	AffectedNowDirect      float64         `json:"affected_now_direct"`
	AffectedNowIndirect    float64         `json:"affected_now_indirect"`
	AffectedFutureDirect   float64         `json:"affected_future_direct"`
	AffectedFutureIndirect float64         `json:"affected_future_indirect"`
	Reflection             string          `json:"reflection"`
	Points                 float64         `json:"points"`
	Evaluated              bool            `json:"evaluated"`
	ClassifyStatus         string          `json:"classify_status"`
	ClassifyError          string          `json:"classify_error"`
	PenaltyScore           float64         `json:"penalty_score"`
	Penalty                json.RawMessage `json:"penalty"`
	Banned                 bool            `json:"banned"`
	State                  string          `json:"state"`
	SavedNoteID            int64           `json:"saved_note_id"`
}

// actionGetItem returns one article with the stored content for inline
// reading: the extracted full content if present, else the feed-provided
// snippet.
func actionGetItem(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var pr struct {
		ItemID int64 `json:"item_id"`
	}
	if err := json.Unmarshal(params, &pr); err != nil {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: invalid params: %v", err)}
	}
	if pr.ItemID <= 0 {
		return nil, &notetype.BadRequestError{Message: "rss: item_id is required"}
	}

	var it ArticleDetail
	var fullContent, feedContent, tagsJSON, penaltyJSON string
	var banned, evaluated int
	err := db.QueryRow(`
		SELECT i.id, i.feed_id, COALESCE(NULLIF(f.name,''), ''), i.title, i.url, i.author,
		       i.published_at, i.fetched_at, i.full_status,
		       i.full_content, i.feed_content,
		       i.category, i.summary, i.tags_json, i.quality,
		       i.significance, i.negativity, i.personal_relevance,
		       i.affected_now_direct, i.affected_now_indirect,
		       i.affected_future_direct, i.affected_future_indirect,
		       i.reflection, i.points, i.evaluated,
		       i.classify_status, i.classify_error,
		       i.penalty_score, i.penalty_json, i.banned, i.state, i.saved_note_id
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE i.id = ? AND f.note_id = ?
	`, pr.ItemID, noteID).Scan(
		&it.ID, &it.FeedID, &it.FeedName, &it.Title, &it.URL, &it.Author,
		&it.PublishedAt, &it.FetchedAt, &it.FullStatus,
		&fullContent, &feedContent,
		&it.Category, &it.Summary, &tagsJSON, &it.Quality,
		&it.Significance, &it.Negativity, &it.PersonalRelevance,
		&it.AffectedNowDirect, &it.AffectedNowIndirect,
		&it.AffectedFutureDirect, &it.AffectedFutureIndirect,
		&it.Reflection, &it.Points, &evaluated,
		&it.ClassifyStatus, &it.ClassifyError,
		&it.PenaltyScore, &penaltyJSON, &banned, &it.State, &it.SavedNoteID,
	)
	if err == sql.ErrNoRows {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: item %d not found in note %d", pr.ItemID, noteID)}
	} else if err != nil {
		return nil, fmt.Errorf("rss: load item: %w", err)
	}
	it.Tags = parseTagsJSON(tagsJSON)
	it.Penalty = json.RawMessage(penaltyJSON)
	it.Banned = banned != 0
	it.Evaluated = evaluated != 0

	// Content preference matches classification: extracted full content,
	// falling back to the feed-provided snippet.
	switch {
	case fullContent != "":
		it.Content, it.ContentSource = fullContent, "extracted"
	case feedContent != "":
		it.Content, it.ContentSource = feedContent, "feed"
	}

	return &it, nil
}

// actionRefresh enqueues the refresh_feeds job for this reader (force),
// bypassing the per-feed interval check.
func (p *Plugin) actionRefresh(noteID int64) (any, error) {
	if p.enqueue == nil {
		return nil, &notetype.BadRequestError{Message: "rss: job system not available"}
	}
	payload, err := json.Marshal(map[string]any{"note_id": noteID, "force": true})
	if err != nil {
		return nil, fmt.Errorf("rss: marshal refresh payload: %w", err)
	}
	runID, err := p.enqueue(PluginID, JobRefreshFeeds, payload)
	if err != nil {
		return nil, fmt.Errorf("rss: enqueue refresh: %w", err)
	}
	return map[string]any{"run_id": runID}, nil
}

// itemStateActionParams is the body for set_item_state.
type itemStateActionParams struct {
	ItemID int64  `json:"item_id"`
	State  string `json:"state"`
}

func actionSetItemState(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var p itemStateActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: invalid params: %v", err)}
	}
	if p.ItemID <= 0 {
		return nil, &notetype.BadRequestError{Message: "rss: item_id is required"}
	}
	switch p.State {
	case "new", "read", "starred", "rejected":
	default:
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: invalid state %q", p.State)}
	}
	res, err := db.Exec(
		`UPDATE ct_rss_items SET state = ?
		 WHERE id = ? AND feed_id IN (SELECT id FROM ct_rss_feeds WHERE note_id = ?)`,
		p.State, p.ItemID, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: set item state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: item %d not found in note %d", p.ItemID, noteID)}
	}
	return map[string]any{"item_id": p.ItemID, "state": p.State}, nil
}

// itemForSave is the data needed to render an article as a note body.
type itemForSave struct {
	ID          int64
	FeedID      int64
	FeedName    string
	Title       string
	URL         string
	Author      string
	PublishedAt string
	Summary     string
	Category    string
	Quality     int
	Tags        string
	FullContent string
	FeedContent string
	FullStatus  string
	SavedNoteID int64

	Significance      float64
	Negativity        float64
	PersonalRelevance float64
	Evaluated         bool
}

// saveAsNoteBody renders the article as markdown for the created note.
func saveAsNoteBody(it *itemForSave) string {
	var b strings.Builder
	content := it.FullContent
	if content == "" {
		content = it.FeedContent
	}
	if content == "" && it.URL != "" {
		content = "_(no content available — open the original article)_"
	}

	fmt.Fprintf(&b, "# %s\n\n", it.Title)
	if it.URL != "" {
		fmt.Fprintf(&b, "[Open original](%s)\n\n", it.URL)
	}
	meta := make([]string, 0, 5)
	if it.FeedName != "" {
		meta = append(meta, fmt.Sprintf("**Feed:** %s", it.FeedName))
	}
	if it.Author != "" {
		meta = append(meta, fmt.Sprintf("**Author:** %s", it.Author))
	}
	if it.PublishedAt != "" {
		meta = append(meta, fmt.Sprintf("**Published:** %s", it.PublishedAt))
	}
	if it.Category != "" && it.Quality > 0 {
		meta = append(meta, fmt.Sprintf("**Classification:** %s (quality %d/10)", it.Category, it.Quality))
	} else if it.Category != "" {
		meta = append(meta, fmt.Sprintf("**Classification:** %s", it.Category))
	}
	if it.Evaluated {
		meta = append(meta, fmt.Sprintf("**Evaluation:** significance %.2f · negativity %.2f · personal relevance %.2f",
			it.Significance, it.Negativity, it.PersonalRelevance))
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " · "))
		b.WriteString("\n\n")
	}
	if it.Summary != "" {
		fmt.Fprintf(&b, "> %s\n\n", it.Summary)
	}
	if content != "" {
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}

// actionSaveAsNote creates a real standard-type child note under the reader
// holding the article, then queues embedding generation (_system/vss_index) —
// the same path the ingest pipelines use.
func (p *Plugin) actionSaveAsNote(db *sql.DB, noteID int64, params json.RawMessage) (any, error) {
	var pr struct {
		ItemID int64 `json:"item_id"`
	}
	if err := json.Unmarshal(params, &pr); err != nil {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: invalid params: %v", err)}
	}
	if pr.ItemID <= 0 {
		return nil, &notetype.BadRequestError{Message: "rss: item_id is required"}
	}

	it := &itemForSave{}
	var tagsJSON string
	var evaluated int
	err := db.QueryRow(`
		SELECT i.id, i.feed_id, COALESCE(NULLIF(f.name,''), f.url), i.title, i.url, i.author,
		       i.published_at, i.summary, i.category, i.quality, i.tags_json,
		       i.significance, i.negativity, i.personal_relevance, i.evaluated,
		       i.full_content, i.feed_content, i.full_status, i.saved_note_id
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE i.id = ? AND f.note_id = ?
	`, pr.ItemID, noteID).Scan(
		&it.ID, &it.FeedID, &it.FeedName, &it.Title, &it.URL, &it.Author,
		&it.PublishedAt, &it.Summary, &it.Category, &it.Quality, &tagsJSON,
		&it.Significance, &it.Negativity, &it.PersonalRelevance, &evaluated,
		&it.FullContent, &it.FeedContent, &it.FullStatus, &it.SavedNoteID,
	)
	if err == sql.ErrNoRows {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: item %d not found in note %d", pr.ItemID, noteID)}
	} else if err != nil {
		return nil, fmt.Errorf("rss: load item: %w", err)
	}
	it.Tags = tagsJSON
	it.Evaluated = evaluated != 0

	// Idempotent: already saved → point at the existing note.
	if it.SavedNoteID > 0 {
		return map[string]any{"note_id": it.SavedNoteID, "item_id": it.ID}, nil
	}

	body := saveAsNoteBody(it)
	if len(body) > defaultFeedLength {
		body = body[:defaultFeedLength]
	}
	title := it.Title
	if title == "" {
		title = "RSS article"
	}
	if len(title) > 300 {
		title = title[:300] + "…"
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("rss: begin save-as-note tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO notes (title, type, parent_id) VALUES (?, 'standard', ?)`,
		title, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: create note: %w", err)
	}
	newNoteID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("rss: note id: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO updates (note_id, body) VALUES (?, ?)`, newNoteID, body); err != nil {
		return nil, fmt.Errorf("rss: create note body: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE ct_rss_items SET saved_note_id = ?, state = 'read' WHERE id = ?`,
		newNoteID, it.ID,
	); err != nil {
		return nil, fmt.Errorf("rss: mark item saved: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("rss: commit save-as-note: %w", err)
	}

	// Queue embedding generation, the same job the note-creation HTTP path
	// uses. Failure to enqueue must not fail the whole action — the note
	// exists; embeddings catch up on the next index pass.
	if p.enqueue != nil {
		payload, _ := json.Marshal(map[string]any{"note_id": newNoteID})
		if _, err := p.enqueue("_system", "vss_index", payload); err != nil {
			log.Printf("rss: enqueue vss_index for note %d: %v", newNoteID, err)
		}
	}

	return map[string]any{"note_id": newNoteID, "item_id": it.ID}, nil
}

// actionReapplyRules re-reads the note-body rules and re-scores all of the
// reader's items (no LLM involved): rule penalties AND the derived points
// (a `weights:` edit changes the points of already-evaluated items).
func actionReapplyRules(db *sql.DB, noteID int64) (any, error) {
	body, err := noteBody(db, noteID)
	if err != nil {
		return nil, err
	}
	rules := parseRules(body)

	cfg, err := loadConfigRow(db, noteID)
	if err != nil {
		return nil, err
	}
	threshold := rules.effectiveThreshold(cfg.RejectThreshold)
	minPoints := rules.effectiveMinPoints()
	weights := rules.effectiveWeights()

	rows, err := db.Query(`
		SELECT i.id, i.title, COALESCE(NULLIF(i.full_content,''), i.feed_content, ''),
		       i.significance, i.negativity, i.personal_relevance,
		       i.affected_now_direct, i.affected_now_indirect,
		       i.affected_future_direct, i.affected_future_indirect,
		       i.evaluated
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ?`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: reapply: load items: %w", err)
	}
	type itemRow struct {
		id        int64
		title     string
		content   string
		metrics   evalMetrics
		evaluated bool
	}
	items := []itemRow{}
	for rows.Next() {
		var it itemRow
		if err := rows.Scan(&it.id, &it.title, &it.content,
			&it.metrics.Significance, &it.metrics.Negativity, &it.metrics.Relevance,
			&it.metrics.NowDirect, &it.metrics.NowIndirect,
			&it.metrics.FutureDirect, &it.metrics.FutureIndirect,
			&it.evaluated); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	applied, nowRejected := 0, 0
	for _, it := range items {
		res := applyRules(rules, it.title, it.content)
		points := computePoints(it.metrics, weights, res.Score)
		isRejected := res.Banned || (threshold > 0 && res.Score >= threshold) ||
			(minPoints > 0 && it.evaluated && points < minPoints)
		if _, err := db.Exec(
			`UPDATE ct_rss_items SET penalty_score = ?, penalty_json = ?, banned = ?, points = ? WHERE id = ?`,
			res.Score, penaltyJSONString(res.Entries), boolToInt(res.Banned), points, it.id,
		); err != nil {
			return nil, fmt.Errorf("rss: reapply: update item %d: %w", it.id, err)
		}
		applied++
		if isRejected {
			nowRejected++
		}
	}
	return map[string]any{
		"applied":    applied,
		"rejected":   nowRejected,
		"threshold":  threshold,
		"min_points": minPoints,
		"warnings":   rules.Warnings,
	}, nil
}

var _ notetype.ActionHandler = (*Plugin)(nil)
var _ notetype.JobEnqueuer = (*Plugin)(nil)
