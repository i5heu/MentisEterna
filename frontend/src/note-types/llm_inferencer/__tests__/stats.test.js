import {
    ROUTING,
    routingOf,
    sourceLabel,
    fmtNum,
    fmtTokens,
    fmtDuration,
    fmtWhen,
    percentOf,
    analyticsRows,
    hasStats,
} from "../stats.js";

// These tests guard the statistics rendering contract between the server
// view (llminferencer.StatsView) and the note component: routing kinds
// must never misclassify remote spend as local (or vice versa), the
// formatters must survive zero/null/garbage payloads, and the display
// rows must keep one row per usage×routing×model without recomputing
// token sums.

describe("llm_inferencer stats helpers", () => {
    describe("routing", () => {
        it("maps sources to remote/local exactly as the backend defines", () => {
            // The backend's llm.ChatSource* strings are the contract;
            // "note" rows are remote provider calls, "local"/"config"
            // are served by the config.toml system.
            expect(ROUTING.note).toBe("remote");
            expect(ROUTING.local).toBe("local");
            expect(ROUTING.config).toBe("local");
        });

        it("treats unknown sources as remote so nothing hides from the split", () => {
            // Future/unknown sources must never be counted into "local"
            // — the local↔remote split is about provider spend.
            expect(routingOf("something-new")).toBe("remote");
            expect(routingOf("")).toBe("remote");
            expect(routingOf(undefined)).toBe("remote");
            expect(routingOf(null)).toBe("remote");
        });

        it("labels every known source humanly", () => {
            expect(sourceLabel("note")).toContain("Remote");
            expect(sourceLabel("local")).toContain("pinned");
            expect(sourceLabel("config")).toContain("config.toml");
            // Unknown sources pass through instead of crashing.
            expect(sourceLabel("zzz")).toBe("zzz");
        });
    });

    describe("formatters", () => {
        it("formats numbers with separators and survives garbage", () => {
            expect(fmtNum(1234567)).toBe("1,234,567");
            expect(fmtNum(0)).toBe("0");
            expect(fmtNum(null)).toBe("0");
            expect(fmtTokens(undefined)).toBe("0");
        });

        it("compacts token counts at k/M boundaries", () => {
            // Readers need to scan token budgets quickly; full precision
            // stays available in the raw numbers behind the badge.
            expect(fmtTokens(940)).toBe("940");
            expect(fmtTokens(12345)).toBe("12.3k");
            expect(fmtTokens(1230000)).toBe("1.2M");
        });

        it("renders latencies across the ms/s boundary", () => {
            expect(fmtDuration(640)).toBe("640 ms");
            expect(fmtDuration(1210)).toBe("1.2 s");
        });

        it("parses SQLite UTC stamps into local date-times", () => {
            // SQLite CURRENT_TIMESTAMP has no zone marker; the helper
            // must parse it as UTC, not local time.
            const out = fmtWhen("2026-05-12 03:00:00");
            expect(out).not.toBe("—");
            expect(out).not.toContain("T");
            // Unparsable input passes through untouched.
            expect(fmtWhen("whenever")).toBe("whenever");
            expect(fmtWhen("")).toBe("—");
        });

        it("hides the percentage when the total is zero", () => {
            // A 0-total split must show no fake "0%" badge.
            expect(percentOf(5, 0)).toBe("");
            expect(percentOf(0, 0)).toBe("");
            expect(percentOf(25, 100)).toBe("25%");
        });
    });

    describe("analyticsRows", () => {
        const rows = [
            {
                usage: "rss_classify",
                label: "RSS Evaluation",
                source: "note",
                provider: "openrouter",
                model: "prov/small",
                calls: 10,
                errors: 1,
                prompt_tokens: 100,
                completion_tokens: 50,
                total_tokens: 150,
                avg_duration_ms: 120,
                last_used_at: "2026-05-12 03:00:00",
            },
            {
                usage: "rss_classify",
                label: "RSS Evaluation",
                source: "config",
                model: "gemma-4-e2b",
                calls: 4,
                errors: 0,
                prompt_tokens: 40,
                completion_tokens: 8,
                total_tokens: 48,
                avg_duration_ms: 80,
                last_used_at: "",
            },
            {
                usage: "test",
                label: "Connection Test",
                source: "note",
                provider: "openrouter",
                model: "prov/big",
                calls: 2,
                errors: 2,
                prompt_tokens: 0,
                completion_tokens: 0,
                total_tokens: 0,
                avg_duration_ms: 0,
                last_used_at: "2026-05-11 09:00:00",
            },
        ];

        it("derives routing and keeps token sums untouched", () => {
            const out = analyticsRows(rows);
            const byKey = Object.fromEntries(out.map((r) => [r.key, r]));
            const remote = byKey["rss_classify|note|prov/small"];
            expect(remote.routing).toBe("remote");
            expect(remote.totalTokens).toBe(150);
            expect(remote.calls).toBe(10);
            expect(remote.errors).toBe(1);
            expect(remote.provider).toBe("openrouter");
            const local = byKey["rss_classify|config|gemma-4-e2b"];
            expect(local.routing).toBe("local");
            expect(local.provider).toBe("");
        });

        it("sorts by label, remote first, then call volume", () => {
            // Stable, predictable table order — the server's ORDER BY is
            // fine for SQL but the card wants usage-first grouping.
            const out = analyticsRows(rows);
            expect(out[0].label).toBe("Connection Test");
            expect(out[1].label).toBe("RSS Evaluation");
            expect(out[1].routing).toBe("remote");
            expect(out[2].routing).toBe("local");
        });

        it("survives empty and malformed input", () => {
            expect(analyticsRows([])).toEqual([]);
            expect(analyticsRows(undefined)).toEqual([]);
            // Missing numbers default to 0 instead of NaN poisoning the UI.
            const out = analyticsRows([
                { usage: "x", label: "X", source: "config", model: "m" },
            ]);
            expect(out[0].calls).toBe(0);
            expect(out[0].totalTokens).toBe(undefined);
            expect(out[0].label).toBe("X");
            expect(out[0].usage).toBe("x");
        });
    });

    describe("hasStats", () => {
        it("gates on rows or recent calls, never crashing on null", () => {
            expect(hasStats(null)).toBe(false);
            expect(hasStats({})).toBe(false);
            expect(hasStats({ rows: [], recent: [] })).toBe(false);
            expect(hasStats({ rows: [{ usage: "x" }], recent: [] })).toBe(true);
            expect(hasStats({ rows: [], recent: [{ usage: "x" }] })).toBe(true);
        });
    });
});
