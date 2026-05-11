<template>
  <div v-if="attachments?.length" class="note-attachments">
    <h4>Attachments</h4>
    <ul>
      <li v-for="file in attachments" :key="file.id">
        <a :href="file.url" target="_blank" rel="noreferrer">{{ file.filename }}</a>
        <span class="attachment-size">{{ formatSize(file.size_bytes) }}</span>
        <button
          v-if="editing"
          class="btn-ghost attachment-remove-btn"
          title="Remove attachment"
          @click="$emit('remove', file)"
        >
          ✕
        </button>
      </li>
    </ul>
  </div>
</template>

<script setup>
defineProps({
  attachments: Array,
  editing: Boolean,
});
defineEmits(["remove"]);

function formatSize(bytes) {
  if (!bytes) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
</script>

<style scoped>
.note-attachments {
  margin-top: 12px;
  padding: 8px 12px;
  border-top: 1px solid var(--border-color, #333);
}
.note-attachments h4 {
  margin: 0 0 6px;
  font-size: 0.85rem;
  color: var(--font-color-secondary, #999);
}
.note-attachments ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.note-attachments li {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
  font-size: 0.85rem;
}
.note-attachments a {
  color: var(--accent-color, #60a5fa);
  text-decoration: none;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.note-attachments a:hover {
  text-decoration: underline;
}
.attachment-size {
  color: var(--font-color-secondary, #666);
  font-size: 0.75rem;
  white-space: nowrap;
}
.attachment-remove-btn {
  font-size: 0.75rem;
  padding: 2px 6px;
}
</style>
