package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/i5heu/MentisEterna/internal/config"
	"github.com/i5heu/MentisEterna/pkg/notetype"

	// Register all built-in note types (llm_inferencer is needed below).
	_ "github.com/i5heu/MentisEterna/pkg/notetype/builtins"
)

// ---------------------------------------------------------------------------
// Archive chat (POST /notes/chat — retrieval-augmented answers over notes)
// ---------------------------------------------------------------------------

// chatStubServer is an OpenAI-compatible chat backend recording the prompts
// it receives; it answers with a canned message.
type chatStubServer struct {
	mu       sync.Mutex
	requests []string
	server   *httptest.Server
}

func newChatStubServer(t *testing.T, answer string) *chatStubServer {
	t.Helper()
	stub := &chatStubServer{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, string(body))
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, answer)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *chatStubServer) lastRequest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return ""
	}
	return s.requests[len(s.requests)-1]
}

// parseChatStream decodes the NDJSON event stream of one archive chat call.
func parseChatStream(t *testing.T, w *httptest.ResponseRecorder) []chatStreamEvent {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("chat: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Fatalf("chat: content type = %q, want NDJSON", ct)
	}
	var events []chatStreamEvent
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		if line == "" {
			continue
		}
		var ev chatStreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("chat: decode event %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func chatEvent(events []chatStreamEvent, typ string) *chatStreamEvent {
	for i := range events {
		if events[i].Type == typ {
			return &events[i]
		}
	}
	return nil
}

func chatSourceIDs(ev *chatStreamEvent) []int64 {
	if ev == nil {
		return nil
	}
	ids := make([]int64, 0, len(ev.Sources))
	for _, src := range ev.Sources {
		ids = append(ids, src.ID)
	}
	return ids
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// setupChatServer returns a VSS-enabled server (skips without sqlite-vec)
// with plugin schemas and one indexed note pair: a public and a highly
// private note.
func setupChatServer(t *testing.T) (*Server, NoteDetail, NoteDetail) {
	t.Helper()
	s := newTestServerWithEmbedder(t)
	if err := notetype.Registry["llm_inferencer"].InitSchema(s.db.DB); err != nil {
		t.Fatalf("llm_inferencer schema: %v", err)
	}

	public := helperCreateNoteSync(t, s, "Public research", "The deployment uses kubernetes with a postgres cluster.", nil)
	private := helperCreateNoteSync(t, s, "Private diary", "My therapist SECRETMARKER-9271 said the kubernetes migration went well.", nil)
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/privacy", private.ID), strings.NewReader(`{"high_private":true}`))
	w := httptest.NewRecorder()
	s.setNotePrivacy(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("privacy toggle: %d: %s", w.Code, w.Body.String())
	}
	return s, public, private
}

// chatWithPosts sends one archive chat question through the handler.
func chatWithPost(t *testing.T, s *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/notes/chat", strings.NewReader(fmt.Sprintf(`{"query":%q}`, query)))
	w := httptest.NewRecorder()
	s.archiveChat(w, r)
	return w
}

func TestArchiveChatRemoteExcludesPrivateNotes(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	s, public, private := setupChatServer(t)

	stub := newChatStubServer(t, "The notes say the migration went well.")
	// An llm_inferencer note binds the chat usage to the remote stub.
	inferencer := helperCreateNoteRaw(t, s, fmt.Sprintf(`{
		"title":"Inferencer","body":"","type":"llm_inferencer",
		"custom_data":{
			"provider":"compatible",
			"base_url":%q,
			"api_key":"sk-chat",
			"default_tier":"small",
			"tiers":{"small":{"model":"prov/small"}},
			"usages":{"chat":{"tier":"small"}}
		}
	}`, stub.server.URL))
	_ = inferencer

	w := chatWithPost(t, s, "kubernetes migration")
	events := parseChatStream(t, w)

	sources := chatEvent(events, "sources")
	if sources == nil || sources.Routing == nil {
		t.Fatalf("no sources event in stream: %+v", events)
	}
	if sources.Routing.Source != "note" {
		t.Fatalf("routing source = %q, want remote note binding", sources.Routing.Source)
	}
	if sources.PrivateExcluded != 1 {
		t.Errorf("private_excluded = %d, want 1", sources.PrivateExcluded)
	}
	ids := chatSourceIDs(sources)
	if !containsID(ids, public.ID) {
		t.Errorf("public note %d missing from sources: %v", public.ID, ids)
	}
	if containsID(ids, private.ID) {
		t.Errorf("private note %d leaked into remote sources: %v", private.ID, ids)
	}

	answer := chatEvent(events, "answer")
	if answer == nil || answer.Answer != "The notes say the migration went well." {
		t.Fatalf("answer event wrong: %+v", answer)
	}

	// The remote provider never saw the private note's content.
	req := stub.lastRequest()
	if req == "" {
		t.Fatal("remote stub received no chat request")
	}
	if strings.Contains(req, "SECRETMARKER-9271") {
		t.Error("remote provider received the private note's body")
	}
	if !strings.Contains(req, "kubernetes with a postgres cluster") {
		t.Error("remote provider should receive the public note's body")
	}
}

func TestArchiveChatLocalIncludesPrivateNotes(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	s, public, private := setupChatServer(t)

	stub := newChatStubServer(t, "Local answer.")
	// The chat usage is pinned to the local config.toml system: its base URL
	// points at the stub, and the inferencer note pins {local: true}.
	config.Get().LLM.BaseURL = stub.server.URL
	helperCreateNoteRaw(t, s, `{
		"title":"Inferencer","body":"","type":"llm_inferencer",
		"custom_data":{
			"provider":"compatible",
			"base_url":"http://127.0.0.1:9",
			"default_tier":"small",
			"tiers":{"small":{"model":"prov/small"}},
			"usages":{"chat":{"local":true}}
		}
	}`)

	w := chatWithPost(t, s, "kubernetes migration")
	events := parseChatStream(t, w)

	sources := chatEvent(events, "sources")
	if sources == nil || sources.Routing == nil {
		t.Fatalf("no sources event in stream: %+v", events)
	}
	if sources.Routing.Source != "local" {
		t.Fatalf("routing source = %q, want local pin", sources.Routing.Source)
	}
	ids := chatSourceIDs(sources)
	if !containsID(ids, public.ID) || !containsID(ids, private.ID) {
		t.Errorf("local chat must include both notes, got %v", ids)
	}
	for _, src := range sources.Sources {
		if src.ID == private.ID && !src.HighPrivate {
			t.Error("private source must carry the high_private flag")
		}
	}

	// The local backend saw everything — including the private note.
	req := stub.lastRequest()
	if req == "" {
		t.Fatal("local stub received no chat request")
	}
	if !strings.Contains(req, "SECRETMARKER-9271") {
		t.Error("local LLM should receive the private note's body")
	}
}

func TestArchiveChatRequiresQuery(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/notes/chat", strings.NewReader(`{"query":"  "}`))
	w := httptest.NewRecorder()
	s.archiveChat(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty query, got %d", w.Code)
	}
}

func TestArchiveChatWithoutVectorIndex(t *testing.T) {
	// No embedder → the stream reports the error instead of crashing.
	s := newTestServer(t)
	w := chatWithPost(t, s, "anything")
	events := parseChatStream(t, w)
	if chatEvent(events, "error") == nil {
		t.Fatalf("expected an error event without VSS, got %+v", events)
	}
}

// ---------------------------------------------------------------------------
// Related notes (GET /notes/:id/related)
// ---------------------------------------------------------------------------

func TestGetNoteRelated(t *testing.T) {
	s := newTestServerWithEmbedder(t)

	root := helperCreateNoteSync(t, s, "Root project", "kubernetes postgres body", nil)
	child := helperCreateNoteSync(t, s, "Child thread", "kubernetes postgres body", &root.ID)
	other := helperCreateNoteSync(t, s, "Unrelated sibling", "kubernetes postgres body", nil)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%d/related", child.ID), nil)
	w := httptest.NewRecorder()
	s.getNoteRelated(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("related: %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Available bool          `json:"available"`
		Results   []RelatedNote `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available {
		t.Fatal("related must report available=true with VSS")
	}
	var ids []int64
	for _, res := range resp.Results {
		ids = append(ids, res.ID)
	}
	if !containsID(ids, other.ID) {
		t.Errorf("semantic sibling %d missing from related: %v", other.ID, ids)
	}
	if containsID(ids, child.ID) {
		t.Error("the note itself must be excluded")
	}
	if containsID(ids, root.ID) {
		t.Error("ancestors must be excluded (already visible as parents)")
	}
}

func TestGetNoteRelatedMissingNote(t *testing.T) {
	s := newTestServerWithEmbedder(t)
	r := httptest.NewRequest(http.MethodGet, "/notes/99999/related", nil)
	w := httptest.NewRecorder()
	s.getNoteRelated(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Privacy flag (POST /notes/:id/privacy, high_private on create/update)
// ---------------------------------------------------------------------------

func TestSetNotePrivacyToggle(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Secrets", "private body", nil)

	// On.
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/privacy", n.ID), strings.NewReader(`{"high_private":true}`))
	w := httptest.NewRecorder()
	s.setNotePrivacy(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("privacy on: %d: %s", w.Code, w.Body.String())
	}
	var detail NoteDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !detail.HighPrivate {
		t.Error("privacy toggle must report high_private=true")
	}

	// The flag survives a plain body-only update (field omitted).
	r = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(`{"title":"Secrets","body":"edited"}`))
	w = httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !detail.HighPrivate {
		t.Error("high_private must survive updates that omit the field")
	}

	// Off.
	r = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%d/privacy", n.ID), strings.NewReader(`{"high_private":false}`))
	w = httptest.NewRecorder()
	s.setNotePrivacy(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("privacy off: %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.HighPrivate {
		t.Error("privacy toggle must clear the flag")
	}
}

func TestCreateNoteWithHighPrivate(t *testing.T) {
	s := newTestServer(t)
	n, err := s.createNoteCore(createNoteInput{
		Title:       "Born private",
		Body:        "body",
		Type:        "standard",
		HighPrivate: true,
	})
	if err != nil {
		t.Fatalf("createNoteCore: %v", err)
	}
	if !n.HighPrivate {
		t.Error("created note must carry the high_private flag")
	}

	// The flag also flows through note summaries (search/list shape).
	var flag bool
	if err := s.db.QueryRow(`SELECT high_private FROM notes WHERE id = ?`, n.ID).Scan(&flag); err != nil || !flag {
		t.Fatalf("persisted flag: %v (want true)", err)
	}
}

func TestUpdateNoteCanChangeHighPrivate(t *testing.T) {
	s := newTestServer(t)
	n := helperCreateNote(t, s, "Toggle me", "body", nil)

	r := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/notes/%d", n.ID), strings.NewReader(`{"title":"Toggle me","body":"body","high_private":true}`))
	w := httptest.NewRecorder()
	s.updateNote(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", w.Code, w.Body.String())
	}
	var detail NoteDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !detail.HighPrivate {
		t.Error("update must be able to set high_private")
	}
}
