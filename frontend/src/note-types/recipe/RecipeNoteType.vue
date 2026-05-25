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
        <p v-if="dirty && !editing" class="recipe-print-hint">
            Save your changes before printing so the printer uses the latest
            recipe.
        </p>
        <p v-if="printError" class="recipe-print-error">{{ printError }}</p>
        <div v-if="printPreview" class="recipe-print-preview">
            <div class="recipe-print-preview-title">Printer Preview</div>
            <pre class="recipe-print-preview-box">{{ printPreview }}</pre>
        </div>

        <h3>Ingredients</h3>

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
                    <th v-if="editing"></th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="(ing, idx) in localIngredients" :key="idx">
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
                    <td v-else>{{ ing.amount || "-" }}</td>

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
                    <td v-else>{{ ing.non_metric_amount || "-" }}</td>

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
const localServings = ref("");
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

let hydrating = false;

function hasText(value) {
    return String(value ?? "").trim() !== "";
}

function normalizeString(value) {
    if (value == null) return "";
    return String(value).trim();
}

function normalizeIngredient(raw) {
    const normalized = {
        name: normalizeString(raw?.name),
        prepare: normalizeString(raw?.prepare),
        amount: normalizeString(raw?.amount),
        unit: normalizeString(raw?.unit),
        non_metric_amount: normalizeString(raw?.non_metric_amount),
        non_metric_unit: normalizeString(raw?.non_metric_unit),
        metric_validated: !!raw?.metric_validated,
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

function shouldShowMetricValidatedField(ingredient) {
    return (
        hasText(ingredient?.amount) &&
        hasText(ingredient?.unit) &&
        hasText(ingredient?.non_metric_amount) &&
        hasText(ingredient?.non_metric_unit)
    );
}

function hasRecipeCustomData(raw) {
    const data = buildRecipeCustomData(raw);
    return (
        data.ingredients.length > 0 ||
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
    localServings.value = data.servings;
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
            name: ingredient.name,
            prepare: ingredient.prepare,
            amount: ingredient.amount,
            unit: ingredient.unit,
            non_metric_amount: ingredient.non_metric_amount,
            non_metric_unit: ingredient.non_metric_unit,
            metric_validated: shouldShowMetricValidatedField(ingredient)
                ? !!ingredient.metric_validated
                : false,
        })),
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

async function printRecipe() {
    printError.value = "";
    printPreview.value = "";

    if (!props.note?.id) return;

    try {
        const result = await execRecipeAction(
            props.note.id,
            "print_recipe",
            {},
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
        name: "",
        prepare: "",
        amount: "",
        unit: "",
        non_metric_amount: "",
        non_metric_unit: "",
        metric_validated: false,
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
