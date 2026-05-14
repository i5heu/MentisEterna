# MentisEterna — Agent Guide

Personal note-taking app with vector search. Go backend + Vue 3 frontend, SQLite + VSS extensions, LocalAI embeddings.

## Architecture

```
cmd/server/      — main binary (Go HTTP server)
cmd/backfill/    — one-shot tool: generate missing embeddings for existing notes
cmd/restore/     — one-shot tool: download + decrypt a backup from S3
internal/backup/ — AES-256-GCM encrypted database backups to S3
internal/db/     — SQLite wrapper, migrations, auth, session management
internal/llm/    — LocalAI embedding client (interface: Embedder)
internal/server/ — HTTP handlers, WebAuthn, SPA static serving
pkg/notetype/    — Note type plugin interface, registry, test harness, and built-in plugins
frontend/        — Vue 3 + Vite app (dev proxy → :8080)
frontend/src/note-types/ — Vue components per note type + shared registry
FrontEndDist/    — Vite build output (served by Go server at runtime)
lib/             — Pre-built SQLite extensions: vector0.so, vss0.so (do NOT regenerate)
```

Notes store content history in `updates` table (body was migrated out of `notes`). VSS table `vss_notes` holds 2560-dim embeddings indexed by `rowid = notes.id`.

## Creating Custom Note Types

Note types are plugin-based. Each type is a Go package in `pkg/notetype/<name>/` that implements the `Plugin` interface defined in `pkg/notetype/notetype.go`. Plugins self-register via `init()` and are auto-discovered by the server at startup.

### The Plugin interface (required)

Every note type must implement the `Plugin` base interface:

- `ID() string` — unique short name (e.g. `"yourtype"`)
- `InitSchema(db *sql.DB) error` — create `ct_yourtype_*` tables with `FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE`
- `Manifest() Manifest` — static type metadata (label, icon, category, editor/viewer modes, actions, capabilities)
- `CronJobs() []CronJob` — background tasks (or return nil)

### Optional capability interfaces

Plugins may also implement these optional interfaces as needed:

- `ConfigValidator` — `ValidateConfig(json.RawMessage) error` — validate the persisted config payload
- `ConfigSaver` — `SaveConfig(ctx, tx, userID, noteID, config) error` — persist config in a transaction
- `ConfigLoader` — `LoadConfig(ctx, db, userID, noteID) (json.RawMessage, error)` — load persisted config
- `ViewBuilder` — `BuildView(ctx, db, userID, noteID) (any, error)` — build computed/derived view data
- `ActionHandler` — `HandleAction(ctx, db, userID, noteID, actionID, params) (any, error)` — execute actions

**Capability declaration is mandatory**: if a plugin's `Manifest` declares `HasConfig=true`, the plugin MUST implement `ConfigValidator`, `ConfigSaver`, and `ConfigLoader`. Similarly for `HasView` (→ `ViewBuilder`) and `HasActions` (→ `ActionHandler`). The server validates this at startup and will `log.Fatal` on mismatch.

The server populates `plugin.config` and `plugin.view` on note detail responses from `ConfigLoader` and `ViewBuilder`. Actions are declared in the manifest and dispatched through `ActionHandler`.

### API routes

- `GET /note-types` — catalog of all available note types (includes synthetic `standard` type)
- `GET /notes/:id` — note detail with `plugin.config` and `plugin.view`
- `POST /notes/:id/actions/:actionID` — execute a plugin action
- `POST /notes/:id/action` — legacy action route (delegates to same dispatcher)

### Step-by-step (5 steps)

1. **Create the package** — `pkg/notetype/yourtype/yourtype.go`

2. **Implement the `notetype.Plugin` interface**:
   - `ID() string` — unique short name (e.g. `"yourtype"`)
   - `InitSchema(db *sql.DB) error` — create `ct_yourtype_*` tables with `FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE`
   - `Manifest() notetype.Manifest` — static metadata (label, icon, category, sort_order, editor/viewer, actions, capability flags)
   - `CronJobs() []notetype.CronJob` — background tasks (or return nil)

   **Also implement capability interfaces as needed:**
   - `ConfigValidator` / `ConfigSaver` / `ConfigLoader` — if the type has persistent config (set `HasConfig: true` in manifest)
   - `ViewBuilder` — if the type generates computed view data (set `HasView: true` in manifest)
   - `ActionHandler` — if the type supports RPC actions (declare them in manifest and set `HasActions: true`)

   Call `notetype.Register(&YourPlugin{})` in an `init()` function.

3. **Register in the builtins package** — add a blank import to `pkg/notetype/builtins/builtins.go`:
   ```go
   _ "github.com/i5heu/MentisEterna/pkg/notetype/yourtype"
   ```
   The `cmd/server/main.go` already imports the `builtins` package, so no changes needed there.

