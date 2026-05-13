<template>
    <div class="layout">
        <!-- Sidebar -->
        <aside class="sidebar">
            <div class="sidebar-header">
                <img
                    src="../assets/MentisEterna_logo.svg"
                    alt="Logo"
                    class="app-logo"
                    title="Keyboard shortcuts (Shift+?)"
                    @click="showHotkeys = !showHotkeys"
                />
                <span class="app-title">MentisEterna</span>
                <button
                    class="btn-ghost icon-btn"
                    title="Logout"
                    @click="$emit('logout')"
                >
                    ⏻
                </button>
            </div>
            <button class="btn-amber new-btn" @click="newNote">
                + New Note
            </button>
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
                        :class="{
                            active: selected?.id === sr.id,
                            highlighted: highlightedIndex === idx,
                        }"
                        @click="selectSearchResult(sr)"
                    >
                        <span class="note-title">{{
                            sr.title || "Untitled"
                        }}</span>
                        <span class="note-date"
                            >{{ fmtDate(sr.updated_at) }} —
                            {{ relevancePct(sr.distance) }}</span
                        >
                    </div>
                    <div
                        v-if="searchResults.length === 0 && !searching"
                        class="empty-list"
                    >
                        No results
                    </div>
                </template>
                <!-- Standard list mode (root notes only) -->
                <template v-else>
                    <div
                        v-for="(note, idx) in rootNotes"
                        :key="note.id"
                        class="note-item"
                        :class="{
                            active: selected?.id === note.id,
                            highlighted: highlightedIndex === idx,
                        }"
                        @click="selectNote(note)"
                    >
                        <span class="note-title">{{
                            note.title || "Untitled"
                        }}</span>
                        <span class="note-date">{{
                            fmtDate(note.updated_at)
                        }}</span>
                        <span
                            v-if="note.pinned"
                            class="pin-indicator"
                            title="Pinned"
                        >
                            📌
                        </span>
                    </div>
                    <div
                        v-if="rootNotes.length === 0 && !loading"
                        class="empty-list"
                    >
                        No notes yet
                    </div>
                </template>
                <div v-if="loading || searching" class="empty-list">
                    Loading…
                </div>
            </div>
            <div class="sidebar-footer">
                <JobQueue :token="token" @job-done="onJobDone" />
                <button
                    class="btn-ghost backup-btn"
                    :disabled="backingUp"
                    title="Back up the database now"
                    @click="triggerBackup"
                >
                    {{ backingUp ? "Backing up…" : "Backup Now" }}
                </button>
                <p v-if="backupErr" class="reg-error">
                    {{ backupErr }}
                </p>
                <p v-if="backupOk" class="reg-ok">{{ backupOk }}</p>
                <button
                    class="btn-ghost passkey-btn"
                    :disabled="registeringPasskey"
                    @click="registerPasskey"
                >
                    &#128273;
                    {{
                        registeringPasskey ? "Registering…" : "Register Passkey"
                    }}
                </button>
                <p v-if="regPasskeyErr" class="reg-error">
                    {{ regPasskeyErr }}
                </p>
                <p v-if="regPasskeyOk" class="reg-ok">Passkey registered.</p>
            </div>
        </aside>

        <!-- Editor / Chat Pane -->
        <main class="editor-pane">
            <template v-if="selected">
                <!-- Header bar -->
                <div class="editor-header">
                    <div class="editor-header-left">
                        <input
                            v-if="isEditing"
                            v-model="editTitle"
                            class="title-input"
                            placeholder="Note title (leave blank to auto-generate)"
                            @input="dirty = true"
                        />
                        <h2 v-else class="title-display">
                            {{ selected.title || "Untitled" }}
                        </h2>
                        <div v-if="isEditing" class="type-row">
                            <span class="parent-label">Type:</span>
                            <select
                                v-model="noteType"
                                class="type-select"
                                @change="onTypeChange()"
                            >
                                <option
                                    v-for="opt in typeOptions"
                                    :key="opt.value"
                                    :value="opt.value"
                                >
                                    {{ opt.label }}
                                </option>
                            </select>
                        </div>
                        <div v-if="isEditing" class="tag-row">
                            <span class="parent-label">Tags:</span>
                            <div class="tag-list">
                                <span
                                    v-for="(tag, i) in editTags"
                                    :key="i"
                                    class="tag-chip"
                                >
                                    {{ tag }}
                                    <button
                                        class="tag-remove"
                                        @click="
                                            editTags.splice(i, 1);
                                            dirty = true;
                                        "
                                        title="Remove tag"
                                    >
                                        ×
                                    </button>
                                </span>
                                <div class="tag-input-wrapper">
                                    <input
                                        v-model="tagSearch"
                                        class="tag-input"
                                        placeholder="Add tag…"
                                        @input="onTagInput()"
                                        @keydown.enter.prevent="
                                            addTagFromSearch()
                                        "
                                        @keydown.backspace="onTagBackspace()"
                                        @keydown.escape="tagOptions = []"
                                        @focus="onTagInput()"
                                    />
                                    <div
                                        v-if="tagOptions.length > 0"
                                        class="tag-dropdown"
                                    >
                                        <div
                                            v-for="(opt, i) in tagOptions"
                                            :key="opt"
                                            class="tag-dropdown-item"
                                            @click="addTag(opt)"
                                            :ref="
                                                (el) => {
                                                    if (el) el._tagIndex = i;
                                                }
                                            "
                                        >
                                            {{ opt }}
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </div>
                        <div v-if="isEditing" class="parent-row">
                            <span class="parent-label">Parent:</span>
                            <div class="parent-picker-wrapper">
                                <input
                                    v-model="parentSearch"
                                    class="parent-input"
                                    placeholder="Search parent…"
                                    @focus="showParentPicker = true"
                                    @input="onParentSearchInput()"
                                />
                                <button
                                    v-if="selected.parent_id"
                                    class="btn-ghost parent-clear-btn"
                                    title="Remove parent"
                                    @click="clearParent()"
                                >
                                    ✕
                                </button>
                                <div
                                    v-if="
                                        showParentPicker &&
                                        (parentOptions.length > 0 ||
                                            parentSearching)
                                    "
                                    class="parent-dropdown"
                                >
                                    <div
                                        v-if="parentSearching"
                                        class="parent-dropdown-item muted"
                                    >
                                        Searching…
                                    </div>
                                    <div
                                        v-for="opt in parentOptions"
                                        :key="opt.id"
                                        class="parent-dropdown-item"
                                        @click="selectParent(opt)"
                                    >
                                        {{ opt.title }}
                                    </div>
                                </div>
                            </div>
                        </div>
                        <div v-if="ancestors.length" class="breadcrumb-trail">
                            <span
                                v-for="(anc, i) in ancestors"
                                :key="anc.id"
                                class="breadcrumb-seg"
                                :class="{
                                    'breadcrumb-current':
                                        i === ancestors.length - 1,
                                }"
                                @click="selectBreadcrumb(anc)"
                                >{{ anc.title || "Untitled"
                                }}<span
                                    v-if="i < ancestors.length - 1"
                                    class="breadcrumb-colon"
                                    >:</span
                                ></span
                            >
                        </div>
                    </div>
                    <div class="editor-actions">
                        <button class="btn-ghost" @click="toggleEdit">
                            {{ isEditing ? "🖉 View" : "✎ Edit" }}
                        </button>
                        <button
                            class="btn-amber btn-child"
                            @click="newChildNote"
                        >
                            + Child
                        </button>
                        <button
                            class="btn-ghost"
                            @click="onAttachFile"
                            :disabled="!selected?.id"
                        >
                            📎 Attach
                        </button>
                        <button
                            class="btn-primary"
                            :disabled="!dirty || saving"
                            @click="save"
                        >
                            {{ saving ? "Saving…" : "Save" }}
                        </button>
                        <button
                            class="btn-ghost"
                            :class="{ active: showHistory }"
                            @click="toggleHistory"
                        >
                            History
                        </button>
                        <button
                            class="btn-ghost pin-editor-btn"
                            :class="{ pinned: selected?.pinned }"
                            :title="
                                selected?.pinned ? 'Unpin note' : 'Pin note'
                            "
                            @click="togglePin(selected)"
                        >
                            📌
                        </button>
                        <button class="btn-danger" @click="confirmDelete">
                            Delete
                        </button>
                    </div>
                </div>
                <p v-if="saveError" class="save-error">{{ saveError }}</p>

                <!-- Chat Feed -->
                <div class="chat-feed">
                    <!-- Root message: the selected note -->
                    <div class="chat-message chat-message-root">
                        <div class="message-meta">
                            <span class="message-author">{{
                                selected.title || "Untitled"
                            }}</span>
                            <span class="message-date">{{
                                fmtDateFull(selected.created_at)
                            }}</span>
                            <span class="message-badge">root</span>
                        </div>
                        <div class="message-body">
                            <div v-if="isEditing" class="body-textarea-wrapper">
                                <textarea
                                    ref="bodyTextarea"
                                    v-model="editBody"
                                    class="body-textarea"
                                    placeholder="Write your note here… (drag files here)"
                                    @input="onLinkEditorInput('body')"
                                    @click="
                                        onLinkEditorCaretMove('body', $event)
                                    "
                                    @keyup="
                                        onLinkEditorCaretMove('body', $event)
                                    "
                                    @scroll="onLinkEditorScroll('body')"
                                    @dragover.prevent
                                    @drop.prevent="onBodyDrop"
                                />
                                <!-- [[ Link search popup -->
                                <div
                                    v-if="
                                        linkSearchVisible &&
                                        linkSearchTarget === 'body'
                                    "
                                    class="link-search-popup"
                                    :style="linkPopupStyle"
                                >
                                    <div
                                        v-if="
                                            !linkSearching &&
                                            !linkSearchQuery.trim()
                                        "
                                        class="link-search-status"
                                    >
                                        Start typing to search notes…
                                    </div>
                                    <div
                                        v-else-if="linkSearching"
                                        class="link-search-status"
                                    >
                                        Searching…
                                    </div>
                                    <div
                                        v-for="(r, idx) in linkSearchResults"
                                        :key="r.id"
                                        class="link-search-item"
                                        :class="{
                                            highlighted:
                                                idx === linkSearchIndex,
                                        }"
                                        :style="
                                            idx === linkSearchIndex
                                                ? {
                                                      background: '#2f2000',
                                                      color: '#fff',
                                                      boxShadow:
                                                          'inset 4px 0 0 #ffb400',
                                                      outline:
                                                          '1px solid rgba(255, 180, 0, 0.35)',
                                                  }
                                                : null
                                        "
                                        @click="selectLinkResult(r)"
                                        @mouseenter="linkSearchIndex = idx"
                                    >
                                        <span
                                            class="link-search-title"
                                            :style="
                                                idx === linkSearchIndex
                                                    ? {
                                                          fontWeight: '700',
                                                      }
                                                    : null
                                            "
                                            >{{ r.title || "Untitled" }}</span
                                        >
                                        <span
                                            class="link-search-relevance"
                                            :style="
                                                idx === linkSearchIndex
                                                    ? {
                                                          color: 'rgba(255, 255, 255, 0.92)',
                                                      }
                                                    : null
                                            "
                                            >{{
                                                relevancePct(r.distance)
                                            }}</span
                                        >
                                    </div>
                                    <div
                                        v-if="
                                            !linkSearching &&
                                            linkSearchQuery.trim() &&
                                            linkSearchResults.length === 0
                                        "
                                        class="link-search-status"
                                    >
                                        No notes found
                                    </div>
                                </div>
                            </div>
                            <div
                                v-else
                                class="body-rendered markdown-body"
                                v-html="renderedBody"
                            />
                        </div>
                        <NoteTypeRenderer
                            v-if="selected"
                            :note="selected"
                            :token="token"
                            :editing="isEditing"
                            :customData="customData"
                            :uiSchema="
                                selected.ui_schema || selected.plugin?.view
                            "
                            @selectNote="(id) => selectNoteById(id)"
                            @update:custom-data="
                                (d) => {
                                    customData = d;
                                    dirty = true;
                                }
                            "
                        />
                        <NoteAttachments
                            :attachments="selected.attachments"
                            :editing="isEditing"
                            :token="token"
                            @remove="removeAttachment"
                        />
                    </div>

                    <!-- Child messages (direct children of the selected note) -->
                    <div
                        v-for="child in children"
                        :key="child.id"
                        class="chat-message chat-message-child"
                    >
                        <div class="message-meta">
                            <span class="message-author">{{
                                child.title || "Untitled"
                            }}</span>
                            <span class="message-date">{{
                                fmtDateFull(child.created_at)
                            }}</span>
                        </div>
                        <div
                            class="message-body markdown-body"
                            v-html="renderMarkdown(child.body)"
                        />
                        <div class="message-actions">
                            <button
                                class="btn-ghost btn-thread"
                                @click="selectNoteFromChild(child)"
                            >
                                → ({{ child.child_count ?? 0 }})
                            </button>
                        </div>
                    </div>

                    <!-- Children loading / empty state -->
                    <div v-if="childrenLoading" class="chat-status">
                        Loading replies…
                    </div>
                    <div
                        v-else-if="children.length === 0 && selected.id"
                        class="chat-status"
                    >
                        No replies yet
                    </div>

                    <!-- History section (inline toggle) -->
                    <div v-if="showHistory" class="chat-history-section">
                        <div class="history-header">
                            <span>Edit History</span>
                            <button
                                class="btn-ghost icon-btn"
                                @click="showHistory = false"
                            >
                                ✕
                            </button>
                        </div>
                        <div v-if="historyLoading" class="history-empty">
                            Loading…
                        </div>
                        <div
                            v-else-if="history.length === 0"
                            class="history-empty"
                        >
                            No history yet
                        </div>
                        <div
                            v-else
                            v-for="entry in history"
                            :key="entry.id"
                            class="history-entry"
                            @click="restoreBody(entry.body)"
                        >
                            <span class="history-date">{{
                                fmtDateFull(entry.created_at)
                            }}</span>
                            <pre class="history-preview"
                                >{{ entry.body.slice(0, 120)
                                }}{{ entry.body.length > 120 ? "…" : "" }}</pre
                            >
                        </div>
                    </div>
                </div>

                <!-- Chat Composer (quick reply) -->
                <div class="chat-composer">
                    <input
                        v-model="newReplyTitle"
                        class="composer-title"
                        placeholder="Reply title (optional — auto-generated if blank)"
                        @keydown.enter.exact="sendReply"
                    />
                    <div class="composer-body-row">
                        <textarea
                            ref="newReplyTextarea"
                            v-model="newReplyBody"
                            class="composer-textarea"
                            placeholder="Write a reply…"
                            rows="2"
                            @input="onLinkEditorInput('reply')"
                            @click="onLinkEditorCaretMove('reply', $event)"
                            @keyup="onLinkEditorCaretMove('reply', $event)"
                            @scroll="onLinkEditorScroll('reply')"
                            @keydown.enter.meta.exact="sendReply"
                            @keydown.enter.ctrl.exact="sendReply"
                        />
                        <button
                            class="btn-primary composer-send"
                            :disabled="sendingReply"
                            @click="sendReply"
                        >
                            {{ sendingReply ? "…" : "Send" }}
                        </button>
                        <div
                            v-if="
                                linkSearchVisible &&
                                linkSearchTarget === 'reply'
                            "
                            class="link-search-popup"
                            :style="linkPopupStyle"
                        >
                            <div
                                v-if="!linkSearching && !linkSearchQuery.trim()"
                                class="link-search-status"
                            >
                                Start typing to search notes…
                            </div>
                            <div
                                v-else-if="linkSearching"
                                class="link-search-status"
                            >
                                Searching…
                            </div>
                            <div
                                v-for="(r, idx) in linkSearchResults"
                                :key="r.id"
                                class="link-search-item"
                                :class="{
                                    highlighted: idx === linkSearchIndex,
                                }"
                                :style="
                                    idx === linkSearchIndex
                                        ? {
                                              background: '#2f2000',
                                              color: '#fff',
                                              boxShadow:
                                                  'inset 4px 0 0 #ffb400',
                                              outline:
                                                  '1px solid rgba(255, 180, 0, 0.35)',
                                          }
                                        : null
                                "
                                @click="selectLinkResult(r)"
                                @mouseenter="linkSearchIndex = idx"
                            >
                                <span
                                    class="link-search-title"
                                    :style="
                                        idx === linkSearchIndex
                                            ? {
                                                  fontWeight: '700',
                                              }
                                            : null
                                    "
                                    >{{ r.title || "Untitled" }}</span
                                >
                                <span
                                    class="link-search-relevance"
                                    :style="
                                        idx === linkSearchIndex
                                            ? {
                                                  color: 'rgba(255, 255, 255, 0.92)',
                                              }
                                            : null
                                    "
                                    >{{ relevancePct(r.distance) }}</span
                                >
                            </div>
                            <div
                                v-if="
                                    !linkSearching &&
                                    linkSearchQuery.trim() &&
                                    linkSearchResults.length === 0
                                "
                                class="link-search-status"
                            >
                                No notes found
                            </div>
                        </div>
                    </div>
                </div>
            </template>
            <div v-else class="no-selection">
                <p>Select a note or create a new one</p>
            </div>
        </main>

        <!-- Thread Sidebar (right) -->
        <aside v-if="threadNote" class="thread-sidebar">
            <div class="thread-sidebar-header">
                <button
                    class="btn-ghost icon-btn"
                    @click="closeThreadSidebar"
                    title="Close thread"
                >
                    ✕
                </button>
                <span class="thread-sidebar-title">Thread</span>
                <button
                    class="btn-ghost icon-btn"
                    @click="selectNote(threadNote)"
                    title="Open full view"
                >
                    ⤢
                </button>
            </div>
            <!-- Breadcrumb -->
            <div v-if="threadAncestors.length" class="thread-breadcrumb">
                <span
                    v-for="(anc, i) in threadAncestors"
                    :key="anc.id"
                    class="breadcrumb-seg"
                    :class="{
                        'breadcrumb-current': i === threadAncestors.length - 1,
                    }"
                    @click="openThreadSidebar(anc)"
                    >{{ anc.title || "Untitled"
                    }}<span
                        v-if="i < threadAncestors.length - 1"
                        class="breadcrumb-colon"
                        >:</span
                    ></span
                >
            </div>
            <!-- Chat feed (same structure as main) -->
            <div class="chat-feed">
                <!-- Root message: the thread note -->
                <div class="chat-message chat-message-root">
                    <div class="message-meta">
                        <span class="message-author">{{
                            threadNote.title || "Untitled"
                        }}</span>
                        <span class="message-date">{{
                            fmtDateFull(threadNote.created_at)
                        }}</span>
                        <span class="message-badge">root</span>
                    </div>
                    <div
                        class="message-body markdown-body"
                        v-html="renderMarkdown(threadNote.body)"
                    />
                    <NoteTypeRenderer
                        :note="threadNote"
                        :token="token"
                        :editing="false"
                        :customData="
                            threadNote.plugin?.config || threadNote.custom_data
                        "
                        :uiSchema="
                            threadNote.ui_schema || threadNote.plugin?.view
                        "
                        @selectNote="(id) => selectNoteById(id)"
                    />
                    <NoteAttachments
                        :attachments="threadNote.attachments"
                        :editing="false"
                        :token="token"
                    />
                </div>

                <!-- Child messages of the thread note -->
                <div
                    v-for="tc in threadChildren"
                    :key="tc.id"
                    class="chat-message chat-message-child"
                >
                    <div class="message-meta">
                        <span class="message-author">{{
                            tc.title || "Untitled"
                        }}</span>
                        <span class="message-date">{{
                            fmtDateFull(tc.created_at)
                        }}</span>
                    </div>
                    <div
                        class="message-body markdown-body"
                        v-html="renderMarkdown(tc.body)"
                    />
                    <div class="message-actions">
                        <button
                            class="btn-ghost btn-thread"
                            @click="selectThreadChild(tc)"
                        >
                            → ({{ tc.child_count ?? 0 }})
                        </button>
                    </div>
                </div>

                <div v-if="threadChildrenLoading" class="chat-status">
                    Loading replies…
                </div>
                <div
                    v-else-if="threadChildren.length === 0"
                    class="chat-status"
                >
                    No replies yet
                </div>
            </div>
            <!-- Thread composer -->
            <div class="thread-composer">
                <input
                    v-model="threadReplyTitle"
                    class="composer-title"
                    placeholder="Reply title (optional — auto-generated if blank)"
                />
                <div class="composer-body-row">
                    <textarea
                        ref="threadReplyTextarea"
                        v-model="threadReplyBody"
                        class="composer-textarea"
                        placeholder="Write a reply…"
                        rows="2"
                        @input="onLinkEditorInput('threadReply')"
                        @click="onLinkEditorCaretMove('threadReply', $event)"
                        @keyup="onLinkEditorCaretMove('threadReply', $event)"
                        @scroll="onLinkEditorScroll('threadReply')"
                    />
                    <button
                        class="btn-primary composer-send"
                        :disabled="threadSendingReply"
                        @click="sendThreadReply"
                    >
                        {{ threadSendingReply ? "…" : "Send" }}
                    </button>
                    <div
                        v-if="
                            linkSearchVisible &&
                            linkSearchTarget === 'threadReply'
                        "
                        class="link-search-popup"
                        :style="linkPopupStyle"
                    >
                        <div
                            v-if="!linkSearching && !linkSearchQuery.trim()"
                            class="link-search-status"
                        >
                            Start typing to search notes…
                        </div>
                        <div
                            v-else-if="linkSearching"
                            class="link-search-status"
                        >
                            Searching…
                        </div>
                        <div
                            v-for="(r, idx) in linkSearchResults"
                            :key="r.id"
                            class="link-search-item"
                            :class="{ highlighted: idx === linkSearchIndex }"
                            :style="
                                idx === linkSearchIndex
                                    ? {
                                          background: '#2f2000',
                                          color: '#fff',
                                          boxShadow: 'inset 4px 0 0 #ffb400',
                                          outline:
                                              '1px solid rgba(255, 180, 0, 0.35)',
                                      }
                                    : null
                            "
                            @click="selectLinkResult(r)"
                            @mouseenter="linkSearchIndex = idx"
                        >
                            <span
                                class="link-search-title"
                                :style="
                                    idx === linkSearchIndex
                                        ? {
                                              fontWeight: '700',
                                          }
                                        : null
                                "
                                >{{ r.title || "Untitled" }}</span
                            >
                            <span
                                class="link-search-relevance"
                                :style="
                                    idx === linkSearchIndex
                                        ? {
                                              color: 'rgba(255, 255, 255, 0.92)',
                                          }
                                        : null
                                "
                                >{{ relevancePct(r.distance) }}</span
                            >
                        </div>
                        <div
                            v-if="
                                !linkSearching &&
                                linkSearchQuery.trim() &&
                                linkSearchResults.length === 0
                            "
                            class="link-search-status"
                        >
                            No notes found
                        </div>
                    </div>
                </div>
            </div>
        </aside>

        <!-- Delete confirm modal -->
        <div
            v-if="showDeleteModal"
            class="modal-overlay"
            @click.self="showDeleteModal = false"
        >
            <div class="modal">
                <p>
                    Delete <strong>{{ selected?.title || "this note" }}</strong
                    >?
                </p>
                <div class="modal-actions">
                    <button class="btn-ghost" @click="showDeleteModal = false">
                        Cancel
                    </button>
                    <button
                        class="btn-danger"
                        :disabled="deleting"
                        @click="doDelete"
                    >
                        {{ deleting ? "Deleting…" : "Delete" }}
                    </button>
                </div>
            </div>
        </div>

        <!-- Hotkey help modal -->
        <div
            v-if="showHotkeys"
            class="modal-overlay"
            @click.self="showHotkeys = false"
        >
            <div class="modal hotkey-modal">
                <div class="hotkey-modal-header">
                    <span>Keyboard Shortcuts</span>
                    <button
                        class="btn-ghost icon-btn"
                        @click="showHotkeys = false"
                    >
                        ✕
                    </button>
                </div>
                <div class="hotkey-list">
                    <div v-for="hk in hotkeys" :key="hk.key" class="hotkey-row">
                        <kbd class="hotkey-key">{{ hk.key }}</kbd>
                        <span class="hotkey-desc">{{ hk.desc }}</span>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import MarkdownIt from "markdown-it";
