<template>
    <div class="modal-overlay" @click.self="closeTeleportModal">
        <div class="teleport-modal">
            <div class="modal-header">
                <h2>Device Teleport</h2>
                <button
                    class="btn-ghost modal-close-btn"
                    @click="closeTeleportModal"
                >
                    ✕
                </button>
            </div>
            <div class="modal-body">
                <p v-if="!peers.length" class="teleport-empty">
                    No paired devices yet — open Settings → Devices &amp; Teleport
                    to pair this browser with your other devices.
                </p>

                <!-- Paired devices -->
                <div v-if="peers.length" class="device-card">
                    <div class="device-card-header">
                        <span class="device-card-title">Paired devices</span>
                    </div>
                    <div
                        v-for="peer in peers"
                        :key="peer.id"
                        class="peer-row"
                    >
                        <span
                            class="peer-status-dot"
                            :class="
                                onlinePeerIds.has(peer.id)
                                    ? 'peer-online'
                                    : 'peer-offline'
                            "
                            :title="
                                onlinePeerIds.has(peer.id)
                                    ? 'Online'
                                    : 'Offline'
                            "
                        ></span>
                        <span class="peer-name">{{
                            peer.name || "Device"
                        }}</span>
                        <code class="peer-fp">{{
                            shortFingerprint(peer.id)
                        }}</code>
                        <button
                            class="btn-ghost peer-remove"
                            @click="removePaired(peer.id)"
                        >
                            Remove
                        </button>
                    </div>
                </div>

                <!-- Teleport: send -->
                <div v-if="onlinePeers.length" class="device-card">
                    <div class="device-card-header">
                        <span class="device-card-title">Send to device</span>
                    </div>
                    <div
                        v-for="peer in onlinePeers"
                        :key="peer.id"
                        class="teleport-peer"
                    >
                        <div class="teleport-peer-header">
                            <span class="peer-name">{{
                                peer.name || "Device"
                            }}</span>
                            <span
                                v-if="progress[peer.id]"
                                class="teleport-progress"
                                >{{ progress[peer.id] }}</span
                            >
                        </div>
                        <textarea
                            v-model="textDrafts[peer.id]"
                            class="teleport-text"
                            rows="2"
                            placeholder="Message text…"
                        ></textarea>
                        <div class="device-actions">
                            <button
                                class="btn-ghost"
                                :disabled="sendingTo[peer.id]"
                                @click="sendTextTo(peer)"
                            >
                                Send text
                            </button>
                            <button
                                class="btn-ghost"
                                :disabled="sendingTo[peer.id]"
                                @click="pickFileFor(peer)"
                            >
                                Send file…
                            </button>
                        </div>
                        <p v-if="sendErrs[peer.id]" class="msg-error">{{
                            sendErrs[peer.id]
                        }}</p>
                    </div>
                    <input
                        ref="fileInput"
                        type="file"
                        class="hidden-file-input"
                        @change="fileChosen"
                    />
                </div>

                <!-- Incoming -->
                <div v-if="incomingItems.length" class="device-card">
                    <div class="device-card-header">
                        <span class="device-card-title">Incoming</span>
                    </div>
                    <div
                        v-for="(item, idx) in incomingItems"
                        :key="idx"
                        class="incoming-item"
                        :class="{ 'incoming-new': idx === 0 }"
                    >
                        <div class="incoming-head">
                            <span class="incoming-kind">{{
                                item.kind === "file" ? "File" : "Text"
                            }}</span>
                            <span class="peer-name">{{
                                item.kind === "file"
                                    ? item.name
                                    : "Text message"
                            }}</span>
                            <span class="incoming-meta">{{
                                item.kind === "file"
                                    ? formatBytes(item.size)
                                    : item.size + " chars"
                            }}</span>
                        </div>
                        <pre v-if="item.kind === 'text'" class="incoming-text">{{
                            item.text
                        }}</pre>
                        <div class="device-actions">
                            <button
                                v-if="item.kind === 'text'"
                                class="btn-ghost"
                                @click="copyIncoming(item)"
                            >
                                Copy
                            </button>
                            <button
                                v-if="item.kind === 'file'"
                                class="btn-ghost"
                                @click="downloadIncoming(item)"
                            >
                                Download
                            </button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";
