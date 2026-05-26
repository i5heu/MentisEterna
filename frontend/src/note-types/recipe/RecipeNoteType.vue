<template>
    <div class="recipe-editor">
        <div v-if="isRecipeEmpty" class="recipe-import-empty-state">
            <p class="empty-hint recipe-import-empty-hint">
                This recipe is empty. You can either add ingredients manually or
                import one or more recipes via JSON.
            </p>
            <button
                class="btn-ghost btn-sm"
                @click="showImportPanel = !showImportPanel"
            >
                {{
                    showImportPanel
                        ? "Hide Recipe JSON Import"
                        : "Import Recipe via JSON"
                }}
            </button>

            <div v-if="showImportPanel" class="recipe-import-panel">
                <p class="recipe-import-help">
                    Paste a JSON object that matches the schema below. Each item
                    in <code>recipes</code> becomes its own recipe note.
                </p>
                <textarea
                    v-model="importJsonText"
                    class="recipe-import-textarea"
                    rows="10"
                    :placeholder="recipeImportExampleText"
                ></textarea>
                <p v-if="displayImportError" class="recipe-import-error">
                    {{ displayImportError }}
                </p>
                <div class="recipe-import-actions">
                    <button class="btn-amber btn-sm" @click="importRecipeJson">
                        Import JSON
                    </button>
                    <button class="btn-ghost btn-sm" @click="clearImportJson">
                        Clear
                    </button>
                    <button
                        class="btn-ghost btn-sm"
                        :disabled="copyingSchema"
                        @click="copyRecipeSchema"
                    >
                        {{ copyingSchema ? "Copied" : "Copy Schema" }}
                    </button>
                </div>

                <div class="recipe-schema-card">
                    <div class="recipe-schema-header">Recipe JSON Schema</div>
                    <pre class="recipe-schema-text">{{
                        recipeImportSchemaText
                    }}</pre>
                </div>
            </div>
        </div>

        <div v-if="note?.id && !editing" class="recipe-actions-row">
            <button
                class="btn-amber btn-sm"
                :disabled="printingRecipe || dirty"
                @click="printRecipe"
            >
                {{ printingRecipe ? "Printing..." : "Print Recipe" }}
            </button>
        </div>
        <div v-if="!editing" class="recipe-scale-row">
            <label class="recipe-scale-label">
                <span>Show ingredient amounts for</span>
                <input
                    v-model="localDisplayServings"
                    type="number"
                    min="0.25"
                    step="0.25"
                    class="detail-input recipe-scale-input"
                    :disabled="!canScaleIngredients"
                    placeholder="Servings"
                />
                <span>servings</span>
            </label>
            <button
                v-if="hasScaledIngredientView"
                class="btn-ghost btn-sm"
                @click="resetDisplayedServings"
            >
                Reset
            </button>
        </div>
        <p v-if="!editing && !canScaleIngredients" class="recipe-scale-hint">
            Enter a numeric recipe servings value to enable ingredient scaling.
        </p>
        <p v-if="dirty && !editing" class="recipe-print-hint">
            Save your changes before printing so the printer uses the latest
            recipe.
        </p>
        <p v-if="printError" class="recipe-print-error">{{ printError }}</p>
        <div v-if="printPreview" class="recipe-print-preview">
            <div class="recipe-print-preview-title">Printer Preview</div>
            <pre class="recipe-print-preview-box">{{ printPreview }}</pre>
        </div>

        <div v-if="editing" class="ingredient-order-controls">
            <label class="ingredient-order-toggle">
                <input
                    type="checkbox"
                    :checked="localIngredientOrderManual"
                    @change="setIngredientOrderManual($event.target.checked)"
                />
                <span>Use manual ingredient order</span>
            </label>
            <p class="ingredient-order-hint">
                {{
                    localIngredientOrderManual
                        ? "Manual ingredient order is used for the recipe view and printing."
                        : "If manual order is off, the recipe view and printed version are sorted by category and then alphabetically."
                }}
            </p>
        </div>

        <h3>Ingredients</h3>

        <div class="ingredient-table-wrapper">
        <table class="ingredient-table">
            <thead>
                <tr>
                    <th>Name</th>
                    <th>Prepare</th>
                    <th>Metric Amount</th>
                    <th>Metric Unit</th>
                    <th>Non-Metric Amount</th>
                    <th>Non-Metric Type</th>
                    <th>Metric Validated</th>
                    <th>Grocery Category</th>
                    <th v-if="editing">Actions</th>
                </tr>
            </thead>
            <tbody>
                <tr
                    v-for="(ing, idx) in ingredientRows"
                    :key="ing.id || `${idx}:${ing.name}:${ing.prepare}`"
                >
                    <td v-if="editing">
                        <input
                            v-model="ing.name"
                            placeholder="Ingredient name"
                        />
                    </td>
                    <td v-else>{{ ing.name || "-" }}</td>

                    <td v-if="editing">
                        <input
                            v-model="ing.prepare"
                            placeholder="e.g. chopped"
                        />
                    </td>
                    <td v-else>{{ ing.prepare || "-" }}</td>

                    <td v-if="editing">
                        <input
                            v-model="ing.amount"
                            placeholder="e.g. 2"
                            class="amount-input"
                        />
                    </td>
                    <td v-else>{{ formatIngredientAmount(ing) }}</td>

                    <td v-if="editing">
                        <select v-model="ing.unit" class="unit-select">
                            <option value="">—</option>
                            <option value="mg">mg</option>
                            <option value="g">g</option>
                            <option value="kg">kg</option>
                            <option value="ml">ml</option>
                            <option value="l">l</option>
                            <option value="pcs">pcs</option>
                        </select>
                    </td>
                    <td v-else>{{ ing.unit || "-" }}</td>

                    <td v-if="editing">
                        <input
                            v-model="ing.non_metric_amount"
                            placeholder="e.g. 1"
                            class="amount-input"
                        />
                    </td>
                    <td v-else>{{ formatIngredientNonMetricAmount(ing) }}</td>

                    <td v-if="editing">
                        <select
                            v-model="ing.non_metric_unit"
                            class="unit-select non-metric-unit-select"
                        >
                            <option value="">—</option>
                            <option value="teaspoon">Teaspoon</option>
                            <option value="tablespoon">Tablespoon</option>
                            <option value="cup">Cup</option>
                        </select>
                    </td>
                    <td v-else>
                        {{ formatNonMetricUnit(ing.non_metric_unit) }}
                    </td>

                    <td>
                        <template v-if="shouldShowMetricValidatedField(ing)">
                            <input
                                v-if="editing"
                                type="checkbox"
                                v-model="ing.metric_validated"
                                class="detail-checkbox"
                            />
                            <span v-else>
                                {{ ing.metric_validated ? "Yes" : "No" }}
                            </span>
                        </template>
                        <span v-else class="muted-dash">—</span>
                    </td>

                    <td>
                        <select
                            v-if="editing"
                            :value="ingredientCategorySelection(ing)"
                            class="unit-select category-select"
                            @change="setIngredientCategorySelection(ing, $event.target.value)"
                        >
                            <option value="__auto__">
                                {{ autoCategoryOptionLabel(ing) }}
                            </option>
                            <option
                                v-for="category in GROCERY_CATEGORY_OPTIONS"
                                :key="category"
                                :value="category"
                            >
                                {{ formatGroceryCategoryLabel(category) }}
                            </option>
                        </select>
                        <span v-else>{{ formatIngredientCategory(ing) }}</span>
                    </td>

                    <td v-if="editing">
                        <div class="ingredient-row-actions">
                            <button
                                v-if="localIngredientOrderManual"
                                class="btn-ghost btn-sm ingredient-order-btn"
                                :disabled="idx === 0"
                                @click="moveIngredient(idx, -1)"
                            >
                                ↑
                            </button>
                            <button
                                v-if="localIngredientOrderManual"
                                class="btn-ghost btn-sm ingredient-order-btn"
                                :disabled="idx === ingredientRows.length - 1"
                                @click="moveIngredient(idx, 1)"
                            >
                                ↓
                            </button>
                            <button
                                class="btn-ghost btn-sm"
                                @click="removeIngredient(idx)"
                            >
                                &times;
                            </button>
                        </div>
                    </td>
                </tr>
            </tbody>
        </table>
        </div>
        <button v-if="editing" class="btn-ghost btn-sm" @click="addIngredient">
            + Add Ingredient
        </button>
        <p v-if="!editing && localIngredients.length === 0" class="empty-hint">
            No ingredients yet. Switch to edit mode to add some.
        </p>

        <h3 class="recipe-section-title">Details</h3>
        <div class="recipe-details">
            <div class="detail-row">
                <span class="detail-label">Servings</span>
                <input
                    v-if="editing"
                    v-model="localServings"
                    placeholder="e.g. 4"
                    class="detail-input"
                />
                <span v-else>{{ localServings || "-" }}</span>
            </div>
            <div class="detail-row">
                <span class="detail-label">Attention Time</span>
                <input
                    v-if="editing"
                    v-model="localAttentionTime"
                    placeholder="e.g. 30m"
                    class="detail-input"
                />
                <span v-else>{{ localAttentionTime || "-" }}</span>
            </div>
            <div class="detail-row">
                <span class="detail-label">Total Time</span>
                <input
                    v-if="editing"
                    v-model="localTotalTime"
                    placeholder="e.g. 1h"
                    class="detail-input"
                />
                <span v-else>{{ localTotalTime || "-" }}</span>
            </div>
            <div class="detail-row">
                <span class="detail-label">Grams per Serving</span>
                <input
                    v-if="editing"
                    v-model="localGramsPerServing"
                    placeholder="e.g. 250"
                    class="detail-input"
                />
                <span v-else>{{ localGramsPerServing || "-" }}</span>
            </div>
            <div class="detail-row">
                <span class="detail-label">kcal per Serving</span>
                <input
                    v-if="editing"
                    v-model="localKcalPerServing"
                    placeholder="e.g. 350"
                    class="detail-input"
                />
                <span v-else>{{ localKcalPerServing || "-" }}</span>
            </div>
            <div class="detail-row detail-row-rating">
                <span class="detail-label">Rating</span>
                <div v-if="editing" class="rating-editor">
                    <input
                        v-model.number="localRating"
                        type="range"
                        min="0"
                        max="10"
                        step="1"
                        class="rating-range"
                    />
                    <span class="rating-value"
                        >{{ formatRatingStars(localRating) }}
                        {{ localRating }}/10</span
                    >
                </div>
                <span v-else class="rating-value"
                    >{{ formatRatingStars(localRating) }}
                    {{ localRating }}/10</span
                >
            </div>
            <div class="detail-row detail-row-checkbox">
                <span class="detail-label">Freezable</span>
                <input
                    v-if="editing"
                    type="checkbox"
                    v-model="localFreezable"
                    class="detail-checkbox"
                />
                <span v-else>{{ localFreezable ? "Yes" : "No" }}</span>
            </div>
            <div v-if="localFreezable" class="detail-row">
                <span class="detail-label">Pre-cook Servings</span>
                <input
                    v-if="editing"
                    v-model="localPreCookServings"
                    placeholder="e.g. 8"
                    class="detail-input"
                />
                <span v-else>{{ localPreCookServings || "-" }}</span>
            </div>
        </div>
    </div>
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { usePluginAction } from "../shared/usePluginAction.js";

