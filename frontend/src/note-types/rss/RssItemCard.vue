<template>
    <div class="rss-item" :class="{ 'rss-item-dimmed': dimmed }">
        <div class="rss-item-head">
            <a
                v-if="item.url"
                class="rss-item-title"
                :href="item.url"
                target="_blank"
                rel="noopener noreferrer"
                >{{ item.title || "(untitled)" }}</a
            >
            <span v-else class="rss-item-title rss-item-title-plain">{{
                item.title || "(untitled)"
            }}</span>
            <button
                v-if="item.state === 'starred'"
                class="btn-ghost btn-sm rss-star"
                title="Unstar"
                @click="$emit('mark', item.state === 'new' ? 'new' : 'read')"
            >
                ★
            </button>
            <button
                v-else
                class="btn-ghost btn-sm rss-star"
                title="Star"
                @click="$emit('mark', 'starred')"
            >
                ☆
            </button>
        </div>
        <div class="rss-item-badges">
            <span v-if="item.category" class="rss-badge">{{
                item.category
            }}</span>
            <span
                class="rss-badge rss-badge-quality"
                :title="`LLM quality rating: ${item.quality}/10`"
                >Q{{ item.quality }}</span
            >
            <span
                v-if="item.penalty_score > 0"
                class="rss-badge rss-badge-penalty"
                :title="penaltyTooltip"
                >−{{ item.penalty_score }}</span
            >
            <span v-if="item.banned" class="rss-badge rss-badge-banned"
                >banned</span
            >
            <span class="rss-badge rss-badge-feed">
                {{ item.feed_name }}
            </span>
            <span v-if="publishedLabel" class="rss-badge">{{
                publishedLabel
            }}</span>
            <span
                v-if="item.classify_status === 'error'"
                class="rss-badge rss-badge-banned"
                :title="item.classify_error || ''"
                >⚠ classify failed</span
            >
            <span v-if="item.saved_note_id" class="rss-badge">∼ saved</span>
        </div>
        <p v-if="item.summary" class="rss-item-summary">{{ item.summary }}</p>
        <div class="rss-item-tags" v-if="item.tags && item.tags.length">
            <span v-for="t in item.tags" :key="t" class="rss-badge rss-badge-tag">{{ t }}</span>
        </div>
        <div class="rss-item-actions">
            <button
                class="btn-ghost btn-sm"
                :disabled="busy"
                @click="$emit('save')"
                :title="item.saved_note_id ? 'Already saved as a note' : 'Save this article as a real child note'"
            >
                {{ item.saved_note_id ? "Saved" : "Save as Note" }}
            </button>
            <button
                class="btn-ghost btn-sm"
                :disabled="busy"
                @click="$emit('reclassify')"
                title="Re-run the LLM classification"
            >
                Reclassify
            </button>
            <button
                class="btn-ghost btn-sm"
                :disabled="busy"
                @click="$emit('mark', item.state === 'read' ? 'new' : 'read')"
            >
                {{ item.state === "read" ? "Mark unread" : "Mark read" }}
            </button>
            <button
                class="btn-ghost btn-sm"
                :disabled="busy"
                title="Manually reject this article"
                @click="$emit('mark', 'rejected')"
            >
                Reject
            </button>
        </div>
    </div>
</template>

<script setup>
import { computed } from "vue";

const props = defineProps({
    item: { type: Object, required: true },
    busy: { type: Boolean, default: false },
    dimmed: { type: Boolean, default: false },
});

defineEmits(["mark", "reclassify", "save"]);

const penaltyTooltip = computed(() => {
    const entries = Array.isArray(props.item.penalty)
        ? props.item.penalty
        : [];
    if (!entries.length) return "penalty points from your rules note";
    return entries
        .map((e) => `${e.phrase}: ${e.points} × ${e.occurrences || 1}`)
        .join("\n");
});

const publishedLabel = computed(() => {
    if (!props.item.published_at) return "";
    const d = new Date(props.item.published_at);
    if (Number.isNaN(d.getTime())) return props.item.published_at;
    return d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
    });
});
</script>

<style scoped>
.rss-item {
    border: 1px solid var(--border-color, rgba(127, 127, 127, 0.25));
    border-radius: 8px;
    padding: 0.55rem 0.7rem;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    background: var(--bg-color, transparent);
}

.rss-item-dimmed {
    opacity: 0.65;
}

.rss-item-head {
    display: flex;
    gap: 0.4rem;
    align-items: baseline;
}

.rss-item-title {
    font-weight: 600;
    text-decoration: none;
    color: var(--font-color, inherit);
    flex: 1;
}

.rss-item-title-plain {
    flex: 1;
}

.rss-star {
    padding: 0 0.35rem;
}

.rss-item-badges,
.rss-item-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
}

.rss-badge {
    font-size: 0.72rem;
    padding: 0.05rem 0.45rem;
    border-radius: 99px;
    background: var(--bg-color-secondary, rgba(127, 127, 127, 0.15));
    color: var(--font-color-secondary);
    cursor: default;
}

.rss-badge-quality {
    background: rgba(92, 184, 92, 0.18);
}

.rss-badge-penalty {
    background: rgba(240, 173, 78, 0.22);
    white-space: pre-line;
}

.rss-badge-banned {
    background: rgba(217, 83, 79, 0.25);
}

.rss-badge-tag {
    background: transparent;
    border: 1px solid var(--border-color, rgba(127, 127, 127, 0.3));
}

.rss-item-summary {
    font-size: 0.88rem;
    margin: 0;
    color: var(--font-color-secondary);
}

.rss-item-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    margin-top: 0.15rem;
}
</style>