import { sendLiveMessage } from "../api.js";
import { getOrCreateDevice, listPeers, removePeer } from "../device/store.js";
import {
    closeTeleportModal,
    sendFile,
    sendText,
    setIncomingHandler,
    setProgressHandler,
} from "../device/teleport.js";

const peers = ref([]);
const onlinePeerIds = ref(new Set());
const textDrafts = reactive({});
const sendingTo = reactive({});
const progress = reactive({});
const sendErrs = reactive({});
const incomingItems = ref([]);
const fileInput = ref(null);
let pendingFilePeer = null;

const onlinePeers = computed(() =>
    peers.value.filter((p) => onlinePeerIds.value.has(p.id)),
);

function shortFingerprint(id) {
    if (!id) return "";
    return id.slice(0, 8) + "…" + id.slice(-8);
}

function refreshPeers() {
    peers.value = listPeers();
}

function formatBytes(bytes) {
    if (bytes == null || bytes < 0) return "N/A";
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const val = bytes / Math.pow(1024, i);
    return val.toFixed(val < 10 ? 1 : 0) + " " + units[i];
}

function removePaired(id) {
    removePeer(id);
    const next = new Set(onlinePeerIds.value);
    next.delete(id);
    onlinePeerIds.value = next;
    refreshPeers();
}

async function sendTextTo(peer) {
    sendingTo[peer.id] = true;
    sendErrs[peer.id] = "";
    progress[peer.id] = "";
    try {
        await sendText(peer, textDrafts[peer.id] || "");
        textDrafts[peer.id] = "";
        progress[peer.id] = "Sent.";
    } catch (e) {
        sendErrs[peer.id] = e.message || "Send failed";
    } finally {
        sendingTo[peer.id] = false;
    }
}

function pickFileFor(peer) {
    pendingFilePeer = peer;
    if (fileInput.value) {
        fileInput.value.value = "";
        fileInput.value.click();
    }
}

async function fileChosen(event) {
    const file = event.target.files && event.target.files[0];
    if (fileInput.value) {
        fileInput.value.value = "";
    }
    const peer = pendingFilePeer;
    pendingFilePeer = null;
    if (!file || !peer) {
        return;
    }
    sendingTo[peer.id] = true;
    sendErrs[peer.id] = "";
    progress[peer.id] = "";
    try {
        await sendFile(peer, file);
        progress[peer.id] = "Sent.";
    } catch (e) {
        sendErrs[peer.id] = e.message || "Send failed";
    } finally {
        sendingTo[peer.id] = false;
    }
}

async function copyIncoming(item) {
    try {
        await navigator.clipboard.writeText(item.text);
    } catch {
        // Clipboard may be unavailable; nothing else to do.
    }
}

