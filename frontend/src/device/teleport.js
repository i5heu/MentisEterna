// DeviceTeleport — the logical E2E "DataChannel" carried over the server's
// same-user websocket relay. All frames are AES-GCM ciphertext (opaque to the
// relay server), keyed per-pairing via ECDH-derived session keys.
//
// Imports sendLiveMessage from ../api.js; api.js does not import this module
// (no cycle). Inbound device.msg frames arrive via window "live:message"
// events and are forwarded to handleDeviceMsg by the view.

import { sendLiveMessage } from "../api.js";
import { b64ToBuf, bufToB64, decryptJSON, encryptJSON } from "./crypto.js";
import { listPeers, sessionKeyForPeer } from "./store.js";

export const CHANNEL = "teleport";
const CHUNK_BYTES = 256 * 1024; // 256 KiB plaintext per chunk
const STALE_MS = 60 * 1000;

const transfers = new Map(); // transfer_id -> { kind, name, mime, size, total, chunks }
let incomingHandler = null; // fn({ kind, name, mime, size, text?, bytes? })
let progressHandler = null; // fn({ peerId, transfer_id, sent, total })
let sweepTimer = null;

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

function sweepStaleTransfers() {
    const now = Date.now();
    for (const [id, t] of transfers) {
        if (now - t.startedAt > STALE_MS) {
            transfers.delete(id);
        }
    }
}

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
 * Handle an inbound device.msg relayed by the server. Returns the assembled
 * item ({ kind, name, mime, size, text?/bytes? }) or null when the frame is
 * not teleport traffic, is from an unpaired peer, or a transfer is still in
 * progress. Throws on decryption failure (tampered/unkeyed payload).
 */
export async function handleDeviceMsg(payload) {
    if (!payload || payload.type !== "device.msg" || payload.channel !== CHANNEL) {
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
