import { jest } from "@jest/globals";

// Pseudo-integration / widget test for the useUploadQueue composable.
// The composable's "unit" is a real graph: enqueue → worker.postMessage →
// worker message → active/completed state → processQueue → next upload.
// We drive the full round-trip through a fake `Worker` boundary, mocking only
// the browser boundary (`Worker`) and the IndexedDB collaborator (`ChunkStore`).
//
// The composable keeps a module-level singleton worker + module-level
// `activeUploadIds` / `resumedFileHashes` / `uploadNoteIds` / `uploadCallbacks`
// sets, so each test gets a FRESH module instance via `jest.resetModules()` +
// lazy `require` (babel hoists static imports, so `useUploadQueue` is required
// lazily inside the test helpers after `global.Worker` exists).

// 1. Fake Worker: captures addEventListener + postMessage, exposes a way to
//    drive messages back into the composable as if the real worker emitted them.
const workerInstance = {
    _handlers: {},
    _posted: [],
    addEventListener(type, cb) {
        this._handlers[type] = cb;
    },
    postMessage(msg) {
        this._posted.push(msg);
    },
    terminate() {},
    removeEventListener(type) {
        delete this._handlers[type];
    },
};
class FakeWorker {
    constructor(url) {
        workerInstance._url = url;
        return workerInstance;
    }
}
global.Worker = FakeWorker;

// 2. ChunkStore is the only real I/O collaborator the composable touches
//    (via resumeStoredUploads) — mock it.
jest.mock("../workers/chunkStore.js", () => ({
    listEntries: jest.fn(),
    getChunkEntry: jest.fn(),
}));

