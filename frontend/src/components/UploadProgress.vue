<template>
    <div v-if="hasContent" class="upload-progress-panel">
        <!-- Active upload -->
        <div v-if="active" class="upload-active">
            <div class="upload-header">
                <span class="upload-filename" :title="active.filename">
                    {{ active.filename }}
                </span>
                <button
                    v-if="active.status !== 'finalizing'"
                    class="upload-cancel-btn"
                    title="Cancel upload"
                    @click="$emit('cancel', active.uploadId)"
                >
                    ✕
                </button>
            </div>
            <div class="upload-stats">
                <span>{{ active.status === 'hashing' ? 'Hashing…' : active.status === 'finalizing' ? 'Finalizing…' : `${active.percent}%` }}</span>
                <span v-if="active.speed > 0">{{ formatSpeed(active.speed) }}</span>
                <span v-if="active.percent > 0 && active.percent < 100 && active.speed > 0">{{ formatETA(active) }}</span>
            </div>
            <div class="progress-bar">
                <div
                    class="progress-fill"
                    :class="{ finalizing: active.status === 'finalizing' }"
                    :style="{ width: active.percent + '%' }"
                />
            </div>
        </div>

        <!-- Recently completed -->
        <div v-if="completed.length && !active" class="upload-completed">
            <template v-for="entry in completed" :key="entry.uploadId">
                <div v-if="entry.error" class="completed-entry error">
                    <span class="completed-check">✕</span>
                    <span class="completed-filename">{{ entry.filename }}</span>
                    <span class="completed-error">{{ entry.error }}</span>
                </div>
                <div v-else class="completed-entry">
                    <span class="completed-check">✓</span>
                    <span class="completed-filename">{{ entry.filename }}</span>
                </div>
            </template>
        </div>

        <!-- Queue badge -->
        <div v-if="queueCount > 0" class="upload-queue-badge">
            {{ queueCount }} pending
        </div>
    </div>
</template>

<script setup>
import { computed } from "vue";
import { useUploadQueue } from "../composables/useUploadQueue.js";

const { queue, active, completed, queueCount } = useUploadQueue();

const hasContent = computed(() => active.value || completed.length > 0 || queueCount.value > 0);

function formatSpeed(bytesPerSec) {
    if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
    if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
    return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
}

function formatETA(active) {
    if (!active.speed || active.speed <= 0) return "";
    const remaining = active.total - active.loaded;
    const seconds = remaining / active.speed;
    if (seconds < 60) return `${Math.round(seconds)}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
</script>

<style scoped>
.upload-progress-panel {
    position: fixed;
    bottom: 16px;
    left: 16px;
    z-index: 1000;
    background: var(--panel-bg, #061320);
    border: 1px solid var(--border-color, #7e7567);
    border-radius: 10px;
    padding: 12px 16px;
    min-width: 280px;
    max-width: 380px;
    box-shadow: 0 4px 24px var(--shadow-color, rgba(0, 0, 0, 0.6));
    font-size: 0.85rem;
    color: var(--font-color, #e0e8e4);
}

.upload-active {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.upload-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 8px;
}

.upload-filename {
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
}

.upload-cancel-btn {
    background: transparent;
    border: 1px solid var(--border-color, #7e7567);
    color: var(--font-color-secondary, #a5b0ad);
    border-radius: 4px;
    padding: 1px 6px;
    font-size: 0.75rem;
    cursor: pointer;
    flex-shrink: 0;
}
.upload-cancel-btn:hover {
    background: var(--raised-bg, #0a1d2d);
    color: var(--heading-color, #bf0604);
}

.upload-stats {
    display: flex;
    gap: 12px;
    font-size: 0.75rem;
    color: var(--font-color-secondary, #a5b0ad);
}

.progress-bar {
    width: 100%;
    height: 6px;
    background: var(--raised-bg, #0a1d2d);
    border-radius: 3px;
    overflow: hidden;
    margin-top: 4px;
}

.progress-fill {
    height: 100%;
    background: var(--accent-teal, #6d9484);
    border-radius: 3px;
    transition: width 0.2s ease;
}
.progress-fill.finalizing {
    animation: pulse 1s ease-in-out infinite;
}

@keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
}

.upload-completed {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.completed-entry {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.85rem;
}
.completed-entry.error {
    color: var(--heading-color, #bf0604);
}

.completed-check {
    color: var(--accent-teal, #6d9484);
    font-weight: 700;
    flex-shrink: 0;
}
.completed-entry.error .completed-check {
    color: var(--heading-color, #bf0604);
}

.completed-filename {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.completed-error {
    font-size: 0.75rem;
    color: var(--heading-color, #bf0604);
}

.upload-queue-badge {
    margin-top: 6px;
    padding-top: 6px;
    border-top: 1px solid var(--border-color, #7e7567);
    font-size: 0.75rem;
    color: var(--font-color-secondary, #a5b0ad);
}
</style>
