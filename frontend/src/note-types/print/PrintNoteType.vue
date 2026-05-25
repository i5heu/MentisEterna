<template>
    <div class="print-panel">
        <h3>Thermal Printer</h3>

        <!-- Target selection -->
        <div class="select-section">
            <label class="select-label">
                <span>Note to print:</span>
                <select
                    v-model="selectedNoteId"
                    class="note-select"
                    @change="onTargetChange"
                >
                    <option :value="0" disabled>-- Select a note --</option>
                    <option
                        v-for="c in candidates"
                        :key="c.note_id"
                        :value="c.note_id"
                    >
                        {{ c.type_label }}: {{ c.title }}
                    </option>
                </select>
            </label>
        </div>

        <!-- Print button -->
        <div class="action-section">
            <button
                class="btn-amber"
                :disabled="printing || !selectedNoteId"
                @click="doPrint"
            >
                {{ printing ? "Printing..." : "Print" }}
            </button>
        </div>

        <!-- Result -->
        <div v-if="printError" class="result-error">
            {{ printError }}
        </div>

        <!-- Preview (when printer unavailable) -->
        <div v-if="previewText" class="preview-section">
            <h4>Preview</h4>
            <pre class="preview-box">{{ previewText }}</pre>
        </div>

        <!-- Empty state -->
        <p v-if="candidates.length === 0" class="empty-hint">
            No printable notes found. Create a recipe note first.
        </p>
    </div>
</template>

<script setup>
import { ref, watch, computed } from "vue";
import { usePluginAction } from "../shared/usePluginAction.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

const {
    loading: printing,
    error: actionError,
    execute,
} = usePluginAction(() => props.token);

// --- State ---
const selectedNoteId = ref(0);
const printError = ref(null);
const previewText = ref(null);
let hydrating = false;

// --- Derived ---
const candidates = computed(() => {
    return props.customData?.candidates || [];
});

// --- Hydrate from customData ---
function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    const targetId = cd?.target_note_id || 0;
    selectedNoteId.value = targetId;
    printError.value = null;
    previewText.value = null;
    hydrating = false;
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also catch async customData arrival.
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        const tid = cd?.target_note_id;
        if (tid && tid > 0 && selectedNoteId.value === 0) {
            hydrateFromProp();
        }
    },
);

// --- Persist target selection ---
function onTargetChange() {
    emit("update:customData", {
        target_note_id: selectedNoteId.value,
    });
}

// --- Print action ---
async function doPrint() {
    printError.value = null;
    previewText.value = null;

    if (!selectedNoteId.value) return;

    // Save the target first, then print.
    emit("update:customData", {
        target_note_id: selectedNoteId.value,
    });

    try {
        const result = await execute(props.note.id, "print", {
            target_note_id: selectedNoteId.value,
        });

        if (result?.printed) {
            // Success — nothing extra to show.
        } else if (result?.preview) {
            previewText.value = result.preview;
            printError.value = result.error || "Printer not available";
        }
    } catch (e) {
        printError.value = (e && e.message) || String(e);
    }
}
</script>

<style scoped>
.print-panel h3 {
    font-size: 1.1rem;
    margin: 0.5rem 0 0.5rem;
    color: var(--font-color-secondary);
}

.select-section {
    margin: 0.75rem 0;
}

.select-label {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    font-size: 0.9rem;
}

.note-select {
    padding: 0.4rem;
    font-size: 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--bg-input);
    color: var(--font-color);
    max-width: 100%;
}

.action-section {
    margin: 0.75rem 0;
}

.btn-amber {
    padding: 0.5rem 1.2rem;
    font-size: 0.95rem;
    border: none;
    border-radius: 4px;
    background: var(--amber, #f0a030);
    color: #fff;
    cursor: pointer;
    font-weight: 600;
}

.btn-amber:disabled {
    opacity: 0.5;
    cursor: not-allowed;
}

.result-error {
    margin-top: 0.5rem;
    padding: 0.4rem 0.6rem;
    background: var(--raised-bg);
    border: 1px solid var(--heading-color);
    border-radius: 4px;
    font-size: 0.85rem;
    color: var(--heading-color);
}

.preview-section {
    margin-top: 0.75rem;
}

.preview-section h4 {
    font-size: 0.95rem;
    margin: 0 0 0.3rem;
}

.preview-box {
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    padding: 0.5rem;
    font-size: 0.8rem;
    line-height: 1.3;
    white-space: pre;
    overflow-x: auto;
    max-height: 300px;
    overflow-y: auto;
    border-radius: 4px;
    color: var(--pre-text-color);
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
