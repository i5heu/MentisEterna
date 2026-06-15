package server

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/searchindex"
)

const (
	searchResultLimit      = 10
	searchCandidateLimit   = 30
	searchChunkQueryLimit  = 80
	searchAttachmentQueryK = 20
)

// SearchResult extends NoteSummary with ranked search metadata.
type SearchResult struct {
	NoteSummary
	Distance float64  `json:"distance"`
	Path     string   `json:"path,omitempty"`
	Tags     []string `json:"tags"`
}

type noteSearchScore struct {
	sum   float64
	count int
}

func (s *noteSearchScore) AddHit(distance float64) {
	s.sum += distance
	s.count++
}

func (s *noteSearchScore) Average() float64 {
	if s == nil || s.count == 0 {
		return 2
	}
	return s.sum / float64(s.count)
}

// searchNotes performs a hybrid semantic search over note paragraphs, titles,
// paths, tags, and attachment OCR/STT embeddings.
// GET /notes/search?q=your+query[&types=standard,recipe]
func (s *Server) searchNotes(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	if !s.db.VSSAvailable() {
		log.Printf("semantic search unavailable: sqlite-vec is not loaded")
		http.Error(w, "semantic search system error", http.StatusInternalServerError)
		return
	}
	if s.llm == nil {
		log.Printf("semantic search unavailable: embedding client is not configured")
		http.Error(w, "semantic search system error", http.StatusInternalServerError)
		return
	}

	allowedTypes := parseSearchTypeFilter(r)
	query = llm.TruncateForEmbedding(query)
	vec, err := s.llm.GenerateEmbedding(query)
	if err != nil {
		log.Printf("semantic search embedding error: %v", err)
		http.Error(w, "semantic search system error", http.StatusInternalServerError)
		return
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	scores, err := s.searchNoteChunkHits(vecJSON, allowedTypes)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_ocr", "ocr_embedding", allowedTypes); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_stt", "stt_embedding", allowedTypes); err != nil {
		writeErr(w, err)
		return
	}

	results, err := s.buildSearchResults(scores, query)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func parseSearchTypeFilter(r *http.Request) []string {
	seen := map[string]bool{}
	var out []string
	for _, key := range []string{"types", "type"} {
		for _, raw := range r.URL.Query()[key] {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part == "" || seen[part] {
					continue
				}
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildTypeFilterClause(alias string, types []string) (string, []any) {
	if len(types) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(types))
	args := make([]any, len(types))
	for i, noteType := range types {
		placeholders[i] = "?"
		args[i] = noteType
	}
	return fmt.Sprintf(" AND %s.type IN (%s)", alias, strings.Join(placeholders, ",")), args
}

func (s *Server) searchNoteChunkHits(vecJSON string, allowedTypes []string) (map[int64]*noteSearchScore, error) {
	typeClause, typeArgs := buildTypeFilterClause("n", allowedTypes)
	query := fmt.Sprintf(`
		SELECT c.note_id, vss_note_search.distance
		FROM vss_note_search
		JOIN note_search_chunks c ON c.id = vss_note_search.rowid
		JOIN notes n ON n.id = c.note_id
		WHERE vss_note_search.embedding MATCH ? AND k = %d%s
		ORDER BY vss_note_search.distance ASC
	`, searchChunkQueryLimit, typeClause)
	args := append([]any{vecJSON}, typeArgs...)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scores := map[int64]*noteSearchScore{}
	for rows.Next() {
		var noteID int64
		var distance float64
		if err := rows.Scan(&noteID, &distance); err != nil {
			return nil, err
		}
		if scores[noteID] == nil {
			scores[noteID] = &noteSearchScore{}
		}
		scores[noteID].AddHit(distance)
	}
	return scores, rows.Err()
}

func (s *Server) mergeAttachmentSearchHits(scores map[int64]*noteSearchScore, vecJSON, tableName, columnName string, allowedTypes []string) error {
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name = ?)`, tableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + tableName).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	rows, err := s.db.Query(
		fmt.Sprintf(`
			SELECT rowid, distance
			FROM %s
			WHERE %s MATCH ? AND k = %d
			ORDER BY distance ASC
		`, tableName, columnName, searchAttachmentQueryK),
		vecJSON,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	fileDistByID := map[int64]float64{}
	for rows.Next() {
		var fileID int64
		var distance float64
		if err := rows.Scan(&fileID, &distance); err != nil {
			return err
		}
		fileDistByID[fileID] = distance
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(fileDistByID) == 0 {
		return nil
	}

	fileIDs := make([]int64, 0, len(fileDistByID))
	for fileID := range fileDistByID {
		fileIDs = append(fileIDs, fileID)
	}
	placeholders := make([]string, len(fileIDs))
	args := make([]any, 0, len(fileIDs)+len(allowedTypes))
	for i, fileID := range fileIDs {
		placeholders[i] = "?"
		args = append(args, fileID)
	}
	typeClause, typeArgs := buildTypeFilterClause("n", allowedTypes)
	args = append(args, typeArgs...)

	refRows, err := s.db.Query(`
		SELECT DISTINCT fr.note_id, fr.file_id
		FROM files_refs fr
		JOIN files f ON f.id = fr.file_id
		JOIN notes n ON n.id = fr.note_id
		WHERE fr.file_id IN (`+strings.Join(placeholders, ",")+`)
		  AND f.deleted_at IS NULL`+typeClause,
		args...,
	)
	if err != nil {
		return nil
	}
	defer refRows.Close()

	for refRows.Next() {
		var noteID, fileID int64
		if err := refRows.Scan(&noteID, &fileID); err != nil {
			return err
		}
		if scores[noteID] == nil {
			scores[noteID] = &noteSearchScore{}
		}
		scores[noteID].AddHit(fileDistByID[fileID])
	}
	return refRows.Err()
}

func (s *Server) buildSearchResults(scores map[int64]*noteSearchScore, query string) ([]SearchResult, error) {
	if len(scores) == 0 {
		return []SearchResult{}, nil
	}

	type candidate struct {
		id       int64
		distance float64
	}
	candidates := make([]candidate, 0, len(scores))
	for id, score := range scores {
		candidates = append(candidates, candidate{id: id, distance: score.Average()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance == candidates[j].distance {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].distance < candidates[j].distance
	})
	if len(candidates) > searchCandidateLimit {
		candidates = candidates[:searchCandidateLimit]
	}

	noteIDs := make([]int64, 0, len(candidates))
	for _, candidate := range candidates {
		noteIDs = append(noteIDs, candidate.id)
	}
	summaries, err := s.loadSearchSummaries(noteIDs)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		summary, ok := summaries[candidate.id]
		if !ok {
			continue
		}

		pathSegments, err := searchindex.LoadPathSegments(s.db.DB, candidate.id)
		if err != nil {
			pathSegments = nil
		}
		path := searchindex.DisplayPath(pathSegments)

		tags, err := loadTags(s.db.DB, candidate.id)
		if err != nil {
			tags = []string{}
		} else if tags == nil {
			tags = []string{}
		}

		distance := scores[candidate.id].Average() - literalSearchBoost(query, summary.Title, path, tags)
		if distance < 0 {
			distance = 0
		}

		results = append(results, SearchResult{
			NoteSummary: summary,
			Distance:    distance,
			Path:        path,
			Tags:        tags,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Distance == results[j].Distance {
			return results[i].ID < results[j].ID
		}
		return results[i].Distance < results[j].Distance
	})
	if len(results) > searchResultLimit {
		results = results[:searchResultLimit]
	}
	return results, nil
}

func (s *Server) loadSearchSummaries(ids []int64) (map[int64]NoteSummary, error) {
	if len(ids) == 0 {
		return map[int64]NoteSummary{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(noteSelectSQL+` WHERE n.id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]NoteSummary, len(ids))
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out[summary.ID] = summary
	}
	return out, rows.Err()
}

func literalSearchBoost(query, title, path string, tags []string) float64 {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0
	}

	var boost float64
	titleLower := strings.ToLower(strings.TrimSpace(title))
	pathLower := strings.ToLower(strings.TrimSpace(path))

	switch {
	case titleLower == q:
		boost += 0.35
	case strings.Contains(titleLower, q):
		boost += 0.18
	}

	switch {
	case pathLower == q:
		boost += 0.25
	case pathLower != "" && strings.Contains(pathLower, q):
		boost += 0.12
	}

	for _, tag := range tags {
		tagLower := strings.ToLower(strings.TrimSpace(tag))
		switch {
		case tagLower == q:
			boost += 0.25
			return boost
		case strings.Contains(tagLower, q):
			boost += 0.12
		}
	}

	return boost
}
