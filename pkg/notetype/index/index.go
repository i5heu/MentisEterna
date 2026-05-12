// Package index implements an "index" note type that shows a configurable
// tag-based index of notes. It supports two modes:
//
//   - "global": all tags across all notes in the database.
//   - "local":  tags found within the note's immediate siblings+self (same
//     parent) and all their descendants.
//
// The user can optionally restrict the view to specific tags via selected_tags.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "index"

func init() {
	notetype.Register(&IndexPlugin{})
}

type IndexPlugin struct{}

func (p *IndexPlugin) ID() string { return pluginID }

// InitSchema — the index plugin doesn't need its own tables; it queries
// the tags / tags_refs / notes tables directly.
func (p *IndexPlugin) InitSchema(db *sql.DB) error {
	return nil
}

// --- Payload types ---

// Payload is what the frontend sends when saving the index configuration.
type Payload struct {
	Mode         string   `json:"mode"`          // "global" or "local"
	SelectedTags []string `json:"selected_tags"` // empty = show all
}

// IndexEntry groups notes under a single tag.
type IndexEntry struct {
	Tag   string      `json:"tag"`
	Count int         `json:"count"`
	Notes []IndexNote `json:"notes"`
}

// IndexNote is a lightweight note reference used in index entries.
type IndexNote struct {
	NoteID    int64  `json:"note_id"`
	Title     string `json:"title"`
	ParentID  *int64 `json:"parent_id"`
	CreatedAt string `json:"created_at"`
}

// Response is what ProcessLoad returns to the frontend.
type Response struct {
	Mode         string       `json:"mode"`
	SelectedTags []string     `json:"selected_tags"`
	Entries      []IndexEntry `json:"entries"`
}

// --- NoteType interface ---

func (p *IndexPlugin) Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("index: invalid payload: %w", err)
	}
	if payload.Mode != "" && payload.Mode != "global" && payload.Mode != "local" {
		return fmt.Errorf("index: mode must be 'global' or 'local', got %q", payload.Mode)
	}
	return nil
}

func (p *IndexPlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
	// No plugin tables — configuration is stored entirely in the note's
	// custom_data field, which the server persists automatically.
	return nil
}

func (p *IndexPlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// The index configuration is stored in the note body as JSON.
	// (There are no plugin tables for this type.)
	var body string
	err := db.QueryRow(`
		SELECT COALESCE(u.body, '') FROM updates u
		WHERE u.note_id = ?
		ORDER BY u.id DESC LIMIT 1
	`, noteID).Scan(&body)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("index: load body: %w", err)
	}

	var cfg Payload
	if body != "" {
		if err := json.Unmarshal([]byte(body), &cfg); err != nil {
			// Body isn't valid JSON config — return defaults.
			cfg = Payload{Mode: "global"}
		}
	}
	if cfg.Mode == "" {
		cfg.Mode = "global"
	}

	// Build the index.
	entries, err := buildIndex(db, noteID, cfg)
	if err != nil {
		return nil, err
	}

	return &Response{
		Mode:         cfg.Mode,
		SelectedTags: cfg.SelectedTags,
		Entries:      entries,
	}, nil
}

func (p *IndexPlugin) UISchema() json.RawMessage {
	return json.RawMessage(`[
	{
		"$el": "p",
		"children": "Shows notes grouped by tags. Configure mode to see tags globally or within the current branch."
	}
]`)
}

func (p *IndexPlugin) CronJobs() []notetype.CronJob {
	return nil
}

// --- Index building ---

