import { ref, reactive, computed, onUnmounted } from "vue";
import * as ChunkStore from "../workers/chunkStore.js";

// Module-level singleton -- one worker shared across all components.
let worker = null;
let workerRefs = 0;
/** @type {Set<string>} */
const activeUploadIds = new Set();
/** @type {Set<string>} */
const resumedFileHashes = new Set(); // prevent duplicate resume entries
/** @type {Map<string, Function>} */
const uploadCallbacks = new Map();
/** @type {Map<string, number>} */
const uploadNoteIds = new Map(); // uploadId -> noteId

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
const DEFAULT_CONCURRENCY = 2; // max simultaneous uploads

/**
 * Vue composable: manages a shared upload queue with a Web Worker backend.
 * Supports multiple concurrent uploads via a configurable concurrency limit,
 * and auto-resume of interrupted uploads from IndexedDB.
 */
export function useUploadQueue() {
    const queue = ref([]);
    /** @type {import("vue").Ref<Array<{uploadId: string, filename: string, loaded: number, total: number, percent: number, speed: number, status: string}>>} */
    const active = ref([]);
    const completed = reactive([]);
    let completeTimer = null;
    const concurrency = ref(DEFAULT_CONCURRENCY);

    if (!worker) {
        ensureWorker();
    }

    function handleWorkerMessage(event) {
        const msg = event.data || {};

    	    switch (msg.type) {
    	        case "progress": {
    	            // Update or insert an active entry.
    	            const idx = active.value.findIndex(a => a.uploadId === msg.uploadId);
    	            const entry = {
    	                uploadId: msg.uploadId,
    	                filename: msg.filename,
    	                loaded: msg.loaded,
    	                total: msg.total,
    	                percent: msg.percent,
    	                speed: msg.speed,
    	                status: msg.status,
    	                noteId: msg.noteId || uploadNoteIds.get(msg.uploadId) || 0,
    	            };
                if (idx >= 0) {
                    active.value[idx] = entry;
                } else {
                    active.value.push(entry);
                }
                break;
            }

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
                completed.push(completedEntry);
                completeTimer = setTimeout(() => {
                    completed.length = 0;
                }, 5000);

                		// Fire per-upload completion callback if registered
                		if (uploadCallbacks.has(msg.uploadId)) {
                			const cb = uploadCallbacks.get(msg.uploadId);
                			uploadCallbacks.delete(msg.uploadId);
                			// Attach the uploadId as placeholder token so the callback
                			// can replace the provisional markdown link with the real URL.
                			const result = msg.result || {};
                			result._placeholderToken = msg.uploadId;
                			cb(result);
                		}

                // Remove from active list.
                activeUploadIds.delete(msg.uploadId);
                uploadNoteIds.delete(msg.uploadId);
                active.value = active.value.filter(a => a.uploadId !== msg.uploadId);
                processQueue();
                break;
            }

            case "error": {
                const errorEntry = {
                    uploadId: msg.uploadId,
                    filename: msg.filename,
                    error: msg.error,
                };
                completed.push(errorEntry);

                if (uploadCallbacks.has(msg.uploadId)) {
                    uploadCallbacks.delete(msg.uploadId);
                }

                activeUploadIds.delete(msg.uploadId);
                uploadNoteIds.delete(msg.uploadId);
                active.value = active.value.filter(a => a.uploadId !== msg.uploadId);
                processQueue();
                break;
            }
        }
    }

    worker.addEventListener("message", handleWorkerMessage);

    function processQueue() {
        // Start queued uploads until we hit the concurrency limit.
        while (activeUploadIds.size < concurrency.value && queue.value.length > 0) {
            const next = queue.value.shift();
            activeUploadIds.add(next._id);

            // Register per-upload callback if provided
            if (next.onComplete) {
                uploadCallbacks.set(next._id, next.onComplete);
            }

            if (next._resumeEntry) {
                // Resume upload from IndexedDB — strip Vue proxies before posting
                // because structuredClone can't handle Proxy objects.
                const plain = JSON.parse(JSON.stringify(next._resumeEntry));
                uploadNoteIds.set(next._id, plain.noteId || 0);
                worker.postMessage({
                    type: "resume",
                    uploadId: next._id,
                    fileHash: next._fileHash,
                    entry: plain,
                });
            } else {
                // Fresh upload (file provided)
                uploadNoteIds.set(next._id, next.noteId || 0);
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
        }
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
    		return entry._id;
    	}

    	/**
    	 * Enqueue multiple files at once. Each file gets its own queue entry
    	 * and they will upload concurrently (up to the concurrency limit).
    	 * @param {File[]} files
    	 * @param {number} noteId
    	 * @param {string} token
    	 * @param {{ inline?: boolean, chunkSize?: number, onComplete?: (result: any) => void }} [opts]
    	 * @returns {string[]}
    	 */
    	function enqueueMultiple(files, noteId, token, opts = {}) {
    		const ids = [];
    		for (const file of files) {
    			ids.push(enqueue(file, noteId, token, opts));
    		}
    		return ids;
    	}

    	function enqueueAttachment(file, noteId, token, opts = {}) {
    		return enqueue(file, noteId, token, { ...opts, inline: false });
    	}

    	function enqueueInline(file, noteId, token, opts = {}) {
    		return enqueue(file, noteId, token, { ...opts, inline: true });
    	}

    /**
     * Enqueue multiple files as inline uploads.
     * @param {File[]} files
     * @param {number} noteId
     * @param {string} token
     * @param {{ chunkSize?: number, onComplete?: (result: any) => void }} [opts]
     */
	function enqueueMultipleInline(files, noteId, token, opts = {}) {
		return enqueueMultiple(files, noteId, token, { ...opts, inline: true });
	}

    function cancel(uploadId) {
        if (activeUploadIds.has(uploadId)) {
            worker.postMessage({ type: "cancel", uploadId });
            activeUploadIds.delete(uploadId);
            active.value = active.value.filter(a => a.uploadId !== uploadId);
            processQueue();
        }
    }

    /**
     * Check IndexedDB for stored partial uploads and resume them.
     * Call this when a note is selected so we can resume orphaned uploads
     * from a previous browser session.
     * @param {string} token - auth token
     */
    async function resumeStoredUploads(token) {
        try {
            const entries = await ChunkStore.listEntries();
            for (const entry of entries) {
                // Skip entries we're already processing or have already enqueued for resume.
                if (activeUploadIds.has(entry.fileHash) || resumedFileHashes.has(entry.fileHash)) continue;

                // Skip entries already sitting in the queue waiting to be picked up.
                if (queue.value.some(q => q._fileHash === entry.fileHash)) continue;

                // Skip if no token yet
                if (!token) continue;

                const resumeEntry = {
                    _id: `resume-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`,
                    _fileHash: entry.fileHash,
                    _resumeEntry: {
                        noteId: entry.noteId,
                        token,
                        chunkSize: DEFAULT_CHUNK_SIZE,
                    },
                };

                // Try to get the full entry from IndexedDB
                const fullEntry = await ChunkStore.getChunkEntry(entry.fileHash);
                if (!fullEntry) continue;

                // Update token on the entry
                fullEntry.token = token;

                resumeEntry._resumeEntry = fullEntry;

                resumedFileHashes.add(entry.fileHash);
                queue.value.push(resumeEntry);
                // Add to active immediately so the progress panel shows it
                active.value.push({
                    uploadId: resumeEntry._id,
                    filename: entry.filename,
                    loaded: 0,
                    total: fullEntry.totalSize,
                    percent: 0,
                    speed: 0,
                    status: "resuming",
                });
            }
            processQueue();
        } catch (err) {
            console.warn("[useUploadQueue] Failed to resume stored uploads:", err);
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
        concurrency,
        enqueue,
        enqueueMultiple,
        enqueueAttachment,
        enqueueInline,
        enqueueMultipleInline,
        cancel,
        resumeStoredUploads,
        defaultChunkSize: DEFAULT_CHUNK_SIZE,
        defaultConcurrency: DEFAULT_CONCURRENCY,
    };
}