const GROCERY_CATEGORY_OPTIONS = [
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

const GROCERY_CATEGORY_SORT_INDEX = Object.fromEntries(
    GROCERY_CATEGORY_OPTIONS.map((category, index) => [category, index]),
);

const RECIPE_IMPORT_SCHEMA = {
    type: "object",
    description:
        "Recipe import document. Each entry in recipes becomes one recipe note.",
    additionalProperties: false,
    required: ["recipes"],
    properties: {
        recipes: {
            type: "array",
            minItems: 1,
            description: "Recipes to import.",
            items: {
                type: "object",
                additionalProperties: false,
                properties: {
                    title: {
                        type: "string",
                        description: "Recipe note title.",
                    },
                    body: {
                        type: "string",
                        description:
                            "Recipe note body / instructions in markdown.",
                    },
                    ingredients: {
                        type: "array",
                        description: "Recipe ingredients.",
                        items: {
                            type: "object",
                            additionalProperties: false,
                            required: ["name"],
                            properties: {
                                name: {
                                    type: "string",
                                    minLength: 1,
                                    description: "Ingredient name.",
                                },
                                prepare: {
                                    type: "string",
                                    description:
                                        "How the ingredient should be prepared, e.g. chopped or sliced.",
                                },
                                amount: {
                                    oneOf: [
                                        { type: "string" },
                                        { type: "number" },
                                    ],
                                    description:
                                        "Metric or pcs amount. Use together with unit.",
                                },
                                unit: {
                                    type: "string",
                                    enum: ["mg", "g", "kg", "ml", "l", "pcs"],
                                    description:
                                        "Metric or pcs unit for amount.",
                                },
                                non_metric_amount: {
                                    oneOf: [
                                        { type: "string" },
                                        { type: "number" },
                                    ],
                                    description:
                                        "Non-metric amount. Use together with non_metric_unit.",
                                },
                                non_metric_unit: {
                                    type: "string",
                                    enum: ["teaspoon", "tablespoon", "cup"],
                                    description:
                                        "Non-metric unit for non_metric_amount.",
                                },
                                metric_validated: {
                                    type: "boolean",
                                    description:
                                        "Only meaningful when both metric and non-metric values are present. If true, grocery lists use metric values. If false, grocery lists use non-metric values. If only non-metric values are present, grocery lists use those directly.",
                                },
                            },
                            allOf: [
                                {
                                    if: { required: ["amount"] },
                                    then: { required: ["unit"] },
                                },
                                {
                                    if: { required: ["unit"] },
                                    then: { required: ["amount"] },
                                },
                                {
                                    if: { required: ["non_metric_amount"] },
                                    then: { required: ["non_metric_unit"] },
                                },
                                {
                                    if: { required: ["non_metric_unit"] },
                                    then: { required: ["non_metric_amount"] },
                                },
                            ],
                        },
                    },
                    servings: {
                        oneOf: [{ type: "string" }, { type: "number" }],
                    },
                    attention_time: { type: "string" },
                    total_time: { type: "string" },
                    grams_per_serving: {
                        oneOf: [{ type: "string" }, { type: "number" }],
                    },
                    kcal_per_serving: {
                        oneOf: [{ type: "string" }, { type: "number" }],
                    },
                    rating: {
                        type: "integer",
                        minimum: 0,
                        maximum: 10,
                    },
                    freezable: { type: "boolean" },
                    pre_cook_servings: {
                        oneOf: [{ type: "string" }, { type: "number" }],
                    },
                },
            },
        },
    },
};

const RECIPE_IMPORT_EXAMPLE = {
    recipes: [
        {
            title: "Coconut Rice Bowl",
            body: "Steam the rice. Saute the vegetables. Mix everything together and serve warm.",
            ingredients: [
                { name: "Rice", prepare: "washed", amount: 250, unit: "g" },
                { name: "Coconut milk", amount: 400, unit: "ml" },
                {
                    name: "Chili flakes",
                    prepare: "crushed",
                    amount: 500,
                    unit: "mg",
                    non_metric_amount: 1,
                    non_metric_unit: "teaspoon",
                    metric_validated: false,
                },
                { name: "Carrot", prepare: "sliced", amount: 2, unit: "pcs" },
            ],
            servings: 4,
            attention_time: "20m",
            total_time: "35m",
            grams_per_serving: 480,
            kcal_per_serving: 620,
            rating: 8,
            freezable: true,
            pre_cook_servings: 8,
        },
        {
            title: "Quick Tomato Pasta",
            body: "Boil pasta. Simmer the tomato sauce. Toss and serve.",
            ingredients: [
                {
                    name: "Parmesan",
                    prepare: "grated",
                    amount: 80,
                    unit: "g",
                    non_metric_amount: 1,
                    non_metric_unit: "cup",
                    metric_validated: true,
                },
                {
                    name: "Soy Sauce",
                    non_metric_amount: 2,
                    non_metric_unit: "tablespoon",
                },
            ],
            servings: 3,
            attention_time: "15m",
            total_time: "25m",
            kcal_per_serving: 540,
            rating: 6,
            freezable: false,
        },
    ],
};

const recipeImportSchemaText = JSON.stringify(RECIPE_IMPORT_SCHEMA, null, 2);
const recipeImportExampleText = JSON.stringify(RECIPE_IMPORT_EXAMPLE, null, 2);

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    dirty: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
    actionError: { type: String, default: "" },
});

