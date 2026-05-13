<template>
    <div v-if="note" class="note-type-renderer">
        <!-- Recipe type: ingredient table -->
        <div v-if="note.type === 'recipe'" class="recipe-editor">
            <h3>Ingredients</h3>
            <table class="ingredient-table">
                <thead>
                    <tr>
                        <th>Name</th>
                        <th>Amount</th>
                        <th>Unit</th>
                        <th v-if="editing"></th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="(ing, idx) in ingredients" :key="idx">
                        <td v-if="editing">
                            <input
                                v-model="ing.name"
                                placeholder="Ingredient name"
                            />
                        </td>
                        <td v-else>{{ ing.name || "-" }}</td>
                        <td v-if="editing">
                            <input
                                v-model="ing.amount"
                                placeholder="e.g. 2"
                                class="amount-input"
                            />
                        </td>
                        <td v-else>{{ ing.amount || "-" }}</td>
                        <td v-if="editing">
                            <input
                                v-model="ing.unit"
                                placeholder="e.g. cups"
                                class="unit-input"
                            />
                        </td>
                        <td v-else>{{ ing.unit || "-" }}</td>
                        <td v-if="editing">
                            <button
                                class="btn-ghost btn-sm"
                                @click="removeIngredient(idx)"
                            >
                                &times;
                            </button>
                        </td>
                    </tr>
                </tbody>
            </table>
            <button
                v-if="editing"
                class="btn-ghost btn-sm"
                @click="addIngredient"
            >
                + Add Ingredient
            </button>
            <p v-if="!editing && ingredients.length === 0" class="empty-hint">
                No ingredients yet. Switch to edit mode to add some.
            </p>
        </div>

        <!-- Recipe Overview type: dashboard with grocery list -->
        <div v-if="note.type === 'recipe_overview'" class="overview-dashboard">
            <h3>Recipe Overview</h3>

            <!-- Recipe selection list -->
            <div class="recipe-selection-section">
                <h4>Select Recipes</h4>
                <p
                    v-if="
                        !overviewData ||
                        !overviewData.recipes ||
                        !overviewData.recipes.length
                    "
                    class="empty-hint"
                >
                    No recipe notes found. Create notes with type "recipe"
                    first.
                </p>
                <div v-else class="recipe-select-list">
                    <div
                        v-for="r in overviewData.recipes"
                        :key="r.note_id"
                        class="recipe-select-row"
                    >
                        <label class="recipe-checkbox-label">
                            <input
                                type="checkbox"
                                :value="r.note_id"
                                v-model="selectedRecipeIds"
                                class="recipe-checkbox"
                            />
                            <span class="recipe-select-title">{{
                                r.title
                            }}</span>
                            <span class="recipe-select-count"
                                >{{ r.ingredient_count }} ingredients</span
                            >
                        </label>
                        <span
                            v-if="r.in_recent_list"
                            class="recent-badge"
                            title="Used in a grocery list within the last 3 weeks"
                            >🕒 Recent</span
                        >
                        <button
                            class="btn-ghost btn-sm"
                            @click="$emit('selectNote', r.note_id)"
                        >
                            View
                        </button>
                    </div>
                </div>
            </div>

            <!-- Configuration: days and people -->
            <div class="config-section">
                <div class="config-row-inline">
                    <label class="config-label-inline">
                        <span>Days:</span>
                        <input
                            type="number"
                            v-model.number="configDays"
                            min="1"
                            max="90"
                            class="config-input-num"
                        />
                    </label>
                    <label class="config-label-inline">
                        <span>People:</span>
                        <input
                            type="number"
                            v-model.number="configPeople"
                            min="1"
                            max="100"
                            class="config-input-num"
                        />
                    </label>
                </div>
            </div>

            <!-- Generate button -->
            <div class="generate-section">
                <button
                    class="btn-amber"
                    :disabled="generatingList || selectedRecipeIds.length === 0"
                    @click="generateGroceryList"
                >
                    {{
                        generatingList
                            ? "Generating..."
                            : `Generate Grocery List (${selectedRecipeIds.length} recipes → ${configDays} days × ${configPeople} people)`
                    }}
                </button>
                <p v-if="selectedRecipeIds.length === 0" class="config-hint">
                    Select at least one recipe above to generate a grocery list.
                </p>
            </div>

            <!-- Latest grocery list result -->
            <div
                v-if="latestList && latestList.items && latestList.items.length"
                class="grocery-list-current"
            >
                <h4>
                    Latest Grocery List
                    <span class="list-meta"
                        >({{ latestList.num_days }}d ×
                        {{ latestList.num_people }}p)</span
                    >
                </h4>
                <table class="ingredient-table">
                    <thead>
                        <tr>
                            <th>Item</th>
                            <th>Amount</th>
                            <th>Unit</th>
                        </tr>
                    </thead>
                    <tbody>
                        <tr v-for="(item, idx) in latestList.items" :key="idx">
                            <td>{{ item.name }}</td>
                            <td>{{ item.amount }}</td>
                            <td>{{ item.unit }}</td>
                        </tr>
                    </tbody>
                </table>
            </div>

            <!-- Past grocery lists -->
            <div v-if="pastLists.length > 0" class="past-lists-section">
                <h4>Past Grocery Lists</h4>
                <div
                    v-for="gl in pastLists"
                    :key="gl.id"
                    class="past-list-card"
                >
                    <div class="past-list-header">
                        <span class="past-list-date">{{
                            formatDate(gl.generated_at)
                        }}</span>
                        <span class="past-list-config"
                            >{{ gl.num_days }}d × {{ gl.num_people }}p —
                            {{ gl.items ? gl.items.length : 0 }} items</span
                        >
                        <span class="past-list-recipes"
                            >{{
                                gl.recipe_ids ? gl.recipe_ids.length : 0
                            }}
                            recipes</span
                        >
                        <button
                            class="btn-ghost btn-sm btn-toggle"
                            @click="togglePastList(gl.id)"
                        >
                            {{ expandedLists.has(gl.id) ? "▾" : "▸" }}
                        </button>
                        <button
                            class="btn-ghost btn-sm btn-delete"
                            :disabled="deletingListId === gl.id"
                            @click="deleteGroceryList(gl.id)"
                        >
                            {{ deletingListId === gl.id ? "..." : "✕" }}
                        </button>
                    </div>
                    <div
                        v-if="expandedLists.has(gl.id)"
                        class="past-list-items"
                    >
                        <table class="ingredient-table">
                            <thead>
                                <tr>
                                    <th>Item</th>
                                    <th>Amount</th>
                                    <th>Unit</th>
                                </tr>
                            </thead>
                            <tbody>
                                <tr v-for="(item, idx) in gl.items" :key="idx">
                                    <td>{{ item.name }}</td>
                                    <td>{{ item.amount }}</td>
                                    <td>{{ item.unit }}</td>
                                </tr>
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        </div>

        <!-- Example type: checklist with checkboxes -->
        <div v-if="note.type === 'example'" class="checklist-editor">
            <h3>Checklist</h3>
            <div
                v-for="(item, idx) in checklistItems"
                :key="idx"
                class="checklist-row"
            >
                <input
                    v-if="editing"
                    type="checkbox"
                    :checked="item.checked"
                    @change="item.checked = $event.target.checked"
                />
                <span v-else class="checklist-mark">{{
                    item.checked ? "☑" : "☐"
                }}</span>
                <input
                    v-if="editing"
                    v-model="item.label"
                    class="checklist-input"
                    placeholder="Item text"
                />
                <span v-else :class="{ 'checked-text': item.checked }">{{
                    item.label || "-"
                }}</span>
                <button
                    v-if="editing"
                    class="btn-ghost btn-sm"
                    @click="removeChecklistItem(idx)"
                >
                    &times;
                </button>
            </div>
            <button
                v-if="editing"
                class="btn-ghost btn-sm"
                @click="addChecklistItem"
            >
                + Add Item
            </button>
            <p
                v-if="!editing && checklistItems.length === 0"
                class="empty-hint"
            >
                No items yet. Switch to edit mode to add some.
            </p>
        </div>

        <!-- Index type: tag-based note index -->
        <div v-if="note.type === 'index'" class="index-view">
            <div v-if="editing" class="index-config">
                <label class="config-row">
                    <span class="config-label">Mode:</span>
                    <select
                        v-model="indexMode"
                        class="config-select"
                        @change="onIndexConfigChange"
                    >
                        <option value="global">Global (all notes)</option>
                        <option value="local">Local (this branch)</option>
                    </select>
                </label>
                <p class="config-hint">
                    Global shows tagged notes from the entire workspace. Local
                    shows only notes within the same parent and their
                    descendants.
                </p>
            </div>
            <div
                v-if="
                    indexData && indexData.entries && indexData.entries.length
                "
                class="index-entries"
            >
                <div
                    v-for="entry in indexData.entries"
                    :key="entry.tag"
                    class="index-entry"
                >
                    <div class="index-tag-header">
                        <span class="index-tag-name">🏷 {{ entry.tag }}</span>
                        <span class="index-tag-count"
                            >{{ entry.count }} note{{
                                entry.count !== 1 ? "s" : ""
                            }}</span
                        >
                    </div>
                    <div class="index-note-list">
                        <div
                            v-for="n in entry.notes"
                            :key="n.note_id"
                            class="index-note-card"
                            @click="$emit('selectNote', n.note_id)"
                        >
                            <span class="index-note-title">{{
                                n.title || "Untitled"
                            }}</span>
                            <span class="index-note-date">{{
                                n.created_at
                                    ? new Date(
                                          n.created_at,
                                      ).toLocaleDateString()
                                    : ""
                            }}</span>
                        </div>
                    </div>
                </div>
            </div>
            <p v-else class="empty-hint">
                No tags found.
                <template v-if="indexMode === 'local'">
                    Try switching to Global mode or tag some notes within this
                    branch.
                </template>
                <template v-else>
                    Add tags to your notes to see them indexed here.
                </template>
            </p>
        </div>

        <!-- Generic custom data form (fallback for other types) -->
        <div
            v-if="
                note.ui_schema &&
                note.type !== 'recipe' &&
                note.type !== 'recipe_overview' &&
                note.type !== 'example' &&
                note.type !== 'index'
            "
            class="custom-form"
        >
            <p class="type-label">
                Type: <strong>{{ note.type }}</strong>
            </p>
            <pre class="debug-json">{{
                JSON.stringify(note.custom_data, null, 2)
            }}</pre>
        </div>
    </div>
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { pluginAction } from "../api.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
});