import markdownItFootnote from "markdown-it-footnote";
const md = new MarkdownIt({ html: false, linkify: true, breaks: true }).use(
    markdownItFootnote,
);
import {
    fetchNotes,
    fetchNote,
    createNote,
    updateNote,
    deleteNote,
    fetchNoteHistory,
    fetchChildren,
    fetchAncestors,
    searchNotes,
    setNotePin,
    fetchTags,
    beginPasskeyRegistration,
    triggerBackup as apiTriggerBackup,
} from "../api.js";
import NoteTypeRenderer from "../components/NoteTypeRenderer.vue";
import NoteAttachments from "../components/NoteAttachments.vue";
import JobQueue from "../components/JobQueue.vue";
import {
    uploadAttachment,
    uploadInlineFile,
    deleteAttachment,
} from "../api.js";
import {
    getTypeOptions,
    getNoteTypeOrDefault,
    fetchAndMergeManifests,
} from "../note-types/registry.js";

const props = defineProps({ token: String });
const emit = defineEmits(["logout"]);

const notes = ref([]);
const loading = ref(false);
const selected = ref(null);
const editTitle = ref("");
const editBody = ref("");
const noteType = ref("standard");
const customData = ref(null);
const dirty = ref(false);
const saving = ref(false);
const bodyTextarea = ref(null);
const newReplyTextarea = ref(null);
const threadReplyTextarea = ref(null);

