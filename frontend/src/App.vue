<template>
    <RecordAudioView v-if="isRecordAudioPage()" />
    <template v-else>
        <div v-if="!authReady" class="app-loading">Loading…</div>
        <LoginView v-else-if="!token" @logged-in="onLogin" />
        <OptionsView
            v-else-if="currentView === 'options'"
            :token="token"
            :ws-connected="wsConnected"
            :ws-latency="wsLatency"
            :ws-latency-detail="wsLatencyDetail"
            @logout="onLogout"
            @back="currentView = 'notes'"
        />
        <NotesView
            v-else
            :token="token"
            :ws-connected="wsConnected"
            :ws-latency="wsLatency"
            :ws-latency-detail="wsLatencyDetail"
            @logout="onLogout"
            @navigate-options="currentView = 'options'"
        />
        <PairRequestModal />
        <DeviceTeleportModal v-if="teleportModalOpen" />
    </template>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from "vue";
import { fetchSession, startLiveUpdates, stopLiveUpdates } from "./api.js";
import { flushPendingAudio } from "./audio/audioQueue.js";
import { useLiveStatus } from "./composables/useLiveStatus.js";
import { teleportModalOpen } from "./device/teleport.js";
import LoginView from "./views/LoginView.vue";
import NotesView from "./views/NotesView.vue";
import OptionsView from "./views/OptionsView.vue";
import RecordAudioView from "./views/RecordAudioView.vue";
import PairRequestModal from "./components/PairRequestModal.vue";
import DeviceTeleportModal from "./components/DeviceTeleportModal.vue";

const { wsConnected, wsLatency, wsLatencyDetail } = useLiveStatus();
const token = ref("");
const authReady = ref(false);
const currentView = ref("notes");

function isRecordAudioPage() {
    return window.location.pathname.startsWith("/recordaudio/");
}

function onLogin() {
    token.value = "cookie-auth";
    currentView.value = "notes";
}

function onLogout() {
    token.value = "";
    authReady.value = true;
    currentView.value = "notes";
}

function onAuthUnauthorized() {
    onLogout();
}

function onOnline() {
    // Upload recordings left pending by earlier sessions. On the recorder page
    // App's token stays "" (bootstrap early-returns), so this no-ops there; the
    // view has its own listener.
    if (token.value) {
        flushPendingAudio().catch(() => {});
    }
}

async function bootstrapSession() {
    if (isRecordAudioPage()) {
        // The recorder page handles its own session/token boot; skip the
        // duplicate session fetch and websocket churn.
        return;
    }
    authReady.value = false;
    try {
        await fetchSession();
        token.value = "cookie-auth";
        // Retry recordings left pending by previous sessions.
        flushPendingAudio().catch(() => {});
    } catch (e) {
        token.value = "";
    } finally {
        authReady.value = true;
    }
}

watch(
    token,
    (nextToken) => {
        if (nextToken) {
            startLiveUpdates();
        } else {
            stopLiveUpdates();
        }
    },
    { immediate: true },
);

onMounted(() => {
    window.addEventListener("auth:unauthorized", onAuthUnauthorized);
    window.addEventListener("online", onOnline);
    bootstrapSession();
});

onUnmounted(() => {
    window.removeEventListener("auth:unauthorized", onAuthUnauthorized);
    window.removeEventListener("online", onOnline);
    stopLiveUpdates();
});
</script>

<style>
*,
*::before,
*::after {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
}

.app-loading {
    min-height: 100vh;
    display: grid;
    place-items: center;
    color: var(--font-color, #cbe8dc);
}

:root {
    color-scheme: dark;

    /* Background Foundations — near-black with cold blue-green depth */
    --html-bg: #01070c;
    --body-bg: #021219;

    /* Typography */
    --font-color: #cbe8dc;
    --font-color-secondary: #7aa695;
    --header-title-color: #ffb347;
    --heading-color: #ff5c33;
    --pre-text-color: #ffd28a;
    --date-color: #45cfc0;

    /* UI Elements & Accents */
    --border-color: #143c46;
    --accent-teal: #3fe0cf;
    --category-bg-color: #06231d;
    --tag-bg-color: #ffb347;
    --content-type-bg-color: #b23a1a;

    /* Effects */
    --shadow-color: rgba(3, 16, 20, 0.75);
    --showBorder-color: #01070c;

    /* Surface Layers */
    --panel-bg: #041019;
    --raised-bg: #082029;
    --accent-teal-dim: #27968a;

    /* System font — terminal mono everywhere */
    --font-mono: ui-monospace, "JetBrains Mono", "Fira Code", "Cascadia Code",
        "IBM Plex Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace;

    font-size: 15px;
}

html {
    color-scheme: dark;
    background: var(--html-bg);
}

body {
    background: var(--body-bg);
    color: var(--font-color);
    font-family: var(--font-mono);
    letter-spacing: 0.01em;
    min-height: 100vh;
}

/* CRT scanlines over the whole screen (subtle, click-transparent) */
body::after {
    content: "";
    position: fixed;
    inset: 0;
    pointer-events: none;
    background: repeating-linear-gradient(
        0deg,
        rgba(0, 0, 0, 0.18) 0,
        rgba(0, 0, 0, 0.18) 1px,
        transparent 1px,
        transparent 3px
    );
    opacity: 0.3;
}

::selection {
    background: rgba(63, 224, 207, 0.28);
    color: var(--font-color);
}

button {
    cursor: pointer;
    border: none;
    border-radius: 3px;
    font-size: 0.82rem;
    font-family: inherit;
    padding: 0.45rem 1rem;
    transition:
        background 0.15s,
        border-color 0.15s,
        box-shadow 0.15s,
        opacity 0.15s;
}

button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
}

input,
textarea {
    font-family: inherit;
    font-size: 0.92rem;
    background: #030b14;
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    padding: 0.5rem 0.75rem;
    outline: none;
    transition: border-color 0.15s, box-shadow 0.15s;
}

input:focus,
textarea:focus {
    border-color: var(--accent-teal);
    box-shadow: 0 0 0 1px rgba(63, 224, 207, 0.25),
        0 0 14px rgba(63, 224, 207, 0.18);
}

textarea {
    resize: vertical;
    line-height: 1.6;
}

.btn-primary {
    background: rgba(63, 224, 207, 0.12);
    color: var(--accent-teal);
    border: 1px solid rgba(63, 224, 207, 0.45);
    text-transform: uppercase;
    letter-spacing: 0.08em;
}
.btn-primary:hover:not(:disabled) {
    background: rgba(63, 224, 207, 0.22);
    border-color: var(--accent-teal);
    box-shadow: 0 0 12px rgba(63, 224, 207, 0.25);
}

.btn-amber {
    background: var(--tag-bg-color);
    color: var(--html-bg);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
}
.btn-amber:hover:not(:disabled) {
    background: #ffc167;
}

.btn-danger {
    background: var(--heading-color);
    color: #170502;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
}
.btn-danger:hover:not(:disabled) {
    background: #ff744d;
}

.btn-ghost {
    background: transparent;
    color: var(--font-color-secondary);
    border: 1px solid var(--border-color);
    text-transform: uppercase;
    letter-spacing: 0.08em;
}
.btn-ghost:hover:not(:disabled) {
    background: var(--raised-bg);
    color: var(--font-color);
    border-color: var(--accent-teal-dim);
}

.btn-sm {
    padding: 0.2rem 0.5rem;
    font-size: 0.78rem;
}
</style>
