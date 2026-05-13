<template>
    <div v-if="activeNote" class="note-type-renderer">
        <!-- Custom component from the registry -->
        <component
            v-if="typeDef && typeDef.component"
            :is="typeDef.component"
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="resolvedCustomData"
            :uiSchema="resolvedUiSchema"
            @selectNote="(id) => $emit('selectNote', id)"
            @update:customData="(d) => $emit('update:customData', d)"
        />

        <!-- Schema fallback for types without a custom component -->
        <SchemaNoteType
            v-else-if="
                typeDef && typeDef.supportsSchemaFallback && resolvedUiSchema
            "
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="resolvedCustomData"
            :uiSchema="resolvedUiSchema"
            @selectNote="(id) => $emit('selectNote', id)"
            @update:customData="(d) => $emit('update:customData', d)"
        />

        <!-- Last-resort fallback for unsupported types -->
        <UnsupportedNoteType
            v-else-if="activeNote.type && !typeDef"
            :note="activeNote"
            :token="token"
            :editing="editing"
            :customData="resolvedCustomData"
            :uiSchema="resolvedUiSchema"
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

/**
 * Resolve the effective uiSchema, falling back to plugin.view from the
 * new PluginDetail shape when not provided explicitly as a prop.
 */
const resolvedUiSchema = computed(() => {
    if (props.uiSchema) return props.uiSchema;
    const plugin = props.note?.plugin;
    if (!plugin || typeof plugin !== "object") return null;
    return plugin.view || null;
});

/**
 * Resolve the effective customData, falling back to plugin.config from the
 * new PluginDetail shape when not provided explicitly as a prop.
 */
const resolvedCustomData = computed(() => {
    if (props.customData != null) return props.customData;
    const plugin = props.note?.plugin;
    if (!plugin || typeof plugin !== "object") return null;
    return plugin.config || null;
});
</script>

<style scoped>
.note-type-renderer {
    margin-top: 0.5rem;
}
</style>
