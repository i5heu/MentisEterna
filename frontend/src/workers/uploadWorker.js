// Chunked Upload Web Worker
// Handles large file uploads with progress tracking and SHA-256 integrity checks.
// Supports multiple concurrent uploads and resume after tab close / browser crash.
//
// Upload flow:
//   1. Hash the file → compute file_sha256
//   2. Read all chunks into IndexedDB (so we can resume if the browser crashes)
//   3. POST /notes/{id}/chunked/start (idempotent: returns existing session if same hash)
//   4. Upload each missing chunk from IndexedDB
//   5. POST /notes/{id}/chunked/{uploadID}/finish
//   6. Poll for server-side processing completion
//   7. Clean up IndexedDB entry

import * as ChunkStore from "./chunkStore.js";

const VERBOSE = true;

// Track all active uploads by uploadId, each with its own abort flag.
/** @type {Map<string, {aborted: boolean}>} */
const activeUploads = new Map();

function post(msg) {
    self.postMessage(msg);
}

/**
 * Compute SHA-256 hash of an ArrayBuffer using SubtleCrypto.
 */
async function sha256(buffer) {
    const hashBuffer = await crypto.subtle.digest("SHA-256", buffer);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * Read a specific chunk of a file as a Uint8Array.
 */
function readChunk(file, start, end) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(new Uint8Array(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(file.slice(start, end));
    });
}

/**
 * Start a chunked upload session on the server.
 */
async function startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks, fileSha256, placeholderToken) {
	const body = JSON.stringify({
		inline,
		filename,
		mime_type: mimeType,
		total_size: totalSize,
		chunk_size: chunkSize,
		total_chunks: totalChunks,
		file_sha256: fileSha256 || "",
		placeholder_token: placeholderToken || "",
	});

    const res = await fetch(`/notes/${noteId}/chunked/start`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body,
    });

    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `Start failed: HTTP ${res.status}`);
    }
    return res.json();
}

/**
 * Upload a single chunk to the server.
 */
async function uploadChunk(token, noteId, uploadId, index, chunkBlob, chunkSha256) {
    const formData = new FormData();
    formData.append("chunk", chunkBlob, "chunk");
    formData.append("index", String(index));
    formData.append("sha256", chunkSha256);

    const res = await fetch(`/notes/${noteId}/chunked/${uploadId}/chunk`, {
        method: "POST",
        credentials: "include",
        body: formData,
    });

    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `Chunk ${index} failed: HTTP ${res.status}`);
    }
    return res.json();
}

/**
 * Upload a chunk with retry on transient failures.
 * Makes up to 3 attempts with exponential backoff (1s, 2s).
 */
async function uploadChunkWithRetry(token, noteId, uploadId, index, chunkBlob, chunkSha256) {
    const maxAttempts = 3;

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
        try {
            return await uploadChunk(token, noteId, uploadId, index, chunkBlob, chunkSha256);
        } catch (err) {
            if (attempt < maxAttempts - 1) {
                const delay = [1000, 2000][attempt];
                if (VERBOSE) console.warn("[retry] Chunk", index, "attempt", attempt + 1, "failed:", err.message, "-- retrying in", delay + "ms");
                await new Promise(r => setTimeout(r, delay));
            } else {
                throw err;
            }
        }
    }
}

/**
 * Finish the chunked upload session.
 */
async function finishUpload(token, noteId, uploadId) {
    const res = await fetch(`/notes/${noteId}/chunked/${uploadId}/finish`, {
        method: "POST",
        credentials: "include",
    });

    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `Finish failed: HTTP ${res.status}`);
    }
    return res.json();
}

/**
 * Abort an active upload session.
 */
async function cancelUpload(token, noteId, uploadId) {
    const res = await fetch(`/notes/${noteId}/chunked/${uploadId}/cancel`, {
        method: "POST",
        credentials: "include",
    });
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `Cancel failed: HTTP ${res.status}`);
    }
    return res.json();
}

/**
 * Poll server status during finish phase. If the session reaches "done"
 * with a result, returns the result. If the poll is stopped externally
 * (pollState.done set to true), returns null.
 * @returns {Promise<object|null>}
 */
