# MentisEterna

> ⚠️⚠️⚠️ Do not use this. ⚠️⚠️⚠️   
> This is a terrible generated mess of doom i created for prototyping. I only use it in a private VPN. It is absolutly not secure or safe to use.    

<p align="center" style="margin: 2em;">
    <img width="280" height="280" style="border-radius: 3%; max-width: 100%" alt="Logo of OuroborosDB" src=".media/MentisEterna_logo.svg">
</p>


## TODO MVP
- [ ] Pin notes 
- [x] Chat like UI
- [ ] Note Types
- [ ] Pseudo-Plugins
  - [ ] Test harness
- [ ] cron system
- [ ] Job Queue Indicator
- [ ] S3 Media Storage (Encrypted)
- [ ] Note linking and backlinking
- [ ] Encrypted Backup
- [ ] SQLite AES-256 in OFB mode
- [ ] Security Review and Auth hardening
## TODO
- [ ] Auto Title Generator
  - [ ] alternative OLLAMA url
- [ ] OCR for images and pdfs
- [ ] speech to text notes
- [ ] tags
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

## Prerequisites

- Ollama: [Installation Guide](https://ollama.com/docs/installation)
- Qwen/Qwen3-Embedding-4B-GGUF: `ollama pull hf.co/Qwen/Qwen3-Embedding-4B-GGUF:Q4_K_M`

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
