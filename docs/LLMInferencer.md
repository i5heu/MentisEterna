# LLM Inferencer — the App's Remote LLM Provider Note

An `llm_inferencer` note type for MentisEterna: a **remote LLM inference
provider configured entirely in a note** — the "business logic lives in
notes" paradigm applied to model selection. **The note type is a
singleton: exactly one LLM Inferencer note may exist per app** (the server
rejects creating or switching to a second one with a 400, and the frontend
type picker hides the type once an instance exists). That one note defines
the provider endpoint (OpenRouter, Mistral La Plateforme, or any
OpenAI-compatible server), the API key, a model for each of the four
capability tiers — **big / middle / small / tiny** — and maps the app's
LLM usages (e.g. `rss_classify`) to those tiers, to direct models, or
explicitly to the **local LLM** (the `config.toml` feature/tier system).

The note applies **globally**: usages resolve through it from anywhere in
the note tree. Unmapped usages — and usages pinned to local — resolve
through the `config.toml` feature/tier system, whose per-usage defaults are
surfaced in the note's UI ((system default tier → model), with a
**Reset to system defaults** button that clears all overrides).

## Product decisions (settled with user)

| Decision | Choice |
|---|---|
| **Uniqueness** | **One `llm_inferencer` note per app** (manifest `Singleton: true`). `createNoteCore` and `updateNote` count existing notes of the type inside the write transaction and reject a second one with a 400. The frontend type picker hides the type while an instance exists (edit one instead). Legacy databases created before the rule may hold several notes: resolution uses the lowest note id and warns — delete the extras. |
| Providers | `openrouter` (default base `https://openrouter.ai/api`), `mistral` (`https://api.mistral.ai`), and `compatible` (any OpenAI-compatible endpoint, base URL required). Endpoint URLs are built by `llm.OpenAIEndpoint(base, path)`: **any common base shape works** — the API root, an OpenAI-SDK-style base ending in `/v1` (what provider docs tell users to paste), or even the full endpoint URL — and is normalized to `<base>/v1/chat/completions` (and `/v1/models`), so the version segment is never doubled into a provider 404. Both OpenRouter and Mistral speak the OpenAI chat-completions shape. |
| API key | **Stored in the note config, write-only.** `LoadConfig` never returns it — note views only see `api_key_set: true/false`. Saving with an empty `api_key` **keeps** the stored key (normal UI round-trips can't wipe it); typing a new one replaces it; the `clear_api_key` action removes it. The key lives only in `ct_llm_inferencer_config.api_key` (and encrypted backups) — it is never serialized into any view, action result, or classifier metadata. |
| Tiers | Four fixed capability tiers: `big`, `middle`, `small`, `tiny`. Each tier has a model and an optional per-tier `base_url` override. |
| Usage mapping | A `usages` map: usage id → `{tier}`, `{model}` (direct model override), or `{local: true}` (explicit pin to the config.toml local LLM — beats the note's `default_tier`). XOR: exactly one of the three. Mappings for usages whose call sites aren't migrated yet are accepted and shown as "pending backend wiring" — they activate when the backend starts routing that usage. |
| Resolution | Global through the app's single note: call sites ask `ResolveChat(db, usage)` with no anchor. Nothing binds → `config.toml` feature/tier client. An explicit `{local: true}` binding resolves to the same `config.toml` client, but reported as `Source: "local"` (the pin is the user's explicit choice, distinct from a plain fallback). |
| System defaults | The view reports, per usage, what `config.toml` would serve (`system_default: {tier, model, base_url}` via `llm.UsageSystemDefault`), and effective bindings (explicit / `via_default` / `local` flag). The frontend shows these defaults next to every usage in viewer and editor, and the editor's **Reset to system defaults** button clears the usage map + default tier so every usage uses the `config.toml` defaults again (provider, endpoint, key, and tier model definitions stay). |
| OCR / STT / embeddings | Deliberately out of scope: different API shapes (image/audio/file endpoints, dedicated models). They stay bound to `config.toml`. |
| Wired in v1 | `rss_classify` (RSS article classification). Others (`title`, `tags`, `subtasks`) are pre-wired into the catalog and the resolver — see "Wiring a new usage" below. |

## 1. The note and its config

```
🧠 LLM Inferencer (type: llm_inferencer — ONE per app, applies app-wide)
│   config: provider, base_url, api_key (write-only), tiers{big,middle,small,tiny},
│           default_tier, usages{rss_classify: {tier: small} | {model: x} | {local: true}, ...}
│   view:   provider label, key-set flag, tier table, resolved usage bindings,
│           per-usage system (config.toml) defaults, warnings
```

Config JSON shape (identical across `ValidateConfig` / `SaveConfig` /
`LoadConfig`):

```json
{
  "provider": "openrouter",
  "base_url": "",
  "api_key": "",
  "api_key_set": true,
  "default_tier": "middle",
  "tiers": {
    "big":    { "model": "prov/huge", "base_url": "" },
    "middle": { "model": "prov/mid" },
    "small":  { "model": "prov/small" },
    "tiny":   { "model": "prov/tiny" }
  },
  "usages": {
    "rss_classify": { "tier": "small" },
    "title":       { "local": true },
    "tags":        { "model": "prov/other" }
  }
}
```

- `api_key` **never round-trips**: `LoadConfig` always returns `""`, plus the
  computed `api_key_set` boolean. Save semantics: empty keeps, non-empty
  replaces, `clear_api_key` removes.
- `base_url` empty = provider default; per-tier `base_url` (most specific)
  beats the note-level `base_url`, which beats the provider default. A
  trailing `/v1` (or a full endpoint URL) is normalized at request time, so
  both conventions dial the same endpoint.
- Unknown usage ids are rejected by validation (typos surface immediately);
  known-but-unwired ids are allowed (shown as pending backend wiring).

Tables: `ct_llm_inferencer_config` (one row per note; `api_key` never
selected by `LoadConfig`) and `ct_llm_inferencer_tiers` (up to 4 rows per
note). Both cascade away with the note.

## 2. Resolution (`resolve.go`)

`ResolveChat(db, usage)` — no anchor parameter: the note type is a
singleton, so the one note (wherever it sits in the tree) applies globally.

1. The app's `llm_inferencer` note — if it yields a **concrete model** for
   the usage — binds:
   - explicit `usages[usage].model` → binds with that model,
   - explicit `usages[usage].tier` with a configured model on that tier →
     binds; **without** a model the binding is incomplete and does NOT
     capture (surfaced as a view warning),
   - explicit `usages[usage].local` → `Source: "local"`: the usage is
     pinned to the config.toml feature/tier client (the local LLM) —
     the note's `default_tier` does NOT capture it,
   - otherwise `default_tier` when that tier has a model → binds,
   - otherwise the note is skipped for that usage.

2. Nothing (or nobody) binds → the `config.toml` feature/tier client
   (`llm.NewFeatureChatClient`) — `Source: "config"`.

Databases from **before the singleton rule** may hold several
`llm_inferencer` notes: they resolve in id order (lowest wins), the
winner carries a warning, and the view warns about the duplicates —
delete the extras; the server refuses to create new ones.

The resolved **API key never leaves the resolver as data**: it goes
straight into the `*llm.ChatClient` it returns; `ResolvedChat` is
JSON-safe. `ResolvedChat.Source` is one of `note` / `local` / `config`.

## 3. Provider client (`internal/llm`)

`llm.NewChatClientWithKey(baseURL, model, apiKey)` — an OpenAI-compatible
`ChatClient` that sends `Authorization: Bearer <key>` when a key is set.
API-keyed clients skip the LocalAI idle-stop machinery (`BeginBackendUse`
is a no-op for them — the `/backend/shutdown` behavior is LocalAI-only and
must never fire at OpenRouter/Mistral).

All endpoint URLs (chat, embeddings, OCR, STT, model lists — for both the
`config.toml` clients and inferencer notes) go through
`llm.OpenAIEndpoint(base, path)`, which accepts every base-URL convention
(API root, `…/v1`, full endpoint) and strips trailing slashes — the
OpenAI-SDK bases (`https://openrouter.ai/api/v1`) used to produce
`…/v1/v1/chat/completions` and provider 404s.

The **usage catalog** also lives in `internal/llm` (`usage.go`):

| usage id | label | wired | config.toml tier |
|---|---|---|---|
| `rss_classify` | RSS Classification | ✅ | medium |
| `title` | Note Title Generation | — (pending) | tiny |
| `tags` | Auto Tags | — (pending) | medium |
| `subtasks` | Subtask Generation | — (pending) | smart |

`Wired` = the call site resolves through `ResolveChat` today. Others can be
configured now and activate with the code that wires them.

`llm.UsageSystemDefault(usage)` returns the effective `config.toml`
binding for a usage — the per-usage "system default"
(`{Tier, Model, BaseURL}`) shown in the inferencer note's UI.

## 4. Actions

| action | what it does |
|---|---|
| `test` | One tiny chat completion ("Reply with the single word: OK") through the **exact production client** the resolver hands out — same transport, auth header, and retry policy. Params: optional `{tier}` or `{model}` (XOR; both empty = default tier). Returns `{ok, latency_ms, model, base_url, provider, reply, error}`. Failures carry the full dialed URL (`llm request to <url> returned 404: …`) — the first thing to check on an endpoint 404. |
| `list_models` | `GET <base>/v1/models` (OpenAI-compatible shape; works for OpenRouter and Mistral). Feeds the frontend model datalist; capped at 500. Same base-URL normalization; errors include the dialed URL. |
| `clear_api_key` | Deletes the stored key. Refresh strategy `reload_view` so open tabs update. |

## 5. Wiring a new usage (the deep-paradigm recipe)

1. Add the usage entry to `usageSpecs` in `internal/llm/usage.go` (id,
   feature key, label, description, `Wired: false`).
2. At the call site, replace the fixed feature client with a resolution:

   ```go
   client, res, err := llminferencer.ResolveChat(db, "<usage>")
   // on err: fall back to llm.NewFeatureChatClient("<usage>")
   ```

3. Flip the catalog entry to `Wired: true`. Any inferencer note that already
   maps the usage starts routing through it immediately.

## 6. RSS classification (`rss_classify`) as wired

`pkg/notetype/rss/classify.go` — the `newClassifier` seam:

```go
client, _, err := llminferencer.ResolveChat(db, "rss_classify")
if err != nil || client == nil {
    return llmClassifier{client: llm.NewRSSClassifierClient()} // graceful degradation
}
return llmClassifier{client: client}
```

Classification (cron batch + manual reclassify) resolves once per run.
The inferencer note is global, so reader placement in the tree no longer
matters; a `{local: true}` pin or no binding falls back to `config.toml`.

## 7. Frontend

`frontend/src/note-types/llm_inferencer/LlmInferencerNoteType.vue` (registry
id `llm_inferencer`, manifest `singleton: true`):

- **Uniqueness in the picker**: `isSingletonType(id)` + the manifest flag
  make NotesView's edit-mode type select drop `llm_inferencer` once an
  instance exists (the search type filter keeps it). Editing the existing
  note keeps it listed; the server 400s violations regardless.
- **Editor**: provider select, base URL (placeholder = provider default),
  write-only key input (password field; "leave empty to keep, type to
  replace"), default-tier select, one model row per tier with a per-tier
  **Test** button, and a per-usage mapping editor with four modes:
  **Not mapped (system default)** / **By tier** / **Direct model** /
  **Local (config.toml)**. Every usage row previews the effective
  resolution (`Resolves to <model> via <source> · System default:
  tier → model`). A **Load models** button fills a `<datalist>` for the
  model inputs.
- **Reset to system defaults**: the usage-mappings header button clears
  the usage map and the default tier (with a confirm) so every usage uses
  the `config.toml` system defaults again; provider/endpoint/key/tier
  definitions stay.
- **Viewer**: provider badge, key-set chip, "unique · applies app-wide"
  chip, tier table, and a usage table with four columns: usage (with
  active/pending badge), this note's binding, the model it resolves to
  via which source, and the config.toml system default. Warnings
  (incomplete bindings, endpoint overrides, legacy duplicate notes) and
  a connection test round it off.
- The emitted payload strips empty tiers (backend validation rejects them)
  and only includes `api_key` when the user typed one.

## 8. Security posture

- The API key sits in `ct_llm_inferencer_config.api_key` — server-side only.
  It is excluded from every API response (`LoadConfig` masks it; views
  return `api_key_set` only; `ResolvedChat` carries no key). Backups are
  AES-256-GCM encrypted (`docs/Backups.md`).
- The `test`/`list_models` actions run with the caller's note scope and use
  the stored key only against the note's own configured endpoint.
- Provider endpoints are user-configured exactly like `[llm] base_url` in
  `config.toml` (same trust level — a single-user self-hosted app).

## 9. Testing

- `go test ./pkg/notetype/llminferencer/` — plugin harness battery
  (manifest incl. singleton flag/config round-trip/view/actions) plus:
  write-only key semantics (mask + keep-on-empty + replace + clear),
  binding rules (incl. `{local: true}` pins), global resolution (single
  note binds anywhere in the tree, legacy duplicates: lowest id wins +
  warning, incomplete-binding fall-through, per-tier base URL, local pin
  beats default tier), action tests against `httptest` providers (auth
  header, latency, replies, model list), guards for nil db (harness
  contract), and the view's per-usage system defaults + duplicate warning.
- `go test ./internal/server/ -run TestSingleton` — singleton enforcement:
  second create rejected 400, switching another note's type rejected 400,
  editing/downgrading the singleton stays legal, `singleton: true` in the
  `/note-types` catalog.
- `go test ./internal/llm/` — keyed-client behavior (Bearer header, no auth
  header when keyless, idle-stop never fires for keyed clients), the usage
  catalog/feature factory, `UsageSystemDefault` matching the feature
  clients, and the endpoint builder (table cases + rapid properties:
  convention agreement, idempotence, no doubled `/v1`; plus client-level
  regressions for `/v1`-style bases on chat and embeddings, and error
  messages naming the dialed URL).
- `go test ./pkg/notetype/rss/` — the classifier seam stubs resolve
  through the global inferencer note end-to-end (model/endpoint/key from
  the note, config feature client before/without one).
