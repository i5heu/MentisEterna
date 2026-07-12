<template>
    <div class="job-queue" :class="{ 'job-queue-inline': inline }">
        <button
            v-if="!inline"
            class="job-queue-toggle"
            :title="'Job queue (' + pendingCount + ' pending)'"
            @click="expanded = !expanded"
        >
            <span class="job-icon">⚙</span>
            <span v-if="pendingCount > 0" class="job-badge">{{
                pendingCount
            }}</span>
        </button>

        <div v-if="inline || expanded" class="job-queue-panel">
            <div v-if="!inline" class="job-panel-header">
                <span>Job Queue</span>
                <button class="btn-ghost" @click="expanded = false">✕</button>
            </div>
            <div v-if="loading" class="job-empty">Loading…</div>
            <div v-else-if="runs.length === 0" class="job-empty">
                No recent jobs.
            </div>
            <div v-else class="job-list">
                <div
                    v-for="run in runs"
                    :key="run.id"
                    class="job-item"
                    :class="'job-' + run.status"
                >
                    <span
                        class="job-status-icon"
                        :class="'icon-' + run.status"
                        >{{ statusIcon(run.status) }}</span
                    >
                    <div class="job-info">
                        <span class="job-name"
                            >{{ run.plugin_id ? run.plugin_id + "/" : ""
                            }}{{ run.job_name }}</span
                        >
                        <span class="job-time">{{
                            fmtTime(run.created_at)
                        }}</span>
                        <span
                            v-if="
                                run.status === 'failed' ||
                                run.status === 'errored'
                            "
                            class="job-error"
                            >{{ run.error }}</span
                        >
                        <span
                            v-if="
                                run.status === 'completed' ||
                                run.status === 'done'
                            "
                            class="job-result"
                            >{{ run.result }}</span
                        >
                    </div>
                    <div class="job-actions">
                        <button
                            v-if="
                                run.status === 'failed' ||
                                run.status === 'errored' ||
                                run.status === 'cancelled'
                            "
                            class="btn-ghost job-retry-btn"
                            title="Retry"
                            @click="doRetry(run.id)"
                        >
                            ↻
                        </button>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import { fetchJobs, retryJob } from "../api.js";

const props = defineProps({ token: String, inline: Boolean });
const emit = defineEmits(["job-done"]);

const expanded = ref(false);
const loading = ref(false);
const runs = ref([]);
const pendingCount = ref(0);
const seenDone = ref(new Set());

let loadTimer = null;

function statusIcon(status) {
    switch (status) {
        case "pending":
            return "⏳";
        case "running":
            return "⟳";
        case "completed":
        case "done":
            return "✓";
        case "failed":
        case "errored":
            return "✗";
        case "cancelled":
            return "⊘";
        default:
            return "?";
    }
}

function fmtTime(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const hour = String(d.getHours()).padStart(2, "0");
    const minute = String(d.getMinutes()).padStart(2, "0");
    return `${month}-${day} ${hour}:${minute}`;
}

async function load() {
    loading.value = true;
    try {
        const data = await fetchJobs(props.token);
        runs.value = data.runs || [];
        pendingCount.value = data.pending_count ?? data.total ?? 0;
        for (const r of runs.value) {
            if (
                r.job_name === "generate_title" &&
                (r.status === "completed" || r.status === "done") &&
                !seenDone.value.has(r.id)
            ) {
                seenDone.value.add(r.id);
                emit("job-done", { name: "generate_title", runId: r.id });
            }
        }
    } catch {
        // Silently ignore errors when polling.
    } finally {
        loading.value = false;
    }
}

async function doRetry(runId) {
    try {
        await retryJob(props.token, runId);
        await load();
    } catch (e) {
        console.error("retry job failed", e);
    }
}

function scheduleLoad() {
    if (loadTimer) return;
    loadTimer = window.setTimeout(() => {
        loadTimer = null;
        load();
    }, 100);
}

function onLiveMessage(event) {
    const type = event?.detail?.type;
    if (type !== "jobs.changed" && type !== "live.ready") return;
    scheduleLoad();
}

onMounted(() => {
    load();
    window.addEventListener("live:message", onLiveMessage);
});

onUnmounted(() => {
    window.removeEventListener("live:message", onLiveMessage);
    if (loadTimer) {
        window.clearTimeout(loadTimer);
        loadTimer = null;
    }
});
</script>

<style scoped>
.job-queue {
    position: relative;
}

.job-queue-toggle {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 10px;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--panel-bg);
    color: var(--font-color);
    cursor: pointer;
    font-size: 14px;
    transition: background 0.15s;
}

.job-queue-toggle:hover {
    background: var(--raised-bg);
}

.job-icon {
    font-size: 16px;
}

.job-badge {
    background: var(--tag-bg-color);
    color: var(--html-bg);
    border-radius: 10px;
    padding: 0 6px;
    font-size: 11px;
    font-weight: 600;
    line-height: 18px;
    min-width: 18px;
    text-align: center;
}

.job-queue-panel {
    position: absolute;
    bottom: 100%;
    right: 0;
    margin-bottom: 8px;
    width: 340px;
    max-height: 400px;
    overflow-y: auto;
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 4px 16px var(--shadow-color);
    z-index: 100;
}

/* Inline variant: no absolute positioning, max-height with scroll */
.job-queue-inline .job-queue-panel {
    position: static;
    width: 100%;
    max-height: 300px;
    overflow-y: auto;
    margin-bottom: 0;
    border-radius: 0;
    border: none;
    box-shadow: none;
    background: transparent;
}

.job-panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border-color);
    font-size: 14px;
    font-weight: 600;
    color: var(--font-color);
}

.job-empty {
    padding: 16px 14px;
    color: var(--font-color-secondary);
    font-size: 13px;
}

.job-list {
    padding: 4px 0;
}

.job-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 14px;
    border-bottom: 1px solid var(--border-color);
    font-size: 13px;
    color: var(--font-color);
}

.job-item:last-child {
    border-bottom: none;
}

.job-status-icon {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    font-size: 12px;
    font-family: inherit;
    background: var(--raised-bg);
    color: var(--font-color-secondary);
}

.icon-completed,
.icon-done {
    background: rgba(74, 222, 128, 0.12);
    color: var(--accent-teal);
}

.icon-failed,
.icon-errored {
    background: rgba(248, 113, 113, 0.12);
    color: var(--heading-color);
}

.icon-running {
    background: rgba(59, 130, 246, 0.12);
    color: #60a5fa;
}

.icon-pending {
    color: var(--tag-bg-color);
}

.job-info {
    flex: 1;
    min-width: 0;
}

.job-name {
    display: block;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.job-time {
    display: block;
    font-size: 11px;
    color: var(--font-color-secondary);
    margin-top: 2px;
}

.job-error {
    display: block;
    font-size: 11px;
    color: var(--heading-color);
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.job-result {
    display: block;
    font-size: 11px;
    color: var(--font-color-secondary);
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.job-actions {
    flex-shrink: 0;
}

.job-retry-btn {
    padding: 2px 8px;
    font-size: 14px;
    line-height: 1;
}

.job-running .job-status-icon {
    animation: spin 1.5s linear infinite;
}

@keyframes spin {
    from {
        transform: rotate(0deg);
    }
    to {
        transform: rotate(360deg);
    }
}
</style>
