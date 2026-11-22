# RSS Reader — Design & Implementation Plan

An `rss` note type ("RSS Reader") for MentisEterna. **Everything lives in one
note**: the feed list in its config, the ban words / penalty points in its
body, the merged article timeline in its view — except the classification
prompt, which stays **in its own note** (`rss_prompt`), resolved from the
hierarchy. Articles are fetched into fast plugin tables; an explicit
"Save as Note" promotes keepers into real child notes.

## Product decisions (settled with user)

| Decision | Choice |
|---|---|
| Shape | **One note = the whole reader.** Feeds + settings in its config, ban words / penalty points in its **own body** (user-written lines, parsed by the plugin — the plugin reads bodies like `recipe`/`print` do, never writes to them), articles in plugin tables under the note. |
| Prompt | The only exception: classification prompt lives in a separate **`rss_prompt` note** whose body (latest `updates` row) is the system prompt. Resolved from the hierarchy: nearest `rss_prompt` in the note's subtree, then siblings, then up the ancestor chain (nearest wins). None found ⇒ built-in default prompt. |
| LLM classification | Output is **structured JSON** `{category, summary, quality, tags[]}`; we add contract framing + validation + one retry. |
| Ban words / penalties | Matching article → rejected bucket, hidden behind a "show rejected" toggle; never silently destroyed. Penalty points accumulate; ≥ threshold ⇒ rejected. Threshold: `threshold:` line in body > config `reject_threshold` (default 5, 0 = disabled). |
| Article storage | Plugin tables, not auto-notes. "Save as Note" creates a real child note (with embeddings) under the RSS note — ingest-pipeline pattern. |

Out of scope for v1 in the last section.

---

## 1. Note types and hierarchy

Two note types, one Go package `pkg/notetype/rss`:

```
🗒️ My Reader (type: rss)          ← the entire RSS subsystem in ONE note
│   config: feeds[], interval, thresholds, caps…
│   body:   ban words / penalty lines (user-typed, parsed read-only)
│   view:   merged timeline + per-feed status
├── 🧠 Prompt (type: rss_prompt)  ← body = classification prompt (the exception)
└── 📄 saved article (type: standard, created by "Save as Note")
```

- `rss` — the reader. `HasConfig` (feeds + numeric settings), `HasView`
  (merged timeline), `HasActions`. Note title = reader name.
- `rss_prompt` — marker type. No config/view/actions; renders through the
  standard body textarea. Body = classification prompt.

**Prompt resolution** (`scope.go`) — for an `rss` note:

