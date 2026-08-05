import { indexedDB } from "fake-indexeddb";
import * as ChunkStore from "../workers/chunkStore.js";

// Heavy-dependency test: real IndexedDB CRUD through fake-indexeddb.
global.indexedDB = indexedDB;

const meta = {
    filename: "a",
    mimeType: "m",
    totalSize: 10,
    chunkSize: 5,
    totalChunks: 2,
    inline: false,
    noteId: 1,
    token: "t",
};

describe("chunkStore", () => {
    // Clear all stored entries between tests so state doesn't leak.
    beforeEach(async () => {
        const entries = await ChunkStore.listEntries();
        for (const e of entries) {
            await ChunkStore.deleteChunkEntry(e.fileHash);
        }
    });

    test("putChunk then getChunkEntry/getChunkData round-trips", async () => {
        const data = new Uint8Array([1, 2, 3]);
        await ChunkStore.putChunk("h", meta, 0, data);

        const entry = await ChunkStore.getChunkEntry("h");
        expect(entry.fileHash).toBe("h");
        expect(entry.chunkIndexes).toEqual([0]);
        expect(entry.filename).toBe("a");
        expect(entry.mimeType).toBe("m");
        expect(entry.totalSize).toBe(10);
        expect(entry.chunkSize).toBe(5);
        expect(entry.totalChunks).toBe(2);
        expect(entry.inline).toBe(false);
        expect(entry.noteId).toBe(1);
        expect(entry.token).toBe("t");

        const stored = await ChunkStore.getChunkData("h", 0);
        expect(stored).toEqual(data);
    });

    test("putting the same index twice does not duplicate chunkIndexes and fills placeholderToken", async () => {
        await ChunkStore.putChunk("h2", meta, 0, new Uint8Array([1]));
        // Second put of the same index (no placeholder) — no duplicate index.
        await ChunkStore.putChunk("h2", meta, 0, new Uint8Array([2]));

        let entry = await ChunkStore.getChunkEntry("h2");
        expect(entry.chunkIndexes).toEqual([0]);

        // A later put that carries a placeholderToken fills the empty field.
        const withToken = { ...meta, placeholderToken: "pt" };
        await ChunkStore.putChunk("h2", withToken, 0, new Uint8Array([3]));

        entry = await ChunkStore.getChunkEntry("h2");
        expect(entry.placeholderToken).toBe("pt");
        expect(entry.chunkIndexes).toEqual([0]);
    });

    test("deleteChunkEntry removes the entry and its chunk data", async () => {
        await ChunkStore.putChunk("h3", meta, 0, new Uint8Array([1]));
        await ChunkStore.putChunk("h3", meta, 1, new Uint8Array([2]));

        await ChunkStore.deleteChunkEntry("h3");

        expect(await ChunkStore.getChunkEntry("h3")).toBeNull();
        expect(await ChunkStore.getChunkData("h3", 0)).toBeNull();
        expect(await ChunkStore.getChunkData("h3", 1)).toBeNull();
    });

    test("listEntries returns fileHash/filename/noteId for stored entries and deleted indexes are null", async () => {
        await ChunkStore.putChunk(
            "ha",
            { ...meta, filename: "one.txt", noteId: 10 },
            0,
            new Uint8Array([1]),
        );
        await ChunkStore.putChunk(
            "hb",
            { ...meta, filename: "two.bin", noteId: 20 },
            0,
            new Uint8Array([2]),
        );

        const entries = await ChunkStore.listEntries();
        expect(entries).toContainEqual({
            fileHash: "ha",
            filename: "one.txt",
            noteId: 10,
        });
        expect(entries).toContainEqual({
            fileHash: "hb",
            filename: "two.bin",
            noteId: 20,
        });

        // Indexes that were never stored resolve to null.
        expect(await ChunkStore.getChunkData("ha", 5)).toBeNull();
        expect(await ChunkStore.getChunkData("hb", 9)).toBeNull();
    });
});
