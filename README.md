# MentisEterna

> ⚠️⚠️⚠️ Do not use this. ⚠️⚠️⚠️   
> This is a terrible generated mess of doom i created for prototyping. I only use it in a private VPN. It is absolutly not secure or safe to use.    

<p align="center" style="margin: 2em;">
    <img width="280" height="280" style="border-radius: 3%; max-width: 100%" alt="Logo of OuroborosDB" src=".media/MentisEterna_logo.svg">
</p>


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
- [ ] Auto Title Generator
  - [ ] OLLAMA url configurable via env var
- [ ] SQLite AES-256 in OFB mode
- [ ] Security Review and Auth hardening (single user focus and auth focus, evrything else not a priority)
- [ ] Encrypted Backup

## TODO Future
- [ ] support AsciiDoc and Markdown (with live preview)
- [ ] OCR for images and pdfs
- [ ] speech to text notes
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

## TODO Note Types
- [x] Recipe (with ingredient table)
  - [x] Recipe Overview (dashboard listing all recipe notes, grocery list generation via RPC action)
- [ ] Task Sytem
  - [ ] Task note type - title, status, dificulty, priority, description, due date, time estimation, time used, recurring options
  - [ ] Task overview dashboard - list all tasks, filter by status, due date, etc. 
  - [ ] Daily task list - 3 tasks per day
- [ ] Home Note Type
  - [ ] Shows latest notes, current tasks, weather
- [ ] Gridfinity note type
- [ ] Car refuling log note type
  - [ ] care refuleling log overview dashboard
- [ ] Wants and wishes note type
  - [ ] wants and wishes overview dashboard
- [ ] Web Fetcher note type - fetches a webpage and extracts the main content (like mercury parser) and saves it as a note 

## Prerequisites

- Ollama: [Installation Guide](https://ollama.com/docs/installation)
- Qwen/Qwen3-Embedding-4B-GGUF: `ollama pull hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M`

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

## Creating Custom Note Types

Note types are plugin-based. Each type lives in `pkg/notetype/<name>/` and implements the `NoteType` interface. The server discovers and initializes all plugins automatically at startup.

### Quick Start

1. **Create your package** at `pkg/notetype/yourtype/yourtype.go`.

2. **Implement the interface** (`pkg/notetype/notetype.go`):

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
    // Return nil if valid, error if not.
    return nil
}

func (p *YourPlugin) ProcessSave(ctx context.Context, tx *sql.Tx, userID int, noteID int64, raw json.RawMessage) error {
    // Called inside an active SQL transaction. Persist your data here.
    // Use DELETE + INSERT for upserts (SQLite doesn't support upsert with FKs cleanly).
    return nil
}

func (p *YourPlugin) ProcessLoad(ctx context.Context, db *sql.DB, userID int, noteID int64) (any, error) {
    // Return your custom data — it will appear as note.custom_data in the frontend.
    return nil, nil
}

func (p *YourPlugin) UISchema() json.RawMessage {
    // Return a JSON schema describing your form. Intended for FormKit but
    // you can also handle rendering yourself in the frontend.
    return nil
}

func (p *YourPlugin) CronJobs() []notetype.CronJob {
    // Return background jobs, e.g.:
    // return []notetype.CronJob{{
    //     Schedule: "@daily",
    //     Task:     func(db *sql.DB) error { ... },
    // }}
    return nil
}
```

3. **Register in main** — add a blank import to `cmd/server/main.go`:

```go
import (
    _ "github.com/i5heu/MentisEterna/pkg/notetype/yourtype"
)
```

4. **Add frontend rendering** — edit `frontend/src/components/NoteTypeRenderer.vue` and add a block for your type:

```vue
<div v-if="note.type === 'yourtype'" class="yourtype-editor">
    <!-- Your custom Vue template here -->
