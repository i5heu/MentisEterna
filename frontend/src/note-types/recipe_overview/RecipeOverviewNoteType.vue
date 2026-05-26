<template>
    <div class="overview-dashboard">
        <h3>Recipe Overview</h3>

        <div class="overview-actions-row">
            <div class="overview-actions-group">
                <button
                    v-if="pantryRecipe"
                    class="btn-ghost btn-sm"
                    :disabled="creatingPantryRecipe || printingPantry"
                    @click="printPantryStaples"
                >
                    {{
                        printingPantry
                            ? "Printing Pantry..."
                            : "Print Pantry Staples"
                    }}
                </button>
                <button
                    v-if="pantryRecipe"
                    class="btn-ghost btn-sm"
                    @click="$emit('selectNote', pantryRecipe.note_id)"
                >
                    View Pantry Staples
                </button>
                <button
                    v-if="pantryRecipe"
                    class="btn-ghost btn-sm"
                    :disabled="
                        !latestList || loadingPantry || savingCurrentList
                    "
                    @click="openPantryModal"
                >
                    Add Pantry Staples
                </button>
                <button
                    v-else
                    class="btn-amber btn-sm"
                    :disabled="creatingPantryRecipe"
                    @click="createPantryStaplesRecipe"
                >
                    {{
                        creatingPantryRecipe
                            ? "Creating Pantry Staples..."
                            : "Create Pantry Staples Recipe"
                    }}
                </button>
                <button
                    v-if="unvalidIngredients.length > 0"
                    class="btn-ghost btn-sm"
                    @click="showUnvalidModal = true"
                >
                    Unvalid Ingredients ({{ unvalidIngredients.length }})
                </button>
            </div>
        </div>

        <div
            v-if="actionErrorMessage || actionPreviewText"
            class="action-feedback"
        >
            <p v-if="actionErrorMessage" class="action-error">
                {{ actionErrorMessage }}
            </p>
            <div v-if="actionPreviewText" class="action-preview">
                <div class="action-preview-title">Printer Preview</div>
                <pre class="action-preview-box">{{ actionPreviewText }}</pre>
            </div>
        </div>

        <!-- Recipe selection list -->
        <div class="recipe-selection-section">
            <h4>Select Recipes</h4>
            <p v-if="!selectableRecipes.length" class="empty-hint">
                No recipe notes found. Create notes with type "recipe" first.
            </p>
            <div v-else class="recipe-select-list">
                <div
                    v-for="r in selectableRecipes"
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
                                @change="togglePreCookRecipe(r.note_id)"
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
        <div v-if="latestList" class="grocery-list-current">
            <div class="current-list-header">
                <h4>
                    Latest Grocery List
                    <span class="list-meta"
                        >({{ latestList.num_people }}p)</span
                    >
                </h4>
                <div class="current-list-actions">
                    <button
                        class="btn-ghost btn-sm btn-print"
                        :disabled="
                            printingListId === latestList.id ||
                            savingCurrentList ||
                            generatingList
                        "
                        @click="printGroceryList(latestList.id)"
                    >
                        {{ printingListId === latestList.id ? "..." : "🖨" }}
                    </button>
                    <button
                        class="btn-ghost btn-sm"
                        :disabled="savingCurrentList"
                        @click="addCurrentListItem"
                    >
                        Add Item
                    </button>
                    <button
                        v-if="currentListDirty"
                        class="btn-ghost btn-sm"
                        :disabled="savingCurrentList"
                        @click="hydrateCurrentListDraft"
                    >
                        Reset
                    </button>
                    <button
                        class="btn-amber btn-sm"
                        :disabled="savingCurrentList || !currentListDirty"
                        @click="saveCurrentGroceryList()"
                    >
                        {{ savingCurrentList ? "Saving..." : "Save Changes" }}
                    </button>
                </div>
            </div>
            <div
                v-if="latestList.recipe_names && latestList.recipe_names.length"
                class="list-recipes"
            >
                Recipes: {{ latestList.recipe_names.join(", ") }}
            </div>
            <p v-if="currentListDirty" class="current-list-hint">
                Save your changes before printing so the printer uses the latest
                grocery list.
            </p>
            <table class="ingredient-table ingredient-table-editable">
                <thead>
                    <tr>
                        <th>Item</th>
                        <th>Amount</th>
                        <th>Unit</th>
                        <th class="item-actions-col">Actions</th>
                    </tr>
                </thead>
                <tbody>
                    <template
                        v-for="group in groupedEditableLatestListItems"
                        :key="`current-${group.category}`"
                    >
                        <tr class="category-group-row">
                            <td colspan="4">
                                {{ formatGroceryCategoryLabel(group.category) }}
                            </td>
                        </tr>
                        <tr
                            v-for="entry in group.items"
                            :key="`${latestList.id}-${entry.index}`"
                        >
                            <td>
                                <input
                                    v-model="entry.item.name"
                                    class="grocery-input"
                                    placeholder="Item name"
                                />
                            </td>
                            <td>
                                <input
                                    v-model="entry.item.amount"
                                    class="grocery-input"
                                    placeholder="Amount"
                                />
                            </td>
                            <td>
                                <input
                                    v-model="entry.item.unit"
                                    class="grocery-input"
                                    placeholder="Unit"
                                />
                            </td>
                            <td class="item-row-actions">
                                <button
                                    class="btn-ghost btn-sm btn-delete-row"
                                    :disabled="savingCurrentList"
                                    @click="removeCurrentListItem(entry.index)"
                                >
                                    ✕
                                </button>
                            </td>
                        </tr>
                    </template>
                    <tr v-if="editableLatestListItems.length === 0">
                        <td colspan="4" class="empty-table-cell">
                            No items yet. Add items manually or from Pantry
                            Staples.
                        </td>
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
                            <template
                                v-for="group in groupPastListItems(gl.items)"
                                :key="`${gl.id}-${group.category}`"
                            >
                                <tr class="category-group-row">
                                    <td colspan="3">
                                        {{
                                            formatGroceryCategoryLabel(
                                                group.category,
                                            )
                                        }}
                                    </td>
                                </tr>
                                <tr
                                    v-for="(item, idx) in group.items"
                                    :key="`${gl.id}-${group.category}-${idx}`"
                                >
                                    <td>{{ item.name }}</td>
                                    <td>{{ item.amount }}</td>
                                    <td>{{ item.unit }}</td>
                                </tr>
                            </template>
                        </tbody>
                    </table>
                </div>
            </div>
        </div>

        <div
            v-if="showPantryModal"
            class="recipe-modal-overlay"
            @click.self="closePantryModal"
        >
            <div class="recipe-modal pantry-modal">
                <div class="recipe-modal-header">
                    <div>
                        <h4>Pantry Staples</h4>
                        <p class="recipe-modal-hint pantry-modal-hint">
                            Select the staples that are missing and add them to
                            the current grocery list.
                        </p>
                    </div>
                    <button class="btn-ghost btn-sm" @click="closePantryModal">
                        ✕
                    </button>
                </div>

                <p v-if="!latestList" class="recipe-modal-hint">
                    Generate a grocery list first so Pantry Staples can be added
                    to it.
                </p>
                <p v-else-if="loadingPantry" class="empty-hint">
                    Loading Pantry Staples...
                </p>
                <p v-else-if="!pantryIngredientRows.length" class="empty-hint">
                    The Pantry Staples recipe has no ingredients yet. Open the
                    recipe and add some staples first.
                </p>
                <div v-else class="pantry-list">
                    <label
                        v-for="(item, idx) in pantryIngredientRows"
                        :key="pantryIngredientSelectionKey(item, idx)"
                        class="pantry-item"
                    >
                        <input
                            type="checkbox"
                            :value="pantryIngredientSelectionKey(item, idx)"
                            v-model="selectedPantryIngredientIds"
                            class="recipe-checkbox pantry-item-checkbox"
                        />
                        <div class="pantry-item-main">
                            <div class="pantry-item-name">{{ item.name }}</div>
                            <div
                                v-if="item.prepare"
                                class="pantry-item-prepare"
                            >
                                {{ item.prepare }}
                            </div>
                            <div class="pantry-item-meta">
                                {{ formatGroceryItemLine(item) }}
                            </div>
                        </div>
                    </label>
                </div>

                <div class="pantry-modal-actions">
                    <button class="btn-ghost btn-sm" @click="closePantryModal">
                        Cancel
                    </button>
                    <button
                        class="btn-amber btn-sm"
                        :disabled="
                            !latestList ||
                            !selectedPantryIngredientIds.length ||
                            savingCurrentList ||
                            loadingPantry
                        "
                        @click="addSelectedPantryItemsToCurrentList"
                    >
                        {{
                            savingCurrentList
                                ? "Adding..."
                                : `Add ${selectedPantryIngredientIds.length} Item${selectedPantryIngredientIds.length === 1 ? "" : "s"}`
                        }}
                    </button>
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
import {
    createNote,
    fetchNote,
    pluginActionV2,
    updateNote,
} from "../../api.js";

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
const printingPantry = ref(false);
const creatingPantryRecipe = ref(false);
const showUnvalidModal = ref(false);
const editableUnvalidIngredients = ref([]);
const savingIngredientIds = ref(new Set());

