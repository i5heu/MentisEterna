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

// actionReclassify synchronously re-runs classification for one article
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
		       COALESCE(NULLIF(f.name,''), '')
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE i.id = ? AND f.note_id = ?
	`, pr.ItemID, noteID).Scan(&it.ID, &it.Title, &it.URL, &it.Content, &feedName)
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
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: classification failed: %v", err)}
	}
	if err := storeClassification(db, it.ID, promptRef.NoteID, classification); err != nil {
		return nil, err
	}
	return map[string]any{
		"item_id":  it.ID,
		"category": classification.Category,
		"summary":  classification.Summary,
		"quality":  classification.Quality,
		"tags":     classification.Tags,
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
	case "reclassify":
		return p.actionReclassify(ctx, db, noteID, params)
	case "reapply_rules":
		return nil, &notetype.BadRequestError{Message: "rss: rules are not implemented yet"}
	default:
		return nil, notetype.ErrUnknownAction
	}
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
	meta := make([]string, 0, 4)
	if it.FeedName != "" {
		meta = append(meta, fmt.Sprintf("**Feed:** %s", it.FeedName))
	}
	if it.Author != "" {
		meta = append(meta, fmt.Sprintf("**Author:** %s", it.Author))
	}
	if it.PublishedAt != "" {
		meta = append(meta, fmt.Sprintf("**Published:** %s", it.PublishedAt))
	}
	if it.Category != "" {
		meta = append(meta, fmt.Sprintf("**Classification:** %s (quality %d/10)", it.Category, it.Quality))
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
	err := db.QueryRow(`
		SELECT i.id, i.feed_id, COALESCE(NULLIF(f.name,''), f.url), i.title, i.url, i.author,
		       i.published_at, i.summary, i.category, i.quality, i.tags_json,
		       i.full_content, i.feed_content, i.full_status, i.saved_note_id
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE i.id = ? AND f.note_id = ?
	`, pr.ItemID, noteID).Scan(
		&it.ID, &it.FeedID, &it.FeedName, &it.Title, &it.URL, &it.Author,
		&it.PublishedAt, &it.Summary, &it.Category, &it.Quality, &tagsJSON,
		&it.FullContent, &it.FeedContent, &it.FullStatus, &it.SavedNoteID,
	)
	if err == sql.ErrNoRows {
		return nil, &notetype.BadRequestError{Message: fmt.Sprintf("rss: item %d not found in note %d", pr.ItemID, noteID)}
	} else if err != nil {
		return nil, fmt.Errorf("rss: load item: %w", err)
	}
	it.Tags = tagsJSON

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
// reader's items (no LLM involved).
func actionReapplyRules(db *sql.DB, noteID int64) (any, error) {
	body, err := noteBody(db, noteID)
	if err != nil {
		return nil, err
	}
	rules := parseRules(body)
	threshold := rules.effectiveThreshold(0) // filled below from config

	cfg, err := loadConfigRow(db, noteID)
	if err != nil {
		return nil, err
	}
	threshold = rules.effectiveThreshold(cfg.RejectThreshold)

	rows, err := db.Query(`
		SELECT i.id, i.title, COALESCE(NULLIF(i.full_content,''), i.feed_content, '')
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ?`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: reapply: load items: %w", err)
	}
	type itemRow struct {
		id      int64
		title   string
		content string
	}
	items := []itemRow{}
	for rows.Next() {
		var it itemRow
		if err := rows.Scan(&it.id, &it.title, &it.content); err != nil {
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
		isRejected := res.Banned || (threshold > 0 && res.Score >= threshold)
		if _, err := db.Exec(
			`UPDATE ct_rss_items SET penalty_score = ?, penalty_json = ?, banned = ? WHERE id = ?`,
			res.Score, penaltyJSONString(res.Entries), boolToInt(res.Banned), it.id,
		); err != nil {
			return nil, fmt.Errorf("rss: reapply: update item %d: %w", it.id, err)
		}
		applied++
		if isRejected {
			nowRejected++
		}
	}
	return map[string]any{
		"applied":   applied,
		"rejected":  nowRejected,
		"threshold": threshold,
		"warnings":  rules.Warnings,
	}, nil
}

var _ notetype.ActionHandler = (*Plugin)(nil)
var _ notetype.JobEnqueuer = (*Plugin)(nil)
