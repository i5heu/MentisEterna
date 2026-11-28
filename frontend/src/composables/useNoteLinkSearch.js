import {
    computed,
    nextTick,
    onMounted,
    onUnmounted,
    ref,
    watch,
} from "vue";
import {
    abortSearchRequest,
    buildSectionGroups,
    flattenVisibleSectionGroups,
    runStreamedSearch,
    setCollapsedSection,
    toggleCollapsedSection,
} from "../views/notesViewSearch.js";

const LINK_HINT_DIGITS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "0"];

/**
 * A link-search "target" abstracts the editable surface the `[[` popup is
 * attached to. Implementations: plain textareas (reply/threadReply composes)
 * and the Monaco note body editor. All coordinates are relative to the
 * element that absolutely positions the popup (the textarea origin for
 * textareas, the wrapper element for Monaco).
 *
 * Shape:
 * {
 *   isAvailable(): boolean,
 *   text: Ref<string>,                    // current content
 *   onChange?: () => void,                // dirty marking on input
 *   getOffset(): number,                  // caret offset in the content
 *   ownsFocus(): boolean,
 *   applyText(newText, selectOffset): void,
 *   getCaretRect(): { left, top },        // caret coords, popup space
 *   getLineHeight(): number,              // px
 *   getClientWidth(): number,             // px, for popup clamping
 *   getViewportTop(): number,             // viewport top, for flip clamping
 * }
 */

/**
 * Measures the caret position inside a textarea by mirroring its styles in
 * an off-DOM div. Returns coordinates relative to the textarea's origin,
 * adjusted for its current scroll.
 */
function getTextareaCaretPosition(textarea, position) {
    const div = document.createElement("div");
    const span = document.createElement("span");
    const style = window.getComputedStyle(textarea);
    const props = [
        "boxSizing",
        "width",
        "height",
        "overflowX",
        "overflowY",
        "borderTopWidth",
        "borderRightWidth",
        "borderBottomWidth",
        "borderLeftWidth",
        "paddingTop",
        "paddingRight",
        "paddingBottom",
        "paddingLeft",
        "fontStyle",
        "fontVariant",
        "fontWeight",
        "fontStretch",
        "fontSize",
        "fontSizeAdjust",
        "lineHeight",
        "fontFamily",
        "textAlign",
        "textTransform",
        "textIndent",
        "textDecoration",
        "letterSpacing",
        "wordSpacing",
        "tabSize",
        "MozTabSize",
    ];

    div.style.position = "absolute";
    div.style.visibility = "hidden";
    div.style.whiteSpace = "pre-wrap";
    div.style.wordWrap = "break-word";
    div.style.top = "0";
    div.style.left = "0";

    for (const prop of props) {
        div.style[prop] = style[prop];
    }

    div.textContent = textarea.value.slice(0, position);
    span.textContent = textarea.value.slice(position) || ".";
    div.appendChild(span);
    document.body.appendChild(div);

    const left = span.offsetLeft - textarea.scrollLeft;
    const top = span.offsetTop - textarea.scrollTop;

    document.body.removeChild(div);
    return { left, top };
}

export function createTextareaLinkTarget({ elementRef, textRef, onChange }) {
    function element() {
        return elementRef.value;
    }

    return {
        isAvailable: () => Boolean(element()),
        text: textRef,
        onChange,
        getOffset: () => element()?.selectionStart ?? 0,
        ownsFocus: () =>
            Boolean(element()) && document.activeElement === element(),
        applyText(newText, selectOffset) {
            textRef.value = newText;
            requestAnimationFrame(() => {
                const el = element();
                if (!el) return;
                el.focus();
                el.setSelectionRange(selectOffset, selectOffset);
            });
        },
        getCaretRect() {
            const el = element();
            if (!el) return { left: 0, top: 0 };
            const { left, top } = getTextareaCaretPosition(
                el,
                el.selectionStart ?? 0,
            );
            return { left, top };
        },
        getLineHeight() {
            const el = element();
            if (!el) return 18;
            const style = window.getComputedStyle(el);
            return parseFloat(style.lineHeight) || parseFloat(style.fontSize) || 18;
        },
        getClientWidth: () => element()?.clientWidth ?? 0,
        getViewportTop: () => element()?.getBoundingClientRect().top ?? 0,
    };
}

