package server

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/internal/searchindex"
)

const (
	searchResultLimit         = 10
	searchCandidateLimit      = 30
	searchChunkQueryLimit     = 80
	searchAttachmentQueryK    = 20
	searchStreamTagLimit      = 6
	searchStreamTitleLimit    = 8
	searchStreamSemanticLimit = 10
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

type searchStreamEvent struct {
	Type    string         `json:"type"`
	Phase   string         `json:"phase,omitempty"`
	Message string         `json:"message,omitempty"`
	Section *searchSection `json:"section,omitempty"`
	Total   int            `json:"total,omitempty"`
}

type searchSection struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Results     []SearchResult `json:"results"`
}

type literalMatch struct {
	ID    int64
	Score float64
	Order int
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
// GET /notes/search?q=your+query[&types=standard,recipe][&stream=1]
func (s *Server) searchNotes(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	allowedTypes := parseSearchTypeFilter(r)
	if wantsStreamedSearch(r) {
		s.streamSearchNotes(w, r, query, allowedTypes)
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

	query = llm.TruncateForEmbedding(query)
	release := llm.BeginBackendUse(s.llm)
	defer release()
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

func wantsStreamedSearch(r *http.Request) bool {
	stream := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("stream")))
	if stream == "1" || stream == "true" || stream == "yes" {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/x-ndjson")
}

func (s *Server) streamSearchNotes(w http.ResponseWriter, r *http.Request, query string, allowedTypes []string) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}

	send := func(event searchStreamEvent) bool {
		if err := encoder.Encode(event); err != nil {
			log.Printf("search stream encode error: %v", err)
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	seen := map[int64]bool{}
	if !send(searchStreamEvent{
		Type:    "status",
		Phase:   "literal",
		Message: "Searching titles and tags…",
	}) {
		return
	}

	for _, phase := range []struct {
		key         string
		label       string
		description string
		limit       int
		search      func(string, []string, map[int64]bool, int) ([]SearchResult, error)
	}{
		{
			key:         "titles",
			label:       "Title matches",
			description: "FTS4 title hits with prefix-token fuzzy matching.",
			limit:       searchStreamTitleLimit,
			search:      s.searchTitleResults,
		},
		{
			key:         "tags",
			label:       "Tag matches",
			description: "FTS4 tag hits with prefix-token fuzzy matching.",
			limit:       searchStreamTagLimit,
			search:      s.searchTagResults,
		},
	} {
		if err := r.Context().Err(); err != nil {
			return
		}
		results, err := phase.search(query, allowedTypes, seen, phase.limit)
		if err != nil {
			send(searchStreamEvent{Type: "error", Phase: phase.key, Message: "Search failed while loading exact matches."})
			return
		}
		if len(results) == 0 {
			continue
		}
		updateSeenSearchResults(seen, results)
		if !send(searchStreamEvent{
			Type: "section",
			Section: &searchSection{
				Key:         phase.key,
				Label:       phase.label,
				Description: phase.description,
				Results:     results,
			},
		}) {
			return
		}
	}

	if err := r.Context().Err(); err != nil {
		return
	}

	if !send(searchStreamEvent{
		Type:    "status",
		Phase:   "semantic",
		Message: "Streaming related notes from embeddings…",
	}) {
		return
	}

	if !s.db.VSSAvailable() || s.llm == nil {
		message := "Semantic results are unavailable right now, but exact matches are ready."
		if len(seen) == 0 {
			message = "No exact matches found, and semantic search is unavailable right now."
		}
		send(searchStreamEvent{Type: "done", Phase: "semantic", Message: message, Total: len(seen)})
		return
	}

	truncated := llm.TruncateForEmbedding(query)
	release := llm.BeginBackendUse(s.llm)
	vec, err := s.llm.GenerateEmbedding(truncated)
	release()
	if err != nil {
		log.Printf("semantic search embedding error: %v", err)
		message := "Exact matches loaded, but semantic results failed."
		if len(seen) == 0 {
			message = "Semantic search failed."
		}
		send(searchStreamEvent{Type: "done", Phase: "semantic", Message: message, Total: len(seen)})
		return
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	scores, err := s.searchNoteChunkHits(vecJSON, allowedTypes)
	if err == nil {
		err = s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_ocr", "ocr_embedding", allowedTypes)
	}
	if err == nil {
		err = s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_stt", "stt_embedding", allowedTypes)
	}
	if err != nil {
		log.Printf("search stream semantic error: %v", err)
		message := "Exact matches loaded, but semantic results failed."
		if len(seen) == 0 {
			message = "Semantic search failed."
		}
		send(searchStreamEvent{Type: "done", Phase: "semantic", Message: message, Total: len(seen)})
		return
	}

	semantic, err := s.buildSearchResultsFiltered(scores, query, seen, searchStreamSemanticLimit)
	if err != nil {
		log.Printf("search stream semantic build error: %v", err)
		send(searchStreamEvent{Type: "done", Phase: "semantic", Message: "Exact matches loaded, but semantic results failed.", Total: len(seen)})
		return
	}
	if len(semantic) > 0 {
		updateSeenSearchResults(seen, semantic)
		if !send(searchStreamEvent{
			Type: "section",
			Section: &searchSection{
				Key:         "semantic",
				Label:       "Related notes",
				Description: "Embedding-based results stream in after exact matches and never move earlier hits.",
				Results:     semantic,
			},
		}) {
			return
		}
	}

	message := "Search complete."
	if len(seen) == 0 {
		message = "No results found."
	}
	send(searchStreamEvent{Type: "done", Phase: "complete", Message: message, Total: len(seen)})
}

func updateSeenSearchResults(seen map[int64]bool, results []SearchResult) {
	for _, result := range results {
		seen[result.ID] = true
	}
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
	return s.buildSearchResultsFiltered(scores, query, nil, searchResultLimit)
}

func (s *Server) buildSearchResultsFiltered(scores map[int64]*noteSearchScore, query string, exclude map[int64]bool, limit int) ([]SearchResult, error) {
	if len(scores) == 0 {
		return []SearchResult{}, nil
	}
	if limit <= 0 {
		limit = searchResultLimit
	}

	type candidate struct {
		id       int64
		distance float64
	}
	candidates := make([]candidate, 0, len(scores))
	for id, score := range scores {
		if exclude != nil && exclude[id] {
			continue
		}
		candidates = append(candidates, candidate{id: id, distance: score.Average()})
	}
	if len(candidates) == 0 {
		return []SearchResult{}, nil
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
	if len(results) > limit {
		results = results[:limit]
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

// ftsColumnQuery builds an FTS4 MATCH expression scoped to a single column
// (e.g. `tags:foo* OR tags:bar*`). FTS4 column-scoped queries use the
// `column:term` syntax and require the column name to be present in the
// MATCH expression for every term.
//
// FTS4 has no native fuzzy/Levenshtein support, so we approximate it by:
//  1. Lowercasing and stripping punctuation.
//  2. Splitting into tokens.
//  3. For each token, emitting `column:token*` (prefix match) — this
//     catches typos that share a prefix (e.g. "recip" -> "recipe",
//     "recipies") and partial-word input.
//  4. Combining tokens with OR so any single token hit surfaces a
//     candidate. Multi-word queries that should be more restrictive are
//     re-ranked by the in-memory fuzzy scorer after the candidate set is
//     fetched.
func ftsColumnQuery(query, column string) string {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return ""
	}
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		parts = append(parts, column+":"+tok+"*")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " OR ")
}

// searchTagResults uses the FTS4 `tags` column to find notes whose tag set
// contains a token starting with any query token. Candidates are then
// re-ranked with the in-memory fuzzy scorer against the full tag string so
// that exact / prefix / subsequence matches sort before loose prefix hits.
func (s *Server) searchTagResults(query string, allowedTypes []string, exclude map[int64]bool, limit int) ([]SearchResult, error) {
	match := ftsColumnQuery(query, "tags")
	if match == "" {
		return []SearchResult{}, nil
	}
	typeClause, typeArgs := buildTypeFilterClause("n", allowedTypes)
	rows, err := s.db.Query(`
		SELECT f.note_id, COALESCE(f.tags, ''), n.pinned,
		       COALESCE(u.created_at, n.created_at) AS updated_at
		FROM notes_fts f
		JOIN notes n ON n.id = f.note_id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE f.tags MATCH ? AND f.tags != ''`+typeClause+`
		ORDER BY n.pinned DESC, updated_at DESC, f.note_id ASC
	`, append([]any{match}, typeArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	best := map[int64]literalMatch{}
	order := 0
	for rows.Next() {
		var noteID int64
		var tagText string
		var pinned bool
		var updatedAt string
		if err := rows.Scan(&noteID, &tagText, &pinned, &updatedAt); err != nil {
			return nil, err
		}
		if exclude != nil && exclude[noteID] {
			continue
		}
		// Re-rank against each individual tag token via the fuzzy scorer so
		// that strong matches (exact / prefix) outrank weak prefix hits.
		bestScore := math.MaxFloat64
		for _, tag := range strings.Fields(tagText) {
			if score, ok := fuzzySearchScore(query, tag); ok && score < bestScore {
				bestScore = score
			}
		}
		if bestScore == math.MaxFloat64 {
			// FTS4 prefix hit but no token-level fuzzy match — keep it with a
			// weak score so it still surfaces but ranks below stronger hits.
			bestScore = 0.5
		}
		upsertLiteralMatch(best, noteID, bestScore, order)
		order++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.buildLiteralResults(matchesFromMap(best), limit)
}

// searchTitleResults uses the FTS4 `title` column to find notes whose title
// contains a token starting with any query token. Candidates are re-ranked
// with the in-memory fuzzy scorer against the full title so exact and prefix
// matches sort before loose prefix hits.
func (s *Server) searchTitleResults(query string, allowedTypes []string, exclude map[int64]bool, limit int) ([]SearchResult, error) {
	match := ftsColumnQuery(query, "title")
	if match == "" {
		return []SearchResult{}, nil
	}
	typeClause, typeArgs := buildTypeFilterClause("n", allowedTypes)
	rows, err := s.db.Query(`
		SELECT f.note_id, COALESCE(f.title, ''), n.pinned,
		       COALESCE(u.created_at, n.created_at) AS updated_at
		FROM notes_fts f
		JOIN notes n ON n.id = f.note_id
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE f.title MATCH ? AND f.title != ''`+typeClause+`
		ORDER BY n.pinned DESC, updated_at DESC, f.note_id ASC
	`, append([]any{match}, typeArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	best := map[int64]literalMatch{}
	order := 0
	for rows.Next() {
		var noteID int64
		var title string
		var pinned bool
		var updatedAt string
		if err := rows.Scan(&noteID, &title, &pinned, &updatedAt); err != nil {
			return nil, err
		}
		if exclude != nil && exclude[noteID] {
			continue
		}
		score, ok := fuzzySearchScore(query, title)
		if !ok {
			// FTS4 prefix hit but the fuzzy scorer (which is stricter about
			// subsequence + length) didn't accept it. Keep it with a weak
			// score so prefix matches still surface.
			score = 0.5
		}
		upsertLiteralMatch(best, noteID, score, order)
		order++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.buildLiteralResults(matchesFromMap(best), limit)
}

func (s *Server) buildLiteralResults(matches []literalMatch, limit int) ([]SearchResult, error) {
	if len(matches) == 0 {
		return []SearchResult{}, nil
	}
	if limit <= 0 {
		limit = len(matches)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			if matches[i].Order == matches[j].Order {
				return matches[i].ID < matches[j].ID
			}
			return matches[i].Order < matches[j].Order
		}
		return matches[i].Score < matches[j].Score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	noteIDs := make([]int64, 0, len(matches))
	scoreByID := make(map[int64]float64, len(matches))
	for _, match := range matches {
		noteIDs = append(noteIDs, match.ID)
		scoreByID[match.ID] = match.Score
	}
	summaries, err := s.loadSearchSummaries(noteIDs)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(matches))
	for _, match := range matches {
		summary, ok := summaries[match.ID]
		if !ok {
			continue
		}

		pathSegments, err := searchindex.LoadPathSegments(s.db.DB, match.ID)
		if err != nil {
			pathSegments = nil
		}
		path := searchindex.DisplayPath(pathSegments)

		tags, err := loadTags(s.db.DB, match.ID)
		if err != nil || tags == nil {
			tags = []string{}
		}

		results = append(results, SearchResult{
			NoteSummary: summary,
			Distance:    scoreByID[match.ID],
			Path:        path,
			Tags:        tags,
		})
	}
	return results, nil
}

func upsertLiteralMatch(best map[int64]literalMatch, noteID int64, score float64, order int) {
	current, exists := best[noteID]
	if !exists || score < current.Score || (score == current.Score && order < current.Order) {
		best[noteID] = literalMatch{ID: noteID, Score: score, Order: order}
	}
}

func matchesFromMap(best map[int64]literalMatch) []literalMatch {
	matches := make([]literalMatch, 0, len(best))
	for _, match := range best {
		matches = append(matches, match)
	}
	return matches
}

func normalizeSearchText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	})
	return strings.Join(parts, " ")
}

func fuzzySearchScore(query, target string) (float64, bool) {
	q := normalizeSearchText(query)
	t := normalizeSearchText(target)
	if q == "" || t == "" {
		return 0, false
	}
	if t == q {
		return 0, true
	}
	if strings.HasPrefix(t, q) {
		return 0.06 + relativeLengthPenalty(t, q, 0.08), true
	}
	if strings.Contains(t, q) {
		return 0.14 + relativeLengthPenalty(t, q, 0.12), true
	}

	qTokens := strings.Fields(q)
	tTokens := strings.Fields(t)
	if len(qTokens) > 0 && len(tTokens) > 0 {
		total := 0.0
		for _, queryToken := range qTokens {
			bestTokenScore, ok := bestTokenScore(queryToken, tTokens)
			if !ok {
				total = -1
				break
			}
			total += bestTokenScore
		}
		if total >= 0 {
			return 0.22 + total/float64(len(qTokens)), true
		}
	}

	compactQuery := strings.ReplaceAll(q, " ", "")
	compactTarget := strings.ReplaceAll(t, " ", "")
	if compactQuery != "" && compactTarget != "" && isSubsequence(compactQuery, compactTarget) {
		return 0.38 + relativeLengthPenalty(compactTarget, compactQuery, 0.18), true
	}

	return 0, false
}

func bestTokenScore(queryToken string, targetTokens []string) (float64, bool) {
	best := math.MaxFloat64
	for _, targetToken := range targetTokens {
		switch {
		case targetToken == queryToken:
			best = minFloat(best, 0.02)
		case strings.HasPrefix(targetToken, queryToken):
			best = minFloat(best, 0.06+relativeLengthPenalty(targetToken, queryToken, 0.08))
		case strings.HasPrefix(queryToken, targetToken):
			best = minFloat(best, 0.08+relativeLengthPenalty(queryToken, targetToken, 0.08))
		case strings.Contains(targetToken, queryToken):
			best = minFloat(best, 0.12+relativeLengthPenalty(targetToken, queryToken, 0.1))
		case strings.Contains(queryToken, targetToken):
			best = minFloat(best, 0.16+relativeLengthPenalty(queryToken, targetToken, 0.1))
		case isSubsequence(queryToken, targetToken):
			best = minFloat(best, 0.22+relativeLengthPenalty(targetToken, queryToken, 0.14))
		}
	}
	if best == math.MaxFloat64 {
		return 0, false
	}
	return best, true
}

func relativeLengthPenalty(target, query string, scale float64) float64 {
	targetLen := len([]rune(target))
	queryLen := len([]rune(query))
	if targetLen == 0 || queryLen == 0 {
		return 0
	}
	diff := targetLen - queryLen
	if diff < 0 {
		diff = -diff
	}
	return math.Min(scale, float64(diff)/float64(maxInt(targetLen, queryLen))*scale)
}

func isSubsequence(query, target string) bool {
	queryRunes := []rune(query)
	if len(queryRunes) == 0 {
		return false
	}
	qi := 0
	for _, r := range []rune(target) {
		if qi < len(queryRunes) && queryRunes[qi] == r {
			qi++
			if qi == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
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
