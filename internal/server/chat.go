package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype/llminferencer"
)

const (
	// Retrieval: how many candidates the vector search ranks, and how many
	// of them feed the LLM context.
	chatCandidateLimit = 12
	chatContextLimit   = 8
	// Context budget: per-note and total rune caps for the excerpts sent to
	// the model (embedding-side truncation applies to the query only).
	chatPerNoteRunes = 3000
	chatContextRunes = 15000
)

// chatSource is one retrieved note the answer may draw on.
type chatSource struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Path        string  `json:"path,omitempty"`
	Distance    float64 `json:"distance"`
	UpdatedAt   string  `json:"updated_at"`
	HighPrivate bool    `json:"high_private"`
}

// chatRouting mirrors how the "chat" usage was resolved (the same shape the
// llm_inferencer resolver reports; the API key never appears).
type chatRouting struct {
	Source   string `json:"source"` // "note" (remote provider) | "local" | "config"
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// chatStreamEvent is one NDJSON line of the archive chat stream.
type chatStreamEvent struct {
	Type            string       `json:"type"` // status | sources | answer | done | error
	Phase           string       `json:"phase,omitempty"`
	Message         string       `json:"message,omitempty"`
	Sources         []chatSource `json:"sources,omitempty"`
	Routing         *chatRouting `json:"routing,omitempty"`
	PrivateExcluded int          `json:"private_excluded,omitempty"`
	Answer          string       `json:"answer,omitempty"`
}

// archiveChat answers a question over the user's own notes
// (retrieval-augmented generation):
//
//	POST /notes/chat {"query": "...", "types": ["standard","recipe"]} → NDJSON stream
//
// The stream emits status → sources → answer → done events. The "chat"
// usage resolves through the llm_inferencer note (remote provider binding)
// or falls back to the config.toml local LLM.
//
// Privacy: notes marked high_private are never sent to a REMOTE provider —
// they are excluded from the retrieval context (and from the reported
// sources) when routing resolved remotely; a local/config resolution
// includes everything. The response reports how many private notes were
// excluded so the UI can say why an answer may be missing context.
func (s *Server) archiveChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Query string   `json:"query"`
		Types []string `json:"types"`
	}
	if !s.decodeJSONBody(w, r, &in) {
		return
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)
	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}
	send := func(event chatStreamEvent) bool {
		if err := encoder.Encode(event); err != nil {
			log.Printf("archive chat stream encode error: %v", err)
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	fail := func(message string) {
		if !send(chatStreamEvent{Type: "error", Message: message}) {
			return
		}
		send(chatStreamEvent{Type: "done", Phase: "error", Message: message})
	}

	if !send(chatStreamEvent{Type: "status", Phase: "retrieve", Message: "Searching notes…"}) {
		return
	}

	if !s.db.VSSAvailable() || s.llm == nil {
		fail("Semantic search is unavailable right now (no vector index or embedding backend).")
		return
	}

	// Resolve the chat client through the app's single llm_inferencer note;
	// fall back to the config.toml client so a broken inferencer note
	// degrades gracefully instead of blocking chat.
	client, res, err := llminferencer.ResolveChat(s.db.DB, "chat")
	if err != nil || client == nil {
		log.Printf("archive chat: resolve chat usage: %v", err)
		client = llm.NewFeatureChatClient("chat")
		res = llminferencer.ResolvedChat{Usage: "chat", Source: llminferencer.SourceConfig}
	}
	remote := res.Source == llminferencer.SourceNote
	routing := chatRouting{
		Source:   res.Source,
		Provider: res.Provider,
		Model:    res.Model,
		BaseURL:  res.BaseURL,
		Tier:     res.Tier,
		Warning:  res.Warning,
	}

	// Retrieve: embed the query, rank note chunks + attachment hits.
	queryTruncated := llm.TruncateForEmbedding(query)
	release := llm.BeginBackendUse(s.llm)
	vec, err := s.llm.GenerateEmbedding(queryTruncated)
	release()
	if err != nil {
		log.Printf("archive chat embedding error: %v", err)
		fail("Semantic search failed while embedding the question.")
		return
	}
	vecJSON := llm.EmbeddingToJSON(vec)

	scores, err := s.searchNoteChunkHits(vecJSON, in.Types)
	if err == nil {
		err = s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_ocr", "ocr_embedding", in.Types)
	}
	if err == nil {
		err = s.mergeAttachmentSearchHits(scores, vecJSON, "vss_files_stt", "stt_embedding", in.Types)
	}
	if err != nil {
		log.Printf("archive chat retrieval error: %v", err)
		fail("Retrieving matching notes failed.")
		return
	}

	candidates, err := s.buildSearchResultsFiltered(scores, query, nil, chatCandidateLimit)
	if err != nil {
		log.Printf("archive chat result build error: %v", err)
		fail("Loading matching notes failed.")
		return
	}

	// Privacy partition: remote providers never see high_private notes.
	var sources []chatSource
	bodies := make(map[int64]string, len(candidates))
	privateExcluded := 0
	for _, cand := range candidates {
		if remote && cand.HighPrivate {
			privateExcluded++
			continue
		}
		bodies[cand.ID] = cand.Body
		if len(sources) >= chatContextLimit {
			continue
		}
		sources = append(sources, chatSource{
			ID:          cand.ID,
			Title:       cand.Title,
			Type:        cand.Type,
			Path:        cand.Path,
			Distance:    cand.Distance,
			UpdatedAt:   cand.UpdatedAt,
			HighPrivate: cand.HighPrivate,
		})
	}

	if !send(chatStreamEvent{
		Type:            "sources",
		Sources:         sources,
		Routing:         &routing,
		PrivateExcluded: privateExcluded,
	}) {
		return
	}

	if len(sources) == 0 {
		answer := "No matching notes found in your archive."
		if privateExcluded > 0 {
			answer = fmt.Sprintf(
				"No matching notes found in your archive. %d highly private note(s) matched but are excluded from remote chat — pin the chat usage to the local LLM to include them.",
				privateExcluded)
		}
		if !send(chatStreamEvent{Type: "answer", Answer: answer}) {
			return
		}
		send(chatStreamEvent{Type: "done", Phase: "complete", Message: "Done.", PrivateExcluded: privateExcluded})
		return
	}

	// Build the RAG prompt: numbered excerpts the model must cite.
	context := buildChatContext(sources, bodies)
	messages := []llm.ChatMessage{
		{Role: "system", Content: chatSystemPrompt},
		{Role: "user", Content: "Note context:\n\n" + context + "\n\nQuestion: " + query},
	}

	release = llm.BeginBackendUse(client)
	answer, err := client.Complete(messages)
	release()
	if err != nil {
		log.Printf("archive chat completion error: %v", err)
		fail("The chat model failed to answer. " + err.Error())
		return
	}

	if !send(chatStreamEvent{Type: "answer", Answer: answer}) {
		return
	}
	send(chatStreamEvent{Type: "done", Phase: "complete", Message: "Done.", PrivateExcluded: privateExcluded})
}

// chatSystemPrompt instructs the model to answer strictly from the provided
// note excerpts and cite them.
const chatSystemPrompt = `You are the archive assistant of MentisEterna, a personal note-taking app. The user asks a question; relevant excerpts from their own notes are provided as context.

Rules:
- Answer the question using the provided note excerpts when they are relevant. Cite the notes you used inline as [N], where N is the source number in the context.
- If the excerpts do not contain the answer, say so plainly. Never invent note content.
- Keep the answer focused and concise. Answer in the language of the question.`

// buildChatContext renders the numbered excerpts sent to the model, capped
// per note and in total (runes). bodies maps note id → current body (the
// retrieval results already carry them; no extra queries needed).
func buildChatContext(sources []chatSource, bodies map[int64]string) string {
	var b strings.Builder
	remaining := chatContextRunes
	for i, src := range sources {
		bodyText := strings.TrimSpace(bodies[src.ID])
		if utf8.RuneCountInString(bodyText) > chatPerNoteRunes {
			bodyText = string([]rune(bodyText)[:chatPerNoteRunes]) + "…"
		}
		entry := fmt.Sprintf("[%d] %s", i+1, strings.TrimSpace(src.Title))
		if src.Path != "" && src.Path != src.Title {
			entry += " (" + src.Path + ")"
		}
		entry += "\n" + bodyText + "\n"
		if remaining-utf8.RuneCountInString(entry) < 0 {
			b.WriteString("…\n")
			break
		}
		remaining -= utf8.RuneCountInString(entry)
		b.WriteString(entry)
		if i < len(sources)-1 {
			b.WriteString("\n")
			remaining--
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
