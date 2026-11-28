import {
    statusIcon,
    progressPercent,
    canCancel,
    fmtDuration,
    runDuration,
    fmtTime,
    mergeProgress,
    pendingCountFrom,
    collectNewTitleDone,
    fmtMs,
    fmtSuccess,
    fmtLast,
} from "../jobSystem.js";

// These tests pin the job-queue rendering contract that JobQueue.vue and
// OptionsView.vue share through jobSystem.js: the live-progress merge
// must fold jobs.progress frames into exactly the running row (the ws
// frame format's consumer), status/cancel handling must cover the v1
// vocabulary the history still contains, and the stats formatters must
// survive zero/null/garbage payloads without crashing the table.

describe("jobSystem helpers", () => {
    describe("statusIcon", () => {
        // Map table: status → glyph. Every case pins one rendered glyph;
        // the v1 entries keep legacy history rows from showing "?".
        const cases = {
            planned: "⏳", // v2 queued state
            pending: "⏳", // v1 queued state (legacy rows)
            running: "⟳", // live spinner glyph
            done: "✓", // v2 success
            completed: "✓", // v1 success (legacy rows)
            errored: "✗", // v2 failure
            failed: "✗", // v1 failure (legacy rows)
            cancelled: "⊘", // user-stopped
        };
        for (const [status, glyph] of Object.entries(cases)) {
            it(`renders ${status} as ${glyph}`, () => {
                expect(statusIcon(status)).toBe(glyph);
            });
        }

        it("renders unknown statuses as a question mark, never crashes", () => {
            // Manual DB edits / future statuses must not throw or render
            // blank — the queue stays readable.
            expect(statusIcon("weird")).toBe("?");
            expect(statusIcon("")).toBe("?");
            expect(statusIcon(undefined)).toBe("?");
        });
    });

    describe("progressPercent", () => {
        it("returns 0 without a total (no bar for unknown work)", () => {
            // The bar only renders when progress_total exists; the
            // percent helper must agree with that gate.
            expect(progressPercent({ progress_done: 5 })).toBe(0);
            expect(progressPercent({ progress_done: 5, progress_total: 0 })).toBe(0);
        });

        it("rounds and clamps into [0, 100]", () => {
            // 2/3 → 67 (rounds), over-complete rows must not overflow
            // the bar, partial rows must not go negative.
            expect(progressPercent({ progress_done: 2, progress_total: 3 })).toBe(67);
            expect(progressPercent({ progress_done: 5, progress_total: 4 })).toBe(100);
            expect(progressPercent({ progress_done: 0, progress_total: 4 })).toBe(0);
        });
    });

    describe("canCancel", () => {
        const cases = {
            planned: true, // queued — cancel wins before start
            running: true, // live task — ctx gets cancelled
            pending: true, // v1 queued vocabulary (legacy rows)
            done: false, // terminal
            errored: false, // terminal
            cancelled: false, // already stopped
            completed: false, // v1 terminal (legacy rows)
            failed: false, // v1 terminal (legacy rows)
            "": false, // garbage from manual edits
        };
        for (const [status, expected] of Object.entries(cases)) {
            it(`treats ${status || "empty"} as ${expected ? "cancellable" : "not cancellable"}`, () => {
                // The cancel button must only ever show while a cancel
                // would be accepted by the backend guard.
                expect(canCancel({ status })).toBe(expected);
            });
        }
    });

    describe("fmtDuration", () => {
        const cases = {
            null: "", // unknown duration renders nothing
            "-5": "", // clock skew must not render "-1s"
            "0": "0s", // instant runs still show
            "59999": "59s", // seconds range boundary
            "60000": "1m00s", // minute rollover pads
            "3725000": "1h02m", // hour rollover pads
        };
        for (const [ms, want] of Object.entries(cases)) {
            it(`formats ${ms}ms as ${JSON.stringify(want)}`, () => {
                const input = ms === "null" ? null : Number(ms);
                expect(fmtDuration(input)).toBe(want);
            });
        }
    });

    describe("runDuration", () => {
        it("prefers the recorded duration_ms", () => {
            // A finished run renders its stored duration, never a live
            // (wrong) elapsed value.
            expect(runDuration({ duration_ms: 1500, status: "done" })).toBe("1s");
        });

        it("renders live elapsed time for running runs without duration", () => {
            // Injected `now` keeps the live ticker deterministic: a run
            // started 10 s ago shows "10s".
            const started = "2026-05-12T10:00:00Z";
            const now = new Date("2026-05-12T10:00:10Z").getTime();
            expect(runDuration({ status: "running", started_at: started }, now)).toBe("10s");
        });

        it("renders nothing for terminal runs without a recorded duration", () => {
            // Old rows without duration_ms must not fabricate a value.
            expect(runDuration({ status: "done" })).toBe("");
        });
    });

    describe("fmtTime", () => {
        it("formats an ISO timestamp as MM-DD HH:mm in local time", () => {
            // Expected value is derived from the same Date the helper
            // uses, so the test holds in every timezone.
            const iso = "2026-05-12T15:04:05Z";
            const d = new Date(iso);
            const pad = (n) => String(n).padStart(2, "0");
            const want = `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
            expect(fmtTime(iso)).toBe(want);
        });

        it("returns '' for missing timestamps", () => {
            // Rows without created_at must not render "Invalid Date".
            expect(fmtTime(null)).toBe("");
            expect(fmtTime("")).toBe("");
            expect(fmtTime(undefined)).toBe("");
        });
    });

    describe("mergeProgress (jobs.progress live frames)", () => {
        const frame = (fields) => ({ run_id: 7, ...fields });

        it("folds a frame into the matching running row", () => {
            // The whole point of the ws progress frame: the running row
            // updates in place without a refetch.
            const runs = [{ id: 7, status: "running", progress_done: 0, progress_total: 0 }];
            expect(mergeProgress(runs, frame({ progress_done: 1, progress_total: 3, progress_message: "step one" }))).toBe(true);
            expect(runs[0]).toMatchObject({ progress_done: 1, progress_total: 3, progress_message: "step one" });
        });

        it("skips rows that are not running", () => {
            // A frame arriving after the run finished must not
            // resurrect progress on a terminal row (the backend drops
            // them too — the UI must agree).
            const runs = [{ id: 7, status: "done", progress_done: 9 }];
            expect(mergeProgress(runs, frame({ progress_done: 1 }))).toBe(false);
            expect(runs[0].progress_done).toBe(9);
        });

        it("skips unknown run ids and null frames without crashing", () => {
            // Frames for runs outside the visible window arrive
            // routinely (limit-50 listings); they are noise, not
            // errors.
            expect(mergeProgress([{ id: 8, status: "running" }], frame({ progress_done: 1 }))).toBe(false);
            expect(mergeProgress([{ id: 7, status: "running" }], null)).toBe(false);
            expect(mergeProgress([{ id: 7, status: "running" }], undefined)).toBe(false);
        });

        it("merges partial frames and never clears an existing message", () => {
            // The backend omits zero-value fields (omitempty): a frame
            // with only done, or only a message, must merge just that —
            // and an empty message never overwrites the last real one.
            const runs = [{ id: 7, status: "running", progress_done: 1, progress_message: "step one" }];
            expect(mergeProgress(runs, frame({ progress_done: 2 }))).toBe(true);
            expect(runs[0].progress_done).toBe(2);
            expect(runs[0].progress_total).toBeUndefined();
            expect(runs[0].progress_message).toBe("step one");

            expect(mergeProgress(runs, frame({ progress_total: 3, progress_message: "" }))).toBe(true);
            expect(runs[0].progress_total).toBe(3);
            expect(runs[0].progress_message).toBe("step one");
        });
    });

    describe("pendingCountFrom", () => {
        const cases = {
            "v2 pending_count": [{ pending_count: 4, runs: [] }, 4],
            "v1 total fallback": [{ total: 2 }, 2],
            "missing counts": [{ runs: [] }, 0],
            "null payload": [null, 0],
        };
        for (const [name, [data, want]] of Object.entries(cases)) {
            it(`reads ${name} as ${want}`, () => {
                // The badge must show the server's number whenever one
                // exists, and 0 (not undefined) otherwise.
                expect(pendingCountFrom(data)).toBe(want);
            });
        }
    });

    describe("collectNewTitleDone (job-done emit)", () => {
        const doneTitle = (id, status = "done") => ({ id, job_name: "generate_title", status });

        it("emits one event per newly-done generate_title run", () => {
            // NotesView reloads titles on this emit; a missed one leaves
            // an "Untitled" note, a spurious one causes extra reloads.
            const seen = new Set();
            expect(collectNewTitleDone([doneTitle(1), doneTitle(2)], seen)).toEqual([
                { name: "generate_title", runId: 1 },
                { name: "generate_title", runId: 2 },
            ]);
        });

        it("dedupes across polls — a done title run emits exactly once", () => {
            // load() runs on every refresh; without the seen-set the
            // same finished run would re-emit forever.
            const seen = new Set();
            const runs = [doneTitle(1)];
            expect(collectNewTitleDone(runs, seen)).toHaveLength(1);
            expect(collectNewTitleDone(runs, seen)).toHaveLength(0);
        });

        it("ignores other jobs and non-done statuses", () => {
            // Only finished title jobs fire the reload; running titles
            // and unrelated done jobs must stay silent.
            const seen = new Set();
            expect(collectNewTitleDone([
                { id: 3, job_name: "generate_title", status: "running" },
                { id: 4, job_name: "other_job", status: "done" },
                { id: 5, job_name: "generate_title", status: "cancelled" },
            ], seen)).toEqual([]);
        });

        it("also accepts the v1 'completed' status", () => {
            // Legacy rows surface with v1 vocabulary; a completed title
            // run must still trigger the reload.
            const seen = new Set();
            expect(collectNewTitleDone([doneTitle(6, "completed")], seen)).toEqual([
                { name: "generate_title", runId: 6 },
            ]);
        });
    });

    describe("OptionsView stats formatters", () => {
        describe("fmtMs", () => {
            const cases = {
                null: "—", // unknown duration shows a dash
                0: "—", // zero-duration runs are noise, not "0ms"
                "-1": "—", // clock skew must not render negatives
                500: "500ms",
                1500: "1.5s",
                59999: "60.0s", // sub-minute boundary keeps one decimal
                60000: "1m00s",
                125000: "2m05s", // second remainder rounds and pads
            };
            for (const [ms, want] of Object.entries(cases)) {
                it(`formats ${ms}ms as ${JSON.stringify(want)}`, () => {
                    expect(fmtMs(ms === "null" ? null : Number(ms))).toBe(want);
                });
            }
        });

        describe("fmtSuccess", () => {
            it("shows a dash when nothing was attempted", () => {
                // Cancelled-only or fresh definitions must not render
                // "NaN%" or "0%" — there is no success data yet.
                expect(fmtSuccess({ runs_done: 0, runs_errored: 0, runs_cancelled: 3 })).toBe("—");
            });

            it("rounds the success rate over done+errored attempts", () => {
                // The rate is attempts-based (cancelled excluded), 3/4
                // → 75%.
                expect(fmtSuccess({ runs_done: 3, runs_errored: 1 })).toBe("75%");
                expect(fmtSuccess({ runs_done: 1, runs_errored: 2 })).toBe("33%");
            });
        });

        describe("fmtLast", () => {
            const cases = {
                "no last status": [{}, "—"],
                "errored shows its error": [{ last_status: "errored", last_error: "boom" }, "boom"],
                "errored without error falls back to the status": [{ last_status: "errored" }, "errored"],
                "done shows its result": [{ last_status: "done", last_result: "ok 1" }, "ok 1"],
                "done without result falls back to the status": [{ last_status: "done" }, "done"],
                "cancelled shows the bare status": [{ last_status: "cancelled" }, "cancelled"],
            };
            for (const [name, [def, want]] of Object.entries(cases)) {
                it(`${name} → ${JSON.stringify(want)}`, () => {
                    // The Last column must surface the verdict: the
                    // concrete error/result when there is one, the raw
                    // status otherwise.
                    expect(fmtLast(def)).toBe(want);
                });
            }
        });
    });
});