const emit = defineEmits(["selectNote", "update:customData", "import:recipes"]);

const { loading: printingRecipe, execute: execRecipeAction } = usePluginAction(
    () => props.token,
);

const localIngredients = ref([]);
const localIngredientOrderManual = ref(false);
const localServings = ref("");
const localDisplayServings = ref("");
const localAttentionTime = ref("");
const localTotalTime = ref("");
const localGramsPerServing = ref("");
const localKcalPerServing = ref("");
const localRating = ref(0);
const localFreezable = ref(false);
const localPreCookServings = ref("");
const showImportPanel = ref(false);
const importJsonText = ref("");
const importError = ref("");
const copyingSchema = ref(false);
const printError = ref("");
const printPreview = ref("");

const isRecipeEmpty = computed(() => {
    const hasIngredients = localIngredients.value.length > 0;
    const hasBody = hasText(props.note?.body);
    const hasAttachments = Array.isArray(props.note?.attachments)
        ? props.note.attachments.length > 0
        : false;
    return !hasIngredients && !hasBody && !hasAttachments;
});

const displayImportError = computed(() => {
    return importError.value || props.actionError || "";
});

const baseServingsNumber = computed(() => parseNumericAmount(localServings.value));
const displayServingsNumber = computed(() =>
    parseNumericAmount(localDisplayServings.value),
);
const canScaleIngredients = computed(() => baseServingsNumber.value != null);
const ingredientScaleFactor = computed(() => {
    if (!canScaleIngredients.value) return 1;
    const target = displayServingsNumber.value;
    if (target == null || target <= 0) return 1;
    return target / baseServingsNumber.value;
});
const hasScaledIngredientView = computed(() => {
    if (!canScaleIngredients.value) return false;
    const target = displayServingsNumber.value;
    if (target == null || target <= 0) return false;
    return Math.abs(target - baseServingsNumber.value) > 1e-9;
});
const orderedIngredients = computed(() =>
    sortIngredientsForDisplay(
        localIngredients.value,
        localIngredientOrderManual.value,
    ),
);
const ingredientRows = computed(() =>
    props.editing ? localIngredients.value : orderedIngredients.value,
);
const printDisplayServings = computed(() => {
    if (!hasScaledIngredientView.value) return "";
    return formatNumericAmount(displayServingsNumber.value);
});

