<!--
    Monaco-based text editor wrapper.

    Used for the note body edit mode. Owns the Monaco instance lifecycle,
    the "mentis" dark theme (terminal aesthetic), auto-growing height,
    external v-model sync, and forwards DOM paste/drop file events plus a
    synchronous key-interception hook (used by the [[ link search popup to
    swallow keys Monaco would otherwise act on).
-->
<script setup>
import { onMounted, onBeforeUnmount, ref, watch } from "vue";
import * as monaco from "monaco-editor";
// Deep esm/vs/... paths are not exported by the package's exports map —
// the mapped specifier is what resolves.
import editorWorker from "monaco-editor/editor/editor.worker.js?worker";

const props = defineProps({
    modelValue: { type: String, default: "" },
    language: { type: String, default: "markdown" },
    placeholder: { type: String, default: "" },
    options: { type: Object, default: () => ({}) },
    // (event: KeyboardEvent) => boolean — return true to block Monaco's own
    // handling of the key (caret movement, newline insertion, ...).
    interceptKey: { type: Function, default: null },
    autoFocus: { type: Boolean, default: true },
});

const emit = defineEmits([
    "update:modelValue",
    "ready",
    "cursor-moved",
    "scrolled",
    "paste-files",
    "drop-files",
]);

// ---------------------------------------------------------------------------
// Monaco environment + theme (module scope, once per app)
// ---------------------------------------------------------------------------

let monacoEnvironmentReady = false;
function ensureMonacoEnvironment() {
    if (monacoEnvironmentReady) return;
    monacoEnvironmentReady = true;
    // Markdown editing needs only the core editor worker (no language
    // services); unknown labels fall through to it as well.
    globalThis.MonacoEnvironment = {
        getWorker() {
            return new editorWorker();
        },
    };
}

// The app's terminal dark theme (see AGENTS.md token table).
const MENTIS_THEME = {
    base: "vs-dark",
    inherit: true,
    rules: [
        { token: "", foreground: "cbe8dc" },
        { token: "keyword", foreground: "3fe0cf" },
        { token: "keyword.json", foreground: "3fe0cf" },
        { token: "string", foreground: "ffd28a" },
        { token: "string.md", foreground: "ffd28a" },
        { token: "variable", foreground: "ffb347" },
        { token: "variable.md", foreground: "ffb347" },
        { token: "comment", foreground: "7aa695" },
        { token: "delimiter", foreground: "7aa695" },
        { token: "tag", foreground: "3fe0cf" },
        { token: "attribute.name", foreground: "7aa695" },
        { token: "attribute.value", foreground: "45cfc0" },
    ],
    colors: {
        "editor.background": "#082029",
        "editor.foreground": "#cbe8dc",
        "editorCursor.foreground": "#3fe0cf",
        "editor.lineHighlightBackground": "#00000000",
        "editor.lineHighlightBorder": "#00000000",
        "editor.selectionBackground": "#143c46",
        "editor.inactiveSelectionBackground": "#0d2b33",
        "editor.selectionHighlightBackground": "#143c4655",
        "editorIndentGuide.background1": "#143c46",
        "editorLineNumber.foreground": "#7aa695",
        "editorLineNumber.activeForeground": "#cbe8dc",
        "editorGutter.background": "#082029",
        "editorWidget.background": "#041019",
        "editorWidget.border": "#143c46",
        "editorWidget.foreground": "#cbe8dc",
        "editorSuggestWidget.background": "#041019",
        "editorSuggestWidget.border": "#143c46",
        "editorSuggestWidget.selectedBackground": "#082029",
        "editorHoverWidget.background": "#041019",
        "editorHoverWidget.border": "#143c46",
        "input.background": "#041019",
        "input.border": "#143c46",
        "input.foreground": "#cbe8dc",
        "focusBorder": "#3fe0cf",
        "scrollbarSlider.background": "#143c4666",
        "scrollbarSlider.hoverBackground": "#143c4699",
        "scrollbarSlider.activeBackground": "#27968a",
        "editorBracketMatch.background": "#143c46",
        "editorBracketMatch.border": "#3fe0cf",
        "minimap.background": "#082029",
        "editorOverviewRuler.background": "#082029",
        "editorPlaceholder.foreground": "#7aa695",
    },
};

let themeReady = false;
function ensureTheme() {
    if (themeReady) return;
    themeReady = true;
    monaco.editor.defineTheme("mentis", MENTIS_THEME);
}

// ---------------------------------------------------------------------------
// Editor state
// ---------------------------------------------------------------------------

const containerEl = ref(null);
let editor = null;
let model = null;
let applyingExternalValue = false;
let anchorDom = null;

function readFontMono() {
    try {
        const value = getComputedStyle(document.documentElement)
            .getPropertyValue("--font-mono")
            .trim();
        return value || "ui-monospace, monospace";
    } catch {
        return "ui-monospace, monospace";
    }
}

function buildOptions() {
    return {
        theme: "mentis",
        fontFamily: readFontMono(),
        fontSize: 14,
        lineHeight: 23,
        wordWrap: "on",
        minimap: { enabled: false },
        lineNumbers: "off",
        lineNumbersMinChars: 0,
        lineDecorationsWidth: 12,
        glyphMargin: false,
        folding: false,
        renderLineHighlight: "none",
        overviewRulerLanes: 0,
        hideCursorInOverviewRuler: true,
        scrollBeyondLastLine: false,
        automaticLayout: true,
        scrollbar: {
            vertical: "hidden",
            horizontal: "hidden",
            alwaysConsumeMouseWheel: false,
            useShadows: false,
        },
        padding: { top: 10, bottom: 10 },
        // Keep the [[ link search popup the only completion surface.
        quickSuggestions: false,
        suggestOnTriggerCharacters: false,
        codeLens: false,
        // No auto-closing brackets: typing `[[` must not auto-insert `]]`.
        autoClosingBrackets: "never",
        autoClosingQuotes: "never",
        autoClosingOvertype: "never",
        autoClosingDelete: "never",
        autoSurround: "never",
        // Never rewrite pasted content behind the user's back.
        unusualLineTerminators: "off",
        copyWithSyntaxHighlighting: false,
        dropIntoEditor: { enabled: false },
        links: false,
        stickyScroll: { enabled: false },
        guides: {
            indentation: false,
            bracketPairs: false,
            highlightActiveIndentation: false,
        },
        unicodeHighlight: {
            ambiguousCharacters: false,
            invisibleCharacters: false,
        },
        insertSpaces: true,
        tabSize: 4,
        cursorBlinking: "smooth",
        placeholder: props.placeholder,
        ...props.options,
    };
}

// Auto-grow: the editor container follows the content height so the page
// (chat feed) scrolls instead of the editor itself. CSS min-height on the
// container keeps a comfortable minimum editing area; layout uses the
// resulting box so the whole surface is clickable.
function syncHeight() {
    if (!editor || !containerEl.value) return;
    const contentHeight = editor.getContentHeight();
    containerEl.value.style.height = `${Math.max(contentHeight, 1)}px`;
    const boxHeight = containerEl.value.getBoundingClientRect().height;
    editor.layout({
        width: containerEl.value.clientWidth,
        height: boxHeight,
    });
}

function getOffset() {
    if (!editor || !model) return 0;
    const position = editor.getPosition();
    if (!position) return 0;
    return model.getOffsetAt(position);
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

onMounted(() => {
    ensureMonacoEnvironment();
    ensureTheme();

    model = monaco.editor.createModel(props.modelValue ?? "", props.language);
    editor = monaco.editor.create(containerEl.value, buildOptions());

    editor.onDidChangeModelContent(() => {
        if (applyingExternalValue) return;
        emit("update:modelValue", model.getValue());
    });
    editor.onDidChangeCursorSelection(() => {
        emit("cursor-moved");
    });
    editor.onDidScrollChange(() => {
        // One frame later: the cursor anchor content widget has been
        // repositioned by then, so popup coords read fresh.
        requestAnimationFrame(() => emit("scrolled"));
    });
    editor.onDidContentSizeChange(syncHeight);
    editor.onKeyDown((e) => {
        if (!props.interceptKey) return;
        const native = e.browserEvent;
        if (native && props.interceptKey(native)) {
            // Block Monaco's command dispatch (its keybinding listener runs
            // on an ancestor node) as well as the native default.
            e.preventDefault();
            e.stopPropagation();
            native.preventDefault();
            native.stopImmediatePropagation();
        }
    });

    // Zero-size content widget anchored at the cursor: gives exact popup
    // coordinates for anything layered on top (link search).
    anchorDom = document.createElement("div");
    anchorDom.style.cssText = "position:absolute;width:0;height:0;top:0;left:0;";
    const anchorWidget = {
        getId: () => "mentis-cursor-anchor",
        getDomNode: () => anchorDom,
        getPosition: () => {
            const position = editor && editor.getPosition();
            if (!position) return null;
            return {
                position,
                preference: [monaco.editor.ContentWidgetPositionPreference.EXACT],
            };
        },
    };
    editor.addContentWidget(anchorWidget);

    // File paste / drop forwarding (capture phase, before Monaco sees them).
    containerEl.value.addEventListener(
        "paste",
        (e) => {
            const files = e.clipboardData?.files;
            if (!files || files.length === 0) return;
            emit("paste-files", e);
            if (e.defaultPrevented) {
                e.stopImmediatePropagation();
                e.stopPropagation();
            }
        },
        true,
    );
    containerEl.value.addEventListener(
        "dragover",
        (e) => {
            if (e.dataTransfer?.types?.includes?.("Files")) {
                e.preventDefault();
            }
        },
        true,
    );
    containerEl.value.addEventListener(
        "drop",
        (e) => {
            const files = e.dataTransfer?.files;
            if (!files || files.length === 0) return;
            e.preventDefault();
            e.stopImmediatePropagation();
            e.stopPropagation();
            emit("drop-files", e);
        },
        true,
    );

    syncHeight();
    emit("ready");
    if (props.autoFocus) {
        editor.focus();
    }
});

onBeforeUnmount(() => {
    if (editor) {
        editor.dispose();
        editor = null;
    }
    if (model) {
        model.dispose();
        model = null;
    }
    anchorDom = null;
});

// External value changes (note switch, live sync, upload placeholders):
// push into the model while keeping the caret where it was.
watch(
    () => props.modelValue,
    (value) => {
        if (!editor || !model) return;
        const next = value ?? "";
        if (next === model.getValue()) return;
        const offset = getOffset();
        applyingExternalValue = true;
        try {
            model.setValue(next);
        } finally {
            applyingExternalValue = false;
        }
        try {
            editor.setPosition(
                model.getPositionAt(Math.min(offset, next.length)),
            );
        } catch {
            /* clamp issues are non-fatal */
        }
    },
);

watch(
    () => props.placeholder,
    (value) => {
        editor?.updateOptions({ placeholder: value });
    },
);

// ---------------------------------------------------------------------------
// Exposed API
// ---------------------------------------------------------------------------

function focusEditor() {
    editor?.focus();
}

function ownsFocus() {
    return Boolean(editor?.hasTextFocus());
}

function insertAtCursor(text) {
    if (!editor || !model || !text) return;
    const selection = editor.getSelection();
    const startOffset = selection
        ? model.getOffsetAt(selection.getStartPosition())
        : model.getValue().length;
    editor.executeEdits("mentis-insert", [
        {
            range: selection || model.getFullModelRange(),
            text,
            forceMoveMarkers: false,
        },
    ]);
    try {
        editor.setPosition(
            model.getPositionAt(Math.min(startOffset + text.length, model.getValue().length)),
        );
    } catch {
        /* non-fatal */
    }
    editor.focus();
}

function applyText(newText, selectOffset) {
    if (!editor || !model) return;
    editor.executeEdits("mentis-apply", [
        {
            range: model.getFullModelRange(),
            text: newText,
            forceMoveMarkers: false,
        },
    ]);
    try {
        editor.setPosition(
            model.getPositionAt(
                Math.max(0, Math.min(selectOffset, newText.length)),
            ),
        );
    } catch {
        /* non-fatal */
    }
    editor.focus();
}

function getCaretViewportRect() {
    if (!anchorDom) return { left: 0, top: 0 };
    const rect = anchorDom.getBoundingClientRect();
    return { left: rect.left, top: rect.top };
}

defineExpose({
    isReady: () => Boolean(editor && model),
    focus: focusEditor,
    ownsFocus,
    getOffset,
    insertAtCursor,
    applyText,
    getCaretViewportRect,
    lineHeightPx: () => {
        if (!editor) return 23;
        try {
            return editor.getOption(monaco.editor.EditorOption.lineHeight) || 23;
        } catch {
            return 23;
        }
    },
    layout: () => syncHeight(),
});
</script>

<template>
    <div ref="containerEl" class="monaco-editor-container"></div>
</template>

<style scoped>
.monaco-editor-container {
    width: 100%;
    min-height: 0;
}

/* Monaco internals: keep the surface clean and terminal-flavored. */
.monaco-editor-container :deep(.monaco-editor) {
    background: transparent;
}

.monaco-editor-container :deep(.monaco-editor .overflow-guard) {
    border-radius: inherit;
}
</style>
