<template>
    <div v-if="visible" class="modal-overlay" @click.self="$emit('close')">
        <div class="upload-modal">
            <div class="modal-header">
                <h2>Upload Attachment</h2>
                <button class="btn-ghost modal-close-btn" @click="$emit('close')">✕</button>
            </div>

            <div class="modal-body">
                <!-- Drag zone / file picker (only show when no active upload) -->
                <div
                    v-if="!active"
                    class="drop-zone"
                    :class="{ dragging: isDragging }"
                    @dragover.prevent="isDragging = true"
                    @dragleave.prevent="isDragging = false"
                    @drop.prevent="onDrop"
                >
                    <p class="drop-zone-text">
                        <template v-if="isDragging">Drop file here</template>
                        <template v-else>
                            <span class="drop-zone-icon">📁</span>
                            Drag a file here or
                            <button class="drop-zone-link" @click="openFilePicker">browse</button>
                        </template>
                    </p>
                    <input
                        ref="fileInput"
                        type="file"
                        class="file-input-hidden"
                        @change="onFileSelected"
                    />
                </div>

                <!-- Active upload progress -->
                <div v-if="active" class="upload-active-block">
                    <div class="upload-header">
                        <span class="upload-filename" :title="active.filename">
                            {{ active.filename }}
                        </span>
                    </div>
                    <div class="upload-stats">
                        <span>{{ active.status === 'hashing' ? 'Hashing…' : active.status === 'finalizing' ? 'Finalizing…' : `${active.percent}%` }}</span>
                        <span v-if="active.speed > 0">{{ formatSpeed(active.speed) }}</span>
                        <span v-if="active.percent > 0 && active.percent < 100 && active.speed > 0">{{ formatETA() }}</span>
                    </div>
                    <div class="progress-bar">
                        <div
                            class="progress-fill"
                            :class="{ finalizing: active.status === 'finalizing' }"
                            :style="{ width: active.percent + '%' }"
                        />
                    </div>
                    <div class="upload-actions">
                        <button
                            v-if="active.status !== 'finalizing'"
                            class="btn-ghost btn-sm"
                            @click="onCancel"
                        >
                            Cancel
                        </button>
                    </div>
                </div>

                <!-- Completed -->
                <div v-if="completed.length && !active" class="completed-section">
                    <template v-for="entry in completed" :key="entry.uploadId">
                        <div v-if="entry.error" class="completed-entry error">
                            <span class="completed-icon">✕</span>
                            <span>{{ entry.filename }}</span>
                            <span class="completed-error-detail">{{ entry.error }}</span>
                        </div>
                        <div v-else class="completed-entry">
                            <span class="completed-icon success">✓</span>
                            <span>{{ entry.filename }}</span>
                        </div>
                    </template>
                </div>

                <!-- Queue -->
                <div v-if="queueCount > 0" class="queue-section">
                    <span class="queue-badge">{{ queueCount }} pending</span>
                </div>
            </div>

            <div class="modal-footer">
                <button class="btn-primary" @click="$emit('close')">
                    {{ completed.length ? "Done" : "Close" }}
                </button>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, watch } from "vue";
import { useUploadQueue } from "../composables/useUploadQueue.js";

const props = defineProps({
    visible: Boolean,
    noteId: Number,
    token: String,
});
const emit = defineEmits(["close", "uploaded"]);

const { active, completed, queueCount, enqueueAttachment, cancel } = useUploadQueue();

const fileInput = ref(null);
const isDragging = ref(false);

// Watch for completion
watch(
    () => completed.length,
    (newLen) => {
        if (newLen > 0) {
            const last = completed[completed.length - 1];
            if (last && last.result) {
                emit("uploaded", last.result);
            }
        }
    },
);

function openFilePicker() {
    fileInput.value?.click();
}

function onFileSelected(event) {
    const file = event.target.files[0];
    if (!file) return;
    enqueueAttachment(file, props.noteId, props.token);
    // Reset so the same file can be re-selected
    event.target.value = "";
}