async function pollFinishStatus(noteId, serverUploadId, token, uploadId, filename, totalSize, totalChunks, pollState) {
    let lastLoggedStatus = "uploading";

    while (!pollState.done) {
        try {
            const statusRes = await fetch(`/notes/${noteId}/chunked/${serverUploadId}`, {
                credentials: "include",
            });

            if (pollState.done) break;

            if (statusRes.ok) {
                const statusData = await statusRes.json();
                const status = statusData.status || "";
                let statusText = "Finalizing...";
                if (status === "assembling") statusText = "Assembling chunks...";
                else if (status === "verifying") statusText = "Verifying integrity...";
                else if (status === "processing") statusText = "Encrypting and uploading...";
                else if (status === "done") statusText = "Done";
                else if (status === "failed") statusText = "Failed";
                else if (status === "not_found") statusText = "Finalizing...";

                if (VERBOSE && status !== lastLoggedStatus) {
                    console.log("[" + status + "]", statusText);
                    lastLoggedStatus = status;
                }

                post({
                    type: "progress", uploadId, filename,
                    loaded: totalSize, total: totalSize, percent: 100, speed: 0,
                    status: statusText, noteId,
                });

                // If done, return the result so the caller can propagate it.
                if (status === "done") {
                    pollState.done = true;
                    return statusData.result || null;
                }
            }
        } catch (err) {
            if (VERBOSE) console.warn("[poll] status check failed:", err.message);
        }

        if (pollState.done) break;
        await new Promise(r => setTimeout(r, 300));
    }
    return null;
}

function isAborted(uploadId) {
    const state = activeUploads.get(uploadId);
    return !state || state.aborted;
}

/**
 * Fetch pending upload sessions for a note from the server.
 * This allows resuming uploads where previously uploaded chunks are still on the server.
 * @returns {Promise<Array>}
 */
async function fetchPendingSessions(noteId, token) {
    const res = await fetch(`/notes/${noteId}/chunked/pending`, {
        credentials: "include",
    });
    if (!res.ok) return [];
    return res.json();
}

/**
 * Main upload orchestrator.
 * @param {{ file: File, noteId: number, token: string, inline: boolean, chunkSize: number, uploadId: string }} data
 */
