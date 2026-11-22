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
| Shape | **One note = the whole reader.** Feeds + settings in its config, rules (ban words / penalties / point weights) in its **own body** (user-written lines, parsed by the plugin — the plugin reads bodies like `recipe`/`print` do, never writes to them), articles in plugin tables under the note. |
| Prompt | The only exception: evaluation prompt lives in a separate **`rss_prompt` note** whose body (latest `updates` row) is the system prompt. Resolved from the hierarchy: nearest `rss_prompt` in the note's subtree, then siblings, then up the ancestor chain (nearest wins). None found ⇒ built-in default prompt (the significance-evaluation prompt). |
| LLM evaluation | Output is **structured JSON**: `evaluationReflectionParagraph`, significance (0.0–1.0), negativity (0.0/0.5/1.0), personalRelevance (0.0–1.0), four humans-affected counts (millions), tags; we add contract framing + validation + one retry. |
| Points | Each evaluated article gets **points 0–10**: `10 × (wSig·significance + wRel·relevance + wPos·(1−negativity) + wHum·humansTerm) − rule penalties`. Weights and the `min_points` reject floor are body rules; the timeline's default sort is **unread first, ranked by points** (read items sink). |
| Ban words / penalties | Matching article → rejected bucket, hidden behind a "show rejected" toggle; never silently destroyed. Penalty points accumulate and subtract from points; ≥ threshold ⇒ rejected (threshold 0 = disabled). Evaluated items below `min_points` (default 2.0; 0 = off) ⇒ rejected — only applies to items whose classification actually carried evaluation data. |
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
├── 🧠 Prompt (type: rss_prompt)  ← body = evaluation prompt (the exception)
└── 📄 saved article (type: standard, created by "Save as Note")
```

- `rss` — the reader. `HasConfig` (feeds + numeric settings), `HasView`
  (merged timeline), `HasActions`. Note title = reader name.
- `rss_prompt` — marker type. No config/view/actions; renders through the
  standard body textarea. Body = evaluation prompt.

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
            insert items (state=new, classify + summary pending)
                     ▼
            evaluate batch (≤25/batch, drains queue, prompt from scope) ─▶ llm.NewRSSClassifierClient
                     ▼
            summarize batch (≤25/batch, drains its own queue, built-in prompt) ─▶ llm.NewRSSSummarizerClient
                     ├─▶ per-item plugin.progress ws events, {stage: evaluate|summarize} (ProgressReporter) ─▶ reader progress bar
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
    category        TEXT    NOT NULL DEFAULT '',  -- legacy (pre-evaluation prompts only)
    summary         TEXT    NOT NULL DEFAULT '',  -- legacy column; DISCARDED by the
                                                   -- pipeline (summary_json is the only summary)
    tags_json       TEXT    NOT NULL DEFAULT '[]',
    quality         INTEGER NOT NULL DEFAULT 0,   -- legacy (pre-evaluation prompts only)
    significance    REAL    NOT NULL DEFAULT 0,   -- LLM evaluation 0..1
    negativity      REAL    NOT NULL DEFAULT 0,   -- 0 positive .. 1 negative
    personal_relevance REAL NOT NULL DEFAULT 0,  -- 0..1
    affected_now_direct   REAL NOT NULL DEFAULT 0, -- millions
    affected_now_indirect REAL NOT NULL DEFAULT 0,
    affected_future_direct REAL NOT NULL DEFAULT 0,
    affected_future_indirect REAL NOT NULL DEFAULT 0,
    reflection      TEXT    NOT NULL DEFAULT '',  -- evaluationReflectionParagraph
    points          REAL    NOT NULL DEFAULT 0,   -- computed 0..10
    evaluated       INTEGER NOT NULL DEFAULT 0,   -- classification carried evaluation data
    summary_json    TEXT    NOT NULL DEFAULT '',   -- telegraph F/O/Q digest {"what_changed":[…],"why":[…],"implications":[…]}
    summary_status  TEXT    NOT NULL DEFAULT '',   -- ''|pending|done|error|disabled
    summary_error   TEXT    NOT NULL DEFAULT '',
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

The ten evaluation columns and the three summarizer columns are additive;
databases created before them are upgraded on plugin startup by
`ensureItemColumns` (PRAGMA table_info + `ALTER TABLE ADD COLUMN` per missing
column). Rows that predate the summarizer get `summary_status ''` — a plain
refresh never auto-summarizes them (no surprise bulk LLM cost after the
upgrade); the explicit **Summarize missing** action queues exactly those.

**Rejected** is computed: `banned=1 OR penalty_score >= threshold (0=off) OR
state='rejected' OR (evaluated=1 AND min_points > 0 AND points < min_points)`.

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

## 5. Prompt convention (LLM evaluation)

The body of the resolved `rss_prompt` note is the evaluation instruction,
sent as the system message. The classifier appends a fixed binding output
contract (analysis text may precede the JSON — the default prompt opens
with a reasoning checklist — but the reply must END with exactly one JSON
object in this shape):

```
{"evaluationReflectionParagraph": "<string; minimum one sentence explanation>",
 "potentialFutureDirectlyAffectedHumansInMillions": <float; 0.0 if unknown>,
 "potentialFutureIndirectlyAffectedHumansInMillions": <float; 0.0 if unknown>,
 "alreadyDirectlyAffectedHumansInMillions": <float; 0.0 if unknown>,
 "alreadyIndirectlyAffectedHumansInMillions": <float; 0.0 if unknown>,
 "significance": <float; between 0.0 and 1.0>,
 "negativity": <float; 0.0 = positive, 0.5 = neutral, 1.0 = negative>,
 "personalRelevance": <float; between 0.0 and 1.0, per personal context>,
 "tags": [up to 10 string tags]}
