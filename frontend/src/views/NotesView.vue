<template>
    <div class="layout">
        <!-- Sidebar -->
        <aside class="sidebar">
            <div class="sidebar-header">
                <div class="shortcut-anchor sidebar-logo-anchor">
                    <img
                        src="../assets/MentisEterna_logo.svg"
                        alt="Logo"
                        class="app-logo"
                        :title="getShortcutLabel('show-shortcuts')"
                        @click="showHotkeys = !showHotkeys"
                    />
                    <ShortcutHint
                        v-if="shortcutHintsVisible"
                        :label="getHintLabel('show-shortcuts')"
                    />
                </div>
                <span class="app-title">MentisEterna</span>
                <span class="ws-indicator" :class="{ connected: wsConnected, disconnected: !wsConnected }" :title="wsIndicatorTitle">
                    <span class="ws-dot"></span>
                    <span v-if="wsLatency != null" class="ws-latency">{{ wsLatency }} ms</span>
                </span>
                <button
                    class="btn-ghost icon-btn shortcut-anchor"
                    :title="getShortcutLabel('open-options')"
                    @click="openOptions"
                >
                    ⚙
                    <ShortcutHint
                        v-if="shortcutHintsVisible"
                        :label="getHintLabel('open-options')"
                    />
                </button>
            </div>
            <button
                class="btn-amber new-btn shortcut-anchor"
                :title="getShortcutLabel('new-note')"
                @click="newNote"
            >
                + New Note
                <ShortcutHint
                    v-if="shortcutHintsVisible"
                    :label="getHintLabel('new-note')"
                />
            </button>
            <div class="search-box shortcut-anchor">
                <div class="search-input-row">
                    <input
                        v-model="searchQuery"
                        ref="searchInputEl"
                        type="text"
                        placeholder="Search notes… (.i filters, .a all, .t tags)"
                        class="search-input"
                        :title="getShortcutLabel('focus-search')"
                        @input="onSearchInput"
                        @keydown="onSearchFieldKeydown"
                    />
                    <button
                        v-if="searchQuery.trim()"
                        type="button"
                        class="search-clear-btn"
                        :class="{ spinning: searching }"
                        title="Clear search"
                        aria-label="Clear search"
                        @click="clearSearchInput({ focus: true })"
                    >
                        ✕
                    </button>
                </div>
                <div class="search-box-meta">
                    <div v-if="searchFilterSummary" class="search-filter-summary">
                        {{ searchFilterSummary }}
                    </div>
                    <div v-if="searchStatusMessage" class="search-stream-status">
                        {{ searchStatusMessage }}
                    </div>
                </div>
                <div v-if="searchTypePickerVisible" class="search-type-panel">
                    <div class="search-type-panel-header">
                        <span class="search-type-panel-title"
                            >Include note types</span
                        >
                        <div class="search-type-panel-actions">
                            <button
                                type="button"
                                class="btn-ghost btn-sm search-type-action"
                                @click="setSearchTypes(['standard'])"
                            >
                                Standard
                            </button>
                            <button
                                type="button"
                                class="btn-ghost btn-sm search-type-action"
                                @click="setSearchTypes(allSearchTypeValues)"
                            >
                                All
                            </button>
                        </div>
                    </div>
                    <label
                        v-for="opt in typeOptions"
                        :key="opt.value"
                        class="search-type-option"
                    >
                        <input
                            type="checkbox"
                            :checked="searchSelectedTypes.includes(opt.value)"
                            @change="
                                toggleSearchType(
                                    opt.value,
                                    $event.target.checked,
                                )
                            "
                        />
                        <span>{{ opt.label }}</span>
                    </label>
                </div>
                <ShortcutHint
                    v-if="shortcutHintsVisible"
                    :label="getHintLabel('focus-search')"
                    position="top-left"
                />
            </div>
            <div class="note-list shortcut-anchor">
                <ShortcutHint
                    v-if="shortcutHintsVisible"
                    label="↑↓"
                    position="top-left"
                />
                <ShortcutHint
                    v-if="shortcutHintsVisible"
                    label="↵"
                    position="top-right"
                />
                <div class="note-list-scroll">
                <!-- Search results mode -->
                <template v-if="hasSidebarSearch">
                    <div
                        v-for="(section, sectionIdx) in searchSectionGroups"
                        :key="section.key"
                        class="search-section"
                        :class="{
                            collapsed: section.collapsed,
                            highlighted:
                                searchKeyboardMode &&
                                highlightedIndex >= 0 &&
                                searchSelectableEntries[highlightedIndex]?.kind ===
                                    'section' &&
                                searchSelectableEntries[highlightedIndex]?.key ===
                                    section.key,
                        }"
                    >
                        <button
                            type="button"
                            class="search-section-header"
                            :class="{
                                collapsed: section.collapsed,
                                highlighted:
                                    searchKeyboardMode &&
                                    highlightedIndex >= 0 &&
                                    searchSelectableEntries[highlightedIndex]?.kind ===
                                        'section' &&
                                    searchSelectableEntries[highlightedIndex]?.key ===
                                        section.key,
                            }"
                            @click="toggleSearchSection(section.key)"
                        >
                            <span class="search-section-heading">
                                <span class="search-section-caret">{{
                                    section.collapsed ? '▸' : '▾'
                                }}</span>
                                <span class="search-section-title">{{ section.label }}</span>
                            </span>
                            <span class="search-section-count">{{
                                section.items.length
                            }}</span>
                        </button>
                        <div
                            v-if="section.description && !section.collapsed"
                            class="search-section-description"
                        >
                            {{ section.description }}
                        </div>
                        <div
                            v-if="!section.collapsed"
                            v-for="(item, itemIdx) in section.items"
                            :key="item.key"
                            class="note-item search-note-item"
                            :class="{
                                active: selected?.id === item.result.id,
                                highlighted:
                                    searchKeyboardMode &&
                                    highlightedIndex >= 0 &&
                                    searchSelectableEntries[highlightedIndex]?.kind ===
                                        'result' &&
                                    searchSelectableEntries[highlightedIndex]?.item?.key ===
                                        item.key,
                            }"
                            @click="selectSearchResult(item.result)"
                        >
                            <span class="note-title-row">
                                <span class="note-title">{{
                                    item.result.title || "Untitled"
                                }}</span>
                                <span
                                    v-if="
                                        item.result.type &&
                                        item.result.type !== 'standard'
                                    "
                                    class="search-result-type"
                                >
                                    {{ noteTypeLabel(item.result.type) }}
                                </span>
                            </span>
                            <span
                                v-if="item.result.path"
                                class="search-result-path"
                                >{{ item.result.path }}</span
                            >
                            <span
                                v-if="item.result.tags && item.result.tags.length"
                                class="search-result-tags"
                                >{{ formatSearchTags(item.result.tags) }}</span
                            >
                            <span class="note-date"
                                >{{ fmtDate(item.result.updated_at) }} —
                                {{ relevancePct(item.result.distance) }}</span
                            >
                        </div>
                    </div>
                    <div v-if="searchError && !searching" class="empty-list">
                        {{ searchError }}
                    </div>
                    <div
                        v-else-if="searchResults.length === 0 && searching"
                        class="empty-list"
                    >
                        {{ searchStatusMessage || "Searching…" }}
                    </div>
                    <div
                        v-else-if="searchResults.length === 0 && !searching"
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
                <div v-if="loading && !hasSidebarSearch" class="empty-list">
                    Loading…
                </div>
                </div>
            </div>
        </aside>

        <!-- Editor / Chat Pane -->
        <main class="editor-pane">
            <template v-if="selected">
                <!-- Header bar -->
                <div class="editor-header">
                    <div class="editor-header-left">
                        <div
                            v-if="isEditing"
                            class="shortcut-anchor title-input-anchor"
                        >
                            <input
                                ref="editTitleInput"
                                v-model="editTitle"
                                class="title-input"
                                :title="getShortcutLabel('focus-note-title')"
                                placeholder="Note title (leave blank to auto-generate)"
                                @input="dirty = true"
                            />
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('focus-note-title')
                                "
                                :label="getHintLabel('focus-note-title')"
                                position="top-left"
                            />
                        </div>
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
                        <div v-if="selected.id" class="tag-row auto-tag-row">
                            <span class="parent-label">Auto Tags:</span>
                            <div class="tag-list auto-tag-list">
                                <span
                                    v-for="tag in selected.auto_tags || []"
                                    :key="tag"
                                    class="tag-chip auto-tag-chip"
                                >
                                    {{ tag }}
                                </span>
                                <span
                                    v-if="
                                        (!selected.auto_tags ||
                                            selected.auto_tags.length === 0) &&
                                        !autoTagsRefreshing
                                    "
                                    class="tag-hint"
                                >
                                    No auto tags yet.
                                </span>
                                <button
                                    type="button"
                                    class="btn-ghost auto-tag-refresh-btn"
                                    :disabled="
                                        dirty || saving || autoTagsRefreshing
                                    "
                                    @click="refreshAutoTags()"
                                >
                                    {{
                                        autoTagsRefreshing
                                            ? "Refreshing…"
                                            : "Refresh"
                                    }}
                                </button>
                                <span
                                    v-if="dirty"
                                    class="tag-hint auto-tag-hint"
                                >
                                    Save first to refresh from unsaved edits.
                                </span>
                                <span
                                    v-else-if="autoTagsInfo"
                                    class="tag-hint auto-tag-hint"
                                >
                                    {{ autoTagsInfo }}
                                </span>
                                <span
                                    v-if="autoTagsError"
                                    class="save-error auto-tag-error"
                                >
                                    {{ autoTagsError }}
                                </span>
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
                                    @keydown="onParentFieldKeydown"
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
                                        (parentSearchActive ||
                                            parentOptions.length > 0 ||
                                            parentSearching)
                                    "
                                    class="parent-dropdown"
                                >
                                    <div
                                        v-if="parentSearchStatusMessage"
                                        class="parent-dropdown-status"
                                    >
                                        {{ parentSearchStatusMessage }}
                                    </div>
                                    <template
                                        v-for="section in parentSectionGroups"
                                        :key="section.key"
                                    >
                                                                        <button
                                                                            type="button"
                                                                            class="parent-dropdown-section-label"
                                                                            :class="{
                                                                                collapsed: section.collapsed,
                                                                                highlighted:
                                                                                    parentKeyboardMode &&
                                                                                    parentHighlightedIndex >= 0 &&
                                                                                    parentSelectableEntries[
                                                                                        parentHighlightedIndex
                                                                                    ]?.kind === 'section' &&
                                                                                    parentSelectableEntries[
                                                                                        parentHighlightedIndex
                                                                                    ]?.key === section.key,
                                                                            }"
                                                                            @click="toggleParentSearchSection(section.key)"
                                                                        >
                                            <span class="parent-dropdown-section-heading">
                                                <span class="parent-dropdown-caret">{{
                                                    section.collapsed ? '▸' : '▾'
                                                }}</span>
                                                <span>{{ section.label }}</span>
                                            </span>
                                            <span class="parent-dropdown-section-count">{{
                                                section.items.length
                                            }}</span>
                                        </button>
                                        <div
                                            v-if="!section.collapsed"
                                            v-for="item in section.items"
                                            :key="item.key"
                                            class="parent-dropdown-item"
                                            :class="{
                                                highlighted:
                                                    parentKeyboardMode &&
                                                    parentHighlightedIndex >= 0 &&
                                                    parentSelectableEntries[
                                                        parentHighlightedIndex
                                                    ]?.kind === 'result' &&
                                                    parentSelectableEntries[
                                                        parentHighlightedIndex
                                                    ]?.item?.key === item.key,
                                            }"
                                            @click="selectParent(item.result)"
                                            @mouseenter="
                                                parentHighlightedIndex =
                                                    item.flatIndex
                                            "
                                        >
                                            <span class="parent-dropdown-title-row">
                                                <span class="parent-dropdown-title">{{
                                                    item.result.title ||
                                                    "Untitled"
                                                }}</span>
                                                <span
                                                    v-if="
                                                        item.result.type &&
                                                        item.result.type !==
                                                            'standard'
                                                    "
                                                    class="search-result-type"
                                                >
                                                    {{
                                                        noteTypeLabel(
                                                            item.result.type,
                                                        )
                                                    }}
                                                </span>
                                            </span>
                                            <span
                                                v-if="item.result.path"
                                                class="parent-dropdown-meta"
                                                >{{ item.result.path }}</span
                                            >
                                            <span
                                                v-if="
                                                    item.result.tags &&
                                                    item.result.tags.length
                                                "
                                                class="parent-dropdown-meta"
                                                >{{
                                                    formatSearchTags(
                                                        item.result.tags,
                                                    )
                                                }}</span
                                            >
                                        </div>
                                    </template>
                                    <div
                                        v-if="
                                            parentSearchActive &&
                                            parentSearch.trim() &&
                                            !parentSearching &&
                                            parentOptions.length === 0
                                        "
                                        class="parent-dropdown-item muted"
                                    >
                                        No matching parent notes
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
                        <button
                            class="btn-ghost shortcut-anchor"
                            :title="getShortcutLabel('toggle-edit')"
                            @click="toggleEdit"
                        >
                            {{ isEditing ? "🖉 View" : "✎ Edit" }}
                            <ShortcutHint
                                v-if="shortcutHintsVisible"
                                :label="getHintLabel('toggle-edit')"
                            />
                        </button>
                        <button
                            class="btn-amber btn-child shortcut-anchor"
                            :title="getShortcutLabel('new-child-note')"
                            @click="newChildNote"
                        >
                            + Child
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('new-child-note')
                                "
                                :label="getHintLabel('new-child-note')"
                            />
                        </button>
                        <button
                            class="btn-ghost shortcut-anchor"
                            :title="getShortcutLabel('attach-file')"
                            @click="onAttachFile"
                            :disabled="!selected?.id"
                        >
                            📎 Attach
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('attach-file')
                                "
                                :label="getHintLabel('attach-file')"
                            />
                        </button>
                        <button
                            class="btn-primary shortcut-anchor"
                            :title="getShortcutLabel('save-note')"
                            :disabled="!dirty || saving"
                            @click="save"
                        >
                            {{ saving ? "Saving…" : "Save" }}
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('save-note')
                                "
                                :label="getHintLabel('save-note')"
                            />
                        </button>
                        <button
                            class="btn-ghost shortcut-anchor"
                            :class="{ active: showHistory }"
                            :title="getShortcutLabel('toggle-history')"
                            @click="toggleHistory"
                        >
                            History
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('toggle-history')
                                "
                                :label="getHintLabel('toggle-history')"
                            />
                        </button>
                        <button
                            class="btn-ghost pin-editor-btn shortcut-anchor"
                            :class="{ pinned: selected?.pinned }"
                            :title="getShortcutLabel('toggle-pin')"
                            @click="togglePin(selected)"
                        >
                            📌
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('toggle-pin')
                                "
                                :label="getHintLabel('toggle-pin')"
                            />
                        </button>
                        <button
                            class="btn-danger shortcut-anchor"
                            :title="getShortcutLabel('delete-note')"
                            @click="confirmDelete"
                        >
                            Delete
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('delete-note')
                                "
                                :label="getHintLabel('delete-note')"
                            />
                        </button>
                    </div>
                </div>
                <p v-if="saveError" class="save-error">{{ saveError }}</p>
                <p v-else-if="liveRefreshPending" class="save-info">
                    A newer version of this note is available. Save or leave
                    this note to refresh it.
                </p>

                <!-- Chat Feed -->
                <div
                    ref="mainChatFeedEl"
                    class="chat-feed"
                    @scroll="scheduleLinkHintRefresh"
                >
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
                                    :title="getShortcutLabel('focus-note-body')"
                                    placeholder="Write your note here… (drag files here)"
                                    @input="onLinkEditorInput('body')"
                                    @click="
                                        onLinkEditorCaretMove('body', $event)
                                    "
                                    @keyup="
                                        onLinkEditorCaretMove('body', $event)
                                    "
                                    @keydown="onLinkEditorKeydown('body', $event)"
                                    @scroll="onLinkEditorScroll('body')"
                                                    @dragover.prevent
                                                    @drop.prevent="onBodyDrop"
                                                    @paste="onBodyPaste"
                                />
                                <ShortcutHint
                                    v-if="
                                        shortcutHintsVisible &&
                                        isShortcutEnabled('focus-note-body')
                                    "
                                    :label="getHintLabel('focus-note-body')"
                                    position="top-left"
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
                                        v-if="!linkSearchQuery.trim()"
                                        class="link-search-status"
                                    >
                                        Start typing to search notes…
                                    </div>
                                    <div
                                        v-if="linkSearchStatusMessage"
                                        class="link-search-status"
                                    >
                                        {{ linkSearchStatusMessage }}
                                    </div>
                                    <template
                                        v-for="section in linkSearchSectionGroups"
                                        :key="section.key"
                                    >
                                        <button
                                            type="button"
                                            class="link-search-section-header"
                                            :class="{
                                                collapsed: section.collapsed,
                                                highlighted:
                                                    linkKeyboardMode &&
                                                    linkSearchIndex >= 0 &&
                                                    linkSelectableEntries[
                                                        linkSearchIndex
                                                    ]?.kind === 'section' &&
                                                    linkSelectableEntries[
                                                        linkSearchIndex
                                                    ]?.key === section.key,
                                            }"
                                            @click="toggleLinkSearchSection(section.key)"
                                        >
                                            <span class="link-search-section-heading">
                                                <span class="link-search-caret">{{
                                                    section.collapsed ? '▸' : '▾'
                                                }}</span>
                                                <span>{{ section.label }}</span>
                                            </span>
                                            <span>{{ section.items.length }}</span>
                                        </button>
                                        <div
                                            v-if="!section.collapsed"
                                            v-for="item in section.items"
                                            :key="item.key"
                                            class="link-search-item"
                                            :class="{
                                                highlighted:
                                                    linkKeyboardMode &&
                                                    linkSearchIndex >= 0 &&
                                                    linkSelectableEntries[
                                                        linkSearchIndex
                                                    ]?.kind === 'result' &&
                                                    linkSelectableEntries[
                                                        linkSearchIndex
                                                    ]?.item?.key === item.key,
                                            }"
                                            @click="selectLinkResult(item.result)"
                                            @mouseenter="
                                                linkSearchIndex = item.flatIndex
                                            "
                                        >
                                            <div class="link-search-body">
                                                <span class="link-search-title-row">
                                                    <span class="link-search-title">{{
                                                        item.result.title ||
                                                        "Untitled"
                                                    }}</span>
                                                    <span
                                                        v-if="
                                                            item.result.type &&
                                                            item.result.type !==
                                                                'standard'
                                                        "
                                                        class="search-result-type"
                                                    >
                                                        {{
                                                            noteTypeLabel(
                                                                item.result.type,
                                                            )
                                                        }}
                                                    </span>
                                                </span>
                                                <span
                                                    v-if="item.result.path"
                                                    class="link-search-meta"
                                                    >{{ item.result.path }}</span
                                                >
                                                <span
                                                    v-if="
                                                        item.result.tags &&
                                                        item.result.tags.length
                                                    "
                                                    class="link-search-meta"
                                                    >{{
                                                        formatSearchTags(
                                                            item.result.tags,
                                                        )
                                                    }}</span
                                                >
                                            </div>
                                            <span class="link-search-relevance">{{
                                                relevancePct(
                                                    item.result.distance,
                                                )
                                            }}</span>
                                        </div>
                                    </template>
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
                            :key="selectedRendererKey"
                            :note="selected"
                            :token="token"
                            :editing="isEditing"
                            :dirty="dirty"
                            :customData="customData"
                            :uiSchema="
                                selected.ui_schema || selected.plugin?.view
                            "
                            :actionError="saveError"
                            @selectNote="(id) => selectNoteById(id)"
                            @update:customData="
                                (d) => {
                                    customData = d;
                                    dirty = true;
                                }
                            "
                            @import:recipes="importRecipes"
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
                            <span
                                class="message-author clickable"
                                @click="selectNote(child)"
                                title="Open in main view"
                                >{{ child.title || "Untitled" }}</span
                            >
                            <span class="message-date">{{
                                fmtDateFull(child.created_at)
                            }}</span>
                        </div>
                        <div
                            class="message-body markdown-body"
                            v-html="renderMarkdown(child.body, child.attachments)"
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
                    <div
                        v-if="
                            !childrenLoaded &&
                            isLazyChildren(selected?.type) &&
                            selected.id
                        "
                        class="chat-status"
                    >
                        <button
                            class="btn-ghost btn-sm"
                            @click="loadChildren(selected.id)"
                        >
                            📋 Load children
                        </button>
                    </div>
                    <div v-else-if="childrenLoading" class="chat-status">
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
                    <div class="shortcut-anchor composer-title-anchor">
                        <input
                            ref="newReplyTitleInput"
                            v-model="newReplyTitle"
                            class="composer-title"
                            :title="getShortcutLabel('focus-reply-title')"
                            placeholder="Reply title (optional — auto-generated if blank)"
                            @keydown.enter.exact="sendReply"
                        />
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('focus-reply-title')
                            "
                            :label="getHintLabel('focus-reply-title')"
                            position="top-left"
                        />
                    </div>
                    <div class="composer-body-row shortcut-anchor">
                        <textarea
                            ref="newReplyTextarea"
                            v-model="newReplyBody"
                            class="composer-textarea"
                            :title="getShortcutLabel('focus-reply-body')"
                            placeholder="Write a reply…"
                            rows="2"
                            @input="onLinkEditorInput('reply')"
                            @click="onLinkEditorCaretMove('reply', $event)"
                            @keyup="onLinkEditorCaretMove('reply', $event)"
                            @keydown="onLinkEditorKeydown('reply', $event)"
                            @scroll="onLinkEditorScroll('reply')"
                            @keydown.enter.meta.exact="sendReply"
                            @keydown.enter.ctrl.exact="sendReply"
                        />
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('focus-reply-body')
                            "
                            :label="getHintLabel('focus-reply-body')"
                            position="top-left"
                        />
                        <button
                            class="btn-primary composer-send shortcut-anchor"
                            :title="getShortcutLabel('send-reply')"
                            :disabled="!canSendReply"
                            @click="sendReply"
                        >
                            {{ sendingReply ? "…" : "Send" }}
                            <ShortcutHint
                                v-if="
                                    shortcutHintsVisible &&
                                    isShortcutEnabled('send-reply')
                                "
                                :label="getHintLabel('send-reply')"
                                position="top-left"
                            />
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
                                v-if="!linkSearchQuery.trim()"
                                class="link-search-status"
                            >
                                Start typing to search notes…
                            </div>
                            <div
                                v-if="linkSearchStatusMessage"
                                class="link-search-status"
                            >
                                {{ linkSearchStatusMessage }}
                            </div>
                            <template
                                v-for="section in linkSearchSectionGroups"
                                :key="section.key"
                            >
                                <button
                                    type="button"
                                    class="link-search-section-header"
                                    :class="{
                                        collapsed: section.collapsed,
                                        highlighted:
                                            linkKeyboardMode &&
                                            linkSearchIndex >= 0 &&
                                            linkSelectableEntries[
                                                linkSearchIndex
                                            ]?.kind === 'section' &&
                                            linkSelectableEntries[
                                                linkSearchIndex
                                            ]?.key === section.key,
                                    }"
                                    @click="toggleLinkSearchSection(section.key)"
                                >
                                    <span class="link-search-section-heading">
                                        <span class="link-search-caret">{{
                                            section.collapsed ? '▸' : '▾'
                                        }}</span>
                                        <span>{{ section.label }}</span>
                                    </span>
                                    <span>{{ section.items.length }}</span>
                                </button>
                                <div
                                    v-if="!section.collapsed"
                                    v-for="item in section.items"
                                    :key="item.key"
                                    class="link-search-item"
                                    :class="{
                                        highlighted:
                                            linkKeyboardMode &&
                                            linkSearchIndex >= 0 &&
                                            linkSelectableEntries[
                                                linkSearchIndex
                                            ]?.kind === 'result' &&
                                            linkSelectableEntries[
                                                linkSearchIndex
                                            ]?.item?.key === item.key,
                                    }"
                                    @click="selectLinkResult(item.result)"
                                    @mouseenter="
                                        linkSearchIndex = item.flatIndex
                                    "
                                >
                                    <div class="link-search-body">
                                        <span class="link-search-title-row">
                                            <span class="link-search-title">{{
                                                item.result.title ||
                                                "Untitled"
                                            }}</span>
                                            <span
                                                v-if="
                                                    item.result.type &&
                                                    item.result.type !==
                                                        'standard'
                                                "
                                                class="search-result-type"
                                            >
                                                {{
                                                    noteTypeLabel(
                                                        item.result.type,
                                                    )
                                                }}
                                            </span>
                                        </span>
                                        <span
                                            v-if="item.result.path"
                                            class="link-search-meta"
                                            >{{ item.result.path }}</span
                                        >
                                        <span
                                            v-if="
                                                item.result.tags &&
                                                item.result.tags.length
                                            "
                                            class="link-search-meta"
                                            >{{
                                                formatSearchTags(
                                                    item.result.tags,
                                                )
                                            }}</span
                                        >
                                    </div>
                                    <span class="link-search-relevance">{{
                                        relevancePct(item.result.distance)
                                    }}</span>
                                </div>
                            </template>
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
                    class="btn-ghost icon-btn shortcut-anchor"
                    :title="getShortcutLabel('close-thread')"
                    @click="closeThreadSidebar"
                >
                    ✕
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('close-thread')
                        "
                        :label="getHintLabel('close-thread')"
                    />
                </button>
                <span class="thread-sidebar-title">Thread</span>
                <button
                    class="btn-ghost icon-btn shortcut-anchor"
                    :title="getShortcutLabel('open-thread-main-view')"
                    @click="selectNote(threadNote)"
                >
                    ⤢
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('open-thread-main-view')
                        "
                        :label="getHintLabel('open-thread-main-view')"
                    />
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
            <div
                ref="threadChatFeedEl"
                class="chat-feed"
                @scroll="scheduleLinkHintRefresh"
            >
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
                        v-html="renderMarkdown(threadNote.body, threadNote.attachments)"
                    />
                    <NoteTypeRenderer
                        :key="threadRendererKey"
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
                        <span
                            class="message-author clickable"
                            @click="selectThreadChild(tc)"
                            title="Open in thread"
                            >{{ tc.title || "Untitled" }}</span
                        >
                        <span class="message-date">{{
                            fmtDateFull(tc.created_at)
                        }}</span>
                    </div>
                    <div
                        class="message-body markdown-body"
                        v-html="renderMarkdown(tc.body, tc.attachments)"
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

                <div
                    v-if="
                        !threadChildrenLoaded &&
                        isLazyChildren(threadNote?.type)
                    "
                    class="chat-status"
                >
                    <button
                        class="btn-ghost btn-sm"
                        @click="loadThreadChildren"
                    >
                        📋 Load children
                    </button>
                </div>
                <div v-else-if="threadChildrenLoading" class="chat-status">
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
                <div class="shortcut-anchor composer-title-anchor">
                    <input
                        ref="threadReplyTitleInput"
                        v-model="threadReplyTitle"
                        class="composer-title"
                        :title="getShortcutLabel('focus-thread-reply-title')"
                        placeholder="Reply title (optional — auto-generated if blank)"
                    />
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('focus-thread-reply-title')
                        "
                        :label="getHintLabel('focus-thread-reply-title')"
                        position="top-left"
                    />
                </div>
                <div class="composer-body-row shortcut-anchor">
                    <textarea
                        ref="threadReplyTextarea"
                        v-model="threadReplyBody"
                        class="composer-textarea"
                        :title="getShortcutLabel('focus-thread-reply-body')"
                        placeholder="Write a reply…"
                        rows="2"
                        @input="onLinkEditorInput('threadReply')"
                        @click="onLinkEditorCaretMove('threadReply', $event)"
                        @keyup="onLinkEditorCaretMove('threadReply', $event)"
                        @keydown="onLinkEditorKeydown('threadReply', $event)"
                        @scroll="onLinkEditorScroll('threadReply')"
                        @keydown.enter.meta.exact="sendThreadReply"
                        @keydown.enter.ctrl.exact="sendThreadReply"
                    />
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('focus-thread-reply-body')
                        "
                        :label="getHintLabel('focus-thread-reply-body')"
                        position="top-left"
                    />
                    <button
                        class="btn-primary composer-send shortcut-anchor"
                        :title="getShortcutLabel('send-thread-reply')"
                        :disabled="!canSendThreadReply"
                        @click="sendThreadReply"
                    >
                        {{ threadSendingReply ? "…" : "Send" }}
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('send-thread-reply')
                            "
                            :label="getHintLabel('send-thread-reply')"
                            position="top-left"
                        />
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
                            v-if="!linkSearchQuery.trim()"
                            class="link-search-status"
                        >
                            Start typing to search notes…
                        </div>
                        <div
                            v-if="linkSearchStatusMessage"
                            class="link-search-status"
                        >
                            {{ linkSearchStatusMessage }}
                        </div>
                        <template
                            v-for="section in linkSearchSectionGroups"
                            :key="section.key"
                        >
                            <button
                                type="button"
                                class="link-search-section-header"
                                :class="{
                                    collapsed: section.collapsed,
                                    highlighted:
                                        linkKeyboardMode &&
                                        linkSearchIndex >= 0 &&
                                        linkSelectableEntries[
                                            linkSearchIndex
                                        ]?.kind === 'section' &&
                                        linkSelectableEntries[
                                            linkSearchIndex
                                        ]?.key === section.key,
                                }"
                                @click="toggleLinkSearchSection(section.key)"
                            >
                                <span class="link-search-section-heading">
                                    <span class="link-search-caret">{{
                                        section.collapsed ? '▸' : '▾'
                                    }}</span>
                                    <span>{{ section.label }}</span>
                                </span>
                                <span>{{ section.items.length }}</span>
                            </button>
                            <div
                                v-if="!section.collapsed"
                                v-for="item in section.items"
                                :key="item.key"
                                class="link-search-item"
                                :class="{
                                    highlighted:
                                        linkKeyboardMode &&
                                        linkSearchIndex >= 0 &&
                                        linkSelectableEntries[
                                            linkSearchIndex
                                        ]?.kind === 'result' &&
                                        linkSelectableEntries[
                                            linkSearchIndex
                                        ]?.item?.key === item.key,
                                }"
                                @click="selectLinkResult(item.result)"
                                @mouseenter="linkSearchIndex = item.flatIndex"
                            >
                                <div class="link-search-body">
                                    <span class="link-search-title-row">
                                        <span class="link-search-title">{{
                                            item.result.title || "Untitled"
                                        }}</span>
                                        <span
                                            v-if="
                                                item.result.type &&
                                                item.result.type !== 'standard'
                                            "
                                            class="search-result-type"
                                        >
                                            {{ noteTypeLabel(item.result.type) }}
                                        </span>
                                    </span>
                                    <span
                                        v-if="item.result.path"
                                        class="link-search-meta"
                                        >{{ item.result.path }}</span
                                    >
                                    <span
                                        v-if="
                                            item.result.tags &&
                                            item.result.tags.length
                                        "
                                        class="link-search-meta"
                                        >{{
                                            formatSearchTags(item.result.tags)
                                        }}</span
                                    >
                                </div>
                                <span class="link-search-relevance">{{
                                    relevancePct(item.result.distance)
                                }}</span>
                            </div>
                        </template>
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
                    <button
                        class="btn-ghost shortcut-anchor"
                        :title="getShortcutLabel('cancel-delete')"
                        @click="showDeleteModal = false"
                    >
                        Cancel
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('cancel-delete')
                            "
                            :label="getHintLabel('cancel-delete')"
                        />
                    </button>
                    <button
                        class="btn-danger shortcut-anchor"
                        :title="getShortcutLabel('confirm-delete')"
                        :disabled="deleting"
                        @click="doDelete"
                    >
                        {{ deleting ? "Deleting…" : "Delete" }}
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('confirm-delete')
                            "
                            :label="getHintLabel('confirm-delete')"
                        />
                    </button>
                </div>
            </div>
        </div>

        <LinkHintOverlay
            v-if="linkHintMode"
            :items="linkHintEntries"
            :typedBuffer="linkHintTyped"
        />
        <KeyboardShortcutsHelpModal v-model="showHotkeys" :items="hotkeys" />

        <!-- Upload modal -->
        <UploadModal
            :visible="showUploadModal"
            :note-id="selected?.id"
            :token="token"
            @close="showUploadModal = false"
            @uploaded="onChunkedUploadComplete"
        />

        <!-- Upload progress panel -->
        <UploadProgress />
    </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from "vue";
