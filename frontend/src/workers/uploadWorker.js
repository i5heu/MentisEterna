// Chunked Upload Web Worker
// Handles large file uploads with progress tracking and SHA-256 integrity checks.

const VERBOSE = true;

let activeUpload = null;
let aborted = false;

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
 * Read a specific chunk of a file as an ArrayBuffer.
 */
function readChunk(file, start, end) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(file.slice(start, end));
    });
}

/**
 * Start a chunked upload session on the server.
 */
async function startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks) {
    const body = JSON.stringify({
        inline,
        filename,
        mime_type: mimeType,
        total_size: totalSize,
        chunk_size: chunkSize,
        total_chunks: totalChunks,
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
 * Finish the chunked upload session (no body needed, server knows the session).
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
 * Poll server status during finish phase. Returns when the session is done
 * or when the AbortController's signal fires.
 */
async function pollFinishStatus(noteId, serverUploadId, token, uploadId, filename, totalSize, totalChunks, signal) {
    let lastLoggedStatus = "uploading";

    while (!signal.aborted) {
        try {
            const statusRes = await fetch(`/notes/${noteId}/chunked/${serverUploadId}`, {
                credentials: "include",
                signal,
            });

            if (statusRes.ok) {
                const statusData = await statusRes.json();
                const status = statusData.status || "";
                let statusText = "Finalizing...";
                if (status === "assembling") statusText = "Assembling chunks...";
                else if (status === "verifying") statusText = "Verifying integrity...";
                else if (status === "processing") statusText = "Encrypting and uploading...";
                else if (status === "done") statusText = "Done";
                else if (status === "failed") statusText = "Failed";

                if (VERBOSE && status !== lastLoggedStatus) {
                    console.log("[" + status + "]", statusText);
                    lastLoggedStatus = status;
                }

                post({
                    type: "progress", uploadId, filename,
                    loaded: totalSize, total: totalSize, percent: 100, speed: 0,
                    status: statusText,
                });
            }
        } catch (err) {
            // AbortError means we cancelled the poll — that's expected.
            // Other errors (network blip) just retry on next loop iteration.
            if (err.name === "AbortError") break;
        }

        await new Promise(r => setTimeout(r, 300));
    }
}

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

    activeUpload = { uploadId, noteId, token };
    aborted = false;

    try {
        // --- PHASE 1: Start session ---
        if (VERBOSE) console.log("[start] Creating upload session...");
        const session = await startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks);
        const serverUploadId = session.upload_id || uploadId;
        if (VERBOSE) console.log("[start] Server upload_id:", serverUploadId);

        // --- PHASE 2: Upload chunks ---
        if (VERBOSE) console.log("[uploading] Sending", totalChunks, "chunks...");
        let loaded = 0;
        const startTime = performance.now();

        post({ type: "progress", uploadId, filename, loaded: 0, total: totalSize, percent: 0, speed: 0, status: "uploading" });

        for (let i = 0; i < totalChunks; i++) {
            if (aborted) {
                console.warn("[cancelled] Upload aborted by user");
                try { await cancelUpload(token, noteId, serverUploadId); } catch (_) { /* ignore */ }
                post({ type: "error", uploadId, filename, error: "Upload cancelled" });
                if (VERBOSE) console.groupEnd();
                return;
            }

            const chunkStart = i * chunkSize;
            const chunkEnd = Math.min(chunkStart + chunkSize, totalSize);
            const chunkBuffer = await readChunk(file, chunkStart, chunkEnd);
            const chunkSha256 = await sha256(chunkBuffer);
            const chunkBlob = new Blob([chunkBuffer], { type: "application/octet-stream" });

            await uploadChunkWithRetry(token, noteId, serverUploadId, i, chunkBlob, chunkSha256);

            loaded += chunkBuffer.byteLength;

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

        if (VERBOSE) console.log("[uploading] All", totalChunks, "chunks uploaded. Waiting for server...");

        // --- PHASE 3: Server-side processing ---
        // Use AbortController to cleanly cancel the poll when finish completes.
        const pollAbort = new AbortController();
        const pollPromise = pollFinishStatus(
            noteId, serverUploadId, token, uploadId, filename,
            totalSize, totalChunks, pollAbort.signal,
        );

        // finishUpload blocks until server is done. Once it returns, abort the poll.
        const result = await finishUpload(token, noteId, serverUploadId);
        pollAbort.abort();

        // Wait for the poll to notice the abort signal
        await pollPromise;

        if (VERBOSE) {
            console.log("[done] Upload complete:", filename);
            console.log("[done] Result:", result);
            console.groupEnd();
        }

        post({ type: "complete", uploadId, filename, result });
        activeUpload = null;
    } catch (error) {
        console.error("[error]", filename, error.message || error);
        if (VERBOSE) console.groupEnd();
        post({ type: "error", uploadId, filename, error: error.message || String(error) });
        activeUpload = null;
    }
}

// Handle messages from the main thread
self.addEventListener("message", (event) => {
    const data = event.data || {};

    if (data.type === "upload") {
        doUpload(data);
        return;
    }

    if (data.type === "cancel") {
        aborted = true;
        if (activeUpload) {
            const { token, noteId, uploadId } = activeUpload;
            cancelUpload(token, noteId, uploadId).catch(() => { /* fire-and-forget */ });
        }
    }
});