let hydrating = false;

function hasText(value) {
    return String(value ?? "").trim() !== "";
}

function normalizeString(value) {
    if (value == null) return "";
    return String(value).trim();
}

function parseNumericAmount(value) {
    const trimmed = normalizeString(value);
    if (!/^\d+(?:[.,]\d+)?$/.test(trimmed)) {
        return null;
    }
    const parsed = Number.parseFloat(trimmed.replace(",", "."));
    return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

function formatNumericAmount(value) {
    if (!Number.isFinite(value) || value <= 0) return "";
    return String(Number(value.toFixed(6)));
}

function normalizeIngredient(raw) {
    const normalized = {
        id: Number.isFinite(Number(raw?.id)) ? Number(raw.id) : 0,
        name: normalizeString(raw?.name),
        prepare: normalizeString(raw?.prepare),
        amount: normalizeString(raw?.amount),
        unit: normalizeString(raw?.unit),
        non_metric_amount: normalizeString(raw?.non_metric_amount),
        non_metric_unit: normalizeString(raw?.non_metric_unit),
        metric_validated: !!raw?.metric_validated,
        grocery_category: normalizeGroceryCategory(raw?.grocery_category),
        grocery_category_manual: !!raw?.grocery_category_manual,
    };
    if (!shouldShowMetricValidatedField(normalized)) {
        normalized.metric_validated = false;
    }
    return normalized;
}

function normalizeRating(value) {
    const num = Number(value);
    if (!Number.isFinite(num)) return 0;
    return Math.max(0, Math.min(10, Math.round(num)));
}

function buildRecipeCustomData(raw) {
    const safe = raw && typeof raw === "object" ? raw : {};
    return {
        ingredients: Array.isArray(safe.ingredients)
            ? safe.ingredients.map(normalizeIngredient)
            : [],
        ingredient_order_manual: !!safe.ingredient_order_manual,
        servings: normalizeString(safe.servings),
        attention_time: normalizeString(safe.attention_time),
        total_time: normalizeString(safe.total_time),
        grams_per_serving: normalizeString(safe.grams_per_serving),
        kcal_per_serving: normalizeString(safe.kcal_per_serving),
        rating: normalizeRating(safe.rating),
        freezable: !!safe.freezable,
        pre_cook_servings: normalizeString(safe.pre_cook_servings),
    };
}

function normalizeGroceryCategory(value) {
    const normalized = normalizeString(value).toLowerCase();
    return GROCERY_CATEGORY_OPTIONS.includes(normalized) ? normalized : "";
}

function ingredientCategorySortIndex(category) {
    return (
        GROCERY_CATEGORY_SORT_INDEX[
            normalizeGroceryCategory(category) || "other"
        ] ?? GROCERY_CATEGORY_SORT_INDEX.other
    );
}

function sortIngredientsForDisplay(ingredients, manual) {
    const rows = Array.isArray(ingredients) ? [...ingredients] : [];
    if (manual) return rows;
    return rows.sort((left, right) => {
        const categoryDiff =
            ingredientCategorySortIndex(left?.grocery_category) -
            ingredientCategorySortIndex(right?.grocery_category);
        if (categoryDiff !== 0) return categoryDiff;

        const leftName = normalizeString(left?.name).toLowerCase();
        const rightName = normalizeString(right?.name).toLowerCase();
        if (leftName !== rightName) {
            return leftName.localeCompare(rightName);
        }

        return normalizeString(left?.prepare)
            .toLowerCase()
            .localeCompare(normalizeString(right?.prepare).toLowerCase());
    });
}

function formatGroceryCategoryLabel(category) {
    switch (normalizeGroceryCategory(category) || "other") {
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

function shouldShowMetricValidatedField(ingredient) {
    return (
        hasText(ingredient?.amount) &&
        hasText(ingredient?.unit) &&
        hasText(ingredient?.non_metric_amount) &&
        hasText(ingredient?.non_metric_unit)
    );
}

function ingredientCategorySelection(ingredient) {
    return ingredient?.grocery_category_manual
        ? normalizeGroceryCategory(ingredient?.grocery_category) || "other"
        : "__auto__";
}

function autoCategoryOptionLabel(ingredient) {
    const autoCategory = normalizeGroceryCategory(ingredient?.grocery_category);
    if (autoCategory) {
        return `Auto (${formatGroceryCategoryLabel(autoCategory)})`;
    }
    return "Auto";
}

function setIngredientCategorySelection(ingredient, value) {
    if (value === "__auto__") {
        ingredient.grocery_category_manual = false;
        return;
    }
    ingredient.grocery_category = normalizeGroceryCategory(value) || "other";
    ingredient.grocery_category_manual = true;
}

function formatIngredientCategory(ingredient) {
    const category = normalizeGroceryCategory(ingredient?.grocery_category);
    if (!category) return "-";
    const suffix = ingredient?.grocery_category_manual ? " (manual)" : "";
    return `${formatGroceryCategoryLabel(category)}${suffix}`;
}

function hasRecipeCustomData(raw) {
    const data = buildRecipeCustomData(raw);
    return (
        data.ingredients.length > 0 ||
        data.ingredient_order_manual ||
        hasText(data.servings) ||
        hasText(data.attention_time) ||
        hasText(data.total_time) ||
        hasText(data.grams_per_serving) ||
        hasText(data.kcal_per_serving) ||
        data.rating > 0 ||
        data.freezable ||
        hasText(data.pre_cook_servings)
    );
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
            return "-";
    }
}

function setLocalRecipeState(raw, { emitAfter = false } = {}) {
    hydrating = true;
    const data = buildRecipeCustomData(raw);

    localIngredients.value = data.ingredients;
    localIngredientOrderManual.value = data.ingredient_order_manual;
    localServings.value = data.servings;
    localDisplayServings.value =
        parseNumericAmount(data.servings) != null
            ? formatNumericAmount(parseNumericAmount(data.servings))
            : "";
    localAttentionTime.value = data.attention_time;
    localTotalTime.value = data.total_time;
    localGramsPerServing.value = data.grams_per_serving;
    localKcalPerServing.value = data.kcal_per_serving;
    localRating.value = data.rating;
    localFreezable.value = data.freezable;
    localPreCookServings.value = data.pre_cook_servings;
    hydrating = false;

    if (emitAfter) {
        emitCustomData();
    }
}

function resetImportPanel() {
    showImportPanel.value = false;
    importJsonText.value = "";
    importError.value = "";
    copyingSchema.value = false;
}

function hydrateFromProp() {
    setLocalRecipeState(props.customData, { emitAfter: false });
    resetImportPanel();
    printError.value = "";
    printPreview.value = "";
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });

watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (
            hasRecipeCustomData(cd) &&
            !hasRecipeCustomData({
                ingredients: localIngredients.value,
                ingredient_order_manual: localIngredientOrderManual.value,
                servings: localServings.value,
                attention_time: localAttentionTime.value,
                total_time: localTotalTime.value,
                grams_per_serving: localGramsPerServing.value,
                kcal_per_serving: localKcalPerServing.value,
                rating: localRating.value,
                freezable: localFreezable.value,
                pre_cook_servings: localPreCookServings.value,
            })
        ) {
            hydrateFromProp();
        }
    },
);

