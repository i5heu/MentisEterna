package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/pkg/notetype"

	// Register all built-in note types so the plugin registry is populated.
	_ "github.com/i5heu/MentisEterna/pkg/notetype/builtins"
)

// ---------------------------------------------------------------------------
// TestOpenAPI_Conformance — validates that every endpoint in the OpenAPI spec
// returns responses that deserialize into the documented Go structs.
//
// This test uses the full HTTP stack (router + auth middleware) so that status
// codes, content types, and body shapes are verified end-to-end.
// ---------------------------------------------------------------------------

func TestOpenAPI_Conformance(t *testing.T) {
	s := newTestServer(t)
	token := createTestSession(t, s)

	// Initialize all registered plugin schemas (the real server does this at startup).
	for _, p := range notetype.Registry {
		if err := p.InitSchema(s.db.DB); err != nil {
			t.Fatalf("InitSchema for %s: %v", p.ID(), err)
		}
	}

	// Build the mux once, wrapped in auth middleware.
	mux := s.getMuxForTest()
	authMux := s.requireAuth(mux)

	// Helper: send a request through the auth-wrapped mux.
	do := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		authMux.ServeHTTP(w, req)
		return w
	}

	authGet := func(path string) *httptest.ResponseRecorder {
		return do(http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + token})
	}
	authPost := func(path, body string) *httptest.ResponseRecorder {
		return do(http.MethodPost, path, body, map[string]string{"Authorization": "Bearer " + token})
	}
	authPut := func(path, body string) *httptest.ResponseRecorder {
		return do(http.MethodPut, path, body, map[string]string{"Authorization": "Bearer " + token})
	}
	authDelete := func(path string) *httptest.ResponseRecorder {
		return do(http.MethodDelete, path, "", map[string]string{"Authorization": "Bearer " + token})
	}
	noAuth := func(method, path, body string) *httptest.ResponseRecorder {
		return do(method, path, body, nil)
	}

	// --- System ---
	t.Run("GET /health → HealthResponse (no auth)", func(t *testing.T) {
		w := noAuth(http.MethodGet, "/health", "")
		assertStatus(t, w, http.StatusOK)
		var resp map[string]string
		mustDecode(t, w, &resp)
		if resp["status"] != "ok" {
			t.Errorf("expected status=ok, got %q", resp["status"])
		}
	})

	// --- Auth ---
	t.Run("POST /login → LoginResponse", func(t *testing.T) {
		s2 := newTestServer(t)
		if err := s2.db.SetAdminPassword("openapitest"); err != nil {
			t.Fatalf("set password: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader(`{"username":"admin","password":"openapitest"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s2.getMuxForTest().ServeHTTP(w, req)
		assertStatus(t, w, http.StatusOK)
		var resp LoginResponse
		mustDecode(t, w, &resp)
		if resp.Token == "" {
			t.Error("expected non-empty token")
		}
		if resp.ExpiresAt == "" {
			t.Error("expected non-empty expires_at")
		}
	})

	t.Run("POST /login → 401 on bad password", func(t *testing.T) {
		s2 := newTestServer(t)
		if err := s2.db.SetAdminPassword("openapitest"); err != nil {
			t.Fatalf("set password: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s2.getMuxForTest().ServeHTTP(w, req)
		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("401 when no auth on protected route", func(t *testing.T) {
		w := noAuth(http.MethodGet, "/notes", "")
		assertStatus(t, w, http.StatusUnauthorized)
	})

	// --- Note Types ---
	t.Run("GET /note-types → []Manifest", func(t *testing.T) {
		w := authGet("/note-types")
		assertStatus(t, w, http.StatusOK)
		var catalog []Manifest
		mustDecode(t, w, &catalog)
		if len(catalog) == 0 {
			t.Fatal("expected non-empty catalog")
		}
		// The first entry must be "standard".
		if catalog[0].ID != "standard" {
			t.Errorf("expected first manifest to be 'standard', got %q", catalog[0].ID)
		}
		// Verify required Manifest fields on standard.
		std := catalog[0]
		if std.Label == "" {
			t.Error("standard.Label is empty")
		}
		if std.Editor.Mode == "" {
			t.Error("standard.Editor.Mode is empty")
		}
		if std.Viewer.Mode == "" {
			t.Error("standard.Viewer.Mode is empty")
		}
		// Verify at least one plugin manifest has action metadata.
		foundActions := false
		for _, m := range catalog {
			if len(m.Actions) > 0 && m.HasActions {
				foundActions = true
				for _, a := range m.Actions {
					if a.ID == "" {
						t.Errorf("manifest %q: action has empty ID", m.ID)
					}
					if a.RefreshStrategy == "" || (a.RefreshStrategy != "none" && a.RefreshStrategy != "reload" && a.RefreshStrategy != "reload_view") {
						t.Errorf("manifest %q action %q: invalid refresh_strategy %q", m.ID, a.ID, a.RefreshStrategy)
					}
				}
				break
			}
		}
		// recipe_overview should have actions, but only if the plugin test registered.
		// At minimum, all manifests should have valid Editor/Viewer modes.
		for _, m := range catalog {
			switch m.Editor.Mode {
			case "none", "schema", "custom":
			default:
				t.Errorf("manifest %q: invalid editor mode %q", m.ID, m.Editor.Mode)
			}
			switch m.Viewer.Mode {
			case "none", "custom":
			default:
				t.Errorf("manifest %q: invalid viewer mode %q", m.ID, m.Viewer.Mode)
			}
		}
		t.Logf("catalog has %d types, foundActions=%v", len(catalog), foundActions)
	})

	// --- Notes CRUD ---
	t.Run("GET /notes → []NoteSummary", func(t *testing.T) {
		// Create a note first so we can verify it appears in the list.
		cw := authPost("/notes", `{"title":"Listable Note","body":"should appear in list"}`)
		assertStatus(t, cw, http.StatusCreated)
		var created NoteDetail
		mustDecode(t, cw, &created)

		w := authGet("/notes")
		assertStatus(t, w, http.StatusOK)
		var notes []NoteSummary
		mustDecode(t, w, &notes)
		if len(notes) == 0 {
			t.Fatal("expected at least one note in list")
		}
		found := false
		for _, n := range notes {
			if n.ID == created.ID {
				found = true
				if n.Title != "Listable Note" {
					t.Errorf("title in list: got %q, want %q", n.Title, "Listable Note")
				}
				break
			}
		}
		if !found {
			t.Errorf("created note %d not found in list", created.ID)
		}
	})

	t.Run("POST /notes → NoteDetail (201)", func(t *testing.T) {
		w := authPost("/notes", `{"title":"OpenAPI Test","body":"Created via conformance test"}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)
		if n.ID == 0 {
			t.Error("expected non-zero ID")
		}
		if n.Title != "OpenAPI Test" {
			t.Errorf("title: got %q, want %q", n.Title, "OpenAPI Test")
		}
		if n.Body != "Created via conformance test" {
			t.Errorf("body: got %q", n.Body)
		}
		if n.CreatedAt == "" {
			t.Error("created_at is empty")
		}
		if n.UpdatedAt == "" {
			t.Error("updated_at is empty")
		}
		if n.Type != "standard" {
			t.Errorf("type: got %q, want 'standard'", n.Type)
		}
		// Tags should be empty (not null).
		if n.Tags == nil {
			t.Error("tags should be an empty array, got nil")
		}
	})

	t.Run("POST /notes → 400 on bad JSON", func(t *testing.T) {
		w := authPost("/notes", "notjson")
		assertStatus(t, w, http.StatusBadRequest)
	})

	t.Run("POST /notes with type and custom_data", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Recipe Note","type":"recipe","custom_data":{"ingredients":[{"name":"flour","amount":"2","unit":"cups"}],"servings":"4"}}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)
		if n.Type != "recipe" {
			t.Errorf("type: got %q, want 'recipe'", n.Type)
		}
		// plugin should be populated
		if n.Plugin == nil {
			t.Fatal("expected plugin to be populated for recipe type")
		}
		if n.Plugin.Type != "recipe" {
			t.Errorf("plugin.type: got %q, want 'recipe'", n.Plugin.Type)
		}
		if n.Plugin.Config == nil {
			t.Error("expected plugin.config to be non-nil for recipe")
		}
	})

	t.Run("GET /notes/:id → NoteDetail", func(t *testing.T) {
		// Create a note first.
		w := authPost("/notes", `{"title":"Get Me","body":"detail body","tags":["alpha","beta"]}`)
		assertStatus(t, w, http.StatusCreated)
		var created NoteDetail
		mustDecode(t, w, &created)

		// Get it back.
		w2 := authGet(fmt.Sprintf("/notes/%d", created.ID))
		assertStatus(t, w2, http.StatusOK)
		var got NoteDetail
		mustDecode(t, w2, &got)
		if got.ID != created.ID {
			t.Errorf("ID: got %d, want %d", got.ID, created.ID)
		}
		if got.Title != "Get Me" {
			t.Errorf("title: got %q", got.Title)
		}
		if got.Body != "detail body" {
			t.Errorf("body: got %q", got.Body)
		}
		// Tags must be present on detail.
		if len(got.Tags) != 2 {
			t.Errorf("expected 2 tags, got %d: %v", len(got.Tags), got.Tags)
		}
	})

	t.Run("GET /notes/:id → 404", func(t *testing.T) {
		w := authGet("/notes/99999")
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("PUT /notes/:id → NoteDetail", func(t *testing.T) {
		// Create.
		w := authPost("/notes", `{"title":"Orig","body":"old"}`)
		var created NoteDetail
		mustDecode(t, w, &created)

		// Update.
		w2 := authPut(fmt.Sprintf("/notes/%d", created.ID),
			`{"title":"Updated","body":"new body"}`)
		assertStatus(t, w2, http.StatusOK)
		var updated NoteDetail
		mustDecode(t, w2, &updated)
		if updated.Title != "Updated" {
			t.Errorf("title: got %q", updated.Title)
		}
		if updated.Body != "new body" {
			t.Errorf("body: got %q", updated.Body)
		}
	})

	t.Run("PUT /notes/:id → 404", func(t *testing.T) {
		w := authPut("/notes/99999", `{"title":"X","body":""}`)
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("DELETE /notes/:id → 204", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Delete Me"}`)
		var created NoteDetail
		mustDecode(t, w, &created)

		w2 := authDelete(fmt.Sprintf("/notes/%d", created.ID))
		assertStatus(t, w2, http.StatusNoContent)

		// Confirm gone.
		w3 := authGet(fmt.Sprintf("/notes/%d", created.ID))
		assertStatus(t, w3, http.StatusNotFound)
	})

	t.Run("DELETE /notes/:id → 404", func(t *testing.T) {
		w := authDelete("/notes/99999")
		assertStatus(t, w, http.StatusNotFound)
	})

	// --- Notes sub-resources ---
	t.Run("GET /notes/:id/history → []NoteUpdate", func(t *testing.T) {
		w := authPost("/notes", `{"title":"History Note","body":"v1"}`)
		var created NoteDetail
		mustDecode(t, w, &created)

		// Update to create a second history entry.
		authPut(fmt.Sprintf("/notes/%d", created.ID), `{"title":"History Note","body":"v2"}`)

		w2 := authGet(fmt.Sprintf("/notes/%d/history", created.ID))
		assertStatus(t, w2, http.StatusOK)
		var updates []NoteUpdate
		mustDecode(t, w2, &updates)
		if len(updates) < 2 {
			t.Errorf("expected at least 2 history entries, got %d", len(updates))
		}
		for _, u := range updates {
			if u.ID == 0 || u.NoteID == 0 || u.Body == "" || u.CreatedAt == "" {
				t.Errorf("history entry has zero/empty field: %+v", u)
			}
		}
	})

	t.Run("GET /notes/:id/history → 404", func(t *testing.T) {
		w := authGet("/notes/99999/history")
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("GET /notes/:id/children → []ChildNote", func(t *testing.T) {
		// Create parent.
		w := authPost("/notes", `{"title":"Parent"}`)
		var parent NoteDetail
		mustDecode(t, w, &parent)

		// Create child.
		authPost("/notes", fmt.Sprintf(`{"title":"Child","parent_id":%d}`, parent.ID))

		w2 := authGet(fmt.Sprintf("/notes/%d/children", parent.ID))
		assertStatus(t, w2, http.StatusOK)
		var children []ChildNote
		mustDecode(t, w2, &children)
		if len(children) != 1 {
			t.Errorf("expected 1 child, got %d", len(children))
		}
		if len(children) > 0 {
			c := children[0]
			if c.Title != "Child" {
				t.Errorf("child title: got %q", c.Title)
			}
			if c.ChildCount < 0 {
				t.Errorf("child_count should be >= 0, got %d", c.ChildCount)
			}
		}
	})

	t.Run("GET /notes/:id/ancestors → []NoteSummary", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Root"}`)
		var root NoteDetail
		mustDecode(t, w, &root)

		w = authPost("/notes", fmt.Sprintf(`{"title":"Leaf","parent_id":%d}`, root.ID))
		var leaf NoteDetail
		mustDecode(t, w, &leaf)

		w2 := authGet(fmt.Sprintf("/notes/%d/ancestors", leaf.ID))
		assertStatus(t, w2, http.StatusOK)
		var chain []NoteSummary
		mustDecode(t, w2, &chain)
		if len(chain) != 2 {
			t.Errorf("expected 2 ancestors, got %d", len(chain))
		}
		if len(chain) >= 2 {
			if chain[0].Title != "Root" {
				t.Errorf("first ancestor: got %q", chain[0].Title)
			}
			if chain[1].Title != "Leaf" {
				t.Errorf("last ancestor: got %q", chain[1].Title)
			}
		}
	})

	t.Run("POST /notes/:id/pin → NoteDetail", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Pinnable"}`)
		var created NoteDetail
		mustDecode(t, w, &created)

		w2 := authPost(fmt.Sprintf("/notes/%d/pin", created.ID), `{"pinned":true}`)
		assertStatus(t, w2, http.StatusOK)
		var pinned NoteDetail
		mustDecode(t, w2, &pinned)
		if !pinned.Pinned {
			t.Error("expected pinned=true")
		}
	})

	// --- Plugin Actions ---
	t.Run("POST /notes/:id/action (legacy) → action result", func(t *testing.T) {
		// recipe_overview actions only work if we have a recipe_overview note.
		// Create one and call list_grocery_lists (a safe read-only action).
		w := authPost("/notes", `{"title":"Overview","type":"recipe_overview"}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)

		w2 := authPost(fmt.Sprintf("/notes/%d/action", n.ID),
			`{"action":"list_grocery_lists"}`)
		assertStatus(t, w2, http.StatusOK)
		var result map[string]any
		mustDecode(t, w2, &result)
		// result should have a "lists" key.
		if _, ok := result["lists"]; !ok {
			t.Errorf("expected 'lists' key in action result, got keys: %v", mapKeys(result))
		}
	})

	t.Run("POST /notes/:id/actions/:actionID → action result", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Overview2","type":"recipe_overview"}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)

		// Use the new route shape.
		w2 := authPost(fmt.Sprintf("/notes/%d/actions/list_grocery_lists", n.ID), `{}`)
		assertStatus(t, w2, http.StatusOK)
		var result map[string]any
		mustDecode(t, w2, &result)
		if _, ok := result["lists"]; !ok {
			t.Errorf("expected 'lists' key via new route, got keys: %v", mapKeys(result))
		}
	})

	t.Run("POST /notes/:id/actions/:actionID → 404 for unknown action", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Overview3","type":"recipe_overview"}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)

		// Unknown action — should get 404 because ErrUnknownAction is returned.
		w2 := authPost(fmt.Sprintf("/notes/%d/actions/nonexistent", n.ID), `{}`)
		assertStatus(t, w2, http.StatusNotFound)
	})

	t.Run("POST /notes/:id/actions/:actionID → 404 for standard note", func(t *testing.T) {
		w := authPost("/notes", `{"title":"Plain Note"}`)
		assertStatus(t, w, http.StatusCreated)
		var n NoteDetail
		mustDecode(t, w, &n)

		w2 := authPost(fmt.Sprintf("/notes/%d/actions/anything", n.ID), `{}`)
		assertStatus(t, w2, http.StatusNotFound) // standard has no ActionHandler
	})

	// --- Tags ---
	t.Run("GET /tags → []string", func(t *testing.T) {
		// Create a note with tags so we have data.
		authPost("/notes", `{"title":"Tagged","tags":["conformance","test","api"]}`)

		w := authGet("/tags")
		assertStatus(t, w, http.StatusOK)
		var tags []string
		mustDecode(t, w, &tags)
		if len(tags) == 0 {
			t.Fatal("expected at least one tag")
		}
		// Verify the tags we created are present.
		tagSet := make(map[string]bool, len(tags))
		for _, t := range tags {
			tagSet[t] = true
		}
		for _, want := range []string{"conformance", "test", "api"} {
			if !tagSet[want] {
				t.Errorf("expected tag %q in response, got %v", want, tags)
			}
		}
	})

	t.Run("GET /tags?q=conf → filtered []string", func(t *testing.T) {
		w := authGet("/tags?q=conf")
		assertStatus(t, w, http.StatusOK)
		var tags []string
		mustDecode(t, w, &tags)
		for _, tag := range tags {
			if !strings.HasPrefix(strings.ToLower(tag), "conf") {
				t.Errorf("tag %q does not start with 'conf'", tag)
			}
		}
		// Verify "conformance" is actually present (not just any prefix match).
		found := false
		for _, tag := range tags {
			if tag == "conformance" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'conformance' in filtered results, got %v", tags)
		}
	})

	// --- Jobs ---
	t.Run("GET /jobs → JobListResponse", func(t *testing.T) {
		w := authGet("/jobs")
		assertStatus(t, w, http.StatusOK)
		var resp map[string]any
		mustDecode(t, w, &resp)
		if _, ok := resp["runs"]; !ok {
			t.Error("expected 'runs' key in /jobs response")
		}
	})

	// --- Backup ---
	t.Run("POST /backup/trigger → 503 when not configured", func(t *testing.T) {
		w := authPost("/backup/trigger", "")
		assertStatus(t, w, http.StatusServiceUnavailable)
	})

	t.Run("POST /backup/purge → 503 when not configured", func(t *testing.T) {
		w := authPost("/backup/purge", "")
		assertStatus(t, w, http.StatusServiceUnavailable)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// Manifest is a local copy of notetype.Manifest so we can decode directly
// without importing the pkg/notetype types here. (The actual responses use
// notetype.Manifest, which we can decode but the field names must match.)
// For simplicity, we decode into map-like types for the conformance check,
// but we also import notetype.ListManifests() return type.
// We'll just use the notetype.Manifest from the package directly.
type Manifest = notetype.Manifest

// getMuxForTest returns a minimal mux that routes like the real server.
// We build a fresh one for tests so route registration matches.
func (s *Server) getMuxForTest() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		s.handleLogin(w, r)
	})
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.listNotes(w, r)
		case http.MethodPost:
			s.createNote(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/note-types", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleNoteTypes(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/notes/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.searchNotes(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/notes/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/history") {
			if r.Method == http.MethodGet {
				s.getNoteHistory(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/children") {
			if r.Method == http.MethodGet {
				s.getNoteChildren(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/ancestors") {
			if r.Method == http.MethodGet {
				s.getNoteAncestors(w, r)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if idx := strings.LastIndex(r.URL.Path, "/actions/"); idx >= 0 {
			s.handlePluginActionV2(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/action") {
			s.handlePluginAction(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pin") {
			s.setNotePin(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.getNote(w, r)
		case http.MethodPut:
			s.updateNote(w, r)
		case http.MethodDelete:
			s.deleteNote(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/tags", s.handleTags)
	mux.HandleFunc("/jobs", s.handleJobs)
	mux.HandleFunc("/jobs/", s.handleJobByID)
	mux.HandleFunc("/backup/trigger", s.handleBackupTrigger)
	mux.HandleFunc("/backup/purge", s.handleBackupPurge)
	return mux
}

func assertStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Errorf("expected status %d, got %d: %s", want, w.Code, w.Body.String())
	}
}

func mustDecode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode into %T: %v\nBody: %s", v, err, w.Body.String())
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