// Tags state
const editTags = ref([]);
const tagSearch = ref("");
const tagOptions = ref([]);

function insertAtCursor(text) {
    const el = bodyTextarea.value;
    if (!el) return;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    editBody.value =
        editBody.value.slice(0, start) + text + editBody.value.slice(end);
    requestAnimationFrame(() => {
        el.focus();
        const pos = start + text.length;
        el.setSelectionRange(pos, pos);
    });
    dirty.value = true;
}

// Note-type options from the registry (single source of truth)
const typeOptions = getTypeOptions();
const saveError = ref("");
const showDeleteModal = ref(false);
const deleting = ref(false);
const showHistory = ref(false);
const showHotkeys = ref(false);
const history = ref([]);
const historyLoading = ref(false);

// Search state
const searchQuery = ref("");
const searchResults = ref([]);
const searching = ref(false);
const highlightedIndex = ref(-1);
let searchTimeout = null;

// [[ Link search state
const linkSearchQuery = ref("");
const linkSearchResults = ref([]);
const linkSearching = ref(false);
const linkSearchIndex = ref(-1);
const linkSearchVisible = ref(false);
const linkSearchTarget = ref(null);
const linkPopupStyle = ref({ left: "20px", top: "20px" });
let linkSearchTimeout = null;

function getLinkSearchContext(target = linkSearchTarget.value) {
    switch (target) {
        case "body":
            return {
                textarea: bodyTextarea,
                text: editBody,
                onChange: () => {
                    dirty.value = true;
                },
            };
        case "reply":
            return {
                textarea: newReplyTextarea,
                text: newReplyBody,
            };
        case "threadReply":
            return {
                textarea: threadReplyTextarea,
                text: threadReplyBody,
            };
        default:
            return null;
    }
}

// Children state
const children = ref([]);
const childrenLoading = ref(false);

// Thread sidebar state
const threadNote = ref(null); // the note whose thread is shown in the right sidebar
const threadChildren = ref([]);
const threadChildrenLoading = ref(false);
const threadAncestors = ref([]);
const threadReplyTitle = ref("");
const threadReplyBody = ref("");
const threadSendingReply = ref(false);

// Reply composer state
const newReplyTitle = ref("");
const newReplyBody = ref("");
const sendingReply = ref(false);

// Parent selector state
const parentSearch = ref("");
const parentOptions = ref([]);

// Passkey registration state
const registeringPasskey = ref(false);
const regPasskeyErr = ref("");
const regPasskeyOk = ref(false);

// Backup state
const backingUp = ref(false);
const backupErr = ref("");
const backupOk = ref("");
const ancestors = ref([]);
const parentSearching = ref(false);
const showParentPicker = ref(false);
let parentSearchTimeout = null;

// Root notes (no parent_id) — shown in the sidebar
const rootNotes = computed(() =>
    notes.value.filter((n) => n.parent_id == null),
);

// The list currently shown in the sidebar (search results or root notes)
const sidebarList = computed(() =>
    searchQuery.value.trim() ? searchResults.value : rootNotes.value,
);

// Hotkeys definition
const hotkeys = [
    { key: isMac() ? "⌘ N" : "Ctrl+N", desc: "New note" },
    { key: isMac() ? "⌘ S" : "Ctrl+S", desc: "Save note" },
    { key: isMac() ? "⌘ E" : "Ctrl+E", desc: "Toggle edit / preview" },
    { key: isMac() ? "⌘ H" : "Ctrl+H", desc: "Toggle history panel" },
    { key: isMac() ? "⌘ K" : "Ctrl+K", desc: "Focus search bar" },
    { key: "↑ ↓", desc: "Navigate list / search results" },
    { key: "Enter", desc: "Open highlighted note" },
    { key: "Esc", desc: "Close panel / blur / clear highlight" },
    { key: "Shift+?", desc: "Toggle this help dialog" },
];

