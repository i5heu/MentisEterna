<template>
    <div v-if="hasContent" class="upload-progress-panel">
        <!-- Active uploads -->
        <div
            v-for="entry in active"
            :key="entry.uploadId"
            class="upload-active"
        >
            <div class="upload-header">
                <span class="upload-filename" :title="entry.filename">
                    {{ entry.filename }}
                </span>
                <button
                        v-if="isCancellable(entry)"
                        class="upload-cancel-btn"
                        title="Cancel upload"
                        @click="cancel(entry.uploadId)"
                    >
                    ✕
                </button>
            </div>
            <div class="upload-stats">
                <span>{{ statusLabel(entry) }}</span>
                <span v-if="entry.speed > 0 && entry.percent < 100">{{ formatSpeed(entry.speed) }}</span>
                <span v-if="entry.percent > 0 && entry.percent < 100 && entry.speed > 0">{{ formatETA(entry) }}</span>
            </div>
            <div class="progress-bar">
                <div
                    class="progress-fill"
                    :class="{ indeterminate: isProcessing(entry) }"
                    :style="isProcessing(entry) ? {} : { width: entry.percent + '%' }"
                />
            </div>
        </div>

        <!-- Recently completed -->
        <div v-if="completed.length && active.length === 0" class="upload-completed">
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

const { queue, active, completed, queueCount, cancel } = useUploadQueue();

const hasContent = computed(() => active.value.length > 0 || completed.length > 0 || queueCount.value > 0);

function statusLabel(entry) {
    if (!entry) return "";
    const s = entry.status;
    if (!s || s === "uploading") return `${entry.percent}%`;
    if (s === "staging") return "Staging...";
    if (s === "resuming") return "Resuming...";
    return s.charAt(0).toUpperCase() + s.slice(1);
}

// Processing phases: server is doing work or we're staging chunks, bar should animate
function isProcessing(entry) {
    if (!entry) return false;
    const s = entry.status;
    return s === "staging" || s === "assembling" || s === "verifying" || s === "processing" || s === "done" || s === "Assembling chunks..." || s === "Verifying integrity..." || s === "Encrypting and uploading..." || s === "Done";
}

// Allow cancel only during chunk upload, not during server processing
function isCancellable(entry) {
    if (!entry) return false;
    const s = entry.status;
    return s === "uploading" || s === "hashing" || s === "staging" || s === "resuming" || !s;
}

function formatSpeed(bytesPerSec) {
    if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
    if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
    return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
}

function formatETA(entry) {
    if (!entry) return "";
    const speed = entry.speed;
    if (!speed || speed <= 0) return "";
    const remaining = entry.total - entry.loaded;
    const seconds = remaining / speed;
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
    display: flex;
    flex-direction: column;
    gap: 10px;
}

.upload-active {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding-bottom: 10px;
    border-bottom: 1px solid var(--border-color, #444);
}
.upload-active:last-of-type {
    padding-bottom: 0;
    border-bottom: none;
}

.upload-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
}

.upload-filename {
    font-weight: 600;
    font-size: 0.85rem;
    word-break: break-word;
    overflow-wrap: break-word;
}

.upload-cancel-btn {
    background: none;
    border: none;
    color: var(--font-color-secondary, #999);
    cursor: pointer;
    font-size: 1rem;
    line-height: 1;
    padding: 2px;
}
.upload-cancel-btn:hover {
    color: var(--heading-color, #bf0604);
}

.upload-stats {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 0.75rem;
    color: var(--font-color-secondary, #999);
}

.progress-bar {
    width: 100%;
    height: 4px;
    background: var(--border-color, #444);
    border-radius: 2px;
    overflow: hidden;
}

.progress-fill {
    height: 100%;
    background: var(--accent-teal, #60a5fa);
    border-radius: 2px;
    transition: width 0.2s ease;
}

.progress-fill.indeterminate {
    width: 100%;
    animation: indeterminate-bar 1.5s ease-in-out infinite;
}

@keyframes indeterminate-bar {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(100%); }
}

.upload-completed {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.completed-entry {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.8rem;
}

.completed-entry.error {
    color: var(--heading-color, #bf0604);
}

.completed-check {
    font-weight: 700;
    color: var(--accent-teal, #60a5fa);
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
    font-size: 0.7rem;
    margin-left: auto;
    opacity: 0.8;
}

.upload-queue-badge {
    padding-top: 6px;
    border-top: 1px solid var(--border-color, #444);
    text-align: center;
    font-size: 0.7rem;
    color: var(--font-color-secondary, #999);
}
</style>
