<template>
    <div class="record-audio">
        <div v-if="state === 'booting'" class="card">
            <div class="pulse">●</div>
            <p>Preparing microphone…</p>
        </div>

        <div v-else-if="state === 'invalid'" class="card">
            <h1>Recording</h1>
            <p>Invalid recorder link. Use <code>/recordaudio/&lt;parentId&gt;[/date]</code>.</p>
            <a class="btn" href="/">Back to notes</a>
        </div>

        <div v-else-if="state === 'needs-auth'" class="card">
            <h1>Sign in to MentisEterna</h1>
            <p>You need an active session to record audio.</p>
            <a class="btn" href="/">Sign in</a>
        </div>

        <div v-else-if="state === 'unconfigured'" class="card">
            <h1>Recording unavailable</h1>
            <p>Audio ingest is not configured on the server.</p>
            <a class="btn" href="/">Back to notes</a>
        </div>

        <div v-else-if="state === 'mic-denied'" class="card">
            <h1>Microphone needed</h1>
            <p>Recording needs microphone access. Grant it in the browser permission prompt, then try again.</p>
            <button class="btn" type="button" @click="requestMic">Try again</button>
        </div>

        <div v-else-if="state === 'recording'" class="card recording">
            <div class="rec-dot" aria-hidden="true"></div>
            <p class="elapsed">{{ elapsed }}</p>
            <button class="btn stop" type="button" @click="stopRecording">Stop recording</button>
        </div>

        <div v-else-if="state === 'uploading'" class="card">
            <h1>Uploading…</h1>
            <p>Your recording is saved and being uploaded.</p>
        </div>

        <div v-else-if="state === 'done'" class="card">
            <h1>Note created</h1>
            <p v-if="noteId">Saved as note <a :href="`/note/${noteId}`">#{{ noteId }}</a>.</p>
            <p v-else>Uploaded successfully.</p>
            <a class="btn" href="/">Back to notes</a>
        </div>

        <div v-else-if="state === 'queued'" class="card">
            <h1>Recording saved</h1>
            <p>Could not upload right now. It will retry automatically when you are back online.</p>
            <button class="btn" type="button" @click="handleOnline">Try again</button>
        </div>

        <div v-else-if="state === 'save-failed'" class="card">
            <h1>Could not save</h1>
            <p>This browser could not store the recording locally.</p>
            <button class="btn" type="button" @click="retrySave">Try again</button>
        </div>
    </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import { fetchSession } from "../api.js";
import {
    getIngestToken,
    addPendingAudio,
    removePendingAudio,
    listPendingAudio,
    uploadAudioRecording,
    flushPendingAudio,
} from "../audio/audioQueue.js";

const state = ref("booting");
const noteId = ref(null);
const elapsed = ref("00:00");

// URL parse, once: /recordaudio/<parentId>[/date]
const m = window.location.pathname.match(/^\/recordaudio\/(\d+)(?:\/(date))?\/?$/);
const parentId = m ? Number(m[1]) : NaN;
const dateFlag = m ? m[2] === "date" : false;
if (!m) {
    state.value = "invalid";
}

let recorder = null;
let stream = null;
let chunks = [];
let timerHandle = null;
let token = "";
let savedEntry = null;
let startTime = 0;
let booted = false;

const EXT_BY_MIME = {
    "audio/webm": "webm",
    "audio/ogg": "ogg",
    "audio/mp4": "m4a",
    "audio/mpeg": "mp3",
};

function pickRecorderMime() {
    if (typeof MediaRecorder === "undefined") {
        return "";
    }
    return (
        ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus", "audio/mp4"].find((t) =>
            MediaRecorder.isTypeSupported(t),
        ) || ""
    );
}

function extFor(mimeType) {
    const base = String(mimeType || "").split(";")[0].trim();
    return EXT_BY_MIME[base] || "webm";
}

function pad2(n) {
    return String(n).padStart(2, "0");
}

function localTimestamp() {
    const d = new Date();
    return (
        `${d.getFullYear()}${pad2(d.getMonth() + 1)}${pad2(d.getDate())}` +
        `-${pad2(d.getHours())}${pad2(d.getMinutes())}${pad2(d.getSeconds())}`
    );
}

function updateElapsed() {
    const secs = Math.floor((Date.now() - startTime) / 1000);
    elapsed.value = `${pad2(Math.floor(secs / 60))}:${pad2(secs % 60)}`;
}

async function requestMic() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
        state.value = "mic-denied";
        return;
    }
    try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        startRecorder(stream);
    } catch (e) {
        state.value = "mic-denied";
    }
}

function startRecorder(stream) {
    const mime = pickRecorderMime();
    recorder = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
    chunks = [];
    recorder.ondataavailable = (e) => {
        if (e.data && e.data.size > 0) {
            chunks.push(e.data);
        }
    };
    recorder.onstop = onStop;
    recorder.start(1000); // 1s timeslice
    startTime = Date.now();
    timerHandle = setInterval(updateElapsed, 500);
    updateElapsed();
    state.value = "recording";
}