async function doUpload(data) {
    const { file, noteId, token, inline, chunkSize, uploadId } = data;
    const filename = file.name;
    const mimeType = file.type || "application/octet-stream";
    const totalSize = file.size;
    const totalChunks = Math.ceil(totalSize / chunkSize);

    if (VERBOSE) {
        console.group("uploadWorker:", filename);
        console.log("File size:", totalSize, "bytes,", "Chunks:", totalChunks, "Chunk size:", chunkSize);
    }

    activeUploads.set(uploadId, { aborted: false });

    try {
        // --- PHASE 1: Compute file SHA-256 ---
        if (VERBOSE) console.log("[hashing] Computing file SHA-256...");
        post({ type: "progress", uploadId, filename, loaded: 0, total: totalSize, percent: 0, speed: 0, status: "hashing" });

        const fileBuffer = await readChunk(file, 0, totalSize);
        const fileSha256 = await sha256(fileBuffer);
        if (VERBOSE) console.log("[hashing] File SHA-256:", fileSha256.slice(0, 12) + "...");

        if (isAborted(uploadId)) {
            if (VERBOSE) console.warn("[cancelled] Upload aborted during hashing");
            post({ type: "error", uploadId, filename, error: "Upload cancelled" });
            if (VERBOSE) console.groupEnd();
            return;
        }

        // --- PHASE 2: Persist all chunks to IndexedDB for resume ---
        if (VERBOSE) console.log("[staging] Persisting chunks to IndexedDB...");
        post({ type: "progress", uploadId, filename, loaded: 0, total: totalSize, percent: 0, speed: 0, status: "staging" });

        const meta = {
            filename,
            mimeType,
            totalSize,
            chunkSize,
            totalChunks,
            inline,
            noteId,
            token,
            placeholderToken: uploadId,
        };
        for (let i = 0; i < totalChunks; i++) {
            if (isAborted(uploadId)) {
                if (VERBOSE) console.warn("[cancelled] Upload aborted during staging");
                post({ type: "error", uploadId, filename, error: "Upload cancelled" });
                if (VERBOSE) console.groupEnd();
                return;
            }
            const chunkStart = i * chunkSize;
            const chunkEnd = Math.min(chunkStart + chunkSize, totalSize);
            const chunkData = await readChunk(file, chunkStart, chunkEnd);
            await ChunkStore.putChunk(fileSha256, meta, i, chunkData);
        }
        if (VERBOSE) console.log("[staging] All", totalChunks, "chunks persisted to IndexedDB");

        if (isAborted(uploadId)) {
            if (VERBOSE) console.warn("[cancelled] Upload aborted after staging");
            post({ type: "error", uploadId, filename, error: "Upload cancelled" });
            if (VERBOSE) console.groupEnd();
            return;
        }

        // --- PHASE 3: Start or resume session ---
        if (VERBOSE) console.log("[start] Creating/resuming upload session...");
        	const session = await startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks, fileSha256, uploadId);
        const serverUploadId = session.upload_id || uploadId;
        const alreadyDone = session.chunks_done || [];
        if (VERBOSE) {
            console.log("[start] Server upload_id:", serverUploadId);
            if (alreadyDone.length > 0) {
                console.log("[start] Resuming — server already has", alreadyDone.length, "/", totalChunks, "chunks");
            }
        }

        	if (isAborted(uploadId)) {
        		if (VERBOSE) console.warn("[cancelled] Upload aborted after start");
        		try { await cancelUpload(token, noteId, serverUploadId); } catch (_) { /* ignore */ }
        		post({ type: "error", uploadId, filename, error: "Upload cancelled" });
        		if (VERBOSE) console.groupEnd();
        		return;
        	}

        	// Tell the main thread the session is created so it can insert
        	// placeholder markdown immediately (before the upload completes).
        	if (inline) {
        		post({ type: "started", uploadId, filename, noteId });
        	}

        	// --- PHASE 4: Upload missing chunks from IndexedDB (if any) ---
        const allDone = alreadyDone.length === totalChunks;
        if (allDone) {
            if (VERBOSE) console.log("[uploading] All chunks already on server. Skipping to poll.");
            post({ type: "progress", uploadId, filename, loaded: totalSize, total: totalSize, percent: 100, speed: 0, status: "Finalizing..." });
        } else {
            if (VERBOSE) console.log("[uploading] Sending chunks...");
            let loaded = Math.min(alreadyDone.length * chunkSize, totalSize);
            const startTime = performance.now();

            post({ type: "progress", uploadId, filename, loaded, total: totalSize, percent: Math.min(100, Math.round((loaded / totalSize) * 100)), speed: 0, status: "uploading" });

            for (let i = 0; i < totalChunks; i++) {
                if (isAborted(uploadId)) {
                    console.warn("[cancelled] Upload aborted by user");
                    try { await cancelUpload(token, noteId, serverUploadId); } catch (_) { /* ignore */ }
                    post({ type: "error", uploadId, filename, error: "Upload cancelled" });
                    if (VERBOSE) console.groupEnd();
                    return;
                }

                // Skip chunks the server already has.
                if (alreadyDone.includes(i)) {
                    loaded = Math.min((i + 1) * chunkSize, totalSize);
                    continue;
                }

                // Read chunk from IndexedDB.
                const chunkData = await ChunkStore.getChunkData(fileSha256, i);
                if (!chunkData) {
                    throw new Error(`Chunk ${i} missing from IndexedDB — cannot resume. Re-add the file.`);
                }

                const chunkBuffer = chunkData.buffer.slice(
                    chunkData.byteOffset,
                    chunkData.byteOffset + chunkData.byteLength,
                );
                const chunkSha256 = await sha256(chunkBuffer);
                const chunkBlob = new Blob([chunkBuffer], { type: "application/octet-stream" });

                await uploadChunkWithRetry(token, noteId, serverUploadId, i, chunkBlob, chunkSha256);

                loaded = Math.min(loaded + chunkData.byteLength, totalSize);

                const elapsed = (performance.now() - startTime) / 1000;
                const speed = elapsed > 0 ? loaded / elapsed : 0;
                const percent = Math.round((loaded / totalSize) * 100);

                if (VERBOSE) console.log("[uploading] Chunk", i + 1, "/", totalChunks, "sha256:", chunkSha256.slice(0, 12) + "...", percent + "%");

                post({ type: "chunk_done", uploadId, index: i });
                post({
                    type: "progress",
                    uploadId,
                    filename,
                    loaded,
                    total: totalSize,
                    percent,
                    speed,
                    status: "uploading",
                });
            }
            if (VERBOSE) console.log("[uploading] All chunks uploaded. Waiting for server...");
        }

        // --- PHASE 5: Server-side processing ---
        // Poll for completion. If we uploaded chunks, call finish to trigger
        // server-side assembly. If all chunks were already on the server, just
        // poll — the server is already processing from a previous request.
        const pollState = { done: false };
        const pollPromise = pollFinishStatus(
            noteId, serverUploadId, token, uploadId, filename,
            totalSize, totalChunks, pollState,
        );

        let result = null;
        if (allDone) {
            // All chunks already on server — just wait for the poll to see "done".
            if (VERBOSE) console.log("[poll] Waiting for server to finish processing...");
            result = await pollPromise;
        } else {
            // We uploaded chunks — trigger server-side assembly.
            try {
                result = await finishUpload(token, noteId, serverUploadId);
            } catch (err) {
                const msg = (err.message || "").toLowerCase();
                if (msg.includes("already being finalized") || msg.includes("409")) {
                    if (VERBOSE) console.log("[finish] Server already processing. Waiting for poll...");
                    result = await pollPromise;
                } else {
                    pollState.done = true;
                    throw err;
                }
            }
            // finishUpload succeeded — tell the poll to stop.
            pollState.done = true;
            // Drain the poll promise (it may have already returned with the result).
            const pollResult = await pollPromise;
            if (!result) result = pollResult;
        }

        // --- PHASE 6: Clean up IndexedDB ---
        await ChunkStore.deleteChunkEntry(fileSha256);

        if (VERBOSE) {
            console.log("[done] Upload complete:", filename);
            console.log("[done] Result:", result);
            console.groupEnd();
        }

        post({ type: "complete", uploadId, filename, result });
    } catch (error) {
        console.error("[error]", filename, error.message || error);
        if (VERBOSE) console.groupEnd();
        post({ type: "error", uploadId, filename, error: error.message || String(error) });
    } finally {
        activeUploads.delete(uploadId);
    }
}

