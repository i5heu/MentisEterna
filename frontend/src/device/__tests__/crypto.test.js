// E2E crypto primitives — node jest environment (no DOM/localStorage, so
// store.js and pairing.js are intentionally not imported here).

import {
    b64ToBuf,
    bufToB64,
    decryptJSON,
    deriveSessionKey,
    encryptJSON,
    fingerprint,
    generateDeviceKeyPair,
    signPayload,
    verifyPayload,
} from "../crypto.js";

describe("device ECDH + AES-GCM primitives", () => {
    test("key symmetry: both sides of a pairing derive the same session key", async () => {
        const a = await generateDeviceKeyPair();
        const b = await generateDeviceKeyPair();

        const aSide = await deriveSessionKey(a.privateKeyJwk, b.publicKeyJwk);
        const bSide = await deriveSessionKey(b.privateKeyJwk, a.publicKeyJwk);

        const secret = { hello: "device b", n: 42, nested: { ok: true } };
        const ciphertext = await encryptJSON(aSide, secret);
        // B-side key decrypts A-side ciphertext and vice versa.
        expect(await decryptJSON(bSide, ciphertext)).toEqual(secret);

        const ciphertext2 = await encryptJSON(bSide, { back: "at you" });
        expect(await decryptJSON(aSide, ciphertext2)).toEqual({ back: "at you" });
    });

    test("encryptJSON/decryptJSON round-trip", async () => {
        const a = await generateDeviceKeyPair();
        const b = await generateDeviceKeyPair();
        const key = await deriveSessionKey(a.privateKeyJwk, b.publicKeyJwk);

        const obj = { text: "héllo wörld — ünïcode ✓", list: [1, 2, 3] };
        const payload = await encryptJSON(key, obj);
        expect(typeof payload).toBe("string");
        expect(await decryptJSON(key, payload)).toEqual(obj);
    });

    test("tampered ciphertext is rejected", async () => {
        const a = await generateDeviceKeyPair();
        const b = await generateDeviceKeyPair();
        const key = await deriveSessionKey(a.privateKeyJwk, b.publicKeyJwk);

        const payload = await encryptJSON(key, { secret: "do not read" });
        const bytes = b64ToBuf(payload);
        bytes[bytes.length - 1] ^= 0xff; // flip one byte in the ciphertext
        await expect(decryptJSON(key, bufToB64(bytes))).rejects.toThrow();
    });

    test("fingerprint is stable per key and differs across keypairs", async () => {
        const a = await generateDeviceKeyPair();
        const b = await generateDeviceKeyPair();

        const a1 = await fingerprint(a.publicKeyJwk);
        const a2 = await fingerprint(a.publicKeyJwk);
        const b1 = await fingerprint(b.publicKeyJwk);

        expect(a1).toBe(a2);
        expect(a1).toMatch(/^[0-9a-f]{64}$/);
        expect(a1).not.toBe(b1);
    });

    test("signPayload/verifyPayload round-trip: only the owning key verifies", async () => {
        const a = await generateDeviceKeyPair();
        const b = await generateDeviceKeyPair();

        const request = {
            kind: "pair.request",
            v: 1,
            request_id: "req-1",
            name: "Dev A",
            id: await fingerprint(a.publicKeyJwk),
            pub: a.publicKeyJwk,
        };
        const sig = await signPayload(a.privateKeyJwk, request);

        expect(typeof sig).toBe("string");
        expect(await verifyPayload(a.publicKeyJwk, request, sig)).toBe(true);
        // A different device's public key must not verify A's signature.
        expect(await verifyPayload(b.publicKeyJwk, request, sig)).toBe(false);
    });

    test("tampered pairing payload fails verification", async () => {
        const a = await generateDeviceKeyPair();

        const request = {
            kind: "pair.request",
            v: 1,
            request_id: "req-2",
            name: "Dev A",
            id: await fingerprint(a.publicKeyJwk),
            pub: a.publicKeyJwk,
        };
        const sig = await signPayload(a.privateKeyJwk, request);

        // Tampered name, extra fields, and garbage signatures all fail —
        // extra fields are excluded from the canonical bytes, so only the
        // signed identity fields matter.
        expect(
            await verifyPayload(a.publicKeyJwk, { ...request, name: "Evil" }, sig),
        ).toBe(false);
        expect(
            await verifyPayload(a.publicKeyJwk, request, "AAAA"),
        ).toBe(false);
        expect(
            await verifyPayload(
                a.publicKeyJwk,
                { ...request, kind: "pair.response" },
                sig,
            ),
        ).toBe(true); // kind/request_id are not part of the signed identity
    });
});
