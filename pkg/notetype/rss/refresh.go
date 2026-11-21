package rss

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	refreshTimeout       = 10 * time.Minute
	feedFetchWorkers     = 4
	fullContentMinRunes  = 500
	keepFreshFeedSeconds = 0
	classifyBatchSize    = 25
)

// refreshNote refreshes all enabled feeds of a reader note: fetch (parallel),
// parse, dedup-insert items, resolve full content, classify pending items,
// and sweep retention. force=false honors per-feed interval freshness.
func refreshNote(db *sql.DB, noteID int64, force bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	cfg, err := loadConfigRow(db, noteID)
	if err != nil {
		return "", err
	}

	// Rules live in the reader note's own body — re-read every refresh.
	rulesBody, err := noteBody(db, noteID)
	if err != nil {
		return "", err
	}
	rules := parseRules(rulesBody)

	feeds, err := loadFeedRows(db, noteID)
	if err != nil {
		return "", err
	}
	if len(feeds) == 0 {
		return "no enabled feeds", nil
	}

	outcomes := refreshFeedsParallel(ctx, feeds, cfg, force)

	var feedsOK, feedsUnchanged, feedsFresh, feedsError int
	var newItems, fullFetched, rejectedCount int
	now := nowStr()
	for _, oc := range outcomes {
		switch {
		case oc.errStr != "":
			feedsError++
			if _, err := db.Exec(
				`UPDATE ct_rss_feeds SET last_fetch_at = ?, last_status = 'error', last_error = ? WHERE id = ?`,
				now, oc.errStr, oc.feed.ID,
			); err != nil {
				return "", fmt.Errorf("rss: record feed error: %w", err)
			}
			continue
		case oc.fresh:
			feedsFresh++
			continue
		case oc.notModified:
			feedsUnchanged++
			if _, err := db.Exec(
				`UPDATE ct_rss_feeds SET last_fetch_at = ?, last_status = 'not_modified', last_error = '' WHERE id = ?`,
				now, oc.feed.ID,
			); err != nil {
				return "", fmt.Errorf("rss: record feed 304: %w", err)
			}
			continue
		}

		feedsOK++
		inserted, rejectedByRules, err := insertFeedItems(db, oc, cfg, rules)
		if err != nil {
			return "", err
		}
		newItems += inserted
		rejectedCount += rejectedByRules
		fullFetched += oc.fetched

		feedName := oc.feedTitle
		var nameUpdated int64
		if oc.feed.Name == "" && feedName != "" {
			res, err := db.Exec(`UPDATE ct_rss_feeds SET name = ? WHERE id = ? AND name = ''`, feedName, oc.feed.ID)
			if err != nil {
				return "", fmt.Errorf("rss: fill feed name: %w", err)
			}
			nameUpdated, _ = res.RowsAffected()
		}
		_ = nameUpdated

		if _, err := db.Exec(
			`UPDATE ct_rss_feeds SET etag = ?, last_modified = ?, last_fetch_at = ?,
			        last_status = 'ok', last_error = '' WHERE id = ?`,
			oc.etag, oc.lastModified, now, oc.feed.ID,
		); err != nil {
			return "", fmt.Errorf("rss: record feed success: %w", err)
		}
	}

	classified := 0
	if cfg.AutoClassify {
		classified = classifyPendingItems(db, noteID, cfg)
	}

	swept, err := runRetention(db, noteID, cfg)
	if err != nil {
		return "", err
	}

	summary := fmt.Sprintf(
		"%d ok, %d unchanged, %d fresh-skipped, %d error(s); +%d new (%d full-text), %d classified, %d rejected",
		feedsOK, feedsUnchanged, feedsFresh, feedsError, newItems, fullFetched, classified, rejectedCount,
	)
	if swept > 0 {
		summary += fmt.Sprintf(", %d swept", swept)
	}
	return summary, nil
}

// --- per-feed fetch (parallel) ---

type feedRow struct {
	ID           int64
	URL          string
	Name         string
	Etag         string
	LastModified string
	LastFetchAt  string // "" when never fetched
}

type preparedItem struct {
	Key         string
	Title       string
	Link        string
	Author      string
	PublishedAt string
	FeedContent string
	FullContent string
	FullStatus  string
}

