# MentisEterna — Agent Guide

Personal note-taking app with vector search. Go backend + Vue 3 frontend, SQLite + VSS extensions, Ollama embeddings.

## Architecture

```
cmd/server/      — main binary (Go HTTP server)
cmd/backfill/    — one-shot tool: generate missing embeddings for existing notes
internal/db/     — SQLite wrapper, migrations, auth, session management
internal/llm/    — Ollama embedding client (interface: Embedder)
internal/server/ — HTTP handlers, WebAuthn, SPA static serving
pkg/notetype/    — Note type plugin interface, registry, test harness, and built-in plugins
frontend/        — Vue 3 + Vite app (dev proxy → :8080)
FrontEndDist/    — Vite build output (served by Go server at runtime)
lib/             — Pre-built SQLite extensions: vector0.so, vss0.so (do NOT regenerate)
```

Notes store content history in `updates` table (body was migrated out of `notes`). VSS table `vss_notes` holds 2560-dim embeddings indexed by `rowid = notes.id`.

## Creating Custom Note Types

Note types are plugin-based. Each type is a Go package in `pkg/notetype/<name>/` that implements the `NoteType` interface defined in `pkg/notetype/notetype.go`. Plugins self-register via `init()` and are auto-discovered by the server at startup.

### Step-by-step (5 steps)

1. **Create the package** — `pkg/notetype/yourtype/yourtype.go`

2. **Implement the `notetype.NoteType` interface**:
   - `ID() string` — unique short name (e.g. `"yourtype"`)
   - `InitSchema(db *sql.DB) error` — create `ct_yourtype_*` tables with `FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE`
   - `Validate(payload json.RawMessage) error` — return nil if payload is valid
   - `ProcessSave(ctx, tx, userID, noteID, payload) error` — persist data within the SQL transaction (DELETE old rows then INSERT new ones)
   - `ProcessLoad(ctx, db, userID, noteID) (any, error)` — return custom data for the frontend
   - `UISchema() json.RawMessage` — FormKit-compatible JSON schema (or nil)
   - `CronJobs() []notetype.CronJob` — background tasks (or return nil)

   Call `notetype.Register(&YourPlugin{})` in an `init()` function.

3. **Register in main** — add a blank import to `cmd/server/main.go`:
   ```go
   _ "github.com/i5heu/MentisEterna/pkg/notetype/yourtype"
   ```

4. **Add frontend rendering** — edit `frontend/src/components/NoteTypeRenderer.vue` and add a `v-if="note.type === 'yourtype'"` block. Use the `editing` prop to toggle between editable inputs and read-only display.

5. **Register in the type selector** — add to `typeOptions` in `frontend/src/views/NotesView.vue`:
   ```js
   { value: "yourtype", label: "Your Type" },
   ```

### Critical conventions

- **Table names**: always prefix with `ct_<pluginID>_` (e.g. `ct_recipe_ingredients`).
- **Foreign keys**: always `REFERENCES notes(id) ON DELETE CASCADE`.
- **Payload shape**: `Validate`, `ProcessSave`, and `ProcessLoad` must all use the **same JSON structure**. Wrap arrays in an object: `{"items": [...]}` not `[...]`. The test harness catches shape mismatches automatically.
- **Upserts in plugin tables**: DELETE old rows, then INSERT new ones. `INSERT OR REPLACE` with foreign keys can cause issues.
- **Plugin actions (RPC)**: Call `server.RegisterPluginActionHandler("yourtype", handler)` in `init()` to expose custom `POST /notes/:id/action` endpoints. The frontend calls `pluginAction(token, noteId, "action_name", params)`.
- **❌ NEVER store plugin config or data in the note body (`updates` table)**. The note body is for user-written markdown content only. Plugin configuration and data MUST be stored in dedicated plugin tables (`ct_<pluginID>_*`). Reading from `updates.body` inside `ProcessLoad` to recover plugin state is a misuse and unacceptable. Always create proper tables via `InitSchema` and persist through `ProcessSave`.

### Reference implementations

| Plugin | ID | What it does |
|---|---|---|
| `pkg/notetype/example/` | `example` | Minimal checklist with items (label, checked) — best starting point for new plugins |
| `pkg/notetype/recipe/` | `recipe` | Ingredient table (name, amount, unit) with add/remove rows |
| `pkg/notetype/recipeoverview/` | `recipe_overview` | Dashboard aggregating all recipes + "Generate Grocery List" RPC action |

## Testing Plugins

### Automatic test harness (`pkg/notetype/plugintest/`)

Every plugin gets a free test battery. Create a single test file:

```go
// pkg/notetype/yourtype/yourtype_test.go
func TestYourPlugin(t *testing.T) {
    plugintest.Run(t, &YourPlugin{}, plugintest.TestData{
        ValidPayload:   `{"things":[{"name":"Foo"}]}`,
        InvalidPayload: `{"things":[{"name":""}]}`,
    })
}
```

