<template>
  <div class="layout">
    <!-- Sidebar -->
    <aside class="sidebar">
      <div class="sidebar-header">
        <img src="../assets/MentisEterna_logo.svg" alt="Logo" class="app-logo" />
        <span class="app-title">MentisEterna</span>
        <button class="btn-ghost icon-btn" title="Logout" @click="$emit('logout')">⏻</button>
      </div>
      <div class="search-box">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Semantic search…"
          class="search-input"
          @input="onSearchInput"
        />
        <span v-if="searching" class="search-spinner">⟳</span>
      </div>
      <button class="btn-amber new-btn" @click="newNote">+ New Note</button>
      <div class="note-list">
        <!-- Search results mode -->
        <template v-if="searchQuery.trim()">
          <div
            v-for="sr in searchResults"
            :key="sr.id"
            class="note-item"
            :class="{ active: selected?.id === sr.id }"
            @click="selectSearchResult(sr)"
          >
            <span class="note-title">{{ sr.title || 'Untitled' }}</span>
            <span class="note-date">{{ fmtDate(sr.updated_at) }} — {{ relevancePct(sr.distance) }}</span>
          </div>
          <div v-if="searchResults.length === 0 && !searching" class="empty-list">
            No results
          </div>
        </template>
        <!-- Standard list mode -->
        <template v-else>
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
        </template>
        <div v-if="loading || searching" class="empty-list">Loading…</div>
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
            <button class="btn-ghost" :class="{ active: showHistory }" @click="toggleHistory">
              History
            </button>
            <button class="btn-danger" @click="confirmDelete">Delete</button>
          </div>
        </div>
        <p v-if="saveError" class="save-error">{{ saveError }}</p>
        <div class="editor-body">
          <textarea
            v-model="editBody"
            class="body-textarea"
            placeholder="Write your note here…"
            @input="dirty = true"
          />
          <aside v-if="showHistory" class="history-panel">
            <div class="history-header">History</div>
            <div v-if="historyLoading" class="history-empty">Loading…</div>
            <div v-else-if="history.length === 0" class="history-empty">No history yet</div>
            <div
              v-else
              v-for="entry in history"
              :key="entry.id"
              class="history-entry"
              @click="restoreBody(entry.body)"
            >
              <span class="history-date">{{ fmtDateFull(entry.created_at) }}</span>
              <pre class="history-preview">{{ entry.body.slice(0, 120) }}{{ entry.body.length > 120 ? '…' : '' }}</pre>
            </div>
          </aside>
        </div>
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
import { fetchNotes, createNote, updateNote, deleteNote, fetchNoteHistory, searchNotes } from '../api.js'

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
const showHistory = ref(false)
const history = ref([])
const historyLoading = ref(false)

// Search state
const searchQuery = ref('')
const searchResults = ref([])
const searching = ref(false)
let searchTimeout = null

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
  showHistory.value = false
  history.value = []
}

function selectSearchResult(sr) {
  // sr is a SearchResult (Note + distance). Select it like a normal note.
  selected.value = { id: sr.id, title: sr.title, parent_id: sr.parent_id,
    body: sr.body, created_at: sr.created_at, updated_at: sr.updated_at }
  editTitle.value = sr.title
  editBody.value = sr.body
  dirty.value = false
  saveError.value = ''
  showHistory.value = false
  history.value = []
}

function newNote() {
  selected.value = { id: null, title: '', body: '' }
  editTitle.value = ''
  editBody.value = ''
  dirty.value = true
  saveError.value = ''
  showHistory.value = false
  history.value = []
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

async function toggleHistory() {
  if (!selected.value?.id) return
  showHistory.value = !showHistory.value
  if (showHistory.value && history.value.length === 0) {
    historyLoading.value = true
    try {
      history.value = await fetchNoteHistory(props.token, selected.value.id)
    } finally {
      historyLoading.value = false
    }
  }
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
      // Refresh history if open
      if (showHistory.value) {
        history.value = await fetchNoteHistory(props.token, updated.id)
      }
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

function restoreBody(body) {
  editBody.value = body
  dirty.value = true
}

function fmtDate(iso) {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function fmtDateFull(iso) {
  if (!iso) return ''
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

function onSearchInput() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(doSearch, 300)
}

async function doSearch() {
  const q = searchQuery.value.trim()
  if (!q) {
    searchResults.value = []
    return
  }
  searching.value = true
  try {
    searchResults.value = await searchNotes(props.token, q)
  } catch (e) {
    searchResults.value = []
  } finally {
    searching.value = false
  }
}

function relevancePct(distance) {
  // sqlite-vss cosine distance ranges [0, 2] where 0 is identical.
  // Map to percentage: distance 0 → 100%, distance 2 → 0%
  if (distance == null) return ''
  const pct = Math.max(0, Math.round((1 - distance / 2) * 100))
  return pct + '% match'
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
  background: var(--panel-bg);
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

.app-logo {
  width: 3rem;
  height: 3rem;
  border-radius: 25%;
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

.search-box {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-color);
}

.search-input {
  flex: 1;
  font-size: 0.82rem;
  padding: 0.35rem 0.6rem;
}

.search-spinner {
  color: var(--accent-teal);
  font-size: 1.1rem;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
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

.note-item:hover { background: var(--raised-bg); }

.note-item.active {
  background: var(--raised-bg);
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
  background: var(--panel-bg);
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
  background: var(--panel-bg);
}

.editor-body {
  flex: 1;
  display: flex;
  overflow: hidden;
}

.body-textarea {
  flex: 1;
  border: none;
  border-radius: 0;
  background: var(--html-bg);
  padding: 1.25rem;
  font-size: 0.95rem;
  line-height: 1.7;
  min-height: 0;
  min-width: 0;
  resize: none;
}

.body-textarea:focus { border-color: transparent; }

.history-panel {
  width: 280px;
  min-width: 220px;
  border-left: 1px solid var(--border-color);
  background: var(--panel-bg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.history-header {
  padding: 0.65rem 1rem;
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--font-color-secondary);
  border-bottom: 1px solid var(--border-color);
}

.history-empty {
  padding: 1.25rem 1rem;
  font-size: 0.85rem;
  color: var(--font-color-secondary);
  text-align: center;
}

.history-entry {
  padding: 0.65rem 1rem;
  cursor: pointer;
  border-bottom: 1px solid var(--border-color);
  transition: background 0.1s;
  overflow-y: auto;
}

.history-entry:hover { background: var(--raised-bg); }

.history-date {
  display: block;
  font-size: 0.75rem;
  color: var(--date-color);
  margin-bottom: 0.3rem;
}

.history-preview {
  font-size: 0.8rem;
  color: var(--font-color);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  font-family: inherit;
  line-height: 1.5;
}

.btn-ghost.active {
  background: var(--raised-bg);
  border-color: var(--accent-teal);
  color: var(--accent-teal);
}

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
  background: var(--raised-bg);
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