import MarkdownIt from "markdown-it";
import markdownItFootnote from "markdown-it-footnote";
import spoilerPlugin from "../spoilerPlugin.js";
const md = new MarkdownIt({ html: false, linkify: true, breaks: true }).use(
    markdownItFootnote,
).use(spoilerPlugin);
import {
    fetchNotes,
    fetchNote,
    createNote,
    updateNote,
    deleteNote,
    fetchNoteHistory,
    fetchChildren,
    fetchAncestors,
    streamSearchNotes,
    setNotePin,
    fetchTags,
    generateAutoTags,
    pluginActionV2,
} from "../api.js";
import NoteTypeRenderer from "../components/NoteTypeRenderer.vue";
import NoteAttachments from "../components/NoteAttachments.vue";
import ShortcutHint from "../components/ShortcutHint.vue";
import LinkHintOverlay from "../components/LinkHintOverlay.vue";
import KeyboardShortcutsHelpModal from "../components/KeyboardShortcutsHelpModal.vue";
import UploadModal from "../components/UploadModal.vue";
import UploadProgress from "../components/UploadProgress.vue";
import { useUploadQueue } from "../composables/useUploadQueue.js";
import {
    deleteAttachment,
} from "../api.js";
import {
    getTypeOptions,
    getNoteTypeOrDefault,
    getDefaultChildType,
    isLazyChildren,
    fetchAndMergeManifests,
} from "../note-types/registry.js";
import {
    isEditableElement,
    useKeyboardShortcuts,
} from "../composables/useKeyboardShortcuts.js";