function isMac() {
    return /Mac|iPod|iPhone|iPad/.test(
        navigator.platform || navigator.userAgentData?.platform || "",
    );
}

async function registerPasskey() {
    regPasskeyErr.value = "";
    regPasskeyOk.value = false;
    registeringPasskey.value = true;
    try {
        await beginPasskeyRegistration(props.token);
        regPasskeyOk.value = true;
    } catch (e) {
        if (e.name === "NotAllowedError" || e.message?.includes("NotAllowed")) {
            regPasskeyErr.value = "Cancelled.";
        } else {
            regPasskeyErr.value = e.message || "Registration failed";
        }
    } finally {
        registeringPasskey.value = false;
    }
}

async function triggerBackup() {
    backupErr.value = "";
    backupOk.value = "";
    backingUp.value = true;
    try {
        const res = await apiTriggerBackup(props.token);
        backupOk.value = `Backup queued (run #${res.run_id}). Check the job queue for progress.`;
        setTimeout(() => {
            backupOk.value = "";
        }, 8000);
    } catch (e) {
        backupErr.value = e.message || "Backup failed";
    } finally {
        backingUp.value = false;
    }
}

// Edit / View toggle
const isEditing = ref(false);
const renderedBody = computed(() => {
    if (!editBody.value)
        return '<p style="color: var(--font-color-secondary);">Nothing to preview</p>';
    return md.render(editBody.value);
});

// Render any markdown body (used for child messages)
function renderMarkdown(body) {
    if (!body)
        return '<p style="color: var(--font-color-secondary);">Empty</p>';
    return md.render(body);
}

function toggleEdit() {
    isEditing.value = !isEditing.value;
}

function onTypeChange() {
    const def = getNoteTypeOrDefault(noteType.value);
    customData.value = def.emptyCustomData();
    dirty.value = true;
}

async function ensureSelectedNoteSaved() {
    if (!selected.value?.id) await save();
    if (!selected.value?.id)
        throw new Error("Save the note before uploading files");
}

async function togglePin(note) {
    if (!note?.id) return;
    const newPinned = !note.pinned;
    try {
        await setNotePin(props.token, note.id, newPinned);
        // Reload the note list so sort order (pinned first) is correct.
        await loadNotes();
        // Update selected note pinned state.
        if (selected.value?.id === note.id) {
            selected.value.pinned = newPinned;
        }
    } catch (e) {
        saveError.value = e.message;
    }
}

onMounted(loadNotes);
onMounted(() => fetchAndMergeManifests(props.token));

async function loadNotes() {
    loading.value = true;
    try {
        notes.value = await fetchNotes(props.token);
    } finally {
        loading.value = false;
    }
}

async function selectNote(note) {
    threadNote.value = null;
    // Re-fetch from server to get full enriched data (plugin.config, plugin.view, etc.)
    try {
        const full = await fetchNote(props.token, note.id);
        selected.value = full;
        editTitle.value = full.title;
        editBody.value = full.body;
        noteType.value = full.type || "standard";
        customData.value = full.plugin?.config || full.custom_data || null;
        editTags.value = full.tags || [];
    } catch {
        // Fallback to the sidebar data if fetch fails.
        selected.value = note;
        editTitle.value = note.title;
        editBody.value = note.body;
        noteType.value = note.type || "standard";
        customData.value = note.plugin?.config || note.custom_data || null;
        editTags.value = note.tags || [];
    }
    dirty.value = false;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    isEditing.value = false;
    highlightedIndex.value = rootNotes.value.indexOf(note);
    loadChildren(note.id);
    populateParentSearch(note);
    await loadAncestors(note.id);
    pushURL();
}

async function selectSearchResult(sr) {
    threadNote.value = null;
    // Re-fetch full note for proper hydration.
    try {
        const full = await fetchNote(props.token, sr.id);
        selected.value = full;
        editTitle.value = full.title;
        editBody.value = full.body;
        noteType.value = full.type || "standard";
        customData.value = full.plugin?.config || full.custom_data || null;
        editTags.value = full.tags || [];
    } catch {
        selected.value = {
            id: sr.id,
            title: sr.title,
            parent_id: sr.parent_id,
            type: sr.type || "standard",
            pinned: sr.pinned || false,
            body: sr.body,
            created_at: sr.created_at,
            updated_at: sr.updated_at,
        };
        editTitle.value = sr.title;
        editBody.value = sr.body;
        noteType.value = sr.type || "standard";
        customData.value = null;
        editTags.value = sr.tags || [];
    }
    dirty.value = false;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    isEditing.value = false;
    highlightedIndex.value = searchResults.value.indexOf(sr);
    loadChildren(sr.id);
    populateParentSearch(selected.value);
    await loadAncestors(sr.id);
    pushURL();
}

function populateParentSearch(note) {
    if (note?.parent_id) {
        const p = notes.value.find((n) => n.id === note.parent_id);
        parentSearch.value = p ? p.title : "";
    } else {
        parentSearch.value = "";
        ancestors.value = [];
    }
}

function newNote(parentNote = null) {
    threadNote.value = null;
    selected.value = {
        id: null,
        title: "",
        body: "",
        type: "standard",
        parent_id: parentNote ? parentNote.id : null,
    };
    editTitle.value = "";
    editBody.value = "";
    noteType.value = "standard";
    const typeDef = getNoteTypeOrDefault("standard");
    customData.value = typeDef.emptyCustomData();
    editTags.value = [];
    dirty.value = true;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    highlightedIndex.value = -1;
    children.value = [];
    parentSearch.value = "";
    ancestors.value = [];
    isEditing.value = true;
    if (parentNote) {
        parentSearch.value = parentNote.title || "";
    }
    pushURL();
    requestAnimationFrame(() =>
        document.querySelector(".body-textarea")?.focus(),
    );
}

function newChildNote() {
    if (!selected.value?.id) return;
    newNote(selected.value);
    isEditing.value = true;
    requestAnimationFrame(() =>
        document.querySelector(".body-textarea")?.focus(),
    );
}

function confirmDelete() {
    showDeleteModal.value = true;
}

async function doDelete() {
    deleting.value = true;
    try {
        await deleteNote(props.token, selected.value.id);
        notes.value = notes.value.filter((n) => n.id !== selected.value.id);
        selected.value = null;
        threadNote.value = null;
        showDeleteModal.value = false;
        pushURL();
    } finally {
        deleting.value = false;
    }
}

async function toggleHistory() {
    if (!selected.value?.id) return;
    showHistory.value = !showHistory.value;
    if (showHistory.value && history.value.length === 0) {
        historyLoading.value = true;
        try {
            history.value = await fetchNoteHistory(
                props.token,
                selected.value.id,
            );
        } finally {
            historyLoading.value = false;
        }
    }
}

async function loadChildren(noteId) {
    if (!noteId) {
        children.value = [];
        return;
    }
    childrenLoading.value = true;
    try {
        children.value = await fetchChildren(props.token, noteId);
    } catch {
        children.value = [];
    } finally {
        childrenLoading.value = false;
    }
}

function onParentSearchInput() {
    clearTimeout(parentSearchTimeout);
    parentSearchTimeout = setTimeout(doParentSearch, 200);
}

async function doParentSearch() {
    const q = parentSearch.value.trim();
    if (!q) {
        parentOptions.value = [];
        return;
    }
    parentSearching.value = true;
    try {
        const results = await searchNotes(props.token, q);
        // Filter out the current note so it can't be its own parent
        parentOptions.value = results
            .filter((r) => r.id !== selected.value?.id)
            .slice(0, 8);
    } catch {
        parentOptions.value = [];
    } finally {
        parentSearching.value = false;
    }
}

function selectParent(note) {
    selected.value = { ...selected.value, parent_id: note.id };
    parentSearch.value = note.title;
    parentOptions.value = [];
    showParentPicker.value = false;
    dirty.value = true;
}

function clearParent() {
    selected.value = { ...selected.value, parent_id: null };
    parentSearch.value = "";
    ancestors.value = [];
    parentOptions.value = [];
    dirty.value = true;
}

async function loadAncestors(noteId) {
    if (!noteId) {
        ancestors.value = [];
        return;
    }
    try {
        ancestors.value = await fetchAncestors(props.token, noteId);
    } catch {
        ancestors.value = [];
    }
}

function selectBreadcrumb(anc) {
    if (anc.id === selected.value?.id) return;
    selectNote(anc);
}

// Child path: breadcrumb-based path for a child note
function childPath(child) {
    const chain = ancestors.value;
    const titles = chain.map((n) => n.title || "Untitled");
    titles.push(child.title || "Untitled");
    return titles.join(":");
}

function selectNoteFromChild(child) {
    // On mobile / narrow screens, navigate into the note directly
    if (window.innerWidth < 768) {
        selectNote(child);
        return;
    }
    // Otherwise open the thread in the right sidebar
    openThreadSidebar(child);
}

// openNoteInThreadById is called from NoteTypeRenderer (e.g., recipe overview cards)
// when the user clicks a linked note. It opens the note in the thread sidebar.
async function selectNoteById(id) {
    // Try to find the note in our loaded list first.
    let note = notes.value.find((n) => n.id === id);
    if (!note) {
        // Fetch fresh from the server.
        try {
            note = await fetchNote(props.token, id);
        } catch {
            return;
        }
    }
    // Open in the thread sidebar (right panel) instead of replacing the main editor.
    openThreadSidebar(note);
}

