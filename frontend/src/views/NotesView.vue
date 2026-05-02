<template>
  <div class="layout">
    <!-- Sidebar -->
    <aside class="sidebar">
      <div class="sidebar-header">
        <img src="../assets/MentisEterna_logo.svg" alt="Logo" class="app-logo" />
        <span class="app-title">MentisEterna</span>
        <button class="btn-ghost icon-btn" title="Logout" @click="$emit('logout')">⏻</button>
      </div>
      <button class="btn-amber new-btn" @click="newNote">+ New Note</button>
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
      <div class="note-list">
        <!-- Search results mode -->
        <template v-if="searchQuery.trim()">
          <div
            v-for="(sr, idx) in searchResults"
            :key="sr.id"
            class="note-item"
            :class="{ active: selected?.id === sr.id, highlighted: highlightedIndex === idx }"
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
            v-for="(note, idx) in notes"
            :key="note.id"
            class="note-item"
            :class="{ active: selected?.id === note.id, highlighted: highlightedIndex === idx }"
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
            <button class="btn-ghost" @click="toggleEdit">
              {{ isEditing ? '🖉 View' : '✎ Edit' }}
            </button>
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
            v-if="isEditing"
            v-model="editBody"
            class="body-textarea"
            placeholder="Write your note here…"
            @input="dirty = true"
          />
          <div
            v-else
            class="body-rendered markdown-body"
            v-html="renderedBody"
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

    <!-- Hotkey help modal -->
    <div v-if="showHotkeys" class="modal-overlay" @click.self="showHotkeys = false">
      <div class="modal hotkey-modal">
        <div class="hotkey-modal-header">
          <span>Keyboard Shortcuts</span>
          <button class="btn-ghost icon-btn" @click="showHotkeys = false">✕</button>
        </div>
        <div class="hotkey-list">
          <div v-for="hk in hotkeys" :key="hk.key" class="hotkey-row">
            <kbd class="hotkey-key">{{ hk.key }}</kbd>
            <span class="hotkey-desc">{{ hk.desc }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- "?" help button -->
    <button class="help-btn" title="Keyboard shortcuts (Shift+?)" @click="showHotkeys = !showHotkeys">?</button>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { marked } from 'marked'
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
const showHotkeys = ref(false)
const history = ref([])
const historyLoading = ref(false)

// Search state
const searchQuery = ref('')
const searchResults = ref([])
const searching = ref(false)
const highlightedIndex = ref(-1)
let searchTimeout = null

// The list currently shown in the sidebar (search results or standard notes)
const sidebarList = computed(() =>
  searchQuery.value.trim() ? searchResults.value : notes.value
)

// Hotkeys definition
const hotkeys = [
  { key: isMac() ? '⌘ N' : 'Ctrl+N', desc: 'New note' },
  { key: isMac() ? '⌘ S' : 'Ctrl+S', desc: 'Save note' },
  { key: isMac() ? '⌘ E' : 'Ctrl+E', desc: 'Toggle edit / preview' },
  { key: isMac() ? '⌘ H' : 'Ctrl+H', desc: 'Toggle history panel' },
  { key: isMac() ? '⌘ K' : 'Ctrl+K', desc: 'Focus search bar' },
  { key: '↑ ↓', desc: 'Navigate list / search results' },
  { key: 'Enter', desc: 'Open highlighted note' },
  { key: 'Esc', desc: 'Close panel / blur / clear highlight' },
  { key: 'Shift+?', desc: 'Toggle this help dialog' },
]

function isMac() {
  return /Mac|iPod|iPhone|iPad/.test(navigator.platform || navigator.userAgentData?.platform || '')
}

// Edit / View toggle
const isEditing = ref(false)
const renderedBody = computed(() => {
  if (!editBody.value) return '<p style="color: var(--font-color-secondary);">Nothing to preview</p>'
  return marked.parse(editBody.value)
})

function toggleEdit() {
  isEditing.value = !isEditing.value
}

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
  isEditing.value = false
  highlightedIndex.value = notes.value.indexOf(note)
}

function selectSearchResult(sr) {
  selected.value = { id: sr.id, title: sr.title, parent_id: sr.parent_id,
    body: sr.body, created_at: sr.created_at, updated_at: sr.updated_at }
  editTitle.value = sr.title
  editBody.value = sr.body
  dirty.value = false
  saveError.value = ''
  showHistory.value = false
  history.value = []
  isEditing.value = false
  highlightedIndex.value = searchResults.value.indexOf(sr)
}

