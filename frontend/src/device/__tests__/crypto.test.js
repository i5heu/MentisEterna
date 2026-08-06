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
});
