/**
 * Note-type registry — single source of truth for note-type definitions.
 *
 * Powers both the type picker in NotesView.vue and the renderer lookup in
 * NoteTypeRenderer.vue.  Each entry describes one note type and how the
 * frontend should handle it.
 *
 * The backend /note-types endpoint returns Manifest[] objects that are
 * merged into the local registry entries to provide editor mode, viewer
 * mode, actions metadata, and default_config.  Call fetchAndMergeManifests()
 * after login to synchronize with the server.
 *
 * Optional per-type flags:
 *
 *   defaultChildType: "typeId"  — child notes created under this type
 *                                  will default to the given type (e.g.
 *                                  task_overview → "task"). Omitting this
 *                                  falls back to "standard".
 *
 *   lazyChildren: true           — children are not auto-fetched when this
 *                                  note is selected. A "Load children"
 *                                  button is shown instead. Use for
 *                                  dashboard/overview types where the
 *                                  children list is secondary.
 */

import { defineAsyncComponent } from "vue";
import { fetchNoteTypes } from "../api.js";

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
        id: "recipe_overview",
        label: "Recipe Overview",
        component: defineAsyncComponent(
            () => import("./recipe_overview/RecipeOverviewNoteType.vue"),
        ),
        emptyCustomData: () => ({
            recipes: [],
            grocery_lists: [],
            unvalid_ingredients: [],
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return {
                    recipes: [],
                    grocery_lists: [],
                    unvalid_ingredients: [],
                };
            }
            return {
                recipes: Array.isArray(raw.recipes) ? raw.recipes : [],
                grocery_lists: Array.isArray(raw.grocery_lists)
                    ? raw.grocery_lists
                    : [],
                unvalid_ingredients: Array.isArray(raw.unvalid_ingredients)
                    ? raw.unvalid_ingredients
                    : [],
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
        defaultChildType: "recipe",
        lazyChildren: true,
    },

    {
        id: "recipe",
        label: "Recipe",
        component: defineAsyncComponent(
            () => import("./recipe/RecipeNoteType.vue"),
        ),
        emptyCustomData: () => ({
            ingredients: [],
            ingredient_order_manual: false,
            servings: "",
            attention_time: "",
            total_time: "",
            grams_per_serving: "",
            kcal_per_serving: "",
            rating: 0,
            freezable: false,
            pre_cook_servings: "",
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return {
                    ingredients: [],
                    ingredient_order_manual: false,
                    servings: "",
                    attention_time: "",
                    total_time: "",
                    grams_per_serving: "",
                    kcal_per_serving: "",
                    rating: 0,
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
                    id: Number.isFinite(Number(i.id)) ? Number(i.id) : 0,
                    name: i.name || "",
                    prepare: i.prepare || "",
                    amount: i.amount || "",
                    unit: i.unit || "",
                    non_metric_amount: i.non_metric_amount || "",
                    non_metric_unit: i.non_metric_unit || "",
                    metric_validated: !!i.metric_validated,
                    grocery_category: i.grocery_category || "",
                    grocery_category_manual: !!i.grocery_category_manual,
                })),
                ingredient_order_manual: !!raw.ingredient_order_manual,
                servings: raw.servings || "",
                attention_time: raw.attention_time || "",
                total_time: raw.total_time || "",
                grams_per_serving: raw.grams_per_serving || "",
                kcal_per_serving: raw.kcal_per_serving || "",
                rating: Number.isFinite(Number(raw.rating))
                    ? Math.max(0, Math.min(10, Number(raw.rating)))
                    : 0,
                freezable: !!raw.freezable,
                pre_cook_servings: raw.pre_cook_servings || "",
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
        id: "home",
        label: "Home",
        component: defineAsyncComponent(
            () => import("./home/HomeNoteType.vue"),
        ),
        emptyCustomData: () => ({
            recent_notes: [],
            stats: {},
            mind_dump: "",
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return { recent_notes: [], stats: {}, mind_dump: "" };
            }
            return {
                recent_notes: Array.isArray(raw.recent_notes)
                    ? raw.recent_notes
                    : [],
                stats: raw.stats || {},
                mind_dump: raw.mind_dump || "",
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
    },

    {
        id: "task",
        label: "Task",
        component: defineAsyncComponent(
            () => import("./task/TaskNoteType.vue"),
        ),
        emptyCustomData: () => ({
            status: "todo",
            difficulty: 0,
            fun: 0,
            priority: 0,
            description: "",
            due_date: "",
            time_estimation: "",
            time_used: "",
            recurring: "none",
            recurring_days: 0,
            completed_at: "",
            pending_does_not_force_daily_inclusion: false,
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return {
                    status: "todo",
                    difficulty: 0,
                    fun: 0,
                    priority: 0,
                    description: "",
                    due_date: "",
                    time_estimation: "",
                    time_used: "",
                    recurring: "none",
                    recurring_days: 0,
                    completed_at: "",
                    pending_does_not_force_daily_inclusion: false,
                };
            }
            return {
                status: raw.status || "todo",
                difficulty: raw.difficulty ?? 0,
                fun: raw.fun ?? 0,
                priority: raw.priority ?? 0,
                description: raw.description || "",
                due_date: raw.due_date || "",
                time_estimation: raw.time_estimation || "",
                time_used: raw.time_used || "",
                recurring: raw.recurring || "none",
                recurring_days: raw.recurring_days ?? 0,
                completed_at: raw.completed_at || "",
                pending_does_not_force_daily_inclusion:
                    raw.pending_does_not_force_daily_inclusion ?? false,
            };
        },
        supportsSchemaFallback: false,
    },

    {
        id: "task_overview",
        label: "Task Overview",
        component: defineAsyncComponent(
            () => import("./taskoverview/TaskOverviewNoteType.vue"),
        ),
        emptyCustomData: () => ({
            daily_task_count: 3,
            urgent_due_days: 3,
            priority_weight: 4,
            due_urgency_weight: 6,
            difficulty_weight: -1,
            fun_weight: 0.75,
            time_estimation_weight: -0.5,
            fun_time_weight: 0.1,
            tasks: [],
            scored_open_tasks: [],
            daily_tasks: [],
            daily_history: [],
            stats: {},
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return {
                    daily_task_count: 3,
                    urgent_due_days: 3,
                    priority_weight: 4,
                    due_urgency_weight: 6,
                    difficulty_weight: -1,
                    fun_weight: 0.75,
                    time_estimation_weight: -0.5,
                    fun_time_weight: 0.1,
                    tasks: [],
                    scored_open_tasks: [],
                    daily_tasks: [],
                    daily_history: [],
                    stats: {},
                };
            }
            return {
                daily_task_count: raw.daily_task_count ?? 3,
                urgent_due_days: raw.urgent_due_days ?? 3,
                priority_weight: raw.priority_weight ?? 4,
                due_urgency_weight: raw.due_urgency_weight ?? 6,
                difficulty_weight: raw.difficulty_weight ?? -1,
                fun_weight: raw.fun_weight ?? 0.75,
                time_estimation_weight: raw.time_estimation_weight ?? -0.5,
                fun_time_weight: raw.fun_time_weight ?? 0.1,
                tasks: Array.isArray(raw.tasks) ? raw.tasks : [],
                scored_open_tasks: Array.isArray(raw.scored_open_tasks)
                    ? raw.scored_open_tasks
                    : [],
                daily_tasks: Array.isArray(raw.daily_tasks)
                    ? raw.daily_tasks
                    : [],
                daily_history: Array.isArray(raw.daily_history)
                    ? raw.daily_history
                    : [],
                stats: raw.stats || {},
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
        defaultChildType: "task",
        lazyChildren: true,
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
                entries: Array.isArray(raw.entries)
                    ? raw.entries.map((entry) => ({
                          tag: entry?.tag || "",
                          source: entry?.source || "unknown",
                          count: Number.isFinite(Number(entry?.count))
                              ? Number(entry.count)
                              : 0,
                          user_count: Number.isFinite(Number(entry?.user_count))
                              ? Number(entry.user_count)
                              : 0,
                          auto_count: Number.isFinite(Number(entry?.auto_count))
                              ? Number(entry.auto_count)
                              : 0,
                          notes: Array.isArray(entry?.notes)
                              ? entry.notes.map((note) => ({
                                    note_id: Number.isFinite(
                                        Number(note?.note_id),
                                    )
                                        ? Number(note.note_id)
                                        : 0,
                                    title: note?.title || "",
                                    parent_id: Number.isFinite(
                                        Number(note?.parent_id),
                                    )
                                        ? Number(note.parent_id)
                                        : null,
                                    created_at: note?.created_at || "",
                                    source: note?.source || "unknown",
                                    has_user_tag: !!note?.has_user_tag,
                                    has_auto_tag: !!note?.has_auto_tag,
                                }))
                              : [],
                      }))
                    : [],
            };
        },
        supportsSchemaFallback: false,
    },

    {
        id: "print",
        label: "Print",
        component: defineAsyncComponent(
            () => import("./print/PrintNoteType.vue"),
        ),
        emptyCustomData: () => ({
            target_note_id: 0,
            candidates: [],
        }),
        normalizeCustomData(raw, _note) {
            if (!raw || typeof raw !== "object") {
                return { target_note_id: 0, candidates: [] };
            }
            return {
                target_note_id: raw.target_note_id || 0,
                candidates: Array.isArray(raw.candidates) ? raw.candidates : [],
            };
        },
        supportsSchemaFallback: false,
        supportsActions: true,
    },
];

// ---------------------------------------------------------------------------
// Manifest cache (populated from GET /note-types)
// ---------------------------------------------------------------------------

/** Map from note type id → server Manifest */
const manifestCache = new Map();

/**
 * Fetch manifests from the server and merge into the local registry.
 * Must be called after login (requires a valid token).
 */
export async function fetchAndMergeManifests(token) {
    try {
        const manifests = await fetchNoteTypes(token);
        if (!Array.isArray(manifests)) return;
        for (const m of manifests) {
            manifestCache.set(m.id, m);
            // Merge manifest data into the registry entry if it exists
            const entry = registry.find((t) => t.id === m.id);
            if (entry) {
                // Update label from server if available
                if (m.label) entry.label = m.label;
                // Store manifest metadata
                entry.manifest = m;
                entry.editorMode = m.editor?.mode || "none";
                entry.editorSchema = m.editor?.schema || null;
                entry.viewerMode = m.viewer?.mode || "none";
                entry.actions = m.actions || [];
                entry.hasConfig = m.has_config || false;
                entry.hasView = m.has_view || false;
                entry.hasActions = m.has_actions || false;
                // Use server-provided default_config if available
                if (m.default_config != null) {
                    entry.emptyCustomData = () =>
                        JSON.parse(JSON.stringify(m.default_config));
                }
                // Set schema fallback support based on editor mode
                entry.supportsSchemaFallback = m.editor?.mode === "schema";
            }
        }
    } catch {
        // Silently ignore — registry falls back to hardcoded defaults.
    }
}

/**
 * Get the manifest for a given note type id (or null if not available).
 */
export function getManifest(id) {
    return manifestCache.get(id) || null;
}

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
 * Get the default child type for a given parent note type.
 * Returns the parent's defaultChildType if configured, otherwise "standard".
 */
export function getDefaultChildType(parentTypeId) {
    const entry = getNoteType(parentTypeId);
    return (entry && entry.defaultChildType) || "standard";
}

/**
 * Returns true if the note type uses lazy (on-demand) child loading.
 * Override cards like task/review overviews don't need children eagerly.
 */
export function isLazyChildren(typeId) {
    const entry = getNoteType(typeId);
    return !!(entry && entry.lazyChildren);
}

/**
 * The raw registry array.  Import only when you need to iterate everything.
 */
export { registry };