const editableLatestListItems = ref([]);
const savingCurrentList = ref(false);
const showPantryModal = ref(false);
const loadingPantry = ref(false);
const pantryRecipeDetail = ref(null);
const selectedPantryIngredientIds = ref([]);
const actionErrorMessage = ref("");
const actionPreviewText = ref("");

const GROCERY_CATEGORY_ORDER = [
    "vegetables",
    "fruit",
    "meat",
    "dairy",
    "fish",
    "chilled & deli",
    "frozen",
    "spices",
    "beverages",
    "household",
    "other",
];

const allRecipes = computed(() => {
    const recipes = overviewData.value?.recipes;
    return Array.isArray(recipes) ? recipes : [];
});

const pantryRecipe = computed(() => {
    return allRecipes.value.find((recipe) => isPantryRecipe(recipe)) || null;
});

const selectableRecipes = computed(() => {
    return allRecipes.value.filter((recipe) => !isPantryRecipe(recipe));
});

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

const pantryIngredientRows = computed(() => {
    const rawIngredients =
        pantryRecipeDetail.value?.plugin?.config?.ingredients ||
        pantryRecipeDetail.value?.custom_data?.ingredients ||
        [];
    if (!Array.isArray(rawIngredients)) return [];
    return rawIngredients.filter((item) => normalizeString(item?.name) !== "");
});