function emitCustomData() {
    if (hydrating) return;
    emit("update:customData", {
        ingredients: localIngredients.value.map((ingredient) => ({
            id: ingredient.id || 0,
            name: ingredient.name,
            prepare: ingredient.prepare,
            amount: ingredient.amount,
            unit: ingredient.unit,
            non_metric_amount: ingredient.non_metric_amount,
            non_metric_unit: ingredient.non_metric_unit,
            metric_validated: shouldShowMetricValidatedField(ingredient)
                ? !!ingredient.metric_validated
                : false,
            grocery_category: normalizeGroceryCategory(
                ingredient.grocery_category,
            ),
            grocery_category_manual: !!ingredient.grocery_category_manual,
        })),
        ingredient_order_manual: localIngredientOrderManual.value,
        servings: localServings.value,
        attention_time: localAttentionTime.value,
        total_time: localTotalTime.value,
        grams_per_serving: localGramsPerServing.value,
        kcal_per_serving: localKcalPerServing.value,
        rating: normalizeRating(localRating.value),
        freezable: localFreezable.value,
        pre_cook_servings: localPreCookServings.value,
    });
}

watch(
    [
        localIngredients,
        localIngredientOrderManual,
        localServings,
        localAttentionTime,
        localTotalTime,
        localGramsPerServing,
        localKcalPerServing,
        localRating,
        localFreezable,
        localPreCookServings,
    ],
    emitCustomData,
    { deep: true },
);

