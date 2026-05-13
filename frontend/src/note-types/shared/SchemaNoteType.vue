<template>
    <div class="schema-type">
        <p class="type-label">
            Schema-driven note type: <strong>{{ note?.type || "unknown" }}</strong>
        </p>
        <pre
            v-if="customData"
            class="debug-json"
        >{{ JSON.stringify(customData, null, 2) }}</pre>
        <p v-else class="empty-hint">No data yet.</p>
    </div>
</template>

<script setup>
defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});
</script>

<style scoped>
.schema-type {
    padding: 0.75rem;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--raised-bg);
}

.type-label {
    font-size: 0.9rem;
    margin-bottom: 0.5rem;
}

.debug-json {
    background: var(--panel-bg);
    padding: 0.5rem;
    border-radius: 4px;
    font-size: 0.8rem;
    color: var(--pre-text-color);
    overflow-x: auto;
    max-height: 300px;
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
