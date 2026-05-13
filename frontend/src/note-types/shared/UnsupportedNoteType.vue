<template>
    <div class="unsupported-type">
        <p class="type-label">
            Note type <strong>{{ note?.type || "unknown" }}</strong> is not
            supported by the current frontend.
        </p>
        <p class="type-hint">
            The note data is preserved on the server, but this client cannot
            render it. Contact the administrator or update the frontend.
        </p>
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
.unsupported-type {
    padding: 0.75rem;
    border: 1px dashed var(--border-color);
    border-radius: 6px;
    background: var(--raised-bg);
}

.type-label {
    font-size: 0.9rem;
    margin-bottom: 0.25rem;
}

.type-hint {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}
</style>
