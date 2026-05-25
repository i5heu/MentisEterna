<template>
    <div class="overview-dashboard">
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
                No recipe notes found. Create notes with type "recipe" first.
            </p>
            <div v-else class="recipe-select-list">
                <div
                    v-for="r in overviewData.recipes"
                    :key="r.note_id"
                    class="recipe-select-row"
                >
                    <img
                        v-if="r.thumbnail_url"
                        :src="r.thumbnail_url"
                        :alt="`${r.title} thumbnail`"
                        class="recipe-thumbnail"
                        loading="lazy"
                    />
                    <label class="recipe-checkbox-label">
                        <input
                            type="checkbox"
                            :value="r.note_id"
                            v-model="selectedRecipeIds"
                            class="recipe-checkbox"
                        />
                        <span class="recipe-select-title">{{ r.title }}</span>
                        <span class="recipe-select-count"
                            >{{ r.ingredient_count }} ingredients</span
                        >
                    </label>
                    <div class="recipe-row-right">
                        <span
                            v-if="r.in_recent_list"
                            class="recent-badge"
                            title="Used in a grocery list within the last 3 weeks"
                            >&#x1F552; Recent</span
                        >
                        <label
                            v-if="r.freezable && r.pre_cook_servings"
                            class="precook-checkbox-label"
                            :title="`Pre-cook ${r.pre_cook_servings} servings instead of scaling by people`"
                        >
                            <input
                                type="checkbox"
                                :checked="preCookRecipeIds.has(r.note_id)"
                                :disabled="
                                    !selectedRecipeIds.includes(r.note_id)
                                "
                                @change="
                                    preCookRecipeIds.has(r.note_id)
                                        ? preCookRecipeIds.delete(r.note_id)
                                        : preCookRecipeIds.add(r.note_id)
                                "
                                class="recipe-checkbox"
                            />
                            <span class="precook-label">Pre-cook</span>
                        </label>
                        <button
                            class="btn-ghost btn-sm"
                            @click="$emit('selectNote', r.note_id)"
                        >
                            View
                        </button>
                    </div>
                </div>
            </div>
        </div>

        <!-- Configuration -->
        <div class="config-section">
            <div class="config-row-inline">
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
                <span class="config-hint"
                    >Each recipe's serving size is scaled to fit (pre-cook
                    recipes use their own serving size)</span
                >
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
                        : `Generate Grocery List (${selectedRecipeIds.length} recipes${
                              preCookRecipeIds.size > 0
                                  ? `, ${preCookRecipeIds.size} pre-cook`
                                  : ""
                          } for ${configPeople} ${configPeople === 1 ? "person" : "people"})`
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
                <span class="list-meta">({{ latestList.num_people }}p)</span>
                <button
                    class="btn-ghost btn-sm btn-print"
                    :disabled="printingListId === latestList.id"
                    @click="printGroceryList(latestList.id)"
                >
                    {{ printingListId === latestList.id ? "..." : "🖨" }}
                </button>
            </h4>
            <div
                v-if="latestList.recipe_names && latestList.recipe_names.length"
                class="list-recipes"
            >
                Recipes: {{ latestList.recipe_names.join(", ") }}
            </div>
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
            <div v-for="gl in pastLists" :key="gl.id" class="past-list-card">
                <div class="past-list-header">
                    <span class="past-list-date">{{
                        formatDate(gl.generated_at)
                    }}</span>
                    <span class="past-list-config"
                        >{{ gl.num_people }}p &mdash;
                        {{ gl.items ? gl.items.length : 0 }} items</span
                    >
                    <span class="past-list-recipes">{{
                        gl.recipe_names
                            ? gl.recipe_names.join(", ")
                            : gl.recipe_ids
                              ? gl.recipe_ids.length + " recipes"
                              : "0 recipes"
                    }}</span>
                    <button
                        class="btn-ghost btn-sm btn-toggle"
                        @click="togglePastList(gl.id)"
                    >
                        {{ expandedLists.has(gl.id) ? "▾" : "▸" }}
                    </button>
                    <button
                        class="btn-ghost btn-sm btn-print"
                        :disabled="printingListId === gl.id"
                        @click="printGroceryList(gl.id)"
                    >
                        {{ printingListId === gl.id ? "..." : "🖨" }}
                    </button>
                    <button
                        class="btn-ghost btn-sm btn-delete"
                        :disabled="deletingListId === gl.id"
                        @click="deleteGroceryList(gl.id)"
                    >
                        {{ deletingListId === gl.id ? "..." : "✕" }}
                    </button>
                </div>
                <div v-if="expandedLists.has(gl.id)" class="past-list-items">
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
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { pluginActionV2 } from "../../api.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

const overviewData = ref(null);
const generatingList = ref(false);

const selectedRecipeIds = ref([]);
const preCookRecipeIds = ref(new Set());
const configDays = ref(8);
const configPeople = ref(1);
const expandedLists = ref(new Set());
const deletingListId = ref(null);
const printingListId = ref(null);

const latestList = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 0 ? lists[0] : null;
});

const pastLists = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 1 ? lists.slice(1) : [];
});

// Guard to break echo-back loop.
let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    overviewData.value = cd || { recipes: [], grocery_lists: [] };

    const lists = overviewData.value.grocery_lists || [];
    if (lists.length > 0 && selectedRecipeIds.value.length === 0) {
        const latest = lists[0];
        if (latest.recipe_ids && latest.recipe_ids.length > 0) {
            selectedRecipeIds.value = [...latest.recipe_ids];
            configDays.value = latest.num_days || 8;
            configPeople.value = latest.num_people || 1;
        }
    }
    hydrating = false;
}

