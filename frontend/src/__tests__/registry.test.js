import {
    getNoteType,
    getNoteTypeOrDefault,
    getTypeOptions,
    getDefaultChildType,
    isLazyChildren,
    getManifest,
    fetchAndMergeManifests,
} from "../note-types/registry.js";
import { fetchNoteTypes } from "../api.js";

jest.mock("vue", () => ({
    // defineAsyncComponent just wraps its loader; identity is enough for tests.
    defineAsyncComponent: (loader) => loader,
}));

jest.mock("../api.js", () => ({
    fetchNoteTypes: jest.fn(),
}));

describe("note-types registry", () => {
    beforeEach(() => {
        fetchNoteTypes.mockReset();
    });

    test("getNoteType finds a known type and null for unknown", () => {
        const recipe = getNoteType("recipe");
        expect(recipe).not.toBeNull();
        expect(recipe.id).toBe("recipe");
        expect(recipe.label).toBe("Recipe");

        expect(getNoteType("does_not_exist")).toBeNull();
    });

    test("getNoteTypeOrDefault falls back to the first (standard) entry", () => {
        expect(getNoteTypeOrDefault("does_not_exist").id).toBe("standard");
        expect(getNoteTypeOrDefault("task").id).toBe("task");
    });

    test("getTypeOptions returns value+label pairs for every type", () => {
        const options = getTypeOptions();
        expect(options.length).toBeGreaterThan(0);
        for (const opt of options) {
            expect(typeof opt.value).toBe("string");
            expect(typeof opt.label).toBe("string");
        }
        expect(options).toContainEqual({ value: "task", label: "Task" });
    });

    test("getDefaultChildType uses configured default or 'standard'", () => {
        expect(getDefaultChildType("recipe_overview")).toBe("recipe");
        expect(getDefaultChildType("recipe")).toBe("standard");
        expect(getDefaultChildType("missing")).toBe("standard");
    });

    test("isLazyChildren is true only for configured overview types", () => {
        expect(isLazyChildren("recipe_overview")).toBe(true);
        expect(isLazyChildren("recipe")).toBe(false);
        expect(isLazyChildren("missing")).toBe(false);
    });

    test("getManifest returns null before any manifests are fetched", () => {
        expect(getManifest("task")).toBeNull();
    });

    test("fetchAndMergeManifests caches manifests and updates labels", async () => {
        fetchNoteTypes.mockResolvedValue([
            { id: "task", label: "Renamed Task" },
        ]);

        await fetchAndMergeManifests("token");
        expect(fetchNoteTypes).toHaveBeenCalledWith("token");

        expect(getManifest("task")).toEqual({ id: "task", label: "Renamed Task" });
        expect(getNoteType("task").label).toBe("Renamed Task");
    });

    test("fetchAndMergeManifests tolerates fetch failure", async () => {
        fetchNoteTypes.mockRejectedValue(new Error("network down"));
        await expect(fetchAndMergeManifests("token")).resolves.toBeUndefined();
        expect(getNoteType("task")).not.toBeNull();
    });
});
