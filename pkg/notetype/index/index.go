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

	internaltags "github.com/i5heu/MentisEterna/internal/tags"
	"github.com/i5heu/MentisEterna/pkg/notetype"
)

const pluginID = "index"

func init() {
	notetype.Register(&IndexPlugin{})
}

type IndexPlugin struct{}

func (p *IndexPlugin) ID() string { return pluginID }

func (p *IndexPlugin) InitSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS ct_index_config (
			note_id INTEGER NOT NULL,
			mode    TEXT    NOT NULL DEFAULT 'global',
			selected_tags_json TEXT NOT NULL DEFAULT '[]',
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE,
			PRIMARY KEY (note_id)
		);
	`)
	return err
}

// --- Payload types ---

// Payload is what the frontend sends / LoadConfig returns for the config.
type Payload struct {
	Mode         string   `json:"mode"`          // "global" or "local"
	SelectedTags []string `json:"selected_tags"` // empty = show all
}

// IndexEntry groups notes under a single tag.
type IndexEntry struct {
	Tag       string      `json:"tag"`
	Source    string      `json:"source"`
	Count     int         `json:"count"`
	UserCount int         `json:"user_count"`
	AutoCount int         `json:"auto_count"`
	Notes     []IndexNote `json:"notes"`
}

// IndexNote is a lightweight note reference used in index entries.
type IndexNote struct {
	NoteID     int64  `json:"note_id"`
	Title      string `json:"title"`
	ParentID   *int64 `json:"parent_id"`
	CreatedAt  string `json:"created_at"`
	Source     string `json:"source"`
	HasUserTag bool   `json:"has_user_tag"`
	HasAutoTag bool   `json:"has_auto_tag"`
}

// Response is what BuildView returns to the frontend.
type Response struct {
	Mode         string       `json:"mode"`
	SelectedTags []string     `json:"selected_tags"`
	Entries      []IndexEntry `json:"entries"`
}

func (p *IndexPlugin) CronJobs() []notetype.CronJob {
	return nil
}

func (p *IndexPlugin) Manifest() notetype.Manifest {
	return notetype.Manifest{
		ID:            "index",
		Label:         "Tag Index",
		Description:   "Shows notes grouped by tags",
		Category:      "Navigation",
		SortOrder:     300,
		DefaultConfig: json.RawMessage(`{"mode":"global","selected_tags":[]}`),
		Editor: notetype.EditorMeta{Mode: "custom", Schema: json.RawMessage(`[
	{
		"$el": "p",
		"children": "Shows notes grouped by tags. Configure mode to see tags globally or within the current branch."
	}
]`)},
		Viewer:     notetype.ViewerMeta{Mode: "custom"},
		HasConfig:  true,
		HasView:    true,
		HasActions: false,
	}
}

func (p *IndexPlugin) ValidateConfig(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var pl Payload
	if err := json.Unmarshal(payload, &pl); err != nil {
		return fmt.Errorf("index: invalid payload: %w", err)
	}
	if pl.Mode != "" && pl.Mode != "global" && pl.Mode != "local" {
		return fmt.Errorf("index: mode must be 'global' or 'local', got %q", pl.Mode)
	}
	return nil
}

func (p *IndexPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
	var payload Payload
	if len(config) > 0 {
		if err := json.Unmarshal(config, &payload); err != nil {
			return fmt.Errorf("index: unmarshal payload: %w", err)
		}
	}
	if payload.Mode == "" {
		payload.Mode = "global"
	}
	payload.SelectedTags = internaltags.NormalizeNames(payload.SelectedTags)

	tagsJSON, err := json.Marshal(payload.SelectedTags)
	if err != nil {
		return fmt.Errorf("index: marshal selected_tags: %w", err)
	}

	// Upsert: delete old row then insert new.
	if _, err := tx.Exec(`DELETE FROM ct_index_config WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("index: delete old config: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO ct_index_config (note_id, mode, selected_tags_json) VALUES (?, ?, ?)`,
		noteID, payload.Mode, string(tagsJSON),
	); err != nil {
		return fmt.Errorf("index: insert config: %w", err)
	}

	return nil
}

func (p *IndexPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
	var mode string
	var tagsJSON string
	err := db.QueryRow(
		`SELECT mode, selected_tags_json FROM ct_index_config WHERE note_id = ?`,
		noteID,
	).Scan(&mode, &tagsJSON)
	if err == sql.ErrNoRows {
		return json.RawMessage(`{"mode":"global","selected_tags":[]}`), nil
	} else if err != nil {
		return nil, fmt.Errorf("index: load config: %w", err)
	}

	var selectedTags []string
	if tagsJSON != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &selectedTags); err != nil {
			selectedTags = nil
		}
	}
	selectedTags = internaltags.NormalizeNames(selectedTags)

	cfg := Payload{Mode: mode, SelectedTags: selectedTags}
	return json.Marshal(cfg)
}