function clearImportJson() {
    importJsonText.value = "";
    importError.value = "";
}

async function copyRecipeSchema() {
    if (!navigator?.clipboard?.writeText) return;
    try {
        await navigator.clipboard.writeText(recipeImportSchemaText);
        copyingSchema.value = true;
        setTimeout(() => {
            copyingSchema.value = false;
        }, 1200);
    } catch (err) {
        console.error("copy recipe schema:", err);
    }
}

function importRecipeJson() {
    importError.value = "";

    if (!importJsonText.value.trim()) {
        importError.value = "Paste a recipe import JSON document first.";
        return;
    }

    emit("import:recipes", importJsonText.value);
}

function formatRatingStars(rating) {
    const safeRating = normalizeRating(rating);
    return "★".repeat(safeRating) + "☆".repeat(10 - safeRating);
}

function scaleAmountString(amount, factor) {
    const trimmed = normalizeString(amount);
    if (!trimmed) return "";
    if (!Number.isFinite(factor) || factor <= 0) return trimmed;
    const numeric = parseNumericAmount(trimmed);
    if (numeric == null) return trimmed;
    return formatNumericAmount(numeric * factor);
}

function formatIngredientAmount(ingredient) {
    const scaled = scaleAmountString(ingredient?.amount, ingredientScaleFactor.value);
    return scaled || "-";
}