const props = defineProps({
    token: String,
    wsConnected: Boolean,
    wsLatency: Number,
    wsLatencyDetail: Object,
});

const wsIndicatorTitle = computed(() => {
    if (!props.wsConnected) return "Disconnected";
    if (props.wsLatency == null) return "Connected";
    const parts = [`RTT ${props.wsLatency} ms`];
    if (props.wsLatencyDetail?.serverProcessingMs != null) {
        parts.push(`server ${props.wsLatencyDetail.serverProcessingMs} ms`);
    }
    return `Connected (${parts.join(", ")})`;
});
const emit = defineEmits(["logout", "navigate-options"]);

const notes = ref([]);
const loading = ref(false);
const selected = ref(null);
const editTitle = ref("");
const editBody = ref("");
const noteType = ref("standard");
const customData = ref(null);
const dirty = ref(false);
const saving = ref(false);
const liveRefreshPending = ref(false);
const selectedRendererKey = ref(0);
const threadRendererKey = ref(0);
const editTitleInput = ref(null);
const UNSAVED_NOTE_WARNING =
    "You have unsaved changes in this note. Leave without saving?";
const searchInputEl = ref(null);
const bodyTextarea = ref(null);
const newReplyTitleInput = ref(null);
const newReplyTextarea = ref(null);
const threadReplyTitleInput = ref(null);
const threadReplyTextarea = ref(null);
const mainChatFeedEl = ref(null);
const threadChatFeedEl = ref(null);

// Tags state
const editTags = ref([]);
const tagSearch = ref("");
const tagOptions = ref([]);
const autoTagsRefreshing = ref(false);
const autoTagsError = ref("");
const autoTagsInfo = ref("");

// Upload queue composable + upload modal
const { enqueueAttachment, enqueueInline, enqueueMultipleInline, resumeStoredUploads, active: activeUploads } = useUploadQueue();
const showUploadModal = ref(false);

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
const history = ref([]);
const historyLoading = ref(false);

// Search state
const searchQuery = ref("");
const searchSections = ref([]);
const searchCollapsedSections = ref({});
const searchStatusMessage = ref("");
const searchError = ref("");
const searching = ref(false);
const highlightedIndex = ref(-1);
const searchKeyboardMode = ref(false);
const searchSelectedTypes = ref(["standard"]);
const activeSearchSectionKey = ref("");
const searchResults = computed(() =>
    flattenVisibleSectionGroups(searchSectionGroups.value),
);
let searchTimeout = null;
const sidebarSearchRequest = { controller: null };

// [[ and // link search state
const linkSearchQuery = ref("");
const linkSearchSections = ref([]);
const linkSearchCollapsedSections = ref({});
const linkSearchStatusMessage = ref("");
const linkSearchResults = computed(() =>
    flattenVisibleSectionGroups(linkSearchSectionGroups.value),
);
const linkSearching = ref(false);
const linkKeyboardMode = ref(false);
const linkSearchIndex = ref(-1);
const linkSearchVisible = ref(false);
const linkSearchTarget = ref(null);
const linkSearchTriggerType = ref(null);
const linkSearchTriggerStart = ref(-1);
const linkPopupStyle = ref({ left: "20px", top: "20px" });
let linkSearchTimeout = null;
const linkSearchRequest = { controller: null };

const linkHintMode = ref(false);
const linkHintTyped = ref("");
const linkHintEntries = ref([]);
const LINK_HINT_DIGITS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "0"];
let linkHintRefreshFrame = 0;

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

function normalizeLinkHintKey(key) {
    if (!key) return "";
    return key.length === 1 ? key.toUpperCase() : key;
}

function linkHintAlphaPrefix(index) {
    let value = Number(index) + 1;
    let label = "";
    while (value > 0) {
        const remainder = (value - 1) % 26;
        label = String.fromCharCode(65 + remainder) + label;
        value = Math.floor((value - 1) / 26);
    }
    return label;
}

function linkHintLabelForIndex(index) {
    if (index < LINK_HINT_DIGITS.length) {
        return LINK_HINT_DIGITS[index];
    }
    const offset = index - LINK_HINT_DIGITS.length;
    const prefixIndex = Math.floor(offset / LINK_HINT_DIGITS.length);
    const digit = LINK_HINT_DIGITS[offset % LINK_HINT_DIGITS.length];
    return `${linkHintAlphaPrefix(prefixIndex)}${digit}`;
}

function isVisibleLinkHintElement(anchor) {
    if (!(anchor instanceof HTMLElement)) return false;
    const rect = anchor.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return false;
    const style = window.getComputedStyle(anchor);
    if (
        style.display === "none" ||
        style.visibility === "hidden" ||
        style.pointerEvents === "none"
    ) {
        return false;
    }
    return !(
        rect.bottom < 0 ||
        rect.right < 0 ||
        rect.top > window.innerHeight ||
        rect.left > window.innerWidth
    );
}

function collectLinkHintEntries() {
    const roots = [mainChatFeedEl.value, threadChatFeedEl.value].filter(Boolean);
    const entries = [];

    for (const root of roots) {
        const anchors = root.querySelectorAll(".markdown-body a[href]");
        for (const anchor of anchors) {
            if (!isVisibleLinkHintElement(anchor)) continue;
            const rect = anchor.getBoundingClientRect();
            const top = rect.top <= 28 ? rect.bottom + 6 : rect.top;
            entries.push({
                id: `link-hint-${entries.length}`,
                label: linkHintLabelForIndex(entries.length),
                top: Math.max(8, top),
                left: Math.max(8, Math.min(rect.left, window.innerWidth - 16)),
                element: anchor,
                href: anchor.getAttribute("href") || "",
            });
        }
    }

    return entries;
}

function cancelLinkHintRefresh() {
    if (!linkHintRefreshFrame) return;
    window.cancelAnimationFrame(linkHintRefreshFrame);
    linkHintRefreshFrame = 0;
}

function closeLinkHintMode() {
    linkHintMode.value = false;
    linkHintTyped.value = "";
    linkHintEntries.value = [];
    cancelLinkHintRefresh();
}

function refreshLinkHintEntries() {
    if (!linkHintMode.value) return;
    const entries = collectLinkHintEntries();
    linkHintEntries.value = entries;
    if (entries.length === 0) {
        closeLinkHintMode();
        return;
    }
    if (
        linkHintTyped.value &&
        !entries.some((entry) => entry.label.startsWith(linkHintTyped.value))
    ) {
        linkHintTyped.value = "";
    }
}

function scheduleLinkHintRefresh() {
    if (!linkHintMode.value) return;
    cancelLinkHintRefresh();
    linkHintRefreshFrame = window.requestAnimationFrame(() => {
        linkHintRefreshFrame = 0;
        refreshLinkHintEntries();
    });
}

async function openLinkHintMode() {
    hideHintOverlay();
    closeLinkSearch();
    linkHintTyped.value = "";
    linkHintMode.value = true;
    await nextTick();
    refreshLinkHintEntries();
}

function toggleLinkHintMode() {
    if (linkHintMode.value) {
        closeLinkHintMode();
        return;
    }
    openLinkHintMode();
}

function prefixLinkHintMatches(prefix) {
    if (!prefix) return linkHintEntries.value;
    return linkHintEntries.value.filter((entry) =>
        entry.label.startsWith(prefix),
    );
}

function openLinkHintEntry(entry) {
    const target = entry?.element;
    closeLinkHintMode();
    if (!target || !document.contains(target)) return;
    target.focus?.({ preventScroll: true });
    target.click();
}

function applyLinkHintInput(nextLabel) {
    const exact = linkHintEntries.value.find((entry) => entry.label === nextLabel);
    if (exact) {
        openLinkHintEntry(exact);
        return true;
    }
    const prefixMatches = prefixLinkHintMatches(nextLabel);
    if (prefixMatches.length > 0) {
        linkHintTyped.value = nextLabel;
        return true;
    }
    return false;
}

function onLinkHintKeyDown(event) {
    if (!linkHintMode.value || event.defaultPrevented) return;

    const normalizedKey = normalizeLinkHintKey(event.key);
    if (
        normalizedKey === "L" &&
        (event.ctrlKey || event.metaKey) &&
        !event.altKey
    ) {
        event.preventDefault();
        event.stopPropagation();
        closeLinkHintMode();
        return;
    }

    if (["Shift", "Control", "Alt", "Meta"].includes(event.key)) {
        return;
    }

    if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeLinkHintMode();
        return;
    }

    if (event.key === "Backspace") {
        event.preventDefault();
        event.stopPropagation();
        linkHintTyped.value = linkHintTyped.value.slice(0, -1);
        return;
    }

    if (event.key === "Enter") {
        const matches = prefixLinkHintMatches(linkHintTyped.value);
        if (matches.length === 1) {
            event.preventDefault();
            event.stopPropagation();
            openLinkHintEntry(matches[0]);
        }
        return;
    }

    if (!/^[A-Z0-9]$/.test(normalizedKey)) {
        return;
    }

    event.preventDefault();
    event.stopPropagation();

    if (applyLinkHintInput(`${linkHintTyped.value}${normalizedKey}`)) {
        return;
    }
    if (applyLinkHintInput(normalizedKey)) {
        return;
    }

    linkHintTyped.value = "";
}

function onLinkHintWindowBlur() {
    closeLinkHintMode();
}

// Children state
const children = ref([]);
const childrenLoading = ref(false);
const childrenLoaded = ref(false);

// Thread sidebar state
const threadNote = ref(null); // the note whose thread is shown in the right sidebar
const threadChildren = ref([]);
const threadChildrenLoading = ref(false);
const threadChildrenLoaded = ref(false);
const threadAncestors = ref([]);
const threadReplyTitle = ref("");
const threadReplyBody = ref("");
const threadSendingReply = ref(false);
const DOUBLE_CONTROL_OPEN_MS = 350;
let liveRefreshTimer = null;
let liveRefreshRunning = false;
let liveRefreshQueued = false;
let liveRefreshFullRequested = false;
let liveRefreshSelectedRequested = false;
let liveRefreshThreadRequested = false;
let threadSidebarCtrlTapArmed = false;
let lastThreadSidebarCtrlTapAt = 0;
let threadSidebarCtrlChordUsed = false;

// Reply composer state
const newReplyTitle = ref("");
const newReplyBody = ref("");
const sendingReply = ref(false);

function hasReplyDraft(title, body) {
    return Boolean(String(title || "").trim() || String(body || "").trim());
}

function applySelectedDetail(detail, { remount = true } = {}) {
    if (!detail) return;
    selected.value = {
        ...detail,
        tags: Array.isArray(detail.tags) ? detail.tags : [],
        auto_tags: Array.isArray(detail.auto_tags) ? detail.auto_tags : [],
    };
    editTitle.value = detail.title || "";
    editBody.value = detail.body || "";
    noteType.value = detail.type || "standard";
    customData.value = detail.plugin?.config || detail.custom_data || null;
    editTags.value = Array.isArray(detail.tags) ? [...detail.tags] : [];
    autoTagsRefreshing.value = false;
    autoTagsError.value = "";
    autoTagsInfo.value = "";
    liveRefreshPending.value = false;
    if (remount) {
        selectedRendererKey.value += 1;
    }
}