/**
 * Resume a pending upload from IndexedDB.
 * Used when the user returns to a page and we detect a stored partial upload.
 * @param {string} fileHash
 * @param {object} entry - chunk store entry
 * @param {string} uploadId - fresh upload ID for this resume attempt
 */
async function doResume(fileHash, entry, uploadId) {
    const {
        filename,
        mimeType,
        totalSize,
        chunkSize,
        totalChunks,
        inline,
        noteId,
        token,
        placeholderToken,
    } = entry;

    if (VERBOSE) {
        console.group("uploadWorker [resume]:", filename);
        console.log("Resuming upload of", totalSize, "bytes,", totalChunks, "chunks");
    }

    activeUploads.set(uploadId, { aborted: false });

    try {
        // Check if server has a pending session already.
        const pendingSessions = await fetchPendingSessions(noteId, token);
        let serverUploadId = null;
        let alreadyDone = [];

        // Try to match by file hash.
        for (const s of pendingSessions) {
            if (s.file_sha256 === fileHash) {
                serverUploadId = s.upload_id;
                alreadyDone = s.chunks_done || [];
                break;
            }
        }

        if (!alreadyDone.length && !serverUploadId) {
            if (VERBOSE) console.log("[resume] No pending server session. Starting fresh.");
        }

        // Start (idempotent) or resume session.
        if (VERBOSE) console.log("[resume] Starting upload session (idempotent)...");
        	const stablePlaceholderToken = placeholderToken || uploadId;
        	const session = await startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks, fileHash, stablePlaceholderToken);
        serverUploadId = session.upload_id || uploadId;
        alreadyDone = session.chunks_done || [];

        if (session.status === "done" && session.result) {
            await ChunkStore.deleteChunkEntry(fileHash);
            if (VERBOSE) {
                console.log("[resume] Server already completed this upload. Cleaning up local resume data.");
                console.groupEnd();
            }
            post({ type: "complete", uploadId, filename, result: session.result });
            return;
        }

        if (VERBOSE) {
            console.log("[resume] Server upload_id:", serverUploadId);
            console.log("[resume] Server already has", alreadyDone.length, "/", totalChunks, "chunks");
        }

        // Upload missing chunks from IndexedDB (if any).
        const allDone = alreadyDone.length === totalChunks;
        if (allDone) {
            if (VERBOSE) console.log("[resume] All chunks already on server. Skipping to poll.");
            post({ type: "progress", uploadId, filename, loaded: totalSize, total: totalSize, percent: 100, speed: 0, status: "Finalizing..." });
        } else {
            let loaded = Math.min(alreadyDone.length * chunkSize, totalSize);
            const startTime = performance.now();

            post({ type: "progress", uploadId, filename, loaded, total: totalSize, percent: Math.min(100, Math.round((loaded / totalSize) * 100)), speed: 0, status: "uploading" });

            for (let i = 0; i < totalChunks; i++) {
                if (isAborted(uploadId)) {
                    console.warn("[cancelled] Upload aborted by user");
                    try { await cancelUpload(token, noteId, serverUploadId); } catch (_) { /* ignore */ }
                    post({ type: "error", uploadId, filename, error: "Upload cancelled" });
                    if (VERBOSE) console.groupEnd();
                    return;
                }

                // Skip chunks the server already has.
                if (alreadyDone.includes(i)) {
                    loaded = Math.min((i + 1) * chunkSize, totalSize);
                    continue;
                }

                const chunkData = await ChunkStore.getChunkData(fileHash, i);
                if (!chunkData) {
                    throw new Error(`Chunk ${i} missing from IndexedDB — cannot resume. Re-add the file.`);
                }

                const chunkBuffer = chunkData.buffer.slice(
                    chunkData.byteOffset,
                    chunkData.byteOffset + chunkData.byteLength,
                );
                const chunkSha256 = await sha256(chunkBuffer);
                const chunkBlob = new Blob([chunkBuffer], { type: "application/octet-stream" });

                await uploadChunkWithRetry(token, noteId, serverUploadId, i, chunkBlob, chunkSha256);

                loaded = Math.min(loaded + chunkData.byteLength, totalSize);

                const elapsed = (performance.now() - startTime) / 1000;
                const speed = elapsed > 0 ? loaded / elapsed : 0;
                const percent = Math.round((loaded / totalSize) * 100);

                post({ type: "chunk_done", uploadId, index: i });
                post({
                    type: "progress",
                    uploadId,
                    filename,
                    loaded,
                    total: totalSize,
                    percent,
                    speed,
                    status: "uploading",
                    noteId,
                });
            }
            if (VERBOSE) console.log("[resume] All chunks uploaded. Waiting for server...");
        }

        // Server-side processing.
        const pollState2 = { done: false };
        const pollPromise2 = pollFinishStatus(
            noteId, serverUploadId, token, uploadId, filename,
            totalSize, totalChunks, pollState2,
        );

        let result2 = null;
        if (allDone) {
            if (VERBOSE) console.log("[poll] Waiting for server to finish processing...");
            result2 = await pollPromise2;
        } else {
            try {
                result2 = await finishUpload(token, noteId, serverUploadId);
            } catch (err) {
                const msg = (err.message || "").toLowerCase();
                if (msg.includes("already being finalized") || msg.includes("409")) {
                    if (VERBOSE) console.log("[finish] Server already processing. Waiting for poll...");
                    result2 = await pollPromise2;
                } else {
                    pollState2.done = true;
                    throw err;
                }
            }
            pollState2.done = true;
            const pollResult2 = await pollPromise2;
            if (!result2) result2 = pollResult2;
        }

        // Clean up IndexedDB.
        await ChunkStore.deleteChunkEntry(fileHash);

        if (VERBOSE) {
            console.log("[resume] Upload complete:", filename);
            console.groupEnd();
        }

        post({ type: "complete", uploadId, filename, result: result2 });
    } catch (error) {
        console.error("[resume error]", filename, error.message || error);
        if (VERBOSE) console.groupEnd();
        post({ type: "error", uploadId, filename, error: error.message || String(error) });
    } finally {
        activeUploads.delete(uploadId);
    }
}

// Handle messages from the main thread
self.addEventListener("message", (event) => {
    const data = event.data || {};

    if (data.type === "upload") {
        doUpload(data);
        return;
    }

    if (data.type === "resume") {
        // data: { fileHash, entry, uploadId }
        doResume(data.fileHash, data.entry, data.uploadId);
        return;
    }

    if (data.type === "cancel") {
        const state = activeUploads.get(data.uploadId);
        if (state) {
            state.aborted = true;
        }
    }
});
