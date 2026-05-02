<template>
  <div class="layout">
    <!-- Sidebar -->
    <aside class="sidebar">
      <div class="sidebar-header">
        <span class="app-title">MentisEterna</span>
        <button class="btn-ghost icon-btn" title="Logout" @click="$emit('logout')">⏻</button>
      </div>
      <button class="btn-amber new-btn" @click="newNote">+ New Note</button>
      <div class="note-list">
        <div
          v-for="note in notes"
          :key="note.id"
          class="note-item"
          :class="{ active: selected?.id === note.id }"
          @click="selectNote(note)"
        >
          <span class="note-title">{{ note.title || 'Untitled' }}</span>
          <span class="note-date">{{ fmtDate(note.updated_at) }}</span>
        </div>
        <div v-if="notes.length === 0 && !loading" class="empty-list">
          No notes yet
        </div>
        <div v-if="loading" class="empty-list">Loading…</div>
      </div>
    </aside>

    <!-- Editor -->
    <main class="editor-pane">
      <template v-if="selected">
        <div class="editor-header">
          <input
            v-model="editTitle"
            class="title-input"
            placeholder="Note title"
            @input="dirty = true"
          />
          <div class="editor-actions">
            <button class="btn-primary" :disabled="!dirty || saving" @click="save">
              {{ saving ? 'Saving…' : 'Save' }}
            </button>
            <button class="btn-danger" @click="confirmDelete">Delete</button>
          </div>
        </div>
        <p v-if="saveError" class="save-error">{{ saveError }}</p>
        <textarea
          v-model="editBody"
          class="body-textarea"
          placeholder="Write your note here…"
          @input="dirty = true"
        />
      </template>
      <div v-else class="no-selection">
        <p>Select a note or create a new one</p>
      </div>
    </main>

    <!-- Delete confirm modal -->
    <div v-if="showDeleteModal" class="modal-overlay" @click.self="showDeleteModal = false">
      <div class="modal">
        <p>Delete <strong>{{ selected?.title || 'this note' }}</strong>?</p>
        <div class="modal-actions">
          <button class="btn-ghost" @click="showDeleteModal = false">Cancel</button>
          <button class="btn-danger" :disabled="deleting" @click="doDelete">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue'
import { fetchNotes, createNote, updateNote, deleteNote } from '../api.js'

const props = defineProps({ token: String })
const emit = defineEmits(['logout'])

const notes = ref([])
const loading = ref(false)
const selected = ref(null)
const editTitle = ref('')
const editBody = ref('')
const dirty = ref(false)
const saving = ref(false)
const saveError = ref('')
const showDeleteModal = ref(false)
const deleting = ref(false)

onMounted(loadNotes)

async function loadNotes() {
  loading.value = true
  try {
    notes.value = await fetchNotes(props.token)
  } finally {
    loading.value = false
  }
}

function selectNote(note) {
  selected.value = note
  editTitle.value = note.title
  editBody.value = note.body
  dirty.value = false
  saveError.value = ''
}

function newNote() {
  selected.value = { id: null, title: '', body: '' }
  editTitle.value = ''
  editBody.value = ''
  dirty.value = true
  saveError.value = ''
}

async function save() {
  saveError.value = ''
  saving.value = true
  try {
    let updated
    if (selected.value.id) {
      updated = await updateNote(props.token, selected.value.id, editTitle.value, editBody.value)
      const idx = notes.value.findIndex(n => n.id === updated.id)
      if (idx !== -1) notes.value[idx] = updated
    } else {
      updated = await createNote(props.token, editTitle.value, editBody.value)
      notes.value.unshift(updated)
    }
    selected.value = updated
    dirty.value = false
  } catch (e) {
    saveError.value = e.message
  } finally {
    saving.value = false
  }
}

function confirmDelete() {
  showDeleteModal.value = true
}

async function doDelete() {
  deleting.value = true
  try {
    await deleteNote(props.token, selected.value.id)
    notes.value = notes.value.filter(n => n.id !== selected.value.id)
    selected.value = null
    showDeleteModal.value = false
  } finally {
    deleting.value = false
  }
}

function fmtDate(iso) {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}
</script>

<style scoped>
.layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

/* Sidebar */
.sidebar {
  width: 260px;
  min-width: 220px;
  background: var(--surface);
  border-right: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sidebar-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1rem 0.75rem;
  border-bottom: 1px solid var(--border-color);
}

.app-title {
  font-size: 1rem;
  font-weight: 700;
  color: var(--header-title-color);
  letter-spacing: 0.02em;
}

.icon-btn {
  padding: 0.3rem 0.5rem;
  font-size: 1rem;
  line-height: 1;
}

.new-btn {
  margin: 0.75rem;
  width: calc(100% - 1.5rem);
}

.note-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.25rem 0;
}

.note-item {
  padding: 0.65rem 1rem;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: background 0.1s, border-color 0.1s;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.note-item:hover { background: var(--surface2); }

.note-item.active {
  background: var(--surface2);
  border-left-color: var(--accent-teal);
}

.note-title {
  font-size: 0.9rem;
  color: var(--font-color);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.note-date {
  font-size: 0.75rem;
  color: var(--date-color);
}

.empty-list {
  padding: 1.5rem 1rem;
  color: var(--font-color-secondary);
  font-size: 0.85rem;
  text-align: center;
}

/* Editor */
.editor-pane {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--html-bg);
}

.editor-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.85rem 1.25rem;
  border-bottom: 1px solid var(--border-color);
  background: var(--surface);
}

.title-input {
  flex: 1;
  font-size: 1.05rem;
  font-weight: 600;
  background: transparent;
  border: none;
  border-bottom: 1px solid transparent;
  border-radius: 0;
  padding: 0.2rem 0;
}

.title-input:focus {
  border-bottom-color: var(--accent-teal);
}

.editor-actions {
  display: flex;
  gap: 0.5rem;
  flex-shrink: 0;
}

.save-error {
  padding: 0.4rem 1.25rem;
  font-size: 0.85rem;
  color: var(--heading-color);
  background: var(--surface);
}

.body-textarea {
  flex: 1;
  width: 100%;
  border: none;
  border-radius: 0;
  background: var(--html-bg);
  padding: 1.25rem;
  font-size: 0.95rem;
  line-height: 1.7;
  min-height: 0;
}

.body-textarea:focus { border-color: transparent; }

.no-selection {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--font-color-secondary);
  font-size: 0.95rem;
}

/* Modal */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(1, 16, 31, 0.75);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.modal {
  background: var(--surface2);
  border: 1px solid var(--border-color);
  border-radius: 10px;
  padding: 1.75rem 2rem;
  max-width: 360px;
  width: 90%;
}

.modal p {
  margin-bottom: 1.25rem;
  font-size: 0.95rem;
}

.modal-actions {
  display: flex;
  gap: 0.75rem;
  justify-content: flex-end;
}
</style>
