import { jest } from "@jest/globals";
import { ref, shallowReactive, nextTick } from "vue";

// Composable ↔ registry boundary test: mock only `registry.js` and exercise the
// real hydrate / re-normalize / emit guard behavior with a reactive props object.
const mockTypeDef = {
    id: "task",
    normalizeCustomData: jest.fn((cd) => (cd ? { ...cd } : {})),
    emptyCustomData: jest.fn(() => ({ cleared: true })),
};
jest.mock("../note-types/registry.js", () => ({
    getNoteTypeOrDefault: jest.fn(() => mockTypeDef),
}));
import { getNoteTypeOrDefault } from "../note-types/registry.js";
import { useNoteTypeDraft } from "../note-types/shared/useNoteTypeDraft.js";

beforeEach(() => {
    mockTypeDef.normalizeCustomData.mockClear();
    mockTypeDef.emptyCustomData.mockClear();
    getNoteTypeOrDefault.mockClear();
});

test("initializes draft from customData, resolving the type from the note", () => {
    const props = shallowReactive({
        note: { type: "task" },
        customData: { items: [{ a: 1 }] },
    });
    const d = useNoteTypeDraft(props);

    expect(getNoteTypeOrDefault).toHaveBeenCalledWith("task");
    expect(d.typeDef).toBe(mockTypeDef);
    expect(d.draft.value).toEqual({ items: [{ a: 1 }] });
});

test("typeId option wins over the note type", () => {
    const props = shallowReactive({
        note: { type: "task" },
        customData: { items: [] },
    });
    useNoteTypeDraft(props, { typeId: "overridden" });
    expect(getNoteTypeOrDefault).toHaveBeenCalledWith("overridden");
});

test("re-normalizes the draft when the customData prop changes", async () => {
    const props = shallowReactive({
        note: { type: "task" },
        customData: { items: [1] },
    });
    const d = useNoteTypeDraft(props);
    expect(d.draft.value).toEqual({ items: [1] });

    props.customData = { items: [2] };
    await nextTick();

    expect(mockTypeDef.normalizeCustomData).toHaveBeenCalledWith(
        { items: [2] },
        props.note,
    );
    expect(d.draft.value).toEqual({ items: [2] });
});

test("emits update when the local draft changes, via the onEmit callback", async () => {
    const onEmit = jest.fn();
    const props = shallowReactive({ note: { type: "task" }, customData: { a: 1 } });
    const d = useNoteTypeDraft(props, { onEmit });

    d.draft.value = { a: 2 };
    await nextTick();

    expect(onEmit).toHaveBeenCalledWith({ a: 2 });
});

test("resetToEmpty resets the draft to the type's empty shape", async () => {
    const props = shallowReactive({ note: { type: "task" }, customData: { a: 1 } });
    const d = useNoteTypeDraft(props);
    d.resetToEmpty();
    expect(mockTypeDef.emptyCustomData).toHaveBeenCalled();
    // flushed when re-emitted (onEmit absent here); draft value updated synchronously
    expect(d.draft.value).toEqual({ cleared: true });
});
