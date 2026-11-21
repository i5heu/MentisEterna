package llminferencer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
)

// ResolvedChat describes how one usage call was resolved. It is safe to
// serialize: the API key never appears here (the client carries it).
type ResolvedChat struct {
	Usage            string `json:"usage"`
	Source           string `json:"source"` // "note" (llm_inferencer note) | "config" (config.toml feature/tier system)
	InferencerNoteID int64  `json:"inferencer_note_id,omitempty"`
	Provider         string `json:"provider,omitempty"` // provider of the binding note
	BaseURL          string `json:"base_url,omitempty"`
	Model            string `json:"model"`
	Tier             string `json:"tier,omitempty"` // tier the usage bound to (note source)
	Warning          string `json:"warning,omitempty"`
}

// Source values for ResolvedChat.Source.
const (
	SourceNote   = "note"
	SourceConfig = "config"
)

// inferencerRow is the server-side view of one inferencer note's persisted
// config — including the stored API key (never leaves this package as data).
type inferencerRow struct {
	NoteID      int64
	Provider    string
	BaseURL     string
	Key         string
	DefaultTier string
	Usages      map[string]UsageBinding
	Tiers       map[string]TierBinding
}

// binding is the derived effective binding for one usage on one note.
type binding struct {
	Model string
	Tier  string
	// Explicit reports an explicit usage binding (vs. the default tier).
	Explicit bool
}

// bindsUsage derives the effective binding for a usage on this note, or nil
// when the note does not yield a concrete model. Rules:
//
//   - explicit usage binding with a direct model → binds,
//   - explicit usage binding with a tier → binds only when that tier has a
//     model (an explicit binding to an empty tier is a config wound that
//     must NOT silently capture the usage),
//   - no explicit binding → default_tier binds when it has a model,
//   - anything else → no binding.
func (r *inferencerRow) bindsUsage(usage string) *binding {
	if ub, ok := r.Usages[usage]; ok {
		if model := strings.TrimSpace(ub.Model); model != "" {
			return &binding{Model: model, Explicit: true}
		}
		if validTier(ub.Tier) {
			if tb, ok := r.Tiers[ub.Tier]; ok && strings.TrimSpace(tb.Model) != "" {
				return &binding{Model: strings.TrimSpace(tb.Model), Tier: ub.Tier, Explicit: true}
			}
			return nil // explicit binding to a tier without a model: incomplete
		}
		return nil
	}
	if validTier(r.DefaultTier) {
		if tb, ok := r.Tiers[r.DefaultTier]; ok && strings.TrimSpace(tb.Model) != "" {
			return &binding{Model: strings.TrimSpace(tb.Model), Tier: r.DefaultTier}
		}
	}
	return nil
}

