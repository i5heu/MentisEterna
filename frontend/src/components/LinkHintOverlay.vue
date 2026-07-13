<template>
    <div class="link-hint-overlay" aria-hidden="true">
        <div v-if="typedBuffer" class="link-hint-buffer">
            {{ typedBuffer }}
        </div>
        <span
            v-for="item in items"
            :key="item.id"
            class="link-hint-badge"
            :class="badgeClass(item.label)"
            :style="badgeStyle(item)"
        >
            {{ item.label }}
        </span>
    </div>
</template>

<script setup>
const props = defineProps({
    items: {
        type: Array,
        default: () => [],
    },
    typedBuffer: { type: String, default: "" },
});

function badgeStyle(item) {
    return {
        left: `${item.left}px`,
        top: `${item.top}px`,
    };
}

function badgeClass(label) {
    const buffer = String(props.typedBuffer || "").toUpperCase();
    if (!buffer) return "";
    const upperLabel = String(label || "").toUpperCase();
    if (upperLabel === buffer) return "link-hint-badge--exact";
    if (upperLabel.startsWith(buffer)) return "link-hint-badge--match";
    return "link-hint-badge--dimmed";
}
</script>

<style scoped>
.link-hint-overlay {
    position: fixed;
    inset: 0;
    z-index: 260;
    pointer-events: none;
}

.link-hint-buffer {
    position: fixed;
    top: 0.9rem;
    right: 1rem;
    min-width: 2.25rem;
    padding: 0.3rem 0.65rem;
    border-radius: 999px;
    border: 1px solid rgba(255, 191, 89, 0.85);
    background: rgba(1, 16, 31, 0.96);
    color: var(--tag-bg-color);
    box-shadow: 0 8px 18px rgba(0, 0, 0, 0.35);
    font-size: 0.8rem;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-align: center;
    text-transform: uppercase;
}

.link-hint-badge {
    position: fixed;
    transform: translateY(-70%);
    min-width: 1.7rem;
    padding: 0.18rem 0.45rem;
    border-radius: 999px;
    border: 1px solid rgba(255, 191, 89, 0.85);
    background: rgba(1, 16, 31, 0.96);
    color: var(--tag-bg-color);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.35);
    font-size: 0.72rem;
    font-weight: 800;
    letter-spacing: 0.05em;
    line-height: 1.1;
    text-transform: uppercase;
    white-space: nowrap;
}

.link-hint-badge--match {
    border-color: color-mix(in srgb, rgba(255, 191, 89, 0.85) 72%, white 28%);
    background: color-mix(in srgb, rgba(1, 16, 31, 0.96) 84%, var(--accent-teal) 16%);
}

.link-hint-badge--exact {
    border-color: color-mix(in srgb, var(--accent-teal) 62%, white 38%);
    background: color-mix(in srgb, rgba(1, 16, 31, 0.96) 78%, var(--accent-teal) 22%);
    box-shadow:
        0 0 0 1px color-mix(in srgb, var(--accent-teal) 30%, transparent),
        0 6px 16px rgba(0, 0, 0, 0.35);
}

.link-hint-badge--dimmed {
    opacity: 0.38;
}
</style>
