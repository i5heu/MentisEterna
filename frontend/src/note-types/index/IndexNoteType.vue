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
                User-applied and auto-generated tags are shown separately.
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
                    <div class="index-tag-heading">
                        <span class="index-tag-name">🏷 {{ entry.tag }}</span>
                        <div class="index-tag-source-list">
                            <span
                                v-if="entry.user_count"
                                class="index-source-badge index-source-user"
                            >
                                User {{ entry.user_count }}
                            </span>
                            <span
                                v-if="entry.auto_count"
                                class="index-source-badge index-source-auto"
                            >
                                Auto {{ entry.auto_count }}
                            </span>
                            <span
                                v-if="entry.source === 'mixed'"
                                class="index-source-badge index-source-mixed"
                            >
                                Mixed
                            </span>
                        </div>
                    </div>
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
                        <div class="index-note-meta">
                            <span
                                class="index-source-badge"
                                :class="sourceBadgeClass(n.source)"
                            >
                                {{ sourceLabel(n.source) }}
                            </span>
                            <span class="index-note-date">{{
                                n.created_at
                                    ? new Date(n.created_at).toLocaleDateString()
                                    : ""
                            }}</span>
                        </div>
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
                Add user tags or generate auto tags to see them indexed here.
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

function sourceLabel(source) {
    switch (source) {
        case "user":
            return "User";
        case "auto":
            return "Auto";
        case "mixed":
            return "User + Auto";
        default:
            return "Unknown";
    }
}

function sourceBadgeClass(source) {
    return {
        "index-source-user": source === "user",
        "index-source-auto": source === "auto",
        "index-source-mixed": source === "mixed",
    };
}

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
    gap: 0.75rem;
    padding: 0.3rem 0.5rem;
    background: var(--raised-bg);
}

.index-tag-heading {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    min-width: 0;
}

.index-tag-name {
    font-weight: 600;
}

.index-tag-source-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
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
    gap: 0.75rem;
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

.index-note-meta {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-shrink: 0;
}

.index-note-date {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}

.index-source-badge {
    display: inline-flex;
    align-items: center;
    border-radius: 999px;
    border: 1px solid var(--border-color);
    padding: 0.1rem 0.45rem;
    font-size: 0.68rem;
    font-weight: 600;
    line-height: 1.2;
    white-space: nowrap;
}

.index-source-user {
    background: var(--accent-teal-dim);
}

.index-source-auto {
    background: var(--accent-amber-dim);
}

.index-source-mixed {
    background: var(--raised-bg);
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