function applyThreadDetail(detail, { remount = true } = {}) {
    if (!detail) return;
    threadNote.value = detail;
    if (remount) {
        threadRendererKey.value += 1;
    }
}

const canSendReply = computed(
    () =>
        Boolean(selected.value?.id) &&
        !sendingReply.value &&
        hasReplyDraft(newReplyTitle.value, newReplyBody.value),
);

const canSendThreadReply = computed(
    () =>
        Boolean(threadNote.value?.id) &&
        !threadSendingReply.value &&
        hasReplyDraft(threadReplyTitle.value, threadReplyBody.value),
);

// Parent selector state
const parentSearch = ref("");
const parentSearchSections = ref([]);
const parentSearchCollapsedSections = ref({});
const parentSearchStatusMessage = ref("");
const parentOptions = computed(() =>
    flattenVisibleSectionGroups(parentSectionGroups.value),
);

const ancestors = ref([]);
const parentSearching = ref(false);
const showParentPicker = ref(false);
const parentSearchActive = ref(false);
const parentKeyboardMode = ref(false);
const parentHighlightedIndex = ref(-1);
let parentSearchTimeout = null;
const parentSearchRequest = { controller: null };

// Root notes (no parent_id) — shown in the sidebar
const rootNotes = computed(() =>
    notes.value.filter((n) => n.parent_id == null),
);

// The list currently shown in the sidebar (search results or root notes)
const allSearchTypeValues = computed(() => typeOptions.map((opt) => opt.value));
const searchMode = computed(() => parseSearchMode(searchQuery.value));
const hasSidebarSearch = computed(() => Boolean(searchMode.value.query));
const searchSelectableEntries = computed(() => {
    const entries = [];
    for (const section of searchSectionGroups.value) {
        if (section.collapsed) {
            entries.push({
                kind: "section",
                key: section.key,
                collapsed: true,
                section,
            });
            continue;
        }
        for (const item of section.items) {
            entries.push({
                kind: "result",
                key: item.key,
                item,
                section,
            });
        }
    }
    return entries;
});

const sidebarList = computed(() => {
    if (hasSidebarSearch.value) {
        return searchKeyboardMode.value
            ? searchSelectableEntries.value
            : searchResults.value;
    }
    return rootNotes.value;
});
const searchTypePickerVisible = computed(() => searchMode.value.useTypePicker);
const searchFilterSummary = computed(() => {
    if (!searchQuery.value.trim()) return "";
    if (searchMode.value.tagOnly) {
        return "Tag-only search. Add .i to choose note types or .a for all.";
    }
    if (searchMode.value.includeAllTypes) {
        return "Including all note types.";
    }
    if (searchMode.value.useTypePicker) {
        const labels = searchMode.value.types.map(noteTypeLabel);
        if (labels.length <= 3) {
            return `Including: ${labels.join(", ")}`;
        }
        return `Including ${labels.length} note types.`;
    }
    return "Searching standard notes only. Add .i to choose note types or .a for all.";
});
const searchSectionGroups = computed(() =>
    buildSectionGroups(searchSections.value, {
        collapsedMap: searchCollapsedSections.value,
    }),
);
const parentSectionGroups = computed(() =>
    buildSectionGroups(parentSearchSections.value, {
        collapsedMap: parentSearchCollapsedSections.value,
    }),
);
const parentSelectableEntries = computed(() => {
    const entries = [];
    for (const section of parentSectionGroups.value) {
        entries.push({
            kind: "section",
            key: section.key,
            collapsed: section.collapsed,
            section,
        });
        if (!section.collapsed) {
            for (const item of section.items) {
                entries.push({
                    kind: "result",
                    key: item.key,
                    item,
                    section,
                });
            }
        }
    }
    return entries;
});
const linkSearchSectionGroups = computed(() =>
    buildSectionGroups(linkSearchSections.value, {
        collapsedMap: linkSearchCollapsedSections.value,
    }),
);
const linkSelectableEntries = computed(() => {
    const entries = [];
    for (const section of linkSearchSectionGroups.value) {
        if (section.collapsed) {
            entries.push({
                kind: "section",
                key: section.key,
                collapsed: true,
                section,
            });
            continue;
        }
        for (const item of section.items) {
            entries.push({
                kind: "result",
                key: item.key,
                item,
                section,
            });
        }
    }
    return entries;
});

watch(searchTypePickerVisible, (visible) => {
    if (visible && searchSelectedTypes.value.length === 0) {
        searchSelectedTypes.value = ["standard"];
    }
});

function normalizeSearchTypes(types) {
    const normalized = [
        ...new Set((types || []).map((t) => String(t).trim())),
    ].filter(Boolean);
    return normalized.length > 0 ? normalized : ["standard"];
}

function parseSearchMode(rawQuery) {
    const tokens = String(rawQuery || "")
        .trim()
        .split(/\s+/)
        .filter(Boolean);
    const cleaned = [];
    let includeAllTypes = false;
    let useTypePicker = false;
    let tagOnly = false;

    for (const token of tokens) {
        if (token === ".a") {
            includeAllTypes = true;
            continue;
        }
        if (token === ".i") {
            useTypePicker = true;
            continue;
        }
        if (token === ".t") {
            tagOnly = true;
            continue;
        }
        cleaned.push(token);
    }

    return {
        query: cleaned.join(" ").trim(),
        includeAllTypes,
        useTypePicker: useTypePicker && !includeAllTypes,
        tagOnly,
        types: includeAllTypes
            ? null
            : useTypePicker
              ? normalizeSearchTypes(searchSelectedTypes.value)
              : ["standard"],
    };
}

function setSearchTypes(types) {
    searchSelectedTypes.value = normalizeSearchTypes(types);
    onSearchInput();
}

function toggleSearchType(type, enabled) {
    const next = new Set(searchSelectedTypes.value);
    if (enabled) {
        next.add(type);
    } else {
        next.delete(type);
    }
    searchSelectedTypes.value = Array.from(next);
    if (searchSelectedTypes.value.length === 0) {
        searchSelectedTypes.value = ["standard"];
    }
    onSearchInput();
}

function noteTypeLabel(type) {
    return typeOptions.find((opt) => opt.value === type)?.label || type;
}

function formatSearchTags(tags) {
    return tags.map((tag) => `#${tag}`).join(" ");
}

function flattenSearchSections(sections) {
    return (sections || []).flatMap((section) => section?.results || []);
}

function flattenVisibleSectionGroups(groups) {
    return (groups || []).flatMap((section) =>
        section?.collapsed ? [] : (section?.items || []).map((item) => item.result),
    );
}

function buildSectionGroups(
    sections,
    { includeFlatIndex = true, collapsedMap = {} } = {},
) {
    let flatIndex = 0;
    return (sections || []).map((section, sectionIndex) => {
        const collapsed = Boolean(collapsedMap?.[section?.key]);
        const items = (section?.results || []).map((result, itemIndex) => {
            const currentFlatIndex = includeFlatIndex ? flatIndex : itemIndex;
            if (!collapsed && includeFlatIndex) {
                flatIndex += 1;
            }
            return {
                key: `${section?.key || sectionIndex}:${result.id}:${itemIndex}`,
                result,
                flatIndex: currentFlatIndex,
            };
        });
        return {
            ...section,
            collapsed,
            items,
        };
    });
}

function setCollapsedSection(collapsedRef, key, collapsed) {
    collapsedRef.value = {
        ...collapsedRef.value,
        [key]: collapsed,
    };
}

function toggleCollapsedSection(collapsedRef, key) {
    setCollapsedSection(collapsedRef, key, !collapsedRef.value?.[key]);
}

function toggleSearchSection(key) {
    activeSearchSectionKey.value = key || "";
    toggleCollapsedSection(searchCollapsedSections, key);
    highlightedIndex.value = -1;
}

function selectableEntryKeyForIndex(index) {
    const entry = searchSelectableEntries.value[index];
    if (!entry) return "";
    if (entry.kind === "section") {
        return entry.key || "";
    }
    return entry.section?.key || "";
}

function toggleParentSearchSection(key) {
    toggleCollapsedSection(parentSearchCollapsedSections, key);
    parentHighlightedIndex.value = -1;
}

function selectableParentEntryKeyForIndex(index) {
    const entry = parentSelectableEntries.value[index];
    if (!entry) return "";
    if (entry.kind === "section") {
        return entry.key || "";
    }
    return entry.section?.key || "";
}

function collapseActiveParentSection() {
    if (!parentKeyboardMode.value || parentHighlightedIndex.value < 0) return false;
    const entry = parentSelectableEntries.value[parentHighlightedIndex.value];
    const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
    if (!key || parentSearchCollapsedSections.value?.[key]) return false;
    setCollapsedSection(parentSearchCollapsedSections, key, true);
    parentHighlightedIndex.value = parentSelectableEntries.value.findIndex(
        (candidate) => candidate.kind === "section" && candidate.key === key,
    );
    return parentHighlightedIndex.value >= 0;
}

function expandActiveParentSection() {
    if (!parentKeyboardMode.value || parentHighlightedIndex.value < 0) return false;
    const entry = parentSelectableEntries.value[parentHighlightedIndex.value];
    const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
    if (!key || !parentSearchCollapsedSections.value?.[key]) return false;
    setCollapsedSection(parentSearchCollapsedSections, key, false);
    parentHighlightedIndex.value = parentSelectableEntries.value.findIndex(
        (candidate) => candidate.kind === "section" && candidate.key === key,
    );
    return parentHighlightedIndex.value >= 0;
}

function toggleLinkSearchSection(key) {
    toggleCollapsedSection(linkSearchCollapsedSections, key);
    linkSearchIndex.value = -1;
}

function collapseActiveLinkSection() {
    if (!linkKeyboardMode.value || linkSearchIndex.value < 0) return false;
    const entry = linkSelectableEntries.value[linkSearchIndex.value];
    const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
    if (!key || linkSearchCollapsedSections.value?.[key]) return false;
    setCollapsedSection(linkSearchCollapsedSections, key, true);
    linkSearchIndex.value = linkSelectableEntries.value.findIndex(
        (candidate) => candidate.kind === "section" && candidate.key === key,
    );
    return linkSearchIndex.value >= 0;
}

function expandActiveLinkSection() {
    if (!linkKeyboardMode.value || linkSearchIndex.value < 0) return false;
    const entry = linkSelectableEntries.value[linkSearchIndex.value];
    const key = entry?.kind === "section" ? entry.key : entry?.section?.key;
    if (!key || !linkSearchCollapsedSections.value?.[key]) return false;
    setCollapsedSection(linkSearchCollapsedSections, key, false);
    linkSearchIndex.value = linkSelectableEntries.value.findIndex(
        (candidate) => candidate.kind === "result" && candidate.section?.key === key,
    );
    return linkSearchIndex.value >= 0;
}

function sectionKeyForFlatIndex(groups, flatIndex) {
    if (flatIndex == null || flatIndex < 0) return "";
    for (const section of groups || []) {
        if (section?.collapsed) continue;
        for (const item of section?.items || []) {
            if (item.flatIndex === flatIndex) {
                return section.key || "";
            }
        }
    }
    return "";
}

function firstFlatIndexForSection(groups, key) {
    for (const section of groups || []) {
        if (section?.key !== key || section?.collapsed) continue;
        return section?.items?.[0]?.flatIndex ?? -1;
    }
    return -1;
}

function collapseActiveSearchSection() {
    if (!searchKeyboardMode.value || highlightedIndex.value < 0) return false;
    const entry = searchSelectableEntries.value[highlightedIndex.value];
    const key =
        entry?.kind === "section"
            ? entry.key
            : entry?.section?.key || activeSearchSectionKey.value;
    if (!key || searchCollapsedSections.value?.[key]) return false;
    activeSearchSectionKey.value = key;
    setCollapsedSection(searchCollapsedSections, key, true);
    highlightedIndex.value = searchSelectableEntries.value.findIndex(
        (candidate) => candidate.kind === "section" && candidate.key === key,
    );
    return highlightedIndex.value >= 0;
}

function expandActiveSearchSection() {
    if (!searchKeyboardMode.value || highlightedIndex.value < 0) return false;
    const entry = searchSelectableEntries.value[highlightedIndex.value];
    const key =
        entry?.kind === "section"
            ? entry.key
            : entry?.section?.key || activeSearchSectionKey.value;
    if (!key || !searchCollapsedSections.value?.[key]) return false;
    activeSearchSectionKey.value = key;
    setCollapsedSection(searchCollapsedSections, key, false);
    highlightedIndex.value = searchSelectableEntries.value.findIndex(
        (candidate) =>
            candidate.kind === "result" && candidate.section?.key === key,
    );
    return highlightedIndex.value >= 0;
}

function onSearchFieldKeydown(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        searchKeyboardMode.value = true;
        handleArrowShortcut(event);
        return;
    }
    if (event.key === "Enter") {
        handleEnterShortcut(event);
        return;
    }
    if (!searchKeyboardMode.value) {
        return;
    }
    if (event.key === "ArrowLeft") {
        event.preventDefault();
        collapseActiveSearchSection();
        return;
    }
    if (event.key === "ArrowRight") {
        event.preventDefault();
        expandActiveSearchSection();
        return;
    }
    exitSearchSelectionMode();
}

function onParentFieldKeydown(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        parentKeyboardMode.value = true;
        handleArrowShortcut(event);
        return;
    }
    if (event.key === "Enter") {
        handleEnterShortcut(event);
        return;
    }
    if (!parentKeyboardMode.value) {
        return;
    }
    if (event.key === "ArrowLeft") {
        event.preventDefault();
        collapseActiveParentSection();
        return;
    }
    if (event.key === "ArrowRight") {
        event.preventDefault();
        expandActiveParentSection();
    }
}

function normalizeSearchSection(section, { filterResult = null } = {}) {
    const results = Array.isArray(section?.results)
        ? section.results.filter((result) =>
              filterResult ? filterResult(result) : true,
          )
        : [];
    if (results.length === 0) {
        return null;
    }
    return {
        key: section?.key || `section-${results[0]?.id || "results"}`,
        label: section?.label || "Results",
        description: section?.description || "",
        results,
    };
}

function abortSearchRequest(store) {
    if (store?.controller) {
        store.controller.abort();
        store.controller = null;
    }
}