// loadInferencerRow loads one inferencer note's config (including the stored
// API key — server-side only). ok=false when the note has no config row.
func loadInferencerRow(db *sql.DB, noteID int64) (*inferencerRow, bool, error) {
	row := &inferencerRow{
		NoteID: noteID,
		Usages: map[string]UsageBinding{},
		Tiers:  map[string]TierBinding{},
	}
	var usagesJSON string
	err := db.QueryRow(
		`SELECT provider, base_url, api_key, default_tier, usages_json
		 FROM ct_llm_inferencer_config WHERE note_id = ?`, noteID,
	).Scan(&row.Provider, &row.BaseURL, &row.Key, &row.DefaultTier, &usagesJSON)
	if err == sql.ErrNoRows {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("llm_inferencer: load config %d: %w", noteID, err)
	}
	if usagesJSON != "" {
		if err := json.Unmarshal([]byte(usagesJSON), &row.Usages); err != nil {
			return nil, false, fmt.Errorf("llm_inferencer: usages of %d: %w", noteID, err)
		}
	}

	rows, err := db.Query(`SELECT tier, model, base_url FROM ct_llm_inferencer_tiers WHERE note_id = ?`, noteID)
	if err != nil {
		return nil, false, fmt.Errorf("llm_inferencer: tiers of %d: %w", noteID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier, model, baseURL string
		if err := rows.Scan(&tier, &model, &baseURL); err != nil {
			return nil, false, fmt.Errorf("llm_inferencer: scan tier of %d: %w", noteID, err)
		}
		row.Tiers[tier] = TierBinding{Model: model, BaseURL: baseURL}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("llm_inferencer: iterate tiers of %d: %w", noteID, err)
	}
	return row, true, nil
}

// effectiveBaseURL resolves the endpoint for a binding: per-tier override >
// note base_url > provider default.
func (r *inferencerRow) effectiveBaseURL(tier string) (string, error) {
	if validTier(tier) {
		if tb, ok := r.Tiers[tier]; ok && strings.TrimSpace(tb.BaseURL) != "" {
			return strings.TrimSpace(tb.BaseURL), nil
		}
	}
	if strings.TrimSpace(r.BaseURL) != "" {
		return strings.TrimSpace(r.BaseURL), nil
	}
	if def, ok := providerDefaultBaseURL[r.Provider]; ok && def != "" {
		return def, nil
	}
	return "", fmt.Errorf("llm_inferencer: note %d: no base URL (provider %q has no default)", r.NoteID, r.Provider)
}

// ResolveChat resolves the chat client for a usage anchored at a note:
//
//  1. the anchor's own subtree (nearest descendant wins), then
//  2. each ancestor's children (siblings, then uncles) up to the root —
//     the same nearest-wins walk rss_prompt notes use — restricted to
//     llm_inferencer notes that yield a concrete model for the usage
//     (explicit binding or default tier with a model),
//  3. no binding anywhere → the config.toml feature/tier system client
//     (Source "config").
//
// It always returns a usable client (or an error on infra failures); the
// API key stays inside the client and is never exposed in ResolvedChat.
//
// The anchor note itself is not a candidate (mirroring rss_prompt); pass the
// consuming note (e.g. the rss reader) — its children, siblings, and
// ancestors' children are searched.
func ResolveChat(db *sql.DB, fromNoteID int64, usage string) (*llm.ChatClient, ResolvedChat, error) {
	res := ResolvedChat{Usage: usage, Source: SourceConfig}

	if db != nil {
		row, warn, err := findBinding(db, fromNoteID, usage)
		if err != nil {
			return nil, res, err
		}
		if row != nil {
			b := row.bindsUsage(usage)
			baseURL, err := row.effectiveBaseURL(b.Tier)
			if err != nil {
				return nil, res, err
			}
			client := llm.NewChatClientWithKey(baseURL, b.Model, row.Key)
			res = ResolvedChat{
				Usage:            usage,
				Source:           SourceNote,
				InferencerNoteID: row.NoteID,
				Provider:         row.Provider,
				BaseURL:          baseURL,
				Model:            b.Model,
				Tier:             b.Tier,
				Warning:          warn,
			}
			return client, res, nil
		}
	}

	// Fallback: config.toml feature/tier system.
	client := llm.NewFeatureChatClient(usage)
	if client == nil {
		return nil, res, fmt.Errorf("llm_inferencer: unknown usage %q", usage)
	}
	res.Model = client.Model
	res.BaseURL = client.BaseURL
	return client, res, nil
}

// findBinding walks the hierarchy for the nearest inferencer note yielding a
// concrete model for the usage, mirroring the rss_prompt walk plus a final
// top-level scope. Returns the winning row, or nil when nothing binds.
func findBinding(db *sql.DB, noteID int64, usage string) (*inferencerRow, string, error) {
	// 1. Anchor's own subtree: BFS by depth.
	if found, err := nearestInSubtree(db, noteID, usage); err != nil {
		return nil, "", err
	} else if found != nil {
		return found.row, found.warning, nil
	}

	// 2. Ancestors' children (siblings, then uncles), closest first.
	ancestors, err := ancestorChain(db, noteID)
	if err != nil {
		return nil, "", err
	}
	for _, ancestor := range ancestors {
		found, warn, err := bindingAmongChildren(db, ancestor, usage)
		if err != nil {
			return nil, "", err
		}
		if found != nil {
			return found, warn, nil
		}
	}

	// 3. Top level (parent_id IS NULL): the implicit root scope. Notes have
	// no root node, so a top-level llm_inferencer note is the natural
	// "bind the whole tree" location — it applies last, after every
	// closer scope.
	return bindingAmongTopLevel(db, usage)
}

// candidate is one binding inferencer note found during the walk.
type candidate struct {
	row     *inferencerRow
	warning string
}

// inferencerChildren returns the inferencer note ids among the children of a
// note, lowest id first.
func inferencerChildren(db *sql.DB, parentID int64) ([]int64, error) {
	return inferencerNotesWhere(db, `parent_id = ?`, parentID)
}

// inferencerTopLevel returns the inferencer note ids at the top level
// (parent_id IS NULL), lowest id first.
func inferencerTopLevel(db *sql.DB) ([]int64, error) {
	return inferencerNotesWhere(db, `parent_id IS NULL`)
}

func inferencerNotesWhere(db *sql.DB, where string, args ...any) ([]int64, error) {
	rows, err := db.Query(
		`SELECT id FROM notes WHERE type = 'llm_inferencer' AND `+where+` ORDER BY id`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("llm_inferencer: scan notes (%s): %w", where, err)
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

// firstBindingAmong returns the first of the note ids (in order) that yields
// a concrete model for the usage, with a warning naming the winner when
// several bind at the same level.
func firstBindingAmong(db *sql.DB, ids []int64, usage string) (*inferencerRow, string, error) {
	var extras []int64
	var winner *inferencerRow
	for _, id := range ids {
		row, ok, err := loadInferencerRow(db, id)
		if err != nil {
			return nil, "", err
		}
		if !ok || row.bindsUsage(usage) == nil {
			continue
		}
		if winner == nil {
			winner = row
			continue
		}
		extras = append(extras, id)
	}
	if winner == nil {
		return nil, "", nil
	}
	warn := ""
	if len(extras) > 0 {
		warn = fmt.Sprintf("multiple llm_inferencer notes bind %q at the same level: using note %d", usage, winner.NoteID)
	}
	return winner, warn, nil
}

// bindingAmongChildren finds a binding inferencer among a node's children.
func bindingAmongChildren(db *sql.DB, nodeID int64, usage string) (*inferencerRow, string, error) {
	ids, err := inferencerChildren(db, nodeID)
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	return firstBindingAmong(db, ids, usage)
}

// bindingAmongTopLevel finds a binding inferencer among top-level notes
// (the implicit root scope).
func bindingAmongTopLevel(db *sql.DB, usage string) (*inferencerRow, string, error) {
	ids, err := inferencerTopLevel(db)
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	return firstBindingAmong(db, ids, usage)
}

// nearestInSubtree BFS-searches the note's subtree for inferencer notes
// binding the usage, returning the shallowest (then lowest-id) binding.
func nearestInSubtree(db *sql.DB, noteID int64, usage string) (*candidate, error) {
	type level struct {
		id    int64
		depth int
	}
	queue := []level{{noteID, 0}}
	seen := map[int64]bool{noteID: true}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		ids, err := inferencerChildren(db, cur.id)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			if winner, warn, err := firstBindingAmong(db, ids, usage); err != nil {
				return nil, err
			} else if winner != nil {
				return &candidate{row: winner, warning: warn}, nil
			}
		}

		rows, err := db.Query(`SELECT id FROM notes WHERE parent_id = ?`, cur.id)
		if err != nil {
			return nil, fmt.Errorf("llm_inferencer: children of %d: %w", cur.id, err)
		}
		var childIDs []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			childIDs = append(childIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, id := range childIDs {
			if !seen[id] {
				seen[id] = true
				queue = append(queue, level{id, cur.depth + 1})
			}
		}
	}
	return nil, nil
}

// ancestorChain returns the parent chain of a note, closest first.
func ancestorChain(db *sql.DB, noteID int64) ([]int64, error) {
	var chain []int64
	current := noteID
	for {
		var parent sql.NullInt64
		err := db.QueryRow(`SELECT parent_id FROM notes WHERE id = ?`, current).Scan(&parent)
		if err == sql.ErrNoRows {
			return chain, nil
		} else if err != nil {
			return nil, fmt.Errorf("llm_inferencer: parent of %d: %w", current, err)
		}
		if !parent.Valid || parent.Int64 == 0 {
			return chain, nil
		}
		chain = append(chain, parent.Int64)
		current = parent.Int64
	}
}
