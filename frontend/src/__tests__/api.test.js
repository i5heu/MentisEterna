import * as api from "../api.js";

// Heavy-dependency test: global fetch + window (dispatchEvent, location).
// api.js has no import-time side effects and references window/fetch only
// inside called functions, so importing it is safe once the globals exist.

global.fetch = jest.fn();

beforeAll(() => {
    global.window = {
        dispatchEvent: jest.fn(),
        location: { protocol: "http:", host: "localhost:8080" },
    };
    if (typeof global.CustomEvent === "undefined") {
        global.CustomEvent = class {
            constructor(type) {
                this.type = type;
            }
        };
    }
});

beforeEach(() => {
    fetch.mockReset();
    global.window.dispatchEvent.mockClear();
});

function okJson(body) {
    return { ok: true, status: 200, json: async () => body };
}

describe("api", () => {
    test("login posts credentials to /login", async () => {
        fetch.mockResolvedValue(okJson({ ok: true }));

        await api.login("u", "p");

        expect(fetch).toHaveBeenCalledWith(
            "/login",
            {
                method: "POST",
                credentials: "include",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ username: "u", password: "p" }),
            },
        );
    });

    test("fetchNotes requests /notes with JSON content type", async () => {
        fetch.mockResolvedValue(okJson([{ id: 1 }]));

        const notes = await api.fetchNotes("tok");

        expect(notes).toEqual([{ id: 1 }]);
        expect(fetch.mock.calls[0][0]).toBe("/notes");
        expect(fetch.mock.calls[0][1].headers).toEqual({
            "Content-Type": "application/json",
        });
    });

    test("401 dispatches auth:unauthorized and rejects", async () => {
        fetch.mockResolvedValue({ status: 401 });

        await expect(api.fetchNote("t", 1)).rejects.toThrow("unauthorized");
        expect(global.window.dispatchEvent).toHaveBeenCalled();
        expect(global.window.dispatchEvent.mock.calls[0][0].type).toBe(
            "auth:unauthorized",
        );
    });

    test("non-OK with body rejects with the body text", async () => {
        fetch.mockResolvedValue({
            ok: false,
            status: 500,
            text: async () => "boom",
        });

        await expect(api.fetchNote("t", 1)).rejects.toThrow("boom");
    });

    test("non-OK with empty body rejects with HTTP status", async () => {
        fetch.mockResolvedValue({
            ok: false,
            status: 500,
            text: async () => "",
        });

        await expect(api.fetchNote("t", 1)).rejects.toThrow("HTTP 500");
    });

    test("204 resolves null for deleteNote", async () => {
        fetch.mockResolvedValue({
            ok: true,
            status: 204,
            json: async () => ({}),
            text: async () => "",
        });

        await expect(api.deleteNote("t", 7)).resolves.toBeNull();
        expect(fetch.mock.calls[0][0]).toBe("/notes/7");
        expect(fetch.mock.calls[0][1].method).toBe("DELETE");
    });

    test("createNote sends default payload", async () => {
        fetch.mockResolvedValue(okJson({ id: 1 }));

        await api.createNote("t", "Title", "Body");

        expect(fetch.mock.calls[0][0]).toBe("/notes");
        const body = JSON.parse(fetch.mock.calls[0][1].body);
        expect(body).toEqual({
            title: "Title",
            body: "Body",
            parent_id: null,
            type: "standard",
            custom_data: null,
            tags: [],
        });
    });

    test("updateNote PUTs the full payload", async () => {
        fetch.mockResolvedValue(okJson({ id: 3 }));

        await api.updateNote("t", 3, "T", "B", null, "task", { x: 1 }, ["a"]);

        expect(fetch.mock.calls[0][0]).toBe("/notes/3");
        expect(fetch.mock.calls[0][1].method).toBe("PUT");
        expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
            title: "T",
            body: "B",
            parent_id: null,
            type: "task",
            custom_data: { x: 1 },
            tags: ["a"],
        });
    });

    test("searchNotes builds deduped, filtered query params", async () => {
        fetch.mockResolvedValue(okJson([]));

        await api.searchNotes("t", "hello", {
            types: ["recipe", "recipe", ""],
            stream: true,
            tagOnly: true,
        });

        const url = fetch.mock.calls[0][0];
        expect(url).toContain("q=hello");
        expect(url).toContain("types=recipe");
        expect(url).toContain("stream=1");
        expect(url).toContain("tag_only=1");
    });

    test("pluginActionV2 POSTs params to the action endpoint", async () => {
        fetch.mockResolvedValue(okJson({}));

        await api.pluginActionV2("t", 5, "myact", { a: 1 });

        expect(fetch.mock.calls[0][0]).toBe("/notes/5/actions/myact");
        expect(fetch.mock.calls[0][1].method).toBe("POST");
        expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ params: { a: 1 } });
    });

    test("fetchNoteTypes requests /note-types", async () => {
        fetch.mockResolvedValue(okJson([]));

        await api.fetchNoteTypes("t");

        expect(fetch.mock.calls[0][0]).toBe("/note-types");
    });

    test("startChunkedUpload builds the expected query string", async () => {
        fetch.mockResolvedValue(okJson({ upload_id: "u" }));

        await api.startChunkedUpload(
            "t", 9, true, "f.bin", "text/plain", 100, 50, 2, "abc",
        );

        expect(fetch.mock.calls[0][0]).toBe(
            "/notes/9/chunked/start?inline=1&filename=f.bin&mime_type=text%2Fplain&total_size=100&chunk_size=50&total_chunks=2&sha256=abc",
        );
    });
});