// Hydrate when the note identity changes.
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also catch async customData arrival.
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (
            cd &&
            (!overviewData.value ||
                !overviewData.value.recipes ||
                overviewData.value.recipes.length === 0)
        ) {
            hydrateFromProp();
        }
    },
);

async function generateGroceryList() {
    generatingList.value = true;
    try {
        const result = await pluginActionV2(
            props.token,
            props.note.id,
            "generate_grocery_list",
            {
                recipe_ids: selectedRecipeIds.value,
                pre_cook_recipe_ids: [...preCookRecipeIds.value],
                num_days: configDays.value,
                num_people: configPeople.value,
            },
        );
        const gl = result.grocery_list;
        overviewData.value = {
            ...overviewData.value,
            grocery_lists: [gl, ...(overviewData.value.grocery_lists || [])],
        };
        emit("update:customData", { ...overviewData.value });
        preCookRecipeIds.value = new Set();
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
        await pluginActionV2(
            props.token,
            props.note.id,
            "delete_grocery_list",
            {
                list_id: listId,
            },
        );
        overviewData.value = {
            ...overviewData.value,
            grocery_lists:
                overviewData.value.grocery_lists?.filter(
                    (gl) => gl.id !== listId,
                ) || [],
        };
        expandedLists.value.delete(listId);
        emit("update:customData", { ...overviewData.value });
    } catch (e) {
        console.error("delete grocery list:", e);
    } finally {
        deletingListId.value = null;
    }
}

async function printGroceryList(listId) {
    printingListId.value = listId;
    try {
        await pluginActionV2(props.token, props.note.id, "print_grocery_list", {
            list_id: listId,
        });
    } catch (e) {
        console.error("print grocery list:", e);
    } finally {
        printingListId.value = null;
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
</script>

<style scoped>
.overview-dashboard {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.overview-dashboard h3 {
    font-size: 1.1rem;
    margin: 0.5rem 0 0.5rem;
    color: var(--font-color-secondary);
}

.recipe-selection-section {
    margin-bottom: 0.75rem;
}

.recipe-selection-section h4 {
    font-size: 0.95rem;
    margin-bottom: 0.4rem;
    color: var(--font-color-secondary);
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
    flex-wrap: wrap;
    padding: 0.45rem 0.6rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 6px;
}

.recipe-thumbnail {
    width: 7rem;
    height: 4.5rem;
    border-radius: 6px;
    object-fit: cover;
    flex-shrink: 0;
    border: 1px solid var(--border-color);
    background: var(--html-bg);
}

.recipe-checkbox-label {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 0.4rem;
    cursor: pointer;
}

.recipe-row-right {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-shrink: 0;
    margin-left: auto;
}

.recipe-checkbox {
    width: 1rem;
    height: 1rem;
    accent-color: var(--accent-teal);
    flex-shrink: 0;
}

.recipe-select-title {
    font-weight: 500;
}

.recipe-select-count {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.precook-checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    cursor: pointer;
}

.precook-label {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.recent-badge {
    font-size: 0.75rem;
    color: var(--tag-bg-color);
    background: rgba(255, 180, 0, 0.1);
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
}

.config-section {
    margin: 0.75rem 0;
}

.config-row-inline {
    display: flex;
    align-items: center;
    gap: 0.75rem;
}

.config-label-inline {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.9rem;
}

.config-input-num {
    width: 4rem;
    padding: 0.25rem 0.4rem;
    font-size: 0.9rem;
    text-align: center;
}

.config-input-num:focus {
    border-color: var(--accent-teal);
}

.generate-section {
    margin: 0.5rem 0 1rem;
}

.grocery-list-current {
    margin: 0.5rem 0;
}

.grocery-list-current h4 {
    font-size: 0.95rem;
    margin-bottom: 0.25rem;
    color: var(--font-color-secondary);
}

.list-recipes {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    margin-bottom: 0.35rem;
}

.list-meta {
    font-weight: 400;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.past-lists-section {
    margin-top: 1rem;
}

.past-lists-section h4 {
    font-size: 0.95rem;
    margin-bottom: 0.4rem;
    color: var(--font-color-secondary);
}

.past-list-card {
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 0.5rem;
    margin-bottom: 0.5rem;
    background: var(--raised-bg);
}

.past-list-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
}

.past-list-date {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color);
    white-space: nowrap;
}

.past-list-config {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    white-space: nowrap;
}

.past-list-recipes {
    flex: 1;
    font-size: 0.8rem;
    color: var(--font-color);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.btn-toggle {
    padding: 0.15rem 0.5rem;
    font-size: 0.9rem;
    flex-shrink: 0;
    margin-left: auto;
}

.btn-print {
    flex-shrink: 0;
    font-size: 0.95rem;
    margin: 0 1em;
}

.btn-delete {
    color: var(--heading-color);
    flex-shrink: 0;
}

.past-list-items {
    margin-top: 0.5rem;
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}

.ingredient-table {
    width: 100%;
    border-collapse: collapse;
}

.ingredient-table th,
.ingredient-table td {
    padding: 0.35rem 0.5rem;
    text-align: left;
    border-bottom: 1px solid var(--border-color);
}

.ingredient-table th {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.03em;
}

.config-hint {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}
</style>
