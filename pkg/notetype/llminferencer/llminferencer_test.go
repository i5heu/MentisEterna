package llminferencer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype"
	"github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

// --- helpers ---

const validPayload = `{
	"provider": "openrouter",
	"base_url": "",
	"api_key": "sk-test-123",
	"default_tier": "middle",
	"tiers": {
		"big":    {"model": "prov/big"},
		"middle": {"model": "prov/mid"},
		"small":  {"model": "prov/small"},
		"tiny":   {"model": "prov/tiny"}
	},
	"usages": {
		"rss_classify": {"tier": "small"}
	}
}`

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d := plugintest.DB(t, notetype.Registry[PluginID])
	return d.DB
}

// createNote inserts a note of the given type (optionally under a parent;
// 0 = root, stored as NULL).
func createNote(t *testing.T, db *sql.DB, title, noteType string, parentID int64) int64 {
	t.Helper()
	var res sql.Result
	var err error
	if parentID == 0 {
		res, err = db.Exec(`INSERT INTO notes (title, type, parent_id) VALUES (?, ?, NULL)`, title, noteType)
	} else {
		res, err = db.Exec(`INSERT INTO notes (title, type, parent_id) VALUES (?, ?, ?)`, title, noteType, parentID)
	}
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// saveCfg saves a config payload for a note inside a transaction.
func saveCfg(t *testing.T, db *sql.DB, noteID int64, payload string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	p := notetype.Registry[PluginID].(notetype.ConfigSaver)
	if len(payload) > 0 {
		if err := p.SaveConfig(context.Background(), tx, 0, noteID, json.RawMessage(payload)); err != nil {
			tx.Rollback()
			t.Fatalf("SaveConfig: %v", err)
		}
	} else {
		if err := p.SaveConfig(context.Background(), tx, 0, noteID, nil); err != nil {
			tx.Rollback()
			t.Fatalf("SaveConfig(nil): %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// loadCfgJSON loads a note's config via the plugin's ConfigLoader.
func loadCfgJSON(t *testing.T, db *sql.DB, noteID int64) map[string]any {
	t.Helper()
	loaded, err := notetype.Registry[PluginID].(notetype.ConfigLoader).LoadConfig(context.Background(), db, 0, noteID)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(loaded, &out); err != nil {
		t.Fatalf("unmarshal loaded config: %v", err)
	}
	return out
}

// --- harness battery ---

func TestLlmInferencerPlugin(t *testing.T) {
	plugintest.Run(t, notetype.Registry[PluginID], plugintest.TestData{
		ValidPayload:   validPayload,
		InvalidPayload: `{"provider":"not-a-provider","tiers":{"big":{"model":"x"}}}`,
	})
}

// --- config validation ---

func TestValidateConfigRules(t *testing.T) {
	p := &Plugin{}
	bad := []string{
		`{"provider":"not-a-provider"}`,
		`{"provider":"compatible"}`,                                                 // no base_url
		`{"provider":"mistral","base_url":"ftp://x"}`,                               // scheme
		`{"provider":"openrouter","default_tier":"huge"}`,                           // bad tier
		`{"provider":"openrouter","tiers":{"huge":{"model":"x"}}}`,                  // unknown tier
		`{"provider":"openrouter","tiers":{"big":{"model":""}}}`,                    // empty model
		`{"provider":"openrouter","tiers":{"big":{"model":"x","base_url":"nope"}}}`, // bad tier base URL
		`{"provider":"openrouter","usages":{"does-not-exist":{"tier":"big"}}}`,      // unknown usage
		fmt.Sprintf(`{"provider":"openrouter","tiers":{"big":{"model":"m"}},"usages":{"rss_classify":{"tier":"big","model":"direct"}}}`), // both
		fmt.Sprintf(`{"provider":"openrouter","tiers":{"big":{"model":"m"}},"usages":{"%s":{"tier":"nope"}}}`, strings.Repeat("x", 50)),  // bad usage tier
		`{"provider":"openrouter","usages":{"rss_classify":{}}}`,                                                                         // empty binding
		`{"provider":"openrouter","usages":[{}}]}`,                                                                                       // malformed
	}
	for i, payload := range bad {
		if err := p.ValidateConfig(json.RawMessage(strings.TrimSpace(payload))); err == nil {
			t.Errorf("case %d: expected validation error for %s", i, payload)
		}
	}
	good := []string{
		``,
		`{"provider":"openrouter"}`,
		`{"provider":"mistral","base_url":"https://api.mistral.eu/v1"}`,
		`{"provider":"compatible","base_url":"http://192.168.1.5:8080"}`,
		`{"provider":"openrouter","api_key":"sk-live","tiers":{"tiny":{"model":"m"}},"usages":{"title":{"model":"prov/big"}}}`,
		`{"provider":"openrouter","default_tier":"tiny","tiers":{"tiny":{"model":"m"}}}`,
	}
	for i, payload := range good {
		if err := p.ValidateConfig(json.RawMessage(payload)); err != nil {
			t.Errorf("case %d: unexpected error for %q: %v", i, payload, err)
		}
	}
}

// --- write-only API key ---

func TestAPIKeyIsWriteOnly(t *testing.T) {
	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	saveCfg(t, db, noteID, validPayload)

	// Loaded config NEVER contains the key; api_key_set reports stored state.
	loaded := loadCfgJSON(t, db, noteID)
	if key, ok := loaded["api_key"].(string); ok && key != "" {
		t.Fatalf("LoadConfig leaked the API key: %q", key)
	}
	if set, _ := loaded["api_key_set"].(bool); !set {
		t.Fatal("api_key_set must be true after saving a key")
	}
	if raw, _ := json.Marshal(loaded); strings.Contains(string(raw), "sk-test-123") {
		t.Fatal("serialized config contains the raw key")
	}

	// Round-trip through the UI keeps the key: empty api_key on save = keep.
	keep := map[string]any{
		"provider":     "openrouter",
		"base_url":     "",
		"api_key":      "",
		"default_tier": "middle",
		"tiers":        map[string]any{"middle": map[string]any{"model": "prov/mid"}},
		"usages":       map[string]any{},
	}
	payload, _ := json.Marshal(keep)
	saveCfg(t, db, noteID, string(payload))

	key, has, err := storedAPIKey(db, noteID)
	if err != nil || !has {
		t.Fatalf("stored key lost after empty-key save: has=%v err=%v", has, err)
	}
	if key != "sk-test-123" {
		t.Fatalf("stored key = %q, want the original", key)
	}

	// A new non-empty key replaces the old one.
	keep["api_key"] = "sk-rotated"
	payload, _ = json.Marshal(keep)
	saveCfg(t, db, noteID, string(payload))
	key, has, err = storedAPIKey(db, noteID)
	if err != nil || !has || key != "sk-rotated" {
		t.Fatalf("stored key = %q has=%v err=%v, want rotated", key, has, err)
	}

	// The clear action wipes it.
	p := notetype.Registry[PluginID].(notetype.ActionHandler)
	res, err := p.HandleAction(context.Background(), db, 0, noteID, "clear_api_key", nil)
	if err != nil {
		t.Fatalf("clear_api_key: %v", err)
	}
	m := res.(map[string]any)
	if cleared, _ := m["cleared"].(bool); !cleared {
		t.Error("clear_api_key did not report cleared")
	}
	key, has, err = storedAPIKey(db, noteID)
	if err != nil || has || key != "" {
		t.Fatalf("key still stored after clear: %q has=%v err=%v", key, has, err)
	}
	loaded = loadCfgJSON(t, db, noteID)
	if set, _ := loaded["api_key_set"].(bool); set {
		t.Error("api_key_set must be false after clear")
	}

	// Idempotent: clearing again succeeds.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "clear_api_key", nil); err != nil {
		t.Errorf("second clear_api_key: %v", err)
	}
}

// --- binding rules (the maps, not the hierarchy) ---

func TestBindsUsageRules(t *testing.T) {
	base := &inferencerRow{
		NoteID: 1,
		Usages: map[string]UsageBinding{},
		Tiers:  map[string]TierBinding{},
	}

	// Nothing configured → no binding.
	if b := base.bindsUsage("rss_classify"); b != nil {
		t.Fatal("empty config must not bind")
	}

	// Explicit tier binding needs the tier's model.
	withTier := &inferencerRow{
		NoteID: 1,
		Usages: map[string]UsageBinding{"rss_classify": {Tier: "small"}},
		Tiers:  map[string]TierBinding{},
	}
	if b := withTier.bindsUsage("rss_classify"); b != nil {
		t.Fatal("binding to a tier without a model must not bind")
	}
	withTier.Tiers["small"] = TierBinding{Model: "prov/small"}
	b := withTier.bindsUsage("rss_classify")
	if b == nil || b.Model != "prov/small" || b.Tier != "small" || !b.Explicit {
		t.Fatalf("explicit tier binding: %+v", b)
	}

	// Direct model binding.
	withModel := &inferencerRow{
		NoteID: 1,
		Usages: map[string]UsageBinding{"rss_classify": {Model: "prov/other"}},
		Tiers:  map[string]TierBinding{"small": {Model: "prov/small"}},
	}
	b = withModel.bindsUsage("rss_classify")
	if b == nil || b.Model != "prov/other" || b.Tier != "" {
		t.Fatalf("direct model binding: %+v", b)
	}

	// Default tier binds unmapped usages.
	withDefault := &inferencerRow{
		NoteID:      1,
		Usages:      map[string]UsageBinding{},
		Tiers:       map[string]TierBinding{"middle": {Model: "prov/mid"}},
		DefaultTier: "middle",
	}
	b = withDefault.bindsUsage("title")
	if b == nil || b.Model != "prov/mid" || b.Tier != "middle" {
		t.Fatalf("default tier binding: %+v", b)
	}

	// Default tier without its model does not bind.
	brokenDefault := &inferencerRow{
		NoteID:      1,
		Usages:      map[string]UsageBinding{},
		Tiers:       map[string]TierBinding{},
		DefaultTier: "big",
	}
	if b := brokenDefault.bindsUsage("title"); b != nil {
		t.Fatal("default tier without model must not bind")
	}

	// Explicit binding overrides the default tier for other usages too.
	// (default applies only to usages without an explicit entry)
	mixed := &inferencerRow{
		NoteID:      1,
		Usages:      map[string]UsageBinding{"tags": {Tier: "tiny"}},
		Tiers:       map[string]TierBinding{"tiny": {Model: "prov/tiny"}, "middle": {Model: "prov/mid"}},
		DefaultTier: "middle",
	}
	if b := mixed.bindsUsage("tags"); b == nil || b.Model != "prov/tiny" {
		t.Fatalf("explicit beats default: %+v", b)
	}
	if b := mixed.bindsUsage("subtasks"); b == nil || b.Model != "prov/mid" {
		t.Fatalf("default fills unmapped: %+v", b)
	}
}

// --- hierarchy resolution ---

// resolveFor asserts resolution from an anchor and returns the client.
func resolveFor(t *testing.T, db *sql.DB, anchor int64, usage string) (*llm.ChatClient, ResolvedChat) {
	t.Helper()
	client, res, err := ResolveChat(db, anchor, usage)
	if err != nil {
		t.Fatalf("ResolveChat: %v", err)
	}
	if client == nil {
		t.Fatal("ResolveChat returned a nil client")
	}
	return client, res
}

func TestResolveFallbackToConfig(t *testing.T) {
	db := testDB(t)
	reader := createNote(t, db, "Reader", "rss", 0)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.Source != SourceConfig {
		t.Fatalf("source = %q, want config fallback", res.Source)
	}
	if res.Model != client.Model || client.Model != llm.FeatureModel("rss_classify") {
		t.Errorf("fallback model = %q, want the config feature model", client.Model)
	}
	if client.BaseURL != llm.FeatureBaseURL("rss_classify") {
		t.Errorf("fallback base URL = %q", client.BaseURL)
	}
	if client.APIKey == "" && strings.Contains(client.BaseURL, "localhost") == false {
		// No assertion to make; fallback clients are unkeyed by definition.
		_ = client
	}
}

func TestResolveSubtreeChildWins(t *testing.T) {
	db := testDB(t)
	reader := createNote(t, db, "Reader", "rss", 0)
	child := createNote(t, db, "Nested inferencer", PluginID, reader)
	saveCfg(t, db, child, validPayload)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.Source != SourceNote || res.InferencerNoteID != child {
		t.Fatalf("resolved %v, want the reader's child note %d", res, child)
	}
	if res.Tier != "small" || res.Model != "prov/small" || client.Model != "prov/small" {
		t.Errorf("tier resolution: res=%+v client=%+v", res, client)
	}
	if res.BaseURL != "https://openrouter.ai/api" || client.BaseURL != res.BaseURL {
		t.Errorf("provider default base URL: %q", res.BaseURL)
	}
	if client.APIKey != "sk-test-123" {
		t.Error("stored API key not applied to the client")
	}
}

func TestResolveSiblingAndAncestor(t *testing.T) {
	db := testDB(t)
	// root (implicit)
	// ├── News (folder-ish standard note)
	// │   ├── Reader (rss)          ← anchor
	// │   └── Local inferencer     ← sibling, binds via default tier
	// └── Root inferencer           ← top-level: bound only if nearer levels don't
	parent := createNote(t, db, "News", "standard", 0)
	createNote(t, db, "Root inferencer", PluginID, 0) // no config saved → does not bind
	reader := createNote(t, db, "Reader", "rss", parent)

	// Root-level note does not bind rss_classify (only default fallback later).
	// With nothing binding: config fallback.
	if _, res := resolveFor(t, db, reader, "rss_classify"); res.Source != SourceConfig {
		t.Fatalf("nothing binds yet: %v", res)
	}

	// A sibling with a default tier now binds.
	sibling := createNote(t, db, "Local inferencer", PluginID, parent)
	saveCfg(t, db, sibling, `{"provider":"mistral","default_tier":"big","tiers":{"big":{"model":"mistral/mega"}}}`)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.Source != SourceNote || res.InferencerNoteID != sibling {
		t.Fatalf("sibling default-tier binding: %v", res)
	}
	if client.Model != "mistral/mega" || client.BaseURL != "https://api.mistral.ai" {
		t.Errorf("mistral resolution: %+v", client)
	}
	if client.APIKey != "" {
		t.Error("no key stored; client must be unkeyed")
	}
}

func TestResolveNearestLevelWins(t *testing.T) {
	db := testDB(t)
	parent := createNote(t, db, "News", "standard", 0)
	reader := createNote(t, db, "Reader", "rss", parent)

	root := createNote(t, db, "Root inferencer", PluginID, 0)
	saveCfg(t, db, root, `{"provider":"openrouter","usages":{"rss_classify":{"tier":"tiny"}},"tiers":{"tiny":{"model":"prov/root-tiny"}}}`)

	uncle := createNote(t, db, "Uncle inferencer", PluginID, parent)
	saveCfg(t, db, uncle, `{"provider":"openrouter","usages":{"rss_classify":{"tier":"tiny"}},"tiers":{"tiny":{"model":"prov/uncle-tiny"}}}`)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.InferencerNoteID != uncle || client.Model != "prov/uncle-tiny" {
		t.Fatalf("nearest level must win: %+v", res)
	}
}

func TestResolveMultipleAtSameLevelLowestIDWins(t *testing.T) {
	db := testDB(t)
	parent := createNote(t, db, "News", "standard", 0)
	reader := createNote(t, db, "Reader", "rss", parent)

	a := createNote(t, db, "A", PluginID, parent)
	saveCfg(t, db, a, `{"provider":"openrouter","default_tier":"tiny","tiers":{"tiny":{"model":"prov/a"}}}`)
	b := createNote(t, db, "B", PluginID, parent)
	saveCfg(t, db, b, `{"provider":"openrouter","default_tier":"tiny","tiers":{"tiny":{"model":"prov/b"}}}`)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.InferencerNoteID != a || client.Model != "prov/a" {
		t.Fatalf("lowest id must win: %+v", res)
	}
	if res.Warning == "" || !strings.Contains(res.Warning, "multiple") {
		t.Errorf("expected multi-binding warning, got %q", res.Warning)
	}
}

func TestResolveIncompleteBindingFallsThrough(t *testing.T) {
	db := testDB(t)
	parent := createNote(t, db, "News", "standard", 0)
	reader := createNote(t, db, "Reader", "rss", parent)

	// Sibling binds rss_classify to a tier with NO model → must not capture.
	incomplete := createNote(t, db, "Incomplete", PluginID, parent)
	saveCfg(t, db, incomplete, `{"provider":"openrouter","usages":{"rss_classify":{"tier":"big"}}}`)

	// Root-level complete binding takes over.
	root := createNote(t, db, "Root inferencer", PluginID, 0)
	saveCfg(t, db, root, validPayload)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.InferencerNoteID != root || client.Model != "prov/small" {
		t.Fatalf("incomplete binding must fall through: %+v", res)
	}
}

func TestResolveTopLevelScopeIsLast(t *testing.T) {
	db := testDB(t)
	// implicit root (top level)
	// ├── Reader (rss)             ← top-level anchor
	// └── Global inferencer         ← top-level binder
	reader := createNote(t, db, "Reader", "rss", 0)
	global := createNote(t, db, "Global inferencer", PluginID, 0)
	saveCfg(t, db, global, validPayload)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if res.Source != SourceNote || res.InferencerNoteID != global {
		t.Fatalf("top-level binder must bind a top-level anchor: %+v", res)
	}
	if client.Model != "prov/small" {
		t.Errorf("model: %+v", res)
	}

	// A closer child beats the top-level binder.
	child := createNote(t, db, "Own inferencer", PluginID, reader)
	saveCfg(t, db, child, `{"provider":"openrouter","usages":{"rss_classify":{"model":"prov/own"}}}`)
	_, res = resolveFor(t, db, reader, "rss_classify")
	if res.InferencerNoteID != child {
		t.Fatalf("subtree must beat top level: %+v", res)
	}
}

func TestResolvePerTierBaseURLOverride(t *testing.T) {
	db := testDB(t)
	parent := createNote(t, db, "News", "standard", 0)
	reader := createNote(t, db, "Reader", "rss", parent)

	sibling := createNote(t, db, "Inferencer", PluginID, parent)
	saveCfg(t, db, sibling, `{
		"provider": "openrouter",
		"default_tier": "big",
		"tiers": {
			"big":  {"model": "prov/big",  "base_url": "http://127.0.0.1:1999/api"},
			"tiny": {"model": "prov/tiny"}
		}
	}`)

	client, res := resolveFor(t, db, reader, "rss_classify")
	if client.BaseURL != "http://127.0.0.1:1999/api" {
		t.Fatalf("per-tier base URL override ignored: %+v", client)
	}
	if res.Model != "prov/big" {
		t.Errorf("model: %+v", res)
	}
}

func TestResolveUnknownUsageErrors(t *testing.T) {
	db := testDB(t)
	if _, _, err := ResolveChat(db, 0, "no-such-usage"); err == nil {
		t.Fatal("unknown usage must error")
	}
}

func TestResolveUnwiredUsageStillResolves(t *testing.T) {
	db := testDB(t)
	// Deep wiring is opt-in at call sites, but configured mappings for
	// unwired usages resolve now so future wiring needs no migration.
	root := createNote(t, db, "Root inferencer", PluginID, 0)
	saveCfg(t, db, root, `{"provider":"openrouter","usages":{"title":{"model":"prov/title"}}}`)

	client, res := resolveFor(t, db, root, "title")
	if res.Source != SourceNote || client.Model != "prov/title" {
		t.Fatalf("unwired usage resolution: %+v", res)
	}
}

// --- actions against a live (httptest) provider ---

func testChatServer(t *testing.T, key string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
}

func TestActionTestSucceeds(t *testing.T) {
	srv := testChatServer(t, "sk-live-key")
	defer srv.Close()

	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	cfg := fmt.Sprintf(`{
		"provider": "compatible",
		"base_url": %q,
		"api_key": "sk-live-key",
		"default_tier": "tiny",
		"tiers": {"tiny": {"model": "prov/tiny"}, "big": {"model": "prov/big"}}
	}`, srv.URL)
	saveCfg(t, db, noteID, cfg)

	p := notetype.Registry[PluginID].(notetype.ActionHandler)

	// Default tier.
	resAny, err := p.HandleAction(context.Background(), db, 0, noteID, "test", nil)
	if err != nil {
		t.Fatalf("test action: %v", err)
	}
	res := resAny.(TestResult)
	if !res.OK || res.Reply != "OK" || res.Model != "prov/tiny" {
		t.Fatalf("test result: %+v", res)
	}
	if res.BaseURL != srv.URL || res.LatencyMS < 0 {
		t.Errorf("test result details: %+v", res)
	}

	// Explicit tier.
	resAny, err = p.HandleAction(context.Background(), db, 0, noteID, "test", json.RawMessage(`{"tier":"big"}`))
	if err != nil {
		t.Fatalf("test action (big): %v", err)
	}
	if res := resAny.(TestResult); !res.OK || res.Model != "prov/big" {
		t.Errorf("big tier test: %+v", res)
	}

	// Explicit model.
	resAny, err = p.HandleAction(context.Background(), db, 0, noteID, "test", json.RawMessage(`{"model":"prov/custom"}`))
	if err != nil {
		t.Fatalf("test action (model): %v", err)
	}
	if res := resAny.(TestResult); !res.OK || res.Model != "prov/custom" {
		t.Errorf("custom model test: %+v", res)
	}
}

func TestActionTestBadRequests(t *testing.T) {
	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	p := notetype.Registry[PluginID].(notetype.ActionHandler)

	// No config row yet.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "test", nil); err == nil || !strings.Contains(err.Error(), "save the note's config") {
		t.Fatalf("unsaved config: %v", err)
	}

	saveCfg(t, db, noteID, `{"provider":"openrouter","tiers":{"small":{"model":"prov/small"}}}`)

	// No tier/model and no default tier.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "test", nil); err == nil || !strings.Contains(err.Error(), "provide tier or model") {
		t.Fatalf("no tier: %v", err)
	}
	// Tier without model.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "test", json.RawMessage(`{"tier":"big"}`)); err == nil || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("tier without model: %v", err)
	}
	// Both tier and model.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "test", json.RawMessage(`{"tier":"small","model":"x"}`)); err == nil {
		t.Fatal("tier+model must be rejected")
	}
	// Invalid tier name.
	if _, err := p.HandleAction(context.Background(), db, 0, noteID, "test", json.RawMessage(`{"tier":"huge"}`)); err == nil {
		t.Fatal("unknown tier must be rejected")
	}
}

func TestActionTestReportsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	cfg := fmt.Sprintf(`{
		"provider": "compatible",
		"base_url": %q,
		"api_key": "sk-wrong",
		"default_tier": "tiny",
		"tiers": {"tiny": {"model": "prov/tiny"}}
	}`, srv.URL)
	saveCfg(t, db, noteID, cfg)

	p := notetype.Registry[PluginID].(notetype.ActionHandler)
	resAny, err := p.HandleAction(context.Background(), db, 0, noteID, "test", nil)
	if err != nil {
		t.Fatalf("test action must not fail hard on provider errors: %v", err)
	}
	res := resAny.(TestResult)
	if res.OK {
		t.Fatal("result must not be ok against a 401 provider")
	}
	if !strings.Contains(res.Error, "401") && !strings.Contains(res.Error, "invalid") {
		t.Errorf("error text: %q", res.Error)
	}
}

func TestActionListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-list" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"prov/big","name":"Big Model"},{"id":"prov/tiny","name":"Tiny"},{"id":""}]}`))
	}))
	defer srv.Close()

	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	cfg := fmt.Sprintf(`{"provider":"compatible","base_url":%q,"api_key":"sk-list"}`, srv.URL)
	saveCfg(t, db, noteID, cfg)

	p := notetype.Registry[PluginID].(notetype.ActionHandler)
	resAny, err := p.HandleAction(context.Background(), db, 0, noteID, "list_models", nil)
	if err != nil {
		t.Fatalf("list_models: %v", err)
	}
	m := resAny.(map[string]any)
	models := m["models"].([]ModelEntry)
	if len(models) != 2 || models[0].ID != "prov/big" || models[0].Name != "Big Model" {
		t.Fatalf("models: %+v", models)
	}
	if total, _ := m["total"].(int); total != 2 {
		t.Errorf("total: %v", m["total"])
	}
}