This runs 14 sub-tests: ID validity, registry presence, ID uniqueness, schema idempotency, schema-after-notes-table, UI schema JSON validity, empty payload acceptance, valid payload acceptance, invalid payload rejection, **save/load round-trip with shape consistency check**, orphan cleanup, empty save, cron job validity, and action handler registration.

**The most important check**: `SaveLoad_RoundTrip` calls `ProcessSave` → `ProcessLoad` → `json.Marshal` → `Validate`. If `ProcessLoad` returns a different shape than `Validate` expects (e.g. raw array vs wrapped object), this test fails with an explicit hint.

### Helper functions

```go
// Open an in-memory DB with notes table + plugin schema, auto-cleaned up.
d := plugintest.DB(t, &YourPlugin{})

// Insert a note and get its ID.
noteID := plugintest.CreateNote(t, d, "My Note", &YourPlugin{})

// Save a payload inside a transaction.
plugintest.SavePayload(t, d, &YourPlugin{}, noteID, json.RawMessage(`...`))

// Fast mode: only validation + UI schema (3 tests, ~1ms).
plugintest.Quick(t, &YourPlugin{}, plugintest.TestData{...})
```

### Running plugin tests

```bash
go test ./pkg/notetype/...                          # all plugins
go test ./pkg/notetype/recipe/ -run TestRecipePlugin # single plugin
go test ./pkg/notetype/... -v                        # verbose: see every sub-test
```

## Commands

### Go backend
```bash
go build ./cmd/server/       # build server binary
go run ./cmd/server/         # run server (default: :8080, DB: mentis.db)
go run ./cmd/backfill/       # backfill embeddings for notes missing them
go test ./...                # run all tests
go test ./internal/db/       # test a specific package
go test ./internal/server/ -run TestNotesSearch  # run a single test
go test ./pkg/notetype/...   # run all plugin tests (harness + built-in plugins)
go test ./pkg/notetype/recipe/ -run TestRecipePlugin  # run a single plugin's tests
```

### Frontend
```bash
cd frontend
npm install
npm run dev      # dev server with proxy to :8080
npm run build    # outputs to ../FrontEndDist (what the Go server serves)
```

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `DB_PATH` | `mentis.db` | SQLite database path |
| `ADDR` | `:8080` | HTTP listen address |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama instance URL |
| `OLLAMA_EMBEDDING_MODEL` | `hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M` | Embedding model |
| `OLLAMA_CHAT_MODEL` | `llama3.2:3b` | Chat/generation model (title generation) |
| `VSS_EXT_PATH` | auto-detected | Directory containing `vector0.so` and `vss0.so` |

## Key Quirks

**VSS extension loading**: `db.Open()` gracefully falls back to standard SQLite if `.so` files aren't loadable. Tests that require VSS auto-skip with `t.Skip(...)` when VSS is unavailable — this is intentional, not a bug.

**VSS upserts**: `vss0` does NOT support `UPDATE` or `INSERT OR REPLACE`. Always `DELETE` then `INSERT`:
```sql
DELETE FROM vss_notes WHERE rowid = ?;
INSERT INTO vss_notes(rowid, body_embedding) VALUES (?, ?);
```

**WebAuthn**: RPID is hardcoded to `localhost`, origins locked to `http://localhost:8080` and `https://localhost:8080`. Changing the host/port requires updating `internal/server/server.go`.

**Embedding dimension**: 2560 (Qwen3-Embedding-4B). The mock embedder in tests also uses 2560 — keep them in sync if you change models.

**Auth**: Password-based (SHA-512, stored in `auth` table) + WebAuthn passkeys. Sessions last 24 hours. `initAdminPassword()` runs at server startup.

**Static serving**: Go server serves `FrontEndDist/` as an SPA (falls back to `index.html` for unknown paths). Must `npm run build` to reflect frontend changes in production mode.

## Testing Conventions

- DB tests use `db.Open(t.TempDir()+"/test.db")` — real SQLite on temp files
- Server tests use `db.OpenInMemory()` with a `mockEmbedder` (deterministic, no Ollama needed)
- `newTestServer(t)` — basic server with no embedder (nil)
- `newTestServerWithEmbedder(t)` — server with mock embedder + VSS (skips if VSS unavailable)
- `createTestSession(t, s)` — sets admin password "testpass" and returns a session token

## Database Schema Notes

- `notes`: id, title, parent_id, created_at — **no body/updated_at columns** (migrated out)
- `updates`: id, note_id, body, created_at — append-only history
- `vss_notes`: rowid (= notes.id), body_embedding (JSON float array, 2560-dim)
- `auth`: id, username, password_hash (SHA-512 hex)
- `sessions`: token, username, expires_at
- DB opened with `_journal_mode=WAL&_foreign_keys=on`