function downloadIncoming(item) {
    const url = URL.createObjectURL(
        new Blob([item.bytes], { type: item.mime }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = item.name || "teleport-file";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function onLiveMsg(event) {
    const payload = event.detail;
    if (!payload) {
        return;
    }
    if (payload.type === "device.online") {
        if (peers.value.some((p) => p.id === payload.device_id)) {
            onlinePeerIds.value = new Set(onlinePeerIds.value).add(
                payload.device_id,
            );
        }
        return;
    }
    if (payload.type === "device.offline") {
        const next = new Set(onlinePeerIds.value);
        next.delete(payload.device_id);
        onlinePeerIds.value = next;
        return;
    }
    // device.msg frames are forwarded to the device channel by api.js
    // (single forwarder) — teleport reassembly and pairing confirmations
    // work from any view; this modal only reads their results.
}

function onTeleportProgress({ peerId, sent, total }) {
    progress[peerId] = `Sent ${sent}/${total} chunks…`;
}

onMounted(async () => {
    window.addEventListener("live:message", onLiveMsg);
    window.addEventListener("device:peers-changed", refreshPeers);
    setIncomingHandler((item) => {
        incomingItems.value.unshift({ ...item, receivedAt: Date.now() });
    });
    setProgressHandler(onTeleportProgress);
    refreshPeers();
    // Re-announce so presence converges immediately (the hello returns the
    // same-user registry to this connection — see the server presence sync).
    try {
        const me = await getOrCreateDevice();
        sendLiveMessage({ type: "device.hello", device_id: me.id });
    } catch {
        // Device identity unavailable (no WebCrypto); teleport stays disabled.
    }
});

onUnmounted(() => {
    window.removeEventListener("live:message", onLiveMsg);
    window.removeEventListener("device:peers-changed", refreshPeers);
    setIncomingHandler(null);
    setProgressHandler(null);
});
</script>

<style scoped>
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 2050;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
}

.teleport-modal {
    background: var(--panel-bg, #061320);
    border: 1px solid var(--border-color, #7e7567);
    border-radius: 12px;
    width: 100%;
    max-width: 560px;
    max-height: min(80vh, 720px);
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 30px var(--shadow-color, rgba(0, 0, 0, 0.6));
}

.modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border-color, #7e7567);
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
}

.teleport-empty {
    margin: 0;
    font-size: 0.85rem;
    color: var(--font-color-secondary, #a5b0ad);
    line-height: 1.5;
}

/* Devices & Teleport */
.device-card {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.75rem 0.9rem;
    margin-bottom: 0.9rem;
}

.device-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-bottom: 0.5rem;
}

.device-card-title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color);
}

.device-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.5rem;
}

.teleport-text {
    width: 100%;
    box-sizing: border-box;
    background: var(--bg-color, transparent);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    color: var(--font-color);
    font-size: 0.8rem;
    padding: 0.4rem 0.5rem;
    resize: vertical;
}

.peer-row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border-color);
}

.peer-row:last-child {
    border-bottom: none;
}

.peer-status-dot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex-shrink: 0;
}

.peer-status-dot.peer-online {
    background: #22c55e;
    box-shadow: 0 0 4px #22c55e;
}

.peer-status-dot.peer-offline {
    background: #6b7280;
}

.peer-name {
    font-size: 0.8rem;
    color: var(--font-color);
    font-weight: 500;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.peer-fp {
    font-size: 0.7rem;
    color: var(--font-color-secondary);
    flex-shrink: 0;
}

.peer-remove {
    flex-shrink: 0;
}

.teleport-peer {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.6rem 0.7rem;
    margin-bottom: 0.6rem;
}

.teleport-peer-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-bottom: 0.4rem;
}

.teleport-progress {
    font-size: 0.72rem;
    color: var(--accent-teal);
}

.hidden-file-input {
    display: none;
}

.incoming-item {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.6rem 0.7rem;
    margin-bottom: 0.6rem;
}

.incoming-new {
    border-color: var(--accent-teal);
}

.incoming-head {
    display: flex;
    align-items: center;
    gap: 0.6rem;
}

.incoming-kind {
    font-size: 0.7rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent-teal);
    flex-shrink: 0;
}

.incoming-meta {
    font-size: 0.72rem;
    color: var(--font-color-secondary);
    flex-shrink: 0;
}

.incoming-text {
    background: var(--bg-color, transparent);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 0.4rem 0.5rem;
    font-size: 0.78rem;
    color: var(--font-color);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 160px;
    overflow-y: auto;
    margin: 0.4rem 0 0;
}

.msg-error {
    color: var(--heading-color);
    font-size: 0.8rem;
    margin: 0;
}
</style>
