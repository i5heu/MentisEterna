<template>
    <div class="index-view">
        <div v-if="editing" class="index-config">
            <label class="config-row">
                <span class="config-label">Mode:</span>
                <select
                    v-model="localMode"
                    class="config-select"
                    @change="onConfigChange"
                >
                    <option value="global">Global (all notes)</option>
                    <option value="local">Local (this branch)</option>
                </select>
            </label>
            <p class="config-hint">
                Global shows tagged notes from the entire workspace. Local shows
                only notes within the same parent and their descendants.
            </p>
        </div>
        <div
            v-if="indexData && indexData.entries && indexData.entries.length"
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
                                ? new Date(n.created_at).toLocaleDateString()
                                : ""
                        }}</span>
                    </div>
                </div>
            </div>
        </div>
        <p v-else class="empty-hint">
            No tags found.
            <template v-if="localMode === 'local'">
                Try switching to Global mode or tag some notes within this
                branch.
            </template>
            <template v-else>
                Add tags to your notes to see them indexed here.
            </template>
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

const indexData = ref(null);
const localMode = ref("global");

// Guard to break echo-back loop.
let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    indexData.value = cd || { entries: [] };
    localMode.value = (cd && cd.mode) || "global";
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
            cd.entries &&
            cd.entries.length > 0 &&
            (!indexData.value ||
                !indexData.value.entries ||
                indexData.value.entries.length === 0)
        ) {
            hydrateFromProp();
        }
    },
);

function onConfigChange() {
    emit("update:customData", {
        mode: localMode.value,
        selected_tags: indexData.value?.selected_tags || [],
    });
}

// Emit when mode changes via code too.
watch(localMode, () => {
    if (props.note?.type === "index") {
        emit("update:customData", {
            mode: localMode.value,
            selected_tags: indexData.value?.selected_tags || [],
        });
    }
});
</script>

<style scoped>
.index-view {
    gap: 0.5rem;
}

.index-config {
    margin-bottom: 0.75rem;
    padding: 0.5rem;
    background: var(--raised-bg);
    border-radius: 6px;
}

.config-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9rem;
}

.config-label {
    min-width: 3.5rem;
    color: var(--font-color-secondary);
}

.config-select {
    padding: 0.25rem 0.4rem;
    font-size: 0.9rem;
    background: var(--raised-bg);
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    outline: none;
}

.config-select:focus {
    border-color: var(--accent-teal);
}

.config-hint {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    margin-top: 0.25rem;
}

.index-entries {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.index-entry {
    border: 1px solid var(--border-color);
    border-radius: 6px;
    overflow: hidden;
}

.index-tag-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.3rem 0.5rem;
    background: var(--raised-bg);
}

.index-tag-name {
    font-weight: 600;
}

.index-tag-count {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.index-note-list {
    padding: 0.25rem;
}

.index-note-card {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0.35rem 0.5rem;
    cursor: pointer;
    border-radius: 4px;
    transition: background 0.1s;
}

.index-note-card:hover {
    background: var(--raised-bg);
}

.index-note-card:last-child {
    border-bottom: none;
}

.index-note-title {
    font-size: 0.9rem;
}

.index-note-date {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