async function runStreamedSearch({
    query,
    types = null,
    tagOnly = false,
    sectionsRef,
    statusRef,
    searchingRef,
    requestStore,
    errorRef = null,
    filterResult = null,
    onFirstResult = null,
    onReset = null,
    onDone = null,
}) {
    const trimmed = String(query || "").trim();
    abortSearchRequest(requestStore);

    if (!trimmed) {
        sectionsRef.value = [];
        statusRef.value = "";
        searchingRef.value = false;
        if (errorRef) {
            errorRef.value = "";
        }
        onReset?.();
        return;
    }

    const controller = new AbortController();
    requestStore.controller = controller;
    sectionsRef.value = [];
    statusRef.value = "Searching exact matches…";
    if (errorRef) {
        errorRef.value = "";
    }
    searchingRef.value = true;
    let firstResultDelivered = false;

    try {
        await streamSearchNotes(props.token, trimmed, {
            types,
            tagOnly,
            signal: controller.signal,
            onStatus(event) {
                if (requestStore.controller !== controller) return;
                statusRef.value = event?.message || "";
            },
            onSection(section) {
                if (requestStore.controller !== controller) return;
                const normalized = normalizeSearchSection(section, {
                    filterResult,
                });
                if (!normalized) return;
                sectionsRef.value = [...sectionsRef.value, normalized];
                if (!firstResultDelivered) {
                    firstResultDelivered = true;
                    onFirstResult?.();
                }
            },
            onError(event) {
                if (requestStore.controller !== controller || !errorRef) return;
                errorRef.value = event?.message || "Search failed";
            },
            onDone(event) {
                if (requestStore.controller !== controller) return;
                statusRef.value = event?.message || "";
                onDone?.(event);
            },
        });
    } catch (e) {
        if (e?.name === "AbortError") {
            return;
        }
        sectionsRef.value = [];
        statusRef.value = "";
        if (errorRef) {
            errorRef.value = e?.message || "Search failed";
        }
        onReset?.();
    } finally {
        if (requestStore.controller === controller) {
            requestStore.controller = null;
            searchingRef.value = false;
        }
    }
}

function hasUnsavedSelectedChanges() {
    return Boolean(selected.value) && dirty.value;
}

function confirmLeaveCurrentNote() {
    if (!hasUnsavedSelectedChanges()) return true;
    return window.confirm(UNSAVED_NOTE_WARNING);
}

function onBeforeUnload(event) {
    if (!hasUnsavedSelectedChanges()) return;
    event.preventDefault();
    event.returnValue = "";
}

function openOptions() {
    if (!confirmLeaveCurrentNote()) return;
    emit("navigate-options");
}

function exitSearchSelectionMode() {
    searchKeyboardMode.value = false;
    highlightedIndex.value = -1;
}

function focusSearchInput() {
    exitSearchSelectionMode();
    const input = searchInputEl.value || document.querySelector(".search-input");
    if (input) input.focus();
}

function clearSearchInput({ focus = false } = {}) {
    searchQuery.value = "";
    searchSections.value = [];
    searchCollapsedSections.value = {};
    activeSearchSectionKey.value = "";
    searchKeyboardMode.value = false;
    searchStatusMessage.value = "";
    searchError.value = "";
    abortSearchRequest(sidebarSearchRequest);
    clearTimeout(searchTimeout);
    highlightedIndex.value = -1;
    if (focus) {
        requestAnimationFrame(() => {
            searchInputEl.value?.focus();
        });
    }
}

function focusNoteTitle() {
    editTitleInput.value?.focus();
}

function focusBodyEditor() {
    document.querySelector(".body-textarea")?.focus();
}

function focusReplyTitle() {
    newReplyTitleInput.value?.focus();
}

function focusReplyBody() {
    newReplyTextarea.value?.focus();
}

function focusThreadReplyTitle() {
    threadReplyTitleInput.value?.focus();
}

function focusThreadReplyBody() {
    threadReplyTextarea.value?.focus();
}

function toggleHotkeysHelp() {
    const active = document.activeElement;
    if (isEditing.value && isEditableElement(active)) {
        return;
    }
    showHotkeys.value = !showHotkeys.value;
}

const shortcutDefinitions = computed(() => [
    {
        id: "show-shortcuts",
        description: "Toggle keyboard shortcuts help",
        hintKey: "K",
        keys: ["Shift+?"],
        allowInInput: true,
        handler: () => toggleHotkeysHelp(),
    },
    {
        id: "show-link-hints",
        description: "Show link hints for visible note links",
        keys: ["Ctrl+L"],
        allowInInput: true,
        enabled: () => Boolean(selected.value || threadNote.value),
        handler: () => toggleLinkHintMode(),
    },
    {
        id: "open-options",
        description: "Open options",
        hintKey: "O",
        keys: ["Mod+,"],
        allowInInput: true,
        handler: () => openOptions(),
    },
    {
        id: "new-note",
        description: "Create a new note",
        hintKey: "N",
        keys: ["Mod+N"],
        allowInInput: true,
        handler: () => newNote(),
    },
    {
        id: "focus-search",
        description: "Focus the search bar",
        hintKey: "/",
        keys: ["Mod+K", "Ctrl+/"],
        allowInInput: true,
        handler: () => focusSearchInput(),
    },
    {
        id: "focus-note-title",
        description: "Focus the note title",
        hintKey: "Q",
        enabled: () => Boolean(selected.value) && isEditing.value,
        handler: () => focusNoteTitle(),
    },
    {
        id: "focus-note-body",
        description: "Focus the note body",
        hintKey: "W",
        enabled: () => Boolean(selected.value) && isEditing.value,
        handler: () => focusBodyEditor(),
    },
    {
        id: "toggle-edit",
        description: "Toggle edit / preview",
        hintKey: "E",
        keys: ["Mod+E"],
        allowInInput: true,
        enabled: () => Boolean(selected.value),
        handler: () => {
            if (!selected.value) return;
            toggleEdit();
            if (isEditing.value) {
                requestAnimationFrame(() => focusBodyEditor());
            }
        },
    },
    {
        id: "new-child-note",
        description: "Create a child note",
        hintKey: "J",
        allowInInput: true,
        enabled: () => Boolean(selected.value?.id),
        handler: () => newChildNote(),
    },
    {
        id: "attach-file",
        description: "Attach a file",
        hintKey: "A",
        allowInInput: true,
        enabled: () => Boolean(selected.value?.id),
        handler: () => onAttachFile(),
    },
    {
        id: "save-note",
        description: "Save the current note",
        hintKey: "S",
        keys: ["Mod+S"],
        allowInInput: true,
        enabled: () => Boolean(selected.value) && dirty.value && !saving.value,
        handler: () => {
            if (dirty.value && selected.value) save();
        },
    },
    {
        id: "toggle-history",
        description: "Toggle note history",
        hintKey: "H",
        keys: ["Mod+H"],
        allowInInput: true,
        enabled: () => Boolean(selected.value?.id),
        handler: () => toggleHistory(),
    },
    {
        id: "toggle-pin",
        description: "Pin or unpin the current note",
        hintKey: "P",
        allowInInput: true,
        enabled: () => Boolean(selected.value?.id),
        handler: () => togglePin(selected.value),
    },
    {
        id: "delete-note",
        description: "Delete the current note",
        hintKey: "D",
        allowInInput: true,
        enabled: () => Boolean(selected.value?.id),
        handler: () => confirmDelete(),
    },
    {
        id: "focus-reply-title",
        description: "Focus the reply title",
        hintKey: "I",
        allowInInput: true,
        enabled: () => Boolean(selected.value),
        handler: () => focusReplyTitle(),
    },
    {
        id: "focus-reply-body",
        description: "Focus the reply body",
        hintKey: "B",
        allowInInput: true,
        enabled: () => Boolean(selected.value),
        handler: () => focusReplyBody(),
    },
    {
        id: "send-reply",
        description: "Send the reply composer",
        hintKey: "R",
        keys: ["Mod+R"],
        allowInInput: true,
        enabled: () => canSendReply.value,
        handler: () => sendReply(),
    },
    {
        id: "focus-thread-reply-title",
        description: "Focus the thread reply title",
        hintKey: "G",
        enabled: () => Boolean(threadNote.value),
        handler: () => focusThreadReplyTitle(),
    },
    {
        id: "focus-thread-reply-body",
        description: "Focus the thread reply body",
        hintKey: "V",
        enabled: () => Boolean(threadNote.value),
        handler: () => focusThreadReplyBody(),
    },
    {
        id: "close-thread",
        description: "Close the thread sidebar",
        hintKey: "X",
        allowInInput: true,
        enabled: () => Boolean(threadNote.value),
        handler: () => closeThreadSidebar(),
    },
    {
        id: "open-thread-main-view",
        description: "Open the thread note in the main view",
        hintKey: "U",
        allowInInput: true,
        enabled: () => Boolean(threadNote.value),
        handler: () => {
            if (threadNote.value) selectNote(threadNote.value);
        },
    },
    {
        id: "send-thread-reply",
        description: "Send the thread reply composer",
        hintKey: "T",
        allowInInput: true,
        enabled: () => canSendThreadReply.value,
        handler: () => sendThreadReply(),
    },
    {
        id: "open-parent",
        description: "Open parent note in main view",
        keys: ["u"],
        enabled: () => Boolean(selected.value?.parent_id),
        handler: async () => {
            const parentId = selected.value?.parent_id;
            if (!parentId) return;
            let parent = notes.value.find((n) => n.id === parentId);
            if (!parent) {
                try {
                    parent = await fetchNote(props.token, parentId);
                } catch {
                    return;
                }
            }
            if (parent) selectNote(parent);
        },
    },
    {
        id: "cancel-delete",
        description: "Cancel delete",
        hintKey: "N",
        keys: ["Escape"],
        visible: () => showDeleteModal.value,
        enabled: () => !deleting.value,
        handler: () => {
            showDeleteModal.value = false;
        },
    },
    {
        id: "confirm-delete",
        description: "Confirm delete",
        hintKey: "Y",
        keys: ["Enter"],
        visible: () => showDeleteModal.value,
        enabled: () => !deleting.value,
        handler: () => doDelete(),
    },
    {
        id: "sidebar-up",
        description: "Move sidebar selection up",
        keys: ["ArrowUp"],
        allowInInput: true,
        handler: (event) => handleArrowShortcut(event),
    },
    {
        id: "sidebar-down",
        description: "Move sidebar selection down",
        keys: ["ArrowDown"],
        allowInInput: true,
        handler: (event) => handleArrowShortcut(event),
    },
    {
        id: "sidebar-open",
        description: "Open the highlighted note or link result",
        keys: ["Enter"],
        allowInInput: true,
        handler: (event) => handleEnterShortcut(event),
    },
    {
        id: "close-context",
        description: "Close the active panel or clear focus",
        keys: ["Escape"],
        allowInInput: true,
        handler: (event) => handleEscapeShortcut(event),
    },
]);

const {
    showHelp: showHotkeys,
    hintOverlayVisible: shortcutHintsVisible,
    helpItems: hotkeys,
    getHintLabel,
    getShortcutLabel,
    isShortcutEnabled,
    hideHintOverlay,
} = useKeyboardShortcuts(shortcutDefinitions, {
    suspendWhen: () => linkHintMode.value,
});

// Edit / View toggle
const isEditing = ref(false);
const renderedBody = computed(() => {
    if (!editBody.value)
        return '<p style="color: var(--font-color-secondary);">Nothing to preview</p>';
	    return postProcessMediaTags(md.render(editBody.value), selected.value?.attachments);
});

// Render any markdown body (used for child messages)
function renderMarkdown(body, attachments) {
    if (!body)
        return '<p style="color: var(--font-color-secondary);">Empty</p>';
	    return postProcessMediaTags(md.render(body), attachments);
}

