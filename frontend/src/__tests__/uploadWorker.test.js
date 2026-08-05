import { jest } from "@jest/globals";

// Flagship heavy-dependency test: mocks `self`, `fetch`, `FileReader`, and
// `ChunkStore`, then drives the worker through the message handler it
// registers at import time. `doUpload`/`doResume` are not exported, so we
// capture the handler that `self.addEventListener("message", …)` registers.
//
// IMPORT-ORDER NOTE: static `import` statements are hoisted above module
// body statements, so `global.self` must NOT be assigned via an import-time
// side effect of this file's imports. We assign the globals and call
// jest.mock at top scope, then `require` the worker lazily inside
// `beforeAll` after `self` exists.

// 1. Capture the message handler registered at import time.
const messageHandlers = {};
global.self = {
    postMessage: jest.fn(),
    addEventListener: (type, cb) => {
        messageHandlers[type] = cb;
    },
};

// 2. FileReader polyfill (Node 22 has none): readAsArrayBuffer synchronously.
global.FileReader = class {
    readAsArrayBuffer(data) {
        this.result = data;
        this.onload?.();
    }
};

// 3. ChunkStore is the only real I/O dependency — mock it.
jest.mock("../workers/chunkStore.js", () => ({
    putChunk: jest.fn().mockResolvedValue(),
    getChunkData: jest.fn(),
    deleteChunkEntry: jest.fn().mockResolvedValue(),
}));
import * as ChunkStore from "../workers/chunkStore.js";

// 4. Real global.fetch is replaced by a per-test route mock.
global.fetch = jest.fn();