type feedOutcome struct {
	feed         feedRow
	errStr       string
	fresh        bool // skipped: fetched recently and not forced
	notModified  bool
	etag         string
	lastModified string
	feedTitle    string
	items        []preparedItem
	fetched      int // items whose full content was resolved
}

func loadFeedRows(db *sql.DB, noteID int64) ([]feedRow, error) {
	rows, err := db.Query(
		`SELECT id, url, COALESCE(name,''), COALESCE(etag,''), COALESCE(last_modified,''), COALESCE(last_fetch_at,'')
		 FROM ct_rss_feeds WHERE note_id = ? AND enabled = 1 ORDER BY id`, noteID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: load feeds: %w", err)
	}
	defer rows.Close()

	var feeds []feedRow
	for rows.Next() {
		var f feedRow
		if err := rows.Scan(&f.ID, &f.URL, &f.Name, &f.Etag, &f.LastModified, &f.LastFetchAt); err != nil {
			return nil, fmt.Errorf("rss: scan feed: %w", err)
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

// refreshFeedsParallel fetches feeds concurrently (bounded by
// feedFetchWorkers) and returns outcomes in arbitrary order. DB writes are
// not performed here — SQLite serializes writes and callers apply them
// sequentially to avoid busy contention.
func refreshFeedsParallel(ctx context.Context, feeds []feedRow, cfg Config, force bool) []feedOutcome {
	ch := make(chan feedOutcome, len(feeds))
	sem := make(chan struct{}, feedFetchWorkers)
	var wg sync.WaitGroup
	for _, f := range feeds {
		wg.Add(1)
		go func(f feedRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ch <- fetchFeedOutcome(ctx, f, cfg, force)
		}(f)
	}
	go func() { wg.Wait(); close(ch) }()

	outcomes := make([]feedOutcome, 0, len(feeds))
	for oc := range ch {
		outcomes = append(outcomes, oc)
	}
	return outcomes
}

// fetchFeedOutcome fetches and parses one feed and prepares item rows,
// including full-content resolution for short/missing article bodies.
func fetchFeedOutcome(ctx context.Context, f feedRow, cfg Config, force bool) feedOutcome {
	oc := feedOutcome{feed: f}

	// Freshness check (per-feed interval; skipped entirely when forced).
	if !force && f.LastFetchAt != "" {
		if t := parseDBTime(f.LastFetchAt); !t.IsZero() {
			if time.Since(t) < time.Duration(cfg.IntervalMinutes)*time.Minute {
				oc.fresh = true
				return oc
			}
		}
	}

	res, err := fetchURL(ctx, f.URL, f.Etag, f.LastModified, acceptFeed)
	if err != nil {
		oc.errStr = err.Error()
		return oc
	}
	if res.NotModified {
		oc.notModified = true
		return oc
	}

	doc, err := ParseFeed(res.Body)
	if err != nil {
		oc.errStr = fmt.Sprintf("feed parse: %v", err)
		return oc
	}
	oc.feedTitle = strings.TrimSpace(doc.Title)
	oc.etag = res.Etag
	oc.lastModified = res.LastModified

	for _, item := range doc.Items {
		p, fetched := prepareItem(ctx, item, cfg)
		oc.items = append(oc.items, p)
		oc.fetched += fetched
	}
	return oc
}

// prepareItem converts a parsed feed item into a prepared row, resolving
// full content when the feed content is missing or very short.
func prepareItem(ctx context.Context, item FeedItem, cfg Config) (preparedItem, int) {
	p := preparedItem{
		Key:         item.ItemKey(),
		Title:       clipRunes(strings.TrimSpace(item.Title), 1000),
		Link:        strings.TrimSpace(item.Link),
		Author:      clipRunes(strings.TrimSpace(item.Author), 500),
		PublishedAt: item.PublishedAt,
		FeedContent: clipRunes(item.Content, defaultFeedLength),
		FullStatus:  "none",
	}
	fetched := 0
	short := utf8.RuneCountInString(p.FeedContent) < fullContentMinRunes
	if cfg.FetchFullContent && short && p.Link != "" {
		pres, err := fetchURL(ctx, p.Link, "", "", acceptHTML)
		switch {
		case err != nil:
			p.FullStatus = "failed"
		default:
			if text, ok := extractArticle(pres.Body); ok && strings.TrimSpace(text) != "" {
				p.FullContent = text
				p.FullStatus = "fetched"
				fetched = 1
			} else {
				p.FullStatus = "failed"
			}
		}
	}
	return p, fetched
}

// insertFeedItems inserts the outcome's new items (dedup via
// feed_id+item_key conflict target), applies the note-body rules to newly
// inserted rows, and returns (inserted, rejectedByRules).
func insertFeedItems(db *sql.DB, oc feedOutcome, cfg Config, rules RulesDoc) (int, int, error) {
	now := nowStr()
	inserted := 0
	rejected := 0
	threshold := rules.effectiveThreshold(cfg.RejectThreshold)
	for _, it := range oc.items {
		classifyStatus := "pending"
		if !cfg.AutoClassify {
			classifyStatus = "disabled"
		}
		res, err := db.Exec(`
			INSERT INTO ct_rss_items
				(feed_id, item_key, title, url, author, published_at, fetched_at,
				 feed_content, full_content, full_status, state, classify_status, penalty_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'new', ?, '[]')
			ON CONFLICT(feed_id, item_key) DO NOTHING`,
			oc.feed.ID, it.Key, it.Title, it.Link, it.Author, it.PublishedAt, now,
			it.FeedContent, it.FullContent, it.FullStatus, classifyStatus,
		)
		if err != nil {
			return inserted, rejected, fmt.Errorf("rss: insert item %q: %w", it.Key, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		inserted++
		itemID, _ := res.LastInsertId()

		ruleRes := applyRules(rules, it.Title, pickContent(it))
		isRejected := ruleRes.Banned || (threshold > 0 && ruleRes.Score >= threshold)
		if isRejected {
			rejected++
		}
		if _, err := db.Exec(
			`UPDATE ct_rss_items SET penalty_score = ?, penalty_json = ?, banned = ? WHERE id = ?`,
			ruleRes.Score, penaltyJSONString(ruleRes.Entries), boolToInt(ruleRes.Banned), itemID,
		); err != nil {
			return inserted, rejected, fmt.Errorf("rss: score item %q: %w", it.Key, err)
		}
	}
	return inserted, rejected, nil
}

// pickContent returns the best available content for scoring.
func pickContent(it preparedItem) string {
	if it.FullContent != "" {
		return it.FullContent
	}
	return it.FeedContent
}

// --- retention ---

// runRetention deletes expired items and trims per-feed overflow. Starred
// and saved-note items are always kept.
func runRetention(db *sql.DB, noteID int64, cfg Config) (int, error) {
	feedIDs, err := noteFeedIDs(db, noteID)
	if err != nil {
		return 0, err
	}
	rows := 0
	now := time.Now().UTC()
	for _, feedID := range feedIDs {
		cutoff := now.AddDate(0, 0, -cfg.RetentionDays).Format(timeFormat)
		res, err := db.Exec(
			`DELETE FROM ct_rss_items
			 WHERE feed_id = ? AND state != 'starred' AND saved_note_id = 0
			   AND fetched_at != '' AND fetched_at < ?`,
			feedID, cutoff,
		)
		if err != nil {
			return rows, fmt.Errorf("rss: retention delete feed %d: %w", feedID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rows += int(n)
		}

		res, err = db.Exec(
			`DELETE FROM ct_rss_items
			 WHERE feed_id = ? AND state != 'starred' AND saved_note_id = 0
			   AND id NOT IN (
			       SELECT id FROM ct_rss_items WHERE feed_id = ?
			       ORDER BY COALESCE(NULLIF(published_at,''), fetched_at) DESC, id DESC
			       LIMIT ?
			   )`,
			feedID, feedID, cfg.MaxItemsPerFeed,
		)
		if err != nil {
			return rows, fmt.Errorf("rss: retention trim feed %d: %w", feedID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			rows += int(n)
		}
	}
	return rows, nil
}

func noteFeedIDs(db *sql.DB, noteID int64) ([]int64, error) {
	rows, err := db.Query(`SELECT id FROM ct_rss_feeds WHERE note_id = ?`, noteID)
	if err != nil {
		return nil, fmt.Errorf("rss: list note feeds: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- helpers ---

// nowStr returns the current UTC timestamp in DB format.
func nowStr() string { return time.Now().UTC().Format(timeFormat) }

// parseDBTime parses a stored timestamp (with or without millis).
func parseDBTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{timeFormat, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
