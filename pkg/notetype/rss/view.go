package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

// View item limit constants.
const (
	viewItemsLimit    = 200
	viewRejectedLimit = 50
)

// FeedStatus is one feed of the reader in the view.
type FeedStatus struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Enabled     bool   `json:"enabled"`
	LastFetchAt string `json:"last_fetch_at"`
	LastStatus  string `json:"last_status"`
	LastError   string `json:"last_error"`
	NewCount    int    `json:"new_count"`
}

// ItemView is one article in the view.
type ItemView struct {
	ID             int64           `json:"id"`
	FeedID         int64           `json:"feed_id"`
	FeedName       string          `json:"feed_name"`
	Title          string          `json:"title"`
	URL            string          `json:"url"`
	Author         string          `json:"author"`
	PublishedAt    string          `json:"published_at"`
	Summary        string          `json:"summary"`
	Category       string          `json:"category"`
	Tags           []string        `json:"tags"`
	Quality        int             `json:"quality"`
	ClassifyStatus string          `json:"classify_status"`
	PenaltyScore   float64         `json:"penalty_score"`
	Penalty        json.RawMessage `json:"penalty"`
	Banned         bool            `json:"banned"`
	State          string          `json:"state"`
	FullStatus     string          `json:"full_status"`
	SavedNoteID    int64           `json:"saved_note_id"`
}

// ViewCounts holds the reader's light statistics.
type ViewCounts struct {
	Total           int `json:"total"`
	New             int `json:"new"`
	Read            int `json:"read"`
	Starred         int `json:"starred"`
	Rejected        int `json:"rejected"`
	PendingClassify int `json:"pending_classify"`
}

// View is the BuildView response for an rss note.
type View struct {
	Settings     Config       `json:"settings"`
	Feeds        []FeedStatus `json:"feeds"`
	Prompt       *PromptRef   `json:"prompt"`
	RulesWarning string       `json:"rules_warning"`
	Items        []ItemView   `json:"items"`
	Rejected     []ItemView   `json:"rejected"`
	Counts       ViewCounts   `json:"counts"`
}

// BuildView implements notetype.ViewBuilder.
func (p *Plugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	cfg, err := loadConfigRow(db, noteID)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	feeds, err := loadFeedStatuses(db, noteID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]int, len(feeds))
	for i, f := range feeds {
		byID[f.ID] = i
	}

	items, rejected, counts, err := loadItems(db, noteID, cfg.RejectThreshold, feeds, byID)
	if err != nil {
		return nil, err
	}

	prompt, err := resolvePromptNote(db, noteID)
	if err != nil {
		prompt = PromptRef{Default: true, Title: "Built-in default prompt", Warning: "scope resolution failed"}
	}

	return &View{
		Settings: cfg,
		Feeds:    feeds,
		Prompt:   &prompt,
		Items:    items,
		Rejected: rejected,
		Counts:   counts,
	}, nil
}

// loadFeedStatuses loads the reader's feeds with fetch status and new counts.
func loadFeedStatuses(db *sql.DB, noteID int64) ([]FeedStatus, error) {
	rows, err := db.Query(`
		SELECT f.id, COALESCE(NULLIF(f.name, ''), f.url), f.url, f.enabled,
		       COALESCE(f.last_fetch_at, ''), f.last_status, f.last_error,
		       (SELECT COUNT(*) FROM ct_rss_items i WHERE i.feed_id = f.id AND i.state = 'new')
		FROM ct_rss_feeds f WHERE f.note_id = ? ORDER BY f.id`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: view: load feeds: %w", err)
	}
	defer rows.Close()

	feeds := []FeedStatus{}
	for rows.Next() {
		var f FeedStatus
		var enabled int
		if err := rows.Scan(&f.ID, &f.Name, &f.URL, &enabled, &f.LastFetchAt, &f.LastStatus, &f.LastError, &f.NewCount); err != nil {
			return nil, fmt.Errorf("rss: view: scan feed: %w", err)
		}
		f.Enabled = enabled != 0
		feeds = append(feeds, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rss: view: iterate feeds: %w", err)
	}
	return feeds, nil
}

// loadItems loads non-rejected items, rejected items, and counts for a
// reader. Rejection is computed: banned OR penalty >= threshold OR
// state='rejected'.
func loadItems(db *sql.DB, noteID int64, threshold float64, feeds []FeedStatus, byID map[int64]int) ([]ItemView, []ItemView, ViewCounts, error) {
	var counts ViewCounts
	if len(feeds) == 0 {
		return []ItemView{}, []ItemView{}, counts, nil
	}

	items := make([]ItemView, 0, viewItemsLimit)
	rejected := make([]ItemView, 0, viewRejectedLimit)

	rows, err := db.Query(`
		SELECT i.id, i.feed_id, i.title, i.url, i.author, i.published_at,
		       i.category, i.summary, i.tags_json, i.quality, i.classify_status,
		       i.penalty_score, i.penalty_json, i.banned, i.state, i.full_status, i.saved_note_id
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ?
		ORDER BY COALESCE(NULLIF(i.published_at, ''), i.fetched_at) DESC, i.id DESC
	`, noteID)
	if err != nil {
		return nil, nil, counts, fmt.Errorf("rss: view: load items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it ItemView
		var tagsJSON, penaltyJSON string
		var banned int
		if err := rows.Scan(&it.ID, &it.FeedID, &it.Title, &it.URL, &it.Author, &it.PublishedAt,
			&it.Category, &it.Summary, &tagsJSON, &it.Quality, &it.ClassifyStatus,
			&it.PenaltyScore, &penaltyJSON, &banned, &it.State, &it.FullStatus, &it.SavedNoteID); err != nil {
			return nil, nil, counts, fmt.Errorf("rss: view: scan item: %w", err)
		}
		it.Tags = parseTagsJSON(tagsJSON)
		it.Penalty = json.RawMessage(penaltyJSON)
		it.Banned = banned != 0
		if idx, ok := byID[it.FeedID]; ok {
			it.FeedName = feeds[idx].Name
		}

		counts.Total++
		switch it.State {
		case "starred":
			counts.Starred++
		case "read":
			counts.Read++
		case "new":
			counts.New++
		}
		if it.ClassifyStatus == "pending" {
			counts.PendingClassify++
		}

		if it.Banned || it.State == "rejected" || it.PenaltyScore >= threshold {
			counts.Rejected++
			if len(rejected) < viewRejectedLimit {
				rejected = append(rejected, it)
			}
			continue
		}
		if len(items) < viewItemsLimit {
			items = append(items, it)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, counts, fmt.Errorf("rss: view: iterate items: %w", err)
	}
	return items, rejected, counts, nil
}

// parseTagsJSON decodes an item's tags_json column ([]string), tolerating
// empty or malformed values.
func parseTagsJSON(raw string) []string {
	tags := []string{}
	if raw == "" {
		return tags
	}
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	return tags
}

var _ notetype.ViewBuilder = (*Plugin)(nil)