function newNote() {
  selected.value = { id: null, title: '', body: '' }
  editTitle.value = ''
  editBody.value = ''
  dirty.value = true
  saveError.value = ''
  showHistory.value = false
  history.value = []
  highlightedIndex.value = -1
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
  highlightedIndex.value = -1
}

async function doSearch() {
  const q = searchQuery.value.trim()
  if (!q) {
    searchResults.value = []
    highlightedIndex.value = -1
    return
  }
  searching.value = true
  try {
    searchResults.value = await searchNotes(props.token, q)
    highlightedIndex.value = searchResults.value.length > 0 ? 0 : -1
  } catch (e) {
    searchResults.value = []
    highlightedIndex.value = -1
  } finally {
    searching.value = false
  }
}

function relevancePct(distance) {
  if (distance == null) return ''
  const pct = Math.max(0, Math.round((1 - distance / 2) * 100))
  return pct + '% match'
}

// ── Keyboard shortcut handler ──
function onKeyDown(e) {
  const mod = isMac() ? e.metaKey : e.ctrlKey

  // Shift+? => toggle hotkey help
  if (e.shiftKey && e.key === '?') {
    e.preventDefault()
    showHotkeys.value = !showHotkeys.value
    return
  }

  // Esc => close modals / history / blur / clear highlight
  if (e.key === 'Escape') {
    if (showHotkeys.value) { showHotkeys.value = false; return }
    if (showDeleteModal.value) { showDeleteModal.value = false; return }
    if (showHistory.value) { showHistory.value = false; return }
    if (highlightedIndex.value >= 0) { highlightedIndex.value = -1; return }
    if (document.activeElement?.tagName === 'INPUT' || document.activeElement?.tagName === 'TEXTAREA') {
      document.activeElement.blur()
      return
    }
    return
  }

  // Ctrl/Cmd+N => new note
  if (mod && e.key === 'n') {
    e.preventDefault()
    newNote()
    return
  }

  // Ctrl/Cmd+S => save
  if (mod && e.key === 's') {
    e.preventDefault()
    if (dirty.value && selected.value) save()
    return
  }

  // Ctrl/Cmd+E => toggle edit/view
  if (mod && e.key === 'e') {
    e.preventDefault()
    if (selected.value) {
      toggleEdit()
      if (isEditing.value) {
        requestAnimationFrame(() => document.querySelector('.body-textarea')?.focus())
      }
    }
    return
  }

  // Ctrl/Cmd+H => toggle history
  if (mod && e.key === 'h') {
    e.preventDefault()
    if (selected.value?.id) toggleHistory()
    return
  }

  // Ctrl/Cmd+K => focus search
  if (mod && e.key === 'k') {
    e.preventDefault()
    const inp = document.querySelector('.search-input')
    if (inp) inp.focus()
    return
  }

  // Arrow Up / Down => navigate sidebar list
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    const list = sidebarList.value
    if (list.length === 0) return
    e.preventDefault()
    if (highlightedIndex.value < 0) {
      highlightedIndex.value = e.key === 'ArrowDown' ? 0 : list.length - 1
    } else if (e.key === 'ArrowDown') {
      highlightedIndex.value = (highlightedIndex.value + 1) % list.length
    } else {
      highlightedIndex.value = (highlightedIndex.value - 1 + list.length) % list.length
    }
    // Scroll highlighted item into view after DOM update
    requestAnimationFrame(() => {
      const el = document.querySelector('.note-item.highlighted')
      if (el) el.scrollIntoView({ block: 'nearest' })
    })
    return
  }

  // Enter => open highlighted note, or first result if focus is in search input
  // (Skip if the user is typing in the title or body editor)
  if (e.key === 'Enter') {
    const tag = document.activeElement?.tagName
    const inSearch = document.activeElement?.classList.contains('search-input')
    // Don't intercept Enter in editor inputs/textarea (title, body, etc.)
    if (!inSearch && (tag === 'INPUT' || tag === 'TEXTAREA')) return
    const idx = inSearch ? 0 : highlightedIndex.value
    if (idx >= 0 && idx < sidebarList.value.length) {
      e.preventDefault()
      const item = sidebarList.value[idx]
      if (searchQuery.value.trim()) {
        selectSearchResult(item)
      } else {
        selectNote(item)
      }
      if (inSearch) document.activeElement?.blur()
    }
    return
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown)
})
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

