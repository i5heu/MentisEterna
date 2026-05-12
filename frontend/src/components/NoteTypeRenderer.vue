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
            <h3>All Recipes</h3>
            <div
                v-if="
                    overviewData &&
                    overviewData.recipes &&
                    overviewData.recipes.length
                "
                class="recipe-list"
            >
                <div
                    v-for="r in overviewData.recipes"
                    :key="r.note_id"
                    class="recipe-card"
                >
                    <span class="recipe-title">{{ r.title }}</span>
                    <span class="recipe-count"
                        >{{ r.ingredient_count }} ingredients</span
                    >
                    <button
                        class="btn-ghost btn-sm"
                        @click="$emit('selectNote', r.note_id)"
                    >
                        View
                    </button>
                </div>
            </div>
            <p v-else class="empty-hint">
                No recipe notes found. Create notes with type "recipe" first.
            </p>

            <div class="grocery-section">
                <button
                    class="btn-amber"
                    :disabled="generatingList"
                    @click="generateGroceryList"
                >
                    {{
                        generatingList
                            ? "Generating..."
                            : "Generate Grocery List (8 days)"
                    }}
                </button>

                <div
                    v-if="
                        overviewData &&
                        overviewData.grocery_items &&
                        overviewData.grocery_items.length
                    "
                    class="grocery-list"
                >
                    <h4>Grocery List</h4>
                    <table class="ingredient-table">
                        <thead>
                            <tr>
                                <th>Item</th>
                                <th>Amount</th>
                                <th>Unit</th>
                            </tr>
                        </thead>
                        <tbody>
                            <tr
                                v-for="(
                                    item, idx
                                ) in overviewData.grocery_items"
                                :key="idx"
                            >
                                <td>{{ item.name }}</td>
                                <td>{{ item.amount }}</td>
                                <td>{{ item.unit }}</td>
                            </tr>
                        </tbody>
                    </table>
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
import { ref, watch } from "vue";
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
                grocery_items: [],
            };
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
            null,
        );
        overviewData.value = {
            ...overviewData.value,
            grocery_items: result.items || [],
        };
    } catch (e) {
        console.error("generate grocery list:", e);
    } finally {
        generatingList.value = false;
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

.recipe-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    margin-bottom: 1.5rem;
}

.recipe-card {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.6rem 0.75rem;
    background: var(--raised-bg);
    border-radius: 6px;
    border: 1px solid var(--border-color);
}

.recipe-title {
    flex: 1;
    font-weight: 600;
}

.recipe-count {
    color: var(--font-color-secondary);
    font-size: 0.85rem;
}

.grocery-section {
    margin-top: 1.5rem;
}

.grocery-list {
    margin-top: 1rem;
}

.grocery-list h4 {
    font-size: 0.9rem;
    color: var(--accent-teal);
    margin-bottom: 0.5rem;
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