func (p *IndexPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
	// Load config.
	cfg, err := p.LoadConfig(ctx, db, userID, noteID)
	if err != nil {
		return nil, err
	}
	var payload Payload
	if err := json.Unmarshal(cfg, &payload); err != nil {
		return nil, fmt.Errorf("index: unmarshal own config: %w", err)
	}

	// Build the tag index.
	entries, err := buildIndex(db, noteID, payload)
	if err != nil {
		return nil, err
	}

	return &Response{
		Mode:         payload.Mode,
		SelectedTags: payload.SelectedTags,
		Entries:      entries,
	}, nil
}

// --- Index building ---

func buildIndex(db *sql.DB, noteID int64, cfg Payload) ([]IndexEntry, error) {
	var noteIDs []int64

	switch cfg.Mode {
	case "local":
		ids, err := localScopeIDs(db, noteID)
		if err != nil {
			return nil, err
		}
		noteIDs = ids
	default:
		noteIDs = nil // global — all notes
	}

	rows, err := queryTagIndex(db, cfg.SelectedTags, noteIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type row struct {
		tag        string
		noteID     int64
		title      string
		parentID   *int64
		createdAt  string
		hasUserTag bool
		hasAutoTag bool
	}

	entriesMap := make(map[string]*IndexEntry)
	var tagOrder []string

	for rows.Next() {
		var r row
		if err := rows.Scan(&r.tag, &r.noteID, &r.title, &r.parentID, &r.createdAt, &r.hasUserTag, &r.hasAutoTag); err != nil {
			return nil, fmt.Errorf("index: scan row: %w", err)
		}
		entry, exists := entriesMap[r.tag]
		if !exists {
			entry = &IndexEntry{Tag: r.tag}
			entriesMap[r.tag] = entry
			tagOrder = append(tagOrder, r.tag)
		}
		entry.Notes = append(entry.Notes, IndexNote{
			NoteID:     r.noteID,
			Title:      r.title,
			ParentID:   r.parentID,
			CreatedAt:  r.createdAt,
			Source:     classifyTagSource(r.hasUserTag, r.hasAutoTag),
			HasUserTag: r.hasUserTag,
			HasAutoTag: r.hasAutoTag,
		})
		entry.Count = len(entry.Notes)
		if r.hasUserTag {
			entry.UserCount++
		}
		if r.hasAutoTag {
			entry.AutoCount++
		}
		entry.Source = classifyTagSource(entry.UserCount > 0, entry.AutoCount > 0)
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
	var parentID *int64
	err := db.QueryRow(`SELECT parent_id FROM notes WHERE id = ?`, noteID).Scan(&parentID)
	if err != nil {
		return nil, fmt.Errorf("index: find parent of %d: %w", noteID, err)
	}

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

	allIDs := make([]int64, len(siblingIDs))
	copy(allIDs, siblingIDs)

	queue := make([]int64, len(siblingIDs))
	copy(queue, siblingIDs)

	for len(queue) > 0 {
		var children []int64
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

func queryTagIndex(db *sql.DB, selectedTags []string, noteIDs []int64) (*sql.Rows, error) {
	baseQuery := `
		SELECT t.name, n.id, n.title, n.parent_id,
		       COALESCE(
		         (SELECT u.created_at FROM updates u WHERE u.note_id = n.id ORDER BY u.id DESC LIMIT 1),
		         n.created_at
		       ) AS created_at,
		       MAX(CASE WHEN tr.source = 'user' THEN 1 ELSE 0 END) AS has_user_tag,
		       MAX(CASE WHEN tr.source = 'auto' THEN 1 ELSE 0 END) AS has_auto_tag
		FROM tags t
		JOIN (
			SELECT note_id, tag_id, 'user' AS source FROM tags_refs
			UNION ALL
			SELECT note_id, tag_id, 'auto' AS source FROM auto_tags_refs
		) tr ON tr.tag_id = t.id
		JOIN notes n ON n.id = tr.note_id
	`

	var conditions []string
	var args []any

	if len(selectedTags) > 0 {
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
	query += " GROUP BY t.name, n.id, n.title, n.parent_id, created_at"
	query += " ORDER BY t.name, n.title"

	return db.Query(query, args...)
}

func classifyTagSource(hasUser, hasAuto bool) string {
	switch {
	case hasUser && hasAuto:
		return "mixed"
	case hasUser:
		return "user"
	case hasAuto:
		return "auto"
	default:
		return "unknown"
	}
}

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
