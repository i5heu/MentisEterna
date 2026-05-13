/**
 * Note-type registry — single source of truth for note-type definitions.
 *
 * Powers both the type picker in NotesView.vue and the renderer lookup in
 * NoteTypeRenderer.vue.  Each entry describes one note type and how the
 * frontend should handle it.
 */

import { defineAsyncComponent } from "vue";

// ---------------------------------------------------------------------------
// Standard helpers used by several types
// ---------------------------------------------------------------------------

const NULL_DATA = () => null;

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

const registry = [
    {
        id: "standard",
        label: "Standard Note",
        component: null, // no custom renderer needed
        emptyCustomData: NULL_DATA,
        normalizeCustomData: (raw, _note) => raw || null,
        supportsSchemaFallback: false,
    },

    {
        id: "recipe",
        label: "Recipe",
        component: defineAsyncComponent(
            () => import("./recipe/RecipeNoteType.vue"),
        ),
        emptyCustomData: () => ({
            ingredients: [],
            servings: "",
            attention_time: "",
            total_time: "",
            grams_per_serving: "",
            kcal_per_serving: "",
            freezable: false,
            pre_cook_servings: "",
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return {
                    ingredients: [],
                    servings: "",
                    attention_time: "",
                    total_time: "",
                    grams_per_serving: "",
                    kcal_per_serving: "",
                    freezable: false,
                    pre_cook_servings: "",
                };
            }
            const ings = Array.isArray(raw.ingredients)
                ? raw.ingredients
                : Array.isArray(raw)
                  ? raw
                  : [];
            return {
                ingredients: ings.map((i) => ({
                    name: i.name || "",
                    amount: i.amount || "",
                    unit: i.unit || "",
                })),
                servings: raw.servings || "",
                attention_time: raw.attention_time || "",
                total_time: raw.total_time || "",
                grams_per_serving: raw.grams_per_serving || "",
                kcal_per_serving: raw.kcal_per_serving || "",
                freezable: !!raw.freezable,
                pre_cook_servings: raw.pre_cook_servings || "",
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
    },

    {
        id: "recipe_overview",
        label: "Recipe Overview",
        component: defineAsyncComponent(
            () => import("./recipe_overview/RecipeOverviewNoteType.vue"),
        ),
        emptyCustomData: () => ({
            recipes: [],
            grocery_lists: [],
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return { recipes: [], grocery_lists: [] };
            }
            return {
                recipes: Array.isArray(raw.recipes) ? raw.recipes : [],
                grocery_lists: Array.isArray(raw.grocery_lists)
                    ? raw.grocery_lists
                    : [],
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
    },

    {
        id: "example",
        label: "Example (Checklist)",
        component: defineAsyncComponent(
            () => import("./example/ChecklistNoteType.vue"),
        ),
        emptyCustomData: () => ({
            items: [],
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return { items: [] };
            }
            // The backend may return { items: [...] } or just a raw array.
            const its = raw.items || raw;
            return {
                items: Array.isArray(its)
                    ? its.map((it) => ({
                          label: it.label || "",
                          checked: !!it.checked,
                      }))
                    : [],
            };
        },
        supportsSchemaFallback: false,
    },

    {
        id: "index",
        label: "Tag Index",
        component: defineAsyncComponent(
            () => import("./index/IndexNoteType.vue"),
        ),
        emptyCustomData: () => ({
            mode: "global",
            selected_tags: [],
            entries: [],
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return { mode: "global", selected_tags: [], entries: [] };
            }
            return {
                mode: raw.mode || "global",
                selected_tags: Array.isArray(raw.selected_tags)
                    ? raw.selected_tags
                    : [],
                entries: Array.isArray(raw.entries) ? raw.entries : [],
            };
        },
        supportsSchemaFallback: false,
    },
];

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Get a registry entry by note-type id.
 * Returns null if the id is unknown.
 */
export function getNoteType(id) {
    return registry.find((t) => t.id === id) || null;
}

/**
 * Like getNoteType but never returns null — falls back to "standard".
 */
export function getNoteTypeOrDefault(id) {
    return getNoteType(id) || registry[0];
}

/**
 * Build the picker options array (value + label) consumed by NotesView.vue.
 */
export function getTypeOptions() {
    return registry.map((t) => ({ value: t.id, label: t.label }));
}

/**
 * The raw registry array.  Import only when you need to iterate everything.
 */
export { registry };
