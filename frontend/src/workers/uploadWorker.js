// Chunked Upload Web Worker
// Handles large file uploads with progress tracking and SHA-256 integrity checks.

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
async function startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks, fileSha256) {
    const body = JSON.stringify({
        inline,
        filename,
        mime_type: mimeType,
        total_size: totalSize,
        chunk_size: chunkSize,
        total_chunks: totalChunks,
        file_sha256: fileSha256,
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

async function doUpload(data) {
    const { file, noteId, token, inline, chunkSize, uploadId } = data;
    const filename = file.name;
    const mimeType = file.type || "application/octet-stream";
    const totalSize = file.size;
    const totalChunks = Math.ceil(totalSize / chunkSize);

    activeUpload = { uploadId, noteId, token };
    aborted = false;

    try {
        // Compute full-file SHA-256 for server-side validation
        post({ type: "progress", uploadId, filename, loaded: 0, total: totalSize, percent: 0, speed: 0, status: "hashing" });

        const fileBuffer = await readChunk(file, 0, totalSize);
        const fileSha256 = await sha256(fileBuffer);

        // Start the upload session on server
        const session = await startUploadSession(token, noteId, inline, filename, mimeType, totalSize, chunkSize, totalChunks, fileSha256);
        const serverUploadId = session.upload_id || uploadId;

        // Upload chunks
        let loaded = 0;
        const startTime = performance.now();

        for (let i = 0; i < totalChunks; i++) {
            if (aborted) {
                try { await cancelUpload(token, noteId, serverUploadId); } catch (_) { /* ignore */ }
                post({ type: "error", uploadId, filename, error: "Upload cancelled" });
                return;
            }

            const chunkStart = i * chunkSize;
            const chunkEnd = Math.min(chunkStart + chunkSize, totalSize);
            const chunkBuffer = await readChunk(file, chunkStart, chunkEnd);
            const chunkSha256 = await sha256(chunkBuffer);
            const chunkBlob = new Blob([chunkBuffer], { type: "application/octet-stream" });

            await uploadChunk(token, noteId, serverUploadId, i, chunkBlob, chunkSha256);

            loaded += chunkBuffer.byteLength;

            // Calculate speed
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
            });
        }

        // Finish the upload
        post({ type: "progress", uploadId, filename, loaded: totalSize, total: totalSize, percent: 100, speed: 0, status: "finalizing" });
        const result = await finishUpload(token, noteId, serverUploadId);

        post({ type: "complete", uploadId, filename, result });
        activeUpload = null;
    } catch (error) {
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
