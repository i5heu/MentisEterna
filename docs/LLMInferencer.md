# LLM Inferencer — Remote LLM Provider Notes

An `llm_inferencer` note type for MentisEterna: a **remote LLM inference
provider configured entirely in a note** — the "business logic lives in
notes" paradigm applied to model selection. One note defines the provider
endpoint (OpenRouter, Mistral La Plateforme, or any OpenAI-compatible
server), the API key, a model for each of the four capability tiers —
**big / middle / small / tiny** — and maps the app's LLM usages
(e.g. `rss_classify`) to those tiers. Usages resolve through the **note
hierarchy** (nearest inferencer note wins) and fall back to the
`config.toml` feature/tier system when nothing binds.

## Product decisions (settled with user)

| Decision | Choice |
|---|---|
| Providers | `openrouter` (default base `https://openrouter.ai/api`), `mistral` (`https://api.mistral.ai`), and `compatible` (any OpenAI-compatible endpoint, base URL required). The chat client appends `/v1/chat/completions`; both OpenRouter and Mistral speak the OpenAI chat-completions shape. |
| API key | **Stored in the note config, write-only.** `LoadConfig` never returns it — note views only see `api_key_set: true/false`. Saving with an empty `api_key` **keeps** the stored key (normal UI round-trips can't wipe it); typing a new one replaces it; the `clear_api_key` action removes it. The key lives only in `ct_llm_inferencer_config.api_key` (and encrypted backups) — it is never serialized into any view, action result, or classifier metadata. |
| Tiers | Four fixed capability tiers: `big`, `middle`, `small`, `tiny`. Each tier has a model and an optional per-tier `base_url` override. |
| Usage mapping | A `usages` map: usage id → `{tier}` or `{model}` (direct model override, XOR). An empty map + `default_tier` binds all usages to one tier. Mappings for usages whose call sites aren't migrated yet are accepted and shown as "pending backend wiring" — they activate when the backend starts routing that usage. |
| Resolution | Same nearest-wins walk as `rss_prompt`: anchor's own subtree (BFS) → siblings → uncles up the ancestor chain → **top level** (notes without a parent are the implicit tree root). Nothing binds → `config.toml` feature/tier client. |
| OCR / STT / embeddings | Deliberately out of scope: different API shapes (image/audio/file endpoints, dedicated models). They stay bound to `config.toml`. |
| Wired in v1 | `rss_classify` (RSS article classification). Others (`title`, `tags`, `subtasks`) are pre-wired into the catalog and the resolver — see "Wiring a new usage" below. |

## 1. The note and its config

```
🧠 GPT-6 via OpenRouter (type: llm_inferencer)
│   config: provider, base_url, api_key (write-only), tiers{big,middle,small,tiny},
│           default_tier, usages{rss_classify: {tier: small}, ...}
│   view:   provider label, key-set flag, tier table, resolved usage bindings,
│           warnings (incomplete bindings, endpoint overrides)
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
    "title":       { "model": "prov/other" }
  }
}
```

- `api_key` **never round-trips**: `LoadConfig` always returns `""`, plus the
  computed `api_key_set` boolean. Save semantics: empty keeps, non-empty
  replaces, `clear_api_key` removes.
- `base_url` empty = provider default; per-tier `base_url` (most specific)
  beats the note-level `base_url`, which beats the provider default.
- Unknown usage ids are rejected by validation (typos surface immediately);
  known-but-unwired ids are allowed (shown as pending backend wiring).

Tables: `ct_llm_inferencer_config` (one row per note; `api_key` never
selected by `LoadConfig`) and `ct_llm_inferencer_tiers` (up to 4 rows per
note). Both cascade away with the note.

## 2. Resolution (`resolve.go`)

`ResolveChat(db, fromNoteID, usage)` walks for the nearest
`llm_inferencer` note that yields a **concrete model** for the usage:

1. the anchor's own subtree (BFS by `parent_id`, shallowest then lowest id),
2. each ancestor's children — siblings, then uncles — up the chain,
3. the **top level** (implicit tree root: notes with `parent_id IS NULL`)
   — the last scope, so one top-level inferencer note binds the whole tree
   and any closer note overrides it locally.

Binding rules per candidate note:

- explicit `usages[usage].model` → binds with that model,
- explicit `usages[usage].tier` with a configured model on that tier → binds,
  **without** a model the binding is incomplete and does NOT capture
  (surfaced as a view warning),
- otherwise `default_tier` when that tier has a model → binds,
- otherwise the note is skipped.

Multiple binders at the same level → lowest note id wins, and the winner
carries a warning (e.g. "multiple llm_inferencer notes bind rss_classify at
the same level: using note 42").

The anchor note itself is never a candidate (same semantics as `rss_prompt`;
its *children*, *siblings*, and *ancestors' children* are).

Nothing binds → the `config.toml` feature/tier client
(`llm.NewFeatureChatClient`) — `Source: "config"` in the returned
`ResolvedChat`. The resolved **API key never leaves the resolver as data**:
it goes straight into the `*llm.ChatClient` it returns; `ResolvedChat` is
JSON-safe.

## 3. Provider client (`internal/llm`)

`llm.NewChatClientWithKey(baseURL, model, apiKey)` — an OpenAI-compatible
`ChatClient` that sends `Authorization: Bearer <key>` when a key is set.
API-keyed clients skip the LocalAI idle-stop machinery (`BeginBackendUse`
is a no-op for them — the `/backend/shutdown` behavior is LocalAI-only and
must never fire at OpenRouter/Mistral).

The **usage catalog** also lives in `internal/llm` (`usage.go`):

| usage id | label | wired | config.toml tier |
|---|---|---|---|
| `rss_classify` | RSS Classification | ✅ | medium |
| `title` | Note Title Generation | — (pending) | tiny |
| `tags` | Auto Tags | — (pending) | medium |
| `subtasks` | Subtask Generation | — (pending) | smart |

`Wired` = the call site resolves through `ResolveChat` today. Others can be
configured now and activate with the code that wires them.

## 4. Actions

| action | what it does |
|---|---|
| `test` | One tiny chat completion ("Reply with the single word: OK") through the **exact production client** the resolver hands out — same transport, auth header, and retry policy. Params: optional `{tier}` or `{model}` (XOR; both empty = default tier). Returns `{ok, latency_ms, model, base_url, provider, reply, error}`. |
| `list_models` | `GET <base>/v1/models` (OpenAI-compatible shape; works for OpenRouter and Mistral). Feeds the frontend model datalist; capped at 500. |
| `clear_api_key` | Deletes the stored key. Refresh strategy `reload_view` so open tabs update. |

## 5. Wiring a new usage (the deep-paradigm recipe)

1. Add the usage entry to `usageSpecs` in `internal/llm/usage.go` (id,
   feature key, label, description, `Wired: false`).
2. At the call site (where the note/context is known), replace the fixed
   feature client with a resolution:

   ```go
   client, res, err := llminferencer.ResolveChat(db, noteID, "<usage>")
   // on err: fall back to llm.NewFeatureChatClient("<usage>")
   ```

3. Flip the catalog entry to `Wired: true`. Any inferencer note that already
   maps the usage starts routing through it immediately.

## 6. RSS classification (`rss_classify`) as wired

`pkg/notetype/rss/classify.go` — the `newClassifier` seam is now
`newClassifier(db, noteID)`:

```go
client, _, err := llminferencer.ResolveChat(db, noteID, "rss_classify")
if err != nil || client == nil {
    return llmClassifier{client: llm.NewRSSClassifierClient()} // graceful degradation
}
return llmClassifier{client: client}
```

Classification (cron batch + manual reclassify) resolves once per run,
anchored at the reader note — so a reader nested under a branch with its own
inferencer note classifies through that branch's provider, and everything
else falls back up the tree, ultimately to `config.toml`.

## 7. Frontend

`frontend/src/note-types/llm_inferencer/LlmInferencerNoteType.vue` (registry
id `llm_inferencer`):

- **Editor**: provider select, base URL (placeholder = provider default),
  write-only key input (password field; "leave empty to keep, type to
  replace"), default-tier select, one model row per tier with a per-tier
  **Test** button, and a per-usage mapping editor (not mapped / by tier /
  direct model) with the live preview `→ tier small → prov/small`. A
  "Load models" button fills a `<datalist>` for the model inputs.
- **Viewer**: provider badge, key-set chip, tier table, resolved usage
  bindings with active/pending badges, warnings, and a connection test.
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
  (manifest/config round-trip/view/actions) plus: write-only key semantics
  (mask + keep-on-empty + replace + clear), binding rules, hierarchy
  resolution (subtree/sibling/uncle/top-level, nearest wins, lowest-id ties,
  incomplete-binding fall-through, per-tier base URL), action tests against
  `httptest` providers (auth header, latency, replies, model list), guards
  for nil db (harness contract).
- `go test ./internal/llm/` — keyed-client behavior (Bearer header, no auth
  header when keyless, idle-stop never fires for keyed clients) and the
  usage catalog/feature factory.
- `go test ./pkg/notetype/rss/` — the classifier seam stubs are
  `newClassifier(db, noteID)`; one integration test proves resolution
  through a sibling inferencer note end-to-end.