```

- **Built-in default prompt** = the significance-evaluation prompt: a neutral
  system assessing an article's impact on humanity on a fixed 0.0–1.0 scale
  (per-level definitions and examples embedded), event negativity
  (0.0 positive / 0.5 neutral / 1.0 negative), personal relevance for
  Germany/EU + cloud/distributed-systems interests, four humans-affected
  estimates (already/potential-future × direct/indirect, in millions), a
  reflection paragraph, and up to 10 tags. Replaces the old
  category/summary/quality contract (the request-shape keys survive for
  custom prompts: `category`, `summary`, `quality` are still parsed when a
  prompt emits them; missing legacy fields default to empty).
- No prompt note in scope ⇒ the built-in default applies — works out of the
  box; view reports `prompt: "default"` so the UI can nudge.
- The prompt's optional `"error"` field (fields that could not be identified)
  is stored in `classify_error` while the status stays `done`.
- **The old summary is discarded from the pipeline**: the evaluation never
  writes the `summary` column (a re-evaluation even clears a stale legacy
  value), and no surface renders it anymore — the reflection paragraph
  stays in its own `reflection` column (API-visible via `get_item`), and
  the **only summary is the telegraph digest** (`summary_json`, §5a).

## 5a. Summarizer prompt (telegraph F/O/Q digest)

A second, independent LLM pass condenses every article into a structured
summary — the digest rendered on each card and in the article modal. Unlike
the evaluation prompt it is **built-in** (`defaultSummarizerPrompt` in
`pkg/notetype/rss/summarize.go`, not nota-resolvable yet); editing it is a
code change + restart. A fixed binding tail (`summarizeContract`) is
appended so the reply is machine-readable.

- **What it asks for** (verbatim design): ≤200 words, telegraph-style,
  each line labeled `F:` (fact), `O:` opinion + source, `Q:` quote +
  source, grouped under three questions — *What has changed in reality?* /
  *Why has reality changed?* / *What are the new implications?* — all
  translated to English except German text, no source links.
- **Output contract**: `{"content": {"What has changed in reality?": [...],
  "Why has reality changed?": [...], "What are the new implications?": [...]}}`.
  Parsed tolerantly: fences/prose around the object, missing `content`
  wrapper, rephrased question keys (fuzzy bucketing) all recover; unknown
  keys are dropped; lines are trimmed and clipped (≤300 runes, ≤15 per
  section). A reply carrying no lines at all is an error (one corrective
  retry, then `summary_status='error'`).
- **Storage**: the three sections land in `ct_rss_items.summary_json` under
  canonical keys `what_changed` / `why` / `implications` with a
  `summary_status` machine (`''` legacy / `pending` / `done` / `error` /
  `disabled`) — separate from the evaluation's `classify_status`.
- **Client**: `llm.NewRSSSummarizerClient()` bound to feature
  `rss_summarize` (usage `rss_summarize` in the llm_inferencer catalog;
  default tier `small` — cheaper than the evaluation's `medium`, overridable
  via `[llm.features.rss_summarize]` or an inferencer note usage mapping).
- **Drain**: `summarizePendingItems` runs right after the evaluation drain
  inside the same refresh job, own ≤25-item batches, 6-minute budget, gated
  on `auto_classify`. Items without stored content complete instantly with
  an empty done summary instead of burning an LLM call (and blocking the
  queue). Per-item progress streams with `stage:"summarize"`.

- User message = article title + feed name + URL + content (full_content
  preferred, feed_content fallback), capped at ~10k chars.
- Client: `llm.NewRSSClassifierClient()` bound to feature `rss_classify`
  (usage `rss_classify` in the llm_inferencer catalog; default tier `medium`,
  overridable via `[llm.features.rss_classify]` or an inferencer note usage
  mapping).
- Parse: lenient JSON extraction (prose/fences tolerated); strict clipping +
  clamping into the schema; one retry with the parse error appended; failure
  ⇒ `classify_status='error'` (item stays listed; next refresh re-picks up
  pending items).
- Backend lease (`llm.BeginBackendUse`) held across the run. Pending items
  drain in ≤25-item batches until the queue is empty or an 8-minute budget
  is spent — a `reclassify_all` reset is cleared within one refresh job.
- Editing the prompt note body takes effect on next refresh /
  re-evaluation.

## 6. Rules convention (ban words / penalties / point weights — in the rss note's body)

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
threshold: 8                     ← overrides config reject_threshold (0 = off)
min_points: 2                   ← points floor for evaluated items (0 = off)
weights: significance=0.5, relevance=0.25, positivity=0.15, humans=0.10
```

