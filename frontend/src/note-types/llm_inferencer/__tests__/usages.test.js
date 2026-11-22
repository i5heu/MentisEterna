import {
    TIERS,
    normalizeUsages,
    usageModeOf,
    bindingForMode,
    emitUsages,
    systemDefaultText,
} from "../usages.js";

// The failure these tests guard against, verbatim from the wild:
// llm_inferencer: unknown usage "1" (not in the app usage catalog) — caused by
// a non-object usages value degrading into numeric index keys somewhere
// upstream. The normalizer and the emit whitelist must make that impossible.

describe("llm_inferencer usages helpers", () => {
    describe("normalizeUsages", () => {
        it("keeps well-formed bindings in canonical shape", () => {
            // Prevents shape drift: trimmed values only, no extra fields.
            expect(
                normalizeUsages({
                    a: { local: true, noise: 1 },
                    b: { tier: "  middle  " },
                    c: { model: " prov/x " },
                }),
            ).toEqual({
                a: { local: true },
                b: { tier: "middle" },
                c: { model: "prov/x" },
            });
        });

        it("never turns an array into index-keyed bindings", () => {
            // THE regression: an array-shaped usages value used to become
            // {"0": …, "1": …} — the "unknown usage 1" save failure.
            expect(
                normalizeUsages([{ local: true }, { tier: "big" }]),
            ).toEqual({});
        });

        it("drops junk-shaped entries instead of inventing bindings", () => {
            // Malformed values (strings, arrays, empties, nulls) never become
            // bindings. A stray KEY like "1" is shape-valid at this layer —
            // keys are not this function's business — and gets dropped by
            // emitUsages' catalog whitelist (and by the server's sanitizer).
            expect(
                normalizeUsages({
                    good: { tier: "tiny" },
                    "1": { tier: "big" }, // stray key: survives here, whitelisted later
                    empty: {}, // no binding at all
                    str: "middle", // string, not an object
                    arr: [{ tier: "big" }], // array binding
                    nil: null,
                }),
            ).toEqual({ good: { tier: "tiny" }, "1": { tier: "big" } });
        });

        it("treats null, strings, and undefined as empty", () => {
            // Guards every call site: no branch may throw on missing data.
            expect(normalizeUsages(null)).toEqual({});
            expect(normalizeUsages(undefined)).toEqual({});
            expect(normalizeUsages("usages")).toEqual({});
            expect(normalizeUsages({})).toEqual({});
        });
    });

    describe("usageModeOf", () => {
        it("maps binding shapes to the selector modes", () => {
            // Prevents the editor select from desyncing from the draft.
            expect(usageModeOf(undefined)).toBe("none");
            expect(usageModeOf(null)).toBe("none");
            expect(usageModeOf({})).toBe("model"); // typed-but-empty model
            expect(usageModeOf({ model: "" })).toBe("model");
            expect(usageModeOf({ tier: "big" })).toBe("tier");
            expect(usageModeOf({ local: true })).toBe("local");
        });
    });

    describe("bindingForMode", () => {
        it("builds the binding for each mode switch", () => {
            // Prevents invalid drafts: mode switches only ever produce the
            // three canonical binding shapes or an unmap.
            expect(bindingForMode("none", { tier: "big" })).toBeNull();
            expect(bindingForMode("local", { tier: "big" })).toEqual({
                local: true,
            });
            expect(bindingForMode("model", { tier: "big" })).toEqual({
                model: "",
            });
        });

        it("keeps the tier when re-entering tier mode", () => {
            // Prevents accidental tier resets when the user switches
            // tier → model → tier while fiddling with a mapping.
            expect(bindingForMode("tier", { tier: "middle" })).toEqual({
                tier: "middle",
            });
            expect(bindingForMode("tier", undefined)).toEqual({
                tier: "small",
            });
        });
    });

    describe("emitUsages", () => {
        it("emits only catalog-known ids in canonical shape", () => {
            // The save-side whitelist: junk keys in the draft can never
            // reach the server, so "unknown usage 1" cannot be produced —
            // no matter what got into the draft upstream.
            expect(
                emitUsages(
                    {
                        "1": { tier: "big" }, // junk key
                        rss_classify: { tier: "small", local: true }, // conflict → local wins
                        title: { model: " prov/x " },
                        tags: {}, // empty → dropped
                        subtasks: "middle", // malformed → dropped
                    },
                    ["rss_classify", "title", "tags", "subtasks"],
                ),
            ).toEqual({
                rss_classify: { local: true },
                title: { model: "prov/x" },
            });
        });

        it("allows any key when no catalog is known yet", () => {
            // Unsaved notes have no server view: the fallback rows are the
            // draft keys themselves, so nothing may be filtered away.
            expect(emitUsages({ a: { tier: "big" } }, [])).toEqual({
                a: { tier: "big" },
            });
        });

        it("never emits junk-shaped sources", () => {
            // Arrays as the usages map or as bindings stay out of payloads.
            expect(emitUsages([{ tier: "big" }], ["a", "b"])).toEqual({});
        });
    });

    describe("TIERS", () => {
        it("is the fixed capability tier list", () => {
            // The editor and backend agree on exactly these tier names.
            expect(TIERS).toEqual(["big", "middle", "small", "tiny"]);
        });
    });

    describe("systemDefaultText", () => {
        it("formats the config.toml default row", () => {
            // Prevents rendering blanks where the system default matters.
            expect(
                systemDefaultText({ tier: "medium", model: "gemma-x" }),
            ).toBe("medium → gemma-x");
            expect(systemDefaultText({ model: "gemma-x" })).toBe(
                "system → gemma-x",
            );
            expect(systemDefaultText(null)).toBe("—");
            expect(systemDefaultText({})).toBe("—");
        });
    });
});