const emit = defineEmits(["selectNote", "update:customData"]);

const ingredients = ref([]);
const overviewData = ref(null);
const generatingList = ref(false);
const checklistItems = ref([]);
const indexData = ref(null);
const indexMode = ref("global");

// Recipe overview state
const selectedRecipeIds = ref([]);
const configDays = ref(8);
const configPeople = ref(1);
const expandedLists = ref(new Set());
const deletingListId = ref(null);

// Computed: latest list and past lists (exclude the first element).
const latestList = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 0 ? lists[0] : null;
});

const pastLists = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 1 ? lists.slice(1) : [];
});

// Watch for note changes and initialize local state.
watch(
    () => props.note,
    (n) => {
        if (!n) return;
        if (n.type === "recipe") {
            // custom_data is { ingredients: [...] } from the backend.
            const ings = n.custom_data?.ingredients || n.custom_data;
            ingredients.value = Array.isArray(ings)
                ? ings.map((ing) => ({ ...ing }))
                : [];
        }
        if (n.type === "recipe_overview") {
            overviewData.value = n.custom_data || {
                recipes: [],
                grocery_lists: [],
            };
            // Pre-select recipes that were in the most recent list (if any).
            const lists = overviewData.value.grocery_lists || [];
            if (lists.length > 0 && selectedRecipeIds.value.length === 0) {
                const latest = lists[0];
                if (latest.recipe_ids && latest.recipe_ids.length > 0) {
                    selectedRecipeIds.value = [...latest.recipe_ids];
                    configDays.value = latest.num_days || 8;
                    configPeople.value = latest.num_people || 1;
                }
            }
        }
        if (n.type === "example") {
            // custom_data is { items: [...] } from the backend.
            const its = n.custom_data?.items || n.custom_data;
            checklistItems.value = Array.isArray(its)
                ? its.map((it) => ({
                      label: it.label || "",
                      checked: !!it.checked,
                  }))
                : [];
        }
        if (n.type === "index") {
            // custom_data is { mode, selected_tags, entries } from the backend.
            indexData.value = n.custom_data || { entries: [] };
            indexMode.value = indexData.value.mode || "global";
        }
    },
    { immediate: true },
);

