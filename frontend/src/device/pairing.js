// Public-key exchange payloads for pairing: the shareable "code" (paste or QR).
//
// A pairing payload is the JSON stringification of { v: 1, name, id, pub }
// where pub is this device's ECDH public key as a JWK. The other device
// imports it, stores the peer, and derives the shared session key from it.

import { getMyName, addPeer, getOrCreateDevice } from "./store.js";

/**
 * This device's share payload: { v: 1, name, id, pub }.
 * id is the fingerprint (routing id) of pub.
 */
export async function mySharePayload() {
    const me = await getOrCreateDevice();
    return { v: 1, name: getMyName(), id: me.id, pub: me.publicKeyJwk };
}

/** The paste/shareable pairing "code". */
export async function sharePayloadText() {
    return JSON.stringify(await mySharePayload());
}

/** Render a pairing code as a QR data URL (PNG). */
export async function renderQR(text) {
    const QRCode = (await import("qrcode")).default;
    return QRCode.toDataURL(text, { width: 256, margin: 1 });
}

/**
 * Scan a QR code from a camera stream onto a 2D canvas using jsQR. Resolves
 * with the decoded text, or null if nothing decoded before the 30 s deadline
 * or the video ended. Stops the stream tracks when done. Camera/permission
 * failures are thrown (not swallowed) so callers can surface the real error.
 */
export async function scanQRFromCamera(canvas, video) {
    const jsQR = (await import("jsqr")).default;
    const stream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: "environment" },
    });
    const tracks = stream.getTracks();
    const stop = () => tracks.forEach((t) => t.stop());
    try {
        video.srcObject = stream;
        await video.play();
        const ctx = canvas.getContext("2d", { willReadFrequently: true });
        const deadline = Date.now() + 30 * 1000;
        // Poll frames until a QR decodes, the video ends, or the scan times out.
        for (;;) {
            if (video.ended || Date.now() > deadline) {
                return null;
            }
            if (video.readyState >= 2 && video.videoWidth > 0) {
                canvas.width = video.videoWidth;
                canvas.height = video.videoHeight;
                ctx.drawImage(video, 0, 0);
                const imageData = ctx.getImageData(
                    0,
                    0,
                    canvas.width,
                    canvas.height,
                );
                const result = jsQR(
                    imageData.data,
                    imageData.width,
                    imageData.height,
                );
                if (result && result.data) {
                    return result.data;
                }
            }
            await new Promise((r) => setTimeout(r, 120));
        }
    } finally {
        stop();
    }
}

/**
 * Validate a pairing payload and persist the peer. Throws a TypeError with a
 * readable message on malformed input.
 */
export async function importPeerFromPayload(text, fallbackName) {
    let payload;
    try {
        payload = JSON.parse(text);
    } catch {
        throw new TypeError("Pairing code is not valid JSON.");
    }
    if (!payload || payload.v !== 1 || !payload.pub || !payload.pub.x) {
        throw new TypeError(
            "Pairing code is not a valid device share (v=1 with a public key required).",
        );
    }
    return addPeer(payload.name || fallbackName || "Device", payload.pub);
}
