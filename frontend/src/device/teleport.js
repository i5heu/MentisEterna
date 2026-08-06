// DeviceTeleport — the logical E2E "DataChannel" carried over the server's
// same-user websocket relay. All teleport frames are AES-GCM ciphertext
// (opaque to the relay server), keyed per-pairing via ECDH-derived session
// keys. Pairing itself is a mutual-consent handshake over the same relay:
// the scanner sends a signed pair.request to the target device; the target
// shows a confirmation prompt and replies pair.response; only on accept do
// BOTH sides persist each other as peers (bidirectional).
//
// Imports sendLiveMessage from ../api.js; api.js dynamically imports this
// module to forward inbound device.msg frames (no static cycle). Inbound
// frames also arrive via window "live:message" events for views that want
// the raw stream (presence).

import { ref } from "vue";
import { sendLiveMessage } from "../api.js";
import {
    b64ToBuf,
    bufToB64,
    decryptJSON,
    encryptJSON,
    fingerprint,
    signPayload,
    verifyPayload,
} from "./crypto.js";
import {
    addPeer,
    getMyName,
    getOrCreateDevice,
    listPeers,
    sessionKeyForPeer,
} from "./store.js";

export const CHANNEL = "teleport";
export const PAIR_CHANNEL = "pairing";
const CHUNK_BYTES = 256 * 1024; // 256 KiB plaintext per chunk
const STALE_MS = 60 * 1000;
const PAIR_REQUEST_TIMEOUT_MS = 60 * 1000;
const MAX_PENDING_PAIR_REQUESTS = 5;

const transfers = new Map(); // transfer_id -> { kind, name, mime, size, total, chunks }
let incomingHandler = null; // fn({ kind, name, mime, size, text?, bytes? })
let progressHandler = null; // fn({ peerId, transfer_id, sent, total })
let sweepTimer = null;
let pairTimeout = null; // requester-side request timeout

// Target-side: pairing requests awaiting the local user's confirmation.
// Each entry: { requestId, name, id, pub, receivedAt }.
export const pendingPairRequests = ref([]);

// Requester-side: status of the pairing attempt this device started.
// { state: "pending" | "accepted" | "declined" | "error", requestId,
//   targetName, targetId, error? } — null when idle.
export const pairingState = ref(null);

// Teleport modal visibility (opened from the app title; App.vue hosts the
// modal). Module-level so any view can open it without prop drilling.
export const teleportModalOpen = ref(false);

export function openTeleportModal() {
    teleportModalOpen.value = true;
}

export function closeTeleportModal() {
    teleportModalOpen.value = false;
}

export function setIncomingHandler(fn) {
    incomingHandler = fn;
    if (fn && !sweepTimer) {
        sweepTimer = setInterval(sweepStaleTransfers, 15 * 1000);
    } else if (!fn && sweepTimer) {
        clearInterval(sweepTimer);
        sweepTimer = null;
        transfers.clear();
    }
}

export function setProgressHandler(fn) {
    progressHandler = fn;
}

function notifyPeersChanged() {
    if (typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent("device:peers-changed"));
    }
}

// Re-announce this device after a pairing accept. A hello has two effects:
// the server broadcasts device.online to same-user peers AND returns the
// current registry to the announcer — so one re-hello converges presence in
// both directions right after the new peer was added (device.online
// broadcasts alone would have been filtered before the peer existed).
function reannounceDevice() {
    getOrCreateDevice()
        .then((me) =>
            sendLiveMessage({ type: "device.hello", device_id: me.id }),
        )
        .catch(() => {});
}

function sweepStaleTransfers() {
    const now = Date.now();
    for (const [id, t] of transfers) {
        if (now - t.startedAt > STALE_MS) {
            transfers.delete(id);
        }
    }
}

// ---------------------------------------------------------------------------
// Pairing handshake (mutual consent)
// ---------------------------------------------------------------------------

/**
 * Request a pairing with the device described by a validated share payload
 * (see pairing.js parseSharePayload). The request is signed with this
 * device's private key and relayed to the target, which shows a confirmation
 * prompt. pairingState tracks the outcome; on accept both sides persist each
 * other as peers.
 */
