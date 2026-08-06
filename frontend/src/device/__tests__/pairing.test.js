// Pairing handshake (mutual-consent) + teleport channel gate — node jest env.
//
// store.js persists to localStorage, which does not exist in node, so this
// file installs a tiny in-memory shim. api.js is mocked (teleport.js imports
// sendLiveMessage from it), so the tests drive the handshake purely through
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
    fingerprint,
    generateDeviceKeyPair,
    signPayload,
    verifyPayload,
} from "../crypto.js";
import { getOrCreateDevice, listPeers } from "../store.js";
import {
    cancelPairing,
    handleDeviceMsg,
    pairingState,
    pendingPairRequests,
    respondToPairRequest,
    sendPairRequest,
} from "../teleport.js";
import { sendLiveMessage } from "../../api.js";

jest.mock("../../api.js", () => ({
    sendLiveMessage: jest.fn(),
}));

function lastSent() {
    const calls = sendLiveMessage.mock.calls;
    return calls[calls.length - 1][0];
}

function sentMessages() {
    return sendLiveMessage.mock.calls.map(([payload]) => payload);
}

function sentCount() {
    return sendLiveMessage.mock.calls.length;
}

async function deliverPairingMsg(fromDeviceId, msg) {
    return handleDeviceMsg({
        type: "device.msg",
        from_device_id: fromDeviceId,
        channel: "pairing",
        data: JSON.stringify(msg),
    });
}