async function openThreadSidebar(note) {
    // Fetch the full enriched note so plugin data is available for rendering.
    try {
        threadNote.value = await fetchNote(props.token, note.id);
    } catch {
        threadNote.value = note;
    }
    threadReplyTitle.value = "";
    threadReplyBody.value = "";
    // Load children of the thread note
    threadChildrenLoading.value = true;
    try {
        threadChildren.value = await fetchChildren(props.token, note.id);
    } catch {
        threadChildren.value = [];
    } finally {
        threadChildrenLoading.value = false;
    }
    // Load ancestors for breadcrumb
    try {
        threadAncestors.value = await fetchAncestors(props.token, note.id);
    } catch {
        threadAncestors.value = [];
    }
    pushURL();
}

function closeThreadSidebar() {
    threadNote.value = null;
    threadChildren.value = [];
    threadAncestors.value = [];
    pushURL();
}

async function sendThreadReply() {
    if (threadSendingReply.value) return;
    if (!threadNote.value?.id) return;
    threadSendingReply.value = true;
    try {
        const child = await createNote(
            props.token,
            threadReplyTitle.value,
            threadReplyBody.value,
            threadNote.value.id,
        );
        // Reload the note list so sort order is correct.
        await loadNotes();
        threadChildren.value.push(child);
        threadReplyTitle.value = "";
        threadReplyBody.value = "";
        // Update the child_count on the original child in the main children list
        const idx = children.value.findIndex(
            (c) => c.id === threadNote.value.id,
        );
        if (idx !== -1 && children.value[idx].child_count != null) {
            children.value[idx] = {
                ...children.value[idx],
                child_count: children.value[idx].child_count + 1,
            };
        }
    } catch (e) {
        saveError.value = e.message;
    } finally {
        threadSendingReply.value = false;
    }
}

function selectThreadChild(child) {
    // Open the child's thread in the sidebar (drill down)
    openThreadSidebar(child);
}

// --- Tag functions ---

async function onTagInput() {
    const q = tagSearch.value.trim();
    try {
        const result = await fetchTags(props.token, q || "");
        tagOptions.value = Array.isArray(result) ? result : [];
    } catch {
        tagOptions.value = [];
    }
}

function addTag(name) {
    name = name.trim();
    if (!name) return;
    if (!editTags.value.includes(name)) {
        editTags.value.push(name);
        dirty.value = true;
    }
    tagSearch.value = "";
    tagOptions.value = [];
}

function addTagFromSearch() {
    const trimmed = tagSearch.value.trim();
    if (trimmed) {
        addTag(trimmed);
    }
}

function onTagBackspace() {
    if (tagSearch.value === "" && editTags.value.length > 0) {
        editTags.value.pop();
        dirty.value = true;
    }
}

async function save() {
    saveError.value = "";
    saving.value = true;
    try {
        let updated;
        if (selected.value.id) {
            updated = await updateNote(
                props.token,
                selected.value.id,
                editTitle.value,
                editBody.value,
                selected.value.parent_id,
                noteType.value,
                customData.value,
                editTags.value,
            );
            if (showHistory.value) {
                history.value = await fetchNoteHistory(props.token, updated.id);
            }
        } else {
            updated = await createNote(
                props.token,
                editTitle.value,
                editBody.value,
                selected.value.parent_id,
                noteType.value,
                customData.value,
                editTags.value,
            );
        }
        // Reload the full note list so sort order is correct.
        await loadNotes();
        selected.value = updated;
        editTitle.value = updated.title;
        customData.value =
            updated.plugin?.config || updated.custom_data || null;
        dirty.value = false;
        isEditing.value = false;
        populateParentSearch(updated);
        loadChildren(updated.id);
        await loadAncestors(updated.id);
        pushURL();
    } catch (e) {
        saveError.value = e.message;
    } finally {
        saving.value = false;
    }
}

// --- File attachment handlers ---

async function onAttachFile() {
    try {
        await ensureSelectedNoteSaved();
    } catch (e) {
        saveError.value = e.message;
        return;
    }
    const input = document.createElement("input");
    input.type = "file";
    input.onchange = async () => {
        const file = input.files[0];
        if (!file) return;
        try {
            const result = await uploadAttachment(
                props.token,
                selected.value.id,
                file,
            );
            if (!selected.value.attachments) selected.value.attachments = [];
            selected.value.attachments.push(result.file);
        } catch (e) {
            saveError.value = e.message;
        }
    };
    input.click();
}

async function onBodyDrop(e) {
    const file = e.dataTransfer.files[0];
    if (!file) return;
    try {
        await ensureSelectedNoteSaved();
    } catch (err) {
        saveError.value = err.message;
        return;
    }
    try {
        const result = await uploadInlineFile(
            props.token,
            selected.value.id,
            file,
        );
        insertAtCursor(result.markdown);
    } catch (err) {
        saveError.value = err.message;
    }
}

async function removeAttachment(file) {
    try {
        await deleteAttachment(props.token, selected.value.id, file.id);
        selected.value.attachments = selected.value.attachments.filter(
            (f) => f.id !== file.id,
        );
    } catch (e) {
        saveError.value = e.message;
    }
}

// Send a reply (creates a new child note)
async function sendReply() {
    if (sendingReply.value) return;
    if (!selected.value?.id) {
        // If the current note is not yet saved, save it first
        if (dirty.value) await save();
        if (!selected.value?.id) return;
    }
    sendingReply.value = true;
    try {
        const child = await createNote(
            props.token,
            newReplyTitle.value,
            newReplyBody.value,
            selected.value.id,
        );
        // Reload the note list so sort order is correct.
        await loadNotes();
        // Append to children so it appears in the chat feed
        children.value.push(child);
        newReplyTitle.value = "";
        newReplyBody.value = "";
    } catch (e) {
        saveError.value = e.message;
    } finally {
        sendingReply.value = false;
    }
}

function restoreBody(body) {
    editBody.value = body;
    dirty.value = true;
}

function onJobDone(job) {
    if (job.name === "generate_title") {
        loadNotes();
        if (selected.value?.id) {
            loadChildren(selected.value.id);
            // Re-fetch the main note so title, breadcrumb, and URL update.
            fetchNote(props.token, selected.value.id)
                .then((updated) => {
                    if (selected.value?.id === updated.id) {
                        selected.value = updated;
                        editTitle.value = updated.title;
                        loadAncestors(updated.id);
                        populateParentSearch(updated);
                        pushURL();
                    }
                })
                .catch(() => {});
        }
    }
}

function fmtDate(iso) {
    if (!iso) return "";
    return new Date(iso).toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
    });
}

function fmtDateFull(iso) {
    if (!iso) return "";
    return new Date(iso).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
    });
}

function onSearchInput() {
    clearTimeout(searchTimeout);
    searchTimeout = setTimeout(doSearch, 300);
    highlightedIndex.value = -1;
}

async function doSearch() {
    const q = searchQuery.value.trim();
    if (!q) {
        searchResults.value = [];
        highlightedIndex.value = -1;
        return;
    }
    searching.value = true;
    try {
        searchResults.value = await searchNotes(props.token, q);
        highlightedIndex.value = searchResults.value.length > 0 ? 0 : -1;
    } catch (e) {
        searchResults.value = [];
        highlightedIndex.value = -1;
    } finally {
        searching.value = false;
    }
}

function relevancePct(distance) {
    if (distance == null) return "";
    const pct = Math.max(0, Math.round((1 - distance / 2) * 100));
    return pct + "% match";
}

// ── [[ Link search helpers ──
function onLinkEditorInput(target) {
    const context = getLinkSearchContext(target);
    if (!context) return;
    linkSearchTarget.value = target;
    context.onChange?.();
    updateLinkSearchFromCursor();
}

function onLinkEditorCaretMove(target, e) {
    if (
        e?.type === "keyup" &&
        ["ArrowUp", "ArrowDown", "Enter", "Escape", "Tab"].includes(e.key)
    ) {
        return;
    }
    linkSearchTarget.value = target;
    updateLinkSearchFromCursor();
}

function onLinkEditorScroll(target) {
    if (!linkSearchVisible.value || linkSearchTarget.value !== target) return;
    updateLinkPopupPosition();
}

function updateLinkSearchFromCursor() {
    const context = getLinkSearchContext();
    const el = context?.textarea.value;
    if (!context || !el) {
        closeLinkSearch();
        return;
    }
    const pos = el.selectionStart ?? 0;
    const textBefore = context.text.value.slice(0, pos);
    // Find the last [[ before the cursor that hasn't been closed with ]]
    const lastOpen = textBefore.lastIndexOf("[[");
    const lastClose = textBefore.lastIndexOf("]]");
    if (lastOpen !== -1 && lastOpen > lastClose) {
        const query = textBefore.slice(lastOpen + 2);
        if (!query.includes("]]") && !query.includes("\n")) {
            const queryUnchanged =
                linkSearchVisible.value && query === linkSearchQuery.value;

            linkSearchQuery.value = query;
            linkSearchVisible.value = true;
            updateLinkPopupPosition();

            if (queryUnchanged) {
                return;
            }

            clearTimeout(linkSearchTimeout);
            if (!query.trim()) {
                linkSearching.value = false;
                linkSearchResults.value = [];
                linkSearchIndex.value = -1;
                return;
            }
            linkSearchTimeout = setTimeout(doLinkSearch, 150);
            return;
        }
    }
    closeLinkSearch();
}