/**
 * Monaco-backed target. `componentRef` points at the exposed API of a
 * MonacoEditor.vue instance (may be null before mount / while unloaded);
 * `wrapperRef` is the element that positions the link popup.
 */
export function createMonacoLinkTarget({
    componentRef,
    wrapperRef,
    textRef,
    onChange,
}) {
    function component() {
        const c = componentRef?.value;
        return c && typeof c.isReady === "function" && c.isReady() ? c : null;
    }

    function wrapper() {
        return wrapperRef?.value ?? null;
    }

    return {
        isAvailable: () => Boolean(component()),
        text: textRef,
        onChange,
        getOffset: () => component()?.getOffset() ?? 0,
        ownsFocus: () => component()?.ownsFocus() ?? false,
        applyText: (newText, selectOffset) =>
            component()?.applyText(newText, selectOffset),
        getCaretRect() {
            const c = component();
            if (!c) return { left: 0, top: 0 };
            const rect = c.getCaretViewportRect();
            const w = wrapper();
            if (!w) return { left: rect.left, top: rect.top };
            const wrapperRect = w.getBoundingClientRect();
            return {
                left: rect.left - wrapperRect.left,
                top: rect.top - wrapperRect.top,
            };
        },
        getLineHeight: () => component()?.lineHeightPx() ?? 23,
        getClientWidth: () => wrapper()?.clientWidth ?? 0,
        getViewportTop: () => wrapper()?.getBoundingClientRect().top ?? 0,
    };
}