function formatIngredientNonMetricAmount(ingredient) {
    const scaled = scaleAmountString(
        ingredient?.non_metric_amount,
        ingredientScaleFactor.value,
    );
    return scaled || "-";
}

function resetDisplayedServings() {
    if (baseServingsNumber.value == null) {
        localDisplayServings.value = "";
        return;
    }
    localDisplayServings.value = formatNumericAmount(baseServingsNumber.value);
}

function setIngredientOrderManual(enabled) {
    const next = !!enabled;
    if (next && !localIngredientOrderManual.value) {
        localIngredients.value = sortIngredientsForDisplay(
            localIngredients.value,
            false,
        );
    }
    localIngredientOrderManual.value = next;
}

function moveIngredient(idx, delta) {
    if (!localIngredientOrderManual.value) return;
    const target = idx + delta;
    if (idx < 0 || target < 0 || target >= localIngredients.value.length) {
        return;
    }
    const next = [...localIngredients.value];
    const [ingredient] = next.splice(idx, 1);
    next.splice(target, 0, ingredient);
    localIngredients.value = next;
}

async function printRecipe() {
    printError.value = "";
    printPreview.value = "";

    if (!props.note?.id) return;

    try {
        const params = {};
        if (printDisplayServings.value) {
            params.display_servings = printDisplayServings.value;
        }
        const result = await execRecipeAction(
            props.note.id,
            "print_recipe",
            params,
        );
        if (result?.preview) {
            printPreview.value = result.preview;
            printError.value = result.error || "Printer not available";
        }
    } catch (err) {
        printError.value = err?.message || String(err);
    }
}

function addIngredient() {
    localIngredients.value.push({
        id: 0,
        name: "",
        prepare: "",
        amount: "",
        unit: "",
        non_metric_amount: "",
        non_metric_unit: "",
        metric_validated: false,
        grocery_category: "",
        grocery_category_manual: false,
    });
}

function removeIngredient(idx) {
    localIngredients.value.splice(idx, 1);
}
</script>

<style scoped>
.recipe-editor h3 {
    font-size: 1.1rem;
    margin: 1rem 0 0.5rem;
    color: var(--font-color-secondary);
}

.recipe-import-empty-state {
    margin-bottom: 1rem;
    padding: 0.85rem 0.9rem;
    border: 1px dashed var(--border-color);
    border-radius: 8px;
    background: var(--raised-bg);
}

.recipe-import-empty-hint {
    margin: 0 0 0.7rem;
}

.recipe-import-panel {
    margin-top: 0.85rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
}