.note-item.highlighted {
  background: var(--raised-bg);
  border-left-color: var(--tag-bg-color);
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

/* Markdown rendered view */
.body-rendered {
  flex: 1;
  overflow-y: auto;
  padding: 1.25rem;
  line-height: 1.7;
  font-size: 0.95rem;
}

.markdown-body h1,
.markdown-body h2,
.markdown-body h3,
.markdown-body h4,
.markdown-body h5,
.markdown-body h6 {
  color: var(--header-title-color);
  margin: 1.3em 0 0.5em;
  line-height: 1.25;
}

.markdown-body h1 { font-size: 1.8rem; border-bottom: 1px solid var(--border-color); padding-bottom: 0.3em; }
.markdown-body h2 { font-size: 1.5rem; border-bottom: 1px solid var(--border-color); padding-bottom: 0.25em; }
.markdown-body h3 { font-size: 1.25rem; }
.markdown-body h4 { font-size: 1.1rem; }

.markdown-body p {
  margin: 0.6em 0;
}

.markdown-body a {
  color: var(--accent-teal);
  text-decoration: underline;
}

.markdown-body strong,
.markdown-body b {
  color: #fff;
  font-weight: 700;
}

.markdown-body em,
.markdown-body i {
  color: var(--font-color);
}

.markdown-body code {
  background: var(--raised-bg);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 0.15em 0.4em;
  font-size: 0.88em;
  color: var(--pre-text-color);
  font-family: 'Cascadia Code', 'Fira Code', 'JetBrains Mono', 'Consolas', monospace;
}

.markdown-body pre {
  background: var(--panel-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1rem;
  overflow-x: auto;
  margin: 0.8em 0;
}

.markdown-body pre code {
  background: none;
  border: none;
  padding: 0;
  font-size: 0.85rem;
  color: var(--pre-text-color);
}

.markdown-body blockquote {
  border-left: 3px solid var(--accent-teal);
  padding: 0.3em 0.8em;
  margin: 0.8em 0;
  color: var(--font-color-secondary);
  background: rgba(109, 148, 132, 0.08);
  border-radius: 0 6px 6px 0;
}

.markdown-body ul,
.markdown-body ol {
  padding-left: 1.5em;
  margin: 0.6em 0;
}

.markdown-body li {
  margin: 0.25em 0;
}

.markdown-body hr {
  border: none;
  border-top: 1px solid var(--border-color);
  margin: 1.5em 0;
}

.markdown-body table {
  border-collapse: collapse;
  width: 100%;
  margin: 0.8em 0;
}

.markdown-body th,
.markdown-body td {
  border: 1px solid var(--border-color);
  padding: 0.45em 0.75em;
  text-align: left;
}

.markdown-body th {
  background: var(--panel-bg);
  color: var(--header-title-color);
  font-weight: 600;
}

.markdown-body img {
  max-width: 100%;
  border-radius: 6px;
  margin: 0.6em 0;
}

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

/* Hotkey help modal */
.hotkey-modal {
  max-width: 420px;
}

.hotkey-modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.25rem;
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--header-title-color);
}

.hotkey-list {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
}

.hotkey-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.4rem 0;
  border-bottom: 1px solid rgba(26, 44, 61, 0.4);
}

.hotkey-row:last-child {
  border-bottom: none;
}

.hotkey-key {
  background: var(--raised-bg);
  border: 1px solid var(--border-color);
  border-radius: 5px;
  padding: 0.2rem 0.55rem;
  font-size: 0.78rem;
  font-family: 'Cascadia Code', 'Fira Code', 'JetBrains Mono', 'Consolas', monospace;
  color: var(--pre-text-color);
  white-space: nowrap;
}

.hotkey-desc {
  font-size: 0.85rem;
  color: var(--font-color-secondary);
}

/* "?" help button */
.help-btn {
  position: fixed;
  bottom: 1rem;
  right: 1rem;
  width: 2rem;
  height: 2rem;
  border-radius: 50%;
  background: var(--raised-bg);
  color: var(--font-color-secondary);
  border: 1px solid var(--border-color);
  font-size: 0.9rem;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  cursor: pointer;
}

.help-btn:hover {
  background: var(--accent-teal);
  color: #fff;
  border-color: var(--accent-teal);
}
</style>
