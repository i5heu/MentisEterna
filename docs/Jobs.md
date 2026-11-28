# Job System (v2)

MentisEterna's background work — embeddings, OCR/STT, LLM titles/tags/subtasks,
RSS refreshes, encrypted backups, media repair sweeps, plugin cron jobs — runs
on the **job system** in `internal/jobs`. Every run is a `job_runs` row: queued
(`planned`), claimed (`running`), and finalized exactly once (`done`,
`errored`, `cancelled`), with live progress and a durable per-job statistics
ledger.

Design goals: **reliable** (no wedged/duplicated/dropped work), **fast and
multithreaded** (priority lanes, per-job parallelism, instant wakeup),
**observable** (per-run progress + per-job stats + ws events).

## Package layout

```
internal/jobs/
├── jobs.go        public types (CronJob, RunContext, Run, RunEvent), the
│                  Manager, lifecycle (Start/Stop), the observer bus
├── definitions.go definition registration, builtin janitor, zombie recovery,
│                  definition lookup cache
├── queue.go       Enqueue / RetryRun / CancelRun / ListRuns (the queue API)
├── scheduler.go   per-definition cron tickers (ticks never pile up)
├── worker.go      worker lanes, the atomic dequeue, task execution
├── finalize.go    guarded terminal transitions + stats ledger bumps,
│                  automatic retries with backoff
├── progress.go    the throttled per-run progress reporter
├── watchdog.go    timeouts, lost-claim cleanup, delayed-retry wakeups
├── stats.go       the GET /jobs/stats snapshot
└── util.go        time/pointer/log-format helpers
```

## Architecture

```
 enqueue ──┐                                  ┌─ normal workers (all priorities)
           ├── job_runs (planned) ── wake ────┤
 scheduler ─┘        via notify channel       └─ fast workers   (priority ≥ 1 only —
                                                  interactive jobs never starve
                                                  behind slow bulk work)
```

**Event-driven wakeup.** Enqueues and scheduler ticks `kick()` a buffered
notify channel; idle workers block on it and start runs within microseconds.
A slow fallback poll (2 s) is only there to pick up delayed (backoff) retries
and any missed wakeup — the old 1 s busy-poll is gone.

**Worker lanes.** `jobs.workers` normal workers serve everything (highest
priority first). A dedicated `jobs.fast_workers` pool (default 3) serves
*only* `PriorityHigh` jobs — `_system/generate_title`, `generate_auto_tags`,
`generate_subtasks`, `vss_index`, `sync_*_embedding`, `_media` ingest jobs —
so a queue full of RSS refreshes/backups can never delay a note title.

**Atomic dequeue.** Claiming is a single
`UPDATE ... WHERE id = (SELECT candidate ...) AND status = 'planned' RETURNING`
statement: SQLite serializes writers, so the eligibility check (enabled,
due-time, **per-definition `max_parallel` cap**) and the status flip commit
together — two workers can never both pass the parallelism gate. Candidates
rank by `priority DESC, created_at ASC`.

**Exactly-once terminal transition.** Every terminal write (worker success /
panic / error, user cancel, watchdog timeout, lost-claim cleanup) is a guarded
`UPDATE ... WHERE status IN ('running','planned')` **in one transaction with
its `job_definition_stats` bump**. Whichever path wins the race finalizes the
row and bumps the ledger — exactly once, no double counts.

**Cron ticks never pile up.** If the previous run of a 15-minute job is still
active, the tick is skipped — a slow job can never queue copies of itself.

**Zombie recovery.** On startup, runs stuck in `running` (crashed previous
instance) reset to `planned`; planned runs whose definition has no registered
task are errored with a clear message.

## Reliability features

| Feature | Behavior |
|---|---|
| Automatic retries | Failed runs requeue with **exponential backoff + ±20 % jitter** (15 s >> attempt, cap 10 min, stored in `next_run_at`) up to the definition's `MaxRetries`. The errored row stays in the history, annotated with the retry note. Cancellation-induced errors are **never** auto-retried. |
| Timeouts | `Timeout` (per definition) is a hard cap enforced by the watchdog even while the server runs; cooperative tasks see a cancelled context, stubborn ones nonetheless get their row finalized and requeued per retry policy. |
| Watchdog | Every 10 s: enforces timeouts, clears `running` rows no live worker claims (crashed claims / manual DB edits), and kicks workers when delayed retries become due. |
| Cancellation | `CancelRun` works on **planned and running** runs. It commits the terminal `cancelled` row first, then cancels the task context — the worker's own finalize then no-ops (exactly-once holds). |
| Graceful shutdown | `Stop` cancels all running contexts (tasks finalize as cancelled — no auto-retry), waits up to 30 s. Leftover `running` rows reset at the next startup. |
| Panic safety | Task panics are recovered and recorded as the run's error. |

## Progress