export async function sendPairRequest(payload) {
    const me = await getOrCreateDevice();
    if (payload.id === me.id) {
        throw new TypeError("That is this device's own pairing code.");
    }
    const request = {
        kind: "pair.request",
        v: 1,
        request_id: crypto.randomUUID(),
        name: getMyName() || "Device",
        id: me.id,
        pub: me.publicKeyJwk,
    };
    request.sig = await signPayload(me.privateKeyJwk, request);

    pairingState.value = {
        state: "pending",
        requestId: request.request_id,
        targetName: payload.name || "Device",
        targetId: payload.id,
    };
    clearPairTimeout();
    pairTimeout = setTimeout(() => {
        if (
            pairingState.value &&
            pairingState.value.requestId === request.request_id
        ) {
            pairingState.value = {
                state: "error",
                requestId: request.request_id,
                targetName: payload.name || "Device",
                targetId: payload.id,
                error: `No response from ${payload.name || "device"} — is it online and logged into the same account?`,
            };
        }
        pairTimeout = null;
    }, PAIR_REQUEST_TIMEOUT_MS);

    sendLiveMessage({
        type: "device.msg",
        to_device_id: payload.id,
        channel: PAIR_CHANNEL,
        data: JSON.stringify(request),
    });
    return request.request_id;
}

/** Requester cancels a pending request (also closes the prompt on the target). */
export function cancelPairing() {
    const state = pairingState.value;
    clearPairTimeout();
    pairingState.value = null;
    if (state && state.state === "pending") {
        sendLiveMessage({
            type: "device.msg",
            to_device_id: state.targetId,
            channel: PAIR_CHANNEL,
            data: JSON.stringify({
                kind: "pair.response",
                v: 1,
                request_id: state.requestId,
                accepted: false,
            }),
        });
    }
}

function clearPairTimeout() {
    if (pairTimeout) {
        clearTimeout(pairTimeout);
        pairTimeout = null;
    }
}

/**
 * Target responds to a pending request. On accept this device persists the
 * requester as a peer (the requester persists this device when it receives
 * the accept), so a single confirmation connects both directions.
 */
export async function respondToPairRequest(requestId, accepted) {
    const idx = pendingPairRequests.value.findIndex(
        (r) => r.requestId === requestId,
    );
    if (idx < 0) {
        return;
    }
    const [entry] = pendingPairRequests.value.splice(idx, 1);
    const me = await getOrCreateDevice();
    if (accepted) {
        await addPeer(entry.name || "Device", entry.pub);
        notifyPeersChanged();
        reannounceDevice();
    }
    const response = {
        kind: "pair.response",
        v: 1,
        request_id: requestId,
        accepted,
        name: getMyName() || "Device",
        id: me.id,
        pub: me.publicKeyJwk,
    };
    // Decline carries no identity claim, so only accept is signed.
    if (accepted) {
        response.sig = await signPayload(me.privateKeyJwk, response);
    }
    sendLiveMessage({
        type: "device.msg",
        to_device_id: entry.id,
        channel: PAIR_CHANNEL,
        data: JSON.stringify(response),
    });
}

/** Inbound pairing message (channel "pairing", plaintext JSON — public data). */
async function handlePairingMsg(payload) {
    let msg;
    try {
        msg = JSON.parse(payload.data || "");
    } catch {
        return null;
    }
    if (!msg || msg.v !== 1) {
        return null;
    }
    if (msg.kind === "pair.request") {
        // The server stamps from_device_id from the sender's registration, so
        // it must match the id the request claims.
        if (
            !msg.request_id ||
            !msg.pub ||
            !msg.pub.x ||
            payload.from_device_id !== msg.id ||
            pendingPairRequests.value.length >= MAX_PENDING_PAIR_REQUESTS ||
            pendingPairRequests.value.some((r) => r.requestId === msg.request_id)
        ) {
            return null;
        }
        const [idOk, sigOk] = await Promise.all([
            fingerprint(msg.pub)
                .then((f) => f === msg.id)
                .catch(() => false),
            verifyPayload(msg.pub, msg, msg.sig),
        ]);
        if (!idOk || !sigOk) {
            return null; // forged or malformed: ignore
        }
        pendingPairRequests.value.push({
            requestId: msg.request_id,
            name: msg.name || "Device",
            id: msg.id,
            pub: msg.pub,
            receivedAt: Date.now(),
        });
        return null;
    }
    if (msg.kind === "pair.response") {
        // A cancel from the requester closes this device's open prompt.
        const qi = pendingPairRequests.value.findIndex(
            (r) => r.requestId === msg.request_id,
        );
        if (qi >= 0) {
            pendingPairRequests.value.splice(qi, 1);
        }
        const state = pairingState.value;
        if (!state || state.requestId !== msg.request_id) {
            return null;
        }
        clearPairTimeout();
        if (msg.accepted !== true) {
            pairingState.value = {
                state: "declined",
                requestId: msg.request_id,
                targetName: state.targetName,
                targetId: state.targetId,
            };
            return null;
        }
        // Accept must authenticate as the exact device we requested.
        if (msg.id !== state.targetId || !msg.pub || !msg.pub.x) {
            pairingState.value = {
                state: "error",
                requestId: msg.request_id,
                targetName: state.targetName,
                targetId: state.targetId,
                error: "The pairing response did not match the requested device.",
            };
            return null;
        }
        const [idOk, sigOk] = await Promise.all([
            fingerprint(msg.pub)
                .then((f) => f === msg.id)
                .catch(() => false),
            verifyPayload(msg.pub, msg, msg.sig),
        ]);
        if (!idOk || !sigOk) {
            pairingState.value = {
                state: "error",
                requestId: msg.request_id,
                targetName: state.targetName,
                targetId: state.targetId,
                error: "The pairing response could not be verified.",
            };
            return null;
        }
        await addPeer(msg.name || "Device", msg.pub);
        notifyPeersChanged();
        reannounceDevice();
        pairingState.value = {
            state: "accepted",
            requestId: msg.request_id,
            targetName: msg.name || "Device",
            targetId: msg.id,
        };
        return null;
    }
    return null;
}

