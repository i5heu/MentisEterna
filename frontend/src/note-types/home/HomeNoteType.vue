<template>
    <div class="home-dashboard">
        <!-- Stats Row -->
        <div class="stats-row">
            <div class="stat-card">
                <span class="stat-number">{{
                    viewData.stats?.total_notes || 0
                }}</span>
                <span class="stat-label">Total Notes</span>
            </div>
            <div class="stat-card">
                <span class="stat-number">{{
                    viewData.stats?.notes_last_7_days || 0
                }}</span>
                <span class="stat-label">Last 7 Days</span>
            </div>
            <div class="stat-card">
                <span class="stat-number">{{
                    viewData.stats?.notes_last_30_days || 0
                }}</span>
                <span class="stat-label">Last 30 Days</span>
            </div>
            <div class="stat-card stat-todo">
                <span class="stat-number">{{
                    viewData.stats?.tasks_todo || 0
                }}</span>
                <span class="stat-label">Tasks To Do</span>
            </div>
            <div class="stat-card stat-progress">
                <span class="stat-number">{{
                    viewData.stats?.tasks_in_progress || 0
                }}</span>
                <span class="stat-label">In Progress</span>
            </div>
            <div class="stat-card stat-done">
                <span class="stat-number">{{
                    viewData.stats?.tasks_done || 0
                }}</span>
                <span class="stat-label">Tasks Done</span>
            </div>
        </div>

        <!-- Mind Dump Section -->
        <div class="section mind-dump-section">
            <h4>🧠 Mind Dump</h4>
            <p class="mind-dump-hint">
                Jot down anything on your mind — it'll be saved as a new note.
            </p>
            <textarea
                v-model="mindDumpText"
                rows="4"
                class="mind-dump-textarea"
                placeholder="What's on your mind?"
                @keydown.ctrl.enter="doMindDump"
            ></textarea>
            <div class="mind-dump-actions">
                <input
                    v-model="mindDumpTags"
                    class="mind-dump-tags"
                    placeholder="Tags (comma separated)"
                />
                <button
                    class="btn-primary"
                    @click="doMindDump"
                    :disabled="dumping || !mindDumpText.trim()"
                >
                    {{ dumping ? "Saving..." : "💾 Dump (Ctrl+Enter)" }}
                </button>
            </div>
            <p v-if="dumpError" class="error-msg">{{ dumpError }}</p>
            <p v-if="dumpSuccess" class="success-msg">{{ dumpSuccess }}</p>
        </div>

        <!-- Recent Notes -->
        <div class="section">
            <h4>📝 Recent Notes</h4>
            <div v-if="recentNotes.length === 0" class="empty-hint">
                No notes yet. Create one!
            </div>
            <div v-else class="recent-notes-list">
                <div
                    v-for="n in recentNotes"
                    :key="n.id"
                    class="recent-note-row"
                    @click="$emit('selectNote', n.id)"
                >
                    <span class="recent-note-type-badge">{{ n.type }}</span>
                    <span class="recent-note-title">{{
                        n.title || "Untitled"
                    }}</span>
                    <span class="recent-note-body">{{
                        truncatedBody(n.body)
                    }}</span>
                    <span class="recent-note-date">{{
                        formatDate(n.updated_at)
                    }}</span>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, computed, watch } from "vue";
import { createNote } from "../../api.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

// View data from the server (BuildView result)
const viewData = ref({
    recent_notes: [],
    stats: {},
    mind_dump: "",
});

let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    if (cd && typeof cd === "object") {
        viewData.value = {
            recent_notes: Array.isArray(cd.recent_notes) ? cd.recent_notes : [],
            stats: cd.stats || {},
            mind_dump: cd.mind_dump || "",
        };
    }
    hydrating = false;
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (cd && cd.recent_notes) {
            hydrateFromProp();
        }
    },
);

// Mind dump
const mindDumpText = ref("");
const mindDumpTags = ref("");
const dumping = ref(false);
const dumpError = ref("");
const dumpSuccess = ref("");

