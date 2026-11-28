// Teleport queue semantics (node jest env): per-chunk delivery status
// correlation, inbound queue acks, redelivery dedup, and refusal handling.
//
// store.js persists to localStorage, which does not exist in node, so this
// file installs a tiny in-memory shim. api.js is mocked (teleport.js imports
// sendLiveMessage from it), so the tests drive the channel purely through
// the module functions and the captured relayed messages.

const storage = new Map();
global.localStorage = {
    getItem: (k) => (storage.has(k) ? storage.get(k) : null),
    setItem: (k, v) => storage.set(k, String(v)),
    removeItem: (k) => storage.delete(k),
    clear: () => storage.clear(),
};

import { jest } from "@jest/globals";
import {
    deriveSessionKey,
    encryptJSON,
    fingerprint,
    generateDeviceKeyPair,
} from "../crypto.js";
import { addPeer, getOrCreateDevice, listPeers } from "../store.js";
import {
    handleDeviceMsg,
    handleDeviceStatus,
    sendFile,
    sendText,
    setIncomingHandler,
    setProgressHandler,
} from "../teleport.js";
import { sendLiveMessage } from "../../api.js";

jest.mock("../../api.js", () => ({
    sendLiveMessage: jest.fn(),
}));

function sentEnvelopes() {
    return sendLiveMessage.mock.calls.map(([payload]) => payload);
}

function teleportEnvelopes() {
    return sentEnvelopes().filter(
        (m) => m.type === "device.msg" && m.channel === "teleport",
    );
}

// The send path awaits a per-chunk device.status; the harness delivers the
// given outcome as soon as the relayed envelope shows up.
async function deliverStatusForLastChunk(status, extra = {}) {
    for (let i = 0; i < 200; i++) {
        const envelopes = teleportEnvelopes();
        if (envelopes.length > 0) {
            const last = envelopes[envelopes.length - 1];
            if (last.client_msg_id && !last.__statusDelivered) {
                last.__statusDelivered = true;
                handleDeviceStatus({
                    type: "device.status",
                    client_msg_id: last.client_msg_id,
                    status,
                    ...extra,
                });
                return last;
            }
        }
        await new Promise((r) => setTimeout(r, 5));
    }
    throw new Error("no teleport envelope arrived");
}

// Deliver a queued inbound teleport chunk: encrypt a chunk frame with the
// shared session key (from the remote peer's side) and hand it to the device
// channel as the server would (including the queue id to ack).
async function deliverInboundChunk(remote, frame, queueId) {
    const me = await getOrCreateDevice();
    const key = await deriveSessionKey(remote.privateKeyJwk, me.publicKeyJwk);
    const data = await encryptJSON(key, frame);
    return handleDeviceMsg({
        type: "device.msg",
        from_device_id: await fingerprint(remote.publicKeyJwk),
        channel: "teleport",
        queue_id: queueId,
        data,
    });
}

function fakeFile(bytes, name = "blob.bin", mime = "application/octet-stream") {
    return {
        name,
        type: mime,
        arrayBuffer: async () => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    };
}

// Two-device setup: this browser's identity plus a paired remote peer.
async function pairedRemote() {
    const remote = await generateDeviceKeyPair();
    const remoteId = await fingerprint(remote.publicKeyJwk);
    const peer = await addPeer("Remote Dev", remote.publicKeyJwk);
    return { remote, remoteId, peer };
}