const currentListDirty = computed(() => {
    const savedItems = latestList.value?.items || [];
    return (
        serializeGroceryItems(editableLatestListItems.value) !==
        serializeGroceryItems(savedItems)
    );
});

const groupedEditableLatestListItems = computed(() =>
    groupEditableGroceryItems(editableLatestListItems.value),
);

let hydrating = false;

function clearActionFeedback() {
    actionErrorMessage.value = "";
    actionPreviewText.value = "";
}

function isPantryRecipe(recipe) {
    return recipe?.is_pantry === true;
}

function createEmptyOverviewData() {
    return {
        recipes: [],
        grocery_lists: [],
        unvalid_ingredients: [],
    };
}

function createEmptyRecipePayload() {
    return {
        ingredients: [],
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

function normalizeGroceryCategory(value) {
    const normalized = normalizeString(value).toLowerCase();
    return GROCERY_CATEGORY_ORDER.includes(normalized) ? normalized : "other";
}

function groceryCategorySortIndex(category) {
    return GROCERY_CATEGORY_ORDER.indexOf(normalizeGroceryCategory(category));
}

function formatGroceryCategoryLabel(category) {
    switch (normalizeGroceryCategory(category)) {
        case "vegetables":
            return "Vegetables";
        case "fruit":
            return "Fruit";
        case "meat":
            return "Meat";
        case "dairy":
            return "Dairy";
        case "fish":
            return "Fish";
        case "chilled & deli":
            return "Chilled & Deli";
        case "frozen":
            return "Frozen";
        case "spices":
            return "Spices";
        case "beverages":
            return "Beverages";
        case "household":
            return "Household";
        default:
            return "Other";
    }
}

function compareGroceryItems(left, right) {
    const categoryDiff =
        groceryCategorySortIndex(left?.category) -
        groceryCategorySortIndex(right?.category);
    if (categoryDiff !== 0) return categoryDiff;

    const leftName = normalizeString(left?.name).toLowerCase();
    const rightName = normalizeString(right?.name).toLowerCase();
    if (leftName !== rightName) return leftName.localeCompare(rightName);

    const leftUnit = normalizeString(left?.unit).toLowerCase();
    const rightUnit = normalizeString(right?.unit).toLowerCase();
    if (leftUnit !== rightUnit) return leftUnit.localeCompare(rightUnit);

    return normalizeString(left?.amount)
        .toLowerCase()
        .localeCompare(normalizeString(right?.amount).toLowerCase());
}

function groupEditableGroceryItems(items) {
    const groups = [];
    let currentGroup = null;
    for (const [index, item] of (Array.isArray(items) ? items : []).entries()) {
        const category = normalizeGroceryCategory(item?.category);
        if (!currentGroup || currentGroup.category !== category) {
            currentGroup = { category, items: [] };
            groups.push(currentGroup);
        }
        currentGroup.items.push({ item, index });
    }
    return groups;
}

function groupPastListItems(items) {
    const grouped = [];
    let currentGroup = null;
    for (const item of cloneGroceryItems(items).sort(compareGroceryItems)) {
        const category = normalizeGroceryCategory(item?.category);
        if (!currentGroup || currentGroup.category !== category) {
            currentGroup = { category, items: [] };
            grouped.push(currentGroup);
        }
        currentGroup.items.push(item);
    }
    return grouped;
}

function createEmptyGroceryItem() {
    return {
        category: "other",
        name: "",
        amount: "",
        unit: "",
    };
}

function cloneGroceryItems(items) {
    return Array.isArray(items)
        ? items.map((item) => ({
              category: normalizeGroceryCategory(item?.category),
              name: item?.name || "",
              amount: item?.amount || "",
              unit: item?.unit || "",
          }))
        : [];
}

function serializeGroceryItems(items) {
    return JSON.stringify(
        (Array.isArray(items) ? items : [])
            .map((item) => ({
                category: normalizeGroceryCategory(item?.category),
                name: normalizeString(item?.name),
                amount: normalizeString(item?.amount),
                unit: normalizeString(item?.unit),
            }))
            .filter(
                (item) =>
                    item.name !== "" || item.amount !== "" || item.unit !== "",
            ),
    );
}

function normalizeString(value) {
    return String(value ?? "").trim();
}

function parseNumericAmount(value) {
    const trimmed = normalizeString(value);
    if (!/^\d+(?:[.,]\d+)?$/.test(trimmed)) {
        return null;
    }
    const parsed = Number.parseFloat(trimmed.replace(",", "."));
    return Number.isFinite(parsed) ? parsed : null;
}

function formatNumericAmount(value) {
    if (!Number.isFinite(value)) return "";
    return String(Number(value.toFixed(6)));
}

function canonicalMetricAmountPair(amount, unit) {
    const numericAmount = parseNumericAmount(amount);
    const normalizedUnit = normalizeString(unit);
    if (numericAmount == null || normalizedUnit === "") {
        return null;
    }

    switch (normalizedUnit) {
        case "mg":
            return { amount: numericAmount, unit: "mg" };
        case "g":
            return { amount: numericAmount * 1000, unit: "mg" };
        case "kg":
            return { amount: numericAmount * 1000 * 1000, unit: "mg" };
        case "ml":
            return { amount: numericAmount, unit: "ml" };
        case "l":
            return { amount: numericAmount * 1000, unit: "ml" };
        default:
            return null;
    }
}

function normalizeMetricAmountPair(amount, unit) {
    const numericAmount = parseNumericAmount(amount);
    const normalizedUnit = normalizeString(unit);
    if (numericAmount == null || normalizedUnit === "") {
        return {
            amount: normalizeString(amount),
            unit: normalizedUnit,
        };
    }

    switch (normalizedUnit) {
        case "mg":
            if (numericAmount >= 1000 * 1000) {
                return {
                    amount: formatNumericAmount(numericAmount / (1000 * 1000)),
                    unit: "kg",
                };
            }
            if (numericAmount >= 1000) {
                return {
                    amount: formatNumericAmount(numericAmount / 1000),
                    unit: "g",
                };
            }
            return {
                amount: formatNumericAmount(numericAmount),
                unit: "mg",
            };
        case "g":
            if (numericAmount >= 1000) {
                return {
                    amount: formatNumericAmount(numericAmount / 1000),
                    unit: "kg",
                };
            }
            if (numericAmount > 0 && numericAmount < 1) {
                return {
                    amount: formatNumericAmount(numericAmount * 1000),
                    unit: "mg",
                };
            }
            return {
                amount: formatNumericAmount(numericAmount),
                unit: "g",
            };
        case "kg":
            if (numericAmount > 0 && numericAmount < 1) {
                return {
                    amount: formatNumericAmount(numericAmount * 1000),
                    unit: "g",
                };
            }
            return {
                amount: formatNumericAmount(numericAmount),
                unit: "kg",
            };
        case "ml":
            if (numericAmount >= 1000) {
                return {
                    amount: formatNumericAmount(numericAmount / 1000),
                    unit: "l",
                };
            }
            return {
                amount: formatNumericAmount(numericAmount),
                unit: "ml",
            };
        case "l":
            if (numericAmount > 0 && numericAmount < 1) {
                return {
                    amount: formatNumericAmount(numericAmount * 1000),
                    unit: "ml",
                };
            }
            return {
                amount: formatNumericAmount(numericAmount),
                unit: "l",
            };
        default:
            return {
                amount: formatNumericAmount(numericAmount),
                unit: normalizedUnit,
            };
    }
}

function hasMetricIngredientValue(item) {
    return (
        normalizeString(item?.amount) !== "" &&
        normalizeString(item?.unit) !== ""
    );
}

function hasNonMetricIngredientValue(item) {
    return (
        normalizeString(item?.non_metric_amount) !== "" &&
        normalizeString(item?.non_metric_unit) !== ""
    );
}

function effectivePantryGroceryItem(item) {
    const name = normalizeString(item?.name);
    const metricAmount = normalizeString(item?.amount);
    const metricUnit = normalizeString(item?.unit);
    const nonMetricAmount = normalizeString(item?.non_metric_amount);
    const nonMetricUnit = normalizeString(item?.non_metric_unit);
    const metricValidated = !!item?.metric_validated;
    const category = normalizeGroceryCategory(
        item?.grocery_category || item?.category,
    );

    if (
        hasMetricIngredientValue(item) &&
        (!hasNonMetricIngredientValue(item) || metricValidated)
    ) {
        const normalizedMetric = normalizeMetricAmountPair(
            metricAmount,
            metricUnit,
        );
        return {
            category,
            name,
            amount: normalizedMetric.amount,
            unit: normalizedMetric.unit,
        };
    }

    if (hasNonMetricIngredientValue(item)) {
        return {
            category,
            name,
            amount: nonMetricAmount,
            unit: nonMetricUnit,
        };
    }

    return {
        category,
        name,
        amount: metricAmount,
        unit: metricUnit,
    };
}

function mergeAmountStrings(existingAmount, nextAmount) {
    const left = parseNumericAmount(existingAmount);
    const right = parseNumericAmount(nextAmount);
    if (left != null && right != null) {
        return formatNumericAmount(left + right);
    }

    const existing = normalizeString(existingAmount);
    const next = normalizeString(nextAmount);
    if (existing && next) {
        return `${existing} + ${next}`;
    }
    return next || existing;
}

function mergeGroceryItems(baseItems, additions) {
    const nextItems = cloneGroceryItems(baseItems);

    for (const rawAddition of additions) {
        const addition = {
            category: normalizeGroceryCategory(rawAddition?.category),
            name: normalizeString(rawAddition?.name),
            amount: normalizeString(rawAddition?.amount),
            unit: normalizeString(rawAddition?.unit),
        };
        if (!addition.name) continue;

        const additionCanonical = canonicalMetricAmountPair(
            addition.amount,
            addition.unit,
        );
        const existingIndex = nextItems.findIndex((item) => {
            const sameCategory =
                normalizeGroceryCategory(item.category) === addition.category;
            if (!sameCategory) return false;

            const sameName =
                normalizeString(item.name).toLowerCase() ===
                addition.name.toLowerCase();
            if (!sameName) return false;

            const sameUnit =
                normalizeString(item.unit).toLowerCase() ===
                addition.unit.toLowerCase();
            if (sameUnit) return true;

            const existingCanonical = canonicalMetricAmountPair(
                item.amount,
                item.unit,
            );
            return (
                additionCanonical != null &&
                existingCanonical != null &&
                additionCanonical.unit === existingCanonical.unit
            );
        });

        if (existingIndex >= 0) {
            const existingItem = nextItems[existingIndex];
            const existingCanonical = canonicalMetricAmountPair(
                existingItem.amount,
                existingItem.unit,
            );
            if (
                additionCanonical != null &&
                existingCanonical != null &&
                additionCanonical.unit === existingCanonical.unit
            ) {
                const normalizedMetric = normalizeMetricAmountPair(
                    formatNumericAmount(
                        existingCanonical.amount + additionCanonical.amount,
                    ),
                    additionCanonical.unit,
                );
                nextItems[existingIndex] = {
                    ...existingItem,
                    category: addition.category,
                    amount: normalizedMetric.amount,
                    unit: normalizedMetric.unit,
                };
            } else {
                nextItems[existingIndex] = {
                    ...existingItem,
                    category: addition.category,
                    amount: mergeAmountStrings(
                        existingItem.amount,
                        addition.amount,
                    ),
                    unit: normalizeString(existingItem.unit) || addition.unit,
                };
            }
            continue;
        }

        nextItems.push(addition);
    }

    return nextItems.sort(compareGroceryItems);
}

function sanitizeGroceryItemsForSave(items) {
    const sanitized = [];
    for (const item of Array.isArray(items) ? items : []) {
        const normalized = {
            category: normalizeGroceryCategory(item?.category),
            name: normalizeString(item?.name),
            amount: normalizeString(item?.amount),
            unit: normalizeString(item?.unit),
        };
        if (!normalized.name) {
            if (!normalized.amount && !normalized.unit) continue;
            throw new Error("Each grocery list item needs a name.");
        }
        sanitized.push(normalized);
    }
    return sanitized.sort(compareGroceryItems);
}

function hydrateCurrentListDraft() {
    editableLatestListItems.value = cloneGroceryItems(latestList.value?.items);
}

function hydrateFromProp() {
    hydrating = true;
    clearActionFeedback();
    const cd = props.customData;
    overviewData.value =
        cd && typeof cd === "object" ? cd : createEmptyOverviewData();

    const latest =
        overviewData.value.grocery_lists && overviewData.value.grocery_lists[0]
            ? overviewData.value.grocery_lists[0]
            : null;
    const selectableRecipeIdSet = new Set(
        (overviewData.value.recipes || [])
            .filter((recipe) => !isPantryRecipe(recipe))
            .map((recipe) => recipe.note_id),
    );
    selectedRecipeIds.value = Array.isArray(latest?.recipe_ids)
        ? latest.recipe_ids.filter((id) => selectableRecipeIdSet.has(id))
        : [];
    configDays.value = latest?.num_days || 8;
    configPeople.value = latest?.num_people || 1;
    preCookRecipeIds.value = new Set();
    editableUnvalidIngredients.value = unvalidIngredients.value.map(
        createUnvalidIngredientDraft,
    );
    pantryRecipeDetail.value = null;
    selectedPantryIngredientIds.value = [];
    showPantryModal.value = false;
    showUnvalidModal.value = false;
    hydrateCurrentListDraft();
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

watch(
    () =>
        `${latestList.value?.id || 0}:${serializeGroceryItems(latestList.value?.items || [])}`,
    () => {
        hydrateCurrentListDraft();
    },
    { immediate: true },
);

watch(
    () => pantryRecipe.value?.note_id || 0,
    (nextPantryID) => {
        if (!nextPantryID) {
            pantryRecipeDetail.value = null;
            selectedPantryIngredientIds.value = [];
            showPantryModal.value = false;
            return;
        }
        if (pantryRecipeDetail.value?.id !== nextPantryID) {
            pantryRecipeDetail.value = null;
            selectedPantryIngredientIds.value = [];
        }
    },
);

function replaceGroceryListInState(updatedList) {
    const groceryLists = Array.isArray(overviewData.value?.grocery_lists)
        ? overviewData.value.grocery_lists
        : [];
    overviewData.value = {
        ...overviewData.value,
        grocery_lists: groceryLists.map((list) =>
            list.id === updatedList.id ? updatedList : list,
        ),
    };
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

function togglePreCookRecipe(recipeID) {
    const next = new Set(preCookRecipeIds.value);
    if (next.has(recipeID)) {
        next.delete(recipeID);
    } else {
        next.add(recipeID);
    }
    preCookRecipeIds.value = next;
}

async function generateGroceryList() {
    clearActionFeedback();
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
        preCookRecipeIds.value = new Set();
        hydrateCurrentListDraft();
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to generate grocery list.";
        console.error("generate grocery list:", e);
    } finally {
        generatingList.value = false;
    }
}

async function saveCurrentGroceryList(itemsOverride = null) {
    if (!latestList.value) return null;

    clearActionFeedback();
    savingCurrentList.value = true;
    try {
        const sanitizedItems = sanitizeGroceryItemsForSave(
            itemsOverride ?? editableLatestListItems.value,
        );
        const result = await pluginActionV2(
            props.token,
            props.note.id,
            "update_grocery_list",
            {
                list_id: latestList.value.id,
                items: sanitizedItems,
            },
        );
        const updatedList = result?.grocery_list;
        if (updatedList) {
            replaceGroceryListInState(updatedList);
            hydrateCurrentListDraft();
        }
        return updatedList || null;
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to save grocery list changes.";
        console.error("save grocery list:", e);
        return null;
    } finally {
        savingCurrentList.value = false;
    }
}

function addCurrentListItem() {
    editableLatestListItems.value = [
        ...editableLatestListItems.value,
        createEmptyGroceryItem(),
    ];
}

function removeCurrentListItem(index) {
    editableLatestListItems.value = editableLatestListItems.value.filter(
        (_item, itemIndex) => itemIndex !== index,
    );
}

async function deleteGroceryList(listId) {
    if (!confirm("Delete this grocery list?")) return;
    clearActionFeedback();
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
        if (latestList.value?.id === listId) {
            hydrateCurrentListDraft();
        }
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to delete grocery list.";
        console.error("delete grocery list:", e);
    } finally {
        deletingListId.value = null;
    }
}

async function printGroceryList(listId) {
    clearActionFeedback();
    if (latestList.value?.id === listId && currentListDirty.value) {
        const saved = await saveCurrentGroceryList();
        if (!saved) return;
    }

    printingListId.value = listId;
    try {
        const result = await pluginActionV2(
            props.token,
            props.note.id,
            "print_grocery_list",
            {
                list_id: listId,
            },
        );
        if (result?.preview) {
            actionPreviewText.value = result.preview;
            actionErrorMessage.value = result.error || "Printer not available";
        }
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to print grocery list.";
        console.error("print grocery list:", e);
    } finally {
        printingListId.value = null;
    }
}

async function createPantryStaplesRecipe() {
    clearActionFeedback();
    creatingPantryRecipe.value = true;
    try {
        const created = await createNote(
            props.token,
            "Pantry Staples",
            [
                "Use this recipe as a pantry checklist.",
                "",
                "- Add one ingredient per staple you want to keep stocked.",
                "- Use Recipe Overview to add missing staples to the current grocery list.",
            ].join("\n"),
            props.note?.parent_id ?? null,
            "recipe",
            createEmptyRecipePayload(),
            ["pantry"],
        );
        pantryRecipeDetail.value = created;
        await refreshOverviewData();
        emit("selectNote", created.id);
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to create Pantry Staples recipe.";
        console.error("create pantry recipe:", e);
    } finally {
        creatingPantryRecipe.value = false;
    }
}

async function ensurePantryRecipeLoaded() {
    if (!pantryRecipe.value?.note_id) return null;
    if (pantryRecipeDetail.value?.id === pantryRecipe.value.note_id) {
        return pantryRecipeDetail.value;
    }

    loadingPantry.value = true;
    try {
        pantryRecipeDetail.value = await fetchNote(
            props.token,
            pantryRecipe.value.note_id,
        );
        return pantryRecipeDetail.value;
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to load Pantry Staples recipe.";
        console.error("load pantry recipe:", e);
        return null;
    } finally {
        loadingPantry.value = false;
    }
}

async function openPantryModal() {
    clearActionFeedback();
    showPantryModal.value = true;
    selectedPantryIngredientIds.value = [];
    await ensurePantryRecipeLoaded();
}

function closePantryModal() {
    showPantryModal.value = false;
    selectedPantryIngredientIds.value = [];
}

function pantryIngredientSelectionKey(item, index) {
    if (item?.id != null && item.id !== "") {
        return `id:${item.id}`;
    }
    return `idx:${index}:${normalizeString(item?.name)}`;
}

function formatGroceryItemLine(item) {
    const effectiveItem = effectivePantryGroceryItem(item);
    const amount = normalizeString(effectiveItem.amount);
    const unit = normalizeString(effectiveItem.unit);
    if (amount && unit) return `${amount} ${unit}`;
    return amount || unit || "No amount set";
}

async function addSelectedPantryItemsToCurrentList() {
    if (!latestList.value) return;

    const pantryNote = await ensurePantryRecipeLoaded();
    if (!pantryNote) return;

    const selectedSet = new Set(selectedPantryIngredientIds.value);
    const selectedItems = pantryIngredientRows.value
        .filter((item, index) =>
            selectedSet.has(pantryIngredientSelectionKey(item, index)),
        )
        .map((item) => effectivePantryGroceryItem(item));

    if (selectedItems.length === 0) return;

    const mergedItems = mergeGroceryItems(
        editableLatestListItems.value,
        selectedItems,
    );
    const saved = await saveCurrentGroceryList(mergedItems);
    if (saved) {
        closePantryModal();
    }
}

async function printPantryStaples() {
    if (!pantryRecipe.value?.note_id) return;

    clearActionFeedback();
    printingPantry.value = true;
    try {
        const result = await pluginActionV2(
            props.token,
            pantryRecipe.value.note_id,
            "print_recipe",
            {},
        );
        if (result?.preview) {
            actionPreviewText.value = result.preview;
            actionErrorMessage.value = result.error || "Printer not available";
        }
    } catch (e) {
        actionErrorMessage.value =
            (e && e.message) || "Failed to print Pantry Staples.";
        console.error("print pantry staples:", e);
    } finally {
        printingPantry.value = false;
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
        actionErrorMessage.value =
            (e && e.message) || "Failed to save ingredient changes.";
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

.overview-actions-group {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 0.4rem;
}

.action-feedback {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.action-error {
    margin: 0;
    color: var(--heading-color);
}

.action-preview {
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.75rem;
}

.action-preview-title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    margin-bottom: 0.4rem;
    text-transform: uppercase;
    letter-spacing: 0.03em;
}

.action-preview-box {
    margin: 0;
    white-space: pre-wrap;
    font-size: 0.85rem;
    color: var(--font-color);
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

.current-list-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    flex-wrap: wrap;
    margin-bottom: 0.25rem;
}

.grocery-list-current h4 {
    font-size: 0.95rem;
    margin: 0;
    color: var(--font-color-secondary);
}

.current-list-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.4rem;
}

.current-list-hint {
    margin: 0.35rem 0 0.5rem;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.grocery-input {
    width: 100%;
    padding: 0.35rem 0.45rem;
    font-size: 0.9rem;
}

.ingredient-table-editable td {
    vertical-align: middle;
}

.item-actions-col {
    width: 1%;
    white-space: nowrap;
}

.item-row-actions {
    text-align: right;
}

.btn-delete-row {
    color: var(--heading-color);
}

.empty-table-cell {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
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
    margin: 0 0.25rem;
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

.category-group-row td {
    font-size: 0.78rem;
    font-weight: 700;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    background: color-mix(in srgb, var(--raised-bg) 70%, var(--html-bg) 30%);
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

.pantry-modal {
    width: min(42rem, 92vw);
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

.pantry-modal-hint {
    margin: 0.2rem 0 0;
}

.pantry-list {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
}

.pantry-item {
    display: flex;
    align-items: flex-start;
    gap: 0.65rem;
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.7rem 0.8rem;
    background: var(--html-bg);
    cursor: pointer;
}

.pantry-item-checkbox {
    margin-top: 0.15rem;
}

.pantry-item-main {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
}

.pantry-item-name {
    font-weight: 600;
}

.pantry-item-prepare,
.pantry-item-meta {
    color: var(--font-color-secondary);
    font-size: 0.88rem;
}

.pantry-modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1rem;
    flex-wrap: wrap;
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