// Emit custom data changes so the parent can save.
watch(
    ingredients,
    (val) => {
        emit("update:customData", {
            ingredients: val.map(({ name, amount, unit }) => ({
                name,
                amount,
                unit,
            })),
        });
    },
    { deep: true },
);

// Emit custom data changes for checklist items.
watch(
    checklistItems,
    (val) => {
        emit("update:customData", {
            items: val.map(({ label, checked }) => ({
                label,
                checked,
            })),
        });
    },
    { deep: true },
);

function addIngredient() {
    ingredients.value.push({ name: "", amount: "", unit: "" });
}

function removeIngredient(idx) {
    ingredients.value.splice(idx, 1);
}

function addChecklistItem() {
    checklistItems.value.push({ label: "", checked: false });
}

function removeChecklistItem(idx) {
    checklistItems.value.splice(idx, 1);
}

async function generateGroceryList() {
    generatingList.value = true;
    try {
        const result = await pluginAction(
            props.token,
            props.note.id,
            "generate_grocery_list",
            {
                recipe_ids: selectedRecipeIds.value,
                num_days: configDays.value,
                num_people: configPeople.value,
            },
        );
        const gl = result.grocery_list;
        // Prepend the new list to overviewData.
        overviewData.value = {
            ...overviewData.value,
            grocery_lists: [gl, ...(overviewData.value.grocery_lists || [])],
        };
    } catch (e) {
        console.error("generate grocery list:", e);
    } finally {
        generatingList.value = false;
    }
}

