// Pure job-system UI logic shared by JobQueue.vue and OptionsView.vue.
// No DOM, no refs, no timers beyond an injectable `now` — every
// function here is trivially unit-testable and mirrored 1:1 from the
// components that render it.

/** Icon glyph for a run status (v2 and legacy v1 vocabularies). */
export function statusIcon(status) {
    switch (status) {
        case "pending":
            return "⏳";
        case "planned":
            return "⏳";
        case "running":
            return "⟳";
        case "completed":
        case "done":
            return "✓";
        case "failed":
        case "errored":
            return "✗";
        case "cancelled":
            return "⊘";
        default:
            return "?";
    }
}

/** Progress bar width in percent, clamped to [0, 100]; 0 when unknown. */
export function progressPercent(run) {
    if (!run.progress_total) return 0;
    return Math.min(
        100,
        Math.round((run.progress_done / run.progress_total) * 100),
    );
}

/** A run is cancellable only while planned/pending or running. */
export function canCancel(run) {
    return (
        run.status === "planned" ||
        run.status === "running" ||
        run.status === "pending"
    );
}

/** "1s" / "2m05s" / "1h02m" — "" when unknown or negative. */
export function fmtDuration(ms) {
    if (ms == null || ms < 0) return "";
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
    const h = Math.floor(m / 60);
    return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

/**
 * Human duration of a run: the recorded duration_ms, or the live
 * elapsed time while it is running. `now` is injectable for tests.
 */
export function runDuration(run, now = Date.now()) {
    if (run.duration_ms != null) {
        return fmtDuration(run.duration_ms);
    }
    if (run.status === "running" && run.started_at) {
        return fmtDuration(now - new Date(run.started_at).getTime());
    }
    return "";
}

/** "MM-DD HH:mm" (local time) — "" for falsy input. */
export function fmtTime(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const hour = String(d.getHours()).padStart(2, "0");
    const minute = String(d.getMinutes()).padStart(2, "0");
    return `${month}-${day} ${hour}:${minute}`;
}

/**
 * Folds a jobs.progress live event into the matching running row,
 * in place, without a refetch. Returns true when something merged.
 * Unknown runs, non-running rows, and missing fields are skipped;
 * an empty message never overwrites an existing one.
 */
export function mergeProgress(runs, evt) {
    if (!evt) return false;
    const run = runs.find((r) => r.id === evt.run_id);
    if (!run || run.status !== "running") return false;
    let merged = false;
    if (evt.progress_done != null) {
        run.progress_done = evt.progress_done;
        merged = true;
    }
    if (evt.progress_total != null) {
        run.progress_total = evt.progress_total;
        merged = true;
    }
    if (evt.progress_message) {
        run.progress_message = evt.progress_message;
        merged = true;
    }
    return merged;
}

/** Pending count from a GET /jobs response (v2 field, v1 fallback). */
export function pendingCountFrom(data) {
    if (!data) return 0;
    return data.pending_count ?? data.total ?? 0;
}

/**
 * Emits one job-done event per generate_title run that newly reached
 * done since the last call. `seenDone` is the persistent Set of run
 * ids already reported (mutated in place).
 */
export function collectNewTitleDone(runs, seenDone) {
    const events = [];
    for (const r of runs) {
        if (
            r.job_name === "generate_title" &&
            (r.status === "completed" || r.status === "done") &&
            !seenDone.has(r.id)
        ) {
            seenDone.add(r.id);
            events.push({ name: "generate_title", runId: r.id });
        }
    }
    return events;
}

// --- OptionsView job-stats table formatters ---

/** "—" / "123ms" / "1.5s" / "2m05s" for a definition's avg duration. */
export function fmtMs(ms) {
    if (ms == null || ms <= 0) return "—";
    if (ms < 1000) return `${ms}ms`;
    const s = ms / 1000;
    if (s < 60) return `${s.toFixed(1)}s`;
    const m = Math.floor(s / 60);
    return `${m}m${String(Math.round(s % 60)).padStart(2, "0")}s`;
}

/** Success rate over attempted (done + errored) runs; "—" if none. */
export function fmtSuccess(d) {
    const attempted = d.runs_done + d.runs_errored;
    if (!attempted) return "—";
    return `${Math.round((d.runs_done / attempted) * 100)}%`;
}

/** Last-run column: the error/result when set, else the status. */
export function fmtLast(d) {
    if (!d.last_status) return "—";
    if (d.last_status === "errored" && d.last_error) {
        return d.last_error;
    }
    if (d.last_status === "done" && d.last_result) {
        return d.last_result;
    }
    return d.last_status;
}
