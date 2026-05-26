<template>
    <div v-if="modelValue" class="modal-overlay" @click.self="close">
        <div class="modal keyboard-shortcuts-modal">
            <div class="keyboard-shortcuts-header">
                <span>{{ title }}</span>
                <button class="btn-ghost icon-btn" @click="close">✕</button>
            </div>
            <div v-if="subtitle" class="keyboard-shortcuts-subtitle">
                {{ subtitle }}
            </div>
            <div class="keyboard-shortcuts-list">
                <div
                    v-for="item in items"
                    :key="item.id"
                    class="keyboard-shortcuts-row"
                    :class="{
                        'keyboard-shortcuts-row-disabled': !item.enabled,
                    }"
                >
                    <span class="keyboard-shortcuts-desc">{{
                        item.description
                    }}</span>
                    <kbd class="keyboard-shortcuts-key">{{ item.keys }}</kbd>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
const props = defineProps({
    modelValue: { type: Boolean, default: false },
    title: { type: String, default: "Keyboard Shortcuts" },
    subtitle: {
        type: String,
        default:
            "Hold Ctrl and press the shown key. After 2 seconds, normal browser Ctrl shortcuts resume.",
    },
    items: {
        type: Array,
        default: () => [],
    },
});

const emit = defineEmits(["update:modelValue"]);

function close() {
    emit("update:modelValue", false);
}
</script>

<style scoped>
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 250;
    display: grid;
    place-items: center;
    padding: 1rem;
    background: rgba(1, 16, 31, 0.68);
    backdrop-filter: blur(3px);
}

.modal {
    width: min(100%, 520px);
    padding: 1.25rem;
    border-radius: 12px;
    border: 1px solid var(--border-color);
    background: var(--panel-bg);
    box-shadow: 0 18px 40px rgba(0, 0, 0, 0.35);
}

.icon-btn {
    padding: 0.3rem 0.5rem;
    font-size: 1rem;
    line-height: 1;
}

.keyboard-shortcuts-modal {
    max-width: 520px;
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.keyboard-shortcuts-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 0.5rem;
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--header-title-color);
}

.keyboard-shortcuts-subtitle {
    margin-bottom: 1rem;
    color: var(--font-color-secondary);
    font-size: 0.9rem;
    line-height: 1.5;
}

.keyboard-shortcuts-list {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
    overflow-y: auto;
    flex: 1;
    padding-right: 0.25rem;
}

.keyboard-shortcuts-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.45rem 0;
    border-bottom: 1px solid rgba(26, 44, 61, 0.4);
}

.keyboard-shortcuts-row:last-child {
    border-bottom: none;
}

.keyboard-shortcuts-row-disabled {
    opacity: 0.55;
}

.keyboard-shortcuts-desc {
    color: var(--font-color);
}

.keyboard-shortcuts-key {
    min-width: 9rem;
    padding: 0.22rem 0.55rem;
    border-radius: 6px;
    border: 1px solid var(--border-color);
    background: var(--raised-bg);
    color: var(--tag-bg-color);
    text-align: center;
    font-family: inherit;
    font-size: 0.85rem;
    font-weight: 700;
}
</style>
