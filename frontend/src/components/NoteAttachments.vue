<template>
    <div v-if="attachments?.length" class="note-attachments">
        <h4>Attachments</h4>
        <ul>
            <li v-for="file in attachments" :key="file.id">
                <!-- Audio player for audio files -->
                <template v-if="file.is_audio">
                    <div class="audio-row">
                        <audio
                            :src="file.url"
                            controls
                            class="media-player audio-player"
                            preload="metadata"
                        />
                        <button
                            class="btn-ghost stt-btn"
                            title="Transcribe audio"
                            @click="onTranscribe(file)"
                            :disabled="sttLoading[file.id]"
                        >
                            {{
                                sttLoading[file.id]
                                    ? "Transcribing…"
                                    : "Transcribe"
                            }}
                        </button>
                    </div>
                    <div v-if="sttText[file.id]" class="stt-result">
                        <pre class="stt-text">{{ sttText[file.id] }}</pre>
                    </div>
                    <div v-if="sttError[file.id]" class="stt-error">
                        {{ sttError[file.id] }}
                    </div>
                </template>

                <!-- Regular file link for other files -->
                <template v-else>
                    <a :href="file.url" target="_blank" rel="noreferrer">{{
                        file.filename
                    }}</a>
                </template>

                <span class="attachment-size">{{
                    formatSize(file.size_bytes)
                }}</span>
                <button
                    v-if="editing"
                    class="btn-ghost attachment-remove-btn"
                    title="Remove attachment"
                    @click="$emit('remove', file)"
                >
                    ✕
                </button>
            </li>
        </ul>
    </div>
</template>

<script setup>
import { ref, reactive } from "vue";
import { fetchSTTResult } from "../api.js";

const props = defineProps({
    attachments: Array,
    editing: Boolean,
    token: String,
});
const emit = defineEmits(["remove"]);

const sttLoading = reactive({});
const sttText = reactive({});
const sttError = reactive({});

async function onTranscribe(file) {
    if (!props.token) return;

    // If a result is already showing, clear it and reset to initial state.
    if (sttText[file.id] || sttError[file.id]) {
        sttText[file.id] = "";
        sttError[file.id] = "";
        return;
    }

    sttLoading[file.id] = true;
    sttError[file.id] = "";
    try {
        const result = await fetchSTTResult(props.token, file.id);
        if (result && result.error) {
            sttError[file.id] = result.error;
        } else if (result && result.stt_text) {
            sttText[file.id] = result.stt_text;
        } else {
            sttError[file.id] =
                "No transcription available yet. It may still be processing.";
        }
    } catch (e) {
        // If the file hasn't been transcribed yet, the STT result may not exist.
        // Poll once after a short delay.
        sttError[file.id] =
            "Transcription not yet available. Try again in a moment.";
    }
    sttLoading[file.id] = false;
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
.note-attachments li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    font-size: 0.85rem;
}
.note-attachments a {
    color: var(--accent-color, #60a5fa);
    text-decoration: none;
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.note-attachments a:hover {
    text-decoration: underline;
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

/* Audio player and STT styles */
.audio-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
}
.audio-player {
    height: 32px;
    max-width: 260px;
    flex-shrink: 0;
}
.stt-btn {
    font-size: 0.75rem;
    padding: 3px 8px;
    white-space: nowrap;
    flex-shrink: 0;
}
.stt-result {
    width: 100%;
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
    width: 100%;
    color: var(--heading-color, #bf0604);
    font-size: 0.75rem;
    margin-top: 2px;
}
</style>
