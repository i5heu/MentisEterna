import { jest } from "@jest/globals";
import { ref, shallowRef } from "vue";
import {
    createMonacoLinkTarget,
    createTextareaLinkTarget,
} from "../composables/useNoteLinkSearch.js";

// The link-search targets adapt the two editable surfaces (Monaco body
// editor, reply textareas) behind one interface for useNoteLinkSearch.
// These tests exercise the delegation/geometry glue without any DOM.
// (shallowRef: in the app the refs hold raw DOM elements / component
// instances, which Vue never wraps in reactive proxies.)

beforeEach(() => {
    global.requestAnimationFrame = (cb) => setTimeout(cb, 0);
    global.document = { activeElement: null };
});

afterEach(() => {
    delete global.requestAnimationFrame;
    delete global.document;
});

describe("createTextareaLinkTarget", () => {
    test("isAvailable tracks the element and delegates offset/focus", () => {
        const element = { selectionStart: 5 };
        const elementRef = shallowRef(null);
        const text = ref("hello world");
        const target = createTextareaLinkTarget({ elementRef, textRef: text });

        expect(target.isAvailable()).toBe(false);
        target.getOffset(); // must not throw while unavailable

        elementRef.value = element;
        expect(target.isAvailable()).toBe(true);
        expect(target.getOffset()).toBe(5);
        expect(target.ownsFocus()).toBe(false);

        global.document.activeElement = element;
        expect(target.ownsFocus()).toBe(true);
    });

    test("applyText writes the ref and restores caret + focus", async () => {
        const focused = [];
        const ranges = [];
        const element = {
            selectionStart: 0,
            focus: () => focused.push(true),
            setSelectionRange: (a, b) => ranges.push([a, b]),
        };
        const elementRef = shallowRef(element);
        const text = ref("old");
        const target = createTextareaLinkTarget({ elementRef, textRef: text });

        target.applyText("new text", 4);
        expect(text.value).toBe("new text");

        await new Promise((resolve) => setTimeout(resolve, 1));
        expect(focused).toEqual([true]);
        expect(ranges).toEqual([[4, 4]]);
    });
});

describe("createMonacoLinkTarget", () => {
    function fakeComponent(overrides = {}) {
        return {
            isReady: () => true,
            getOffset: () => 7,
            ownsFocus: () => true,
            applyText: jest.fn(),
            getCaretViewportRect: () => ({ left: 110, top: 230 }),
            lineHeightPx: () => 23,
            ...overrides,
        };
    }

    test("unavailable until the editor component reports ready", () => {
        const componentRef = shallowRef(null);
        const text = ref("body");
        const wrapperRef = ref(null);
        const target = createMonacoLinkTarget({
            componentRef,
            wrapperRef,
            textRef: text,
        });

        expect(target.isAvailable()).toBe(false);
        expect(target.getOffset()).toBe(0);
        expect(target.ownsFocus()).toBe(false);

        componentRef.value = { isReady: () => false };
        expect(target.isAvailable()).toBe(false);

        componentRef.value = fakeComponent();
        expect(target.isAvailable()).toBe(true);
        expect(target.getOffset()).toBe(7);
        expect(target.ownsFocus()).toBe(true);
        expect(target.text.value).toBe("body");
    });

    test("geometry is translated from viewport to wrapper space", () => {
        const componentRef = shallowRef(fakeComponent());
        const wrapperRef = ref({
            clientWidth: 500,
            getBoundingClientRect: () => ({
                left: 40,
                top: 1000,
            }),
        });
        const target = createMonacoLinkTarget({
            componentRef: componentRef,
            wrapperRef,
            textRef: ref(""),
        });

        // caret viewport rect {110, 230} - wrapper origin {40, 1000}
        expect(target.getCaretRect()).toEqual({ left: 70, top: -770 });
        expect(target.getClientWidth()).toBe(500);
        expect(target.getViewportTop()).toBe(1000);
        expect(target.getLineHeight()).toBe(23);
    });

    test("falls back to viewport coords without a wrapper", () => {
        const componentRef = shallowRef(fakeComponent());
        const target = createMonacoLinkTarget({
            componentRef,
            wrapperRef: ref(null),
            textRef: ref(""),
        });
        expect(target.getCaretRect()).toEqual({ left: 110, top: 230 });
        expect(target.getClientWidth()).toBe(0);
        expect(target.getViewportTop()).toBe(0);
    });

    test("applyText delegates to the editor component", () => {
        const applyText = jest.fn();
        const componentRef = shallowRef(fakeComponent({ applyText }));
        const target = createMonacoLinkTarget({
            componentRef,
            wrapperRef: ref(null),
            textRef: ref("x"),
        });
        target.applyText("replaced", 9);
        expect(applyText).toHaveBeenCalledWith("replaced", 9);
    });
});
