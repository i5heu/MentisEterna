package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
)

const relatedResultLimit = 6

// RelatedNote is the slim shape of one related-notes result (no body: the
// panel only links to the note).
type RelatedNote struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Path        string  `json:"path,omitempty"`
	Distance    float64 `json:"distance"`
	UpdatedAt   string  `json:"updated_at"`
	HighPrivate bool    `json:"high_private"`
}

// getNoteRelated returns the semantic neighbors of a note: the notes whose
// content is closest to it in embedding space. The note itself, its
// ancestors, and its direct children are excluded — they are already
// visible in the tree/thread, the panel surfaces forgotten connections.
//
// The seed is the note's own text (title + body, truncated to the embedding
// budget) re-embedded as a query against the shared chunk index, so the
// feature needs no stored whole-note vectors.
//
// GET /notes/:id/related
func (s *Server) getNoteRelated(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}

	if !s.db.VSSAvailable() || s.llm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "results": []RelatedNote{}})
		return
	}

	sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		writeErr(w, err)
		return
	}

	seed := strings.TrimSpace(sum.Title + "\n\n" + sum.Body)
	seed = llm.TruncateForEmbedding(seed)
	vec, err := s.llm.GenerateEmbedding(seed)
	if err != nil {
		writeErr(w, err)
		return
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	scores, err := s.searchNoteChunkHits(vecJSON, nil)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_ocr", "ocr_embedding", nil); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_stt", "stt_embedding", nil); err != nil {
		writeErr(w, err)
		return
	}

	exclude, err := s.relatedExclusionSet(id)
	if err != nil {
		writeErr(w, err)
		return
	}

	results, err := s.buildSearchResultsFiltered(scores, seed, exclude, relatedResultLimit)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]RelatedNote, 0, len(results))
	for _, res := range results {
		out = append(out, RelatedNote{
			ID:          res.ID,
			Title:       res.Title,
			Type:        res.Type,
			Path:        res.Path,
			Distance:    res.Distance,
			UpdatedAt:   res.UpdatedAt,
			HighPrivate: res.HighPrivate,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "results": out})
}

// relatedExclusionSet collects the note ids the related panel must not
// surface: the note itself, its ancestor chain, and its direct children.
func (s *Server) relatedExclusionSet(id int64) (map[int64]bool, error) {
	exclude := map[int64]bool{id: true}

	// Ancestor chain.
	cur := id
	for {
		sum, err := scanSummary(s.db.QueryRow(noteSelectSQL+` WHERE n.id = ?`, cur))
		if errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return nil, err
		}
		if sum.ParentID == nil || *sum.ParentID == 0 {
			break
		}
		cur = *sum.ParentID
		if exclude[cur] {
			break
		}
		exclude[cur] = true
	}

	// Direct children.
	rows, err := s.db.Query(`SELECT id FROM notes WHERE parent_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var childID int64
		if err := rows.Scan(&childID); err != nil {
			return nil, err
		}
		exclude[childID] = true
	}
	return exclude, rows.Err()
}
