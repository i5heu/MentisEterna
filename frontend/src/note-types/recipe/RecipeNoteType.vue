<template>
    <div class="recipe-editor">
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
import { ref, watch } from "vue";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

// Local reactive copies
const localIngredients = ref([]);
const localServings = ref("");
const localAttentionTime = ref("");
const localTotalTime = ref("");
const localGramsPerServing = ref("");
const localKcalPerServing = ref("");
const localFreezable = ref(false);
const localPreCookServings = ref("");

// Guard to break the echo-back loop:
// local change => emit => parent sets customData prop => hydrate watcher
// Without this guard, the hydration overwrites local state, triggering
// the deep watcher again, creating an infinite cycle that crashes the tab.
let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    const safe =
        cd && typeof cd === "object" ? cd : { ingredients: [], servings: "" };
    const ings = Array.isArray(safe.ingredients) ? safe.ingredients : [];
    localIngredients.value = ings.map((i) => ({ ...i }));
    localServings.value = safe.servings || "";
    localAttentionTime.value = safe.attention_time || "";
    localTotalTime.value = safe.total_time || "";
    localGramsPerServing.value = safe.grams_per_serving || "";
    localKcalPerServing.value = safe.kcal_per_serving || "";
    localFreezable.value = !!safe.freezable;
    localPreCookServings.value = safe.pre_cook_servings || "";
    hydrating = false;
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

.unit-input {
    width: 6rem !important;
}

.btn-sm {
    padding: 0.2rem 0.5rem;
    font-size: 0.85rem;
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