function stopRecording() {
    if (recorder && recorder.state !== "inactive") {
        recorder.stop();
    }
}

function onStop() {
    if (stream) {
        stream.getTracks().forEach((t) => t.stop());
    }
    if (timerHandle) {
        clearInterval(timerHandle);
        timerHandle = null;
    }
    const type = (recorder && recorder.mimeType) || "audio/webm";
    const blob = new Blob(chunks, { type });
    const entry = {
        id: (crypto.randomUUID && crypto.randomUUID()) || `rec-${Date.now()}-${Math.random().toString(36).slice(2)}`,
        parentId,
        dateFlag,
        blob,
        mimeType: type,
        filename: `recording-${localTimestamp()}.${extFor(type)}`,
        createdAt: Date.now(),
        attempts: 0,
    };
    persistAndUpload(entry);
}

async function persistAndUpload(entry) {
    savedEntry = entry;
    try {
        await addPendingAudio(entry);
    } catch (e) {
        state.value = "save-failed";
        return;
    }
    await uploadEntry(entry);
}

async function uploadEntry(entry) {
    state.value = "uploading";
    try {
        const res = await uploadAudioRecording(token, entry.parentId, entry.dateFlag, entry.blob, entry.filename);
        await removePendingAudio(entry.id);
        noteId.value = res && res.note && res.note.id ? res.note.id : null;
        state.value = "done";
    } catch (e) {
        state.value = "queued";
    }
}

async function retrySave() {
    if (!savedEntry) {
        return;
    }
    try {
        await addPendingAudio(savedEntry);
    } catch (e) {
        state.value = "save-failed";
        return;
    }
    await uploadEntry(savedEntry);
}

async function handleOnline() {
    if (!token) {
        return;
    }
    try {
        await flushPendingAudio(token);
    } catch (e) {
        // flush never throws; belt and suspenders.
    }
    if (state.value === "queued" && savedEntry) {
        let pending = [];
        try {
            pending = await listPendingAudio();
        } catch (e) {
            pending = [];
        }
        if (!pending.some((p) => p.id === savedEntry.id)) {
            noteId.value = null;
            state.value = "done";
        }
    }
}

async function boot() {
    if (booted || state.value === "invalid") {
        return;
    }
    booted = true;
    try {
        await fetchSession();
    } catch (e) {
        state.value = "needs-auth";
        return;
    }
    try {
        token = await getIngestToken();
    } catch (e) {
        state.value = e && e.status === 401 ? "needs-auth" : "unconfigured";
        return;
    }
    // Retry recordings left pending by earlier sessions.
    flushPendingAudio(token).catch(() => {});
    await requestMic();
}

onMounted(() => {
    window.addEventListener("online", handleOnline);
    boot();
});

onUnmounted(() => {
    window.removeEventListener("online", handleOnline);
    if (timerHandle) {
        clearInterval(timerHandle);
        timerHandle = null;
    }
    if (recorder && recorder.state !== "inactive") {
        try {
            recorder.stop();
        } catch (e) {
            // no-op: stopping may fail without data
        }
    }
    if (stream) {
        stream.getTracks().forEach((t) => t.stop());
    }
});
</script>

<style scoped>
.record-audio {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #01101f;
    color: #e8eef5;
    font-family: system-ui, -apple-system, sans-serif;
    padding: 24px;
    box-sizing: border-box;
}

.card {
    text-align: center;
    max-width: 420px;
    width: 100%;
    padding: 32px 24px;
    border-radius: 12px;
    background: #0b1c2e;
    border: 1px solid #1d3350;
    box-sizing: border-box;
}

.card h1 {
    margin: 0 0 12px;
    font-size: 20px;
}

.card p {
    margin: 0 0 20px;
    line-height: 1.5;
    color: #a9bcd4;
}

.card code {
    background: #0b1c2e;
    padding: 2px 6px;
    border-radius: 4px;
    color: #9ad1ff;
}

.btn {
    display: inline-block;
    padding: 10px 22px;
    border: none;
    border-radius: 8px;
    background: #2b6cb0;
    color: #fff;
    font-size: 15px;
    font-weight: 600;
    cursor: pointer;
    text-decoration: none;
}

.btn:hover {
    background: #3579c2;
}

.btn.stop {
    background: #c0392b;
}

.btn.stop:hover {
    background: #d14a39;
}

.pulse {
    color: #e74c3c;
    font-size: 28px;
    animation: pulse 1.2s ease-in-out infinite;
}

@keyframes pulse {
    0%,
    100% {
        opacity: 1;
    }
    50% {
        opacity: 0.25;
    }
}

.rec-dot {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: #e74c3c;
    margin: 0 auto 16px;
    animation: pulse 1.2s ease-in-out infinite;
}

.elapsed {
    font-size: 44px;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    margin: 0 0 24px;
    color: #e8eef5;
}

.card a:not(.btn) {
    color: #9ad1ff;
}
</style>
