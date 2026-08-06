// Public-key exchange payloads for pairing: the shareable "code" (paste or QR).
//
// A pairing payload is the JSON stringification of { v: 1, name, id, pub }
// where pub is this device's ECDH public key as a JWK and id is its
// fingerprint. Importing a payload only validates it — actual pairing now
// runs the mutual-consent handshake in teleport.js (the other device gets a
// confirmation prompt and both sides connect on accept).

import { fingerprint } from "./crypto.js";
import { getMyName, getOrCreateDevice } from "./store.js";

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
 * Validate a share payload (from paste code or QR) without pairing yet.
 * Throws a TypeError with a readable message on malformed input. The
 * advertised id must be the fingerprint of the advertised public key — a code
 * whose id does not match its key is malformed or forged.
 */
export async function parseSharePayload(text) {
    let payload;
    try {
        payload = JSON.parse(text);
    } catch {
        throw new TypeError("Pairing code is not valid JSON.");
    }
    if (
        !payload ||
        payload.v !== 1 ||
        !payload.pub ||
        !payload.pub.x ||
        typeof payload.id !== "string" ||
        !/^[0-9a-f]{64}$/.test(payload.id)
    ) {
        throw new TypeError(
            "Pairing code is not a valid device share (v=1 with a public key required).",
        );
    }
    const actual = await fingerprint(payload.pub);
    if (actual !== payload.id) {
        throw new TypeError(
            "Pairing code is inconsistent (its id does not match its key).",
        );
    }
    return payload;
}
