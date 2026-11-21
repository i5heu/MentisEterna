package rss

import (
	"database/sql"
	"fmt"
)

// PromptRef describes the classification prompt in scope for a reader.
type PromptRef struct {
	NoteID  int64  `json:"note_id"` // 0 when the built-in default applies
	Title   string `json:"title"`
	Default bool   `json:"default"`
	Warning string `json:"warning,omitempty"`
}

// resolvePromptNote finds the rss_prompt note binding a reader note:
//
//  1. the reader's own subtree (nearest descendant wins: shallowest depth,
//     then lowest id),
//  2. the parent's children (siblings),
//  3. each successive ancestor's children (uncles), up to the root.
//
// Multiple prompts at the same nearest level is a configuration smell: the
// lowest id binds and the others are reported as a warning (surfaced in the
// view). No prompt anywhere → Default=true.
func resolvePromptNote(db *sql.DB, noteID int64) (PromptRef, error) {
	// 1. Reader's subtree: BFS by depth.
	if found, err := nearestPromptInSubtree(db, noteID); err != nil {
		return PromptRef{}, err
	} else if found != nil {
		return *found, nil
	}

	// 2./3. Walk ancestors; scan each ancestor's children (the path's
	// siblings/uncles).
	ancestors, err := ancestorChain(db, noteID)
	if err != nil {
		return PromptRef{}, err
	}
	for _, ancestor := range ancestors {
		found, warning, err := promptAmongChildren(db, ancestor)
		if err != nil {
			return PromptRef{}, err
		}
		if found != nil {
			found.Warning = warning
			return *found, nil
		}
	}

	return PromptRef{Default: true, Title: "Built-in default prompt"}, nil
}

// nearestPromptInSubtree BFS-searches the note's subtree for rss_prompt
// notes, returning the shallowest (then lowest-id) one.
func nearestPromptInSubtree(db *sql.DB, noteID int64) (*PromptRef, error) {
	type level struct {
		id    int64
		depth int
	}
	queue := []level{{noteID, 0}}
	seen := map[int64]bool{noteID: true}
	var extra []int64

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		found, err := promptChildren(db, cur.id)
		if err != nil {
			return nil, err
		}
		if len(found.prompts) > 0 {
			nearest := found.prompts[0]
			for _, p := range found.prompts[1:] {
				extra = append(extra, p.NoteID)
			}
			ref := &PromptRef{NoteID: nearest.NoteID, Title: nearest.Title}
			if len(extra) > 0 {
				ref.Warning = fmt.Sprintf("multiple RSS Prompt notes at the same level: using note %d", nearest.NoteID)
			}
			return ref, nil
		}

		rows, err := db.Query(`SELECT id FROM notes WHERE parent_id = ?`, cur.id)
		if err != nil {
			return nil, fmt.Errorf("rss: scope: children of %d: %w", cur.id, err)
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

// ancestors result: ordered list of note ids from the reader's parent up to
// the root.
type promptScan struct {
	prompts []PromptRef // sorted by id
}

func promptChildren(db *sql.DB, parentID int64) (*promptScan, error) {
	rows, err := db.Query(
		`SELECT id, COALESCE(title, '') FROM notes
		 WHERE parent_id = ? AND type = 'rss_prompt' ORDER BY id`, parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: scope: prompts of %d: %w", parentID, err)
	}
	defer rows.Close()
	scan := &promptScan{}
	for rows.Next() {
		var p PromptRef
		if err := rows.Scan(&p.NoteID, &p.Title); err != nil {
			return nil, err
		}
		scan.prompts = append(scan.prompts, p)
	}
	return scan, rows.Err()
}

// promptAmongChildren returns the prompt bound among a node's children with
// a warning when several exist (lowest id wins deterministically).
func promptAmongChildren(db *sql.DB, nodeID int64) (*PromptRef, string, error) {
	scan, err := promptChildren(db, nodeID)
	if err != nil {
		return nil, "", err
	}
	if len(scan.prompts) == 0 {
		return nil, "", nil
	}
	first := scan.prompts[0]
	warning := ""
	if len(scan.prompts) > 1 {
		warning = fmt.Sprintf("multiple RSS Prompt notes at the same level: using note %d", first.NoteID)
	}
	return &PromptRef{NoteID: first.NoteID, Title: first.Title}, warning, nil
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
			return nil, fmt.Errorf("rss: scope: parent of %d: %w", current, err)
		}
		if !parent.Valid || parent.Int64 == 0 {
			return chain, nil
		}
		chain = append(chain, parent.Int64)
		current = parent.Int64
	}
}

// noteBody reads the current body of a note (latest updates row).
func noteBody(db *sql.DB, noteID int64) (string, error) {
	var body string
	err := db.QueryRow(
		`SELECT body FROM updates WHERE note_id = ? ORDER BY id DESC LIMIT 1`, noteID,
	).Scan(&body)
	if err == sql.ErrNoRows {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("rss: note body of %d: %w", noteID, err)
	}
	return body, nil
}

// promptText loads the classification prompt in scope: the note body of the
// resolved rss_prompt note, or the built-in default when none is in scope.
func promptText(db *sql.DB, noteID int64) (PromptRef, string, error) {
	ref, err := resolvePromptNote(db, noteID)
	if err != nil {
		return ref, "", err
	}
	if ref.Default || ref.NoteID == 0 {
		return ref, defaultPrompt, nil
	}
	body, err := noteBody(db, ref.NoteID)
	if err != nil {
		return ref, defaultPrompt, err
	}
	if body == "" {
		// An empty prompt note reads as "no prompt defined" → run defaults.
		ref.Default = true
		ref.Warning = "prompt note body is empty; using default"
		return ref, defaultPrompt, nil
	}
	return ref, body, nil
}
