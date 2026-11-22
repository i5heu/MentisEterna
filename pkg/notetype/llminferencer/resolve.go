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
	Source           string `json:"source"`                       // "note" (llm_inferencer binding) | "local" (explicit {local:true} pin) | "config" (config.toml fallback)
	InferencerNoteID int64  `json:"inferencer_note_id,omitempty"` // note that bound / pinned the usage ("note" and "local" sources)
	Provider         string `json:"provider,omitempty"`           // provider of the binding note (note source)
	BaseURL          string `json:"base_url,omitempty"`
	Model            string `json:"model"`
	Tier             string `json:"tier,omitempty"` // tier the usage bound to (note source)
	Warning          string `json:"warning,omitempty"`
}

// Source values for ResolvedChat.Source.
const (
	// SourceNote: the llm_inferencer note bound the usage to one of its
	// remote provider tiers / models.
	SourceNote = "note"
	// SourceConfig: nothing bound the usage — the config.toml
	// feature/tier system (the local LLM) serves it.
	SourceConfig = "config"
	// SourceLocal: the note explicitly pinned the usage to the config.toml
	// feature/tier system with a {local: true} usage binding — the user
	// chose the local LLM for this usage.
	SourceLocal = "local"
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
	// Local: the usage is explicitly pinned to the config.toml
	// feature/tier system (the local LLM) — Model/Tier stay empty.
	Local bool
	// Explicit reports an explicit usage binding (vs. the default tier).
	Explicit bool
}

// bindsUsage derives the effective binding for a usage on this note, or nil
// when the note does not yield a binding. Rules:
//
//   - explicit {local: true} binding → binds as a local pin (the usage
//     resolves to the config.toml system, beating the default tier),
//   - explicit usage binding with a direct model → binds,
//   - explicit usage binding with a tier → binds only when that tier has a
//     model (an explicit binding to an empty tier is a config wound that
//     must NOT silently capture the usage),
//   - no explicit binding → default_tier binds when it has a model,
//   - anything else → no binding.
func (r *inferencerRow) bindsUsage(usage string) *binding {
	if ub, ok := r.Usages[usage]; ok {
		if ub.Local {
			return &binding{Local: true, Explicit: true}
		}
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

// ResolveChat resolves the chat client for an app usage (see
// internal/llm's usage catalog).
//
// The llm_inferencer note type is a singleton — one note per app, enforced on
// creation — and that note applies GLOBALLY, wherever it sits in the note
// tree:
//
//  1. the note binds the usage when it yields a concrete remote model
//     (explicit {tier}/{model} binding, or its default_tier) — Source "note",
//  2. an explicit {local: true} binding pins the usage to the config.toml
//     feature/tier system (the app's local LLM), beating the note's
//     remote models and default tier — Source "local",
//  3. no note (or nothing it binds) → the config.toml feature/tier system
//     client — Source "config".
//
// It always returns a usable client (or an error on infra failures / for
// unknown usages); the API key stays inside the client and never appears in
// ResolvedChat.
//
// Databases created before the singleton rule may hold several
// llm_inferencer notes: they resolve in id order (lowest wins) with a
// warning — delete the extras; new ones cannot be created.
func ResolveChat(db *sql.DB, usage string) (*llm.ChatClient, ResolvedChat, error) {
	res := ResolvedChat{Usage: usage, Source: SourceConfig}

	if db != nil {
		row, warn, err := findBinding(db, usage)
		if err != nil {
			return nil, res, err
		}
		if row != nil {
			b := row.bindsUsage(usage)

			if b.Local {
				// Explicit local pin: the config.toml local LLM.
				client := llm.NewFeatureChatClient(usage)
				if client == nil {
					return nil, res, fmt.Errorf("llm_inferencer: unknown usage %q", usage)
				}
				res = ResolvedChat{
					Usage:            usage,
					Source:           SourceLocal,
					InferencerNoteID: row.NoteID,
					Model:            client.Model,
					BaseURL:          client.BaseURL,
					Warning:          warn,
				}
				return client, res, nil
			}

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

// findBinding returns the llm_inferencer note governing the usage. The note
// type is a singleton, so a single global note applies from anywhere in the
// tree (no hierarchy walk needed). Legacy databases that predate the
// singleton rule may hold several notes: they resolve in id order (lowest
// wins), with a warning — delete the extras.
func findBinding(db *sql.DB, usage string) (*inferencerRow, string, error) {
	ids, err := inferencerNotesWhere(db, `1=1`)
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	return firstBindingAmong(db, ids, usage)
}

// firstBindingAmong returns the first of the note ids (in order) that binds
// the usage — a concrete remote model OR an explicit local pin — with a
// warning naming the winner when several bind.
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
		warn = fmt.Sprintf(
			"multiple llm_inferencer notes bind %q — the type is limited to one note per app; using note %d, delete the extras",
			usage, winner.NoteID)
	}
	return winner, warn, nil
}

// inferencerNotesWhere returns the ids of all llm_inferencer notes matching
// the WHERE fragment, lowest id first.
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
