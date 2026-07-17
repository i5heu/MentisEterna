<template>
    <div v-if="attachments?.length || pendingAttachments.length" class="note-attachments">
        <h4>Attachments</h4>
        <ul>
            <!-- Pending uploads (in progress) -->
            <li
                v-for="p in pendingAttachments"
                :key="'pending-' + p.uploadId"
                class="attachment-row pending"
            >
                <div class="attach-file">
                    <span class="pending-spinner" />
                    <span class="pending-filename">{{ p.filename }}</span>
                    <span class="pending-status">{{ statusLabel(p) }}</span>
                </div>
                <span class="attachment-size">{{ formatSize(p.total) }}</span>
                <span class="attachment-size" />
            </li>

            <!-- Finished attachments -->
            <li v-for="file in attachments" :key="file.id" class="attachment-row">
                <!-- Column 1: filename + action button -->
                <div class="attach-file">
                    <template v-if="file.is_audio">
                        <audio
                            :src="file.url"
                            controls
                            class="media-player audio-player"
                            preload="metadata"
                        />
                        <button
                            v-if="sttState[file.id] !== 'has_text'"
                            class="btn-ghost action-btn"
                            :title="sttState[file.id] === 'loading' ? 'Transcribing…' : 'Transcribe audio'"
                            :disabled="sttState[file.id] === 'loading'"
                            @click="onTranscribe(file)"
                        >
                            {{
                                sttState[file.id] === 'loading'
                                    ? "Transcribing…"
                                    : "Transcribe"
                            }}
                        </button>
                        <button
                            v-else
                            class="btn-ghost action-btn"
                            title="Hide transcription"
                            @click="onDismissSTT(file)"
                        >
                            Transcription
                        </button>
                    </template>
                    <template v-else-if="file.is_image">
                        <a :href="file.url" target="_blank" rel="noreferrer">{{ file.filename }}</a>
                        <button
                            v-if="ocrState[file.id] !== 'has_text'"
                            class="btn-ghost action-btn"
                            :title="ocrState[file.id] === 'loading' ? 'Running OCR…' : 'Run OCR on image'"
                            :disabled="ocrState[file.id] === 'loading'"
                            @click="onOCR(file)"
                        >
                            {{
                                ocrState[file.id] === 'loading'
                                    ? "OCR…"
                                    : "OCR"
                            }}
                        </button>
                        <button
                            v-else
                            class="btn-ghost action-btn"
                            title="Hide OCR result"
                            @click="onDismissOCR(file)"
                        >
                            OCR
                        </button>
                    </template>
                    <template v-else>
                        <a :href="file.url" target="_blank" rel="noreferrer">{{ file.filename }}</a>
                    </template>
                </div>

                <!-- Column 2: size -->
                <span class="attachment-size">{{
                    formatSize(file.size_bytes)
                }}</span>

                <!-- Column 4: delete (only in edit mode) -->
                <button
                    v-if="editing"
                    class="btn-ghost attachment-remove-btn"
                    title="Remove attachment"
                    @click="$emit('remove', file)"
                >
                    ✕
                </button>

                <!-- Full-width results below the row -->
                <div v-if="file.is_audio && sttText[file.id]" class="stt-result">
                    <pre class="stt-text">{{ sttText[file.id] }}</pre>
                </div>
                <div v-if="file.is_audio && sttError[file.id]" class="stt-error">
                    {{ sttError[file.id] }}
                </div>
                <div v-if="file.is_image && ocrText[file.id]" class="stt-result">
                    <pre class="stt-text">{{ ocrText[file.id] }}</pre>
                </div>
                <div v-if="file.is_image && ocrError[file.id]" class="stt-error">
                    {{ ocrError[file.id] }}
                </div>
            </li>
        </ul>
    </div>
</template>

<script setup>
import { reactive, computed } from "vue";
import {
    fetchSTTResult,
    triggerSTT,
    fetchOCRResult,
    triggerOCR,
} from "../api.js";
import { useUploadQueue } from "../composables/useUploadQueue.js";

const props = defineProps({
    attachments: Array,
    editing: Boolean,
    token: String,
    noteId: { type: Number, default: null },
});
const emit = defineEmits(["remove"]);

const { active } = useUploadQueue();

// Show uploads that are in progress for this note.
const pendingAttachments = computed(() => {
    if (!props.noteId) return [];
    return active.value.filter(a => a.noteId === props.noteId);
});

function statusLabel(p) {
    if (!p) return "";
    const s = p.status;
    if (!s || s === "uploading") return `${p.percent}%`;
    if (s === "staging") return "Preparing...";
    if (s === "hashing") return "Hashing...";
    if (s === "resuming") return "Resuming...";
    return s;
}

// STT state per file: "idle" | "loading" | "has_text" | "error"
const sttState = reactive({});
const sttText = reactive({});
const sttError = reactive({});

// OCR state per file: "idle" | "loading" | "has_text" | "error"
const ocrState = reactive({});
const ocrText = reactive({});
const ocrError = reactive({});

