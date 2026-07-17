import { ref, reactive, computed, onUnmounted } from "vue";

// Module-level singleton -- one worker shared across all components.
let worker = null;
let workerRefs = 0;
let activeUploadId = null;
/** @type {Map<string, Function>} */
const uploadCallbacks = new Map();

function ensureWorker() {
    if (!worker) {
        worker = new Worker(new URL("../workers/uploadWorker.js", import.meta.url), { type: "module" });
    }
    workerRefs += 1;
    return worker;
}

function releaseWorker() {
    workerRefs -= 1;
    if (workerRefs <= 0 && worker) {
        worker.terminate();
        worker = null;
        workerRefs = 0;
    }
}

const DEFAULT_CHUNK_SIZE = 1024 * 1024; // 1 MB

/**
 * Vue composable: manages a shared upload queue with a Web Worker backend.
 */
export function useUploadQueue() {
    const queue = ref([]);
    const active = ref(null);
    const completed = reactive([]);
    let completeTimer = null;

    if (!worker) {
        ensureWorker();
    }

    function handleWorkerMessage(event) {
        const msg = event.data || {};

        switch (msg.type) {
            case "progress":
                active.value = {
                    uploadId: msg.uploadId,
                    filename: msg.filename,
                    loaded: msg.loaded,
                    total: msg.total,
                    percent: msg.percent,
                    speed: msg.speed,
                    status: msg.status,
                };
                break;

            case "chunk_done":
                break;

            case "complete": {
                const completedEntry = {
                    uploadId: msg.uploadId,
                    filename: msg.filename,
                    result: msg.result,
                    timestamp: Date.now(),
                };
                clearTimeout(completeTimer);
                completed.length = 0;
                completed.push(completedEntry);
                completeTimer = setTimeout(() => {
                    completed.length = 0;
                }, 5000);

                // Fire per-upload completion callback if registered
                if (uploadCallbacks.has(msg.uploadId)) {
                    const cb = uploadCallbacks.get(msg.uploadId);
                    uploadCallbacks.delete(msg.uploadId);
                    cb(msg.result);
                }

                active.value = null;
                activeUploadId = null;
                processQueue();
                break;
            }

            case "error": {
                const errorEntry = {
                    uploadId: msg.uploadId,
                    filename: msg.filename,
                    error: msg.error,
                };
                completed.length = 0;
                completed.push(errorEntry);

                active.value = null;
                activeUploadId = null;
                processQueue();
                break;
            }
        }
    }

    worker.addEventListener("message", handleWorkerMessage);

    function processQueue() {
        if (activeUploadId || queue.value.length === 0) return;

        const next = queue.value.shift();
        activeUploadId = next._id;

        // Register per-upload callback if provided
        if (next.onComplete) {
            uploadCallbacks.set(next._id, next.onComplete);
        }

        worker.postMessage({
            type: "upload",
            uploadId: next._id,
            file: next.file,
            noteId: next.noteId,
            token: next.token,
            inline: next.inline,
            chunkSize: next.chunkSize || DEFAULT_CHUNK_SIZE,
        });
    }

    function enqueue(file, noteId, token, opts = {}) {
        const entry = {
            _id: `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`,
            file,
            noteId,
            token,
            inline: !!opts.inline,
            chunkSize: opts.chunkSize || DEFAULT_CHUNK_SIZE,
            onComplete: opts.onComplete || null,
        };
        queue.value.push(entry);
        processQueue();
    }

    function enqueueAttachment(file, noteId, token, opts = {}) {
        enqueue(file, noteId, token, { ...opts, inline: false });
    }

    function enqueueInline(file, noteId, token, opts = {}) {
        enqueue(file, noteId, token, { ...opts, inline: true });
    }

    function cancel(uploadId) {
        if (activeUploadId && activeUploadId === uploadId) {
            worker.postMessage({ type: "cancel" });
            activeUploadId = null;
            active.value = null;
            processQueue();
        }
    }

    const queueCount = computed(() => queue.value.length);

    onUnmounted(() => {
        worker.removeEventListener("message", handleWorkerMessage);
        releaseWorker();
        clearTimeout(completeTimer);
    });

    return {
        queue,
        active,
        completed,
        queueCount,
        enqueue,
        enqueueAttachment,
        enqueueInline,
        cancel,
        defaultChunkSize: DEFAULT_CHUNK_SIZE,
    };
}
