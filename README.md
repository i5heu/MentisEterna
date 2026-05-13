# MentisEterna

> ☠️🚨⚠️⚠️⚠️ Do not use this. ⚠️⚠️⚠️🚨☠️   
> This is a terrible generated mess of doom i created for prototyping.  
> I only use it in a private VPN.   
> It is absolutly not secure or safe to use.   


As I am building for a long time now on [OuroborosDB](https://github.com/i5heu/ouroboros-db/) for personal knowledge management systems (PKM), I recognized that my inital ideas how a UI for it would look like where not overtaken by a desire for a radical diffrent way how i wanted to interact with my PKM. This is why i created this explorative project to test out a UI paradigm that i always have thought as the ideal but never had the chance to see it in action since no one bothered to build it a pkm in that way.

<p align="center" style="margin: 2em;">
    <img width="280" height="280" style="border-radius: 3%; max-width: 100%" alt="Logo of OuroborosDB" src=".media/MentisEterna_logo.svg">
</p>


## Desgin Paradigm
- **Minimum Decisions**: Any decision that doesn't have to be made is a good decision. The System should make as many decisions as possible for the user to reduce friction and cognitive load.
- **Chat-like UI**: The interface mimics a conversation, each reply is a note. Nested Notes from threads.
- **Note Types**: Everything is a Note except Files. Note types have different logic, schemas, renderers and jobs for lists, indexes, recipes, tasks, journals, etc.
- **Every Thought a Note**: The UI encourages breaking down information into atomic, linked notes.
- **Hotkey-Driven**: Every action has a hotkey. The UI is fully navigable and usable without a mouse.
- **Knowledge Retrieval Maxxing**:
  - Search must be meaningful and fast.
    - Semantic search
    - Search by title, content, tags, and note type.
    - OCR - search of text in images and pdfs.
    - Speech to text - search of spoken content in audio notes.
  - UI must encourage linking and backlinking.
  - Optional automatic title generation and tagging based on content.
  - Editor must recomend related concepts from notes.
  - Editor must recomend web links relatefd to the content.
- **Automation as much as possible**: The system should automate everything it can to lower the friction of creating, maintaining and retrieving knowledge.
- **Audio Notes**: Audio recording notes are first class citizens.


## Usage
Environment variables:
```bash
export LOCALAI_BASE_URL="http://localhost:8080"  # LocalAI server URL
export LOCALAI_EMBEDDING_MODEL="Qwen3-Embedding-4B-GGUF"  # Model for embeddings
export LOCALAI_CHAT_MODEL="gemma-3-4b-it"  # Model for title generation
export LOCALAI_OCR_MODEL="glm-ocr"  # Multimodal model for OCR
export LOCALAI_STT_MODEL="voxtral-mini-4b-realtime"  # Whisper-compatible model for speech-to-text
export MEDIA_CACHE_DIR="/var/mentis/cache"  # Local directory for encrypted file cache
export MEDIA_S3_ENDPOINTS='[
  {
    "id": "primary",
    "bucket": "mentis-files",
    "region": "nbg1",
    "endpoint": "https://nbg1.your-objectstorage.com",
    "access_key_id": "XXX",
    "secret_access_key": "XXX",
    "force_path_style": false
  }
]'  # JSON array of S3 endpoint configurations
export BACKUP_ENCRYPTION_KEY="your-encryption-key"  # Encryption key for backups
```

Create `BACKUP_ENCRYPTION_KEY` with `openssl rand -hex 32`.

**Models**:  
`hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M`  
`hf.co/ggml-org/GLM-OCR-GGUF:Q8_0`


## TODO MVP
- [x] Pin notes 
- [x] Chat like UI
- [x] Note Types
- [x] Pseudo-Plugins
  - [x] Test harness
- [x] Job system with persistent queue, retry, and frontend panel
  - [x] Job Queue Indicator in sidebar
  - [x] Semantic indexing via job queue
- [x] S3 Media Storage (Encrypted)
- [x] Note linking and backlinking
- [x] tags
  - [x] index note type
- [x] Auto Title Generator
  - [x] LocalAI URL configurable via env var
- [x] Security Review and Auth hardening (single user focus and auth focus, evrything else not a priority)
- [x] Encrypted Backup (AES-256-GCM, automated retention — see [docs/Backups.md](docs/Backups.md))

## TODO Future
- [x] OCR for images and pdfs
- [x] speech to text notes
- [ ] Refactor code
  - [x] Split NoteTypeRenderer.vue into multiple components for better maintainability.
  - [ ] especially note types to see if we can abstract some common logic. 
- [ ] Options Page for job list, create backup, logout, register passkey and other settings
  - [ ] Re Index, OCR and STT failed once, button in UI with counter.
- [ ] better search (by title, path and tags)
- [ ] mobile-app - sync?
- [ ] UX Improvements
  - [ ] Drag and Drop for notes
  - [ ] Resizable panes
  - [ ] Better Keyboard Shortcuts
  - [ ] Autocomplete and predictive text
  - [ ] Brainstorm and Research mode
  - [ ] Fast note creation with AI parent selection
  - [ ] Fast Handnote import
- [ ] support AsciiDoc and Markdown (with live preview)
- [ ] SQLite AES-256 in OFB mode
- [ ] Search for Note Types and Path should be shown instead of just title when selecting a note type.

## TODO Note Types
- [x] Recipe (with ingredient table)
  - [x] Recipe Overview (dashboard listing all recipe notes, grocery list generation via RPC action)
  - [x] Recipe Note Type must include following fields: Servings, Attention Time, Total Time, grams per serving, kcal per serving, Freezable (boolean)
  - [ ] If a recipe has can be pre-cocked, there should be a checkbox in the Recipe Overview to pre-cook the recipe. If true the people ammount is ignored and the pre-cook serving size is used for this recipe when generating the grocery list. 
  - [ ] Recipe ingredients must use metric units or pieces. Decimal punctuation must be a dot. 
  - [ ] Grocery List must recalculate the amounts to best metric unit (e.g. 1500g -> 1.5kg, 0.5l -> 500ml, etc.)
  - [ ] Grocery List must be alphabetically sorted and grouped by category (e.g. vegetables, meat, dairy, etc.). using embedding an cosine similarity to these categories.: ["vegetables", "fruit", "meat", "dairy", "fish", "chilled & deli", "frozen", "spices", "beverages", "household", "other"]
- [ ] Task Sytem
  - [ ] Task note type - title, status, dificulty (from 0 to 10), Fun (from -5 to 5), priority (from 0 to 10), description, due date, time estimation, time used, recurring options
  - [ ] Task overview dashboard - list all tasks, filter by status, due date, etc. 
  - [ ] Daily task list - give 3 random tasks per day
- [ ] Home Note Type - Shows latest notes, stats, has "Mind Dump" section for quick note creation.
- [ ] Jornal Note Type - daily journal with mood tracking, done todo items.
- [ ] Skill Note Type - for tracking skills, subskills, resources, progress, etc.
  - [ ] Skill Overview Dashboard Note Type - shows all skills, progress, etc.
- [ ] Car refuling log note type
  - [ ] Car refuleling log overview dashboard
- [ ] Gridfinity note type
  - [ ] Gridfinity overview dashboard with search and filter options
- [ ] Wants and wishes note type - has button to add "Today i wanted this", has fields: Acquisition Cost, Operating Expenses per month, Disposal Costs, potential profit per month, Durability in years, Space Requirements in m³
  - [ ] wants and wishes overview dashboard, with Total Cost of Ownership, Total Potential Profit and space requirements.
- [ ] Web Fetcher note type - fetches a webpage and extracts the main content (like mercury parser) and saves it as a note

## Prerequisites

- LocalAI: [Installation Guide](https://localai.io/basics/getting_started/)
- An embedding model (e.g. `text-embedding-ada-002`) and a chat model (e.g. `gpt-3.5-turbo`) loaded in LocalAI

## S3 Media Storage

MentisEterna supports encrypted file attachments stored on any S3-compatible object storage (AWS S3, MinIO, Backblaze B2, etc.). Files are **AES-256-GCM encrypted** before leaving the server — each file gets its own random 256-bit key. Ciphertext is cached locally and replicated to all configured S3 endpoints.

### Environment Variables

| Variable | Required | Purpose |
|---|---|---|
| `MEDIA_CACHE_DIR` | Yes | Writable directory for local encrypted file cache (e.g. `/var/mentis/cache`) |
| `MEDIA_S3_ENDPOINTS` | Yes | JSON array of endpoint configurations (see format below) |

If these variables are not set, the media subsystem is disabled and a warning is logged at startup. The server still runs — file upload/download endpoints simply return `503 Service Unavailable`.

### `MEDIA_S3_ENDPOINTS` Format

A JSON array of objects, each describing one S3-compatible endpoint:

```json
[
  {
    "id":                "primary",
    "bucket":            "my-bucket",
    "region":            "us-east-1",
    "endpoint":          "https://s3.amazonaws.com",
    "access_key_id":     "AKIAIOSFODNN7EXAMPLE",
    "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "force_path_style":  false
  }
]
```

| Field | Required | Notes |
|---|---|---|
| `id` | Yes | Unique name for this endpoint (e.g. `"primary"`, `"backblaze"`). Must be unique across all endpoints. |
| `bucket` | Yes | S3 bucket name. |
| `region` | No | AWS region. Defaults to `us-east-1` if omitted. |
| `endpoint` | Yes | Base URL of the S3-compatible service. For AWS: `https://s3.amazonaws.com`. For MinIO: `http://localhost:9000`. |
| `access_key_id` | Yes | Access key / username. |
| `secret_access_key` | Yes | Secret key / password. |
| `force_path_style` | No | Use path-style bucket addressing (`/bucket/key`) instead of virtual-hosted style (`bucket.endpoint/key`). Required for MinIO and many self-hosted S3 services. Defaults to `false`. |

### Multiple Endpoints (Replication)

You can configure multiple endpoints for redundancy. Files are uploaded to **all** endpoints simultaneously, and the `repair_replicas` background job (`@every 1m`) heals any replicas that failed during upload:

```json
[
  {
    "id":                "aws-primary",
    "bucket":            "mentis-files",
    "region":            "us-east-1",
    "endpoint":          "https://s3.amazonaws.com",
    "access_key_id":     "AKIA...",
    "secret_access_key": "...",
    "force_path_style":  false
  },
  {
    "id":                "backblaze-backup",
    "bucket":            "mentis-backup",
    "region":            "us-west-004",
    "endpoint":          "https://s3.us-west-004.backblazeb2.com",
    "access_key_id":     "...",
    "secret_access_key": "...",
    "force_path_style":  true
  }
]
```

### Encryption Details

- **Algorithm**: AES-256-GCM (chunked mode, 1 MiB chunks)
- **Key**: Random 256-bit key generated per file (via `crypto/rand`)
- **Nonce**: Random 12-byte base nonce per file, with chunk index XOR'd for each chunk
- **File format**: `MEF1` magic bytes → version byte → chunk size → [plain_len | ciphertext]...
- Keys and nonces are stored in the `files` table alongside the ciphertext SHA-256 hash

### Media Background Jobs

The media subsystem registers these cron jobs:

| Job | Schedule | Purpose |
|---|---|---|
| `repair_replicas` | `@every 1m` | Sweeps for files with failed replicas and retries upload |
| `cleanup_pending_inline` | `@every 1h` | Finalizes inline files whose parent note was confirmed |

### Example: MinIO for Local Development

```bash
export MEDIA_CACHE_DIR="/tmp/mentis-media-cache"
export MEDIA_S3_ENDPOINTS='[
  {
    "id": "minio",
    "bucket": "mentis",
    "endpoint": "http://localhost:9000",
    "access_key_id": "minioadmin",
    "secret_access_key": "minioadmin",
    "force_path_style": true
  }
]'
```

## Database Backups

The SQLite database is backed up to S3 every 12 hours using **AES-256-GCM** encryption. Snapshots use the SQLite Online Backup API, so they're safe and consistent even while the database is being actively written to.

**Automated retention** cleans up old backups every 24 hours:

| Window | Rule |
|---|---|
| Last 7 days | Max 3 per day (newest) |
| 7 days – 3 months | 1 per week (newest) |
| 3 months – 5 years | 1 per month (newest) |
| Older than 5 years | Deleted |

On-demand backup and purge can be triggered via `POST /backup/trigger` and `POST /backup/purge`. Restore with:

```bash
go run ./cmd/restore/ backups/mentis-2026-07-22T03-00-05.db.enc mentis_restored.db
```

Full documentation: [`docs/Backups.md`](docs/Backups.md).

## Creating Custom Note Types

Note types are plugin-based. Each type lives in `pkg/notetype/<name>/` and implements the `NoteType` interface plus one or more **capability interfaces** that tell the server what the plugin can do. The server discovers and initializes all plugins automatically at startup.

### The capability model

Instead of one monolithic interface, plugins declare capabilities by implementing additive interfaces:

| Interface | Purpose | When called |
|---|---|---|
| `NoteType` (legacy base) | ID, schema, validation, save, load, cron | Registration + request lifecycle |
| `ManifestProvider` | Static metadata: label, editor/viewer modes, actions, capabilities | Server startup + `GET /note-types` |
| `ConfigValidator` | Validate persisted config before save | Before note create/update |
| `ConfigSaver` | Persist config within a transaction | Inside the note save transaction |
| `ConfigLoader` | Load persisted config as raw JSON | Note detail responses |
| `ViewBuilder` | Build computed/derived view data | Note detail responses |
| `ActionHandler` | Execute RPC actions | `POST /notes/:id/actions/:actionID` |

The server inspects which interfaces a plugin implements and populates `plugin.config` (from `ConfigLoader`) and `plugin.view` (from `ViewBuilder`) on note detail responses. Actions declared in the `Manifest` are automatically exposed.

### Quick Start

1. **Create your package** at `pkg/notetype/yourtype/yourtype.go`.

2. **Implement the legacy `NoteType` interface** (minimum required):

```go
package yourtype

import (
    "context"
    "database/sql"
    "encoding/json"

    "github.com/i5heu/MentisEterna/pkg/notetype"
)

func init() { notetype.Register(&YourPlugin{}) }

type YourPlugin struct{}

func (p *YourPlugin) ID() string { return "yourtype" }

func (p *YourPlugin) InitSchema(db *sql.DB) error {
    _, err := db.Exec(`
        CREATE TABLE IF NOT EXISTS ct_yourtype_items (
            id      INTEGER PRIMARY KEY AUTOINCREMENT,
            note_id INTEGER NOT NULL,
            label   TEXT    NOT NULL DEFAULT '',
            FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
        )
    `)
    return err
}

func (p *YourPlugin) Validate(raw json.RawMessage) error {
    return nil
}

func (p *YourPlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
    // DELETE old rows, then INSERT new ones (SQLite FK-safe pattern).
    return nil
}

func (p *YourPlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
    return nil, nil
}

func (p *YourPlugin) UISchema() json.RawMessage {
    return nil
}

func (p *YourPlugin) CronJobs() []notetype.CronJob {
    return nil
}
```

3. **Add the new capability interfaces** (recommended):

```go
// ManifestProvider — required for the type to appear in GET /note-types.
func (p *YourPlugin) Manifest() notetype.Manifest {
    return notetype.Manifest{
        ID:            "yourtype",
        Label:         "Your Type",
        Description:   "A custom note type for ...",
        Category:      "General",
        SortOrder:     500,
        DefaultConfig: json.RawMessage(`{"items":[]}`),
        Editor:        notetype.EditorMeta{Mode: "custom", Schema: p.UISchema()},
        Viewer:        notetype.ViewerMeta{Mode: "custom"},
        HasConfig:     true,
        HasView:       false,
        HasActions:    false,
    }
}

// ConfigValidator — validates config before save (replaces Validate for config).
func (p *YourPlugin) ValidateConfig(raw json.RawMessage) error {
    return p.Validate(raw)
}

// ConfigSaver — persists config inside the note transaction.
func (p *YourPlugin) SaveConfig(ctx context.Context, tx *sql.Tx, userID int, noteID int64, config json.RawMessage) error {
    return p.ProcessSave(ctx, tx, userID, noteID, config)
}

// ConfigLoader — loads config for note detail responses.
func (p *YourPlugin) LoadConfig(ctx context.Context, db *sql.DB, userID int, noteID int64) (json.RawMessage, error) {
    // Load rows from ct_yourtype_*, marshal to JSON.
    return json.RawMessage(`{"items":[]}`), nil
}

// ViewBuilder — optional: build computed view data (dashboards, aggregations, etc.).
// func (p *YourPlugin) BuildView(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) { ... }

// ActionHandler — optional: handle RPC actions declared in the Manifest.
// func (p *YourPlugin) HandleAction(ctx context.Context, db *sql.DB, userID int, noteID int64, actionID string, params json.RawMessage) (any, error) { ... }
```

The new `ConfigValidator`/`ConfigSaver`/`ConfigLoader` interfaces can simply delegate to the legacy methods during migration, as shown above. This lets you adopt the new model incrementally.

4. **Register in the builtins package** — add a blank import to `pkg/notetype/builtins/builtins.go`:

```go
import (
    _ "github.com/i5heu/MentisEterna/pkg/notetype/yourtype"
)
```

No changes to `cmd/server/main.go` are needed — it already imports the `builtins` package.

5. **Add frontend rendering** — create a Vue component at `frontend/src/note-types/yourtype/YourTypeNoteType.vue` that accepts the standard props contract (`note`, `token`, `editing`, `customData`, `uiSchema`) and emits `update:customData` when the user edits data. Add a barrel file `frontend/src/note-types/yourtype/index.js` that re-exports the component. See existing components in `frontend/src/note-types/recipe/` and `frontend/src/note-types/example/` for reference implementations.

6. **Register in the note-type registry** — add an entry to `frontend/src/note-types/registry.js`:

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

### Testing Your Plugin

Every plugin gets a **free test battery** via `pkg/notetype/plugintest/`. Create a single test file:

```go
// pkg/notetype/yourtype/yourtype_test.go
package yourtype

import (
    "testing"
    "github.com/i5heu/MentisEterna/pkg/notetype/plugintest"
)

func TestYourPlugin(t *testing.T) {
    plugintest.Run(t, &YourPlugin{}, plugintest.TestData{
        ValidPayload:   `{"things":[{"name":"Foo"}]}`,
        InvalidPayload: `{"things":[{"name":""}]}`,
    })
}
```

This runs **19 sub-tests** automatically:

| Sub-test | What it verifies |
|---|---|
| `ID_NotEmpty` | Plugin ID is non-empty |
| `Registry` | Plugin is findable in the global registry |
| `ID_Uniqueness` | No two plugins share the same ID |
| `InitSchema_Idempotent` | Calling `InitSchema` twice does not error |
| `InitSchema_AfterNotesTable` | Schema works when the `notes` table already exists |
| `UISchema_ValidJSON` | UI schema is parseable JSON |
| `Validate_EmptyPayload` | Empty and null payloads pass validation |
| `Validate_AcceptsValid` | Your valid payload passes validation |
| `Validate_RejectsInvalid` | Your invalid payload is rejected |
| `SaveLoad_RoundTrip` | Save → Load → re-validate catches **shape mismatches** |
| `SaveLoad_OrphanCleanup` | Deleting a note cascades to plugin tables |
| `SaveLoad_EmptySave` | Saving null/empty payload does not crash |
| `CronJobs_NoPanic` | All cron jobs have non-empty schedules and non-nil tasks |
| `Actions_Handler` | (placeholder) Legacy action handler check |
| `Manifest_Provider` | Manifest ID matches plugin ID, all fields valid |
| `Config_RoundTrip` | ValidateConfig → SaveConfig → LoadConfig → ValidateConfig |
| `View_Builder` | BuildView returns JSON-serializable data |
| `Action_Handler` | Each declared action dispatches without panicking |

New sub-tests gracefully skip if the plugin doesn't implement the corresponding capability interface.

**Key check — payload shape consistency**: The `SaveLoad_RoundTrip` test calls `ProcessSave` → `ProcessLoad` → `json.Marshal` → `Validate`. If your `ProcessLoad` returns a different JSON shape than `Validate` expects (e.g. a raw array `[...]` instead of `{"items": [...]}`), this test fails with an explicit hint. The `Config_RoundTrip` test does the same for the new capability interfaces.

**Helper functions** for writing additional custom tests:

```go
func TestMyCustomBehavior(t *testing.T) {
    d := plugintest.DB(t, &YourPlugin{})       // in-memory DB with notes + plugin schema
    noteID := plugintest.CreateNote(t, d, "My Note", &YourPlugin{})
    plugintest.SavePayload(t, d, &YourPlugin{}, noteID, json.RawMessage(`...`))
    // ... your assertions here ...
}
```

**Fast iteration mode**: Use `plugintest.Quick()` during development — runs only 3 tests (validation + UI schema) instead of 19.

### Interface Reference

#### Legacy `NoteType` (base — always required)

| Method | When Called | Purpose |
|---|---|---|
| `ID()` | Registration | Unique short name (e.g. `"recipe"`) |
| `InitSchema(db)` | Server startup | Create `ct_<id>_*` tables |
| `Validate(payload)` | Before save (fallback) | Return `nil` if the custom_data JSON is valid |
| `ProcessSave(ctx, tx, userID, noteID, payload)` | Inside SQL transaction (fallback) | Persist plugin data (DELETE old, INSERT new) |
| `ProcessLoad(ctx, db, userID, noteID)` | Note fetch (fallback) | Return custom data for the frontend |
| `UISchema()` | Note fetch (fallback) | FormKit-compatible JSON schema |
| `CronJobs()` | Server startup | Background tasks with cron schedules |

#### New capability interfaces (additive — recommended)

| Interface | Method | Purpose |
|---|---|---|
| `ManifestProvider` | `Manifest()` | Static type metadata for `GET /note-types` catalog |
| `ConfigValidator` | `ValidateConfig(payload)` | Validate config before save (preferred over `Validate`) |
| `ConfigSaver` | `SaveConfig(ctx, tx, userID, noteID, config)` | Persist config in transaction (preferred over `ProcessSave`) |
| `ConfigLoader` | `LoadConfig(ctx, db, userID, noteID)` | Load config for `plugin.config` on note detail |
| `ViewBuilder` | `BuildView(ctx, db, userID, noteID)` | Build computed view for `plugin.view` on note detail |
| `ActionHandler` | `HandleAction(ctx, db, userID, noteID, actionID, params)` | Execute RPC actions declared in Manifest |

### API Routes for Note Types

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/note-types` | Catalog of all note types (includes `standard`) |
| `GET` | `/notes/:id` | Note detail with `plugin.config` and `plugin.view` |
| `POST` | `/notes/:id/actions/:actionID` | Execute a plugin action (new preferred route) |
| `POST` | `/notes/:id/action` | Legacy action route (deprecated, delegates to same dispatcher) |

### Conventions

- **Table names**: Always prefix with `ct_<pluginID>_` (e.g. `ct_recipe_ingredients`).
- **Foreign keys**: Always reference `notes(id) ON DELETE CASCADE` so cleanup is automatic.
- **Upserts**: SQLite doesn't support `INSERT OR REPLACE` cleanly with foreign keys. Delete first, then insert.
- **Payload shape**: `Validate`, `ProcessSave`, and `ProcessLoad` should all use the same JSON structure (wrap arrays in an object — e.g. `{"ingredients": [...]}` not `[...]`). Same goes for `ValidateConfig` / `SaveConfig` / `LoadConfig`.
- **Config vs View**: Config is what the user edits and you persist. View is derived/computed data (dashboards, aggregations). Keep them separate — `ConfigLoader` returns config, `ViewBuilder` returns view.
- **Cron schedules**: Supports `@every 1h`, `@daily`, `@hourly`. The scheduler is lightweight — for full cron expressions, swap in `robfig/cron/v3`.
- **❌ NEVER store plugin config or data in the note body (`updates` table)**. The note body is for user-written markdown content only. Plugin configuration and data MUST live in dedicated plugin tables (`ct_<pluginID>_*`). Always create proper tables via `InitSchema` and persist through `SaveConfig` or `ProcessSave`.

### Using the Job System

Background work — cron tasks, one-shot operations, anything that should survive restarts — runs through the **job system** (`internal/jobs/`). Every job is persisted in SQLite, retried on failure, and visible in the frontend's job queue panel.

#### Cron Jobs (Scheduled)

Return them from `CronJobs()`. The server picks them up automatically:

```go
func (p *YourPlugin) CronJobs() []notetype.CronJob {
    return []notetype.CronJob{{
        Name:     "weekly_cleanup",
        Schedule: "@daily",   // @every 1h, @daily, @hourly
        Task: func(db *sql.DB, payload []byte) (string, error) {
            // payload is always nil for cron-triggered runs.
            n, err := db.Exec(`DELETE FROM ct_yourtype_stale WHERE ...`)
            if err != nil {
                return "", err
            }
            rows, _ := n.RowsAffected()
            return fmt.Sprintf("Cleaned up %d stale rows", rows), nil
        },
    }}
}
```

**Task signature**: `func(db *sql.DB, payload []byte) (string, error)`
- `db` — the database handle (use this, not a captured closure variable).
- `payload` — `nil` for cron jobs; JSON bytes for ad-hoc jobs.
- Returns a human-readable result string (stored in `job_runs.result`) or an error.

Every cron-triggered run creates a row in `job_runs` with a status: `planned` → `running` → `done` (or `errored`). Failed runs are retryable from the frontend.

#### Ad-Hoc Jobs (On-Demand)

For work triggered by user actions or RPC calls, enqueue a job at runtime.

**Register** the job definition once (e.g. in your `init()` or an action handler). Plugins register through the server's `ActionHandler` pattern — see the internal VSS indexing job for a pattern, or use `RegisterPluginActionHandler` and call the server's jobManager:

```go
// In your plugin action handler:
func handleGenerateReport(db *sql.DB, noteID int64, _ string, _ json.RawMessage) (any, error) {
    // Enqueue a one-shot job. The caller needs a reference to the Manager,
    // which is available on the Server struct. For now, ad-hoc jobs are
    // best registered via action handlers that receive *sql.DB.
    //
    // Future: the Manager will be exposed through a registry similar to
    // RegisterPluginActionHandler. In the meantime, cron-based jobs cover
    // the majority of use cases.
    return map[string]string{"status": "enqueued"}, nil
}
```

#### Job Visibility

All job runs appear in the **frontend sidebar panel** (`JobQueue.vue`):

- ⚙ icon with a badge showing the count of `planned` + `running` jobs.
- Clicking opens a dropdown listing the 50 most recent runs.
- Each row shows: status icon (⏳⟳✓✗⊘), plugin/job name, timestamp, result or error.
- **Errored** and **cancelled** runs have a ↻ retry button.

#### API Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/jobs` | List last 50 runs + `pending_count` |
| `POST` | `/jobs/:id/retry` | Re-queue an errored/cancelled run |
| `POST` | `/jobs/:id/cancel` | Cancel a planned (not-yet-running) job |

#### Architecture

```
┌──────────┐    ┌──────────┐    ┌──────────┐
│ Scheduler │───▶│  Queue   │───▶│  Worker  │───▶ job task
│ (ticks)  │    │ (SQLite) │    │  (2 by   │
└──────────┘    └──────────┘    │  default) │
                                └──────────┘
                                      │
                                 ┌────▼──────┐
                                 │ job_runs   │
                                 │ (history)  │
                                 └────────────┘
```

**Key guarantees**:
- **Atomic dequeue** — Workers claim jobs via compare-and-swap (`UPDATE WHERE id=? AND status='planned'`). SQLite serializes writes, so no two workers ever grab the same job.
- **Zombie recovery** — On startup, any job stuck in `running` (from a crashed server) is reset to `planned` so it re-executes.
- **Data retention** — A built-in `@daily` janitor deletes job runs older than 30 days, preventing unbounded table growth.
- **WAL + busy timeout** — The DB opens with `_journal_mode=WAL&_busy_timeout=5000` so concurrent worker writes don't fail with `database is locked`.

### Existing Plugins (Reference)

| Plugin | ID | Directory | Features | Interfaces |
|---|---|---|---|---|
| Example | `example` | `pkg/notetype/example/` | Minimal checklist — use as a starting point | ManifestProvider, ConfigValidator, ConfigSaver, ConfigLoader |
| Recipe | `recipe` | `pkg/notetype/recipe/` | Ingredient table with name/amount/unit, inline editing | ManifestProvider, ConfigValidator, ConfigSaver, ConfigLoader |
| Recipe Overview | `recipe_overview` | `pkg/notetype/recipeoverview/` | Dashboard listing all recipe notes, grocery list generation via RPC action | ManifestProvider, ViewBuilder, ActionHandler |
| Index | `index` | `pkg/notetype/index/` | Tag-based note index (global or local scope) | ManifestProvider, ConfigValidator, ConfigSaver, ConfigLoader, ViewBuilder |

## Design

Color Palette:

<div style="background:#01101f;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Abyssal Navy — <strong style="margin-left:8px">#01101f</strong></div>

<div style="background:#6d9484;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Sage Teal — <strong style="margin-left:8px">#6d9484</strong></div>

<div style="background:#ffbf59;color:#111;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Warm Amber — <strong style="margin-left:8px">#ffbf59</strong></div>

<div style="background:#bf0604;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Crimson Flame — <strong style="margin-left:8px">#bf0604</strong></div>

<div style="background:#960c05;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Blood Garnet — <strong style="margin-left:8px">#960c05</strong></div>
