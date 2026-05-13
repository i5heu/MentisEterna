<template>
    <div class="checklist-editor">
        <h3>Checklist</h3>
        <div v-for="(item, idx) in localItems" :key="idx" class="checklist-row">
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
                @click="removeItem(idx)"
            >
                &times;
            </button>
        </div>
        <button v-if="editing" class="btn-ghost btn-sm" @click="addItem">
            + Add Item
        </button>
        <p v-if="!editing && localItems.length === 0" class="empty-hint">
            No items yet. Switch to edit mode to add some.
        </p>
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

const localItems = ref([]);

// Guard to break echo-back loop.
let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const items =
        props.customData && Array.isArray(props.customData.items)
            ? props.customData.items
            : [];
    localItems.value = items.map((it) => ({
        label: it.label || "",
        checked: !!it.checked,
    }));
    hydrating = false;
}

// Hydrate when the note identity changes.
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also catch async customData arrival.
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        const items = cd && Array.isArray(cd.items) ? cd.items : [];
        if (items.length > 0 && localItems.value.length === 0) {
            hydrateFromProp();
        }
    },
);

// Emit on change
watch(
    localItems,
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

function addItem() {
    localItems.value.push({ label: "", checked: false });
}

function removeItem(idx) {
    localItems.value.splice(idx, 1);
}
</script>

<style scoped>
.checklist-editor h3 {
    font-size: 1.1rem;
    margin: 0.5rem 0 0.5rem;
    color: var(--font-color-secondary);
}

.checklist-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.3rem;
}

.checklist-mark {
    width: 1.2rem;
    text-align: center;
}

.checklist-input {
    flex: 1;
    padding: 0.3rem 0.4rem;
    font-size: 0.9rem;
}

.checked-text {
    text-decoration: line-through;
    color: var(--font-color-secondary);
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}

.btn-sm {
    padding: 0.2rem 0.5rem;
    font-size: 0.85rem;
}
</style>
