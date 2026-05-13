<template>
    <div v-if="activeNote" class="note-type-renderer">
        <!-- Custom component from the registry -->
        <component
            v-if="typeDef && typeDef.component"
            :is="typeDef.component"
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="customData"
            :uiSchema="uiSchema"
            @selectNote="(id) => $emit('selectNote', id)"
            @update:customData="(d) => $emit('update:customData', d)"
        />

        <!-- Schema fallback for types without a custom component -->
        <SchemaNoteType
            v-else-if="typeDef && typeDef.supportsSchemaFallback && uiSchema"
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="customData"
            :uiSchema="uiSchema"
            @selectNote="(id) => $emit('selectNote', id)"
            @update:customData="(d) => $emit('update:customData', d)"
        />

        <!-- Last-resort fallback for unsupported types -->
        <UnsupportedNoteType
            v-else-if="activeNote.type && !typeDef"
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="customData"
            :uiSchema="uiSchema"
        />
    </div>
</template>

<script setup>
import { computed } from "vue";
import { getNoteTypeOrDefault } from "../note-types/registry.js";
import UnsupportedNoteType from "../note-types/shared/UnsupportedNoteType.vue";
import SchemaNoteType from "../note-types/shared/SchemaNoteType.vue";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

defineEmits(["selectNote", "update:customData"]);

const typeDef = computed(() => {
    if (!props.note) return null;
    return getNoteTypeOrDefault(props.note.type);
});

// Use either the note prop directly or a merged view-model.
// Prefer explicit props when available for draft-driven rendering.
const activeNote = computed(() => props.note);
</script>

<style scoped>
.note-type-renderer {
    margin-top: 0.5rem;
}
</style>