func buildIndex(db *sql.DB, noteID int64, cfg Payload) ([]IndexEntry, error) {
	// Determine which note IDs to include.
	var noteIDs []int64

	switch cfg.Mode {
	case "local":
		ids, err := localScopeIDs(db, noteID)
		if err != nil {
			return nil, err
		}
		noteIDs = ids
	default:
		// "global" — all notes.
		noteIDs = nil
	}

	// Build the query.
	//
	// We want: for each tag (optionally filtered to selected_tags),
	// list the notes that use it, ordered by tag name then note title.
	//
	// If noteIDs is non-nil, restrict to those notes.

	rows, err := queryTagIndex(db, cfg.SelectedTags, noteIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group by tag.
	type row struct {
		tag       string
		noteID    int64
		title     string
		parentID  *int64
		createdAt string
	}

	entriesMap := make(map[string]*IndexEntry)
	var tagOrder []string

	for rows.Next() {
		var r row
		if err := rows.Scan(&r.tag, &r.noteID, &r.title, &r.parentID, &r.createdAt); err != nil {
			return nil, fmt.Errorf("index: scan row: %w", err)
		}
		entry, exists := entriesMap[r.tag]
		if !exists {
			entry = &IndexEntry{Tag: r.tag}
			entriesMap[r.tag] = entry
			tagOrder = append(tagOrder, r.tag)
		}
		entry.Notes = append(entry.Notes, IndexNote{
			NoteID:    r.noteID,
			Title:     r.title,
			ParentID:  r.parentID,
			CreatedAt: r.createdAt,
		})
		entry.Count = len(entry.Notes)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries := make([]IndexEntry, 0, len(tagOrder))
	for _, tag := range tagOrder {
		entries = append(entries, *entriesMap[tag])
	}
	return entries, nil
}

// localScopeIDs returns the IDs of the note itself, its siblings (notes
// sharing the same parent_id), and all descendants of those siblings.
func localScopeIDs(db *sql.DB, noteID int64) ([]int64, error) {
	// 1. Find the parent_id of this note.
	var parentID *int64
	err := db.QueryRow(`SELECT parent_id FROM notes WHERE id = ?`, noteID).Scan(&parentID)
	if err != nil {
		return nil, fmt.Errorf("index: find parent of %d: %w", noteID, err)
	}

	// 2. Get all siblings (including self) — notes with the same parent_id.
	var siblingIDs []int64
	var rows *sql.Rows
	if parentID == nil {
		rows, err = db.Query(`SELECT id FROM notes WHERE parent_id IS NULL`)
	} else {
		rows, err = db.Query(`SELECT id FROM notes WHERE parent_id = ?`, *parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("index: find siblings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		siblingIDs = append(siblingIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 3. Collect all descendants of each sibling via iterative BFS.
	allIDs := make([]int64, len(siblingIDs))
	copy(allIDs, siblingIDs)

	queue := make([]int64, len(siblingIDs))
	copy(queue, siblingIDs)

	for len(queue) > 0 {
		var children []int64
		// Query children of the current queue batch.
		childRows, err := db.Query(`
			SELECT id FROM notes WHERE parent_id IN (
				SELECT id FROM notes WHERE id IN (`+placeholders(len(queue))+`)
			)
		`, int64sToAnys(queue)...)
		if err != nil {
			return nil, fmt.Errorf("index: find children: %w", err)
		}
		for childRows.Next() {
			var cid int64
			if err := childRows.Scan(&cid); err != nil {
				childRows.Close()
				return nil, err
			}
			children = append(children, cid)
		}
		childRows.Close()

		if len(children) == 0 {
			break
		}
		allIDs = append(allIDs, children...)
		queue = children
	}

	return allIDs, nil
}

// queryTagIndex returns rows of (tag_name, note_id, note_title, parent_id, created_at)
// optionally filtered to specific tags and/or note IDs.
func queryTagIndex(db *sql.DB, selectedTags []string, noteIDs []int64) (*sql.Rows, error) {
	baseQuery := `
		SELECT t.name, n.id, n.title, n.parent_id,
		       COALESCE(
		         (SELECT u.created_at FROM updates u WHERE u.note_id = n.id ORDER BY u.id DESC LIMIT 1),
		         n.created_at
		       ) AS created_at
		FROM tags t
		JOIN tags_refs tr ON tr.tag_id = t.id
		JOIN notes n ON n.id = tr.note_id
	`

	var conditions []string
	var args []any

	if len(selectedTags) > 0 {
		// Build IN clause for tags.
		conditions = append(conditions, "t.name IN ("+placeholders(len(selectedTags))+")")
		for _, tag := range selectedTags {
			args = append(args, tag)
		}
	}

	if len(noteIDs) > 0 {
		conditions = append(conditions, "n.id IN ("+placeholders(len(noteIDs))+")")
		for _, id := range noteIDs {
			args = append(args, id)
		}
	}

	query := baseQuery
	if len(conditions) > 0 {
		query += " WHERE " + conditions[0]
		for i := 1; i < len(conditions); i++ {
			query += " AND " + conditions[i]
		}
	}
	query += " ORDER BY t.name, n.title"

	return db.Query(query, args...)
}

// --- Helpers ---

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, 2*n-1)
	b = append(b, '?')
	for i := 1; i < n; i++ {
		b = append(b, ',', '?')
	}
	return string(b)
}

func int64sToAnys(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
