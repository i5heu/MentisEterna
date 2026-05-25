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
                <p v-if="importError" class="recipe-import-error">
                    {{ importError }}
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
                            v-model="ing.amount"
                            placeholder="e.g. 2"
                            class="amount-input"
                        />
                    </td>
                    <td v-else>{{ ing.amount || "-" }}</td>
                    <td v-if="editing">
                        <select v-model="ing.unit" class="unit-select">
                            <option value="g">g</option>
                            <option value="kg">kg</option>
                            <option value="ml">ml</option>
                            <option value="l">l</option>
                            <option value="pcs">pcs</option>
                        </select>
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

const VALID_UNITS = new Set(["g", "kg", "ml", "l", "pcs"]);

const RECIPE_IMPORT_SCHEMA = {
    type: "object",
    additionalProperties: false,
    required: ["recipes"],
    properties: {
        recipes: {
            type: "array",
            minItems: 1,
            items: {
                type: "object",
                additionalProperties: false,
                properties: {
                    title: { type: "string" },
                    body: { type: "string" },
                    ingredients: {
                        type: "array",
                        items: {
                            type: "object",
                            additionalProperties: false,
                            required: ["name", "unit"],
                            properties: {
                                name: { type: "string", minLength: 1 },
                                amount: {
                                    oneOf: [
                                        { type: "string" },
                                        { type: "number" },
                                    ],
                                },
                                unit: {
                                    type: "string",
                                    enum: ["g", "kg", "ml", "l", "pcs"],
                                },
                            },
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
                { name: "Rice", amount: 250, unit: "g" },
                { name: "Coconut milk", amount: 400, unit: "ml" },
                { name: "Carrot", amount: 2, unit: "pcs" },
            ],
            servings: 4,
            attention_time: "20m",
            total_time: "35m",
            grams_per_serving: 480,
            kcal_per_serving: 620,
            freezable: true,
            pre_cook_servings: 8,
        },
        {
            title: "Quick Tomato Pasta",
            body: "Boil pasta. Simmer the tomato sauce. Toss and serve.",
            ingredients: [
                { name: "Pasta", amount: 300, unit: "g" },
                { name: "Tomato sauce", amount: 500, unit: "ml" },
                { name: "Parmesan", amount: 80, unit: "g" },
            ],
            servings: 3,
            attention_time: "15m",
            total_time: "25m",
            kcal_per_serving: 540,
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
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData", "import:recipes"]);

// Local reactive copies
const localIngredients = ref([]);
const localServings = ref("");
const localAttentionTime = ref("");
const localTotalTime = ref("");
const localGramsPerServing = ref("");
const localKcalPerServing = ref("");
const localFreezable = ref(false);
const localPreCookServings = ref("");
const showImportPanel = ref(false);
const importJsonText = ref("");
const importError = ref("");
const copyingSchema = ref(false);

const isRecipeEmpty = computed(() => {
    const hasIngredients = localIngredients.value.length > 0;
    const hasBody = hasText(props.note?.body);
    const hasAttachments = Array.isArray(props.note?.attachments)
        ? props.note.attachments.length > 0
        : false;
    return !hasIngredients && !hasBody && !hasAttachments;
});

// Guard to break the echo-back loop:
// local change => emit => parent sets customData prop => hydrate watcher
// Without this guard, the hydration overwrites local state, triggering
// the deep watcher again, creating an infinite cycle that crashes the tab.
let hydrating = false;

function hasText(value) {
    return String(value ?? "").trim() !== "";
}

function normalizeString(value) {
    if (value == null) return "";
    return String(value).trim();
}

function normalizeIngredient(raw) {
    return {
        name: normalizeString(raw?.name),
        amount: normalizeString(raw?.amount),
        unit: normalizeString(raw?.unit),
    };
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
        freezable: !!safe.freezable,
        pre_cook_servings: normalizeString(safe.pre_cook_servings),
    };
}

function hasImportedRecipeContent(recipe) {
    const data = recipe?.customData || {};
    return (
        hasText(recipe?.title) ||
        hasText(recipe?.body) ||
        (Array.isArray(data.ingredients) && data.ingredients.length > 0) ||
        hasText(data.servings) ||
        hasText(data.attention_time) ||
        hasText(data.total_time) ||
        hasText(data.grams_per_serving) ||
        hasText(data.kcal_per_serving) ||
        data.freezable ||
        hasText(data.pre_cook_servings)
    );
}

function normalizeImportedRecipe(raw) {
    return {
        title: normalizeString(raw?.title),
        body: normalizeString(raw?.body),
        customData: buildRecipeCustomData(raw),
    };
}

function setLocalRecipeState(raw, { emitAfter = false } = {}) {
    hydrating = true;
    const safe = raw && typeof raw === "object" ? raw : {};
    const data = buildRecipeCustomData(safe);

    localIngredients.value = data.ingredients;
    localServings.value = data.servings;
    localAttentionTime.value = data.attention_time;
    localTotalTime.value = data.total_time;
    localGramsPerServing.value = data.grams_per_serving;
    localKcalPerServing.value = data.kcal_per_serving;
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
}

// Hydrate when the note identity changes (user opens a different note).
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also hydrate if customData arrives asynchronously after the note id.
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        const ings = cd && Array.isArray(cd.ingredients) ? cd.ingredients : [];
        if (ings.length > 0 && localIngredients.value.length === 0) {
            hydrateFromProp();
        }
    },
);

// Emit custom data on any local change.
function emitCustomData() {
    if (hydrating) return;
    emit("update:customData", {
        ingredients: localIngredients.value.map(({ name, amount, unit }) => ({
            name,
            amount,
            unit,
        })),
        servings: localServings.value,
        attention_time: localAttentionTime.value,
        total_time: localTotalTime.value,
        grams_per_serving: localGramsPerServing.value,
        kcal_per_serving: localKcalPerServing.value,
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

    let parsed;
    try {
        parsed = JSON.parse(importJsonText.value);
    } catch {
        importError.value = "Recipe import JSON is not valid JSON.";
        return;
    }

    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        importError.value = "Recipe import must be a JSON object.";
        return;
    }

    if (!Array.isArray(parsed.recipes) || parsed.recipes.length === 0) {
        importError.value =
            "Recipe import must include a non-empty recipes array.";
        return;
    }

    const importedRecipes = [];
    for (let i = 0; i < parsed.recipes.length; i += 1) {
        const rawRecipe = parsed.recipes[i];
        if (
            !rawRecipe ||
            typeof rawRecipe !== "object" ||
            Array.isArray(rawRecipe)
        ) {
            importError.value = `Recipe ${i + 1} must be a JSON object.`;
            return;
        }

        const recipe = normalizeImportedRecipe(rawRecipe);
        const ingredients = recipe.customData.ingredients;
        const emptyNameIndex = ingredients.findIndex((ing) => !ing.name);
        if (emptyNameIndex >= 0) {
            importError.value = `Recipe ${i + 1}, ingredient ${emptyNameIndex + 1} is missing a name.`;
            return;
        }

        const invalidUnitIndex = ingredients.findIndex(
            (ing) => !VALID_UNITS.has(ing.unit),
        );
        if (invalidUnitIndex >= 0) {
            importError.value = `Recipe ${i + 1}, ingredient ${invalidUnitIndex + 1} has an invalid unit. Use one of: g, kg, ml, l, pcs.`;
            return;
        }

        if (!hasImportedRecipeContent(recipe)) {
            importError.value = `Recipe ${i + 1} is empty. Provide at least a title, body, ingredients, or recipe details.`;
            return;
        }

        importedRecipes.push(recipe);
    }

    emit("import:recipes", importedRecipes);
    resetImportPanel();
}

function addIngredient() {
    localIngredients.value.push({ name: "", amount: "", unit: "" });
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

.ingredient-table input {
    width: 100%;
    padding: 0.3rem 0.4rem;
    font-size: 0.9rem;
}

.amount-input {
    width: 5rem !important;
}

.unit-select {
    width: 5.5rem;
    padding: 0.3rem 0.4rem;
    font-size: 0.9rem;
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

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
