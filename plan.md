# Plan: Robust Job System

## Goal

Replace the current lightweight goroutine-based cron scheduler with a robust, observable job system that:

1. Persists job state to SQLite so it survives restarts.
2. Records every job run with status (planned / running / done / errored).
3. Exposes job history and queue via HTTP endpoints.
4. Shows the queue in the Vue frontend so users can see what's running and what failed.

## Robustness Principles

These five principles come from industry-standard references on data system architecture and are baked into the design from Phase 1 onward.

1. **Atomic dequeue** (Kleppmann, 2017, *Designing Data-Intensive Applications*, O'Reilly Media) — Workers acquire jobs via a single atomic `UPDATE ... RETURNING` query so no two workers ever grab the same job.
2. **Zombie recovery** (Microsoft Azure Architecture Center, *Asynchronous Request-Reply Pattern*) — On startup, any job stuck in `running` state from a previous crash is reset to `planned` so it re-executes rather than being lost forever.
3. **Data retention** (SQLite Documentation, *Query Planning*) — A built-in janitor job runs daily and deletes `job_runs` rows older than 30 days, preventing unbounded table growth that degrades query performance.
4. **Job payloads** — `job_runs` has a `payload` column so jobs can receive input parameters (e.g. `{"note_id": 42}`). This future-proofs the system for user-triggered background work beyond cron.
5. **SQLite WAL + busy timeout** (SQLite Documentation, *Write-Ahead Logging*) — The database already opens with `_journal_mode=WAL`. The job system additionally sets `_busy_timeout=5000` so concurrent writes from multiple workers do not fail with `database is locked`.

## Architecture Overview

```
                              ┌─────────────────────────┐
                              │       job system         │
                              │                          │
┌──────────┐   ┌──────────┐   │  ┌──────────┐           │
│ Scheduler │──▶│  Queue   │──┼──▶│  Worker  │──▶ job   │
│ (ticks)  │   │ (SQLite) │   │  │  (1..N)  │    Task   │
└──────────┘   └──────────┘   │  └──────────┘           │
                              │       │                  │
                              │  ┌────▼─────────┐       │
                              │  │ job_runs table│       │
                              │  │  (persistent) │       │
                              │  └──────────────┘       │
                              │       │                  │
                              │  ┌────▼─────────┐       │
                              │  │   Janitor    │       │
                              │  │  (retention) │       │
                              │  └──────────────┘       │
                              └─────────────────────────┘
         │                                              │
         ▼                                              ▼
   Plugin.CronJobs()                             HTTP API
                                          GET  /jobs
                                          GET  /jobs/pending
                                          POST /jobs/:id/retry
                                          POST /jobs/:id/cancel
```

## Phase 1: Database Schema

Add two new tables to `internal/db/db.go` migration:

```sql
-- job_definitions: what jobs exist (registered once per plugin)
CREATE TABLE IF NOT EXISTS job_definitions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin_id   TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    schedule    TEXT    NOT NULL,    -- cron expression or "@every 1h"
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE(plugin_id, name)
);

-- job_runs: every individual execution
CREATE TABLE IF NOT EXISTS job_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES job_definitions(id) ON DELETE CASCADE,
    status      TEXT    NOT NULL DEFAULT 'planned',
                -- planned | running | done | errored | cancelled
    payload     TEXT,              -- JSON input parameters (future-proofing)
    started_at  DATETIME,
    finished_at DATETIME,
    error       TEXT,
    result      TEXT,              -- human-readable result summary
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_job_runs_status ON job_runs(status);
CREATE INDEX IF NOT EXISTS idx_job_runs_job_id ON job_runs(job_id);
CREATE INDEX IF NOT EXISTS idx_job_runs_created ON job_runs(created_at);
```

`job_definitions` entries are upserted each time the server starts (by `plugin_id + name`). This allows plugin authors to change schedules without manual DB edits.

### SQLite Optimization

The database connection strings must include `_busy_timeout=5000` alongside the existing `_journal_mode=WAL`. WAL mode allows concurrent readers and writers. The busy timeout prevents instant `database is locked` errors when two workers try to write simultaneously — SQLite will retry for up to 5 seconds instead.

```go
// db.Open already uses:  path+"?_journal_mode=WAL&_foreign_keys=on"
// Update to:              path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
```

## Phase 2: Scheduler + Worker (Go)

### New package: `internal/jobs/`

```
internal/jobs/
    jobs.go       — Manager, Worker, Janitor, public API
    jobs_test.go  — tests
```

### Manager

```go
type Manager struct {
    db       *sql.DB
    workers  int        // default: 2
    stop     chan struct{}
    done     chan struct{}
}
```

**Startup flow**:

1. Server calls `jobManager := jobs.NewManager(db, 2)`.
2. Manager upserts `job_definitions` from `notetype.Registry[plugin.ID()].CronJobs()`.
3. **Zombie recovery**: Manager runs a one-time cleanup query that finds any jobs stuck in `running` state from a previous crash and resets them:
   ```sql
   UPDATE job_runs
   SET status = 'planned', started_at = NULL, error = 'Previous server instance crashed'
   WHERE status = 'running';
   ```
   This ensures no job is abandoned forever. Jobs that have been `running` for longer than 60 minutes could alternatively be handled by a periodic watchdog — but the startup cleanup is sufficient for a single-instance SQLite deployment.
4. Scheduler goroutines launch, each watching a `job_definition` and inserting a `planned` `job_runs` row at the right time.
5. Worker goroutines (2 by default) loop, polling for planned jobs.
6. A built-in janitor job (see Phase 2b) is registered to handle retention.

### Atomic Dequeue (Race Condition Prevention)

Workers do NOT use an in-memory Go channel. Instead, they poll the database atomically:

```sql
UPDATE job_runs
SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = (
    SELECT id FROM job_runs
    WHERE status = 'planned'
    ORDER BY created_at ASC
    LIMIT 1
)
RETURNING id, job_id, payload;
```

If this returns an ID, the worker safely owns that job. If it returns nothing, the queue is empty and the worker sleeps (e.g. 1 second) before polling again.

This approach (referenced from Kleppmann, 2017) guarantees that even with multiple workers, no job is ever dequeued twice. SQLite serializes writes, so the inner `SELECT` and outer `UPDATE` are atomic.

### Worker Execution

Once a worker owns a job, it:

1. Looks up the `job_definition` to get the `Task` function (stored in an in-memory map keyed by `job_id`).
2. Calls `task(db, payload)` in a goroutine with `recover()` to catch panics.
3. On completion, updates the `job_runs` row:
   ```sql
   UPDATE job_runs
   SET status = 'done', finished_at = strftime(...), result = ?
   WHERE id = ?
   ```
4. On error, updates with `status = 'errored', error = ?`.

### Job Payloads

The `payload` column on `job_runs` future-proofs the system for user-triggered jobs. Even though cron jobs don't need parameters, this enables enqueuing ad-hoc jobs like:

```go
manager.Enqueue("yourtype", "export_pdf", json.RawMessage(`{"note_id":42}`))
```

The `CronJob.Task` signature changes to accept payload:

```go
type CronJob struct {
    Name     string
    Schedule string
    Task     func(db *sql.DB, payload []byte) (string, error)
}
```

For cron-triggered jobs, `payload` is always `nil`. For user-triggered jobs, it carries the input parameters.

### Phase 2b: Retention Janitor

A built-in system job (not registered by any plugin) runs daily:

```go
func janitorTask(db *sql.DB, _ []byte) (string, error) {
    res, err := db.Exec(
        `DELETE FROM job_runs WHERE created_at < datetime('now', '-30 days')`,
    )
    if err != nil {
        return "", err
    }
    n, _ := res.RowsAffected()
    return fmt.Sprintf("Cleaned up %d old job runs", n), nil
}
```

This is registered internally by the Manager (not through the plugin system) and runs on a `@daily` schedule.

## Phase 3: HTTP API

Add to `internal/server/server.go`:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/jobs` | List recent job runs (last 50) with pending count |
| `POST` | `/jobs/:id/retry` | Re-queue an errored/cancelled run |
| `POST` | `/jobs/:id/cancel` | Cancel a planned (not yet running) job |

No separate `/jobs/pending` endpoint needed — `GET /jobs` includes `pending_count` in the response.

`GET /jobs` response:

```json
{
  "runs": [
    {
      "id": 42,
      "plugin_id": "recipe_overview",
      "job_name": "weekly_grocery_list",
      "status": "done",
      "started_at": "2026-05-12T08:00:00Z",
      "finished_at": "2026-05-12T08:00:02Z",
      "error": null,
      "result": "Generated grocery list with 15 items"
    }
  ],
  "pending_count": 0
}
```

Register the job manager on the server struct:

```go
type Server struct {
    db           *db.DB
    addr         string
    llm          llm.Embedder
    webauthn     *webauthn.WebAuthn
    sessionStore *webAuthnSessionStore
    jobManager   *jobs.Manager   // NEW
}
```

## Phase 4: Frontend UI

### New component: `frontend/src/components/JobQueue.vue`

A collapsible panel in the sidebar footer showing:

- A badge with the count of pending/running jobs (e.g. "⚙ 2 jobs")
- Clicking opens a dropdown listing recent job runs:
  - Status icon: ⏳ planned, ⟳ running, ✓ done, ✗ errored, ⊘ cancelled
  - Job name and plugin
  - Timestamp
  - Error message (if errored, shown in red)
  - Retry button (if errored or cancelled)

### Integration

Add `<JobQueue :token="token" />` to the sidebar in `NotesView.vue`, below the search box and above the sidebar footer. The component polls `GET /jobs` every 10 seconds when expanded.

### API additions (`frontend/src/api.js`)

```js
export async function fetchJobs(token) { ... }         // GET /jobs
export async function retryJob(token, runId) { ... }    // POST /jobs/:id/retry
export async function cancelJob(token, runId) { ... }   // POST /jobs/:id/cancel
```

## Phase 5: Migration from Old Scheduler

1. Remove `startCronJobs()`, `scheduleCron()`, and `parseSimpleSchedule()` from `internal/server/server.go`.
2. In `Start()`, replace `s.startCronJobs()` with `s.jobManager.Start()`.
3. Graceful shutdown: `s.jobManager.Stop()` cancels all scheduler goroutines, waits for running workers to finish (30s timeout), and closes the done channel.
4. Update the DB connection string to include `_busy_timeout=5000`.
5. Update all plugin `CronJobs()` implementations to include the new `Name` field and `(string, error)` return.

### Plugin migration example

Before:
```go
func (p *SomePlugin) CronJobs() []notetype.CronJob {
    return nil
}
```

After (adding a hypothetical weekly task):
```go
func (p *SomePlugin) CronJobs() []notetype.CronJob {
    return []notetype.CronJob{{
        Name:     "weekly_cleanup",
        Schedule: "@weekly",
        Task: func(db *sql.DB, _ []byte) (string, error) {
            return "Cleaned up 42 records", nil
        },
    }}
}
```

### Interface summary after migration

```go
type CronJob struct {
    Name     string
    Schedule string
    Task     func(db *sql.DB, payload []byte) (result string, err error)
}
```

## Files Changed

| File | Change |
|---|---|
| `internal/jobs/jobs.go` | **New** — Manager, Worker, Janitor, atomic dequeue, zombie recovery |
| `internal/jobs/jobs_test.go` | **New** — tests |
| `internal/db/db.go` | Add `job_definitions` + `job_runs` migration; add `_busy_timeout=5000` |
| `internal/server/server.go` | Add `jobManager` field, `/jobs` routes, remove old `startCronJobs` |
| `pkg/notetype/notetype.go` | `CronJob` gains `Name`; `Task` returns `(string, error)` + takes `[]byte` payload |
| `pkg/notetype/recipe/recipe.go` | Update `CronJobs()` signature |
| `pkg/notetype/recipeoverview/recipeoverview.go` | Update `CronJobs()` signature |
| `pkg/notetype/example/example.go` | Update `CronJobs()` signature |
| `pkg/notetype/plugintest/plugintest.go` | Update harness for new `CronJob` shape |
| `frontend/src/api.js` | Add `fetchJobs`, `retryJob`, `cancelJob` |
| `frontend/src/components/JobQueue.vue` | **New** — job queue panel |
| `frontend/src/views/NotesView.vue` | Add `<JobQueue>` to sidebar |

## Testing Strategy

- **`internal/jobs/jobs_test.go`**: Unit tests using in-memory SQLite.
  - *Atomic dequeue*: Enqueue 3 planned jobs, launch 5 concurrent workers, assert exactly 3 unique IDs processed (no duplicates).
  - *Zombie recovery*: Manually insert a `running` job row, call Manager.Start(), assert it was reset to `planned`.
  - *Error handling*: Schedule a job whose Task returns an error, assert `status = errored` and `error` is non-empty.
  - *Retry*: Retry an errored run, assert a new `planned` row exists pointing to the same `job_id`.
  - *Cancel*: Cancel a planned run, assert `status = cancelled`.
  - *Graceful shutdown*: Stop the manager, assert no new jobs start, assert `done` channel closes, verify running jobs complete before timeout.
  - *Janitor*: Insert 50 run rows with `created_at` 31 days ago, run the janitor, assert all deleted. Insert 50 with `created_at` 1 day ago, assert none deleted.
  - *Payload round-trip*: Enqueue a job with payload, assert worker receives it, assert result is stored.
- **Plugin test harness update**: `CronJobs_NoPanic` sub-test updated to verify the `Name` field, the `(string, error)` return signature, and the `[]byte` payload parameter.