describe("device pairing handshake", () => {
    beforeEach(() => {
        storage.clear();
        sendLiveMessage.mockClear();
        pendingPairRequests.value.splice(0);
        pairingState.value = null;
    });

    test("scan -> target confirmation -> accept connects both devices", async () => {
        const requester = await getOrCreateDevice(); // this browser's device
        const target = await generateDeviceKeyPair(); // the QR-code device
        const targetId = await fingerprint(target.publicKeyJwk);
        const targetPayload = {
            v: 1,
            name: "Dev B",
            id: targetId,
            pub: target.publicKeyJwk,
        };

        // Requester pastes/scans the target's code and starts pairing.
        const requestId = await sendPairRequest(targetPayload);
        expect(pairingState.value).toMatchObject({
            state: "pending",
            targetId,
        });

        // Relayed request: signed, addressed to the target, claims requester id.
        expect(lastSent()).toMatchObject({
            type: "device.msg",
            to_device_id: targetId,
            channel: "pairing",
        });
        const requestMsg = JSON.parse(lastSent().data);
        expect(requestMsg).toMatchObject({
            kind: "pair.request",
            request_id: requestId,
            id: requester.id,
            name: "Device", // no stored name -> fallback
        });
        expect(typeof requestMsg.sig).toBe("string");
        expect(
            await verifyPayload(requester.publicKeyJwk, requestMsg, requestMsg.sig),
        ).toBe(true);

        // Target receives the request -> confirmation prompt appears.
        await deliverPairingMsg(requester.id, requestMsg);
        expect(pendingPairRequests.value).toHaveLength(1);
        expect(pendingPairRequests.value[0]).toMatchObject({
            requestId,
            name: "Device",
            id: requester.id,
        });

        // Target accepts -> persists the requester as a peer, replies, and
        // re-announces so presence converges in both directions.
        await respondToPairRequest(requestId, true);
        expect(listPeers().some((p) => p.id === requester.id)).toBe(true);
        const localResponse = sentMessages()
            .filter((m) => m.type === "device.msg" && m.data)
            .map((m) => JSON.parse(m.data))
            .find((d) => d.kind === "pair.response");
        expect(localResponse).toMatchObject({
            kind: "pair.response",
            request_id: requestId,
            accepted: true,
        });
        expect(
            sentMessages().some(
                (m) => m.type === "device.hello" && m.device_id === requester.id,
            ),
        ).toBe(true);
        // The emitted accept is self-consistent (id = fingerprint(pub), sig
        // verifies against its own pub).
        expect(localResponse.id).toBe(
            await fingerprint(localResponse.pub),
        );
        expect(
            await verifyPayload(localResponse.pub, localResponse, localResponse.sig),
        ).toBe(true);

        // This simulation shares one identity store, so the local response
        // authenticates as the requester — NOT as the requested device. The
        // requester must reject it: a response that does not authenticate as
        // the exact device asked for is dropped.
        await deliverPairingMsg(localResponse.id, localResponse);
        expect(pairingState.value.state).toBe("error");
        expect(pairingState.value.error).toMatch(/did not match/);
        expect(listPeers().some((p) => p.id === targetId)).toBe(false);

        // Now the real target accepts: a response signed with the target's
        // private key, claiming the target's id.
        const realResponse = {
            kind: "pair.response",
            v: 1,
            request_id: requestId,
            accepted: true,
            name: "Dev B",
            id: targetId,
            pub: target.publicKeyJwk,
        };
        realResponse.sig = await signPayload(
            target.privateKeyJwk,
            realResponse,
        );
        await deliverPairingMsg(targetId, realResponse);

        // Single confirmation connects BOTH directions.
        expect(pairingState.value.state).toBe("accepted");
        expect(pairingState.value.targetId).toBe(targetId);
        expect(listPeers().some((p) => p.id === targetId)).toBe(true);
    });

    test("decline leaves both sides unpaired", async () => {
        const requester = await getOrCreateDevice();
        const target = await generateDeviceKeyPair();
        const targetId = await fingerprint(target.publicKeyJwk);

        await sendPairRequest({
            v: 1,
            name: "Dev B",
            id: targetId,
            pub: target.publicKeyJwk,
        });
        const requestMsg = JSON.parse(lastSent().data);

        await deliverPairingMsg(requester.id, requestMsg);
        expect(pendingPairRequests.value).toHaveLength(1);

        await respondToPairRequest(requestMsg.request_id, false);
        expect(listPeers().length).toBe(0); // target never added the requester
        const responseMsg = JSON.parse(lastSent().data);
        expect(responseMsg).toMatchObject({
            kind: "pair.response",
            accepted: false,
        });

        await deliverPairingMsg(targetId, responseMsg);
        expect(pairingState.value.state).toBe("declined");
        expect(listPeers().length).toBe(0); // requester never added the target
    });

    test("forged pair.request is ignored (id/pub mismatch, sender spoof, bad sig)", async () => {
        const requester = await getOrCreateDevice();
        const attacker = await generateDeviceKeyPair();
        const attackerId = await fingerprint(attacker.publicKeyJwk);

        // Case 1: claimed id does not match the advertised key's fingerprint,
        // even though the signature is valid for that (inconsistent) payload.
        const inconsistent = {
            kind: "pair.request",
            v: 1,
            request_id: "evil-1",
            name: "Impostor",
            id: "a".repeat(64),
            pub: attacker.publicKeyJwk,
        };
        inconsistent.sig = await signPayload(attacker.privateKeyJwk, inconsistent);
        await deliverPairingMsg(attackerId, inconsistent);
        expect(pendingPairRequests.value).toHaveLength(0);

        // Case 2: consistent id + valid signature, but the server-stamped
        // from_device_id does not match the claimed id (spoofed sender).
        const spoofed = {
            kind: "pair.request",
            v: 1,
            request_id: "evil-2",
            name: "Impostor",
            id: attackerId,
            pub: attacker.publicKeyJwk,
        };
        spoofed.sig = await signPayload(attacker.privateKeyJwk, spoofed);
        await deliverPairingMsg(requester.id, spoofed);
        expect(pendingPairRequests.value).toHaveLength(0);

        // Case 3: garbage signature.
        const badSig = {
            kind: "pair.request",
            v: 1,
            request_id: "evil-3",
            name: "Impostor",
            id: attackerId,
            pub: attacker.publicKeyJwk,
            sig: "AAAA",
        };
        await deliverPairingMsg(attackerId, badSig);
        expect(pendingPairRequests.value).toHaveLength(0);
    });

    test("requester cancel closes the target's prompt", async () => {
        const requester = await getOrCreateDevice();
        const target = await generateDeviceKeyPair();
        const targetId = await fingerprint(target.publicKeyJwk);

        await sendPairRequest({
            v: 1,
            name: "Dev B",
            id: targetId,
            pub: target.publicKeyJwk,
        });
        const requestMsg = JSON.parse(lastSent().data);
        await deliverPairingMsg(requester.id, requestMsg);
        expect(pendingPairRequests.value).toHaveLength(1);

        cancelPairing();
        expect(pairingState.value).toBeNull();
        const cancelMsg = JSON.parse(lastSent().data);
        expect(cancelMsg).toMatchObject({
            kind: "pair.response",
            accepted: false,
            request_id: requestMsg.request_id,
        });

        // Target receives the cancel -> prompt closes, nothing paired.
        await deliverPairingMsg(requester.id, cancelMsg);
        expect(pendingPairRequests.value).toHaveLength(0);
        expect(listPeers().length).toBe(0);
    });

    test("requester times out when the target never responds", async () => {
        jest.useFakeTimers();
        try {
            const target = await generateDeviceKeyPair();
            const targetId = await fingerprint(target.publicKeyJwk);
            await sendPairRequest({
                v: 1,
                name: "Dev B",
                id: targetId,
                pub: target.publicKeyJwk,
            });
            expect(pairingState.value.state).toBe("pending");

            jest.advanceTimersByTime(61 * 1000);
            expect(pairingState.value.state).toBe("error");
            expect(pairingState.value.error).toMatch(/No response/);
        } finally {
            jest.useRealTimers();
        }
    });

    test("self-pairing is rejected before anything is sent", async () => {
        const me = await getOrCreateDevice();
        const before = sentCount();
        await expect(
            sendPairRequest({
                v: 1,
                name: "Me",
                id: me.id,
                pub: me.publicKeyJwk,
            }),
        ).rejects.toThrow(/own pairing code/);
        expect(sentCount()).toBe(before);
        expect(pairingState.value).toBeNull();
    });

    test("teleport frames from unpaired senders are ignored", async () => {
        const stranger = await generateDeviceKeyPair();
        const result = await handleDeviceMsg({
            type: "device.msg",
            from_device_id: await fingerprint(stranger.publicKeyJwk),
            channel: "teleport",
            data: "AAAA",
        });
        expect(result).toBeNull();
    });
});