function onDrop(event) {
    isDragging.value = false;
    const file = event.dataTransfer.files[0];
    if (!file) return;
    enqueueAttachment(file, props.noteId, props.token);
}

function onCancel() {
    if (active.value) {
        cancel(active.value.uploadId);
    }
}

function formatSpeed(bytesPerSec) {
    if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
    if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
    return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
}

function formatETA() {
    const a = active.value;
    if (!a || !a.speed || a.speed <= 0) return "";
    const remaining = a.total - a.loaded;
    const seconds = remaining / a.speed;
    if (seconds < 60) return `${Math.round(seconds)}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
</script>

<style scoped>
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 2000;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
}

.upload-modal {
    background: var(--panel-bg, #061320);
    border: 1px solid var(--border-color, #7e7567);
    border-radius: 12px;
    width: 420px;
    max-width: 90vw;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 40px var(--shadow-color, rgba(0, 0, 0, 0.6));
    color: var(--font-color, #e0e8e4);
}

.modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 20px 0;
}
.modal-header h2 {
    font-size: 1.1rem;
    margin: 0;
}
.modal-close-btn {
    padding: 2px 8px !important;
    font-size: 1rem !important;
}

.modal-body {
    padding: 16px 20px;
    flex: 1;
    overflow-y: auto;
}

.modal-footer {
    padding: 0 20px 16px;
    display: flex;
    justify-content: flex-end;
}

/* Drop zone */
.drop-zone {
    border: 2px dashed var(--border-color, #7e7567);
    border-radius: 10px;
    padding: 40px 20px;
    text-align: center;
    transition: border-color 0.2s, background 0.2s;
    cursor: pointer;
}
.drop-zone.dragging {
    border-color: var(--accent-teal, #6d9484);
    background: var(--raised-bg, #0a1d2d);
}
.drop-zone-text {
    margin: 0;
    color: var(--font-color-secondary, #a5b0ad);
    font-size: 0.9rem;
}
.drop-zone-icon {
    display: block;
    font-size: 2rem;
    margin-bottom: 8px;
}
.drop-zone-link {
    background: none;
    border: none;
    color: var(--accent-teal, #6d9484);
    cursor: pointer;
    text-decoration: underline;
    padding: 0;
    font-size: inherit;
}
.drop-zone-link:hover {
    color: var(--font-color, #e0e8e4);
}
.file-input-hidden {
    display: none;
}

/* Upload active */
.upload-active-block {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.upload-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
}
.upload-filename {
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.upload-stats {
    display: flex;
    gap: 12px;
    font-size: 0.8rem;
    color: var(--font-color-secondary, #a5b0ad);
}

.progress-bar {
    width: 100%;
    height: 8px;
    background: var(--raised-bg, #0a1d2d);
    border-radius: 4px;
    overflow: hidden;
    margin: 4px 0;
}
.progress-fill {
    height: 100%;
    background: var(--accent-teal, #6d9484);
    border-radius: 4px;
    transition: width 0.2s ease;
}
.progress-fill.finalizing {
    animation: pulse 1s ease-in-out infinite;
}
@keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
}

.upload-actions {
    display: flex;
    gap: 8px;
    margin-top: 4px;
}

/* Completed */
.completed-section {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 8px;
}
.completed-entry {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 0;
    font-size: 0.85rem;
}
.completed-entry.error {
    color: var(--heading-color, #bf0604);
    flex-wrap: wrap;
}
.completed-icon {
    font-weight: 700;
    flex-shrink: 0;
}
.completed-icon.success {
    color: var(--accent-teal, #6d9484);
}
.completed-error-detail {
    font-size: 0.75rem;
    width: 100%;
    padding-left: 22px;
}

/* Queue */
.queue-section {
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--border-color, #7e7567);
}
.queue-badge {
    font-size: 0.8rem;
    color: var(--font-color-secondary, #a5b0ad);
}
</style>