function getTextareaCaretPosition(textarea, position) {
    const div = document.createElement("div");
    const span = document.createElement("span");
    const style = window.getComputedStyle(textarea);
    const props = [
        "boxSizing",
        "width",
        "height",
        "overflowX",
        "overflowY",
        "borderTopWidth",
        "borderRightWidth",
        "borderBottomWidth",
        "borderLeftWidth",
        "paddingTop",
        "paddingRight",
        "paddingBottom",
        "paddingLeft",
        "fontStyle",
        "fontVariant",
        "fontWeight",
        "fontStretch",
        "fontSize",
        "fontSizeAdjust",
        "lineHeight",
        "fontFamily",
        "textAlign",
        "textTransform",
        "textIndent",
        "textDecoration",
        "letterSpacing",
        "wordSpacing",
        "tabSize",
        "MozTabSize",
    ];

    div.style.position = "absolute";
    div.style.visibility = "hidden";
    div.style.whiteSpace = "pre-wrap";
    div.style.wordWrap = "break-word";
    div.style.top = "0";
    div.style.left = "0";

    for (const prop of props) {
        div.style[prop] = style[prop];
    }

    div.textContent = textarea.value.slice(0, position);
    span.textContent = textarea.value.slice(position) || ".";
    div.appendChild(span);
    document.body.appendChild(div);

    const left = span.offsetLeft - textarea.scrollLeft;
    const top = span.offsetTop - textarea.scrollTop;

    document.body.removeChild(div);
    return { left, top };
}

function updateLinkPopupPosition() {
    const el = getLinkSearchContext()?.textarea.value;
    if (!el || !linkSearchVisible.value) return;

    const { left: caretLeft, top: caretTop } = getTextareaCaretPosition(
        el,
        el.selectionStart ?? 0,
    );
    const style = window.getComputedStyle(el);
    const lineHeight =
        parseFloat(style.lineHeight) || parseFloat(style.fontSize) || 18;
    const popupWidth = 320;
    const popupHeight = 220;
    const gap = 8;
    const pad = 8;

    let left = Math.max(pad, caretLeft);
    let top = caretTop + lineHeight + gap;

    const maxLeft = Math.max(pad, el.clientWidth - popupWidth - pad);
    left = Math.min(left, maxLeft);

    const rect = el.getBoundingClientRect();
    const popupBottom = rect.top + top + popupHeight;
    if (popupBottom > window.innerHeight - 12) {
        top = Math.max(pad, caretTop - popupHeight - gap);
    }

    linkPopupStyle.value = {
        left: `${left}px`,
        top: `${top}px`,
    };
}

async function doLinkSearch() {
    const q = linkSearchQuery.value.trim();
    if (!q) {
        linkSearchResults.value = [];
        linkSearchIndex.value = -1;
        return;
    }
    linkSearching.value = true;
    try {
        linkSearchResults.value = await searchNotes(props.token, q);
        linkSearchIndex.value = linkSearchResults.value.length > 0 ? 0 : -1;
    } catch (e) {
        linkSearchResults.value = [];
        linkSearchIndex.value = -1;
    } finally {
        linkSearching.value = false;
    }
}

function closeLinkSearch() {
    linkSearchVisible.value = false;
    linkSearchQuery.value = "";
    linkSearchResults.value = [];
    linkSearchIndex.value = -1;
    linkSearchTarget.value = null;
    clearTimeout(linkSearchTimeout);
}

function selectLinkResult(note) {
    const context = getLinkSearchContext();
    const el = context?.textarea.value;
    if (!context || !el) return;
    const pos = el.selectionStart ?? 0;
    const textBefore = context.text.value.slice(0, pos);
    const textAfter = context.text.value.slice(pos);
    // Find the last [[ before cursor
    const lastOpen = textBefore.lastIndexOf("[[");
    if (lastOpen === -1) return;
    // Replace from [[ to cursor with the markdown link
    const newText =
        textBefore.slice(0, lastOpen) +
        `[${note.title || "Untitled"}](/note/${note.id})`;
    context.text.value = newText + textAfter;
    closeLinkSearch();
    // Place cursor after the inserted link
    requestAnimationFrame(() => {
        el.focus();
        const cursorPos = newText.length;
        el.setSelectionRange(cursorPos, cursorPos);
        updateLinkPopupPosition();
    });
    context.onChange?.();
}

// ── Keyboard shortcut handler ──
function onKeyDown(e) {
    const mod = isMac() ? e.metaKey : e.ctrlKey;

    // Shift+? => toggle hotkey help (skip if typing in an editor field)
    if (e.shiftKey && e.key === "?") {
        const tag = document.activeElement?.tagName;
        if (isEditing.value && (tag === "TEXTAREA" || tag === "INPUT")) return;
        e.preventDefault();
        showHotkeys.value = !showHotkeys.value;
        return;
    }

    // Esc => close link search / modals / history / clear search / clear highlight / blur
    if (e.key === "Escape") {
        if (linkSearchVisible.value) {
            e.preventDefault();
            closeLinkSearch();
            return;
        }
        if (showHotkeys.value) {
            showHotkeys.value = false;
            return;
        }
        if (showDeleteModal.value) {
            showDeleteModal.value = false;
            return;
        }
        if (showHistory.value) {
            showHistory.value = false;
            return;
        }
        // If search bar is focused and has text, clear it
        const inSearch =
            document.activeElement?.classList.contains("search-input");
        if (inSearch && searchQuery.value.trim()) {
            searchQuery.value = "";
            searchResults.value = [];
            highlightedIndex.value = -1;
            return;
        }
        if (highlightedIndex.value >= 0) {
            highlightedIndex.value = -1;
            return;
        }
        if (
            document.activeElement?.tagName === "INPUT" ||
            document.activeElement?.tagName === "TEXTAREA"
        ) {
            document.activeElement.blur();
            return;
        }
        return;
    }

    // Ctrl/Cmd+N => new note
    if (mod && e.key === "n") {
        e.preventDefault();
        newNote();
        return;
    }

    // Ctrl/Cmd+S => save
    if (mod && e.key === "s") {
        e.preventDefault();
        if (dirty.value && selected.value) save();
        return;
    }

    // Ctrl/Cmd+E => toggle edit/view
    if (mod && e.key === "e") {
        e.preventDefault();
        if (selected.value) {
            toggleEdit();
            if (isEditing.value) {
                requestAnimationFrame(() =>
                    document.querySelector(".body-textarea")?.focus(),
                );
            }
        }
        return;
    }

    // Ctrl/Cmd+H => toggle history
    if (mod && e.key === "h") {
        e.preventDefault();
        if (selected.value?.id) toggleHistory();
        return;
    }

    // Ctrl/Cmd+K => focus search
    if (mod && e.key === "k") {
        e.preventDefault();
        const inp = document.querySelector(".search-input");
        if (inp) inp.focus();
        return;
    }

    // Arrow Up / Down => navigate link search popup or sidebar list
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        if (linkSearchVisible.value) {
            const linkList = linkSearchResults.value;
            if (linkList.length === 0) return;
            e.preventDefault();
            if (linkSearchIndex.value < 0) {
                linkSearchIndex.value =
                    e.key === "ArrowDown" ? 0 : linkList.length - 1;
            } else if (e.key === "ArrowDown") {
                linkSearchIndex.value =
                    (linkSearchIndex.value + 1) % linkList.length;
            } else {
                linkSearchIndex.value =
                    (linkSearchIndex.value - 1 + linkList.length) %
                    linkList.length;
            }
            // Scroll highlighted item into view
            requestAnimationFrame(() => {
                const lel = document.querySelector(
                    ".link-search-item.highlighted",
                );
                if (lel) lel.scrollIntoView({ block: "nearest" });
            });
            return;
        }
        const list = sidebarList.value;
        if (list.length === 0) return;
        e.preventDefault();
        if (highlightedIndex.value < 0) {
            highlightedIndex.value =
                e.key === "ArrowDown" ? 0 : list.length - 1;
        } else if (e.key === "ArrowDown") {
            highlightedIndex.value = (highlightedIndex.value + 1) % list.length;
        } else {
            highlightedIndex.value =
                (highlightedIndex.value - 1 + list.length) % list.length;
        }
        // Scroll highlighted item into view after DOM update
        requestAnimationFrame(() => {
            const el = document.querySelector(".note-item.highlighted");
            if (el) el.scrollIntoView({ block: "nearest" });
        });
        return;
    }

    // Enter => select link search result, or open highlighted note, or first result if focus is in search input
    if (e.key === "Enter") {
        if (linkSearchVisible.value) {
            const linkList = linkSearchResults.value;
            if (
                linkList.length > 0 &&
                linkSearchIndex.value >= 0 &&
                linkSearchIndex.value < linkList.length
            ) {
                e.preventDefault();
                selectLinkResult(linkList[linkSearchIndex.value]);
            }
            return;
        }
        const tag = document.activeElement?.tagName;
        const inSearch =
            document.activeElement?.classList.contains("search-input");
        // Don't intercept Enter in editor inputs/textarea (title, body, etc.)
        if (!inSearch && (tag === "INPUT" || tag === "TEXTAREA")) return;
        const idx = inSearch ? 0 : highlightedIndex.value;
        if (idx >= 0 && idx < sidebarList.value.length) {
            e.preventDefault();
            const item = sidebarList.value[idx];
            if (searchQuery.value.trim()) {
                selectSearchResult(item);
            } else {
                selectNote(item);
            }
            if (inSearch) document.activeElement?.blur();
        }
        return;
    }
}

onMounted(() => {
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("click", onClickOutside);
    window.addEventListener("popstate", onPopstate);
    // Restore state from URL on initial load
    loadFromURL();
});

onUnmounted(() => {
    window.removeEventListener("keydown", onKeyDown);
    window.removeEventListener("click", onClickOutside);
    window.removeEventListener("popstate", onPopstate);
});

function onClickOutside(e) {
    if (!e.target.closest(".parent-picker-wrapper")) {
        showParentPicker.value = false;
    }
}

// ── URL routing ──
// URL scheme:
//   /                                          → no selection
//   /note/175:this:is:a:example                → note 175 selected (titles for history only)
//   /note/175:this:is:a:example/thread/178:foo → note 175 with thread 178 in sidebar
//   /note/new                                  → compose a new note
// Only the numeric IDs are parsed on nav; titles are cosmetic.