// Post-process rendered HTML: convert <img> tags to <video>/<audio> for media files.
function postProcessMediaTags(html, attachments) {
    if (!html || !attachments?.length) return html;
    const mediaFiles = new Map();
    for (const f of attachments) {
        if ((f.is_video || f.is_audio) && f.url) {
            mediaFiles.set(f.url, f);
        }
    }
    if (mediaFiles.size === 0) return html;
    // Replace <img src="/file/X/Y" ...> with <video> or <audio> for known media files
    return html.replace(/<img\b[^>]*\bsrc="([^"]*\/file\/[^"]+)"[^>]*>/gi, (match, src) => {
        const file = mediaFiles.get(src);
        if (!file) return match;
        if (file.is_audio) {
            return `<audio src="${src}" controls class="inline-audio" preload="metadata"></audio>`;
        }
        return `<video src="${src}" controls class="inline-video" preload="metadata"></video>`;
    });
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

async function refreshSelectedCollections(noteId, typeValue = null) {
    if (!noteId) return;
    populateParentSearch(selected.value);
    await loadAncestors(noteId);
    if (showHistory.value) {
        historyLoading.value = true;
        try {
            history.value = await fetchNoteHistory(props.token, noteId);
        } finally {
            historyLoading.value = false;
        }
    }
    const effectiveType = typeValue || selected.value?.type || noteType.value;
    if (childrenLoaded.value || !isLazyChildren(effectiveType || "standard")) {
        await loadChildren(noteId);
    }
}

async function refreshSelectedInPlace(noteId = selected.value?.id) {
    if (!noteId) return;
    // If uploads are in progress, skip the refresh to avoid replacing
    // the attachments array that active uploads are pushing to.
    if (activeUploads.value && activeUploads.value.length > 0) return;
    try {
        const full = await fetchNote(props.token, noteId);
        if (selected.value?.id !== noteId) return;
        applySelectedDetail(full);
        await refreshSelectedCollections(noteId, full.type);
    } catch (e) {
        if (selected.value?.id !== noteId) return;
        if ((e?.message || "").includes("not found")) {
            selected.value = null;
            children.value = [];
            ancestors.value = [];
            history.value = [];
            showHistory.value = false;
            pushURL();
        }
    }
}

async function refreshThreadNoteInPlace(noteId = threadNote.value?.id) {
    if (!noteId) return;
    try {
        const full = await fetchNote(props.token, noteId);
        if (threadNote.value?.id !== noteId) return;
        applyThreadDetail(full);
        if (
            threadChildrenLoaded.value ||
            !isLazyChildren((full.type || "standard") ?? "standard")
        ) {
            await loadThreadChildren();
        }
        try {
            threadAncestors.value = await fetchAncestors(props.token, noteId);
        } catch {
            threadAncestors.value = [];
        }
    } catch (e) {
        if (threadNote.value?.id !== noteId) return;
        if ((e?.message || "").includes("not found")) {
            closeThreadSidebar({ updateURL: false });
        }
    }
}

async function runLiveRefresh() {
    if (liveRefreshRunning) {
        liveRefreshQueued = true;
        return;
    }
    liveRefreshRunning = true;
    const refreshFull = liveRefreshFullRequested;
    const refreshSelected = refreshFull || liveRefreshSelectedRequested;
    const refreshThread = refreshFull || liveRefreshThreadRequested;
    liveRefreshFullRequested = false;
    liveRefreshSelectedRequested = false;
    liveRefreshThreadRequested = false;
    try {
        if (refreshFull) {
            await loadNotes();
            if (hasSidebarSearch.value) {
                await doSearch();
            }
        }
        const selectedNoteID = selected.value?.id;
        if (refreshSelected && selectedNoteID) {
            if (activeUploads.value && activeUploads.value.length > 0) {
                // Uploads are in progress; skip the in-place refresh to avoid
                // disrupting the attachments array. The onComplete callbacks
                // handle the UI updates directly.
                liveRefreshPending.value = false;
            } else if (dirty.value || saving.value) {
                liveRefreshPending.value = true;
                await refreshSelectedCollections(selectedNoteID);
            } else {
                await refreshSelectedInPlace(selectedNoteID);
            }
        }
        if (refreshThread && threadNote.value?.id) {
            await refreshThreadNoteInPlace(threadNote.value.id);
        }
    } finally {
        liveRefreshRunning = false;
        if (
            liveRefreshQueued ||
            liveRefreshFullRequested ||
            liveRefreshSelectedRequested ||
            liveRefreshThreadRequested
        ) {
            liveRefreshQueued = false;
            scheduleLiveRefresh();
        }
    }
}

function scheduleLiveRefresh({ full = false, selected: refreshSelected = false, thread: refreshThread = false } = {}) {
    liveRefreshFullRequested = liveRefreshFullRequested || full;
    liveRefreshSelectedRequested = liveRefreshSelectedRequested || refreshSelected;
    liveRefreshThreadRequested = liveRefreshThreadRequested || refreshThread;
    if (liveRefreshTimer) return;
    liveRefreshTimer = window.setTimeout(() => {
        liveRefreshTimer = null;
        runLiveRefresh();
    }, 100);
}

function onLiveMessage(event) {
    const detail = event?.detail;
    if (!detail?.type) return;

    if (detail.type === "live.ready") {
        scheduleLiveRefresh({
            full: true,
            selected: Boolean(selected.value?.id),
            thread: Boolean(threadNote.value?.id),
        });
        return;
    }

    if (detail.type !== "notes.changed") return;

    const changedIDs = new Set(
        Array.isArray(detail.note_ids)
            ? detail.note_ids
                  .map((id) => Number(id))
                  .filter((id) => Number.isInteger(id) && id > 0)
            : [],
    );
    const selectedNoteID = selected.value?.id;
    const threadNoteID = threadNote.value?.id;
    scheduleLiveRefresh({
        full: true,
        selected:
            Boolean(selectedNoteID) &&
            (changedIDs.size === 0 || changedIDs.has(selectedNoteID)),
        thread:
            Boolean(threadNoteID) &&
            (changedIDs.size === 0 || changedIDs.has(threadNoteID)),
    });
}

watch([dirty, saving], ([isDirty, isSaving]) => {
    if (!liveRefreshPending.value || isDirty || isSaving) {
        return;
    }
    scheduleLiveRefresh({ selected: true });
});

watch(
    [
        linkHintMode,
        selectedRendererKey,
        threadRendererKey,
        () => selected.value?.id ?? 0,
        () => threadNote.value?.id ?? 0,
        isEditing,
        () =>
            children.value
                .map((child) => `${child.id}:${child.updated_at || child.created_at || ""}`)
                .join("|"),
        () =>
            threadChildren.value
                .map(
                    (child) =>
                        `${child.id}:${child.updated_at || child.created_at || ""}`,
                )
                .join("|"),
    ],
    async ([active]) => {
        if (!active) return;
        await nextTick();
        scheduleLinkHintRefresh();
    },
);

watch([showHotkeys, showDeleteModal], ([hotkeysOpen, deleteOpen]) => {
    if (hotkeysOpen || deleteOpen) {
        closeLinkHintMode();
    }
});

onMounted(() => {
    loadNotes();
    fetchAndMergeManifests(props.token);
    window.addEventListener("live:message", onLiveMessage);
    // Resume any interrupted uploads from IndexedDB
    resumeStoredUploads(props.token);
});

onUnmounted(() => {
    window.removeEventListener("live:message", onLiveMessage);
    if (liveRefreshTimer) {
        window.clearTimeout(liveRefreshTimer);
        liveRefreshTimer = null;
    }
});

async function loadNotes() {
    loading.value = true;
    try {
        notes.value = await fetchNotes(props.token);
    } finally {
        loading.value = false;
    }
}

async function selectNote(
    note,
    { skipDirtyCheck = false, updateURL = true } = {},
) {
    if (!skipDirtyCheck && !confirmLeaveCurrentNote()) {
        return false;
    }

    threadNote.value = null;
    // Re-fetch from server to get full enriched data (plugin.config, plugin.view, etc.)
    try {
        const full = await fetchNote(props.token, note.id);
        applySelectedDetail(full);
    } catch {
        // Fallback to the sidebar data if fetch fails.
        applySelectedDetail(note);
    }
    dirty.value = false;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    isEditing.value = false;
    highlightedIndex.value = rootNotes.value.indexOf(note);
    const noteTypeVal = noteType.value || "standard";
    if (isLazyChildren(noteTypeVal)) {
        children.value = [];
        childrenLoaded.value = false;
    } else {
        loadChildren(note.id);
    }
    populateParentSearch(note);
    await loadAncestors(note.id);
    if (updateURL) {
        pushURL();
    }
    return true;
}

async function selectSearchResult(
    sr,
    {
        skipDirtyCheck = false,
        updateURL = true,
        preserveHighlight = false,
        highlightIndex = -1,
    } = {},
) {
    if (!skipDirtyCheck && !confirmLeaveCurrentNote()) {
        return false;
    }

    threadNote.value = null;
    // Re-fetch full note for proper hydration.
    try {
        const full = await fetchNote(props.token, sr.id);
        applySelectedDetail(full);
    } catch {
        applySelectedDetail({
            id: sr.id,
            title: sr.title,
            parent_id: sr.parent_id,
            type: sr.type || "standard",
            pinned: sr.pinned || false,
            body: sr.body,
            created_at: sr.created_at,
            updated_at: sr.updated_at,
            tags: sr.tags || [],
        });
    }
    dirty.value = false;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    isEditing.value = false;
    if (preserveHighlight && highlightIndex >= 0) {
        highlightedIndex.value = highlightIndex;
    } else {
        highlightedIndex.value = searchResults.value.indexOf(sr);
    }
    const noteTypeVal = noteType.value || "standard";
    if (isLazyChildren(noteTypeVal)) {
        children.value = [];
        childrenLoaded.value = false;
    } else {
        loadChildren(sr.id);
    }
    populateParentSearch(selected.value);
    await loadAncestors(sr.id);
    if (updateURL) {
        pushURL();
    }
    return true;
}

function populateParentSearch(note) {
    abortSearchRequest(parentSearchRequest);
    parentSearchActive.value = false;
    parentKeyboardMode.value = false;
    parentHighlightedIndex.value = -1;
    parentSearchSections.value = [];
    parentSearchCollapsedSections.value = {};
    parentSearchStatusMessage.value = "";
    if (note?.parent_id) {
        const p = notes.value.find((n) => n.id === note.parent_id);
        parentSearch.value = p ? p.title : "";
    } else {
        parentSearch.value = "";
    }
}

function newNote(
    parentNote = null,
    { skipDirtyCheck = false, updateURL = true } = {},
) {
    if (!skipDirtyCheck && !confirmLeaveCurrentNote()) {
        return false;
    }

    threadNote.value = null;
    const defaultType = parentNote
        ? getDefaultChildType(parentNote.type)
        : "standard";
    selected.value = {
        id: null,
        title: "",
        body: "",
        type: defaultType,
        parent_id: parentNote ? parentNote.id : null,
    };
    liveRefreshPending.value = false;
    selectedRendererKey.value += 1;
    editTitle.value = "";
    editBody.value = "";
    noteType.value = defaultType;
    const typeDef = getNoteTypeOrDefault(defaultType);
    customData.value = typeDef.emptyCustomData();
    editTags.value = [];
    dirty.value = true;
    saveError.value = "";
    showHistory.value = false;
    history.value = [];
    highlightedIndex.value = -1;
    children.value = [];
    childrenLoaded.value = false;
    parentSearch.value = "";
    parentSearchActive.value = false;
    parentSearchSections.value = [];
    parentSearchCollapsedSections.value = {};
    parentSearchStatusMessage.value = "";
    showParentPicker.value = false;
    ancestors.value = [];
    isEditing.value = true;
    if (parentNote) {
        parentSearch.value = parentNote.title || "";
    }
    if (updateURL) {
        pushURL();
    }
    requestAnimationFrame(() =>
        document.querySelector(".body-textarea")?.focus(),
    );
    return true;
}

function newChildNote() {
    if (!selected.value?.id) return;
    const created = newNote(selected.value);
    if (!created) return;
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
        liveRefreshPending.value = false;
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
        childrenLoaded.value = false;
        return;
    }
    childrenLoading.value = true;
    try {
        children.value = await fetchChildren(props.token, noteId);
    } catch {
        children.value = [];
    } finally {
        childrenLoading.value = false;
        childrenLoaded.value = true;
    }
}

async function loadThreadChildren() {
    if (!threadNote.value?.id) return;
    threadChildrenLoading.value = true;
    try {
        threadChildren.value = await fetchChildren(
            props.token,
            threadNote.value.id,
        );
    } catch {
        threadChildren.value = [];
    } finally {
        threadChildrenLoading.value = false;
        threadChildrenLoaded.value = true;
    }
}

function onParentSearchInput() {
    parentSearchActive.value = Boolean(parentSearch.value.trim());
    parentKeyboardMode.value = false;
    parentHighlightedIndex.value = -1;
    clearTimeout(parentSearchTimeout);
    parentSearchTimeout = setTimeout(doParentSearch, 200);
}

async function doParentSearch() {
    const q = parentSearch.value.trim();
    parentSearchCollapsedSections.value = {};
    await runStreamedSearch({
        query: q,
        sectionsRef: parentSearchSections,
        statusRef: parentSearchStatusMessage,
        searchingRef: parentSearching,
        requestStore: parentSearchRequest,
        filterResult: (result) => result.id !== selected.value?.id,
        onFirstResult: () => {
            if (
                parentHighlightedIndex.value < 0 &&
                parentOptions.value.length > 0
            ) {
                parentHighlightedIndex.value = 0;
            }
        },
        onReset: () => {
            parentSearchSections.value = [];
            parentSearchCollapsedSections.value = {};
            parentKeyboardMode.value = false;
            parentHighlightedIndex.value = -1;
        },
        onDone: () => {
            parentSearchStatusMessage.value = "";
        },
    });
}

function selectParent(note) {
    selected.value = { ...selected.value, parent_id: note.id };
    parentSearch.value = note.title;
    parentSearchActive.value = false;
    parentSearchStatusMessage.value = "";
    abortSearchRequest(parentSearchRequest);
    showParentPicker.value = true;
    dirty.value = true;
}

