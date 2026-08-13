// IndexedDB-backed queue of finished audio recordings awaiting upload to
// /ingest/audio. Recordings are persisted before the upload is attempted so a
// tab close, reload, or network failure never loses a finished recording; a
// later flush (page load, online event, recorder page open) retries them.
//
// Object store "pending" holds one entry per recording, keyed by a client
// generated id. Entry shape:
//   { id, parentId, dateFlag, blob, mimeType, filename, createdAt, attempts }

const DB_NAME = "mentis-audio-queue";
const DB_VERSION = 1;
const PENDING_STORE = "pending";

/** @returns {Promise<IDBDatabase>} */
function openDB() {
    return new Promise((resolve, reject) => {
        const req = indexedDB.open(DB_NAME, DB_VERSION);
        req.onupgradeneeded = (event) => {
            const db = event.target.result;
            if (!db.objectStoreNames.contains(PENDING_STORE)) {
                db.createObjectStore(PENDING_STORE, { keyPath: "id", autoIncrement: false });
            }
        };
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error);
    });
}

/**
 * Fetch the ingest bearer token for the current session.
 * @returns {Promise<string>} the ingest token
 * @throws {Error} with `err.status` set on non-2xx responses
 */
export async function getIngestToken() {
    const res = await fetch("/ingest/token", { credentials: "include" });
    if (!res.ok) {
        const err = new Error(`ingest token request failed: ${res.status}`);
        err.status = res.status;
        throw err;
    }
    const body = await res.json();
    return body.token;
}

/** Persist (or overwrite) a pending recording entry. */
export async function addPendingAudio(entry) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(PENDING_STORE, "readwrite");
        tx.objectStore(PENDING_STORE).put(entry);
        tx.oncomplete = () => { db.close(); resolve(); };
        tx.onerror = () => { db.close(); reject(tx.error); };
    });
}

/** List all pending entries, oldest first. */
export async function listPendingAudio() {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(PENDING_STORE, "readonly");
        const req = tx.objectStore(PENDING_STORE).getAll();
        req.onsuccess = () => {
            const entries = req.result || [];
            entries.sort((a, b) => a.createdAt - b.createdAt);
            resolve(entries);
        };
        req.onerror = () => { db.close(); reject(req.error); };
        tx.oncomplete = () => db.close();
    });
}

/** Remove a pending entry once its upload succeeded. */
export async function removePendingAudio(id) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(PENDING_STORE, "readwrite");
        tx.objectStore(PENDING_STORE).delete(id);
        tx.oncomplete = () => { db.close(); resolve(); };
        tx.onerror = () => { db.close(); reject(tx.error); };
    });
}

/**
 * Upload one recording to the ingest endpoint. The endpoint requires the
 * secret ingest bearer token, which the recorder obtains from /ingest/token.
 * Uses XMLHttpRequest (not fetch) so upload progress is observable.
 * @param {Function} [onProgress] called with 0..100 as the request body uploads
 * @returns {Promise<object>} the parsed 201 response (contains the created note)
 * @throws {Error} on any non-2xx response or network failure
 */
export async function uploadAudioRecording(token, parentId, dateFlag, blob, filename, onProgress) {
    const url = dateFlag ? `/ingest/audio/${parentId}/date` : `/ingest/audio/${parentId}`;
    return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        xhr.open("POST", url);
        xhr.setRequestHeader("Authorization", `Bearer ${token}`);
        xhr.responseType = "json";
        xhr.upload.onprogress = (e) => {
            if (onProgress && e.lengthComputable && e.total > 0) {
                onProgress(Math.round((e.loaded / e.total) * 100));
            }
        };
        xhr.onload = () => {
            if (xhr.status >= 200 && xhr.status < 300) {
                resolve(xhr.response);
            } else {
                reject(new Error(`upload failed: ${xhr.status}`));
            }
        };
        xhr.onerror = () => reject(new Error("upload failed: network error"));
        xhr.onabort = () => reject(new Error("upload failed: aborted"));
        const fd = new FormData();
        fd.append("file", blob, filename);
        xhr.send(fd);
    });
}

let flushPromise = null;

/**
 * Attempt to upload every pending recording. Concurrent calls share one run
 * (module-level promise), and the function never throws: individual upload
 * failures leave the entry queued with an incremented attempt count and are
 * reported in `failed`. If no token is passed, one is fetched first; a token
 * fetch failure is not an error, just a reason to retry later.
 * @returns {Promise<{uploaded: string[], failed: string[]}>}
 */
export function flushPendingAudio(token) {
    if (flushPromise) {
        return flushPromise;
    }
    flushPromise = (async () => {
        try {
            const effectiveToken = token || await getIngestToken();
            const uploaded = [];
            const failed = [];
            const entries = await listPendingAudio();
            for (const entry of entries) {
                try {
                    await uploadAudioRecording(effectiveToken, entry.parentId, entry.dateFlag, entry.blob, entry.filename);
                    await removePendingAudio(entry.id);
                    uploaded.push(entry.id);
                } catch (e) {
                    await addPendingAudio({ ...entry, attempts: entry.attempts + 1 });
                    failed.push(entry.id);
                }
            }
            return { uploaded, failed };
        } catch (e) {
            // Token fetch failed (or IDB is unavailable): retry later.
            let pendingIds = [];
            try {
                pendingIds = (await listPendingAudio()).map((entry) => entry.id);
            } catch (_) {
                // Ignore: still report nothing uploaded.
            }
            return { uploaded: [], failed: pendingIds };
        } finally {
            flushPromise = null;
        }
    })();
    return flushPromise;
}
