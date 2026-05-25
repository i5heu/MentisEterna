<template>
    <span v-if="label" class="shortcut-hint" :class="positionClass">
        {{ label }}
    </span>
</template>

<script setup>
import { computed } from "vue";

const props = defineProps({
    label: { type: String, default: "" },
    position: { type: String, default: "top-right" },
});

const positionClass = computed(() => `shortcut-hint--${props.position}`);
</script>

<style scoped>
.shortcut-hint {
    position: absolute;
    z-index: 30;
    min-width: 1.5rem;
    padding: 0.15rem 0.35rem;
    border-radius: 999px;
    border: 1px solid rgba(255, 191, 89, 0.85);
    background: rgba(1, 16, 31, 0.96);
    color: var(--tag-bg-color);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.35);
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    line-height: 1.1;
    text-transform: uppercase;
    pointer-events: none;
    white-space: nowrap;
}

.shortcut-hint--top-right {
    top: -0.45rem;
    right: -0.35rem;
}

.shortcut-hint--top-left {
    top: -0.45rem;
    left: -0.35rem;
}

.shortcut-hint--bottom-right {
    right: -0.35rem;
    bottom: -0.45rem;
}

.shortcut-hint--bottom-left {
    left: -0.35rem;
    bottom: -0.45rem;
}

.shortcut-hint--center-right {
    top: 50%;
    right: -0.35rem;
    transform: translateY(-50%);
}

.shortcut-hint--center-left {
    top: 50%;
    left: -0.35rem;
    transform: translateY(-50%);
}
</style>
