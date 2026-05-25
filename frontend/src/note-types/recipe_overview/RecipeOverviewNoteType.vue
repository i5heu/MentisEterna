<template>
    <div class="overview-dashboard">
        <h3>Recipe Overview</h3>

        <div class="overview-actions-row">
            <button
                v-if="unvalidIngredients.length > 0"
                class="btn-ghost btn-sm"
                @click="showUnvalidModal = true"
            >
                Unvalid Ingredients ({{ unvalidIngredients.length }})
            </button>
        </div>

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
                        <div class="recipe-select-main">
                            <span class="recipe-select-title">{{
                                r.title
                            }}</span>
                            <span class="recipe-rating"
                                >{{ formatRatingStars(r.rating) }}
                                {{ normalizeRating(r.rating) }}/10</span
                            >
                            <span class="recipe-select-count"
                                >{{ r.ingredient_count }} ingredients</span
                            >
                        </div>
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

        <div
            v-if="showUnvalidModal"
            class="recipe-modal-overlay"
            @click.self="showUnvalidModal = false"
        >
            <div class="recipe-modal">
                <div class="recipe-modal-header">
                    <h4>Unvalid Ingredients</h4>
                    <button
                        class="btn-ghost btn-sm"
                        @click="showUnvalidModal = false"
                    >
                        ✕
                    </button>
                </div>
                <p class="recipe-modal-hint">
                    This list contains ingredients from all recipes that are not
                    validated yet or do not have metric ingredients.
                </p>
                <ul class="unvalid-list">
                    <li
                        v-for="item in editableUnvalidIngredients"
                        :key="`${item.recipe_note_id}-${item.ingredient_id}`"
                        class="unvalid-item"
                    >
                        <div class="unvalid-top-row">
                            <div>
                                <div class="unvalid-recipe-title">
                                    {{ item.recipe_title }}
                                </div>
                                <div class="unvalid-ingredient-name">
                                    {{
                                        item.ingredient_name ||
                                        "Unnamed ingredient"
                                    }}
                                </div>
                            </div>
                            <button
                                class="btn-ghost btn-sm"
                                @click="
                                    viewRecipeFromModal(item.recipe_note_id)
                                "
                            >
                                View Recipe
                            </button>
                        </div>

                        <div class="unvalid-edit-grid">
                            <label class="unvalid-field">
                                <span>Metric Amount</span>
                                <input
                                    v-model="item.amount"
                                    class="unvalid-input"
                                />
                            </label>
                            <label class="unvalid-field">
                                <span>Metric Unit</span>
                                <select
                                    v-model="item.unit"
                                    class="unvalid-select"
                                >
                                    <option value="">—</option>
                                    <option value="mg">mg</option>
                                    <option value="g">g</option>
                                    <option value="kg">kg</option>
                                    <option value="ml">ml</option>
                                    <option value="l">l</option>
                                    <option value="pcs">pcs</option>
                                </select>
                            </label>
                            <label class="unvalid-field">
                                <span>Non-Metric Amount</span>
                                <input
                                    v-model="item.non_metric_amount"
                                    class="unvalid-input"
                                />
                            </label>
                            <label class="unvalid-field">
                                <span>Non-Metric Type</span>
                                <select
                                    v-model="item.non_metric_unit"
                                    class="unvalid-select"
                                >
                                    <option value="">—</option>
                                    <option value="teaspoon">Teaspoon</option>
                                    <option value="tablespoon">
                                        Tablespoon
                                    </option>
                                    <option value="cup">Cup</option>
                                </select>
                            </label>
                            <label
                                v-if="
                                    hasMetricValue(item) &&
                                    hasNonMetricValue(item)
                                "
                                class="unvalid-field unvalid-checkbox-field"
                            >
                                <span>Metric Validated</span>
                                <input
                                    v-model="item.metric_validated"
                                    type="checkbox"
                                    class="recipe-checkbox"
                                />
                            </label>
                        </div>

                        <div class="unvalid-values">
                            <span
                                v-if="
                                    item.non_metric_amount ||
                                    item.non_metric_unit
                                "
                            >
                                {{ item.non_metric_amount || "?" }}
                                {{ formatNonMetricUnit(item.non_metric_unit) }}
                            </span>
                            <span
                                v-if="
                                    (item.non_metric_amount ||
                                        item.non_metric_unit) &&
                                    (item.amount || item.unit)
                                "
                            >
                                →
                            </span>
                            <span>
                                {{ item.amount || "?" }} {{ item.unit || "?" }}
                            </span>
                        </div>
                        <div class="unvalid-actions-row">
                            <div class="unvalid-issue">
                                {{ formatIssueType(currentIssueType(item)) }}
                            </div>
                            <button
                                v-if="isDraftDirty(item) || isSavingDraft(item)"
                                class="btn-amber btn-sm"
                                :disabled="isSavingDraft(item)"
                                @click="saveUnvalidIngredient(item)"
                            >
                                {{ isSavingDraft(item) ? "Saving..." : "Save" }}
                            </button>
                        </div>
                    </li>
                </ul>
            </div>
        </div>
    </div>
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { fetchNote, pluginActionV2, updateNote } from "../../api.js";

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
const showUnvalidModal = ref(false);
const editableUnvalidIngredients = ref([]);
const savingIngredientIds = ref(new Set());