4. **Add frontend rendering** — create a Vue component at `frontend/src/note-types/yourtype/YourTypeNoteType.vue` that accepts the standard props contract (`note`, `token`, `editing`, `customData`, `uiSchema`) and emits `update:customData` when the user edits data. Add a barrel file `index.js` that re-exports the component.

5. **Register in the note-type registry** — add an entry to `frontend/src/note-types/registry.js`:
   ```js
   {
       id: "yourtype",
       label: "Your Type",
       component: defineAsyncComponent(() => import("./yourtype/YourTypeNoteType.vue")),
       emptyCustomData: () => ({ /* your default shape */ }),
       normalizeCustomData(raw, _note) { /* normalize server payload */ },
       supportsSchemaFallback: false,
   },
   ```
   No changes to `NoteTypeRenderer.vue` or `NotesView.vue` are needed — the registry powers both the renderer lookup and the type picker automatically.

### Critical conventions

- **Table names**: always prefix with `ct_<pluginID>_` (e.g. `ct_recipe_ingredients`).
- **Foreign keys**: always `REFERENCES notes(id) ON DELETE CASCADE`.
- **Config shape consistency**: `ValidateConfig`, `SaveConfig`, and `LoadConfig` must all use the **same JSON structure**. Wrap arrays in an object: `{"items": [...]}` not `[...]`. The test harness catches shape mismatches automatically.
- **Upserts in plugin tables**: DELETE old rows, then INSERT new ones. `INSERT OR REPLACE` with foreign keys can cause issues.
- **Plugin actions (RPC)**: Declare actions in the `Manifest` (with `HasActions: true`) and implement `ActionHandler` on your plugin struct. The server automatically exposes `POST /notes/:id/actions/:actionID` and the legacy `POST /notes/:id/action`.
- **❌ NEVER store plugin config or data in the note body (`updates` table)**. The note body is for user-written markdown content only. Plugin configuration and data MUST be stored in dedicated plugin tables (`ct_<pluginID>_*`). Always create proper tables via `InitSchema` and persist through `SaveConfig`.

### Reference implementations

| Plugin | ID | What it does | Capabilities |
|---|---|---|---|
| `pkg/notetype/example/` | `example` | Minimal checklist with items (label, checked) — best starting point for new plugins | ConfigValidator, ConfigSaver, ConfigLoader |
| `pkg/notetype/recipe/` | `recipe` | Ingredient table (name, amount, unit) + metadata fields | ConfigValidator, ConfigSaver, ConfigLoader |
| `pkg/notetype/recipeoverview/` | `recipe_overview` | Dashboard aggregating all recipes + "Generate Grocery List" RPC action | ViewBuilder, ActionHandler |
| `pkg/notetype/index/` | `index` | Tag-based note index (global or local scope) | ConfigValidator, ConfigSaver, ConfigLoader, ViewBuilder |

## Frontend Note-Type Architecture

Note-type rendering is powered by a **registry** in `frontend/src/note-types/registry.js`. This is the single source of truth — it drives both the type picker in `NotesView.vue` and the renderer lookup in `NoteTypeRenderer.vue`.

### File structure

```
frontend/src/note-types/
├── registry.js                          # Single source of truth
├── shared/
│   ├── SchemaNoteType.vue               # Schema-driven rendering fallback
│   ├── UnsupportedNoteType.vue          # Unknown-type fallback
│   ├── useNoteTypeDraft.js              # Shared composable for draft sync
│   └── usePluginAction.js               # Shared composable for RPC calls
├── recipe/
│   ├── RecipeNoteType.vue               # Ingredient table + detail fields
│   └── index.js
├── recipe_overview/
│   ├── RecipeOverviewNoteType.vue       # Grocery list dashboard
│   └── index.js
├── example/
│   ├── ChecklistNoteType.vue            # Checklist editor
│   └── index.js
└── index/
    ├── IndexNoteType.vue                # Tag index viewer
    └── index.js
```

### Component contract

Every note-type component receives the same props:

| Prop | Type | Purpose |
|---|---|---|
| `note` | Object | The full note object (for id, title, metadata) |
| `token` | String | Auth token for API calls |
| `editing` | Boolean | Whether the user is in edit mode |
| `customData` | Object | The note-type-specific payload (source of truth for rendering) |
| `uiSchema` | Object | Optional UI schema for schema-driven fallback |

Emits:

| Event | Payload | Purpose |
|---|---|---|
| `update:customData` | Object | Emitted when the user edits data — parent saves it |
| `selectNote` | noteId | Navigate to a linked note |

### Registry entry shape

```js
{
    id: "yourtype",                          // matches backend plugin ID
    label: "Your Type",                       // picker label
    component: defineAsyncComponent(...),      // lazy-loaded Vue component (or null)
    emptyCustomData: () => ({ ... }),          // default payload for new notes
    normalizeCustomData(raw, note) { ... },    // normalize server payload
    supportsSchemaFallback: false,             // use SchemaNoteType if no component
}
```