1. its own descendants (BFS by `parent_id`),
2. siblings (parent's children),
3. uncles, … up the ancestor chain — **nearest** `rss_prompt` wins.

Multiple at the same level → lowest note id wins + warning surfaced in the
view. None → built-in default prompt, flagged in the UI ("add an RSS Prompt
note"). Scope resolves live per refresh/view — moving the prompt note or the
reader instantly rebinds. One `rss_prompt` above several readers can serve
them all.

Deleting the reader note cascades its feed rows and items (`ct_rss_feeds` /
`ct_rss_items` FKs → `ON DELETE CASCADE`). Deleting the prompt note reverts
the reader to the default prompt at next refresh.

## 2. Architecture

```
pkg/notetype/rss/
├── rss.go        — 2 manifests + registration (rss, rss_prompt), InitSchema (DDL)
├── config.go     — config payload struct + validation
├── scope.go      — prompt resolution (nearest rss_prompt in hierarchy)
├── feed.go       — RSS 2.0 / Atom 1.0 parsing (stdlib encoding/xml + charset)
├── fetch.go      — polite HTTP fetch: conditional GET, timeouts, size caps, SSRF guard
├── extract.go    — full-content extractor (golang.org/x/net/html, readability-lite)
├── rules.go      — body rules parser (ban / penalty / threshold) + scoring
├── classify.go   — LLM classification: prompt loading, JSON contract, retry, test seam
├── refresh.go    — per-note refresh orchestration: dedup, rules, retention, summaries
├── jobs.go       — cron task refresh_feeds + ad-hoc task refresh_feeds_now
└── actions.go    — HandleAction dispatch
```

Data flow:

```
cron @every 15m ──▶ refresh_feeds(db)
                     │ for each rss note (per-feed interval check)
                     ▼
        resolve prompt (scope.go) + parse rules (body lines)
                     ▼
            fetch feeds (etags, 304 skip) ─▶ parse ─▶ dedup by (feed, item_key)
                     │                                        │
                     │                          feed content missing/short?
                     ▼                                        ▼
            rules scoring ◀──────────────── full-content fetch + extract
                     │
                     ▼
            insert items (state=new, classify pending)
                     ▼
            classify batch (≤25/run, prompt from scope) ─▶ llm.NewRSSClassifierClient
                     └─▶ job result summary (JobQueue + ws broadcast)
```

## 3. Data model (InitSchema DDL)

```sql
CREATE TABLE IF NOT EXISTS ct_rss_config (
    note_id            INTEGER PRIMARY KEY,
    interval_minutes   INTEGER NOT NULL DEFAULT 60,
    auto_classify      INTEGER NOT NULL DEFAULT 1,
    fetch_full_content INTEGER NOT NULL DEFAULT 1,
    max_items_per_feed INTEGER NOT NULL DEFAULT 100,
    retention_days     INTEGER NOT NULL DEFAULT 90,
    reject_threshold   REAL    NOT NULL DEFAULT 5,   -- 0 = score-rejection disabled
    FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS ct_rss_feeds (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id       INTEGER NOT NULL,
    url           TEXT    NOT NULL,
    name          TEXT    NOT NULL DEFAULT '',       -- '' → feed's own <title>
    enabled       INTEGER NOT NULL DEFAULT 1,
    etag          TEXT    NOT NULL DEFAULT '',
    last_modified TEXT    NOT NULL DEFAULT '',
    last_fetch_at TEXT,
    last_status   TEXT    NOT NULL DEFAULT '',      -- ok | not_modified | error
    last_error    TEXT    NOT NULL DEFAULT '',
    FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE,
    UNIQUE(note_id, url)
);

CREATE TABLE IF NOT EXISTS ct_rss_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id         INTEGER NOT NULL,
    item_key        TEXT    NOT NULL,             -- guid / atom:id; fallback hash(link|title|date)
    title           TEXT    NOT NULL,
    url             TEXT    NOT NULL DEFAULT '',
    author          TEXT    NOT NULL DEFAULT '',
    published_at    TEXT    NOT NULL DEFAULT '',  -- normalized UTC, '' if unknown
    fetched_at      TEXT    NOT NULL,
    prompt_note_id  INTEGER NOT NULL DEFAULT 0,  -- scope snapshot used for classification
    feed_content    TEXT    NOT NULL DEFAULT '',  -- raw RSS-provided content/description
    full_content    TEXT    NOT NULL DEFAULT '',  -- extracted page text
    full_status     TEXT    NOT NULL DEFAULT 'none', -- none|fetched|failed|too_big
    category        TEXT    NOT NULL DEFAULT '',
    summary         TEXT    NOT NULL DEFAULT '',
    tags_json       TEXT    NOT NULL DEFAULT '[]',
    quality         INTEGER NOT NULL DEFAULT 0,  -- 0–10 from LLM
    classify_status TEXT    NOT NULL DEFAULT 'pending', -- pending|done|error|disabled
    classify_error  TEXT    NOT NULL DEFAULT '',
    penalty_score   REAL    NOT NULL DEFAULT 0,
    penalty_json    TEXT    NOT NULL DEFAULT '[]', -- [{"phrase","points","occurrences"}]
    banned          INTEGER NOT NULL DEFAULT 0,  -- any ban word matched
    state           TEXT    NOT NULL DEFAULT 'new', -- new|read|starred|rejected (user reject)
    saved_note_id   INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(feed_id) REFERENCES ct_rss_feeds(id) ON DELETE CASCADE,
    UNIQUE(feed_id, item_key)
);
CREATE INDEX IF NOT EXISTS idx_ct_rss_items_feed  ON ct_rss_items(feed_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_ct_rss_items_state ON ct_rss_items(state, fetched_at DESC);
```

No tables for prompt notes — bodies read live from `updates`. Item content is
truncated to 200 KB. `prompt_note_id` snapshots which prompt classified the
item (audit + "summary came from prompt X" in the UI).

**Rejected** is computed: `banned=1 OR penalty_score >= threshold OR state='rejected'`.

## 4. Config contract (round-trips Validate/Save/Load)

```json
{
  "feeds": [
    {"url": "https://example.com/feed.xml", "name": "Example", "enabled": true}
  ],
  "interval_minutes": 60,
  "auto_classify": true,
  "fetch_full_content": true,
  "max_items_per_feed": 100,
  "retention_days": 90,
  "reject_threshold": 5
}
```

Validation (`config.go`):
- feed URLs: `http`/`https` scheme only, valid, de-duplicated, ≤ 50 per note
- `interval_minutes`: 15–10080 · `max_items_per_feed`: 10–1000 ·
  `retention_days`: 7–3650 · `reject_threshold`: 0–100
- SaveConfig: settings into `ct_rss_config`; feeds rewritten DELETE + INSERT,
  preserving `etag`/`last_modified`/`last_fetch_at` by URL match (plugin
  convention: never `INSERT OR REPLACE` under FKs)

`rss_prompt` has `HasConfig=false` — nothing to persist.

## 5. Prompt convention (classification)

The body of the resolved `rss_prompt` note is the classification instruction,
sent as the system message. The classifier appends a fixed contract suffix:

```
Return ONLY a JSON object, no extra text, exactly this shape:
{"category": "<short label>", "summary": "<1–2 sentences, plain text>",
 "quality": <0–10 integer>, "tags": ["<label>", ...]}
```

- No prompt note in scope ⇒ built-in default prompt (general categories,
  summary, signal-to-noise rating) — works out of the box; view reports
  `prompt: "default"` so the UI can nudge.
- User message = article title + feed name + URL + content (full_content
  preferred, feed_content fallback), capped at ~10k chars.
- Client: `llm.NewRSSClassifierClient()` bound to new feature `rss_classify`
  (default tier `medium`, overridable via `[llm.features.rss_classify]`).
- Parse: reuse the lenient JSON extraction used by tags/subtasks; strict
  struct validation; one retry with the parse error appended; failure ⇒
  `classify_status='error'` (item stays listed; next refresh re-picks up
  pending items).
- Backend lease (`llm.BeginBackendUse`) held across the batch so the model is
  not idle-stopped mid-run.
- Editing the prompt note body takes effect on next refresh / reclassify.

## 6. Rules convention (ban words / penalty points — in the rss note's body)

The reader note's own body is user space: prose is ignored; only rule-shaped
lines are parsed. Matching is case-insensitive against
`title + (full_content || feed_content)`:

```
Reading list filters:            ← prose — silently ignored
# comment lines are ignored
ban: crypto giveaway             ← hard reject (any occurrence ⇒ banned=1)
ban: sponsored post
press release: 6                 ← +6 penalty points per occurrence
webinar: 2
wonderful deep dive: -3          ← negative = bonus
threshold: 8                     ← overrides config reject_threshold
```

- `ban: <phrase>` — hard reject.
- `<phrase>: <points>` — points per occurrence (negative allowed);
  per-phrase occurrence cap 10 (bounds pathological repetition).
- `threshold: <n>` — per-note override of `reject_threshold`.
- Rule-*shaped* but malformed lines (e.g. `foo: abc`, empty `ban:`) collect
  into `rules_warning` shown in the view; ordinary prose is never warned.

Rules are re-read (from the latest `updates` row) on every refresh and on
`reapply_rules`. Body edits follow the normal note save path — the plugin
only ever reads.

## 7. Fetch pipeline

**Feed fetch** (`fetch.go`, shared client):
- `http.Client{Timeout: 20s}`, `User-Agent: MentisEterna-RSS/1.0`
- conditional GET: `If-None-Match` / `If-Modified-Since` per feed row;
  304 ⇒ `last_status='not_modified'`, skip
- response capped at 2 MB (`too_big` status for pages)
- redirect limit 5, every redirect target re-checked by the guard
- **SSRF guard**: DNS-resolve then dial only public IPs — reject
  loopback/RFC1918/link-local/fc00::/7 (protects the LAN-hosted app and
  LocalAI). Behind a `var isPrivateHost` seam so tests can run `httptest`.

**Feed parsing** (`feed.go`): RSS 2.0 + Atom 1.0 via `encoding/xml`
(namespaces: content:encoded, dc:creator), non-UTF-8 via
`x/net/html/charset.NewReader` (same new dependency as extraction). Dates
normalized (RFC1123Z/RFC1123/ISO-8601 → UTC). `item_key` = guid/atom:id,
fallback `sha256(link|title|published)`.

**Full-content resolution** (`extract.go`): for items where feed content is
missing OR < 500 runes and `fetch_full_content` is on:
1. fetch article URL (same client + guards),
2. parse with `golang.org/x/net/html`, strip script/style/nav/aside/footer/
   header/form/iframe/svg,
3. prefer `<article>` / `<main>` / `[role=main]`; else readability-lite:
   score candidate containers by text length ÷ link density, pick best,
4. emit plain text with paragraph breaks, truncate to 200 KB,
5. failure ⇒ `full_status='failed'` (classification/view falls back to
   `feed_content`).

**Retention** (per refresh): drop items older than `retention_days` unless
`state='starred'` or `saved_note_id>0`; trim each feed to `max_items_per_feed`
oldest-beyond-cap.

## 8. Jobs & refresh orchestration (as built)

- **One job definition**, `rss/refresh_feeds`, schedule `@every 15m`. The
  jobs harness rejects empty schedules, so there are no separate ad-hoc
  definitions; the same definition serves both modes:
  - cron tick (nil payload): iterate all `ct_rss_config` notes, refresh
    readers with due feeds (per-feed interval freshness),
  - action enqueued (payload `{"note_id":N,"force":true}`): force-refresh
    that one reader.
  Iteration skips feeds fetched `< interval_minutes` ago or `enabled=0`.
- Feed fetches parallel (errgroup, max 4); classification strictly sequential
  in ≤25-item batches; whole task wrapped in `context.WithTimeout(10 min)`.
- Result summary: `"3 feeds ok, 1 error, +12 new, 12 classified, 2 rejected"` —
  visible in JobQueue + broadcast via ws job events.
- **Ad-hoc job** `refresh_feeds_now` payload `{"note_id":N,"force":true}` —
  registered in `CronJobs()` with an empty schedule (scheduler skips it,
  `Enqueue` reaches it) — used by the "Refresh now" action.
- **JobEnqueuer** (`SetJobEnqueueFunc`): `refresh` action enqueues the ad-hoc
  job; `save_as_note` enqueues `_system/vss_index` (precedent: task plugin
  enqueues `_system/generate_subtasks`).

## 9. Actions (manifest dispatch)

| ID | Params | Behavior |
|---|---|---|
| `refresh` | `{}` | Enqueue `rss/refresh_feeds_now` `{note_id}`; returns `{run_id}` |
| `save_as_note` | `{item_id}` | Create child note (type `standard`) under the reader note: INSERT `notes` (title, type, parent) + `updates` (markdown: link/author/date, summary, content); enqueue `_system/vss_index` `{note_id}`; set `saved_note_id`, state→`read`; returns `{note_id}` for navigation |
| `set_item_state` | `{item_id, state}` | `read` / `new` / `starred` / `rejected` (manual reject) |
| `get_item` | `{item_id}` | Return one article with its stored content for inline reading — `full_content` preferred (`content_source:"extracted"`), `feed_content` fallback (`"feed"`), empty when neither stored — plus the full metadata the view omits. Content is kept out of the view payload (size) and loaded on demand by the reader modal; `RefreshStrategy: none` (pure read, no ws notify); rejected items stay viewable |
| `reclassify` | `{item_id}` | Re-run classification synchronously (60 s bound) with the currently-scoped prompt |
| `reapply_rules` | `{}` | Re-read body rules, recompute penalty/banned for all items (no LLM); returns counts |

## 10. View + frontend

`BuildView` (single `rss` view):

```json
{
  "settings":  { /* config echo */ },
  "feeds":     [ {"id","name","url","enabled","last_fetch_at","last_status","last_error","new_count"} ],
  "prompt":    { "note_id": 12, "title": "My prompt", "default": false } | { "default": true },
  "rules_warning": "line 4 ignored: 'foo: abc'",
  "items":     [ /* ≤200, not rejected: id, feed_id, feed_name, title, url, author,
                     published_at, summary, category, tags, quality, classify_status,
                     penalty_score, penalty, banned, state, saved_note_id, full_status */ ],
  "rejected":  [ /* ≤50 computed-rejected, same shape */ ],
  "counts":    { "total", "new", "read", "starred", "rejected", "pending_classify" }
}
```

Views resolve scopes live (cheap queries) — hierarchy edits reflect on the
next view load.

Frontend (`frontend/src/note-types/rss/`):

- registry entries:
  - `rss` — "RSS Reader", async `RssNoteType.vue`, `defaultChildType:
    "standard"` (children are saved articles + the optional prompt note)
  - `rss_prompt` — "RSS Prompt", `component: null` (body textarea path, same
    as `standard`), no custom data
- **Viewer**: feed status line + Refresh button ("refreshing" until the
  `rss` job done event on the ws bus — same source JobQueue.vue uses;
  fallback: 10 s view polling while the note is open), filter chips (feed /
  state / category), item cards: quality + penalty badges (tooltip from
  `penalty_json`), summary, actions: open original (external), Save as Note
  (→ navigate), Star / Read / Reject / Reclassify; collapsed "Rejected"
  toggle; "Reapply rules" button after body edits. Prompt status chip: shows
  which prompt is in scope or nudges "add an RSS Prompt note" (add-child
  flow with the type picker).
- **Article viewer**: clicking a card's title (or its "Read" button) opens
  a reading modal that loads the article's stored content on demand through
  the `get_item` action — the extracted full text, else the feed snippet
  (labelled as such), else a link to the original. The modal carries the
  item's meta chips (feed, author, date, classification) and the same
  actions as the card (Star / Read / Save as Note / Reclassify); Esc or
  Close dismisses. Rejected (dimmed) cards are readable the same way.
- **Editor**: feeds add/remove/edit (url, name, enabled), interval,
  threshold, full-content, retention/cap inputs, classify toggle
  (`usePluginAction` / `useNoteTypeDraft` composables; hydrate-on-`note.id`
  guard pattern).
- Reminder chip in-edit mode: rules (ban words) are typed in the note body,
  not the config panel.

## 11. Wiring changes outside the plugin (complete list)

| File | Change |
|---|---|
| `go.mod` / `go.sum` | add `golang.org/x/net` (+ `golang.org/x/text` indirect) |
| `pkg/notetype/builtins/builtins.go` | blank import `pkg/notetype/rss` (registers both types) |
| `internal/llm/ollama.go` | export `ChatMessage` + `ChatClient.Complete([]ChatMessage)` (wraps private `complete`) |
| `internal/llm/tiers.go` | featureSpecs `"rss_classify"` (default tier medium) + `NewRSSClassifierClient()` |
| `config.default.toml` | `[llm.features] rss_classify = { tier = "medium" }` |
| `frontend/src/note-types/registry.js` | 2 entries above |
| `AGENTS.md` | plugin rows in the reference-implementation table + pointer to this doc |
| `docs/RSSReader.md` | this plan |

No `internal/server` changes (plugin cron auto-registration, JobEnqueuer
injection, `/note-types` catalog already generic). Manifests: both Category
"Information"; `rss` SortOrder 500 (config/view/actions, custom editor +
viewer), `rss_prompt` SortOrder 501 (marker — renders through the body
textarea like `standard`).

## 12. Testing

- `plugintest.Run` battery ×2: `rss` config round-trip (Valid = §4 payload,
  Invalid = `{"feeds":[{"url":"ftp://x"}]}`); `rss_prompt` passes with empty
  test data (config-less marker).
- `scope_test.go`: prompt resolution — descendants, siblings, uncle-level
  override, nearest wins, ties (lowest id + warning), none (default
  flagged), reader moved rebinds.
- `feed_test.go`: RSS 2.0 + Atom fixtures, CDATA/escaped HTML in
  content:encoded, date variants, guid fallback, charset handling.
- `extract_test.go`: golden HTML pages (article/main found, readability
  fallback, script stripping, oversize).
- `rules_test.go`: body parsing (prose ignored, malformed warned), ban
  matching, points, occurrence cap, threshold override.
- `classify_test.go`: stub classifier via the `newClassifier` seam; contract
  framing; invalid-JSON retry then error status; default prompt; item
  `prompt_note_id` snapshot.
- `refresh`/`actions`/view tests with `httptest` (SSRF seam flipped on):
  dedup by item_key, 304 skip + etag persistence, retention trim, cascade
  delete (delete reader note → feeds + items gone), `save_as_note` inserts
  `notes`+`updates` + enqueues `_system/vss_index` (recorded via injected
  enqueue stub).
- Frontend: smoke test if component-test conventions exist; otherwise
  manual QA checklist (create reader → add feed → items appear → body rules
  → rejected toggle → save as note).

## 13. Milestones (each ends with a green build + `go test ./...` + `npm run build`)

| # | Milestone | Contents | Size |
|---|---|---|---|
| M1 | Types & skeleton | 2 manifests + registration, DDL, config V/S/L + validation, registry entries (placeholder component), plugintest ×2 passes | S |
| M2 | Fetch & store | feed.go, fetch.go (SSRF/caps/etags), extract.go + x/net dep, cron + ad-hoc job, refresh action, retention, view items + feed viewer/editor UI | **L** |
| M3 | Prompt + classification | scope.go (nearest-wins resolution), llm Complete export + feature binding, prompt loading + default prompt, batch classify + retries, reclassify action, UI badges/summary | M |
| M4 | Rules & rejected | body rules parser + scoring, rejected bucket + thresholds, reapply_rules, UI rejected toggle + penalty tooltips + rules-warning chip | M |
| M5 | Save & polish | save_as_note, item states, prompt nudge, docs/AGENTS/config updates, final QA + FrontEndDist build | S |

## 14. Future extensions (explicitly out of scope for v1)

- MCP tools (`list_rss_items`, `save_rss_item_as_note`) on the task-tool pattern
- One prompt note serving several readers by shared placement (already falls
  out of scope resolution); prompt-history per item (re-classify comparison)
- Same-story dedupe across feeds; OPML import/export; illustrated markdown extraction
- Scheduling windows per feed; auto-save high-quality items as notes by rule