.recipe-import-help {
    margin: 0;
    font-size: 0.9rem;
    color: var(--font-color-secondary);
}

.recipe-import-textarea {
    width: 100%;
    min-height: 11rem;
    font-family: Consolas, Monaco, monospace;
    font-size: 0.85rem;
}

.recipe-import-actions {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
}

.recipe-import-error {
    margin: 0;
    color: var(--heading-color);
    font-size: 0.9rem;
}

.recipe-schema-card {
    border: 1px solid var(--border-color);
    border-radius: 6px;
    overflow: hidden;
    background: var(--html-bg);
}

.recipe-schema-header {
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    background: var(--raised-bg);
    border-bottom: 1px solid var(--border-color);
}

.recipe-schema-text {
    margin: 0;
    padding: 0.75rem;
    overflow-x: auto;
    font-size: 0.8rem;
    line-height: 1.5;
    color: var(--font-color-secondary);
    white-space: pre-wrap;
    word-break: break-word;
}

.recipe-section-title {
    margin-top: 1.25rem !important;
}

.recipe-actions-row {
    display: flex;
    justify-content: flex-end;
    margin-bottom: 0.75rem;
}

.recipe-scale-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
    margin-bottom: 0.5rem;
}

.recipe-scale-label {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
    color: var(--font-color-secondary);
    font-size: 0.9rem;
}

.recipe-scale-input {
    max-width: 7rem;
}

.recipe-scale-hint {
    margin: 0 0 0.5rem;
    color: var(--font-color-secondary);
    font-size: 0.85rem;
}

.recipe-print-hint {
    margin: 0 0 0.5rem;
    color: var(--font-color-secondary);
    font-size: 0.85rem;
}

.recipe-print-error {
    margin: 0 0 0.75rem;
    color: var(--heading-color);
    font-size: 0.9rem;
}

.recipe-print-preview {
    margin-bottom: 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 8px;
    background: var(--raised-bg);
    overflow: hidden;
}

.recipe-print-preview-title {
    padding: 0.45rem 0.7rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    border-bottom: 1px solid var(--border-color);
}

.recipe-print-preview-box {
    margin: 0;
    padding: 0.75rem;
    font-size: 0.8rem;
    line-height: 1.4;
    overflow-x: auto;
    white-space: pre;
}

.ingredient-order-controls {
    margin: 0 0 0.85rem;
    padding: 0.75rem 0.85rem;
    border: 1px solid var(--border-color);
    border-radius: 8px;
    background: var(--raised-bg);
}

.ingredient-order-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.92rem;
    font-weight: 600;
}

.ingredient-order-hint {
    margin: 0.45rem 0 0;
    color: var(--font-color-secondary);
    font-size: 0.85rem;
}

.ingredient-table-wrapper {
    overflow-x: auto;
    width: 100%;
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
    vertical-align: middle;
    white-space: nowrap;
}

.ingredient-table tbody tr:nth-child(even) {
    background: rgba(128, 128, 128, 0.06);
}

.ingredient-table th {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.03em;
}

.ingredient-table input {
    width: 100%;
    padding: 0.3rem 0.4rem;
    font-size: 0.9rem;
}

.ingredient-row-actions {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
}

.ingredient-order-btn {
    min-width: 2rem;
}

.amount-input {
    width: 5.5rem !important;
}

.unit-select {
    width: 7rem;
    padding: 0.3rem 0.4rem;
    font-size: 0.9rem;
}

.non-metric-unit-select {
    width: 9rem;
}

.category-select {
    width: 12rem;
}

.muted-dash {
    color: var(--font-color-secondary);
}

.recipe-details {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    margin-top: 0.4rem;
}

.detail-row {
    display: flex;
    align-items: center;
    gap: 0.75rem;
}

.detail-row-checkbox {
    margin-top: 0.2rem;
}

.detail-row-rating {
    align-items: flex-start;
}

.detail-label {
    min-width: 9.5rem;
    font-size: 0.9rem;
    color: var(--font-color-secondary);
}

.detail-input {
    width: 100%;
    max-width: 14rem;
    padding: 0.3rem 0.5rem;
    font-size: 0.9rem;
}

.detail-input:focus {
    border-color: var(--accent-teal);
}

.detail-checkbox {
    width: 1.1rem;
    height: 1.1rem;
    accent-color: var(--accent-teal);
}

.rating-editor {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    width: 100%;
    max-width: 20rem;
}

.rating-range {
    width: 100%;
}

.rating-value {
    font-size: 0.9rem;
    color: var(--font-color);
    letter-spacing: 0.02em;
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