</div>
```

5. **Register in the UI** — add your type to `typeOptions` in `frontend/src/views/NotesView.vue`:

```js
const typeOptions = [
    { value: "standard", label: "Standard Note" },
    { value: "yourtype",  label: "Your Type" },
];
```

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

This runs **14 sub-tests** automatically:

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
| `Actions_Handler` | (placeholder) Action handler is registered |

**Key check — payload shape consistency**: The `SaveLoad_RoundTrip` test calls `ProcessSave` → `ProcessLoad` → `json.Marshal` → `Validate`. If your `ProcessLoad` returns a different JSON shape than `Validate` expects (e.g. a raw array `[...]` instead of `{"items": [...]}`), this test fails with an explicit hint. This is the single most common bug when writing plugins.

**Helper functions** for writing additional custom tests:

```go
func TestMyCustomBehavior(t *testing.T) {
    d := plugintest.DB(t, &YourPlugin{})       // in-memory DB with notes + plugin schema
    noteID := plugintest.CreateNote(t, d, "My Note", &YourPlugin{})
    plugintest.SavePayload(t, d, &YourPlugin{}, noteID, json.RawMessage(`...`))
    // ... your assertions here ...
}
```

**Fast iteration mode**: Use `plugintest.Quick()` during development — runs only 3 tests (validation + UI schema) instead of 14.

### Interface Reference

| Method | When Called | Purpose |
|---|---|---|
| `ID()` | Registration | Unique short name (e.g. `"recipe"`) |
| `InitSchema(db)` | Server startup | Create `ct_<id>_*` tables |
| `Validate(payload)` | Before save | Return `nil` if the custom_data JSON is valid |
| `ProcessSave(ctx, tx, userID, noteID, payload)` | Inside SQL transaction | Persist plugin data (DELETE old, INSERT new) |
| `ProcessLoad(ctx, db, userID, noteID)` | Note fetch | Return custom data for the frontend |
| `UISchema()` | Note fetch | FormKit-compatible JSON schema |
| `CronJobs()` | Server startup | Background tasks with cron schedules |

### Conventions

- **Table names**: Always prefix with `ct_<pluginID>_` (e.g. `ct_recipe_ingredients`).
- **Foreign keys**: Always reference `notes(id) ON DELETE CASCADE` so cleanup is automatic.
- **Upserts**: SQLite VSS tables don't support `INSERT OR REPLACE` or `UPDATE`. Delete first, then insert.
- **Payload shape**: `Validate`, `ProcessSave`, and `ProcessLoad` should all use the same JSON structure (wrap arrays in an object — e.g. `{"ingredients": [...]}` not `[...]`).
- **Cron schedules**: Supports `@every 1h`, `@daily`, `@hourly`. The scheduler is lightweight — for full cron expressions, swap in `robfig/cron/v3`.
- **❌ NEVER store plugin config or data in the note body (`updates` table)**. The note body is for user-written markdown content only. Plugin configuration and data MUST live in dedicated plugin tables (`ct_<pluginID>_*`). Reading from `updates.body` inside `ProcessLoad` to recover plugin state is a misuse and will not be accepted. Always create proper tables via `InitSchema` and persist through `ProcessSave`.

### Plugin Actions (RPC)

Plugins can expose custom RPC endpoints at `POST /notes/:id/action` by calling `server.RegisterPluginActionHandler()` in an `init()` function:

```go
func init() {
    server.RegisterPluginActionHandler("yourtype", func(db *sql.DB, noteID int64, action string, params json.RawMessage) (any, error) {
        switch action {
        case "do_something":
            return doSomething(db, noteID)
        default:
            return nil, fmt.Errorf("unknown action: %s", action)
        }
    })
}
```

The frontend calls it via `pluginAction(token, noteId, "do_something", null)` from `api.js`.

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

| Plugin | ID | Directory | Features |
|---|---|---|---|
| Recipe | `recipe` | `pkg/notetype/recipe/` | Ingredient table with name/amount/unit, inline editing |
| Recipe Overview | `recipe_overview` | `pkg/notetype/recipeoverview/` | Dashboard listing all recipe notes, grocery list generation via RPC action |
| Example | `example` | `pkg/notetype/example/` | Minimal checklist — use as a starting point |

## Design

Color Palette:

<div style="background:#01101f;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Abyssal Navy — <strong style="margin-left:8px">#01101f</strong></div>

<div style="background:#6d9484;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Sage Teal — <strong style="margin-left:8px">#6d9484</strong></div>

<div style="background:#ffbf59;color:#111;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Warm Amber — <strong style="margin-left:8px">#ffbf59</strong></div>

<div style="background:#bf0604;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Crimson Flame — <strong style="margin-left:8px">#bf0604</strong></div>

<div style="background:#960c05;color:#fff;padding:10px;border-radius:6px;max-width:420px;margin:8px 0;">Blood Garnet — <strong style="margin-left:8px">#960c05</strong></div>
