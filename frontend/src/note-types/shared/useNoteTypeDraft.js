/**
 * Shared composable: useNoteTypeDraft
 *
 * Manages a local editable copy of customData for a note-type component.
 * Handles initialization from incoming customData and normalizes via the
 * registry entry.
 */

import { ref, watch } from "vue";
import { getNoteTypeOrDefault } from "../registry.js";

export function useNoteTypeDraft(props, options = {}) {
    const { onEmit, typeId } = options;

    const typeDef = getNoteTypeOrDefault(
        typeId || (props.note && props.note.type),
    );

    const draft = ref(
        typeDef.normalizeCustomData(props.customData, props.note),
    );

    // Re-normalize when customData prop changes
    watch(
        () => props.customData,
        (cd) => {
            draft.value = typeDef.normalizeCustomData(cd, props.note);
        },
    );

    // When the local draft changes, emit update:customData
    watch(
        draft,
        (val) => {
            if (onEmit) {
                onEmit(val);
            } else if (typeof emit !== "undefined") {
                // No automatic emit — consumer watches draft themselves.
            }
        },
        { deep: true },
    );

    return {
        draft,
        typeDef,
        resetToEmpty() {
            draft.value = typeDef.emptyCustomData();
        },
    };
}