function notePath(note) {
    // Build "id:ancestor:ancestor:self" slug for a note
    const chain = ancestors.value;
    let slug = String(note.id);
    for (const n of chain) {
        slug += ":" + (n.title || "Untitled").replace(/[\/:]/g, "-");
    }
    return slug;
}

function threadNotePath(note) {
    // Build "id:ancestor:ancestor:self" slug for the thread note
    const chain = threadAncestors.value;
    let slug = String(note.id);
    for (const n of chain) {
        slug += ":" + (n.title || "Untitled").replace(/[\/:]/g, "-");
    }
    return slug;
}

function buildURL() {
    let url = "/";
    if (selected.value) {
        if (selected.value.id) {
            url = `/note/${notePath(selected.value)}`;
        } else {
            url = "/note/new";
        }
    }
    if (threadNote.value) {
        url += `/thread/${threadNotePath(threadNote.value)}`;
    }
    return url;
}

function pushURL() {
    const url = buildURL();
    window.history.pushState({}, "", url);
}

function replaceURL() {
    const url = buildURL();
    window.history.replaceState({}, "", url);
}

// extractID pulls just the leading numeric ID from a slug like "175:foo:bar"
function extractID(slug) {
    if (!slug) return null;
    const id = parseInt(slug.split(":")[0], 10);
    return isNaN(id) || id <= 0 ? null : id;
}

async function loadFromURL() {
    const path = location.pathname;
    // Match: /note/<slug>  or  /note/<slug>/thread/<slug>
    // Slugs: "new" or "123:title:title" — we only care about the numeric ID before any colon.
    const m = path.match(/^\/note\/([^/]+)(?:\/thread\/([^/]+))?\/?$/);
    if (!m) {
        if (selected.value || threadNote.value) {
            selected.value = null;
            threadNote.value = null;
            replaceURL();
        }
        return;
    }

    const noteSlug = m[1];
    const threadSlug = m[2];

    // Handle /note/new
    if (noteSlug === "new") {
        if (!selected.value || selected.value.id !== null) {
            newNote();
        }
    } else {
        const id = extractID(noteSlug);
        if (!id) return;

        if (!selected.value || selected.value.id !== id) {
            let note = notes.value.find((n) => n.id === id);
            if (!note) {
                try {
                    note = await fetchNote(props.token, id);
                    if (note && !notes.value.some((n) => n.id === note.id)) {
                        notes.value.push(note);
                    }
                } catch {
                    replaceURL();
                    return;
                }
            }
            if (note) {
                selectNote(note);
            } else {
                replaceURL();
                return;
            }
        }
    }

    // Handle thread sidebar
    if (threadSlug) {
        const tid = extractID(threadSlug);
        if (tid) {
            if (!threadNote.value || threadNote.value.id !== tid) {
                let tNote = notes.value.find((n) => n.id === tid);
                if (!tNote) {
                    try {
                        tNote = await fetchNote(props.token, tid);
                        if (
                            tNote &&
                            !notes.value.some((n) => n.id === tNote.id)
                        ) {
                            notes.value.push(tNote);
                        }
                    } catch {
                        // Thread note not found — just clear it
                    }
                }
                if (tNote) {
                    await openThreadSidebar(tNote);
                }
            }
        }
    } else {
        if (threadNote.value) {
            closeThreadSidebar();
        }
    }
}

function onPopstate() {
    loadFromURL();
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
    cursor: pointer;
    transition: opacity 0.15s;
}
.app-logo:hover {
    opacity: 0.8;
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
    from {
        transform: rotate(0deg);
    }
    to {
        transform: rotate(360deg);
    }
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
    transition:
        background 0.1s,
        border-color 0.1s;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
}

.note-item:hover {
    background: var(--raised-bg);
}

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

.pin-indicator {
    position: absolute;
    right: 0.5rem;
    top: 50%;
    transform: translateY(-50%);
    font-size: 0.75rem;
    opacity: 0.5;
}

.note-item {
    position: relative;
}

.pin-editor-btn.pinned {
    color: var(--accent-amber);
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
    align-items: flex-start;
    gap: 0.75rem;
    padding: 0.85rem 1.25rem;
    border-bottom: 1px solid var(--border-color);
    background: var(--panel-bg);
}

.editor-header-left {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    min-width: 0;
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
    width: 100%;
}

.title-input:focus {
    border-bottom-color: var(--accent-teal);
}

.title-display {
    flex: 1;
    font-size: 1.05rem;
    font-weight: 600;
    padding: 0.2rem 0;
    color: var(--font-color);
}

.editor-actions {
    display: flex;
    gap: 0.5rem;
    flex-shrink: 0;
    flex-wrap: wrap;
}

.btn-child {
    font-size: 0.82rem;
    padding: 0.45rem 0.85rem;
}

.save-error {
    padding: 0.4rem 1.25rem;
    font-size: 0.85rem;
    color: var(--heading-color);
    background: var(--panel-bg);
}

/* Parent selector */
.type-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.25rem;
}

.type-select {
    background: var(--raised-bg);
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 0.35rem 0.5rem;
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    cursor: pointer;
}

.type-select:focus {
    border-color: var(--accent-teal);
}

.tag-row {
    display: flex;
    align-items: flex-start;
    gap: 0.4rem;
    margin-bottom: 0.25rem;
}

.tag-list {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem;
    flex: 1;
}

.tag-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    background: var(--accent-teal);
    color: #fff;
    border-radius: 12px;
    padding: 0.15rem 0.55rem;
    font-size: 0.75rem;
    font-weight: 500;
    white-space: nowrap;
}

.tag-remove {
    background: none;
    border: none;
    color: rgba(255, 255, 255, 0.7);
    cursor: pointer;
    padding: 0;
    font-size: 0.85rem;
    line-height: 1;
    margin-left: 0.1rem;
}

.tag-remove:hover {
    color: #fff;
}

.tag-input-wrapper {
    position: relative;
    flex: 1;
    min-width: 100px;
}

.tag-input {
    width: 100%;
    border: 1px dashed var(--border-color);
    background: transparent;
    color: var(--font-color);
    padding: 0.2rem 0.4rem;
    border-radius: 12px;
    font-size: 0.75rem;
    outline: none;
    font-family: inherit;
}

.tag-input:focus {
    border-color: var(--accent-teal);
}

.tag-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    margin-top: 2px;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    max-height: 160px;
    overflow-y: auto;
    z-index: 50;
    box-shadow: 0 4px 12px var(--shadow-color);
}

.tag-dropdown-item {
    padding: 0.35rem 0.6rem;
    font-size: 0.82rem;
    cursor: pointer;
    color: var(--font-color);
    transition: background 0.1s;
}

.tag-dropdown-item:hover {
    background: var(--panel-bg);
}

.parent-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
}

.parent-label {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    white-space: nowrap;
    flex-shrink: 0;
}

.parent-picker-wrapper {
    position: relative;
    flex: 1;
    max-width: 320px;
}

.parent-input {
    width: 100%;
    font-size: 0.8rem;
    padding: 0.25rem 0.5rem;
    border-radius: 4px;
}

.parent-clear-btn {
    position: absolute;
    right: 2px;
    top: 50%;
    transform: translateY(-50%);
    padding: 0.15rem 0.35rem;
    font-size: 0.7rem;
    border: none;
    background: transparent;
    color: var(--font-color-secondary);
}

.parent-clear-btn:hover {
    color: var(--heading-color);
}

.parent-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    margin-top: 2px;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    max-height: 220px;
    overflow-y: auto;
    z-index: 50;
    box-shadow: 0 4px 12px var(--shadow-color);
}

.parent-dropdown-item {
    padding: 0.4rem 0.6rem;
    font-size: 0.82rem;
    cursor: pointer;
    color: var(--font-color);
    transition: background 0.1s;
}

.parent-dropdown-item:hover {
    background: var(--panel-bg);
}

.parent-dropdown-item.muted {
    color: var(--font-color-secondary);
    cursor: default;
}

.breadcrumb-trail {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0;
    font-size: 0.78rem;
    margin-top: 0.3rem;
}

.breadcrumb-seg {
    color: var(--accent-teal);
    cursor: pointer;
    transition:
        color 0.15s,
        text-decoration 0.15s;
    white-space: nowrap;
}

.breadcrumb-seg:hover {
    color: var(--header-title-color);
    text-decoration: underline;
}

.breadcrumb-current {
    color: var(--font-color);
    cursor: default;
    font-weight: 600;
}

.breadcrumb-current:hover {
    color: var(--font-color);
    text-decoration: none;
}

.breadcrumb-colon {
    color: var(--font-color-secondary);
    margin: 0 0.15rem;
    cursor: default;
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

.body-textarea:focus {
    border-color: transparent;
}

/* Body textarea wrapper (for link search popup positioning) */
.body-textarea-wrapper {
    position: relative;
    flex: 1;
    display: flex;
    overflow: visible;
}

/* [[ Link search popup */
.link-search-popup {
    position: absolute;
    width: 320px;
    max-width: min(320px, calc(100% - 16px));
    max-height: 220px;
    overflow-y: auto;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 10px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
    z-index: 50;
}

.link-search-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.55rem 0.85rem;
    cursor: pointer;
    border-bottom: 1px solid var(--border-color);
    font-size: 0.85rem;
    transition:
        background 0.12s,
        box-shadow 0.12s,
        outline-color 0.12s;
}

.link-search-item:last-child {
    border-bottom: none;
}

.link-search-item:hover {
    background: rgba(255, 255, 255, 0.06);
}

.link-search-item.highlighted {
    background: #2f2000;
    color: #fff;
    box-shadow: inset 4px 0 0 #ffb400;
    outline: 1px solid rgba(255, 180, 0, 0.35);
}

.link-search-item.highlighted .link-search-relevance {
    color: rgba(255, 255, 255, 0.92);
}