Tasks receive a `RunContext` with the throttled `Progress(done, total, message)`
reporter (~2 writes/s per run, plus stage-change and 10 s heartbeat emits).
Progress lands on `job_runs` (`progress_done/total/message`, `progress_at`
heartbeat) and is broadcast to all websocket clients as a **`jobs.progress`**
live message; the frontend merges it into the running row without refetching.

```go
func myTask(ctx context.Context, rt *jobs.RunContext) (string, error) {
    for i, item := range items {
        if err := process(rt.DB, item); err != nil { return "", err }
        rt.Progress(i+1, len(items), "processing items")
    }
    return "done", nil
}
```

## Stats

The `job_definition_stats` ledger (keyed by `job_id`) aggregates every
finish — `runs_total/done/errored/cancelled`, `auto_retries`,
min/avg/max/total duration, `last_finished_at/status/result/error` — and
**survives the 30-day run-retention janitor**; it is backfilled once from
existing `job_runs` history at migration.

`GET /jobs/stats` returns:

- `workers`: total/busy/idle for both lanes,
- `queue`: planned / running / delayed (backoff) depth,
- `last_24h`: done/errored counts and avg duration,
- `definitions[]`: per-job metadata (schedule, priority, timeout, retries),
  the lifetime ledger, success rate, and live running/queued counts.

The Options view renders this as worker/queue chips and a per-job table
(success %, avg duration, last result, running/queued), refreshed live via
ws events.

## Defining a job

```go
// A plugin or the server:
func (p *MyPlugin) CronJobs() []notetype.CronJob {
    return []notetype.CronJob{{
        Name:        "my_job",
        Schedule:    "@every 15m",     // "" = ad-hoc (Enqueue only); @hourly/@daily
        Priority:    notetype.PriorityNormal,
        MaxParallel: 2,                // concurrent runs of THIS job
        Timeout:     5 * time.Minute, // 0 = none
        MaxRetries:  1,                // auto-retries with backoff
        Task:        myTask,           // func(ctx, rt) (string, error)
    }}
}
```

Schedules: `@every <duration>` (e.g. `@every 30m`), `@hourly`, `@daily`.
Sub-second timeouts round up to 1 s.

The server registers its own ad-hoc jobs under `_system` / `_media` /
`_backup` (see `internal/server/server.go`); plugins' jobs under their plugin
ID. `Enqueue(pluginID, jobName, payload)` deduplicates identical active runs
(same job + payload) via the partial unique index.

## API

| Endpoint | Purpose |
|---|---|
| `GET /jobs` | Recent runs with live progress fields + `pending_count` |
| `GET /jobs/stats` | Worker pools, queue depth, 24 h window, per-definition ledger |
| `POST /jobs/:id/retry` | Re-run an errored/cancelled run (new row, attempt + 1) |
| `POST /jobs/:id/cancel` | Cancel a planned **or running** run |

Websocket messages: `jobs.changed` (lifecycle — enqueue/running/done/errored/
cancelled/retried; the queue refetches) and `jobs.progress` (throttled live
progress; merged in place).

## Configuration

```toml
[jobs]
workers = 15       # normal workers (all job priorities)
fast_workers = 3   # dedicated pool for high-priority (interactive) jobs
```

## Testing

`go test ./internal/jobs/` is hermetic (in-memory SQLite, no VSS/LLM) and
mirrors the package layout — one test file per source file:

| File | Focus |
|---|---|
| `jobs_test.go` | shared helpers + core behavior: wakeup, fast-lane isolation, max-parallel, progress persistence, auto-retry, watchdog, cancel races, dedupe |
| `util_test.go` | timestamp round-trip (rapid PBT), log formatting, error wrap |
| `lifecycle_test.go` | manager defaults, nil-receiver guards, janitor, Start error paths, Stop idempotency |
| `queue_api_test.go` | Enqueue/RetryRun/CancelRun guards, ListRuns shape/limit, orphan-definition execution |
| `scheduler_test.go` | schedule parsing (incl. `@every 0` rejection), tick bounds, ad-hoc registration, disabled/deleted definitions |
| `progress_test.go` | progress clamps, throttle gates, drop-after-finalize |
| `finalize_test.go` | exactly-once terminal writes, ledger-vs-model PBT, backoff bounds PBT, auto-retry policy |
| `stats_test.go` | empty-DB snapshot, 24h window, per-definition ledger, busy split per lane, retry wakeups |
| `faults_test.go` | fault propagation (missing tables, aborted UPDATE) |

Property-based tests use `pgregory.net/rapid`; structural assertions use
`github.com/google/go-cmp` (both already module dependencies). Run with
`-race` before shipping changes:

```bash
go test ./internal/jobs/ -race -count=2
go test ./internal/jobs/ -cover   # currently ~93% of statements
```

Migration coverage (v1 schema → v2 columns/ledger/backfill) lives in
`internal/db/db_test.go`.
