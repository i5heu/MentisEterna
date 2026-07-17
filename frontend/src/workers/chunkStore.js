// IndexedDB-backed chunk store for upload resume.
// Chunk data is keyed by file SHA-256 hash so we can resume uploads
// after tab close, browser crash, or page reload without re-reading files.
//
// Each entry stores: { fileHash, filename, mimeType, totalSize, chunkSize,
//   totalChunks, inline, noteId, token, chunks: Map<index, Uint8Array> }

const DB_NAME = "mentis-upload-chunks";
const DB_VERSION = 1;
const STORE_NAME = "chunks";

/** @returns {Promise<IDBDatabase>} */
function openDB() {
    return new Promise((resolve, reject) => {
        const req = indexedDB.open(DB_NAME, DB_VERSION);
        req.onupgradeneeded = () => {
            req.result.createObjectStore(STORE_NAME, { keyPath: "fileHash" });
        };
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error);
    });
}

/**
 * Get a chunk-store entry by file hash.
 * @param {string} fileHash - hex SHA-256
 * @returns {Promise<{fileHash: string, filename: string, mimeType: string, totalSize: number, chunkSize: number, totalChunks: number, inline: boolean, noteId: number, token: string, chunks: number[]} | null>}
 */
export async function getChunkEntry(fileHash) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const req = tx.objectStore(STORE_NAME).get(fileHash);
        req.onsuccess = () => {
            resolve(req.result || null);
        };
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}

/**
 * Store a single chunk for a file entry.
 * Creates the entry if it doesn't exist yet.
 * @param {string} fileHash
 * @param {{filename: string, mimeType: string, totalSize: number, chunkSize: number, totalChunks: number, inline: boolean, noteId: number, token: string}} meta
 * @param {number} index - chunk index
 * @param {Uint8Array} data - chunk bytes
 */
export async function putChunk(fileHash, meta, index, data) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);
        const getReq = store.get(fileHash);
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
                    chunks: [],
                };
            }
            // Expand chunks array if needed and store this chunk's index
            if (!entry.chunks.includes(index)) {
                entry.chunks.push(index);
            }
            // Store the binary chunk under a sub-key
            const chunkKey = fileHash + ":" + index;
            store.put(entry);
            store.put({ fileHash: chunkKey, data }, chunkKey);
            resolve();
        };
        getReq.onerror = () => reject(getReq.error);
        tx.oncomplete = () => db.close();
    });
}

/**
 * Read a chunk's binary data.
 * @param {string} fileHash
 * @param {number} index
 * @returns {Promise<Uint8Array | null>}
 */
export async function getChunkData(fileHash, index) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const chunkKey = fileHash + ":" + index;
        const req = tx.objectStore(STORE_NAME).get(chunkKey);
        req.onsuccess = () => {
            const result = req.result;
            resolve(result ? result.data : null);
        };
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}

/**
 * Delete a chunk entry and all its chunk data.
 * @param {string} fileHash
 */
export async function deleteChunkEntry(fileHash) {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readwrite");
        const store = tx.objectStore(STORE_NAME);

        // First get the entry to find all stored chunk indices
        const getReq = store.get(fileHash);
        getReq.onsuccess = () => {
            const entry = getReq.result;
            store.delete(fileHash);
            if (entry && entry.chunks) {
                for (const idx of entry.chunks) {
                    store.delete(fileHash + ":" + idx);
                }
            }
            resolve();
        };
        getReq.onerror = () => {
            // Entry might not exist; just try deleting
            store.delete(fileHash);
            resolve();
        };
        tx.oncomplete = () => db.close();
    });
}

/**
 * List all stored chunk entries (for debugging / recovery).
 * @returns {Promise<Array<{fileHash: string, filename: string, noteId: number}>>}
 */
export async function listEntries() {
    const db = await openDB();
    return new Promise((resolve, reject) => {
        const tx = db.transaction(STORE_NAME, "readonly");
        const req = tx.objectStore(STORE_NAME).getAll();
        req.onsuccess = () => {
            const all = req.result || [];
            // Filter out chunk data entries (they have ":" in fileHash)
            const entries = all.filter(e => e.fileHash.indexOf(":") === -1);
            resolve(entries.map(e => ({
                fileHash: e.fileHash,
                filename: e.filename,
                noteId: e.noteId,
            })));
        };
        req.onerror = () => reject(req.error);
        tx.oncomplete = () => db.close();
    });
}
