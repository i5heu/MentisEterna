package llminferencer

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
)

// Privacy guard for notes marked high_private (notes.high_private = 1): the
// user declared their content too private for remote providers, so it must
// never be sent to an llm_inferencer-bound remote endpoint. Two mechanisms:
//
//   - ResolveChatForNotes: call sites whose prompt embeds note content that
//     cannot be omitted (e.g. the RSS classification prompt built from an
//     rss_prompt note's body) downgrade a remote ("note" source) resolution
//     to the config.toml local LLM for that call.
//
//   - Archive chat retrieval (internal/server/chat.go) instead EXCLUDES
//     high_private notes from the context it sends to a remote provider
//     (public notes keep the remote routing); a local/config resolution
//     includes everything.
//
// Both paths share the same rule: a remote provider never receives content
// of a high_private note.

// NoteIsHighPrivate reports whether a note is marked highly private.
// A missing note is not private (the guard then only concerns the notes that
// exist).
func NoteIsHighPrivate(db *sql.DB, noteID int64) (bool, error) {
	if db == nil || noteID <= 0 {
		return false, nil
	}
	var flag bool
	err := db.QueryRow(`SELECT high_private FROM notes WHERE id = ?`, noteID).Scan(&flag)
	if err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("llm_inferencer: high_private of note %d: %w", noteID, err)
	}
	return flag, nil
}

// AnyNoteHighPrivate reports whether any of the notes is marked highly
// private (ignoring id 0 = "no note"). It short-circuits on the first
// private note.
func AnyNoteHighPrivate(db *sql.DB, noteIDs ...int64) (bool, error) {
	ids := make([]int64, 0, len(noteIDs))
	for _, id := range noteIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if db == nil || len(ids) == 0 {
		return false, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM notes WHERE high_private = 1 AND id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("llm_inferencer: high_private scan: %w", err)
	}
	return n > 0, nil
}

// ResolveChatForNotes resolves the chat client for a usage whose prompt will
// include content of the given notes (e.g. the rss_prompt note body feeding
// the rss_classify prompt). When any of the notes is marked high_private and
// the regular resolution picked a REMOTE provider binding (Source "note"),
// the resolution is downgraded to the config.toml feature/tier client — the
// local LLM — so the private content never leaves the machine. Local and
// config resolutions pass through unchanged (the local LLM may read it).
//
// The downgrade is recorded in ResolvedChat.Warning; the call ledger sees
// the routing as "config".
func ResolveChatForNotes(db *sql.DB, usage string, noteIDs ...int64) (*llm.ChatClient, ResolvedChat, error) {
	client, res, err := ResolveChat(db, usage)
	if err != nil || client == nil {
		return client, res, err
	}
	if res.Source != SourceNote {
		return client, res, nil
	}
	private, err := AnyNoteHighPrivate(db, noteIDs...)
	if err != nil {
		return nil, res, err
	}
	if !private {
		return client, res, nil
	}

	// Downgrade: same fallback as an unbound usage — the config.toml
	// feature/tier system client (the local LLM), tagged "config" for the
	// call ledger.
	local := llm.NewFeatureChatClient(usage)
	if local == nil {
		return nil, res, fmt.Errorf("llm_inferencer: unknown usage %q", usage)
	}
	privateIDs := make([]int64, 0, len(noteIDs))
	for _, id := range noteIDs {
		if id > 0 {
			privateIDs = append(privateIDs, id)
		}
	}
	res = ResolvedChat{
		Usage:   usage,
		Source:  SourceConfig,
		Model:   local.Model,
		BaseURL: local.BaseURL,
		Warning: fmt.Sprintf(
			"note %s is marked highly private: the remote provider was downgraded to the local LLM for this call",
			formatIDList(privateIDs),
		),
	}
	return local, res, nil
}

// formatIDList renders note ids for a human-readable warning ("1", "1, 2").
func formatIDList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ", ")
}
