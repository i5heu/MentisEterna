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

// A partial transfer must survive an offline gap of the target device: the
// server queues undelivered chunks for [device] queue_ttl_minutes (default
// 15) and drains them on the target's next hello, so the stale window has to
// exceed the queue TTL for a mid-transfer reconnect to resume cleanly.
const STALE_MS = 20 * 60 * 1000;
const PAIR_REQUEST_TIMEOUT_MS = 60 * 1000;
// The server confirms each relayed device.msg with a device.status frame
// (delivered / queued / refused). If none arrives, the connection (or the
// server) is gone — fail the send instead of assuming success.
const STATUS_TIMEOUT_MS = 15 * 1000;
const MAX_PENDING_PAIR_REQUESTS = 5;

const transfers = new Map(); // transfer_id -> { kind, name, mime, size, total, chunks }
// Recently completed transfer ids: the server redelivers unacked queue rows
// on reconnect (at-least-once), so a fully assembled transfer can arrive a
// second time — drop those duplicates instead of double-delivering to the
// inbox. Bounded; insertion-ordered (oldest evicted first).
const COMPLETED_TRANSFERS_MAX = 100;
const completedTransfers = new Map(); // transfer_id -> true
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
// Per-message delivery status (server → sender)
// ---------------------------------------------------------------------------
// The server acknowledges every relayed device.msg with a device.status frame
// keyed by the client_msg_id we attach to the relay envelope (never inside
// the E2E payload — the server must be able to echo it). A status frame may
// also arrive later for a message we are no longer waiting on (e.g. a
// "delivered" update when a queued message finally reaches an offline peer):
// those are ignored here.

const pendingStatuses = new Map(); // client_msg_id -> { resolve, reject, timer }

function waitForStatus(clientMsgId, timeoutMs = STATUS_TIMEOUT_MS) {
    return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
            if (pendingStatuses.delete(clientMsgId)) {
                reject(
                    new Error(
                        "No confirmation from the server — the connection may be down. Try again.",
                    ),
                );
            }
        }, timeoutMs);
        pendingStatuses.set(clientMsgId, { resolve, reject, timer });
    });
}

/**
 * Inbound device.status frame (see api.js forwarder). Resolves the waiting
 * sender for "delivered"/"queued" and rejects it on "refused" (unknown
 * device, cross-user target, queue full, offline pairing target).
 */
export function handleDeviceStatus(payload) {
    if (!payload || payload.type !== "device.status" || !payload.client_msg_id) {
        return;
    }
    const entry = pendingStatuses.get(payload.client_msg_id);
    if (!entry) {
        return; // late update for a message nobody is awaiting
    }
    pendingStatuses.delete(payload.client_msg_id);
    clearTimeout(entry.timer);
    if (payload.status === "refused") {
        entry.reject(new Error(payload.error || "The server refused the message."));
        return;
    }
    entry.resolve(payload.status || "delivered");
}

// ---------------------------------------------------------------------------
// Pairing handshake (mutual consent)
// ---------------------------------------------------------------------------

/**
 * Request a pairing with the device described by a validated share payload
 * (see pairing.js parseSharePayload). The request is signed with this
 * device's private key and relayed to the target, which shows a confirmation
 * prompt. pairingState tracks the outcome; on accept both sides persist each
 * other as peers. The relay is live-only for the pairing channel, so an
 * offline target refuses immediately instead of leaving the requester to
 * wait out the full timeout.
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

    const statusWait = waitForStatus(request.request_id);
    sendLiveMessage({
        type: "device.msg",
        to_device_id: payload.id,
        channel: PAIR_CHANNEL,
        client_msg_id: request.request_id,
        data: JSON.stringify(request),
    });
    // Fast-fail without blocking the caller: the pairing channel is
    // live-only, so a refusal (target offline / unknown device) or a dead
    // relay settles the pending state immediately instead of burning the
    // full response timeout.
    statusWait.catch((err) => {
        const state = pairingState.value;
        if (
            !state ||
            state.requestId !== request.request_id ||
            state.state !== "pending"
        ) {
            return; // already resolved (accepted/declined) or superseded
        }
        clearPairTimeout();
        pairingState.value = {
            state: "error",
            requestId: request.request_id,
            targetName: payload.name || "Device",
            targetId: payload.id,
            error: err.message,
        };
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
        // Already paired: re-confirm without a prompt. A prior pairing can
        // end up one-sided (the accept response was refused because this
        // device went offline mid-handshake), and a re-request is how the
        // other side recovers — prompting again for a device the user
        // already confirmed would be noise.
        if (listPeers().some((p) => p.id === msg.id)) {
            const me = await getOrCreateDevice();
            await addPeer(msg.name || "Device", msg.pub);
            notifyPeersChanged();
            reannounceDevice();
            const response = {
                kind: "pair.response",
                v: 1,
                request_id: msg.request_id,
                accepted: true,
                name: getMyName() || "Device",
                id: me.id,
                pub: me.publicKeyJwk,
            };
            response.sig = await signPayload(me.privateKeyJwk, response);
            sendLiveMessage({
                type: "device.msg",
                to_device_id: msg.id,
                channel: PAIR_CHANNEL,
                data: JSON.stringify(response),
            });
            return null;
        }
        if (pendingPairRequests.value.length >= MAX_PENDING_PAIR_REQUESTS) {
            return null; // prompt backlog cap
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
// Teleport channel (E2E-encrypted, queued offline)
// ---------------------------------------------------------------------------

/**
 * Send one E2E-encrypted chunk through the relay and wait for the server's
 * per-message status: resolves "delivered" (handed to the peer's live
 * connection) or "queued" (peer offline — persisted until it reconnects or
 * the queue TTL expires), rejects on "refused" or when no confirmation
 * arrives. Awaiting each chunk gives strict FIFO ordering and real
 * backpressure against the server's per-device queue caps.
 */