func TestHandleActionGuardsAndUnknown(t *testing.T) {
	p := notetype.Registry[PluginID].(notetype.ActionHandler)

	// nil db (harness contract): graceful errors, no panic.
	for _, action := range []string{"test", "list_models", "clear_api_key"} {
		if _, err := p.HandleAction(context.Background(), nil, 0, 0, action, nil); err == nil {
			t.Errorf("action %q with nil db must error", action)
		}
	}

	// Unknown action → ErrUnknownAction.
	if _, err := p.HandleAction(context.Background(), nil, 0, 0, "nope", nil); err != notetype.ErrUnknownAction {
		t.Errorf("unknown action error: %v", err)
	}
}

// --- view ---

func TestBuildView(t *testing.T) {
	db := testDB(t)
	noteID := createNote(t, db, "Inferencer", PluginID, 0)
	p := notetype.Registry[PluginID].(notetype.ViewBuilder)

	// No config yet → defaults, no error.
	viewAny, err := p.BuildView(context.Background(), db, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView without config: %v", err)
	}
	view := viewAny.(View)
	if view.Provider != ProviderOpenRouter || len(view.Tiers) != 0 || len(view.Usages) == 0 {
		t.Fatalf("default view: %+v", view)
	}
	for _, u := range view.Usages {
		if u.Binding != nil {
			t.Error("no binding expected on an unsaved note")
		}
	}

	// Saved config: resolved bindings, masked key presence.
	saveCfg(t, db, noteID, validPayload)
	viewAny, err = p.BuildView(context.Background(), db, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	view = viewAny.(View)
	if !view.APIKeySet {
		t.Error("api_key_set must be true")
	}
	if view.ProviderLabel != "OpenRouter" || view.DefaultBaseURL != "https://openrouter.ai/api" {
		t.Errorf("provider labels: %+v", view)
	}
	if len(view.Tiers) != 4 || view.Tiers[0].Tier != TierBig {
		t.Errorf("tiers: %+v", view.Tiers)
	}
	var byID = map[string]UsageView{}
	for _, u := range view.Usages {
		byID[u.ID] = u
	}
	rc := byID["rss_classify"]
	if rc.Binding == nil || rc.Binding.Tier != "small" || rc.Binding.Model != "prov/small" {
		t.Errorf("rss_classify binding in view: %+v", rc)
	}
	if !rc.Wired {
		t.Error("rss_classify must report wired in the view")
	}
	if byID["title"].Wired {
		t.Error("title must report unwired in the view")
	}

	// Incomplete binding surfaces a warning + incomplete flag.
	saveCfg(t, db, noteID, `{"provider":"openrouter","usages":{"rss_classify":{"tier":"big"}}}`)
	viewAny, err = p.BuildView(context.Background(), db, 0, noteID)
	if err != nil {
		t.Fatalf("BuildView (incomplete): %v", err)
	}
	view = viewAny.(View)
	rc = UsageView{}
	for _, u := range view.Usages {
		if u.ID == "rss_classify" {
			rc = u
		}
	}
	if !rc.Incomplete {
		t.Error("incomplete binding must be flagged")
	}
	if len(view.Warnings) == 0 {
		t.Error("incomplete binding must produce a warning")
	}

	// JSON-serializable.
	if _, err := json.Marshal(viewAny); err != nil {
		t.Errorf("view not serializable: %v", err)
	}
	raw, _ := json.Marshal(viewAny)
	if strings.Contains(string(raw), "sk-test-123") {
		t.Error("view must never contain the API key")
	}
}