describe("uploadWorker", () => {
    beforeAll(() => {
        // self is defined above; safe to load the worker now.
        require("../workers/uploadWorker.js");

        // The worker's VERBOSE flag logs heavily (console.group/log/warn/error)
        // during each upload. No-op those calls so test output stays clean;
        // this does not affect the assertions. Restored in afterAll.
        jest.spyOn(console, "log").mockImplementation(() => {});
        jest.spyOn(console, "warn").mockImplementation(() => {});
        jest.spyOn(console, "error").mockImplementation(() => {});
        jest.spyOn(console, "group").mockImplementation(() => {});
        jest.spyOn(console, "groupEnd").mockImplementation(() => {});
    });

    afterAll(() => {
        jest.restoreAllMocks();
    });

    beforeEach(() => {
        self.postMessage.mockClear();
        fetch.mockClear();
        ChunkStore.getChunkData.mockReset();
        ChunkStore.getChunkData.mockResolvedValue(new Uint8Array(8));
        ChunkStore.putChunk.mockClear();
        ChunkStore.deleteChunkEntry.mockClear();
    });

    // Fake file: 2000 bytes at chunkSize 1024 == exactly 2 chunks.
    const file = {
        name: "a.bin",
        size: 2000,
        type: "application/octet-stream",
        slice: (s, e) => new Uint8Array(new ArrayBuffer(e - s)),
    };

    // Drive a message through the captured handler and flush worker microtasks.
    function dispatchUpload(msg) {
        messageHandlers.message({ data: msg });
        return new Promise((r) => setTimeout(r, 20));
    }

    // Route-based fetch mock keyed on `${METHOD} ${url}`.
    function mockRoutes(routes) {
        fetch.mockImplementation((url, options = {}) => {
            const key = `${options.method || "GET"} ${url}`;
            const route = routes[key];
            if (!route) {
                throw new Error(`unexpected fetch: ${key}`);
            }
            return Promise.resolve(route);
        });
    }

    function respond(body, ok = true, status = 200) {
        return { ok, status, json: async () => body, text: async () => "" };
    }

    function postedMessages() {
        return self.postMessage.mock.calls.map((c) => c[0]);
    }

    test("happy upload uploads all chunks and completes", async () => {
        mockRoutes({
            "POST /notes/1/chunked/start": respond({
                upload_id: "sid",
                chunks_done: [],
            }),
            "POST /notes/1/chunked/sid/chunk": respond({}),
            "POST /notes/1/chunked/sid/finish": respond({ url: "file:///x" }),
            "GET /notes/1/chunked/sid": respond({
                status: "done",
                result: { url: "file:///x" },
            }),
        });

        await dispatchUpload({
            type: "upload",
            file,
            noteId: 1,
            token: "tok",
            inline: false,
            chunkSize: 1024,
            uploadId: "up1",
        });

        // Exactly 2 chunk uploads + 1 finish.
        const chunkCalls = fetch.mock.calls.filter(
            ([url, opts]) =>
                (opts?.method || "GET") === "POST" &&
                url === "/notes/1/chunked/sid/chunk",
        );
        const finishCalls = fetch.mock.calls.filter(
            ([url, opts]) =>
                (opts?.method || "GET") === "POST" &&
                url === "/notes/1/chunked/sid/finish",
        );
        expect(chunkCalls).toHaveLength(2);
        expect(finishCalls).toHaveLength(1);

        const complete = postedMessages().find((m) => m.type === "complete");
        expect(complete).toEqual({
            type: "complete",
            uploadId: "up1",
            filename: "a.bin",
            result: { url: "file:///x" },
        });

        // IndexedDB entry is cleaned up with the file's computed SHA-256
        // (a 64-char lowercase hex string).
        expect(ChunkStore.deleteChunkEntry).toHaveBeenCalledTimes(1);
        const [hash] = ChunkStore.deleteChunkEntry.mock.calls[0];
        expect(typeof hash).toBe("string");
        expect(hash).toHaveLength(64);
        expect(/^[0-9a-f]+$/.test(hash)).toBe(true);
    });

    test("start failure posts an error", async () => {
        mockRoutes({
            "POST /notes/1/chunked/start": {
                ok: false,
                status: 500,
                text: async () => "boom",
            },
        });

        await dispatchUpload({
            type: "upload",
            file,
            noteId: 1,
            token: "tok",
            inline: false,
            chunkSize: 1024,
            uploadId: "up2",
        });

        const error = postedMessages().find((m) => m.type === "error");
        expect(error).toMatchObject({
            type: "error",
            uploadId: "up2",
            filename: "a.bin",
            error: "boom",
        });
    });

    test("missing chunk data posts an error", async () => {
        ChunkStore.getChunkData.mockResolvedValue(null);
        mockRoutes({
            "POST /notes/1/chunked/start": respond({
                upload_id: "sid",
                chunks_done: [],
            }),
            "POST /notes/1/chunked/sid/chunk": respond({}),
            "POST /notes/1/chunked/sid/finish": respond({ url: "file:///x" }),
            "GET /notes/1/chunked/sid": respond({
                status: "done",
                result: { url: "file:///x" },
            }),
        });

        await dispatchUpload({
            type: "upload",
            file,
            noteId: 1,
            token: "tok",
            inline: false,
            chunkSize: 1024,
            uploadId: "up3",
        });

        const error = postedMessages().find((m) => m.type === "error");
        expect(error).toMatchObject({
            type: "error",
            uploadId: "up3",
            filename: "a.bin",
        });
        expect(error.error).toContain("missing from IndexedDB");
    });

    test("resume fetches pending sessions and completes", async () => {
        mockRoutes({
            "GET /notes/1/chunked/pending": respond([]),
            "POST /notes/1/chunked/start": respond({
                upload_id: "sid",
                chunks_done: [],
            }),
            "POST /notes/1/chunked/sid/chunk": respond({}),
            "POST /notes/1/chunked/sid/finish": respond({ url: "file:///x" }),
            "GET /notes/1/chunked/sid": respond({
                status: "done",
                result: { url: "file:///x" },
            }),
        });

        const entry = {
            filename: "a.bin",
            mimeType: "m",
            totalSize: 2000,
            chunkSize: 1024,
            totalChunks: 2,
            inline: false,
            noteId: 1,
            token: "tok",
            placeholderToken: "pt",
        };
        await dispatchUpload({
            type: "resume",
            fileHash: "abc",
            entry,
            uploadId: "r1",
        });

        // Resume must consult the pending-sessions endpoint.
        const pendingCalls = fetch.mock.calls.filter(
            ([url, opts]) =>
                (opts?.method || "GET") === "GET" &&
                url === "/notes/1/chunked/pending",
        );
        expect(pendingCalls.length).toBeGreaterThan(0);

        const complete = postedMessages().find((m) => m.type === "complete");
        expect(complete).toEqual({
            type: "complete",
            uploadId: "r1",
            filename: "a.bin",
            result: { url: "file:///x" },
        });
    });
});
