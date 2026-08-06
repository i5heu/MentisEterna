// WebCrypto E2E primitives for the device channel.
//
// ECDH P-256 key agreement + AES-GCM-256 payload encryption. P-256 is used
// because crypto.subtle X25519 support is not universal across browsers;
// P-256 is supported everywhere. All exports are pure functions over the
// WebCrypto API — no DOM, no localStorage — so this module runs under the
// node jest environment as-is.

const ECDH_PARAMS = { name: "ECDH", namedCurve: "P-256" };
const ECDSA_PARAMS = { name: "ECDSA", namedCurve: "P-256" };
const AES_PARAMS = { name: "AES-GCM", length: 256 };
const IV_BYTES = 12;

/** Generate a fresh ECDH P-256 keypair, exported as JWK objects. */
export async function generateDeviceKeyPair() {
    const pair = await crypto.subtle.generateKey(ECDH_PARAMS, true, ["deriveKey"]);
    const [publicKeyJwk, privateKeyJwk] = await Promise.all([
        crypto.subtle.exportKey("jwk", pair.publicKey),
        crypto.subtle.exportKey("jwk", pair.privateKey),
    ]);
    return { publicKeyJwk, privateKeyJwk };
}

/**
 * Derive the shared AES-GCM-256 session key from one device's private key and
 * the other's public key. ECDH is commutative, so both sides of a pairing
 * derive the same key: deriveSessionKey(A_priv, B_pub) ===
 * deriveSessionKey(B_priv, A_pub).
 */
export async function deriveSessionKey(privateKeyJwk, publicKeyJwk) {
    const priv = await crypto.subtle.importKey(
        "jwk",
        privateKeyJwk,
        ECDH_PARAMS,
        false,
        ["deriveKey"],
    );
    const pub = await crypto.subtle.importKey(
        "jwk",
        publicKeyJwk,
        ECDH_PARAMS,
        false,
        [],
    );
    return crypto.subtle.deriveKey(
        { name: "ECDH", public: pub },
        priv,
        AES_PARAMS,
        false,
        ["encrypt", "decrypt"],
    );
}

/** Stable per-device routing id: SHA-256 over the public JWK, hex-encoded. */
export async function fingerprint(publicKeyJwk) {
    const digest = await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(JSON.stringify(publicKeyJwk)),
    );
    return [...new Uint8Array(digest)]
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

/** Encrypt a JSON-serializable object -> base64(iv || ciphertext). */
export async function encryptJSON(key, obj) {
    const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));
    const pt = new TextEncoder().encode(JSON.stringify(obj));
    const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, pt);
    const out = new Uint8Array(IV_BYTES + ct.byteLength);
    out.set(iv, 0);
    out.set(new Uint8Array(ct), IV_BYTES);
    return bufToB64(out);
}

/** Decrypt base64(iv || ciphertext) -> parsed JSON object. */
export async function decryptJSON(key, dataB64) {
    const raw = b64ToBuf(dataB64);
    const iv = raw.slice(0, IV_BYTES);
    const ct = raw.slice(IV_BYTES);
    const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv }, key, ct);
    return JSON.parse(new TextDecoder().decode(pt));
}

/** Uint8Array -> standard base64 string. */
export function bufToB64(u8) {
    let bin = "";
    for (let i = 0; i < u8.length; i++) {
        bin += String.fromCharCode(u8[i]);
    }
    return btoa(bin);
}

/** Standard base64 string -> Uint8Array. */
export function b64ToBuf(b64) {
    const bin = atob(b64);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) {
        out[i] = bin.charCodeAt(i);
    }
    return out;
}

/**
 * Canonical bytes a pairing payload is signed over: UTF-8 of
 * JSON.stringify({ v, name, id, pub }). Both sides rebuild that exact object
 * from the parsed fields, so key order is deterministic regardless of the
 * wire representation (and unknown extra fields are excluded).
 */
function pairingCanonicalBytes(payload) {
    return new TextEncoder().encode(
        JSON.stringify({
            v: payload.v,
            name: payload.name,
            id: payload.id,
            pub: payload.pub,
        }),
    );
}

// WebCrypto exports JWKs with a key_ops member derived from the generating
// algorithm (e.g. ["deriveKey"] for ECDH keys). Importing that same key
// material under a second algorithm (ECDSA) with different usages fails the
// key_ops check, so the member is stripped — the key material itself is
// algorithm-neutral.
function withoutKeyOps(jwk) {
    const { key_ops, ...clean } = jwk;
    return clean;
}

/**
 * ECDSA-SHA-256 signature over a pairing payload, using this device's P-256
 * private key. The same key material serves ECDH (key agreement) and ECDSA
 * (identity) via separate algorithm imports; the pairing messages themselves
 * are not secret, but the signature proves the sender holds the private key
 * matching the advertised public key — the trust anchor of the confirmation
 * flow (an attacker who claims a device id cannot impersonate it).
 */
export async function signPayload(privateKeyJwk, payload) {
    const key = await crypto.subtle.importKey(
        "jwk",
        withoutKeyOps(privateKeyJwk),
        ECDSA_PARAMS,
        false,
        ["sign"],
    );
    const sig = await crypto.subtle.sign(
        { name: "ECDSA", hash: "SHA-256" },
        key,
        pairingCanonicalBytes(payload),
    );
    return bufToB64(new Uint8Array(sig));
}

/**
 * Verify an ECDSA-SHA-256 signature over a pairing payload against the
 * advertised public key. Returns false for any failure (bad signature format,
 * unparseable key, wrong key) — inbound pairing traffic is untrusted.
 */
export async function verifyPayload(publicKeyJwk, payload, sigB64) {
    try {
        const key = await crypto.subtle.importKey(
            "jwk",
            withoutKeyOps(publicKeyJwk),
            ECDSA_PARAMS,
            false,
            ["verify"],
        );
        return await crypto.subtle.verify(
            { name: "ECDSA", hash: "SHA-256" },
            key,
            b64ToBuf(sigB64),
            pairingCanonicalBytes(payload),
        );
    } catch {
        return false;
    }
}