async function onTranscribe(file) {
    if (!props.token) return;

    // If there's an error (failed transcription), trigger a new STT job.
    if (sttError[file.id]) {
        sttError[file.id] = "";
        sttState[file.id] = "loading";
        try {
            await triggerSTT(props.token, file.id);
        } catch (e) {
            sttError[file.id] = e.message || "Failed to trigger transcription";
            sttState[file.id] = "error";
            return;
        }
        // Job queued — reset to idle so user can check back later.
        sttState[file.id] = "idle";
        return;
    }

    // Fetch existing result.
    sttState[file.id] = "loading";
    sttError[file.id] = "";
    try {
        const result = await fetchSTTResult(props.token, file.id);
        if (result && result.error) {
            sttError[file.id] = result.error;
            sttState[file.id] = "error";
        } else if (result && result.stt_text) {
            sttText[file.id] = result.stt_text.trim();
            sttState[file.id] = "has_text";
        } else {
            sttError[file.id] =
                "No transcription available yet. It may still be processing.";
            sttState[file.id] = "error";
        }
    } catch (e) {
        sttError[file.id] =
            "Transcription not yet available. Try again in a moment.";
        sttState[file.id] = "error";
    }
}

function onDismissSTT(file) {
    sttText[file.id] = "";
    sttError[file.id] = "";
    sttState[file.id] = "idle";
}

async function onOCR(file) {
    if (!props.token) return;

    // If there's an error (failed OCR), trigger a new OCR job.
    if (ocrError[file.id]) {
        ocrError[file.id] = "";
        ocrState[file.id] = "loading";
        try {
            await triggerOCR(props.token, file.id);
        } catch (e) {
            ocrError[file.id] = e.message || "Failed to trigger OCR";
            ocrState[file.id] = "error";
            return;
        }
        // Job queued — reset to idle so user can check back later.
        ocrState[file.id] = "idle";
        return;
    }

    // Fetch existing result.
    ocrState[file.id] = "loading";
    ocrError[file.id] = "";
    try {
        const result = await fetchOCRResult(props.token, file.id);
        if (result && result.error) {
            ocrError[file.id] = result.error;
            ocrState[file.id] = "error";
        } else if (result && result.ocr_text) {
            ocrText[file.id] = result.ocr_text.trim();
            ocrState[file.id] = "has_text";
        } else {
            ocrError[file.id] =
                "No OCR result available yet. It may still be processing.";
            ocrState[file.id] = "error";
        }
    } catch (e) {
        ocrError[file.id] =
            "OCR not yet available. Try again in a moment.";
        ocrState[file.id] = "error";
    }
}

function onDismissOCR(file) {
    ocrText[file.id] = "";
    ocrError[file.id] = "";
    ocrState[file.id] = "idle";
}

function formatSize(bytes) {
    if (!bytes) return "";
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
</script>

<style scoped>
.note-attachments {
    margin-top: 12px;
    padding: 8px 12px;
    border-top: 1px solid var(--border-color, #333);
}
.note-attachments h4 {
    margin: 0 0 6px;
    font-size: 0.85rem;
    color: var(--font-color-secondary, #999);
}
.note-attachments ul {
    list-style: none;
    margin: 0;
    padding: 0;
}

/* Grid row */
.attachment-row {
    display: grid;
    grid-template-columns: 1fr auto auto;
    align-items: baseline;
    gap: 8px;
    padding: 4px 0;
    font-size: 0.85rem;
}

/* Column 1: filename area */
.attach-file {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    overflow: hidden;
}
.attach-file a {
    color: var(--accent-color, #60a5fa);
    text-decoration: none;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.attach-file a:hover {
    text-decoration: underline;
}

/* Pending upload spinner */
.pending-spinner {
    display: inline-block;
    width: 14px;
    height: 14px;
    border: 2px solid var(--border-color, #444);
    border-top-color: var(--accent-teal, #60a5fa);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    flex-shrink: 0;
    margin-right: 4px;
}

@keyframes spin {
    to { transform: rotate(360deg); }
}

.pending-filename {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--font-color-secondary, #999);
}

.pending-status {
    font-size: 0.72rem;
    color: var(--font-color-secondary, #888);
    white-space: nowrap;
    flex-shrink: 0;
}

/* Action button (Transcribe / OCR), 1em left margin from filename */
.action-btn {
    font-size: 0.75rem;
    padding: 3px 8px;
    white-space: nowrap;
    margin-left: 0.5em;
}

.audio-player {
    height: 32px;
    max-width: 260px;
    flex-shrink: 0;
}

.attachment-size {
    color: var(--font-color-secondary, #666);
    font-size: 0.75rem;
    white-space: nowrap;
}

.attachment-remove-btn {
    font-size: 0.75rem;
    padding: 2px 6px;
}

/* Full-width results span all grid columns */
.stt-result {
    grid-column: 1 / -1;
    margin-top: 4px;
}
.stt-text {
    background: var(--raised-bg, #0d2438);
    color: var(--font-color, #e0e8e4);
    border: 1px solid var(--border-color, #1a2c3d);
    border-radius: 6px;
    padding: 8px 12px;
    font-size: 0.8rem;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 200px;
    overflow-y: auto;
    margin: 0;
}
.stt-error {
    grid-column: 1 / -1;
    width: 100%;
    color: var(--heading-color, #bf0604);
    font-size: 0.75rem;
    margin-top: 2px;
}
</style>