async function doMindDump() {
    const body = mindDumpText.value.trim();
    if (!body) return;

    dumping.value = true;
    dumpError.value = "";
    dumpSuccess.value = "";

    try {
        const tags = mindDumpTags.value
            .split(",")
            .map((t) => t.trim())
            .filter(Boolean);

        await createNote(props.token, "", body, null, "standard", null, tags);

        mindDumpText.value = "";
        mindDumpTags.value = "";
        dumpSuccess.value = "Note created! 🎉";
        setTimeout(() => {
            dumpSuccess.value = "";
        }, 3000);
    } catch (e) {
        dumpError.value = "Failed: " + (e.message || "unknown error");
    } finally {
        dumping.value = false;
    }
}

// Recent notes (read from viewData)
const recentNotes = computed(() => viewData.value.recent_notes || []);

function truncatedBody(body) {
    if (!body) return "";
    // Strip markdown for preview
    let text = body.replace(/[#*_`~>]/g, "").trim();
    if (text.length > 80) text = text.substring(0, 80) + "…";
    return text;
}

function formatDate(dateStr) {
    if (!dateStr) return "";
    try {
        const d = new Date(dateStr);
        const now = new Date();
        const diffMs = now - d;
        const diffMins = Math.floor(diffMs / 60000);
        if (diffMins < 1) return "just now";
        if (diffMins < 60) return `${diffMins}m ago`;
        const diffHours = Math.floor(diffMins / 60);
        if (diffHours < 24) return `${diffHours}h ago`;
        const diffDays = Math.floor(diffHours / 24);
        if (diffDays < 7) return `${diffDays}d ago`;
        return d.toLocaleDateString(undefined, {
            month: "short",
            day: "numeric",
        });
    } catch {
        return dateStr;
    }
}
</script>

<style scoped>
.home-dashboard {
    display: flex;
    flex-direction: column;
    gap: 1.5rem;
}

.stats-row {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
}

.stat-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.75rem 1.25rem;
    border-radius: 8px;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    min-width: 5rem;
}

.stat-number {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--font-color);
}

.stat-label {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    text-align: center;
}

.stat-todo {
    border-left: 3px solid #e8e8e8;
}

.stat-progress {
    border-left: 3px solid #22c55e;
}

.stat-done {
    border-left: 3px solid #3b82f6;
}

.section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.section h4 {
    font-size: 1rem;
    margin: 0;
    color: var(--font-color);
}

/* Mind Dump */
.mind-dump-section {
    padding: 1rem;
    border: 2px dashed var(--border-color);
    border-radius: 10px;
    background: var(--raised-bg);
}

.mind-dump-hint {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    margin: 0;
}

.mind-dump-textarea {
    width: 100%;
    padding: 0.6rem 0.75rem;
    font-size: 0.95rem;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    resize: vertical;
    font-family: inherit;
    line-height: 1.5;
    background: var(--raised-bg);
}

.mind-dump-textarea:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.mind-dump-actions {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
}

.mind-dump-tags {
    flex: 1;
    min-width: 10rem;
    max-width: 20rem;
    padding: 0.35rem 0.5rem;
    font-size: 0.85rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--raised-bg);
}

.mind-dump-tags:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.error-msg {
    color: var(--heading-color);
    font-size: 0.8rem;
    margin: 0;
}

.success-msg {
    color: var(--accent-teal);
    font-size: 0.8rem;
    margin: 0;
}

/* Recent Notes */
.recent-notes-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
}

.recent-note-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.6rem;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.1s;
}

.recent-note-row:hover {
    background: var(--panel-bg);
}

.recent-note-type-badge {
    font-size: 0.65rem;
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background: var(--accent-teal-dim);
    color: var(--font-color);
    text-transform: uppercase;
    flex-shrink: 0;
}

.recent-note-title {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--font-color);
    flex: 0 0 auto;
    max-width: 16rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.recent-note-body {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.recent-note-date {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    flex-shrink: 0;
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
    padding: 0.5rem 0;
}
</style>

<style>
@import "../task/status-colors.css";
</style>
