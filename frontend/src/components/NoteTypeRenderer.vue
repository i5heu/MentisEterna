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

        <!-- Generic custom data form (fallback for other types) -->
        <div
            v-if="
                note.ui_schema &&
                note.type !== 'recipe' &&
                note.type !== 'recipe_overview'
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

function addIngredient() {
    ingredients.value.push({ name: "", amount: "", unit: "" });
}

function removeIngredient(idx) {
    ingredients.value.splice(idx, 1);
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
</style>
