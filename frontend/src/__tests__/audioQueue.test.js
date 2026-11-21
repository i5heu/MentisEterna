import { indexedDB } from "fake-indexeddb";
import * as AudioQueue from "../audio/audioQueue.js";

// Heavy-dependency test: real IndexedDB CRUD through fake-indexeddb, global
// fetch for /ingest/token, and a mock XMLHttpRequest class for the upload
// path (uploadAudioRecording deliberately uses XHR rather than fetch so
// upload progress is observable).
global.indexedDB = indexedDB;

global.fetch = jest.fn();

// -- Mock XMLHttpRequest ---------------------------------------------------
//
// The upload endpoint call goes through XMLHttpRequest, so upload responses
// are programmed here instead of on the fetch mock. Every instance records
// open()/setRequestHeader()/send(); queued responders fire asynchronously
// (a microtask after send), like a real browser would. With no queued
// responder the request stays in flight forever (tests that expect a
// response must program one).
class MockXHR {
    static instances = [];
    static responders = [];

    constructor() {
        this.method = null;
        this.url = null;
        this.headers = {};
        this.responseType = "";
        this.response = null;
        this.status = 0;
        this.upload = {};
        this.body = null;
        MockXHR.instances.push(this);
    }

    open(method, url) {
        this.method = method;
        this.url = url;
    }

    setRequestHeader(name, value) {
        this.headers[name.toLowerCase()] = String(value);
    }

    send(body) {
        this.body = body;
        const respond = MockXHR.responders.shift();
        if (respond) {
            queueMicrotask(() => respond(this));
        }
    }

    // Test-side helpers: complete/fail the request the way the browser would.
    complete(status, response) {
        this.status = status;
        this.response = response;
        if (this.onload) this.onload();
    }

    failNetwork() {
        if (this.onerror) this.onerror();
    }
}

global.XMLHttpRequest = MockXHR;

/** Program the next upload to complete with `status` (response becomes xhr.response). */
function respondUploadWith(status, response) {
    MockXHR.responders.push((xhr) => xhr.complete(status, response));
}

/** Program the next upload to fail with a network error (onerror). */
function failUploadNetwork() {
    MockXHR.responders.push((xhr) => xhr.failNetwork());
}

/**
 * Program the next upload to stay in flight until the returned function is
 * called (it then completes with 200). Mirrors gating a fetch with a
 * deferred promise.
 */
function holdUpload() {
    let release;
    const gate = new Promise((resolve) => { release = resolve; });
    MockXHR.responders.push(async (xhr) => {
        await gate;
        xhr.complete(200, { note: { id: 42 }, file: {} });
    });
    return () => release();
}

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
        MockXHR.instances.length = 0;
        MockXHR.responders.length = 0;
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
        respondUploadWith(201, { note: { id: 42 }, file: {} });

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual(["entry-1"]);
        expect(result.failed).toEqual([]);
        expect(await AudioQueue.listPendingAudio()).toEqual([]);

        expect(fetch).not.toHaveBeenCalled();
        expect(MockXHR.instances).toHaveLength(1);
        const xhr = MockXHR.instances[0];
        expect(xhr.method).toBe("POST");
        expect(xhr.url).toBe("/ingest/audio/7/date");
        expect(xhr.headers.authorization).toBe("Bearer tok");
        expect(xhr.body).toBeInstanceOf(FormData);
        const file = xhr.body.get("file");
        expect(file.name).toBe("recording-20260814-120000.webm");
        expect(file.size).toBe("fake audio bytes".length);
        expect(file.type).toBe("audio/webm");
    });

    test("non-date entries upload to the plain parent URL", async () => {
        await AudioQueue.addPendingAudio(makeEntry({ dateFlag: false }));
        respondUploadWith(201, { note: { id: 42 }, file: {} });

        await AudioQueue.flushPendingAudio("tok");

        expect(MockXHR.instances[0].url).toBe("/ingest/audio/7");
    });

    test("failed upload keeps the entry and increments attempts", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        failUploadNetwork();

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual([]);
        expect(result.failed).toEqual(["entry-1"]);
        const [entry] = await AudioQueue.listPendingAudio();
        expect(entry.attempts).toBe(1);
    });

    test("5xx upload keeps the entry and increments attempts", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        respondUploadWith(500, {});

        const result = await AudioQueue.flushPendingAudio("tok");

        expect(result.uploaded).toEqual([]);
        expect(result.failed).toEqual(["entry-1"]);
        const [entry] = await AudioQueue.listPendingAudio();
        expect(entry.attempts).toBe(1);
    });

    test("concurrent flush calls dedupe to one upload per entry", async () => {
        await AudioQueue.addPendingAudio(makeEntry());
        const releaseUpload = holdUpload();

        const p1 = AudioQueue.flushPendingAudio("tok");
        const p2 = AudioQueue.flushPendingAudio("tok");
        expect(p1).toBe(p2);

        releaseUpload();
        const result = await p1;

        expect(result.uploaded).toEqual(["entry-1"]);
        expect(result.failed).toEqual([]);
        expect(MockXHR.instances).toHaveLength(1);
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
        expect(MockXHR.instances).toHaveLength(0);
    });

    test("uploadAudioRecording reports upload progress and resolves with the response", async () => {
        MockXHR.responders.push((xhr) => {
            xhr.upload.onprogress({ lengthComputable: true, loaded: 25, total: 100 });
            xhr.upload.onprogress({ lengthComputable: true, loaded: 100, total: 100 });
            xhr.complete(201, { note: { id: 42 }, file: {} });
        });
        const progress = [];

        const res = await AudioQueue.uploadAudioRecording(
            "tok",
            7,
            true,
            new Blob(["fake audio bytes"], { type: "audio/webm" }),
            "recording-20260814-120000.webm",
            (p) => progress.push(p),
        );

        expect(progress).toEqual([25, 100]);
        expect(res).toEqual({ note: { id: 42 }, file: {} });
    });

    test("getIngestToken surfaces the token and attaches status on failure", async () => {
        fetch.mockResolvedValueOnce(okJson({ token: "s3cret" }));
        expect(await AudioQueue.getIngestToken()).toBe("s3cret");

        fetch.mockResolvedValueOnce(errRes(401));
        const err = await AudioQueue.getIngestToken().catch((e) => e);
        expect(err.status).toBe(401);
    });

    describe("ingestTokenFromURL", () => {
        test("extracts the token from ?token=...", () => {
            expect(AudioQueue.ingestTokenFromURL("?token=abc123")).toBe("abc123");
        });

        test("coexists with other query params", () => {
            expect(AudioQueue.ingestTokenFromURL("?token=abc123&x=1")).toBe("abc123");
        });

        test("absent param yields empty string (session flow)", () => {
            expect(AudioQueue.ingestTokenFromURL("")).toBe("");
        });

        test("empty value yields empty string (session flow)", () => {
            expect(AudioQueue.ingestTokenFromURL("?token=")).toBe("");
        });

        test("unrelated param yields empty string", () => {
            expect(AudioQueue.ingestTokenFromURL("?parent=5")).toBe("");
        });

        test("URL-decodes the token value", () => {
            expect(AudioQueue.ingestTokenFromURL("?token=a%2Bb%20c")).toBe("a+b c");
        });
    });
});