const latestList = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 0 ? lists[0] : null;
});

const pastLists = computed(() => {
    const lists = overviewData.value?.grocery_lists;
    return lists && lists.length > 1 ? lists.slice(1) : [];
});

const unvalidIngredients = computed(() => {
    const items = overviewData.value?.unvalid_ingredients;
    return Array.isArray(items) ? items : [];
});

let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    overviewData.value = cd || {
        recipes: [],
        grocery_lists: [],
        unvalid_ingredients: [],
    };

    const lists = overviewData.value.grocery_lists || [];
    if (lists.length > 0 && selectedRecipeIds.value.length === 0) {
        const latest = lists[0];
        if (latest.recipe_ids && latest.recipe_ids.length > 0) {
            selectedRecipeIds.value = [...latest.recipe_ids];
            configDays.value = latest.num_days || 8;
            configPeople.value = latest.num_people || 1;
        }
    }
    editableUnvalidIngredients.value = unvalidIngredients.value.map(
        createUnvalidIngredientDraft,
    );
    showUnvalidModal.value = false;
    hydrating = false;
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });

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

function normalizeRating(value) {
    const num = Number(value);
    if (!Number.isFinite(num)) return 0;
    return Math.max(0, Math.min(10, Math.round(num)));
}

function formatRatingStars(rating) {
    const safeRating = normalizeRating(rating);
    return "★".repeat(safeRating) + "☆".repeat(10 - safeRating);
}

function formatNonMetricUnit(unit) {
    switch (unit) {
        case "teaspoon":
            return "Teaspoon";
        case "tablespoon":
            return "Tablespoon";
        case "cup":
            return "Cup";
        default:
            return "";
    }
}

function formatIssueType(issueType) {
    switch (issueType) {
        case "missing_metric":
            return "Missing metric ingredient values";
        case "not_validated":
            return "Metric conversion not validated";
        default:
            return "Needs review";
    }
}

function normalizeString(value) {
    return String(value ?? "").trim();
}

function createUnvalidIngredientDraft(item) {
    const draft = {
        ...item,
        amount: item.amount || "",
        unit: item.unit || "",
        non_metric_amount: item.non_metric_amount || "",
        non_metric_unit: item.non_metric_unit || "",
        metric_validated: !!item.metric_validated,
    };
    draft._original = serializeUnvalidIngredientDraft(draft);
    return draft;
}

function hasMetricValue(draft) {
    return (
        normalizeString(draft.amount) !== "" &&
        normalizeString(draft.unit) !== ""
    );
}

function hasNonMetricValue(draft) {
    return (
        normalizeString(draft.non_metric_amount) !== "" &&
        normalizeString(draft.non_metric_unit) !== ""
    );
}

function effectiveMetricValidated(draft) {
    return hasMetricValue(draft) && hasNonMetricValue(draft)
        ? !!draft.metric_validated
        : false;
}

function currentIssueType(draft) {
    if (!hasMetricValue(draft)) return "missing_metric";
    if (hasNonMetricValue(draft) && !effectiveMetricValidated(draft)) {
        return "not_validated";
    }
    return "ok";
}

function serializeUnvalidIngredientDraft(draft) {
    return JSON.stringify({
        amount: normalizeString(draft.amount),
        unit: normalizeString(draft.unit),
        non_metric_amount: normalizeString(draft.non_metric_amount),
        non_metric_unit: normalizeString(draft.non_metric_unit),
        metric_validated: effectiveMetricValidated(draft),
    });
}