async function deleteGroceryList(listId) {
    if (!confirm("Delete this grocery list?")) return;
    deletingListId.value = listId;
    try {
        await pluginAction(props.token, props.note.id, "delete_grocery_list", {
            list_id: listId,
        });
        // Remove from local state.
        overviewData.value = {
            ...overviewData.value,
            grocery_lists:
                overviewData.value.grocery_lists?.filter(
                    (gl) => gl.id !== listId,
                ) || [],
        };
        expandedLists.value.delete(listId);
    } catch (e) {
        console.error("delete grocery list:", e);
    } finally {
        deletingListId.value = null;
    }
}

function togglePastList(listId) {
    const next = new Set(expandedLists.value);
    if (next.has(listId)) {
        next.delete(listId);
    } else {
        next.add(listId);
    }
    expandedLists.value = next;
}

function formatDate(iso) {
    if (!iso) return "";
    try {
        return new Date(iso).toLocaleString();
    } catch {
        return iso;
    }
}

function onIndexConfigChange() {
    emit("update:customData", {
        mode: indexMode.value,
        selected_tags: indexData.value?.selected_tags || [],
    });
}

// Emit custom data changes for index mode.
watch(indexMode, () => {
    if (props.note?.type === "index") {
        emit("update:customData", {
            mode: indexMode.value,
            selected_tags: indexData.value?.selected_tags || [],
        });
    }
});
</script>

<style scoped>
.note-type-renderer {
    margin-top: 1rem;
    border-top: 1px solid var(--border-color);
    padding-top: 1rem;
}

.note-type-renderer h3 {
    font-size: 1rem;
    color: var(--header-title-color);
    margin-bottom: 0.75rem;
}

.ingredient-table {
    width: 100%;
    border-collapse: collapse;
    margin-bottom: 0.5rem;
}

.ingredient-table th,
.ingredient-table td {
    padding: 0.4rem 0.5rem;
    text-align: left;
    border-bottom: 1px solid var(--border-color);
}

.ingredient-table th {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.05em;
}

.ingredient-table input {
    width: 100%;
    padding: 0.3rem 0.5rem;
    font-size: 0.85rem;
}

.amount-input {
    max-width: 80px;
}
.unit-input {
    max-width: 100px;
}

.btn-sm {
    padding: 0.25rem 0.5rem;
    font-size: 0.8rem;
}

.recipe-selection-section {
    margin-bottom: 1.25rem;
}

.recipe-selection-section h4 {
    font-size: 0.9rem;
    color: var(--accent-teal);
    margin-bottom: 0.5rem;
}

.recipe-select-list {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
}

.recipe-select-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.6rem;
    background: var(--raised-bg);
    border-radius: 6px;
    border: 1px solid var(--border-color);
}

.recipe-checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex: 1;
    cursor: pointer;
}

.recipe-checkbox {
    flex-shrink: 0;
    accent-color: var(--accent-teal);
}

.recipe-select-title {
    flex: 1;
    font-weight: 600;
    font-size: 0.9rem;
}

.recipe-select-count {
    color: var(--font-color-secondary);
    font-size: 0.8rem;
    white-space: nowrap;
}