- `ban: <phrase>` — hard reject.
- `<phrase>: <points>` — points per occurrence (negative allowed);
  per-phrase occurrence cap 10 (bounds pathological repetition). Penalty
  points subtract from the evaluation points.
- `threshold: <n>` — per-note override of `reject_threshold`.
- `min_points: <n>` — rejects LLM-evaluated articles whose computed points
  fall below the floor (default 2.0, 0 = disabled). Items classified by
  legacy prompts without evaluation data never fall below the floor.
- `weights: <key>=<value>…` — overrides the points-formula component
  weights (aliases sig/rel/pos/hum; unspecified components keep defaults;
  values clamped to 0–1; unknown keys/malformed values warn).
- Rule-*shaped* but malformed lines (e.g. `foo: abc`, empty `ban:`) collect
  into `rules_warning` shown in the view; ordinary prose is never warned.

Points formula (`score.go`):

```
humansTerm = log10(1 + Σ affected millions) / log10(8001)      # 0..1, world pop ≈ 1.0
points     = clamp[0,10]( 10 × (wSig·significance + wRel·personalRelevance
                             + wPos·(1−negativity) + wHum·humansTerm)
                         − penalty_score )
```

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
  in ≤25-item batches until the queue drains (8-min classify budget,
  bounded by the task's 10-minute timeout); then the summarize drain runs
  the same way (≤25-item batches, 6-min budget, `summary_status='pending'`
  rows, no-content rows complete instantly as done-empty).
- **Live progress**: both drains stream
  `{note_id, stage, done, total, finished}` (`stage` = `evaluate` |
  `summarize`) through the injected `notetype.ProgressReporter` emitter
  after every article; the server broadcasts it as a `plugin.progress` ws
  message, and the reader renders a stage-aware progress bar
  ("⚡ Evaluating x/y" / "✎ Summarizing x/y"; noise-free: nothing emits
  when nothing is pending).
- Result summary: `"3 feeds ok, 1 error, +12 new, 12 classified, 12
  summarized, 2 rejected"` — visible in JobQueue + broadcast via ws job
  events.
- **Ad-hoc job** `refresh_feeds_now` payload `{"note_id":N,"force":true}` —
  registered in `CronJobs()` with an empty schedule (scheduler skips it,
  `Enqueue` reaches it) — used by the "Refresh now" action.
- **JobEnqueuer** (`SetJobEnqueueFunc`): `refresh` action enqueues the ad-hoc
  job; `save_as_note` enqueues `_system/vss_index` (precedent: task plugin
  enqueues `_system/generate_subtasks`).

## 9. Actions (manifest dispatch)

| ID | Params | Behavior |
|---|---|---|
| `refresh` | `{}` | Enqueue `rss/refresh_feeds` `{note_id, force}`; returns `{run_id}` |
| `save_as_note` | `{item_id}` | Create child note (type `standard`) under the reader note: INSERT `notes` (title, type, parent) + `updates` (markdown: link/author/date, **Summary section** from `summary_json` only — the discarded old summary never renders, evaluation line, content); enqueue `_system/vss_index` `{note_id}`; set `saved_note_id`, state→`read`; returns `{note_id}` for navigation |
| `set_item_state` | `{item_id, state}` | `read` / `new` / `starred` / `rejected` (manual reject) |
| `get_item` | `{item_id}` | Return one article with its stored content for inline reading — `full_content` preferred (`content_source:"extracted"`), `feed_content` fallback (`"feed"`), empty when neither stored — plus the full metadata the view omits, including the evaluation (significance, negativity, relevance, humans ×4, reflection, points, evaluated) and the summary fields (`structured_summary` parsed, `summary_status`, `summary_error`). Content is kept out of the view payload (size) and loaded on demand by the reader modal; `RefreshStrategy: none` (pure read, no ws notify); rejected items stay viewable |
| `reclassify` | `{item_id}` | Re-run the LLM evaluation synchronously (60 s bound) with the currently-scoped prompt; recomputes points; returns `{points, significance, negativity, personal_relevance, reflection, tags}` (no summary — the digest is the summarizer's) |
| `reclassify_all` | `{}` | Queue **every** article for a fresh evaluation (`classify_status` → `pending`, errors cleared — existing scores are overwritten by the drain) and schedule a force refresh whose classify stage drains the queue; returns `{reset, run_id}`. Exposed in the reader's **Settings → LLM evaluation** behind a warning confirmation (one LLM call per article) |
| `evaluate_missing` | `{}` | Queue **only articles with no evaluation** (`evaluated = 0`: legacy keyword-classified rows, errored runs, still-pending ones; already-evaluated scores are kept) and schedule the same drain refresh; returns `{reset, run_id}`. Settings button carries the live unevaluated count |
| `summarize_item` | `{item_id}` | Run the built-in summarizer on one article synchronously and store the F/O/Q digest; returns `{item_id, structured_summary}`. Article-modal "Summarize" button (also refreshes an existing digest); errors (no stored content, LLM failure) are recorded on the item |
| `summarize_missing` | `{}` | Queue **only articles without a stored summary** (`summary_status != 'done'`: `''` rows predating the summarizer, errored runs, pending ones; existing summaries are kept) and schedule the drain refresh; returns `{reset, run_id}`. Settings button carries the live unsummarized count, behind a confirmation (one LLM call per article) |
| `reapply_rules` | `{}` | Re-read body rules, recompute penalty/banned AND points for all items (no LLM); returns counts incl. `min_points` |

## 10. View + frontend

`BuildView` (single `rss` view):

```json
{
  "settings":  { /* config echo */ },
  "feeds":     [ {"id","name","url","enabled","last_fetch_at","last_status","last_error","new_count"} ],
  "prompt":    { "note_id": 12, "title": "My prompt", "default": false } | { "default": true },
  "rules_warning": "line 4 ignored: 'foo: abc'",
  "items":     [ /* ≤200, not rejected: id, feed_id, feed_name, title, url, author, published_at,
                     structured_summary, summary_status, category, tags, quality, significance,
                     negativity, personal_relevance,
                     affected_now_direct, affected_now_indirect, affected_future_direct,
                     affected_future_indirect, points, evaluated, classify_status,
                     penalty_score, penalty, banned, state, saved_note_id, full_status */ ],
  "rejected":  [ /* ≤50 computed-rejected, same shape */ ],
  "counts":    { "total", "new", "read", "starred", "rejected", "pending_classify",
                 "evaluated", "unevaluated", "pending_summarize", "summarized", "unsummarized" }
}
```

Views resolve scopes live (cheap queries) — hierarchy edits reflect on the
next view load. The view payload carries the evaluation columns (small
floats) **and the parsed F/O/Q digest** (`structured_summary`; the card
renders it directly so articles are skimmable from the list). The old
`summary` column ships nowhere: it is not in the view payload and the UI
never renders it. The reflection paragraph rides only in `get_item` (the
evaluation's own rationale, not a summary).

Frontend (`frontend/src/note-types/rss/`):

- registry entries:
  - `rss` — "RSS Reader", async `RssNoteType.vue`, `defaultChildType:
    "standard"` (children are saved articles + the optional prompt note)
  - `rss_prompt` — "RSS Prompt", `component: null` (body textarea path, same
    as `standard`), no custom data
- **Viewer**: feed status line + Refresh button. Working state and view
  renewal are **driven by the ws `jobs.changed` events** (the rss plugin's
  refresh runs are broadcast on every lifecycle change — planned/running/
  done/errored — same source JobQueue.vue listens to): the view re-fetches
  when the job finishes, so the spinner and the "N evaluating" chip settle
  with the real job, never before, never indefinitely. While the drain
  runs, the plugin's per-item `plugin.progress` ws stream drives a live
  "⚡ Evaluating x/y" progress bar (framed + colored against the dark
  tokens). A bounded fallback poll (5 s, ≤12 min, settles as soon as the
  pending queue drains) covers ws outages — it also folds the bar away.
  The toolbar keeps **Refresh feeds** / **Reapply rules** and surfaces a
  hint line when articles have no evaluation or summary yet (pointing at
  Settings).
  Sort picker (default **unread first ·
  ★ points**; also best-points / newest / oldest), filter
  chips (feed / state), item cards: ★ points badge (green/amber/red tint by
  negativity; tooltip = significance/negativity/relevance/humans/penalty
  breakdown), legacy category + Q badges, penalty badges (tooltip from
  `penalty_json`), the **telegraph digest** (F/O/Q lines under
  "What changed / Why / New implications" headers — the only summary the
  card shows; "⏳ summarizing…" while queued, nothing before the first
  digest), actions: open
  original (external), Save as Note (→ navigate), Star / Read / Reject /
  Re-evaluate; collapsed "Rejected" toggle; item content stays out of the
  view — the modal loads it through `get_item`. Prompt status chip: shows
  which prompt is in scope or nudges "add an RSS Prompt note" (add-child
  flow with the type picker).
- **Article viewer**: clicking a card's title (or its "Read" button) opens
  a reading modal that loads the article's stored content on demand through
  the `get_item` action — the extracted full text, else the feed snippet
  (labelled as such), else a link to the original. The modal carries the
  item's meta chips (feed, author, date), the full evaluation (★ points,
  significance, negativity, relevance chips, humans-affected line,
  evaluation notes from the model's `error` field), the **F/O/Q digest**
  rendered under the three question headers (the only summary shown — the
  discarded old summary column and the reflection paragraph never render
  anymore), digest-gating hints ("⏳ Summary queued…" / "No summary yet" /
  ⚠ failed, retry via the button), and a **Summarize** button that runs the
  per-item summarizer synchronously and refreshes the open modal; plus the
  same actions as the card (Star / Read / Save as Note / Re-evaluate); Esc
  or Close dismisses. Rejected (dimmed) cards are readable the same way.
- **Editor**: feeds add/remove/edit (url, name, enabled), interval,
  threshold, full-content, retention/cap inputs, classify toggle
  (`usePluginAction` / `useNoteTypeDraft` composables; hydrate-on-`note.id`
  guard pattern).
- Reminder chip in-edit mode: rules (ban words) are typed in the note body,
  not the config panel.
- **Settings → LLM evaluation & summaries** section (feedback lines + ⚡
  progress bar render in edit mode too): **Evaluate missing (N)** queues
  only the articles without an evaluation (count from
  `counts.unevaluated`); **Summarize missing (N)** queues only the articles
  without a stored summary (count from `counts.unsummarized`), behind a
  confirmation — one LLM call per article; **Re-evaluate ALL…** is guarded
  by a ⚠️ warning confirmation — it overwrites every existing score and
  costs one LLM call per article. All three run as the background drain
  job, and the two "missing" actions never touch each other's results.
  The progress bar is stage-aware (⚡ Evaluating / ✎ Summarizing), and the
  article modal's **Summarize** button runs the per-item summarizer
  synchronously.

## 11. Wiring changes outside the plugin (complete list)

| File | Change |
|---|---|
| `go.mod` / `go.sum` | add `golang.org/x/net` (+ `golang.org/x/text` indirect) |
| `pkg/notetype/builtins/builtins.go` | blank import `pkg/notetype/rss` (registers both types) |
| `internal/llm/ollama.go` | export `ChatMessage` + `ChatClient.Complete([]ChatMessage)` (wraps private `complete`) |
| `internal/llm/tiers.go` | featureSpecs `"rss_classify"` (default tier medium) + `"rss_summarize"` (small) + `NewRSSClassifierClient()` / `NewRSSSummarizerClient()`; usage catalog entries for both |
| `config.default.toml` | `[llm.features] rss_classify = { tier = "medium" }`, `rss_summarize = { tier = "small" }` |
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
- `rules_test.go` / `score_test.go`: body parsing (prose ignored,
  malformed warned), ban matching, penalty points, occurrence cap, threshold
  override; `min_points`/`weights` rules, the points formula
  (significance/relevance/positivity/humans/penalty; clamps and floor).
- `classify_test.go`: stub classifier via the `newClassifier` seam; contract
  framing; evaluation parse (clamps/clips, old-summary discard — the
  reflection never leaks into a summary, legacy-shape tolerance),
  invalid-JSON retry then error status, batch
  drain, weights+penalty flowing into the stored points; item
  `prompt_note_id` snapshot.
- `classify_test.go` also covers the progress stream: per-item events
  (`{done,total,finished}`), silence on empty drains and without the
  injected emitter, errored classifications still advance the bar; the
  server side (`internal/server/live_ws_test.go`) tests the ws broadcast
  and wire round-trip. The summarize stage adds `stage:"summarize"`
  labeling (asserted in `summarize_test.go`); the other progress semantics
  are shared through `reportEvaluationProgress`.
- `summarize_test.go`: stub summarizer via the `newSummarizer` seam —
  parse table (contract shape, fences/prose, missing wrapper, fuzzy keys,
  clamps, drop unknown keys, no-lines error), rapid PBT (reply ⟷ stored
  roundtrip + key-bucketing stability), drain battery (done/error status,
  contentless items complete without an LLM call, legacy `''` rows never
  auto-drained, `auto_classify` gate, one corrective retry), staged
  progress, `summarize_item` / `summarize_missing` actions (queue
  targeting + existing-summary preservation + drain job enqueue), view
  counts (`summarized`/`unsummarized`/`pending_summarize`) + ItemView
  digest, `get_item` summary fields, `save_as_note` digest markdown, and
  the pre-summarizer table upgrade (`migration_test.go`).
- `view_eval_test.go`: computed rejection — evaluated items below
  `min_points` rejected (default floor), `min_points: 0` disables, floor
  raised via body rule; evaluation columns round-trip into the view.
- `refresh`/`actions` tests with `httptest` (SSRF seam flipped on):
  dedup by item_key, 304 skip + etag persistence, retention trim, cascade
  delete (delete reader note → feeds + items gone), `save_as_note` inserts
  `notes`+`updates` + enqueues `_system/vss_index` (recorded via injected
  enqueue stub), `reclassify_all` resets statuses + schedules the drain
  refresh, `reapply_rules` recomputes points, `get_item` round-trips the
  evaluation.
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