.link-search-title {
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.link-search-relevance {
    color: var(--font-color-secondary);
    font-size: 0.75rem;
    flex-shrink: 0;
    margin-left: 1rem;
}

.link-search-status {
    padding: 0.55rem 0.85rem;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    text-align: center;
}

/* Markdown rendered view */
.body-rendered {
    flex: 1;
    overflow-y: auto;
    padding: 1.25rem;
    line-height: 1.7;
    font-size: 0.95rem;
}

.markdown-body {
    width: 100%;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
    color: var(--header-title-color);
    margin: 1.3em 0 0.5em;
    line-height: 1.25;
}

.markdown-body :deep(h1) {
    font-size: 1.8rem;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 0.3em;
}
.markdown-body :deep(h2) {
    font-size: 1.5rem;
    border-bottom: 1px solid var(--border-color);
    padding-bottom: 0.25em;
}
.markdown-body :deep(h3) {
    font-size: 1.25rem;
}
.markdown-body :deep(h4) {
    font-size: 1.1rem;
}

.markdown-body :deep(p) {
    margin: 0.6em 0;
}

.markdown-body :deep(a) {
    color: var(--accent-teal);
    text-decoration: underline;
}

.markdown-body :deep(strong),
.markdown-body :deep(b) {
    color: #fff;
    font-weight: 700;
}

.markdown-body :deep(em),
.markdown-body :deep(i) {
    color: var(--font-color);
}

.markdown-body :deep(code) {
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    padding: 0.15em 0.4em;
    font-size: 0.88em;
    color: var(--pre-text-color);
    font-family:
        "Cascadia Code", "Fira Code", "JetBrains Mono", "Consolas", monospace;
}

.markdown-body :deep(pre) {
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 1rem;
    overflow-x: auto;
    margin: 0.8em 0;
}

.markdown-body :deep(pre code) {
    background: none;
    border: none;
    padding: 0;
    font-size: 0.85rem;
    color: var(--pre-text-color);
}

.markdown-body :deep(blockquote) {
    border-left: 3px solid var(--accent-teal);
    padding: 0.3em 0.8em;
    margin: 0.8em 0;
    color: var(--font-color-secondary);
    background: rgba(109, 148, 132, 0.08);
    border-radius: 0 6px 6px 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
    padding-left: 1.5em;
    margin: 0.6em 0;
}

.markdown-body :deep(li) {
    margin: 0.25em 0;
}

.markdown-body :deep(hr) {
    border: none;
    border-top: 1px solid var(--border-color);
    margin: 1.5em 0;
}

.markdown-body :deep(table) {
    border-collapse: collapse;
    width: 100%;
    margin: 0.8em 0;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
    border: 1px solid var(--border-color);
    padding: 0.45em 0.75em;
    text-align: left;
}

.markdown-body :deep(th) {
    background: var(--panel-bg);
    color: var(--header-title-color);
    font-weight: 600;
}

.markdown-body :deep(img) {
    max-width: 100%;
    border-radius: 6px;
    margin: 0.6em 0;
}

/* Footnotes */
.markdown-body :deep(.footnote-ref) {
    color: var(--accent-teal);
    text-decoration: none;
    font-size: 0.8em;
}
.markdown-body :deep(.footnote-backref) {
    color: var(--accent-teal);
    text-decoration: none;
}
.markdown-body :deep(section.footnotes) {
    border-top: 1px solid var(--border-color);
    margin-top: 2em;
    padding-top: 1em;
}
.markdown-body :deep(section.footnotes ol) {
    font-size: 0.88em;
    color: var(--font-color-secondary);
}
.markdown-body :deep(section.footnotes li) {
    margin: 0.4em 0;
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

.history-entry:hover {
    background: var(--raised-bg);
}

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

/* Children panel */
.children-panel {
    width: 260px;
    min-width: 200px;
    border-left: 1px solid var(--border-color);
    background: var(--panel-bg);
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.children-header {
    padding: 0.65rem 1rem;
    font-size: 0.8rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--font-color-secondary);
    border-bottom: 1px solid var(--border-color);
}

.children-empty {
    padding: 1.25rem 1rem;
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    text-align: center;
}

.child-item {
    padding: 0.65rem 1rem;
    cursor: pointer;
    border-bottom: 1px solid var(--border-color);
    transition: background 0.1s;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
}

.child-item:hover {
    background: var(--raised-bg);
}

.child-title {
    font-size: 0.85rem;
    color: var(--accent-teal);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.child-date {
    font-size: 0.7rem;
    color: var(--date-color);
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
    font-family:
        "Cascadia Code", "Fira Code", "JetBrains Mono", "Consolas", monospace;
    color: var(--pre-text-color);
    white-space: nowrap;
}

.hotkey-desc {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
}

/* Sidebar footer (passkey registration) */
.sidebar-footer {
    padding: 0.6rem 0.75rem;
    border-top: 1px solid var(--border-color);
    text-align: center;
}
.passkey-btn {
    width: 100%;
    padding: 0.4rem 0.5rem;
    font-size: 0.8rem;
}
.backup-btn {
    width: 100%;
    padding: 0.4rem 0.5rem;
    font-size: 0.8rem;
    margin-bottom: 0.4rem;
}
.reg-error {
    color: var(--heading-color);
    font-size: 0.75rem;
    margin-top: 0.3rem;
}
.reg-ok {
    color: var(--accent-teal);
    font-size: 0.75rem;
    margin-top: 0.3rem;
}

/* =============================================
   Chat Feed Styles
   ============================================= */

.chat-feed {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
    overflow-y: auto;
    padding: 1rem 1.25rem;
    background: var(--html-bg);
}

/* Chat message bubble */
.chat-message {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.9rem 1.1rem;
    border-radius: 12px;
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    box-shadow: 0 1px 4px var(--shadow-color, rgba(0, 0, 0, 0.15));
    max-width: 100%;
    transition:
        background 0.15s,
        border-color 0.15s;
}

/* Root message (the selected note) – stands out slightly */
.chat-message-root {
    background: var(--raised-bg);
    border-color: var(--accent-teal);
    border-left: 4px solid var(--accent-teal);
}

/* Child messages – slightly inset, visually subordinate */
.chat-message-child {
    max-width: 88%;
    align-self: flex-start;
    border-left: 3px solid var(--border-color);
}

.chat-message-child:hover {
    border-left-color: var(--accent-teal);
    background: var(--raised-bg);
}

/* Message metadata row */
.message-meta {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
}

.message-author {
    font-weight: 600;
    font-size: 0.9rem;
    color: var(--header-title-color);
}

.message-date {
    font-size: 0.72rem;
    color: var(--date-color);
}

.message-badge {
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--accent-teal);
    background: rgba(109, 148, 132, 0.15);
    padding: 0.15rem 0.5rem;
    border-radius: 10px;
}

/* Message body inside bubbles */
.message-body {
    font-size: 0.92rem;
    line-height: 1.65;
    color: var(--font-color);
}

.message-body .body-textarea {
    flex: unset;
    min-height: 120px;
    padding: 0.6rem;
    border-radius: 8px;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    width: 100%;
}

.message-body .body-rendered {
    padding: 0;
    overflow-y: visible;
}

/* Thread / actions row */
.message-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    padding-top: 0.3rem;
    border-top: 1px solid var(--border-color);
}

.btn-thread {
    font-size: 0.78rem;
    color: var(--accent-teal);
    padding: 0.25rem 0.65rem;
}

.btn-thread:hover {
    background: rgba(109, 148, 132, 0.12);
    color: var(--accent-teal);
}

/* =============================================
   Thread Sidebar (right)
   ============================================= */

.thread-sidebar {
    width: 320px;
    min-width: 260px;
    background: var(--panel-bg);
    border-left: 1px solid var(--border-color);
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.thread-sidebar .chat-feed {
    flex: 1;
}

.thread-sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid var(--border-color);
    gap: 0.4rem;
}

.thread-sidebar-title {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--font-color);
    flex: 1;
    text-align: center;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.thread-breadcrumb {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    padding: 0.35rem 0.75rem;
    font-size: 0.72rem;
    border-bottom: 1px solid var(--border-color);
    gap: 0;
}

.thread-composer {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.5rem 0.75rem;
    border-top: 1px solid var(--border-color);
    background: var(--panel-bg);
}

@media (max-width: 767px) {
    .thread-sidebar {
        display: none;
    }
}

/* Status / empty row */
.chat-status {
    padding: 0.85rem 1.1rem;
    font-size: 0.82rem;
    color: var(--font-color-secondary);
    text-align: center;
    font-style: italic;
}

/* History inline section */
.chat-history-section {
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 10px;
    overflow: hidden;
}

.chat-history-section .history-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
}

/* =============================================
   Chat Composer (quick reply bar)
   ============================================= */

.chat-composer {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.7rem 1.25rem;
    border-top: 1px solid var(--border-color);
    background: var(--panel-bg);
}

.composer-title {
    font-size: 0.85rem;
    padding: 0.4rem 0.7rem;
    border-radius: 8px;
    border: 1px solid var(--border-color);
    background: var(--raised-bg);
    color: var(--font-color);
    width: 100%;
}

.composer-body-row {
    position: relative;
    display: flex;
    align-items: flex-end;
    gap: 0.5rem;
    overflow: visible;
}

.composer-textarea {
    flex: 1;
    font-size: 0.85rem;
    padding: 0.45rem 0.7rem;
    border-radius: 10px;
    border: 1px solid var(--border-color);
    background: var(--raised-bg);
    color: var(--font-color);
    resize: none;
    line-height: 1.5;
    font-family: inherit;
    min-height: 36px;
}

.composer-textarea:focus {
    min-height: 250px;
}

.composer-send {
    flex-shrink: 0;
    padding: 0.45rem 1.1rem;
    font-size: 0.85rem;
    font-weight: 600;
    border-radius: 10px;
}
</style>