function isDraftDirty(draft) {
    return serializeUnvalidIngredientDraft(draft) !== draft._original;
}

function isSavingDraft(draft) {
    return savingIngredientIds.value.has(draft.ingredient_id);
}

async function refreshOverviewData() {
    const refreshed = await fetchNote(props.token, props.note.id);
    overviewData.value = {
        recipes: refreshed.plugin?.view?.recipes || [],
        grocery_lists: refreshed.plugin?.view?.grocery_lists || [],
        unvalid_ingredients: refreshed.plugin?.view?.unvalid_ingredients || [],
    };
    editableUnvalidIngredients.value = unvalidIngredients.value.map(
        createUnvalidIngredientDraft,
    );
    if (editableUnvalidIngredients.value.length === 0) {
        showUnvalidModal.value = false;
    }
}

async function saveUnvalidIngredient(draft) {
    const nextSaving = new Set(savingIngredientIds.value);
    nextSaving.add(draft.ingredient_id);
    savingIngredientIds.value = nextSaving;
    try {
        const recipeNote = await fetchNote(props.token, draft.recipe_note_id);
        const customData = JSON.parse(
            JSON.stringify(
                recipeNote.plugin?.config || recipeNote.custom_data || {},
            ),
        );
        const ingredients = Array.isArray(customData.ingredients)
            ? customData.ingredients
            : [];
        const updatedIngredients = ingredients.map((ingredient) => {
            if (ingredient.id !== draft.ingredient_id) return ingredient;
            return {
                ...ingredient,
                amount: normalizeString(draft.amount),
                unit: normalizeString(draft.unit),
                non_metric_amount: normalizeString(draft.non_metric_amount),
                non_metric_unit: normalizeString(draft.non_metric_unit),
                metric_validated: effectiveMetricValidated(draft),
            };
        });

        await updateNote(
            props.token,
            recipeNote.id,
            recipeNote.title || "",
            recipeNote.body || "",
            recipeNote.parent_id,
            recipeNote.type || "recipe",
            {
                ...customData,
                ingredients: updatedIngredients,
            },
            recipeNote.tags || [],
        );

        await refreshOverviewData();
    } catch (e) {
        console.error("save unvalid ingredient:", e);
    } finally {
        const next = new Set(savingIngredientIds.value);
        next.delete(draft.ingredient_id);
        savingIngredientIds.value = next;
    }
}

function viewRecipeFromModal(recipeNoteId) {
    showUnvalidModal.value = false;
    emit("selectNote", recipeNoteId);
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

.overview-actions-row {
    display: flex;
    justify-content: flex-end;
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

.recipe-select-main {
    display: flex;
    flex-direction: column;
    min-width: 0;
    gap: 0.1rem;
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

.recipe-rating {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    letter-spacing: 0.02em;
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

.recipe-modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(1, 16, 31, 0.75);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
}

.recipe-modal {
    width: min(55rem, 92vw);
    max-height: 80vh;
    overflow-y: auto;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 10px;
    padding: 1rem 1.25rem;
}

.recipe-modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.75rem;
}

.recipe-modal-header h4 {
    margin: 0;
    color: var(--header-title-color);
}

.recipe-modal-hint {
    margin: 0 0 1rem;
    color: var(--font-color-secondary);
    font-size: 0.9rem;
}

.unvalid-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
}

.unvalid-item {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.7rem 0.8rem;
    background: var(--html-bg);
}

.unvalid-top-row {
    display: flex;
    align-items: start;
    justify-content: space-between;
    gap: 1rem;
}

.unvalid-recipe-title {
    font-weight: 600;
    margin-bottom: 0.15rem;
}

.unvalid-ingredient-name {
    color: var(--font-color-secondary);
    font-size: 0.9rem;
}

.unvalid-edit-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
    gap: 0.75rem;
    margin-top: 0.75rem;
}

.unvalid-field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.unvalid-checkbox-field {
    justify-content: end;
}

.unvalid-input,
.unvalid-select {
    width: 100%;
    padding: 0.35rem 0.45rem;
    font-size: 0.9rem;
}

.unvalid-values {
    margin-top: 0.45rem;
    font-size: 0.92rem;
}

.unvalid-actions-row {
    margin-top: 0.55rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
}

.unvalid-issue {
    color: var(--heading-color);
    font-size: 0.85rem;
}
</style>
