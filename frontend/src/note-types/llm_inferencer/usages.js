/**
 * Pure helpers for the llm_inferencer note type: usage-binding shapes,
 * normalization, and the emit-side whitelist. The usage catalog is SERVER
 * metadata (the server view builds it) — client-side, a binding is only ever
 * emitted when its id appears in the catalog rows the editor is rendering.
 * These functions are deliberately framework-free so they can be unit-tested
 * (see __tests__/usages.test.js) and reused by the registry normalizer.
 */

export const TIERS = ["big", "middle", "small", "tiny"];

/**
 * normalizeUsages cleans a persisted/edited usages map into well-formed
 * bindings only. Arrays, strings, null, and malformed entries are DROPPED —
 * arrays must never turn into index keys ("0", "1", …): an array-shaped
 * payload is exactly the "unknown usage 1" save failure.
 */
export function normalizeUsages(raw) {
    const out = {};
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return out;
    for (const [id, b] of Object.entries(raw)) {
        if (!b || typeof b !== "object" || Array.isArray(b)) continue;
        if (b.local === true) {
            out[id] = { local: true };
        } else if (typeof b.tier === "string" && b.tier.trim()) {
            out[id] = { tier: b.tier.trim() };
        } else if (typeof b.model === "string" && b.model.trim()) {
            out[id] = { model: b.model.trim() };
        }
    }
    return out;
}

/**
 * usageModeOf reports the editor mode ("none" | "tier" | "model" | "local")
 * for one draft binding (or null).
 */
export function usageModeOf(b) {
    if (!b) return "none";
    if (b.local) return "local";
    return b.tier ? "tier" : "model";
}

/**
 * bindingForMode builds the draft binding a mode switch produces
 * (null = unmap). Switching to "tier" keeps the existing tier when the
 * binding was already a tier binding.
 */
export function bindingForMode(mode, current) {
    switch (mode) {
        case "local":
            return { local: true };
        case "tier":
            return { tier: (current && current.tier) || "small" };
        case "model":
            return { model: "" };
        default:
            return null; // "none" — not mapped
    }
}

/**
 * emitUsages builds the save payload's usages object: only ids the editor
 * actually knows (the usage rows displayed — server catalog ids) are
 * emitted, and only in canonical shape. Whatever else sits in the draft map
 * (stale data, malformed or index keys, broken shapes) is dropped, so a
 * payload the server would reject can never be built here.
 */
export function emitUsages(usages, knownIds) {
    const known = new Set(knownIds || []);
    const anyIdAllowed = known.size === 0; // no catalog yet (unsaved note)
    const out = {};
    for (const [id, b] of Object.entries(usages || {})) {
        if (!anyIdAllowed && !known.has(id)) continue;
        if (!b || typeof b !== "object" || Array.isArray(b)) continue;
        if (b.local === true) {
            out[id] = { local: true };
        } else if (typeof b.tier === "string" && b.tier.trim()) {
            out[id] = { tier: b.tier.trim() };
        } else if (typeof b.model === "string" && b.model.trim()) {
            out[id] = { model: b.model.trim() };
        }
    }
    return out;
}

/**
 * systemDefaultText formats a usage's config.toml system default:
 * "tier → model" (or "—" when unknown).
 */
export function systemDefaultText(sys) {
    if (!sys || !sys.model) return "—";
    return `${sys.tier || "system"} → ${sys.model}`;
}