describe("teleport queue send/ack semantics", () => {
    beforeEach(() => {
        storage.clear();
        sendLiveMessage.mockClear();
        setIncomingHandler(null);
        setProgressHandler(null);
    });

    test("chunks carry a client_msg_id and resolve on their delivered status", async () => {
        const { peer } = await pairedRemote();
        const progressEvents = [];
        setProgressHandler((e) => progressEvents.push(e));

        const sent = sendText(peer, "hello queue");
        const envelope = await deliverStatusForLastChunk("delivered");
        expect(envelope.to_device_id).toBe(peer.id);
        expect(typeof envelope.client_msg_id).toBe("string");

        const summary = await sent;
        expect(summary).toMatchObject({ total: 1, delivered: 1, queued: 0 });
        expect(summary.transferId).toEqual(expect.any(String));
        expect(progressEvents.at(-1)).toMatchObject({
            peerId: peer.id,
            sent: 1,
            total: 1,
            status: "delivered",
        });
    });

    test("an offline peer's chunks resolve as queued with a summary", async () => {
        const { peer } = await pairedRemote();
        const sent = sendText(peer, "offline but queued");
        await deliverStatusForLastChunk("queued");
        const summary = await sent;
        expect(summary).toMatchObject({ total: 1, delivered: 0, queued: 1 });
    });

    test("a refused chunk rejects the send with the server's reason", async () => {
        const { peer } = await pairedRemote();
        const sent = sendText(peer, "nope");
        await deliverStatusForLastChunk("refused", {
            error: "device queue is full — try again after it picks up its backlog",
        });
        await expect(sent).rejects.toThrow(/queue is full/);
    });

    test("multi-chunk files await every chunk status in order", async () => {
        const { peer } = await pairedRemote();
        // 3 chunks of 256 KiB.
        const bytes = new Uint8Array(3 * 256 * 1024);
        const file = fakeFile(bytes, "big.bin");
        const sent = sendFile(peer, file);

        const seen = [];
        for (let i = 0; i < 3; i++) {
            const env = await deliverStatusForLastChunk("delivered");
            seen.push(env);
        }
        const summary = await sent;
        expect(summary).toMatchObject({ total: 3, delivered: 3, queued: 0 });

        // Each chunk had its own correlation id and its own encrypted payload.
        const ids = new Set(seen.map((e) => e.client_msg_id));
        expect(ids.size).toBe(3);
    });

    test("inbound queued frames are acked after processing", async () => {
        const { remote } = await pairedRemote();
        const incoming = [];
        setIncomingHandler((item) => incoming.push(item));

        const frame = {
            op: "chunk",
            transfer_id: "inbound-1",
            kind: "text",
            name: "",
            mime: "text/plain",
            size: 5,
            total: 1,
            seq: 0,
            data: btoa("hello"),
        };
        const result = await deliverInboundChunk(remote, frame, 42);
        expect(result).toMatchObject({ kind: "text", text: "hello" });
        expect(incoming).toHaveLength(1);

        const acks = sentEnvelopes().filter((m) => m.type === "device.ack");
        expect(acks).toEqual([{ type: "device.ack", queue_id: 42 }]);
    });

    test("acks are sent even when processing throws (poison frames drain)", async () => {
        const { remote } = await pairedRemote();
        // Encrypted with the WRONG key (other party), so decryption fails —
        // the frame must still be acked so the queue row is retired.
        const stranger = await generateDeviceKeyPair();
        const me = await getOrCreateDevice();
        const wrongKey = await deriveSessionKey(stranger.privateKeyJwk, me.publicKeyJwk);
        const data = await encryptJSON(wrongKey, { op: "chunk" });

        await expect(
            handleDeviceMsg({
                type: "device.msg",
                from_device_id: await fingerprint(remote.publicKeyJwk),
                channel: "teleport",
                queue_id: 7,
                data,
            }),
        ).rejects.toThrow();
        const acks = sentEnvelopes().filter((m) => m.type === "device.ack");
        expect(acks).toEqual([{ type: "device.ack", queue_id: 7 }]);
    });

    test("frames without a queue_id (direct delivery) are not acked", async () => {
        const { remote } = await pairedRemote();
        const frame = {
            op: "chunk",
            transfer_id: "direct-1",
            kind: "text",
            name: "",
            mime: "text/plain",
            size: 5,
            total: 1,
            seq: 0,
            data: btoa("hola"),
        };
        await deliverInboundChunk(remote, frame, 0);
        const acks = sentEnvelopes().filter((m) => m.type === "device.ack");
        expect(acks).toHaveLength(0);
    });

    test("redelivered duplicate of a completed transfer is dropped, not re-inboxed", async () => {
        const { remote } = await pairedRemote();
        const incoming = [];
        setIncomingHandler((item) => incoming.push(item));

        const frame = {
            op: "chunk",
            transfer_id: "dup-1",
            kind: "text",
            name: "",
            mime: "text/plain",
            size: 2,
            total: 1,
            seq: 0,
            data: btoa("hi"),
        };
        await deliverInboundChunk(remote, frame, 10);
        // At-least-once redelivery: the same row again (new queue id, same
        // transfer). It must be acked but not re-delivered to the inbox.
        const second = await deliverInboundChunk(remote, frame, 11);
        expect(second).toBeNull();
        expect(incoming).toHaveLength(1);
        const acks = sentEnvelopes().filter((m) => m.type === "device.ack");
        expect(acks.map((a) => a.queue_id)).toEqual([10, 11]);
    });

    test("a transfer interrupted mid-way resumes from redelivered chunks", async () => {
        const { remote } = await pairedRemote();
        const incoming = [];
        setIncomingHandler((item) => incoming.push(item));

        // Chunks of a 2-chunk transfer arrive out of order / across a
        // reconnect: seq 1 first (queued redelivery), then seq 0 — full
        // coverage completes the assembly regardless of arrival order.
        const chunk1 = {
            op: "chunk",
            transfer_id: "resume-1",
            kind: "text",
            name: "",
            mime: "text/plain",
            size: 11, // "hello " + "world"
            total: 2,
            seq: 1,
            data: btoa("world"),
        };
        const first = await deliverInboundChunk(remote, chunk1, 20);
        expect(first).toBeNull(); // still incomplete
        expect(incoming).toHaveLength(0);

        const chunk0 = { ...chunk1, seq: 0, data: btoa("hello ") };
        const second = await deliverInboundChunk(remote, chunk0, 21);
        expect(second).toMatchObject({ kind: "text", text: "hello world" });
        expect(incoming).toHaveLength(1);
    });

    test("unpaired senders are ignored, and their queued rows are acked away", async () => {
        const stranger = await generateDeviceKeyPair();
        const me = await getOrCreateDevice();
        const key = await deriveSessionKey(stranger.privateKeyJwk, me.publicKeyJwk);
        const data = await encryptJSON(key, { op: "chunk" });
        const result = await handleDeviceMsg({
            type: "device.msg",
            from_device_id: await fingerprint(stranger.publicKeyJwk),
            channel: "teleport",
            queue_id: 99,
            data,
        });
        expect(result).toBeNull();
        expect(listPeers()).toHaveLength(0);
        // The frame is ignored, but the queue row is still acked: this device
        // will never process it, and leaving it unacked would just re-deliver
        // a poison frame on every reconnect until the TTL runs out.
        const acks = sentEnvelopes().filter((m) => m.type === "device.ack");
        expect(acks).toEqual([{ type: "device.ack", queue_id: 99 }]);
    });
});
