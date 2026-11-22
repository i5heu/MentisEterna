package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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
	ID                     int64              `json:"id"`
	FeedID                 int64              `json:"feed_id"`
	FeedName               string             `json:"feed_name"`
	Title                  string             `json:"title"`
	URL                    string             `json:"url"`
	Author                 string             `json:"author"`
	PublishedAt            string             `json:"published_at"`
	Summary                string             `json:"summary"`
	Category               string             `json:"category"`
	Tags                   []string           `json:"tags"`
	Quality                int                `json:"quality"`
	Significance           float64            `json:"significance"`
	Negativity             float64            `json:"negativity"`
	PersonalRelevance      float64            `json:"personal_relevance"`
	AffectedNowDirect      float64            `json:"affected_now_direct"`
	AffectedNowIndirect    float64            `json:"affected_now_indirect"`
	AffectedFutureDirect   float64            `json:"affected_future_direct"`
	AffectedFutureIndirect float64            `json:"affected_future_indirect"`
	Points                 float64            `json:"points"`
	Evaluated              bool               `json:"evaluated"`
	StructuredSummary      *StructuredSummary `json:"structured_summary,omitempty"`
	SummaryStatus          string             `json:"summary_status"`
	ClassifyStatus         string             `json:"classify_status"`
	PenaltyScore           float64            `json:"penalty_score"`
	Penalty                json.RawMessage    `json:"penalty"`
	Banned                 bool               `json:"banned"`
	State                  string             `json:"state"`
	FullStatus             string             `json:"full_status"`
	SavedNoteID            int64              `json:"saved_note_id"`
}

// ViewCounts holds the reader's light statistics.
type ViewCounts struct {
	Total            int `json:"total"`
	New              int `json:"new"`
	Read             int `json:"read"`
	Starred          int `json:"starred"`
	Rejected         int `json:"rejected"`
	PendingClassify  int `json:"pending_classify"`
	Evaluated        int `json:"evaluated"`         // classification carried an evaluation
	Unevaluated      int `json:"unevaluated"`       // done/pending/error rows without evaluation
	PendingSummarize int `json:"pending_summarize"` // awaiting the summarizer drain
	Summarized       int `json:"summarized"`        // summary_json landed (status done)
	Unsummarized     int `json:"unsummarized"`      // missing a summary (legacy ''/pending/error rows)
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

	// Rules live in the note body; re-parse live so body edits surface
	// immediately and the effective thresholds reflect overrides.
	rulesWarning := ""
	threshold := cfg.RejectThreshold
	minPoints := defaultMinPoints
	rulesBody, err := noteBody(db, noteID)
	if err == nil {
		rules := parseRules(rulesBody)
		threshold = rules.effectiveThreshold(cfg.RejectThreshold)
		minPoints = rules.effectiveMinPoints()
		if len(rules.Warnings) > 0 {
			rulesWarning = strings.Join(rules.Warnings, "; ")
		}
	}

	items, rejected, counts, err := loadItems(db, noteID, threshold, minPoints, feeds, byID)
	if err != nil {
		return nil, err
	}

	prompt, err := resolvePromptNote(db, noteID)
	if err != nil {
		prompt = PromptRef{Default: true, Title: "Built-in default prompt", Warning: "scope resolution failed"}
	}

	return &View{
		Settings:     cfg,
		Feeds:        feeds,
		Prompt:       &prompt,
		RulesWarning: rulesWarning,
		Items:        items,
		Rejected:     rejected,
		Counts:       counts,
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
// reader. Rejection is computed: banned OR penalty >= threshold (0 disables
// the penalty check) OR state='rejected' OR evaluated item below min_points
// (0 disables the points check).
func loadItems(db *sql.DB, noteID int64, threshold, minPoints float64, feeds []FeedStatus, byID map[int64]int) ([]ItemView, []ItemView, ViewCounts, error) {
	var counts ViewCounts
	if len(feeds) == 0 {
		return []ItemView{}, []ItemView{}, counts, nil
	}

	items := make([]ItemView, 0, viewItemsLimit)
	rejected := make([]ItemView, 0, viewRejectedLimit)

	rows, err := db.Query(`
		SELECT i.id, i.feed_id, i.title, i.url, i.author, i.published_at,
		       i.category, i.summary, i.tags_json, i.quality, i.classify_status,
		       i.significance, i.negativity, i.personal_relevance,
		       i.affected_now_direct, i.affected_now_indirect,
		       i.affected_future_direct, i.affected_future_indirect,
		       i.points, i.evaluated,
		       i.summary_json, i.summary_status,
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
		var tagsJSON, penaltyJSON, summaryJSON string
		var banned, evaluated int
		if err := rows.Scan(&it.ID, &it.FeedID, &it.Title, &it.URL, &it.Author, &it.PublishedAt,
			&it.Category, &it.Summary, &tagsJSON, &it.Quality, &it.ClassifyStatus,
			&it.Significance, &it.Negativity, &it.PersonalRelevance,
			&it.AffectedNowDirect, &it.AffectedNowIndirect,
			&it.AffectedFutureDirect, &it.AffectedFutureIndirect,
			&it.Points, &evaluated,
			&summaryJSON, &it.SummaryStatus,
			&it.PenaltyScore, &penaltyJSON, &banned, &it.State, &it.FullStatus, &it.SavedNoteID); err != nil {
			return nil, nil, counts, fmt.Errorf("rss: view: scan item: %w", err)
		}
		it.Tags = parseTagsJSON(tagsJSON)
		it.Penalty = json.RawMessage(penaltyJSON)
		it.Banned = banned != 0
		it.Evaluated = evaluated != 0
		it.StructuredSummary = parseSummaryJSON(summaryJSON)
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
		if it.Evaluated {
			counts.Evaluated++
		} else if it.ClassifyStatus != "disabled" {
			// Missing evaluation: legacy keyword-classified rows, errored
			// runs, items still waiting for their first evaluation — the
			// "Evaluate missing" action targets exactly these.
			counts.Unevaluated++
		}
		switch {
		case it.SummaryStatus == "done":
			counts.Summarized++
		case it.SummaryStatus == "pending":
			counts.PendingSummarize++
			counts.Unsummarized++
		case it.SummaryStatus == "disabled":
			// auto_classify is off: summarization opted out entirely
		default:
			// '' (rows predating the summarizer) or errored runs — the
			// "Summarize missing" action targets exactly these.
			counts.Unsummarized++
		}

		if rejectedItem(it, threshold, minPoints) {
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

// rejectedItem reports whether an item lands in the rejected bucket:
// banned by a rule, manually rejected, over the penalty threshold, or an
// evaluated item below the min_points floor. Threshold/min_points of 0
// disable their respective checks (a zero threshold must not reject
// zero-penalty items).
func rejectedItem(it ItemView, threshold, minPoints float64) bool {
	if it.Banned || it.State == "rejected" {
		return true
	}
	if threshold > 0 && it.PenaltyScore >= threshold {
		return true
	}
	if minPoints > 0 && it.Evaluated && it.Points < minPoints {
		return true
	}
	return false
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
