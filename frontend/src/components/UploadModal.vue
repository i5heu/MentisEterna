<template>
    <div v-if="visible" class="modal-overlay" @click.self="$emit('close')">
        <div class="upload-modal">
            <div class="modal-header">
                <h2>Upload Attachments</h2>
                <button class="btn-ghost modal-close-btn" @click="$emit('close')">✕</button>
            </div>

            <div class="modal-body">
                <!-- Drag zone / file picker (always shown for adding more files) -->
                <div
                    v-if="!isAnyProcessing"
                    class="drop-zone"
                    :class="{ dragging: isDragging }"
                    @dragover.prevent="isDragging = true"
                    @dragleave.prevent="isDragging = false"
                    @drop.prevent="onDrop"
                >
                    <p class="drop-zone-text">
                        <template v-if="isDragging">Drop files here</template>
                        <template v-else>
                            <span class="drop-zone-icon">📁</span>
                            Drag files here or
                            <button class="drop-zone-link" @click="openFilePicker">browse</button>
                        </template>
                    </p>
                    <input
                        ref="fileInput"
                        type="file"
                        multiple
                        class="file-input-hidden"
                        @change="onFileSelected"
                    />
                </div>

                <!-- Active uploads -->
                <div
                    v-for="entry in active"
                    :key="entry.uploadId"
                    class="upload-active-block"
                >
                    <div class="upload-header">
                        <span class="upload-filename" :title="entry.filename">
                            {{ entry.filename }}
                        </span>
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
                    <div class="upload-actions">
                        <button
                            v-if="isCancellable(entry)"
                            class="btn-ghost btn-sm"
                            @click="onCancel(entry.uploadId)"
                        >
                            Cancel
                        </button>
                    </div>
                </div>

                <!-- Completed -->
                <div v-if="completed.length && active.length === 0" class="completed-section">
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
                    {{ completed.length && active.length === 0 ? "Done" : "Close" }}
                </button>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, watch, computed } from "vue";
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

function statusLabel(entry) {
    if (!entry) return "";
    const s = entry.status;
    if (!s || s === "uploading") return `${entry.percent}%`;
    return s.charAt(0).toUpperCase() + s.slice(1);
}

function isProcessing(entry) {
    if (!entry) return false;
    const s = entry.status;
    return s !== "uploading" && s !== "hashing" && s !== "";
}

function isCancellable(entry) {
    if (!entry) return false;
    const s = entry.status;
    return s === "uploading" || s === "hashing" || !s;
}

const isAnyProcessing = computed(() => {
    return active.value.some(a => isProcessing(a));
});

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
    const files = event.target.files;
    if (!files || files.length === 0) return;
    for (let i = 0; i < files.length; i++) {
        enqueueAttachment(files[i], props.noteId, props.token);
    }
    event.target.value = "";
}

function onDrop(event) {
    isDragging.value = false;
    const files = event.dataTransfer.files;
    if (!files || files.length === 0) return;
    for (let i = 0; i < files.length; i++) {
        enqueueAttachment(files[i], props.noteId, props.token);
    }
}

function onCancel(uploadId) {
    cancel(uploadId);
}

function formatSpeed(bytesPerSec) {
    if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`;
    if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
    return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
}

function formatETA(entry) {
    if (!entry || !entry.speed || entry.speed <= 0) return "";
    const remaining = entry.total - entry.loaded;
    const seconds = remaining / entry.speed;
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
    font-size: 1.1rem;
    padding: 4px 8px;
}

.modal-body {
    padding: 16px 20px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.drop-zone {
    border: 2px dashed var(--border-color, #7e7567);
    border-radius: 10px;
    padding: 32px 20px;
    text-align: center;
    transition: border-color 0.2s, background 0.2s;
    cursor: pointer;
}
.drop-zone.dragging {
    border-color: var(--accent-teal, #60a5fa);
    background: rgba(96, 165, 250, 0.05);
}
.drop-zone-text {
    margin: 0;
    font-size: 0.9rem;
    color: var(--font-color-secondary, #999);
}
.drop-zone-icon {
    font-size: 1.5rem;
    display: block;
    margin-bottom: 4px;
}
.drop-zone-link {
    background: none;
    border: none;
    color: var(--accent-teal, #60a5fa);
    cursor: pointer;
    text-decoration: underline;
    font-size: inherit;
}
.file-input-hidden {
    display: none;
}

.upload-active-block {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    border: 1px solid var(--border-color, #444);
    border-radius: 8px;
}

.upload-filename {
    font-weight: 600;
    font-size: 0.85rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.upload-stats {
    display: flex;
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

.upload-actions {
    display: flex;
    gap: 8px;
}

.completed-section {
    display: flex;
    flex-direction: column;
    gap: 6px;
}
.completed-entry {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.82rem;
}
.completed-entry.error {
    color: var(--heading-color, #bf0604);
}
.completed-icon {
    font-weight: 700;
    flex-shrink: 0;
}
.completed-icon.success {
    color: var(--accent-teal, #60a5fa);
}
.completed-error-detail {
    font-size: 0.7rem;
    margin-left: auto;
    opacity: 0.8;
}

.queue-section {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border-color, #444);
}
.queue-badge {
    font-size: 0.7rem;
    color: var(--font-color-secondary, #999);
}

.modal-footer {
    padding: 12px 20px 16px;
    display: flex;
    justify-content: flex-end;
}
</style>