### How `NoteTypeRenderer.vue` resolves rendering

1. If the type has a `component` in the registry → render it with the standard props.
2. Else if the type has `supportsSchemaFallback` and a `uiSchema` → render `SchemaNoteType.vue`.
3. Else if the type is unknown (not in the registry) → render `UnsupportedNoteType.vue`.
4. Else (known type with no custom component, e.g. `standard`) → render nothing (the body textarea handles everything).

### Guard pattern for echo-back loops

Components that hydrate local state from the `customData` prop AND emit `update:customData` on local edits must use a `hydrating` guard flag to break the feedback loop:

```
local edit → emit → parent sets customData → prop change → hydrate → deep watcher → emit → ...
```

The fix: hydrate on `props.note.id` change (note identity), not on every `customData` prop update. See `RecipeNoteType.vue` for the reference implementation.

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

This runs multiple sub-tests: ID validity, registry presence, ID uniqueness, schema idempotency, schema-after-notes-table, cron job validity, **manifest validation**, **config round-trip** (for plugins with config), **view builder** (for plugins with view), and **action handler** (for plugins with actions).

**The most important check**: `Config_RoundTrip` calls `SaveConfig` → `LoadConfig` → `ValidateConfig`. If the loaded config fails validation, the test fails.

### Helper functions

```go
// Open an in-memory DB with notes table + plugin schema, auto-cleaned up.
d := plugintest.DB(t, &YourPlugin{})

// Insert a note and get its ID.
noteID := plugintest.CreateNote(t, d, "My Note", &YourPlugin{})
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
go run ./cmd/restore/ backups/mentis-2026-05-12T03-00-00.db.enc mentis_restored.db  # restore a backup
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
| `LOCALAI_BASE_URL` | `http://localhost:8080` | LocalAI instance URL |
| `LOCALAI_EMBEDDING_MODEL` | `text-embedding-ada-002` | Embedding model |
| `LOCALAI_EMBEDDING_MAX_CHARS` | `16384` | Max runes per embedding request (avoids context overflow) |
| `LOCALAI_CHAT_MODEL` | `gpt-3.5-turbo` | Chat/generation model (title generation) |
| `LOCALAI_OCR_MODEL` | `gpt-4o-mini` | Multimodal vision model for OCR |
| `LOCALAI_STT_MODEL` | `nemo-parakeet-tdt-0.6b` | Whisper-compatible model for speech-to-text transcription |
| `VSS_EXT_PATH` | auto-detected | Directory containing `vector0.so` and `vss0.so` |
| `BACKUP_ENCRYPTION_KEY` | none (backups disabled) | hex-encoded 64-char AES-256 key for encrypted backups |
| `MEDIA_CACHE_DIR` | required for media | Directory for local file cache (also required for backups) |
| `MEDIA_S3_ENDPOINTS` | required for media | JSON array of S3 endpoint configs (also used for backups) |

## Key Quirks

**VSS extension loading**: `db.Open()` gracefully falls back to standard SQLite if `.so` files aren't loadable. Tests that require VSS auto-skip with `t.Skip(...)` when VSS is unavailable — this is intentional, not a bug.

**VSS upserts**: `vss0` does NOT support `UPDATE` or `INSERT OR REPLACE`. Always `DELETE` then `INSERT`:
```sql
DELETE FROM vss_notes WHERE rowid = ?;
INSERT INTO vss_notes(rowid, body_embedding) VALUES (?, ?);
```

**WebAuthn**: RPID is hardcoded to `localhost`, origins locked to `http://localhost:8080` and `https://localhost:8080`. Changing the host/port requires updating `internal/server/server.go`.

**Embedding dimension**: 2560. The mock embedder in tests uses 2560 — keep them in sync with whichever embedding model you use.

**Auth**: Password-based (SHA-512, stored in `auth` table) + WebAuthn passkeys. Sessions last 24 hours. `initAdminPassword()` runs at server startup.

**Static serving**: Go server serves `FrontEndDist/` as an SPA (falls back to `index.html` for unknown paths). Must `npm run build` to reflect frontend changes in production mode.

## Encrypted Backups

AES-256-GCM encrypted database backups to S3, with automated retention (max 3/day for 7d, 1/week for 3m, 1/month for 5y). Full documentation: [`docs/Backups.md`](docs/Backups.md).

Key files:

```
internal/backup/crypto.go     — AES-256-GCM Encrypt/Decrypt, KeyFromHex, GenerateKey
internal/backup/backup.go     — Service orchestrator (snapshot + encrypt + upload, retention purge)
internal/backup/retention.go  — Retention policy classification and purge logic
cmd/restore/main.go           — CLI tool (download + decrypt → output file)
```

## Testing Conventions

- DB tests use `db.Open(t.TempDir()+"/test.db")` — real SQLite on temp files
- Server tests use `db.OpenInMemory()` with a `mockEmbedder` (deterministic, no LocalAI needed)
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