function clearParent() {
    selected.value = { ...selected.value, parent_id: null };
    parentSearch.value = "";
    parentSearchActive.value = false;
    parentKeyboardMode.value = false;
    parentHighlightedIndex.value = -1;
    ancestors.value = [];
    parentSearchSections.value = [];
    parentSearchCollapsedSections.value = {};
    parentSearchStatusMessage.value = "";
    abortSearchRequest(parentSearchRequest);
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

async function openThreadSidebar(note, { updateURL = true } = {}) {
    // Fetch the full enriched note so plugin data is available for rendering.
    try {
        applyThreadDetail(await fetchNote(props.token, note.id));
    } catch {
        applyThreadDetail(note);
    }
    threadReplyTitle.value = "";
    threadReplyBody.value = "";
    const noteTypeVal = threadNote.value?.type || "standard";
    if (isLazyChildren(noteTypeVal)) {
        threadChildren.value = [];
        threadChildrenLoaded.value = false;
    } else {
        threadChildrenLoading.value = true;
        try {
            threadChildren.value = await fetchChildren(props.token, note.id);
        } catch {
            threadChildren.value = [];
        } finally {
            threadChildrenLoading.value = false;
            threadChildrenLoaded.value = true;
        }
    }
    // Load ancestors for breadcrumb
    try {
        threadAncestors.value = await fetchAncestors(props.token, note.id);
    } catch {
        threadAncestors.value = [];
    }
    if (updateURL) {
        pushURL();
    }
}

function closeThreadSidebar({ updateURL = true } = {}) {
    threadNote.value = null;
    threadChildren.value = [];
    threadAncestors.value = [];
    if (updateURL) {
        pushURL();
    }
}

async function sendThreadReply() {
    if (threadSendingReply.value) return;
    if (!threadNote.value?.id) return;
    if (!hasReplyDraft(threadReplyTitle.value, threadReplyBody.value)) return;
    threadSendingReply.value = true;
    try {
        const childType = getDefaultChildType(threadNote.value.type);
        const child = await createNote(
            props.token,
            threadReplyTitle.value,
            threadReplyBody.value,
            threadNote.value.id,
            childType,
        );
        // Reload the note list so sort order is correct.
        await loadNotes();
        threadChildrenLoaded.value = true;
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

function resetThreadSidebarCtrlTapState() {
    threadSidebarCtrlTapArmed = false;
    lastThreadSidebarCtrlTapAt = 0;
    threadSidebarCtrlChordUsed = false;
}

function onThreadSidebarCtrlKeyDown(event) {
    if (linkHintMode.value) return;
    if (event.key === "Control") {
        if (
            event.repeat ||
            event.altKey ||
            event.metaKey ||
            event.shiftKey ||
            !threadNote.value
        ) {
            return;
        }

        const now = Date.now();
        if (
            threadSidebarCtrlTapArmed &&
            now - lastThreadSidebarCtrlTapAt <= DOUBLE_CONTROL_OPEN_MS
        ) {
            hideHintOverlay();
            resetThreadSidebarCtrlTapState();
            selectNote(threadNote.value);
        }
        return;
    }

    if (event.ctrlKey) {
        resetThreadSidebarCtrlTapState();
        threadSidebarCtrlChordUsed = true;
    }
}

function onThreadSidebarCtrlKeyUp(event) {
    if (linkHintMode.value || event.key !== "Control") return;
    if (
        event.altKey ||
        event.metaKey ||
        event.shiftKey ||
        !threadNote.value ||
        threadSidebarCtrlChordUsed
    ) {
        resetThreadSidebarCtrlTapState();
        return;
    }

    threadSidebarCtrlTapArmed = true;
    lastThreadSidebarCtrlTapAt = Date.now();
}

function onThreadSidebarCtrlBlur() {
    resetThreadSidebarCtrlTapState();
}

// --- Tag functions ---

function normalizeTagName(name) {
    return name
        .trim()
        .replace(/^#+/, "")
        .trim()
        .replace(/\s+/g, " ")
        .toLowerCase();
}

async function onTagInput() {
    const q = normalizeTagName(tagSearch.value);
    try {
        const result = await fetchTags(props.token, q || "");
        tagOptions.value = Array.isArray(result) ? result : [];
    } catch {
        tagOptions.value = [];
    }
}

function addTag(name) {
    name = normalizeTagName(name);
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

async function refreshAutoTags() {
    if (
        !selected.value?.id ||
        autoTagsRefreshing.value ||
        dirty.value ||
        saving.value
    ) {
        return;
    }
    autoTagsRefreshing.value = true;
    autoTagsError.value = "";
    autoTagsInfo.value = "";
    try {
        const result = await generateAutoTags(props.token, selected.value.id);
        selected.value = {
            ...selected.value,
            auto_tags: Array.isArray(result?.auto_tags)
                ? result.auto_tags
                : [],
        };
        autoTagsInfo.value = "Auto tags refreshed.";
    } catch (e) {
        autoTagsError.value = e.message;
    } finally {
        autoTagsRefreshing.value = false;
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
        applySelectedDetail(updated);
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

async function importRecipes(importJSON) {
    if (!selected.value || typeof importJSON !== "string") {
        return;
    }

    saveError.value = "";
    saving.value = true;
    try {
        await ensureSelectedNoteSaved();
        const result = await pluginActionV2(
            props.token,
            selected.value.id,
            "import_recipes_json",
            { import_json: importJSON },
        );
        await loadNotes();
        const refreshed = await fetchNote(
            props.token,
            result?.primary_note_id || selected.value.id,
        );
        applySelectedDetail(refreshed);
        dirty.value = false;
        isEditing.value = false;
        populateParentSearch(refreshed);
        loadChildren(refreshed.id);
        await loadAncestors(refreshed.id);
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
    showUploadModal.value = true;
}

function onChunkedUploadComplete(result) {
    if (!selected.value) return;
    addAttachmentIfNew(result?.file);
}

async function onBodyDrop(e) {
    const files = e.dataTransfer.files;
    if (!files || files.length === 0) return;
    try {
        await ensureSelectedNoteSaved();
    } catch (err) {
        saveError.value = err.message;
        return;
    }
    // Use chunked upload via the composable queue — supports multiple files
    const fileArray = Array.from(files);
    enqueueMultipleInline(fileArray, selected.value.id, props.token, {
        onComplete: (result) => {
            if (result && result.markdown) {
                // Prefix with newline so multiple inline uploads stack vertically.
                insertAtCursor("\n" + result.markdown);
            }
            addAttachmentIfNew(result?.file);
        },
    });
}

async function onBodyPaste(e) {
    const items = e.clipboardData?.items;
    if (!items) return;

    const files = [];
    for (let i = 0; i < items.length; i++) {
        const item = items[i];
        if (item.kind === "file") {
            const file = item.getAsFile();
            if (file) files.push(file);
        }
    }
    if (files.length === 0) return;

    // If there are text items too, let the default paste handle the text.
    // We prevent default only for the file items so text still pastes normally.
    const hasText = Array.from(items).some(item => item.kind === "string");
    if (!hasText) {
        e.preventDefault();
    }

    try {
        await ensureSelectedNoteSaved();
    } catch (err) {
        saveError.value = err.message;
        return;
    }

    enqueueMultipleInline(files, selected.value.id, props.token, {
        onComplete: (result) => {
            if (result && result.markdown) {
                // Prefix with newline so multiple inline uploads stack vertically.
                insertAtCursor("\n" + result.markdown);
            }
            addAttachmentIfNew(result?.file);
        },
    });
}

function addAttachmentIfNew(file) {
    if (!file) return;
    if (!selected.value.attachments) selected.value.attachments = [];
    // Avoid duplicates — a save between upload completion and this callback
    // may have already populated attachments from the server.
    if (!selected.value.attachments.some(a => a.id === file.id)) {
        selected.value.attachments.push(file);
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
    if (!hasReplyDraft(newReplyTitle.value, newReplyBody.value)) return;
    if (!selected.value?.id) {
        // If the current note is not yet saved, save it first
        if (dirty.value) await save();
        if (!selected.value?.id) return;
    }
    sendingReply.value = true;
    try {
        const childType = getDefaultChildType(selected.value.type);
        const child = await createNote(
            props.token,
            newReplyTitle.value,
            newReplyBody.value,
            selected.value.id,
            childType,
        );
        // Reload the note list so sort order is correct.
        await loadNotes();
        // Mark children as loaded so the reply appears.
        childrenLoaded.value = true;
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
    const mode = searchMode.value;
    const q = mode.query;
    searchCollapsedSections.value = {};
    activeSearchSectionKey.value = "";
    await runStreamedSearch({
        query: q,
        types: mode.types,
        tagOnly: mode.tagOnly,
        sectionsRef: searchSections,
        statusRef: searchStatusMessage,
        searchingRef: searching,
        requestStore: sidebarSearchRequest,
        errorRef: searchError,
        onFirstResult: () => {
            if (highlightedIndex.value < 0 && searchResults.value.length > 0) {
                highlightedIndex.value = 0;
            }
        },
        onReset: () => {
            searchSections.value = [];
            searchCollapsedSections.value = {};
            activeSearchSectionKey.value = "";
            searchKeyboardMode.value = false;
            searchStatusMessage.value = "";
            highlightedIndex.value = -1;
        },
        onDone: () => {
            searchStatusMessage.value = "";
        },
    });
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

function onLinkEditorKeydown(target, event) {
    if (
        !linkSearchVisible.value ||
        linkSearchTarget.value !== target ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
    ) {
        return;
    }

    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        linkKeyboardMode.value = true;
        handleArrowShortcut(event);
        return;
    }
    if (event.key === "Enter") {
        handleEnterShortcut(event);
        return;
    }
    if (event.key === "Escape") {
        handleEscapeShortcut(event);
        return;
    }
    if (!linkKeyboardMode.value) {
        return;
    }
    if (event.key === "ArrowLeft") {
        event.preventDefault();
        collapseActiveLinkSection();
        return;
    }
    if (event.key === "ArrowRight") {
        event.preventDefault();
        expandActiveLinkSection();
    }
}

function detectLinkSearchTrigger(textBefore) {
    const lastOpen = textBefore.lastIndexOf("[[");
    const lastClose = textBefore.lastIndexOf("]]"
    );
    if (lastOpen !== -1 && lastOpen > lastClose) {
        const query = textBefore.slice(lastOpen + 2);
        if (!query.includes("]]"
        ) && !query.includes("\n")) {
            return {
                type: "wiki",
                start: lastOpen,
                query,
            };
        }
    }

    const lineStart = textBefore.lastIndexOf("\n") + 1;
    const line = textBefore.slice(lineStart);
    const slashMatch = line.match(/^(\s*)\/\/([^\n]*)$/);
    if (slashMatch) {
        return {
            type: "slash",
            start: lineStart + slashMatch[1].length,
            query: slashMatch[2],
        };
    }

    return null;
}

function resetLinkSearchResults() {
    abortSearchRequest(linkSearchRequest);
    linkSearching.value = false;
    linkSearchSections.value = [];
    linkSearchCollapsedSections.value = {};
    linkSearchStatusMessage.value = "";
    linkSearchIndex.value = -1;
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
    const trigger = detectLinkSearchTrigger(textBefore);
    if (!trigger) {
        closeLinkSearch();
        return;
    }

    const queryUnchanged =
        linkSearchVisible.value &&
        trigger.type === linkSearchTriggerType.value &&
        trigger.start === linkSearchTriggerStart.value &&
        trigger.query === linkSearchQuery.value;

    linkSearchTriggerType.value = trigger.type;
    linkSearchTriggerStart.value = trigger.start;
    linkSearchQuery.value = trigger.query;
    linkSearchVisible.value = true;
    updateLinkPopupPosition();

    if (queryUnchanged) {
        return;
    }

    clearTimeout(linkSearchTimeout);
    if (!trigger.query.trim()) {
        resetLinkSearchResults();
        return;
    }
    linkSearchTimeout = setTimeout(doLinkSearch, 150);
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
    linkSearchCollapsedSections.value = {};
    await runStreamedSearch({
        query: q,
        sectionsRef: linkSearchSections,
        statusRef: linkSearchStatusMessage,
        searchingRef: linkSearching,
        requestStore: linkSearchRequest,
        onFirstResult: () => {
            if (linkSearchIndex.value < 0 && linkSearchResults.value.length > 0) {
                linkSearchIndex.value = 0;
            }
        },
        onReset: () => {
            linkSearchSections.value = [];
            linkSearchCollapsedSections.value = {};
            linkSearchStatusMessage.value = "";
            linkKeyboardMode.value = false;
            linkSearchIndex.value = -1;
        },
        onDone: () => {
            linkSearchStatusMessage.value = "";
        },
    });
}

function closeLinkSearch() {
    linkSearchVisible.value = false;
    linkSearchQuery.value = "";
    linkSearchTriggerType.value = null;
    linkSearchTriggerStart.value = -1;
    linkKeyboardMode.value = false;
    resetLinkSearchResults();
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
    const linkText = `[${note.title || "Untitled"}](/note/${note.id})`;

    let replaceStart = -1;
    if (linkSearchTriggerType.value === "wiki") {
        replaceStart =
            linkSearchTriggerStart.value >= 0
                ? linkSearchTriggerStart.value
                : textBefore.lastIndexOf("[[");
    } else if (linkSearchTriggerType.value === "slash") {
        replaceStart = linkSearchTriggerStart.value;
    }
    if (replaceStart == null || replaceStart < 0) return;

    const newText = textBefore.slice(0, replaceStart) + linkText;
    context.text.value = newText + textAfter;
    closeLinkSearch();
    requestAnimationFrame(() => {
        el.focus();
        const cursorPos = newText.length;
        el.setSelectionRange(cursorPos, cursorPos);
        updateLinkPopupPosition();
    });
    context.onChange?.();
}

function handleEscapeShortcut(event) {
    if (linkHintMode.value) {
        event.preventDefault();
        closeLinkHintMode();
        return;
    }
    if (linkSearchVisible.value) {
        event.preventDefault();
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
    if (threadNote.value) {
        closeThreadSidebar();
        return;
    }

    const active = document.activeElement;
    const inSearch = active?.classList.contains("search-input");

    if (inSearch && searchQuery.value.trim()) {
        clearSearchInput();
        return;
    }
    if (highlightedIndex.value >= 0) {
        exitSearchSelectionMode();
        return;
    }
    if (isEditableElement(active)) {
        active.blur();
    }
}

function handleArrowShortcut(event) {
    if (linkSearchVisible.value) {
        const linkList = linkKeyboardMode.value
            ? linkSelectableEntries.value
            : linkSearchResults.value;
        if (linkList.length === 0) return;
        event.preventDefault();
        if (linkSearchIndex.value < 0) {
            linkSearchIndex.value =
                event.key === "ArrowDown" ? 0 : linkList.length - 1;
        } else if (event.key === "ArrowDown") {
            linkSearchIndex.value =
                (linkSearchIndex.value + 1) % linkList.length;
        } else {
            linkSearchIndex.value =
                (linkSearchIndex.value - 1 + linkList.length) % linkList.length;
        }
        requestAnimationFrame(() => {
            const selector = linkKeyboardMode.value
                ? ".link-search-section-header.highlighted, .link-search-item.highlighted"
                : ".link-search-item.highlighted";
            const el = document.querySelector(selector);
            if (el) el.scrollIntoView({ block: "nearest" });
        });
        return;
    }

    const active = document.activeElement;
    const inSearch = active?.classList.contains("search-input");
    const inParentSearch = active?.classList.contains("parent-input");

    if (inParentSearch) {
        const list = parentKeyboardMode.value
            ? parentSelectableEntries.value
            : parentOptions.value;
        if (list.length === 0) return;
        event.preventDefault();
        showParentPicker.value = true;
        if (parentHighlightedIndex.value < 0) {
            parentHighlightedIndex.value =
                event.key === "ArrowDown" ? 0 : list.length - 1;
        } else if (event.key === "ArrowDown") {
            parentHighlightedIndex.value =
                (parentHighlightedIndex.value + 1) % list.length;
        } else {
            parentHighlightedIndex.value =
                (parentHighlightedIndex.value - 1 + list.length) % list.length;
        }
        requestAnimationFrame(() => {
            const selector = parentKeyboardMode.value
                ? ".parent-dropdown-section-label.highlighted, .parent-dropdown-item.highlighted"
                : ".parent-dropdown-item.highlighted";
            const el = document.querySelector(selector);
            if (el) el.scrollIntoView({ block: "nearest" });
        });
        return;
    }

    if (!inSearch && isEditableElement(active)) {
        return;
    }

    const list = sidebarList.value;
    if (list.length === 0) return;
    event.preventDefault();
    if (highlightedIndex.value < 0) {
        highlightedIndex.value =
            event.key === "ArrowDown" ? 0 : list.length - 1;
    } else if (event.key === "ArrowDown") {
        highlightedIndex.value = (highlightedIndex.value + 1) % list.length;
    } else {
        highlightedIndex.value =
            (highlightedIndex.value - 1 + list.length) % list.length;
    }
    activeSearchSectionKey.value = searchKeyboardMode.value
        ? selectableEntryKeyForIndex(highlightedIndex.value)
        : sectionKeyForFlatIndex(searchSectionGroups.value, highlightedIndex.value);
    requestAnimationFrame(() => {
        const selector = searchKeyboardMode.value
            ? ".search-section-header.highlighted, .search-note-item.highlighted"
            : ".note-item.highlighted";
        const el = document.querySelector(selector);
        if (el) el.scrollIntoView({ block: "nearest" });
    });
}

function handleEnterShortcut(event) {
    if (linkSearchVisible.value) {
        const linkList = linkKeyboardMode.value
            ? linkSelectableEntries.value
            : linkSearchResults.value;
        if (
            linkList.length > 0 &&
            linkSearchIndex.value >= 0 &&
            linkSearchIndex.value < linkList.length
        ) {
            event.preventDefault();
            const item = linkList[linkSearchIndex.value];
            if (linkKeyboardMode.value && item?.kind === "section") {
                toggleLinkSearchSection(item.key);
            } else {
                const result = linkKeyboardMode.value ? item?.item?.result : item;
                if (result) {
                    selectLinkResult(result);
                }
            }
        }
        return;
    }

    const active = document.activeElement;
    const inSearch = active?.classList.contains("search-input");
    const inParentSearch = active?.classList.contains("parent-input");

    if (inParentSearch) {
        const list = parentKeyboardMode.value
            ? parentSelectableEntries.value
            : parentOptions.value;
        const idx =
            parentHighlightedIndex.value >= 0 ? parentHighlightedIndex.value : 0;
        if (idx < 0 || idx >= list.length) return;
        event.preventDefault();
        const item = list[idx];
        if (parentKeyboardMode.value && item?.kind === "section") {
            toggleParentSearchSection(item.key);
            return;
        }
        const result = parentKeyboardMode.value ? item?.item?.result : item;
        if (!result) return;
        selectParent(result);
        return;
    }

    if (!inSearch && isEditableElement(active)) {
        return;
    }

    const idx = inSearch
        ? highlightedIndex.value >= 0
            ? highlightedIndex.value
            : 0
        : highlightedIndex.value;
    if (idx < 0 || idx >= sidebarList.value.length) return;

    event.preventDefault();
    const item = sidebarList.value[idx];
    if (hasSidebarSearch.value) {
        if (searchKeyboardMode.value && item?.kind === "section") {
            toggleSearchSection(item.key);
            return;
        }
        const result = searchKeyboardMode.value ? item?.item?.result : item;
        if (!result) return;
        const preservedIndex = highlightedIndex.value;
        Promise.resolve(
            selectSearchResult(result, {
                preserveHighlight: inSearch && searchKeyboardMode.value,
                highlightIndex: preservedIndex,
            }),
        ).then((opened) => {
            if (!opened || !inSearch) return;
            requestAnimationFrame(() => {
                searchInputEl.value?.focus();
            });
        });
    } else {
        selectNote(item);
    }
}

onMounted(() => {
    window.addEventListener("click", onClickOutside);
    window.addEventListener("popstate", onPopstate);
    window.addEventListener("beforeunload", onBeforeUnload);
    window.addEventListener("keydown", onThreadSidebarCtrlKeyDown, true);
    window.addEventListener("keyup", onThreadSidebarCtrlKeyUp, true);
    window.addEventListener("keydown", onLinkHintKeyDown, true);
    window.addEventListener("resize", scheduleLinkHintRefresh);
    window.addEventListener("scroll", scheduleLinkHintRefresh, true);
    window.addEventListener("blur", onThreadSidebarCtrlBlur);
    window.addEventListener("blur", onLinkHintWindowBlur);
    // Restore state from URL on initial load
    loadFromURL();
});

onUnmounted(() => {
    window.removeEventListener("click", onClickOutside);
    window.removeEventListener("popstate", onPopstate);
    window.removeEventListener("beforeunload", onBeforeUnload);
    window.removeEventListener("keydown", onThreadSidebarCtrlKeyDown, true);
    window.removeEventListener("keyup", onThreadSidebarCtrlKeyUp, true);
    window.removeEventListener("keydown", onLinkHintKeyDown, true);
    window.removeEventListener("resize", scheduleLinkHintRefresh);
    window.removeEventListener("scroll", scheduleLinkHintRefresh, true);
    window.removeEventListener("blur", onThreadSidebarCtrlBlur);
    window.removeEventListener("blur", onLinkHintWindowBlur);
    cancelLinkHintRefresh();
    clearTimeout(searchTimeout);
    clearTimeout(parentSearchTimeout);
    clearTimeout(linkSearchTimeout);
    abortSearchRequest(sidebarSearchRequest);
    abortSearchRequest(parentSearchRequest);
    abortSearchRequest(linkSearchRequest);
});

function onClickOutside(e) {
    if (linkHintMode.value) {
        closeLinkHintMode();
    }
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
            newNote(null, { skipDirtyCheck: true, updateURL: false });
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
                await selectNote(note, {
                    skipDirtyCheck: true,
                    updateURL: false,
                });
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
                    await openThreadSidebar(tNote, { updateURL: false });
                }
            }
        }
    } else {
        if (threadNote.value) {
            closeThreadSidebar({ updateURL: false });
        }
    }
}

function onPopstate() {
    closeLinkHintMode();
    if (!confirmLeaveCurrentNote()) {
        pushURL();
        return;
    }
    loadFromURL();
}
</script>
<style scoped>
.shortcut-anchor {
    position: relative;
}

.sidebar-logo-anchor {
    display: flex;
    align-items: center;
    justify-content: center;
}

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

.ws-indicator {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}

.ws-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    display: inline-block;
    flex-shrink: 0;
    transition: background 0.3s;
}

.ws-indicator.connected .ws-dot {
    background: #22c55e;
    box-shadow: 0 0 4px #22c55e;
}

.ws-indicator.disconnected .ws-dot {
    background: #ef4444;
    box-shadow: 0 0 4px #ef4444;
}

.ws-latency {
    font-variant-numeric: tabular-nums;
}

.icon-btn {
    padding: 0.3rem 0.5rem;
    font-size: 1rem;
    line-height: 1;
}

.search-box {
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
    padding: 0.75rem;
    border-bottom: 1px solid var(--border-color);
    background: linear-gradient(
        180deg,
        rgba(109, 148, 132, 0.08),
        rgba(109, 148, 132, 0)
    );
}

.search-input-row {
    position: relative;
    display: flex;
    align-items: center;
    width: 100%;
}

.search-input {
    width: 100%;
    font-size: 0.84rem;
    padding: 0.5rem 2.2rem 0.5rem 0.7rem;
    border-radius: 10px;
    border: 1px solid var(--border-color);
    background: var(--raised-bg);
}

.search-clear-btn {
    position: absolute;
    right: 0.45rem;
    top: 50%;
    transform: translateY(-50%);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1.55rem;
    height: 1.55rem;
    border-radius: 999px;
    border: none;
    background: transparent;
    color: var(--font-color-secondary);
    cursor: pointer;
    flex-shrink: 0;
    transition:
        color 0.12s,
        background 0.12s;
}

.search-clear-btn:hover {
    color: var(--heading-color);
    background: color-mix(in srgb, var(--accent-teal) 10%, var(--raised-bg));
}

.search-clear-btn.spinning {
    animation: searchClearSpin 1s linear infinite;
}

.search-box-meta {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
}

.search-filter-summary {
    font-size: 0.72rem;
    color: var(--date-color);
    line-height: 1.35;
}

.search-stream-status {
    font-size: 0.72rem;
    line-height: 1.4;
    color: var(--accent-teal);
}

.search-type-panel {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    width: 100%;
    padding: 0.55rem 0.6rem;
    border: 1px solid var(--border-color);
    border-radius: 10px;
    background: var(--raised-bg);
}

.search-type-panel-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
}

.search-type-panel-title {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--font-color);
}

.search-type-panel-actions {
    display: flex;
    gap: 0.3rem;
}

.search-type-action {
    padding: 0.15rem 0.45rem;
}

.search-type-option {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    font-size: 0.78rem;
    color: var(--font-color);
}

@keyframes searchClearSpin {
    from {
        transform: translateY(-50%) rotate(0deg);
    }
    to {
        transform: translateY(-50%) rotate(360deg);
    }
}

.new-btn {
    margin: 0.75rem;
    width: calc(100% - 1.5rem);
}

.note-list {
    flex: 1;
    display: flex;
    flex-direction: column;
}

.note-list-scroll {
    flex: 1;
    overflow-y: auto;
    padding: 0.25rem 0;
}

.note-list-scroll > .note-item:first-child {
    margin-top: 0.35rem;
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

.note-title-row {
    display: flex;
    align-items: center;
    gap: 0.45rem;
}

.note-title {
    font-size: 0.9rem;
    color: var(--font-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
    flex: 1;
}

.search-result-type {
    flex-shrink: 0;
    font-size: 0.63rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: color-mix(in srgb, var(--accent-teal) 82%, white 18%);
    background: color-mix(in srgb, var(--accent-teal) 18%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent-teal) 38%, transparent);
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.04);
    padding: 0.16rem 0.5rem;
    border-radius: 999px;
}

.search-result-path,
.search-result-tags {
    font-size: 0.72rem;
    color: var(--date-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.search-section + .search-section {
    margin-top: 0.55rem;
}

.search-section-header {
    width: calc(100% - 1rem);
    margin: 0 0.5rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.5rem 0.7rem;
    border: 1px solid color-mix(in srgb, var(--accent-teal) 26%, var(--border-color));
    border-radius: 12px;
    background: linear-gradient(
        180deg,
        color-mix(in srgb, var(--accent-teal) 18%, var(--raised-bg)),
        var(--raised-bg)
    );
    cursor: pointer;
    text-align: left;
    transition:
        border-color 0.12s,
        transform 0.12s,
        background 0.12s;
}

.search-section-header:hover {
    border-color: color-mix(in srgb, var(--accent-teal) 45%, var(--border-color));
    transform: translateY(-1px);
}

.search-section-header.collapsed {
    opacity: 0.92;
}

.search-section.highlighted .search-section-header,
.search-section-header.highlighted {
    border-color: color-mix(in srgb, var(--accent-teal) 55%, var(--border-color));
    box-shadow:
        inset 4px 0 0 var(--accent-teal),
        0 0 0 1px color-mix(in srgb, var(--accent-teal) 18%, transparent);
}

.search-section-heading {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    min-width: 0;
}

.search-section-caret {
    font-size: 0.85rem;
    color: var(--accent-teal);
    flex-shrink: 0;
}

.search-section-title {
    font-size: 0.7rem;
    font-weight: 800;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--accent-teal) 70%, var(--font-color));
}

.search-section-count {
    font-size: 0.68rem;
    color: var(--accent-teal);
    font-variant-numeric: tabular-nums;
    border-radius: 999px;
    padding: 0.14rem 0.45rem;
    background: color-mix(in srgb, var(--accent-teal) 18%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent-teal) 28%, transparent);
}

.search-section-description {
    padding: 0 1rem 0.35rem;
    font-size: 0.72rem;
    color: var(--date-color);
    line-height: 1.35;
}

.search-note-item {
    border-left-width: 4px;
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
    min-height: 7.4em;
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

.save-info {
    padding: 0.4rem 1.25rem;
    font-size: 0.85rem;
    color: var(--tag-bg-color);
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

.auto-tag-list {
    gap: 0.45rem;
}

.auto-tag-chip {
    background: var(--raised-bg);
    color: var(--font-color);
    border: 1px solid var(--border-color);
}

.auto-tag-refresh-btn {
    padding: 0.2rem 0.55rem;
    font-size: 0.75rem;
}

.tag-hint {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
}

.auto-tag-hint {
    white-space: normal;
}

.auto-tag-error {
    padding: 0;
    background: transparent;
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
    top: calc(100% + 6px);
    left: 0;
    right: 0;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 12px;
    max-height: 260px;
    overflow-y: auto;
    z-index: 50;
    box-shadow: 0 10px 30px rgba(0, 0, 0, 0.16);
}

.parent-dropdown-status,
.parent-dropdown-section-label {
    padding: 0.45rem 0.75rem;
    font-size: 0.7rem;
    color: var(--font-color-secondary);
    background: rgba(255, 255, 255, 0.03);
}

.parent-dropdown-status {
    color: var(--accent-teal);
}

.parent-dropdown-section-label {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    font-weight: 800;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    border: none;
    border-top: 1px solid var(--border-color);
    cursor: pointer;
    text-align: left;
}

.parent-dropdown-section-label:first-of-type {
    border-top: none;
}

.parent-dropdown-section-heading {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
}

.parent-dropdown-caret {
    color: var(--accent-teal);
}

.parent-dropdown-section-count {
    border-radius: 999px;
    padding: 0.12rem 0.4rem;
    color: var(--accent-teal);
    background: color-mix(in srgb, var(--accent-teal) 18%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent-teal) 28%, transparent);
}

.parent-dropdown-item {
    display: flex;
    flex-direction: column;
    gap: 0.18rem;
    padding: 0.55rem 0.75rem;
    font-size: 0.82rem;
    cursor: pointer;
    color: var(--font-color);
    transition: background 0.1s;
}

.parent-dropdown-item:hover {
    background: var(--panel-bg);
}

.parent-dropdown-section-label.highlighted {
    color: color-mix(in srgb, var(--accent-teal) 78%, var(--font-color));
    box-shadow: inset 4px 0 0 var(--accent-teal);
}

.parent-dropdown-item.highlighted {
    background: color-mix(in srgb, var(--accent-teal) 12%, var(--panel-bg));
    box-shadow: inset 3px 0 0 var(--accent-teal);
}

.parent-dropdown-item.muted {
    color: var(--font-color-secondary);
    cursor: default;
}

.parent-dropdown-title-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
}

.parent-dropdown-title {
    flex: 1;
    min-width: 0;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.parent-dropdown-meta {
    font-size: 0.72rem;
    color: var(--date-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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

.chat-feed .body-textarea {
    field-sizing: content;
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
    width: 340px;
    max-width: min(340px, calc(100% - 16px));
    max-height: 260px;
    overflow-y: auto;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 12px;
    box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22);
    z-index: 50;
}

.link-search-section-header {
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.5rem 0.85rem;
    font-size: 0.68rem;
    font-weight: 800;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: color-mix(in srgb, var(--accent-teal) 70%, var(--font-color-secondary));
    background: linear-gradient(
        180deg,
        color-mix(in srgb, var(--accent-teal) 12%, rgba(255, 255, 255, 0.03)),
        rgba(255, 255, 255, 0.03)
    );
    border: none;
    border-top: 1px solid var(--border-color);
    cursor: pointer;
    text-align: left;
}

.link-search-section-header:first-of-type {
    border-top: none;
}

.link-search-section-heading {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
}

.link-search-caret {
    color: var(--accent-teal);
}

.link-search-item {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.8rem;
    padding: 0.65rem 0.85rem;
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

.link-search-section-header.highlighted {
    box-shadow: inset 4px 0 0 #ffb400;
    outline: 1px solid rgba(255, 180, 0, 0.35);
    background: #2f2000;
    color: #fff;
}

.link-search-item.highlighted {
    background: #2f2000;
    color: #fff;
    box-shadow: inset 4px 0 0 #ffb400;
    outline: 1px solid rgba(255, 180, 0, 0.35);
}

.link-search-item.highlighted .link-search-meta,
.link-search-item.highlighted .link-search-relevance {
    color: rgba(255, 255, 255, 0.92);
}

.link-search-body {
    display: flex;
    flex-direction: column;
    gap: 0.18rem;
    min-width: 0;
    flex: 1;
}

.link-search-title-row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
}

.link-search-title {
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
    flex: 1;
}

.link-search-meta {
    font-size: 0.72rem;
    color: var(--date-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.link-search-relevance {
    color: var(--font-color-secondary);
    font-size: 0.75rem;
    flex-shrink: 0;
    margin-left: 0.5rem;
    padding-top: 0.05rem;
}

.link-search-status {
    padding: 0.65rem 0.85rem;
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

.markdown-body :deep(img),
.markdown-body :deep(picture),
.markdown-body :deep(video) {
    display: block;
    max-width: 100%;
    max-height: max(25vh, 20em);
    margin: 0.6em auto;
    border-radius: 6px;
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

/* ── Spoiler / accordion ── */
.markdown-body :deep(details.spoiler) {
    border: 1px solid var(--border-color);
    border-radius: 8px;
    margin: 0.8em 0;
    padding: 0;
    overflow: hidden;
}
.markdown-body :deep(details.spoiler[open]) {
    background: var(--panel-bg);
}
.markdown-body :deep(details.spoiler summary) {
    padding: 0.65em 1em;
    cursor: pointer;
    font-weight: 600;
    color: var(--accent-teal);
    background: var(--raised-bg);
    user-select: none;
    border-radius: 8px;
    transition: background 0.15s;
    list-style: none;
}
.markdown-body :deep(details.spoiler summary::-webkit-details-marker) {
    display: none;
}
.markdown-body :deep(details.spoiler summary::marker) {
    display: none;
    content: "";
}
.markdown-body :deep(details.spoiler summary:hover) {
    background: var(--hover-bg);
}
.markdown-body :deep(details.spoiler[open] summary) {
    border-radius: 8px 8px 0 0;
}
/* Inner content wrapper */
.markdown-body :deep(.spoiler-content) {
    padding: 0.75em 1em;
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

.message-author.clickable {
    cursor: pointer;
}

.message-author.clickable:hover {
    color: var(--accent-amber);
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
    width: 33vw;
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
