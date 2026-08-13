import { indexedDB } from "fake-indexeddb";
import * as AudioQueue from "../audio/audioQueue.js";

// Heavy-dependency test: real IndexedDB CRUD through fake-indexeddb plus
// global fetch for the upload path.
global.indexedDB = indexedDB;

global.fetch = jest.fn();

function okJson(body) {
    return { ok: true, status: 200, json: async () => body };
}

function errRes(status) {
    return { ok: false, status, json: async () => ({}) };
}

function makeEntry(overrides = {}) {
    return {
        id: overrides.id || "entry-1",
        parentId: 7,
        dateFlag: true,
        blob: new Blob(["fake audio bytes"], { type: "audio/webm" }),
        mimeType: "audio/webm",
        filename: "recording-20260814-120000.webm",
        createdAt: 1000,
        attempts: 0,
        ...overrides,
    };
}

describe("audioQueue", () => {
    beforeEach(async () => {
        const entries = await AudioQueue.listPendingAudio();
        for (const e of entries) {
            await AudioQueue.removePendingAudio(e.id);
        }
        fetch.mockReset();
    });

    test("addPendingAudio/listPendingAudio round-trips blob and fields, sorted by createdAt", async () => {
        const later = makeEntry({ id: "entry-2", createdAt: 2000, dateFlag: false, parentId: 3 });
        const earlier = makeEntry({ createdAt: 500 });

        await AudioQueue.addPendingAudio(later);
        await AudioQueue.addPendingAudio(earlier);

        const list = await AudioQueue.listPendingAudio();
        expect(list.map((e) => e.id)).toEqual(["entry-1", "entry-2"]);
        expect(list[0].parentId).toBe(7);
        expect(list[0].dateFlag).toBe(true);
        expect(list[0].filename).toBe("recording-20260814-120000.webm");
        expect(list[0].createdAt).toBe(500);
        expect(list[0].attempts).toBe(0);
        expect(list[0].blob.size).toBe("fake audio bytes".length);
        expect(list[0].blob.type).toBe("audio/webm");
    });

    test("removePendingAudio deletes an entry", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        await AudioQueue.removePendingAudio("entry-1");
        expect(await AudioQueue.listPendingAudio()).toEqual([]);
    });

    test("flushPendingAudio uploads each entry and removes it on 2xx", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        fetch.mockResolvedValueOnce(okJson({ note: { id: 42 }, file: {} }));

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual(["entry-1"]);
        expect(result.failed).toEqual([]);
        expect(await AudioQueue.listPendingAudio()).toEqual([]);

        expect(fetch).toHaveBeenCalledTimes(1);
        const [url, opts] = fetch.mock.calls[0];
        expect(url).toBe("/ingest/audio/7/date");
        expect(opts.method).toBe("POST");
        expect(opts.credentials).toBe("include");
        expect(opts.headers.Authorization).toBe("Bearer tok");
        const file = opts.body.get("file");
        expect(file.name).toBe("recording-20260814-120000.webm");
        expect(file.size).toBe("fake audio bytes".length);
        expect(file.type).toBe("audio/webm");
    });

    test("non-date entries upload to the plain parent URL", async () => {
        await AudioQueue.addPendingAudio(makeEntry({ dateFlag: false }));
        fetch.mockResolvedValueOnce(okJson({ note: { id: 42 }, file: {} }));

        await AudioQueue.flushPendingAudio("tok");

        expect(fetch.mock.calls[0][0]).toBe("/ingest/audio/7");
    });

    test("failed upload keeps the entry and increments attempts", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        fetch.mockRejectedValueOnce(new Error("offline"));

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual([]);
        expect(result.failed).toEqual(["entry-1"]);
        const [entry] = await AudioQueue.listPendingAudio();
        expect(entry.attempts).toBe(1);
    });

    test("5xx upload keeps the entry and increments attempts", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        fetch.mockResolvedValueOnce(errRes(500));

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual([]);
        expect(result.failed).toEqual(["entry-1"]);
        const [entry] = await AudioQueue.listPendingAudio();
        expect(entry.attempts).toBe(1);
    });

    test("concurrent flush calls dedupe to one fetch per entry", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        let resolveFetch;
        fetch.mockReturnValueOnce(
            new Promise((resolve) => {
                resolveFetch = resolve;
            }),
        );

        const p1 = AudioQueue.flushPendingAudio("tok");
        const p2 = AudioQueue.flushPendingAudio("tok");
        expect(p1).toBe(p2);

        resolveFetch(okJson({ note: { id: 42 }, file: {} }));
        const result = await p1;

        expect(result.uploaded).toEqual(["entry-1"]);
        expect(fetch).toHaveBeenCalledTimes(1);
    });

    test("flush with failing token fetch returns failed ids without throwing", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        fetch.mockResolvedValueOnce(errRes(503));

        const result = await AudioQueue.flushPendingAudio();

        expect(result).toEqual({ uploaded: [], failed: ["entry-1"] });
        expect(await AudioQueue.listPendingAudio()).toHaveLength(1);
        // Only the token request happened; no upload attempt.
        expect(fetch).toHaveBeenCalledTimes(1);
        expect(fetch.mock.calls[0][0]).toBe("/ingest/token");
    });

    test("getIngestToken surfaces the token and attaches status on failure", async () => {
        fetch.mockResolvedValueOnce(okJson({ token: "s3cret" }));
        expect(await AudioQueue.getIngestToken()).toBe("s3cret");

        fetch.mockResolvedValueOnce(errRes(401));
        const err = await AudioQueue.getIngestToken().catch((e) => e);
        expect(err.status).toBe(401);
    });
});
