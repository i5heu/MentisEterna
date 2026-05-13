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
 * Resolve the effective customData for rendering.
 *
 * The new API splits plugin data into two fields:
 *   - plugin.config — persisted user-editable config
 *   - plugin.view   — computed/derived view data (recipes list, index entries, etc.)
 *
 * Components need both to render, but should only emit config changes.
 * We always merge view on top of config so that view data is always
 * available, even after the parent stores config-only data in the
 * customData ref (after an update:customData emit).
 */
const resolvedCustomData = computed(() => {
    const plugin = props.note?.plugin;
    const config =
        props.customData != null
            ? props.customData
            : plugin && typeof plugin === "object"
              ? plugin.config
              : null;
    const view = plugin && typeof plugin === "object" ? plugin.view : null;

    // If no view data, return config as-is
    if (!view || typeof view !== "object") {
        return config || null;
    }
    // Merge view on top of config for full rendering
    if (!config || typeof config !== "object") {
        return view;
    }
    return { ...config, ...view };
});
</script>

<style scoped>
.note-type-renderer {
    margin-top: 0.5rem;
}
</style>