// ---------------------------------------------------------------------------
// Teleport channel (E2E-encrypted)
// ---------------------------------------------------------------------------

function sendChunk(peer, frame) {
    return sessionKeyForPeer(peer).then((key) =>
        encryptJSON(key, frame).then((data) => {
            sendLiveMessage({
                type: "device.msg",
                to_device_id: peer.id,
                channel: CHANNEL,
                data,
            });
        }),
    );
}

async function sendBytes(peer, meta, bytes) {
    const transferId = crypto.randomUUID();
    const total = Math.ceil(bytes.length / CHUNK_BYTES);
    let sent = 0;
    for (let seq = 0; seq < total; seq++) {
        const start = seq * CHUNK_BYTES;
        const chunk = bytes.subarray(start, start + CHUNK_BYTES);
        // Sequential awaits preserve order; the single ordered websocket
        // connection means no per-chunk ACK is needed.
        await sendChunk(peer, {
            op: "chunk",
            transfer_id: transferId,
            kind: meta.kind,
            name: meta.name,
            mime: meta.mime,
            size: bytes.length,
            total,
            seq,
            data: bufToB64(chunk),
        });
        sent += 1;
        if (progressHandler) {
            progressHandler({ peerId: peer.id, transfer_id: transferId, sent, total });
        }
    }
    return transferId;
}

/** Send text to a paired peer over the E2E channel. */
export async function sendText(peer, text) {
    return sendBytes(peer, { kind: "text", name: "", mime: "text/plain" }, new TextEncoder().encode(text));
}

/** Send a File to a paired peer over the E2E channel. */
export async function sendFile(peer, file) {
    const bytes = new Uint8Array(await file.arrayBuffer());
    return sendBytes(peer, { kind: "file", name: file.name, mime: file.type || "application/octet-stream" }, bytes);
}

/**
 * Handle an inbound device.msg relayed by the server. Pairing messages are
 * processed (they must work from unpaired senders); teleport frames are only
 * accepted from paired peers. Returns the assembled teleport item
 * ({ kind, name, mime, size, text?/bytes? }) or null when the frame is not
 * teleport traffic, is from an unpaired peer, or a transfer is still in
 * progress. Throws on teleport decryption failure (tampered/unkeyed payload).
 */
export async function handleDeviceMsg(payload) {
    if (!payload || payload.type !== "device.msg") {
        return null;
    }
    if (payload.channel === PAIR_CHANNEL) {
        return handlePairingMsg(payload);
    }
    if (payload.channel !== CHANNEL) {
        return null;
    }
    const peer = listPeers().find((p) => p.id === payload.from_device_id);
    if (!peer) {
        return null; // unpaired sender: ignore
    }
    const frame = await decryptJSON(await sessionKeyForPeer(peer), payload.data);
    if (frame.op !== "chunk") {
        return null;
    }
    let entry = transfers.get(frame.transfer_id);
    if (!entry) {
        entry = {
            kind: frame.kind,
            name: frame.name || "",
            mime: frame.mime || "",
            size: frame.size || 0,
            total: frame.total || 0,
            chunks: [],
            startedAt: Date.now(),
        };
        transfers.set(frame.transfer_id, entry);
    }
    entry.chunks[frame.seq] = b64ToBuf(frame.data);
    if (entry.chunks.filter(Boolean).length >= entry.total) {
        transfers.delete(frame.transfer_id);
        const merged = new Uint8Array(entry.size);
        let offset = 0;
        for (const chunk of entry.chunks) {
            merged.set(chunk, offset);
            offset += chunk.length;
        }
        if (entry.kind === "file") {
            const item = { kind: "file", name: entry.name, mime: entry.mime, size: entry.size, bytes: merged };
            if (incomingHandler) incomingHandler(item);
            return item;
        }
        const item = { kind: "text", name: "", mime: "text/plain", size: entry.size, text: new TextDecoder().decode(merged) };
        if (incomingHandler) incomingHandler(item);
        return item;
    }
    return null;
}
