<template>
    <div class="job-queue">
        <button
            class="job-queue-toggle"
            :title="'Job queue (' + pendingCount + ' pending)'"
            @click="expanded = !expanded"
        >
            <span class="job-icon">⚙</span>
            <span v-if="pendingCount > 0" class="job-badge">{{
                pendingCount
            }}</span>
        </button>

        <div v-if="expanded" class="job-queue-panel">
            <div class="job-panel-header">
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
                    <span class="job-status-icon">{{
                        statusIcon(run.status)
                    }}</span>
                    <div class="job-info">
                        <span class="job-name"
                            >{{ run.plugin_id }}/{{ run.job_name }}</span
                        >
                        <span class="job-time">{{
                            fmtTime(run.created_at)
                        }}</span>
                        <span
                            v-if="run.status === 'errored'"
                            class="job-error"
                            >{{ run.error }}</span
                        >
                        <span
                            v-if="run.status === 'done' && run.result"
                            class="job-result"
                            >{{ run.result }}</span
                        >
                    </div>
                    <div class="job-actions">
                        <button
                            v-if="
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
import { ref, onMounted, onUnmounted, watch } from "vue";
import { fetchJobs, retryJob } from "../api.js";

const props = defineProps({ token: String });

const expanded = ref(false);
const loading = ref(false);
const runs = ref([]);
const pendingCount = ref(0);

let pollTimer = null;

function statusIcon(status) {
    switch (status) {
        case "planned":
            return "⏳";
        case "running":
            return "⟳";
        case "done":
            return "✓";
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
        pendingCount.value = data.pending_count || 0;
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

function startPolling() {
    if (pollTimer) return;
    load();
    pollTimer = setInterval(() => {
        if (expanded.value) {
            load();
        }
    }, 10000);
}

function stopPolling() {
    if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
    }
}

watch(expanded, (val) => {
    if (val) {
        load();
        startPolling();
    }
});

onMounted(() => {
    // Load initial count even when collapsed (for badge).
    load();
    startPolling();
});

onUnmounted(() => {
    stopPolling();
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
    align-items: flex-start;
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
    width: 18px;
    text-align: center;
    font-size: 14px;
    line-height: 1.4;
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