async function sendChunk(peer, frame) {
    const key = await sessionKeyForPeer(peer);
    const data = await encryptJSON(key, frame);
    const clientMsgId = crypto.randomUUID();
    const statusWait = waitForStatus(clientMsgId);
    sendLiveMessage({
        type: "device.msg",
        to_device_id: peer.id,
        channel: CHANNEL,
        client_msg_id: clientMsgId,
        data,
    });
    return statusWait;
}

async function sendBytes(peer, meta, bytes) {
    const transferId = crypto.randomUUID();
    const total = Math.ceil(bytes.length / CHUNK_BYTES);
    let sent = 0;
    let delivered = 0;
    let queued = 0;
    for (let seq = 0; seq < total; seq++) {
        const start = seq * CHUNK_BYTES;
        const chunk = bytes.subarray(start, start + CHUNK_BYTES);
        const status = await sendChunk(peer, {
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
        if (status === "queued") {
            queued += 1;
        } else {
            delivered += 1;
        }
        if (progressHandler) {
            progressHandler({
                peerId: peer.id,
                transfer_id: transferId,
                sent,
                total,
                status,
            });
        }
    }
    return { transferId, total, delivered, queued };
}

/**
 * Send text to a paired peer over the E2E channel. Works whether the peer is
 * online (delivered immediately) or offline (queued server-side until its
 * next hello, bounded by the queue TTL). Resolves with the per-chunk
 * delivery summary { transferId, total, delivered, queued }.
 */
export async function sendText(peer, text) {
    return sendBytes(
        peer,
        { kind: "text", name: "", mime: "text/plain" },
        new TextEncoder().encode(text),
    );
}

/** Send a File to a paired peer over the E2E channel (see sendText). */
export async function sendFile(peer, file) {
    const bytes = new Uint8Array(await file.arrayBuffer());
    return sendBytes(
        peer,
        { kind: "file", name: file.name, mime: file.type || "application/octet-stream" },
        bytes,
    );
}

/**
 * Handle an inbound device.msg relayed by the server. Pairing messages are
 * processed (they must work from unpaired senders); teleport frames are only
 * accepted from paired peers. Returns the assembled teleport item
 * ({ kind, name, mime, size, text?/bytes? }) or null when the frame is not
 * teleport traffic, is from an unpaired peer, or a transfer is still in
 * progress. Throws on teleport decryption failure (tampered/unkeyed payload).
 *
 * Queued frames (relayed from the server's offline queue) carry a queue_id:
 * whatever happens during processing, the frame is acked afterwards so the
 * server retires the row — an unprocessable frame must not clog the queue
 * forever (redelivery is idempotent, so a lost ack only causes a harmless
 * duplicate).
 */
export async function handleDeviceMsg(payload) {
    try {
        return await dispatchDeviceMsg(payload);
    } finally {
        ackDeviceMsg(payload);
    }
}

function ackDeviceMsg(payload) {
    if (
        payload &&
        payload.type === "device.msg" &&
        typeof payload.queue_id === "number" &&
        payload.queue_id > 0
    ) {
        sendLiveMessage({ type: "device.ack", queue_id: payload.queue_id });
    }
}

async function dispatchDeviceMsg(payload) {
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
    if (completedTransfers.has(frame.transfer_id)) {
        return null; // duplicate redelivery of an already-assembled transfer
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
    // Completion requires full coverage of [0, total): a transfer that was
    // interrupted (peer went offline mid-send) resumes from the server queue
    // when the peer returns, and redelivered chunks just overwrite their
    // slots — but a hole means the transfer can never assemble correctly.
    // (Indexed access, not Array#every: array iteration skips holes in
    // sparse arrays, and out-of-order chunk arrival leaves holes.)
    let complete = entry.total > 0 && entry.chunks.length >= entry.total;
    for (let i = 0; complete && i < entry.total; i++) {
        complete = Boolean(entry.chunks[i]);
    }
    if (complete) {
        transfers.delete(frame.transfer_id);
        completedTransfers.set(frame.transfer_id, true);
        if (completedTransfers.size > COMPLETED_TRANSFERS_MAX) {
            const oldest = completedTransfers.keys().next().value;
            completedTransfers.delete(oldest);
        }
        const merged = new Uint8Array(entry.size);
        let offset = 0;
        for (let i = 0; i < entry.total; i++) {
            const chunk = entry.chunks[i];
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
