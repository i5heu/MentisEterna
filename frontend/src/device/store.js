// Persisted device identity + paired peers, all in localStorage.
//
// The browser is the device: tabs of the same browser share localStorage, so
// they share one device identity and one peer list. Private keys never leave
// localStorage; the relay server only ever sees public keys and fingerprints.

import {
    deriveSessionKey,
    fingerprint,
    generateDeviceKeyPair,
} from "./crypto.js";

const KEY_STORAGE = "mentis.device.key";
const PEERS_STORAGE = "mentis.device.peers.v1";
const NAME_STORAGE = "mentis.device.name";

let cachedDevice = null;
const sessionKeyCache = new Map(); // peer id -> Promise<CryptoKey>

/**
 * Get (or lazily create) this device's identity: { id, publicKeyJwk,
 * privateKeyJwk }. id is the SHA-256 fingerprint of the public key.
 */
export async function getOrCreateDevice() {
    if (cachedDevice) {
        return cachedDevice;
    }
    let raw = null;
    try {
        raw = localStorage.getItem(KEY_STORAGE);
    } catch {
        raw = null;
    }
    if (raw) {
        const parsed = JSON.parse(raw);
        if (parsed && parsed.publicKeyJwk && parsed.privateKeyJwk) {
            const id = await fingerprint(parsed.publicKeyJwk);
            cachedDevice = { id, ...parsed };
            return cachedDevice;
        }
    }
    const { publicKeyJwk, privateKeyJwk } = await generateDeviceKeyPair();
    const id = await fingerprint(publicKeyJwk);
    cachedDevice = { id, publicKeyJwk, privateKeyJwk };
    try {
        localStorage.setItem(
            KEY_STORAGE,
            JSON.stringify({ publicKeyJwk, privateKeyJwk }),
        );
    } catch {
        // In-memory identity still works for the session if storage is blocked.
    }
    return cachedDevice;
}

/** Read the paired-peers list (default []). */
export function listPeers() {
    try {
        const raw = localStorage.getItem(PEERS_STORAGE);
        if (!raw) {
            return [];
        }
        const peers = JSON.parse(raw);
        return Array.isArray(peers) ? peers : [];
    } catch {
        return [];
    }
}

function persistPeers(peers) {
    try {
        localStorage.setItem(PEERS_STORAGE, JSON.stringify(peers));
    } catch {
        // Best-effort: pairing is per-browser and non-critical to persist.
    }
}

/** Add or replace a peer; returns the new peer entry. */
export async function addPeer(name, publicKeyJwk) {
    const id = await fingerprint(publicKeyJwk);
    const peers = listPeers();
    const existing = peers.findIndex((p) => p.id === id);
    const peer = {
        id,
        name,
        publicKeyJwk,
        pairedAt: existing >= 0 ? peers[existing].pairedAt : Date.now(),
    };
    if (existing >= 0) {
        peers[existing] = peer;
    } else {
        peers.push(peer);
    }
    persistPeers(peers);
    sessionKeyCache.delete(id);
    return peer;
}

/** Remove a peer by id; returns true if one was removed. */
export function removePeer(id) {
    const peers = listPeers();
    const next = peers.filter((p) => p.id !== id);
    if (next.length === peers.length) {
        return false;
    }
    persistPeers(next);
    sessionKeyCache.delete(id);
    return true;
}

/** Shared AES session key for a peer, cached per peer id. */
export function sessionKeyForPeer(peer) {
    let cached = sessionKeyCache.get(peer.id);
    if (!cached) {
        cached = getOrCreateDevice().then((me) =>
            deriveSessionKey(me.privateKeyJwk, peer.publicKeyJwk),
        );
        // Don't cache a rejected promise forever.
        cached.catch(() => sessionKeyCache.delete(peer.id));
        sessionKeyCache.set(peer.id, cached);
    }
    return cached;
}

/** Display name of this device. */
export function getMyName() {
    try {
        return localStorage.getItem(NAME_STORAGE) || "";
    } catch {
        return "";
    }
}

export function setMyName(name) {
    try {
        localStorage.setItem(NAME_STORAGE, name);
    } catch {
        // Best-effort.
    }
}
