// IndexedDB-backed chunk store for upload resume.
// Chunk data is keyed by file SHA-256 hash so we can resume uploads
// after tab close, browser crash, or page reload without re-reading files.
//
// Two object stores:
//   "entries" — metadata per file (keyPath: "fileHash")
//   "blobs"   — raw Uint8Array chunks (no keyPath, explicit key: "fileHash:index")

const DB_NAME = "mentis-upload-chunks";
const DB_VERSION = 2;
const ENTRIES_STORE = "entries";
const BLOBS_STORE = "blobs";

/** @returns {Promise<IDBDatabase>} */
function openDB() {
    return new Promise((resolve, reject) => {
        const req = indexedDB.open(DB_NAME, DB_VERSION);
        req.onupgradeneeded = (event) => {
            const db = event.target.result;
            // Clean up old single-store schema if upgrading from v1.
            if (event.oldVersion < 1) {
                if (!db.objectStoreNames.contains(ENTRIES_STORE)) {
                    db.createObjectStore(ENTRIES_STORE, { keyPath: "fileHash" });
                }
            }
            if (event.oldVersion < 2) {
                // Remove old v1 store if it exists (name was "chunks").
                if (db.objectStoreNames.contains("chunks")) {
                    db.deleteObjectStore("chunks");
                }
                if (!db.objectStoreNames.contains(ENTRIES_STORE)) {
                    db.createObjectStore(ENTRIES_STORE, { keyPath: "fileHash" });
                }
                if (!db.objectStoreNames.contains(BLOBS_STORE)) {
                    db.createObjectStore(BLOBS_STORE);
                }
            }
        };
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error);
    });
}

/**
 * Get a chunk-store entry by file hash.
 */
export async function getChunkEntry(fileHash) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(ENTRIES_STORE, "readonly");
        const req = tx.objectStore(ENTRIES_STORE).get(fileHash);
        req.onsuccess = () => resolve(req.result || null);
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}

/**
 * Store a single chunk for a file entry.
 * Creates the entry if it doesn't exist yet.
 */
export async function putChunk(fileHash, meta, index, data) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction([ENTRIES_STORE, BLOBS_STORE], "readwrite");
        const entriesStore = tx.objectStore(ENTRIES_STORE);
        const blobsStore = tx.objectStore(BLOBS_STORE);
        let settled = false;

        const finishReject = (error) => {
            if (settled) return;
            settled = true;
            reject(error);
        };

        const getReq = entriesStore.get(fileHash);
        getReq.onsuccess = () => {
            let entry = getReq.result;
            if (!entry) {
                entry = {
                    fileHash,
                    filename: meta.filename,
                    mimeType: meta.mimeType,
                    totalSize: meta.totalSize,
                    chunkSize: meta.chunkSize,
                    totalChunks: meta.totalChunks,
                    inline: meta.inline,
                    noteId: meta.noteId,
                    token: meta.token,
                    placeholderToken: meta.placeholderToken || "",
                    chunkIndexes: [],
                };
            }
            if (!entry.chunkIndexes.includes(index)) {
                entry.chunkIndexes.push(index);
            }
            if (!entry.placeholderToken && meta.placeholderToken) {
                entry.placeholderToken = meta.placeholderToken;
            }
            const chunkKey = fileHash + ":" + index;
            entriesStore.put(entry);
            blobsStore.put(data, chunkKey);
        };
        getReq.onerror = () => finishReject(getReq.error);
        tx.onabort = () => finishReject(tx.error || new Error("putChunk transaction aborted"));
        tx.onerror = () => finishReject(tx.error || new Error("putChunk transaction failed"));
        tx.oncomplete = () => {
            db.close();
            if (settled) return;
            settled = true;
            resolve();
        };
    });
}

/**
 * Read a chunk's binary data.
 */
export async function getChunkData(fileHash, index) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(BLOBS_STORE, "readonly");
        const chunkKey = fileHash + ":" + index;
        const req = tx.objectStore(BLOBS_STORE).get(chunkKey);
        req.onsuccess = () => resolve(req.result || null);
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}

/**
 * Delete a chunk entry and all its chunk data.
 */
export async function deleteChunkEntry(fileHash) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction([ENTRIES_STORE, BLOBS_STORE], "readwrite");
        const entriesStore = tx.objectStore(ENTRIES_STORE);
        const blobsStore = tx.objectStore(BLOBS_STORE);
        let settled = false;

        const finishReject = (error) => {
            if (settled) return;
            settled = true;
            reject(error);
        };

        const getReq = entriesStore.get(fileHash);
        getReq.onsuccess = () => {
            const entry = getReq.result;
            entriesStore.delete(fileHash);
            if (entry && entry.chunkIndexes) {
                for (const idx of entry.chunkIndexes) {
                    blobsStore.delete(fileHash + ":" + idx);
                }
            }
        };
        getReq.onerror = () => {
            entriesStore.delete(fileHash);
        };
        tx.onabort = () => finishReject(tx.error || new Error("deleteChunkEntry transaction aborted"));
        tx.onerror = () => finishReject(tx.error || new Error("deleteChunkEntry transaction failed"));
        tx.oncomplete = () => {
            db.close();
            if (settled) return;
            settled = true;
            resolve();
        };
    });
}

/**
 * List all stored chunk entries.
 */
export async function listEntries() {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(ENTRIES_STORE, "readonly");
        const req = tx.objectStore(ENTRIES_STORE).getAll();
        req.onsuccess = () => {
            const all = req.result || [];
            resolve(all.map(e => ({
                fileHash: e.fileHash,
                filename: e.filename,
                noteId: e.noteId,
            })));
        };
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}