.recent-badge {
    font-size: 0.7rem;
    background: var(--accent-amber-bg, #fff3cd);
    color: var(--accent-amber-text, #856404);
    padding: 0.15rem 0.4rem;
    border-radius: 4px;
    white-space: nowrap;
}

/* --- Recipe Overview new styles --- */

.config-section {
    margin-bottom: 1rem;
    padding: 0.6rem 0.75rem;
    background: var(--raised-bg);
    border-radius: 8px;
    border: 1px solid var(--border-color);
}

.config-row-inline {
    display: flex;
    align-items: center;
    gap: 1.5rem;
}

.config-label-inline {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.85rem;
    color: var(--font-color);
}

.config-input-num {
    width: 55px;
    padding: 0.25rem 0.4rem;
    font-size: 0.85rem;
    text-align: center;
    background: var(--input-bg, var(--html-bg));
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    font-family: inherit;
}

.config-input-num:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.generate-section {
    margin-bottom: 1.25rem;
}

.grocery-list-current {
    margin-bottom: 1.5rem;
}

.grocery-list-current h4 {
    font-size: 0.9rem;
    color: var(--accent-teal);
    margin-bottom: 0.5rem;
}

.list-meta {
    color: var(--font-color-secondary);
    font-weight: 400;
    font-size: 0.8rem;
}

.past-lists-section {
    margin-top: 1.5rem;
    padding-top: 1rem;
    border-top: 2px solid var(--border-color);
}

.past-lists-section h4 {
    font-size: 0.9rem;
    color: var(--header-title-color);
    margin-bottom: 0.75rem;
}

.past-list-card {
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    margin-bottom: 0.5rem;
    overflow: hidden;
}

.past-list-header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
}

.past-list-date {
    font-weight: 600;
    font-size: 0.85rem;
    color: var(--font-color);
}

.past-list-config {
    flex: 1;
    color: var(--font-color-secondary);
    font-size: 0.8rem;
}

.past-list-recipes {
    color: var(--font-color-secondary);
    font-size: 0.8rem;
}

.btn-toggle {
    color: var(--accent-teal);
    font-size: 1rem;
    padding: 0 0.4rem;
}

.btn-delete {
    color: var(--danger-color, #e74c3c);
}

.past-list-items {
    padding: 0 0.75rem 0.5rem 0.75rem;
}

.checklist-editor {
    margin-bottom: 1rem;
}

.checklist-editor h3 {
    font-size: 1rem;
    color: var(--header-title-color);
    margin-bottom: 0.75rem;
}

.checklist-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border-color);
}

.checklist-mark {
    font-size: 1.1rem;
    flex-shrink: 0;
}

.checklist-input {
    flex: 1;
}

.checked-text {
    text-decoration: line-through;
    color: var(--font-color-secondary);
}

.empty-hint {
    color: var(--font-color-secondary);
    font-size: 0.85rem;
    margin-bottom: 1rem;
}

.type-label {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    margin-bottom: 0.5rem;
}

.debug-json {
    background: var(--raised-bg);
    padding: 0.75rem;
    border-radius: 6px;
    font-size: 0.8rem;
    color: var(--pre-text-color);
    overflow-x: auto;
}

/* --- Index view --- */

.index-view {
    margin-bottom: 1rem;
}

.index-config {
    margin-bottom: 1rem;
    padding: 0.75rem;
    background: var(--raised-bg);
    border-radius: 8px;
    border: 1px solid var(--border-color);
}

.config-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
}

.config-label {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    white-space: nowrap;
}

.config-select {
    background: var(--input-bg, var(--html-bg));
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 0.3rem 0.5rem;
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
}

.config-select:focus {
    border-color: var(--accent-teal);
}

.config-hint {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    margin: 0;
}

.index-entries {
    display: flex;
    flex-direction: column;
    gap: 1rem;
}

.index-entry {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    overflow: hidden;
}

.index-tag-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.5rem 0.75rem;
    background: var(--raised-bg);
    border-bottom: 1px solid var(--border-color);
}

.index-tag-name {
    font-weight: 600;
    font-size: 0.9rem;
    color: var(--accent-teal);
}

.index-tag-count {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}

.index-note-list {
    display: flex;
    flex-direction: column;
}

.index-note-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.45rem 0.75rem;
    cursor: pointer;
    transition: background 0.1s;
    border-bottom: 1px solid var(--border-color);
}

.index-note-card:last-child {
    border-bottom: none;
}

.index-note-card:hover {
    background: var(--panel-bg);
}

.index-note-title {
    font-size: 0.85rem;
    font-weight: 500;
    color: var(--font-color);
}

.index-note-date {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}
</style>