export function useNoteLinkSearch({
    token,
    streamSearchNotes,
    targets,
    mainChatFeedEl,
    threadChatFeedEl,
    hideHintOverlay,
    hintRefreshSources = [],
}) {
    const linkSearchQuery = ref("");
    const linkSearchSections = ref([]);
    const linkSearchCollapsedSections = ref({});
    const linkSearchStatusMessage = ref("");
    const linkSearching = ref(false);
    const linkKeyboardMode = ref(false);
    const linkSearchIndex = ref(-1);
    const linkSearchVisible = ref(false);
    const linkSearchTarget = ref(null);
    const linkSearchTriggerType = ref(null);
    const linkSearchTriggerStart = ref(-1);
    const linkPopupStyle = ref({ left: "20px", top: "20px" });
    const linkHintMode = ref(false);
    const linkHintTyped = ref("");
    const linkHintEntries = ref([]);
    const linkSearchRequest = { controller: null };

    let linkSearchTimeout = null;
    let linkHintRefreshFrame = 0;

    function getLinkSearchContext(target = linkSearchTarget.value) {
        return targets?.[target] || null;
    }

    function normalizeLinkHintKey(key) {
        if (!key) return "";
        return key.length === 1 ? key.toUpperCase() : key;
    }

    function linkHintAlphaPrefix(index) {
        let value = Number(index) + 1;
        let label = "";
        while (value > 0) {
            const remainder = (value - 1) % 26;
            label = String.fromCharCode(65 + remainder) + label;
            value = Math.floor((value - 1) / 26);
        }
        return label;
    }

    function linkHintLabelForIndex(index) {
        if (index < LINK_HINT_DIGITS.length) {
            return LINK_HINT_DIGITS[index];
        }
        const offset = index - LINK_HINT_DIGITS.length;
        const prefixIndex = Math.floor(offset / LINK_HINT_DIGITS.length);
        const digit = LINK_HINT_DIGITS[offset % LINK_HINT_DIGITS.length];
        return `${linkHintAlphaPrefix(prefixIndex)}${digit}`;
    }

    function isVisibleLinkHintElement(anchor) {
        if (!(anchor instanceof HTMLElement)) return false;
        const rect = anchor.getBoundingClientRect();
        if (rect.width <= 0 || rect.height <= 0) return false;
        const style = window.getComputedStyle(anchor);
        if (
            style.display === "none" ||
            style.visibility === "hidden" ||
            style.pointerEvents === "none"
        ) {
            return false;
        }
        return !(
            rect.bottom < 0 ||
            rect.right < 0 ||
            rect.top > window.innerHeight ||
            rect.left > window.innerWidth
        );
    }

    function collectLinkHintEntries() {
        const roots = [mainChatFeedEl.value, threadChatFeedEl.value].filter(Boolean);
        const entries = [];

        for (const root of roots) {
            const anchors = root.querySelectorAll(".markdown-body a[href]");
            for (const anchor of anchors) {
                if (!isVisibleLinkHintElement(anchor)) continue;
                const rect = anchor.getBoundingClientRect();
                const top = rect.top <= 28 ? rect.bottom + 6 : rect.top;
                entries.push({
                    id: `link-hint-${entries.length}`,
                    label: linkHintLabelForIndex(entries.length),
                    top: Math.max(8, top),
                    left: Math.max(8, Math.min(rect.left, window.innerWidth - 16)),
                    element: anchor,
                    href: anchor.getAttribute("href") || "",
                });
            }
        }

        return entries;
    }

    function cancelLinkHintRefresh() {
        if (!linkHintRefreshFrame) return;
        window.cancelAnimationFrame(linkHintRefreshFrame);
        linkHintRefreshFrame = 0;
    }

    function closeLinkHintMode() {
        linkHintMode.value = false;
        linkHintTyped.value = "";
        linkHintEntries.value = [];
        cancelLinkHintRefresh();
    }

    function refreshLinkHintEntries() {
        if (!linkHintMode.value) return;
        const entries = collectLinkHintEntries();
        linkHintEntries.value = entries;
        if (entries.length === 0) {
            closeLinkHintMode();
            return;
        }
        if (
            linkHintTyped.value &&
            !entries.some((entry) => entry.label.startsWith(linkHintTyped.value))
        ) {
            linkHintTyped.value = "";
        }
    }

    function scheduleLinkHintRefresh() {
        if (!linkHintMode.value) return;
        cancelLinkHintRefresh();
        linkHintRefreshFrame = window.requestAnimationFrame(() => {
            linkHintRefreshFrame = 0;
            refreshLinkHintEntries();
        });
    }

    async function openLinkHintMode() {
        hideHintOverlay?.();
        closeLinkSearch();
        linkHintTyped.value = "";
        linkHintMode.value = true;
        await nextTick();
        refreshLinkHintEntries();
    }

    function toggleLinkHintMode() {
        if (linkHintMode.value) {
            closeLinkHintMode();
            return;
        }
        openLinkHintMode();
    }

    function prefixLinkHintMatches(prefix) {
        if (!prefix) return linkHintEntries.value;
        return linkHintEntries.value.filter((entry) =>
            entry.label.startsWith(prefix),
        );
    }

    function openLinkHintEntry(entry) {
        const target = entry?.element;
        closeLinkHintMode();
        if (!target || !document.contains(target)) return;
        target.focus?.({ preventScroll: true });
        target.click();
    }

    function applyLinkHintInput(nextLabel) {
        const exact = linkHintEntries.value.find((entry) => entry.label === nextLabel);
        if (exact) {
            openLinkHintEntry(exact);
            return true;
        }
        const prefixMatches = prefixLinkHintMatches(nextLabel);
        if (prefixMatches.length > 0) {
            linkHintTyped.value = nextLabel;
            return true;
        }
        return false;
    }

    function onLinkHintKeyDown(event) {
        if (!linkHintMode.value || event.defaultPrevented) return;

        const normalizedKey = normalizeLinkHintKey(event.key);
        if (
            normalizedKey === "L" &&
            (event.ctrlKey || event.metaKey) &&
            !event.altKey
        ) {
            event.preventDefault();
            event.stopPropagation();
            closeLinkHintMode();
            return;
        }

        if (["Shift", "Control", "Alt", "Meta"].includes(event.key)) {
            return;
        }

        if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            closeLinkHintMode();
            return;
        }

        if (event.key === "Backspace") {
            event.preventDefault();
            event.stopPropagation();
            linkHintTyped.value = linkHintTyped.value.slice(0, -1);
            return;
        }

        if (event.key === "Enter") {
            const matches = prefixLinkHintMatches(linkHintTyped.value);
            if (matches.length === 1) {
                event.preventDefault();
                event.stopPropagation();
                openLinkHintEntry(matches[0]);
            }
            return;
        }

        if (!/^[A-Z0-9]$/.test(normalizedKey)) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();

        if (applyLinkHintInput(`${linkHintTyped.value}${normalizedKey}`)) {
            return;
        }
        if (applyLinkHintInput(normalizedKey)) {
            return;
        }

        linkHintTyped.value = "";
    }

    function onLinkHintWindowBlur() {
        closeLinkHintMode();
    }

    const linkSearchSectionGroups = computed(() =>
        buildSectionGroups(linkSearchSections.value, {
            collapsedMap: linkSearchCollapsedSections.value,
        }),
    );
    const linkSearchResults = computed(() =>
        flattenVisibleSectionGroups(linkSearchSectionGroups.value),
    );
    const linkSelectableEntries = computed(() => {
        const entries = [];
        for (const section of linkSearchSectionGroups.value) {
            if (section.collapsed) {
                entries.push({
                    kind: "section",
                    key: section.key,
                    collapsed: true,
                    section,
                });
                continue;
            }
            for (const item of section.items) {
                entries.push({
                    kind: "result",
                    key: item.key,
                    item,
                    section,
                });
            }
        }
        return entries;
    });

    function toggleLinkSearchSection(key) {
        toggleCollapsedSection(linkSearchCollapsedSections, key);
        linkSearchIndex.value = -1;
    }

    function collapseActiveLinkSection() {
        if (!linkKeyboardMode.value || linkSearchIndex.value < 0) return false;
        const entry = linkSelectableEntries.value[linkSearchIndex.value];
        const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
        if (!key || linkSearchCollapsedSections.value?.[key]) return false;
        setCollapsedSection(linkSearchCollapsedSections, key, true);
        linkSearchIndex.value = linkSelectableEntries.value.findIndex(
            (candidate) => candidate.kind === "section" && candidate.key === key,
        );
        return linkSearchIndex.value >= 0;
    }

    function expandActiveLinkSection() {
        if (!linkKeyboardMode.value || linkSearchIndex.value < 0) return false;
        const entry = linkSelectableEntries.value[linkSearchIndex.value];
        const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
        if (!key || !linkSearchCollapsedSections.value?.[key]) return false;
        setCollapsedSection(linkSearchCollapsedSections, key, false);
        linkSearchIndex.value = linkSelectableEntries.value.findIndex(
            (candidate) => candidate.kind === "result" && candidate.section?.key === key,
        );
        return linkSearchIndex.value >= 0;
    }

    function onLinkEditorInput(target) {
        const context = getLinkSearchContext(target);
        if (!context) return;
        linkSearchTarget.value = target;
        context.onChange?.();
        updateLinkSearchFromCursor();
    }

    function onLinkEditorCaretMove(target, event) {
        if (
            event?.type === "keyup" &&
            ["ArrowUp", "ArrowDown", "Enter", "Escape", "Tab"].includes(event.key)
        ) {
            return;
        }
        linkSearchTarget.value = target;
        updateLinkSearchFromCursor();
    }

    function onLinkEditorScroll(target) {
        if (!linkSearchVisible.value || linkSearchTarget.value !== target) return;
        updateLinkPopupPosition();
    }

    function onLinkEditorKeydown(target, event) {
        if (
            !linkSearchVisible.value ||
            linkSearchTarget.value !== target ||
            event.altKey ||
            event.ctrlKey ||
            event.metaKey
        ) {
            return false;
        }

        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            linkKeyboardMode.value = true;
            return "arrow";
        }
        if (event.key === "Enter") {
            return "enter";
        }
        if (event.key === "Escape") {
            return "escape";
        }
        if (!linkKeyboardMode.value) {
            return false;
        }
        if (event.key === "ArrowLeft") {
            event.preventDefault();
            collapseActiveLinkSection();
            return true;
        }
        if (event.key === "ArrowRight") {
            event.preventDefault();
            expandActiveLinkSection();
            return true;
        }
        return false;
    }

    function detectLinkSearchTrigger(textBefore) {
        const lastOpen = textBefore.lastIndexOf("[[");
        const lastClose = textBefore.lastIndexOf("]]");
        if (lastOpen !== -1 && lastOpen > lastClose) {
            const query = textBefore.slice(lastOpen + 2);
            if (!query.includes("]]") && !query.includes("\n")) {
                return {
                    type: "wiki",
                    start: lastOpen,
                    query,
                };
            }
        }

        const lineStart = textBefore.lastIndexOf("\n") + 1;
        const line = textBefore.slice(lineStart);
        const slashMatch = line.match(/^(\s*)\/\/([^\n]*)$/);
        if (slashMatch) {
            return {
                type: "slash",
                start: lineStart + slashMatch[1].length,
                query: slashMatch[2],
            };
        }

        return null;
    }

    function resetLinkSearchResults() {
        abortSearchRequest(linkSearchRequest);
        linkSearching.value = false;
        linkSearchSections.value = [];
        linkSearchCollapsedSections.value = {};
        linkSearchStatusMessage.value = "";
        linkSearchIndex.value = -1;
    }

    function updateLinkSearchFromCursor() {
        const context = getLinkSearchContext();
        if (!context || !context.isAvailable()) {
            closeLinkSearch();
            return;
        }
        const position = context.getOffset();
        const textBefore = context.text.value.slice(0, position);
        const trigger = detectLinkSearchTrigger(textBefore);
        if (!trigger) {
            closeLinkSearch();
            return;
        }

        const queryUnchanged =
            linkSearchVisible.value &&
            trigger.type === linkSearchTriggerType.value &&
            trigger.start === linkSearchTriggerStart.value &&
            trigger.query === linkSearchQuery.value;

        linkSearchTriggerType.value = trigger.type;
        linkSearchTriggerStart.value = trigger.start;
        linkSearchQuery.value = trigger.query;
        linkSearchVisible.value = true;
        updateLinkPopupPosition();

        if (queryUnchanged) {
            return;
        }

        clearTimeout(linkSearchTimeout);
        if (!trigger.query.trim()) {
            resetLinkSearchResults();
            return;
        }
        linkSearchTimeout = setTimeout(doLinkSearch, 150);
    }

    function updateLinkPopupPosition() {
        const context = getLinkSearchContext();
        if (!context || !context.isAvailable() || !linkSearchVisible.value) return;

        const { left: caretLeft, top: caretTop } = context.getCaretRect();
        const lineHeight = context.getLineHeight();
        const popupWidth = 320;
        const popupHeight = 220;
        const gap = 8;
        const pad = 8;

        let left = Math.max(pad, caretLeft);
        let top = caretTop + lineHeight + gap;

        const maxLeft = Math.max(pad, context.getClientWidth() - popupWidth - pad);
        left = Math.min(left, maxLeft);

        const viewportTop = context.getViewportTop();
        const popupBottom = viewportTop + top + popupHeight;
        if (popupBottom > window.innerHeight - 12) {
            top = Math.max(pad, caretTop - popupHeight - gap);
        }

        linkPopupStyle.value = {
            left: `${left}px`,
            top: `${top}px`,
        };
    }

    async function doLinkSearch() {
        const query = linkSearchQuery.value.trim();
        linkSearchCollapsedSections.value = {};
        await runStreamedSearch({
            token,
            streamSearchNotes,
            query,
            sectionsRef: linkSearchSections,
            statusRef: linkSearchStatusMessage,
            searchingRef: linkSearching,
            requestStore: linkSearchRequest,
            onFirstResult: () => {
                if (linkSearchIndex.value < 0 && linkSearchResults.value.length > 0) {
                    linkSearchIndex.value = 0;
                }
            },
            onReset: () => {
                linkSearchSections.value = [];
                linkSearchCollapsedSections.value = {};
                linkSearchStatusMessage.value = "";
                linkKeyboardMode.value = false;
                linkSearchIndex.value = -1;
            },
            onDone: () => {
                linkSearchStatusMessage.value = "";
            },
        });
    }

    function closeLinkSearch() {
        linkSearchVisible.value = false;
        linkSearchQuery.value = "";
        linkSearchTriggerType.value = null;
        linkSearchTriggerStart.value = -1;
        linkKeyboardMode.value = false;
        resetLinkSearchResults();
        linkSearchTarget.value = null;
        clearTimeout(linkSearchTimeout);
    }

    function linkSearchOwnsFocus() {
        const context = getLinkSearchContext();
        return context ? context.ownsFocus() : false;
    }

    function selectLinkResult(note) {
        const context = getLinkSearchContext();
        if (!context || !context.isAvailable()) return;
        const position = context.getOffset();
        const textBefore = context.text.value.slice(0, position);
        const textAfter = context.text.value.slice(position);
        const linkText = `[${note.title || "Untitled"}](/note/${note.id})`;

        let replaceStart = -1;
        if (linkSearchTriggerType.value === "wiki") {
            replaceStart =
                linkSearchTriggerStart.value >= 0
                    ? linkSearchTriggerStart.value
                    : textBefore.lastIndexOf("[[");
        } else if (linkSearchTriggerType.value === "slash") {
            replaceStart = linkSearchTriggerStart.value;
        }
        if (replaceStart == null || replaceStart < 0) return;

        const newText = textBefore.slice(0, replaceStart) + linkText;
        context.applyText(newText + textAfter, newText.length);
        closeLinkSearch();
        context.onChange?.();
    }

    watch([linkHintMode, ...hintRefreshSources], async ([active]) => {
        if (!active) return;
        await nextTick();
        scheduleLinkHintRefresh();
    });

    onMounted(() => {
        window.addEventListener("keydown", onLinkHintKeyDown, true);
        window.addEventListener("resize", scheduleLinkHintRefresh);
        window.addEventListener("scroll", scheduleLinkHintRefresh, true);
        window.addEventListener("blur", onLinkHintWindowBlur);
    });

    onUnmounted(() => {
        window.removeEventListener("keydown", onLinkHintKeyDown, true);
        window.removeEventListener("resize", scheduleLinkHintRefresh);
        window.removeEventListener("scroll", scheduleLinkHintRefresh, true);
        window.removeEventListener("blur", onLinkHintWindowBlur);
        cancelLinkHintRefresh();
        clearTimeout(linkSearchTimeout);
        abortSearchRequest(linkSearchRequest);
    });

    return {
        closeLinkHintMode,
        closeLinkSearch,
        collapseActiveLinkSection,
        expandActiveLinkSection,
        linkHintEntries,
        linkHintMode,
        linkHintTyped,
        linkKeyboardMode,
        linkPopupStyle,
        linkSearchIndex,
        linkSearchOwnsFocus,
        linkSearchQuery,
        linkSearchResults,
        linkSearchSectionGroups,
        linkSearchStatusMessage,
        linkSearchTarget,
        linkSearchVisible,
        linkSearching,
        linkSelectableEntries,
        onLinkEditorCaretMove,
        onLinkEditorInput,
        onLinkEditorKeydown,
        onLinkEditorScroll,
        scheduleLinkHintRefresh,
        selectLinkResult,
        toggleLinkHintMode,
        toggleLinkSearchSection,
    };
}