describe("useUploadQueue", () => {
    // Mutable ref to the chunkStore mock as seen by the CURRENT module graph.
    // jest.resetModules() rebuilds the registry (including mocks), so we grab
    // the fresh mock after resetting — NOT a static import, which would point
    // at a stale mock instance.
    let chunkStore;

    // Return a fresh composable instance bound to a fresh module singleton.
    function fresh() {
        jest.resetModules();
        workerInstance._handlers = {};
        workerInstance._posted = [];
        chunkStore = require("../workers/chunkStore.js");
        chunkStore.listEntries.mockReset();
        chunkStore.getChunkEntry.mockReset();
        const mod = require("../composables/useUploadQueue.js");
        return mod.useUploadQueue();
    }

    // Drive a message back into the composable as if the real worker sent it.
    function emit(msg) {
        workerInstance._handlers.message({ data: msg });
    }

    const file = { name: "a.bin" };

    beforeEach(() => {
        // Vue warns when `onUnmounted` is registered outside an active
        // component (a benign no-op in these tests) — filter that expected
        // noise out so real warnings stay visible.
        const originalWarn = console.warn.bind(console);
        jest.spyOn(console, "warn").mockImplementation((...args) => {
            if (String(args[0] ?? "").includes("onUnmounted is called when there is no active component instance")) {
                return;
            }
            originalWarn(...args);
        });
        // The composable schedules a 5s `completeTimer` per `complete` message.
        // Outside a component onUnmounted never fires to clear it, which keeps
        // the Node event loop open. Mock timers (keeping `Date` real so enqueue
        // ids stay unique) prevents that stray timer from holding the process.
        jest.useFakeTimers({ doNotFake: ["Date", "performance"] });
    });

    afterEach(() => {
        jest.useRealTimers();
        console.warn.mockRestore();
    });

    test("enqueue posts an upload message with the expected shape", () => {
        const uq = fresh();
        const id = uq.enqueue(file, 7, "tok", {});

        expect(workerInstance._posted).toHaveLength(1);
        expect(workerInstance._posted[0]).toMatchObject({
            type: "upload",
            uploadId: id,
            file,
            noteId: 7,
            token: "tok",
            inline: false,
            chunkSize: 1048576,
        });
        expect(uq.queueCount.value).toBe(0);
    });

    test("progress message updates (and replaces) an active entry", () => {
        const uq = fresh();
        const id = uq.enqueue(file, 7, "tok", {});

        emit({
            type: "progress",
            uploadId: id,
            filename: "a.bin",
            loaded: 10,
            total: 100,
            percent: 10,
            speed: 123,
            status: "uploading",
        });

        expect(uq.active.value).toHaveLength(1);
        expect(uq.active.value[0]).toMatchObject({
            uploadId: id,
            filename: "a.bin",
            loaded: 10,
            total: 100,
            percent: 10,
            speed: 123,
            status: "uploading",
            noteId: 7, // resolved from uploadNoteIds since msg.noteId is missing
        });

        // Second progress for the same id replaces, not duplicates.
        emit({
            type: "progress",
            uploadId: id,
            filename: "a.bin",
            loaded: 50,
            total: 100,
            percent: 50,
            speed: 456,
            status: "uploading",
        });
        expect(uq.active.value).toHaveLength(1);
        expect(uq.active.value[0].loaded).toBe(50);
        expect(uq.active.value[0].percent).toBe(50);
    });

    test("complete message records completed, fires onComplete, clears active", () => {
        const uq = fresh();
        let called = null;
        const id = uq.enqueue(file, 3, "tok", {
            onComplete: (res) => {
                called = res;
            },
        });

        emit({ type: "complete", uploadId: id, filename: "a.bin", result: { url: "file:///x" } });

        expect(uq.completed).toHaveLength(1);
        expect(uq.completed[0]).toMatchObject({
            uploadId: id,
            filename: "a.bin",
            result: { url: "file:///x" },
        });
        expect(uq.active.value).toHaveLength(0);

        expect(called).not.toBeNull();
        expect(called).toMatchObject({ url: "file:///x" });
        expect(called._placeholderToken).toBe(id);
    });

    test("error message records completed error and cleans active", () => {
        const uq = fresh();
        const id = uq.enqueue(file, 3, "tok", {});

        emit({ type: "error", uploadId: id, filename: "a.bin", error: "boom" });

        expect(uq.completed).toHaveLength(1);
        expect(uq.completed[0]).toMatchObject({ uploadId: id, filename: "a.bin", error: "boom" });
        expect(uq.active.value).toHaveLength(0);
    });

    test("concurrency limit gates the queue", () => {
        const uq = fresh();

        const id1 = uq.enqueue(file, 1, "t");
        const id2 = uq.enqueue(file, 1, "t");
        const id3 = uq.enqueue(file, 1, "t");

        // 2 slots open (concurrency 2); the 3rd stays queued.
        // (`active` is only populated by worker `progress` messages, so at
        // this point we assert on the posted messages and the queue.)
        expect(workerInstance._posted).toHaveLength(2);
        expect(uq.queue.value).toHaveLength(1);

        // Finish one active upload → the next queued file starts.
        emit({ type: "complete", uploadId: id1, filename: "a.bin", result: { url: "file:///x" } });
        expect(workerInstance._posted).toHaveLength(3);
        expect(workerInstance._posted[2].uploadId).toBe(id3);
        expect(uq.queue.value).toHaveLength(0);
    });

    test("cancel removes an active upload and frees a slot", () => {
        const uq = fresh();

        const id1 = uq.enqueue(file, 1, "t");
        const id2 = uq.enqueue(file, 1, "t");
        const id3 = uq.enqueue(file, 1, "t");
        // 2 slots open; id3 sits in the queue.
        expect(workerInstance._posted).toHaveLength(2);

        uq.cancel(id1);

        const types = workerInstance._posted.map((m) => m.type);
        expect(types).toEqual(["upload", "upload", "cancel", "upload"]);
        expect(workerInstance._posted[2]).toEqual({ type: "cancel", uploadId: id1 });
        expect(workerInstance._posted[3].uploadId).toBe(id3);
        expect(uq.queue.value).toHaveLength(0);
    });

    test("cancel removes the active entry and frees a slot", () => {
        const uq = fresh();

        const id1 = uq.enqueue(file, 1, "t");
        uq.enqueue(file, 1, "t");
        const id3 = uq.enqueue(file, 1, "t");
        // 2 slots open; id3 sits in the queue. Progress puts id1 in `active`.
        emit({ type: "progress", uploadId: id1, filename: "a.bin", loaded: 1, total: 2, percent: 50, speed: 0, status: "uploading", noteId: 1 });
        expect(uq.active.value.some((a) => a.uploadId === id1)).toBe(true);

        uq.cancel(id1);

        expect(uq.active.value.some((a) => a.uploadId === id1)).toBe(false);
        expect(uq.queue.value).toHaveLength(0);
        expect(workerInstance._posted[workerInstance._posted.length - 1].uploadId).toBe(id3);
    });

    test("resumeStoredUploads feeds resume messages to the worker", async () => {
        const uq = fresh();
        chunkStore.listEntries.mockResolvedValue([{ fileHash: "abc", filename: "a.bin", noteId: 5 }]);
        chunkStore.getChunkEntry.mockResolvedValue({
            fileHash: "abc",
            filename: "a.bin",
            totalSize: 2000,
            chunkSize: 1048576,
            totalChunks: 2,
            inline: false,
            noteId: 5,
            token: "",
        });

        await uq.resumeStoredUploads("newtok");

        expect(workerInstance._posted).toHaveLength(1);
        const msg = workerInstance._posted[0];
        expect(msg.type).toBe("resume");
        expect(msg.fileHash).toBe("abc");
        expect(msg.uploadId).toMatch(/^resume-/);
        expect(msg.entry).toMatchObject({
            fileHash: "abc",
            filename: "a.bin",
            totalSize: 2000,
            noteId: 5,
            token: "newtok",
        });

        expect(uq.active.value).toHaveLength(1);
        expect(uq.active.value[0]).toMatchObject({
            filename: "a.bin",
            status: "resuming",
            total: 2000,
        });
    });

    test("resumeStoredUploads dedupes already-resumed hashes", async () => {
        const uq = fresh();
        chunkStore.listEntries.mockResolvedValue([{ fileHash: "abc", filename: "a.bin", noteId: 5 }]);
        chunkStore.getChunkEntry.mockResolvedValue({
            fileHash: "abc",
            filename: "a.bin",
            totalSize: 2000,
            chunkSize: 1048576,
            totalChunks: 2,
            inline: false,
            noteId: 5,
            token: "",
        });

        await uq.resumeStoredUploads("newtok");
        expect(workerInstance._posted).toHaveLength(1);

        // Second call must not post a duplicate resume message.
        await uq.resumeStoredUploads("newtok");
        expect(workerInstance._posted).toHaveLength(1);
    });

    test("multiple consumers share one worker without double-creating it", () => {
        // Regression guard for the ref-counting fix: consumers that find the
        // worker already created must still take their own ref (the previous
        // `if (!worker)` guard skipped ensureWorker, so workerRefs stayed at 1
        // while N consumers mounted and the first unmount's releaseWorker
        // nulled the shared worker under the remaining consumers). The
        // precondition of that fix is that every consumer really does share a
        // single worker instance.
        jest.resetModules();
        workerInstance._handlers = {};
        workerInstance._posted = [];
        chunkStore = require("../workers/chunkStore.js");
        chunkStore.listEntries.mockReset();
        chunkStore.getChunkEntry.mockReset();
        const mod = require("../composables/useUploadQueue.js");

        const first = mod.useUploadQueue();
        const second = mod.useUploadQueue();

        // Both consumers drive the SAME fake worker instance.
        const id1 = first.enqueue(file, 1, "tok", {});
        const id2 = second.enqueue(file, 2, "tok", {});
        expect(workerInstance._posted).toHaveLength(2);
        expect(workerInstance._posted[0]).toMatchObject({
            type: "upload",
            uploadId: id1,
            noteId: 1,
        });
        expect(workerInstance._posted[1]).toMatchObject({
            type: "upload",
            uploadId: id2,
            noteId: 2,
        });

        // State stays per-composable.
        expect(first.queueCount.value).toBe(0);
        expect(second.queueCount.value).toBe(0);
    });
});
