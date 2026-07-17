<template>
    <div class="options-wrap">
        <div class="options-card">
            <div class="options-header">
                    <div class="options-header-top">
                        <h1
                            class="options-title shortcut-anchor"
                            :title="getShortcutLabel('show-shortcuts')"
                        >
                            Options
                            <ShortcutHint
                                v-if="shortcutHintsVisible"
                                :label="getHintLabel('show-shortcuts')"
                            />
                        </h1>
                        <span class="ws-indicator" :class="{ connected: wsConnected, disconnected: !wsConnected }" :title="wsIndicatorTitle">
                            <span class="ws-dot"></span>
                            <span v-if="wsLatency != null" class="ws-latency">{{ wsLatency }} ms</span>
                        </span>
                    </div>
                    <button
                        class="btn-ghost back-btn shortcut-anchor"
                        :title="getShortcutLabel('back-to-notes')"
                        @click="goBack"
                    >
                        ← Back to Notes
                        <ShortcutHint
                            v-if="shortcutHintsVisible"
                            :label="getHintLabel('back-to-notes')"
                        />
                    </button>
            </div>

            <!-- Section: Server Stats -->
            <section class="options-section">
                <h2 class="section-title">Server Stats</h2>
                <p class="section-desc">
                    Overview of database size, embeddings, media usage, and
                    backup storage.
                </p>
                <button
                    class="btn-ghost"
                    :disabled="fetchingStats"
                    @click="fetchStats"
                >
                    {{ fetchingStats ? "Loading…" : "Refresh Stats" }}
                </button>
                <div v-if="serverStats" class="status-block">
                    <div class="status-row">
                        <span class="status-label">Total Notes</span>
                        <code class="status-value">{{
                            serverStats.total_notes >= 0
                                ? serverStats.total_notes.toLocaleString()
                                : "N/A"
                        }}</code>
                    </div>
                    <!-- Embeddings -->
                    <div class="status-row">
                        <span class="status-label">Note Embeddings</span>
                        <code class="status-value">{{
                            serverStats.vss_notes_count >= 0
                                ? serverStats.vss_notes_count
                                : "N/A"
                        }}</code>
                    </div>
                    <div class="status-row">
                        <span class="status-label">OCR Embeddings</span>
                        <code class="status-value">{{
                            serverStats.vss_ocr_count >= 0
                                ? serverStats.vss_ocr_count
                                : "N/A"
                        }}</code>
                    </div>
                    <div class="status-row">
                        <span class="status-label">STT Embeddings</span>
                        <code class="status-value">{{
                            serverStats.vss_stt_count >= 0
                                ? serverStats.vss_stt_count
                                : "N/A"
                        }}</code>
                    </div>
                    <!-- Sizes -->
                    <div class="status-row">
                        <span class="status-label">DB Size</span>
                        <code class="status-value">{{
                            formatBytes(serverStats.db_size_bytes)
                        }}</code>
                    </div>
                    <div class="status-row">
                        <span class="status-label">Media Usage</span>
                        <code class="status-value">{{
                            formatBytes(serverStats.media_size_bytes)
                        }}</code>
                    </div>
                    <!-- Backups -->
                    <div class="status-row">
                        <span class="status-label">Backup Count</span>
                        <code class="status-value">{{
                            serverStats.backup_count >= 0
                                ? serverStats.backup_count
                                : "N/A"
                        }}</code>
                    </div>
                    <div class="status-row">
                        <span class="status-label">Backup Size</span>
                        <code class="status-value">{{
                            formatBytes(serverStats.backup_size_bytes)
                        }}</code>
                    </div>
                    <div class="status-row stats-total">
                        <span class="status-label">Total Size</span>
                        <code class="status-value">{{
                            formatBytes(
                                (serverStats.db_size_bytes >= 0 ? serverStats.db_size_bytes : 0) +
                                (serverStats.media_size_bytes >= 0 ? serverStats.media_size_bytes : 0) +
                                (serverStats.backup_size_bytes >= 0 ? serverStats.backup_size_bytes : 0),
                            )
                        }}</code>
                    </div>
                    <div
                        v-if="serverStats.backup_error"
                        class="status-row"
                    >
                        <span class="status-label">Backup Error</span>
                        <span class="status-msg status-err-msg">{{
                            serverStats.backup_error
                        }}</span>
                    </div>
                </div>
                <p v-if="serverStatsErr" class="msg-error">
                    {{ serverStatsErr }}
                </p>
            </section>

            <!-- Section: Job Queue -->
            <section class="options-section">
                <h2 class="section-title">Job Queue</h2>
                <p class="section-desc">
                    View and manage background jobs (embeddings, OCR, STT,
                    backups).
                </p>
                <div class="job-queue-embed">
                    <JobQueue
                        :token="token"
                        :inline="true"
                        @job-done="() => {}"
                    />
                </div>
            </section>

            <!-- Section: Printer Connection -->
            <section class="options-section">
                <h2 class="section-title">Printer Connection</h2>
                <p class="section-desc">
                    Thermal receipt printer used for printing recipes and other
                    notes.
                </p>
                <button
                    class="btn-ghost shortcut-anchor"
                    :title="getShortcutLabel('check-printer')"
                    :disabled="checkingPrinter"
                    @click="checkPrinter"
                >
                    {{ checkingPrinter ? "Checking…" : "Check Connection" }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('check-printer')
                        "
                        :label="getHintLabel('check-printer')"
                    />
                </button>
                <div v-if="printerStatus" class="status-block">
                    <div class="status-row">
                        <span class="status-label">Status</span>
                        <span
                            class="status-badge"
                            :class="
                                printerStatus.connected
                                    ? 'status-ok'
                                    : 'status-err'
                            "
                        >
                            {{
                                printerStatus.connected
                                    ? "Connected"
                                    : "Not Connected"
                            }}
                        </span>
                    </div>
                    <div v-if="printerStatus.connected" class="status-row">
                        <span class="status-label">Details</span>
                        <span class="status-value">{{
                            printerStatus.method ||
                            printerStatus.device_path ||
                            "Detected"
                        }}</span>
                    </div>
                    <div v-if="printerStatus.error" class="status-row">
                        <span class="status-label">Error</span>
                        <span class="status-msg status-err-msg">{{
                            printerStatus.error
                        }}</span>
                    </div>
                    <div
                        v-if="
                            printerStatus.checked &&
                            printerStatus.checked.length
                        "
                        class="status-row"
                    >
                        <span class="status-label">Checked</span>
                        <span class="status-value">{{
                            printerStatus.checked.join(", ")
                        }}</span>
                    </div>
                </div>
                <p v-if="printerErr" class="msg-error">{{ printerErr }}</p>
            </section>

            <!-- Section: AI API Connection -->
            <section class="options-section">
                <h2 class="section-title">AI API Connection</h2>
                <p class="section-desc">
                    LocalAI instance providing embeddings, title generation,
                    OCR, and speech-to-text.
                </p>
                <button
                    class="btn-ghost shortcut-anchor"
                    :title="getShortcutLabel('check-ai')"
                    :disabled="checkingAI"
                    @click="checkAI"
                >
                    {{ checkingAI ? "Testing…" : "Test Connection" }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('check-ai')
                        "
                        :label="getHintLabel('check-ai')"
                    />
                </button>
                <div v-if="aiStatus" class="status-block">
                    <div class="status-row">
                        <span class="status-label">Base URL</span>
                        <code class="status-value">{{
                            aiStatus.base_url
                        }}</code>
                    </div>
                    <!-- VSS (Vector Search) -->
                    <div class="status-row">
                        <span class="status-label">VSS (Vector Search)</span>
                        <span
                            class="status-badge"
                            :class="
                                aiStatus.vss && aiStatus.vss.available
                                    ? 'status-ok'
                                    : 'status-err'
                            "
                        >
                            {{
                                aiStatus.vss && aiStatus.vss.available
                                    ? "Available"
                                    : "Unavailable"
                            }}
                        </span>
                    </div>
                    <div
                        v-if="
                            aiStatus.vss &&
                            aiStatus.vss.available &&
                            aiStatus.vss.notes_count !== undefined &&
                            aiStatus.vss.notes_count >= 0
                        "
                        class="status-row"
                    >
                        <span class="status-label">Note Embeddings</span>
                        <code class="status-value">{{
                            aiStatus.vss.notes_count
                        }}</code>
                    </div>
                    <div
                        v-if="
                            aiStatus.vss &&
                            aiStatus.vss.available &&
                            aiStatus.vss.ocr_files_count !== undefined &&
                            aiStatus.vss.ocr_files_count >= 0
                        "
                        class="status-row"
                    >
                        <span class="status-label">OCR Embeddings</span>
                        <code class="status-value">{{
                            aiStatus.vss.ocr_files_count
                        }}</code>
                    </div>
                    <div
                        v-if="
                            aiStatus.vss &&
                            aiStatus.vss.available &&
                            aiStatus.vss.stt_files_count !== undefined &&
                            aiStatus.vss.stt_files_count >= 0
                        "
                        class="status-row"
                    >
                        <span class="status-label">STT Embeddings</span>
                        <code class="status-value">{{
                            aiStatus.vss.stt_files_count
                        }}</code>
                    </div>
                    <div
                        v-if="aiStatus.vss && aiStatus.vss.error"
                        class="status-row"
                    >
                        <span class="status-label">VSS Error</span>
                        <span class="status-msg status-err-msg">{{
                            aiStatus.vss.error
                        }}</span>
                    </div>
                    <!-- Per-service status -->
                    <div
                        v-for="svc in ['embedding', 'chat', 'ocr', 'stt']"
                        :key="svc"
                        class="status-row"
                    >
                        <span class="status-label">{{
                            svc.charAt(0).toUpperCase() + svc.slice(1)
                        }}</span>
                        <span
                            class="status-badge"
                            :class="
                                aiStatus[svc].ok ? 'status-ok' : 'status-err'
                            "
                        >
                            {{ aiStatus[svc].ok ? "OK" : "Error" }}
                        </span>
                    </div>
                    <div
                        v-for="svc in ['embedding', 'chat', 'ocr', 'stt']"
                        :key="'model-' + svc"
                        class="status-row"
                    >
                        <span class="status-label">{{
                            svc.charAt(0).toUpperCase() +
                            svc.slice(1) +
                            " Model"
                        }}</span>
                        <code class="status-value">{{
                            aiStatus[svc].model
                        }}</code>
                    </div>
                    <div
                        v-for="svc in ['embedding', 'chat', 'ocr', 'stt']"
                        :key="'err-' + svc"
                    >
                        <div v-if="aiStatus[svc].error" class="status-row">
                            <span class="status-label">{{
                                svc.charAt(0).toUpperCase() +
                                svc.slice(1) +
                                " Error"
                            }}</span>
                            <span class="status-msg status-err-msg">{{
                                aiStatus[svc].error
                            }}</span>
                        </div>
                    </div>
                </div>
                <p v-if="aiErr" class="msg-error">{{ aiErr }}</p>
            </section>

            <!-- Section: Backup -->
            <section class="options-section">
                <h2 class="section-title">Database Backup</h2>
                <p class="section-desc">
                    Create an AES-256-GCM encrypted backup and upload it to all
                    configured S3 endpoints.
                </p>
                <button
                    class="btn-amber shortcut-anchor"
                    :title="getShortcutLabel('trigger-backup')"
                    :disabled="backingUp"
                    @click="triggerBackup"
                >
                    {{ backingUp ? "Enqueuing…" : "Create Backup Now" }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('trigger-backup')
                        "
                        :label="getHintLabel('trigger-backup')"
                    />
                </button>
                <p v-if="backupErr" class="msg-error">{{ backupErr }}</p>
                <p v-if="backupOk" class="msg-ok">{{ backupOk }}</p>
            </section>

            <!-- Section: Authentication -->
            <section class="options-section">
                <h2 class="section-title">Authentication</h2>
                <p class="section-desc">
                    Register a passkey to sign in without a password. Passkeys
                    are device-bound and more secure than passwords alone.
                </p>
                <button
                    class="btn-ghost shortcut-anchor"
                    :title="getShortcutLabel('register-passkey')"
                    :disabled="registeringPasskey"
                    @click="registerPasskey"
                >
                    &#128273;
                    {{
                        registeringPasskey ? "Registering…" : "Register Passkey"
                    }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('register-passkey')
                        "
                        :label="getHintLabel('register-passkey')"
                    />
                </button>
                <p v-if="regPasskeyErr" class="msg-error">
                    {{ regPasskeyErr }}
                </p>
                <p v-if="regPasskeyOk" class="msg-ok">Passkey registered.</p>
            </section>

            <!-- Section: Reindex & Maintenance -->
            <section class="options-section">
                <h2 class="section-title">Re-Index &amp; Maintenance</h2>
                <p class="section-desc">
                    Re-index notes or file contents whose vector embeddings are
                    missing. Use these if search isn't finding recent notes, or
                    after a model/embedding dimension change.
                </p>

                <div class="reindex-grid">
                    <!-- Reindex Notes -->
                    <div class="reindex-card">
                        <div class="reindex-card-top">
                            <div class="reindex-card-header">
                                <span class="reindex-icon">📝</span>
                                <div>
                                    <h3>Re-Index Notes</h3>
                                    <p class="reindex-card-desc">
                                        Re-generate vector embeddings for all
                                        notes missing them.
                                    </p>
                                </div>
                            </div>
                            <button
                                class="btn-amber btn-sm shortcut-anchor"
                                :title="getShortcutLabel('reindex-notes')"
                                :disabled="reindexingNotes"
                                @click="reindexNotes"
                            >
                                {{
                                    reindexingNotes
                                        ? "Enqueuing…"
                                        : "Re-Index All Notes"
                                }}
                                <ShortcutHint
                                    v-if="
                                        shortcutHintsVisible &&
                                        isShortcutEnabled('reindex-notes')
                                    "
                                    :label="getHintLabel('reindex-notes')"
                                />
                            </button>
                        </div>
                        <div
                            v-if="reindexNotesErr || reindexNotesOk"
                            class="reindex-card-status"
                        >
                            <p v-if="reindexNotesErr" class="msg-error">
                                {{ reindexNotesErr }}
                            </p>
                            <p v-if="reindexNotesOk" class="msg-ok">
                                {{ reindexNotesOk }}
                            </p>
                        </div>
                    </div>

                    <!-- Reindex OCR -->
                    <div class="reindex-card">
                        <div class="reindex-card-top">
                            <div class="reindex-card-header">
                                <span class="reindex-icon">🖼</span>
                                <div>
                                    <h3>Re-Index OCR</h3>
                                    <p class="reindex-card-desc">
                                        Re-generate embeddings for OCR-scanned
                                        file contents.
                                    </p>
                                </div>
                            </div>
                            <div class="reindex-card-actions">
                                <button
                                    class="btn-amber btn-sm shortcut-anchor"
                                    :title="getShortcutLabel('reindex-ocr')"
                                    :disabled="reindexingOCR || reindexingAllOCR"
                                    @click="reindexOCR"
                                >
                                    {{
                                        reindexingOCR
                                            ? "Enqueuing…"
                                            : "Re-Index Missing"
                                    }}
                                    <ShortcutHint
                                        v-if="
                                            shortcutHintsVisible &&
                                            isShortcutEnabled('reindex-ocr')
                                        "
                                        :label="getHintLabel('reindex-ocr')"
                                    />
                                </button>
                                <button
                                    class="btn-danger btn-sm"
                                    :disabled="reindexingOCR || reindexingAllOCR"
                                    @click="reindexAllOCR"
                                >
                                    {{
                                        reindexingAllOCR
                                            ? "Enqueuing…"
                                            : "Re-Index All"
                                    }}
                                </button>
                            </div>
                        </div>
                        <div
                            v-if="reindexOCRErr || reindexOCROk"
                            class="reindex-card-status"
                        >
                            <p v-if="reindexOCRErr" class="msg-error">
                                {{ reindexOCRErr }}
                            </p>
                            <p v-if="reindexOCROk" class="msg-ok">
                                {{ reindexOCROk }}
                            </p>
                        </div>
                        <div
                            v-if="reindexAllOCRErr || reindexAllOCROk"
                            class="reindex-card-status"
                        >
                            <p v-if="reindexAllOCRErr" class="msg-error">
                                {{ reindexAllOCRErr }}
                            </p>
                            <p v-if="reindexAllOCROk" class="msg-ok">
                                {{ reindexAllOCROk }}
                            </p>
                        </div>
                    </div>

                    <!-- Reindex STT -->
                    <div class="reindex-card">
                        <div class="reindex-card-top">
                            <div class="reindex-card-header">
                                <span class="reindex-icon">🎤</span>
                                <div>
                                    <h3>Re-Index STT</h3>
                                    <p class="reindex-card-desc">
                                        Re-generate embeddings for
                                        speech-to-text transcriptions.
                                    </p>
                                </div>
                            </div>
                            <div class="reindex-card-actions">
                                <button
                                    class="btn-amber btn-sm shortcut-anchor"
                                    :title="getShortcutLabel('reindex-stt')"
                                    :disabled="reindexingSTT || reindexingAllSTT"
                                    @click="reindexSTT"
                                >
                                    {{
                                        reindexingSTT
                                            ? "Enqueuing…"
                                            : "Re-Index Missing"
                                    }}
                                    <ShortcutHint
                                        v-if="
                                            shortcutHintsVisible &&
                                            isShortcutEnabled('reindex-stt')
                                        "
                                        :label="getHintLabel('reindex-stt')"
                                    />
                                </button>
                                <button
                                    class="btn-danger btn-sm"
                                    :disabled="reindexingSTT || reindexingAllSTT"
                                    @click="reindexAllSTT"
                                >
                                    {{
                                        reindexingAllSTT
                                            ? "Enqueuing…"
                                            : "Re-Index All"
                                    }}
                                </button>
                            </div>
                        </div>
                        <div
                            v-if="reindexSTTErr || reindexSTTOk"
                            class="reindex-card-status"
                        >
                            <p v-if="reindexSTTErr" class="msg-error">
                                {{ reindexSTTErr }}
                            </p>
                            <p v-if="reindexSTTOk" class="msg-ok">
                                {{ reindexSTTOk }}
                            </p>
                        </div>
                        <div
                            v-if="reindexAllSTTErr || reindexAllSTTOk"
                            class="reindex-card-status"
                        >
                            <p v-if="reindexAllSTTErr" class="msg-error">
                                {{ reindexAllSTTErr }}
                            </p>
                            <p v-if="reindexAllSTTOk" class="msg-ok">
                                {{ reindexAllSTTOk }}
                            </p>
                        </div>
                    </div>

                    <!-- Refresh All Auto Tags -->
                    <div class="reindex-card">
                        <div class="reindex-card-top">
                            <div class="reindex-card-header">
                                <span class="reindex-icon">🏷</span>
                                <div>
                                    <h3>Refresh All Auto Tags</h3>
                                    <p class="reindex-card-desc">
                                        Re-run the chat model across all notes to
                                        rebuild generated auto tags from the
                                        latest content and current tag catalog.
                                    </p>
                                </div>
                            </div>
                            <button
                                class="btn-amber btn-sm shortcut-anchor"
                                :title="getShortcutLabel('refresh-auto-tags')"
                                :disabled="refreshingAllAutoTags"
                                @click="refreshAllAutoTags"
                            >
                                {{
                                    refreshingAllAutoTags
                                        ? "Enqueuing…"
                                        : "Refresh All Auto Tags"
                                }}
                                <ShortcutHint
                                    v-if="
                                        shortcutHintsVisible &&
                                        isShortcutEnabled('refresh-auto-tags')
                                    "
                                    :label="getHintLabel('refresh-auto-tags')"
                                />
                            </button>
                        </div>
                        <div
                            v-if="
                                refreshAllAutoTagsErr ||
                                refreshAllAutoTagsOk
                            "
                            class="reindex-card-status"
                        >
                            <p v-if="refreshAllAutoTagsErr" class="msg-error">
                                {{ refreshAllAutoTagsErr }}
                            </p>
                            <p v-if="refreshAllAutoTagsOk" class="msg-ok">
                                {{ refreshAllAutoTagsOk }}
                            </p>
                        </div>
                    </div>

                    <!-- Recalculate Ingredient Categories -->
                    <div class="reindex-card">
                        <div class="reindex-card-top">
                            <div class="reindex-card-header">
                                <span class="reindex-icon">🛒</span>
                                <div>
                                    <h3>Recalculate Ingredient Categories</h3>
                                    <p class="reindex-card-desc">
                                        Re-run embedding matching for all recipe
                                        ingredients and refresh their stored
                                        grocery categories.
                                    </p>
                                </div>
                            </div>
                            <button
                                class="btn-amber btn-sm shortcut-anchor"
                                :title="
                                    getShortcutLabel(
                                        'recalculate-recipe-categories',
                                    )
                                "
                                :disabled="recalculatingRecipeCategories"
                                @click="recalculateRecipeCategories"
                            >
                                {{
                                    recalculatingRecipeCategories
                                        ? "Enqueuing…"
                                        : "Recalculate All Ingredient Categories"
                                }}
                                <ShortcutHint
                                    v-if="
                                        shortcutHintsVisible &&
                                        isShortcutEnabled(
                                            'recalculate-recipe-categories',
                                        )
                                    "
                                    :label="
                                        getHintLabel(
                                            'recalculate-recipe-categories',
                                        )
                                    "
                                />
                            </button>
                        </div>
                        <div
                            v-if="
                                recalculateRecipeCategoriesErr ||
                                recalculateRecipeCategoriesOk
                            "
                            class="reindex-card-status"
                        >
                            <p
                                v-if="recalculateRecipeCategoriesErr"
                                class="msg-error"
                            >
                                {{ recalculateRecipeCategoriesErr }}
                            </p>
                            <p
                                v-if="recalculateRecipeCategoriesOk"
                                class="msg-ok"
                            >
                                {{ recalculateRecipeCategoriesOk }}
                            </p>
                        </div>
                    </div>
                </div>
            </section>

            <!-- Section: Remove Orphaned S3 Objects -->
            <section class="options-section">
                <h2 class="section-title">Remove Orphaned S3 Objects</h2>
                <p class="section-desc">
                    Delete S3 objects under <code>files/</code> that are no
                    longer referenced by the database. This frees storage
                    without affecting any notes or attachments.
                </p>
                <button
                    class="btn-danger btn-sm shortcut-anchor"
                    :title="getShortcutLabel('delete-unknown-s3')"
                    :disabled="deletingUnknownS3"
                    @click="deleteUnknownS3"
                >
                    {{
                        deletingUnknownS3
                            ? "Scanning…"
                            : "Delete Unknown S3 Files"
                    }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('delete-unknown-s3')
                        "
                        :label="getHintLabel('delete-unknown-s3')"
                    />
                </button>
                <div v-if="deleteUnknownS3Result" class="status-block">
                    <div class="status-row">
                        <span class="status-label">Total deleted</span>
                        <code class="status-value">{{
                            deleteUnknownS3Result.deleted
                        }}</code>
                    </div>
                    <div
                        v-for="ep in deleteUnknownS3Result.by_endpoint"
                        :key="ep.endpoint"
                        class="status-row"
                    >
                        <span class="status-label">{{ ep.endpoint }}</span>
                        <span
                            class="status-badge"
                            :class="ep.error ? 'status-err' : 'status-ok'"
                        >
                            {{ ep.error ? "Error" : ep.deleted + " deleted" }}
                        </span>
                        <span
                            v-if="ep.error"
                            class="status-msg status-err-msg"
                            >{{ ep.error }}</span
                        >
                    </div>
                    <div
                        v-if="
                            deleteUnknownS3Result.errors &&
                            deleteUnknownS3Result.errors.length
                        "
                        class="status-row"
                    >
                        <span class="status-label">Errors</span>
                        <ul class="error-list">
                            <li
                                v-for="(e, i) in deleteUnknownS3Result.errors"
                                :key="i"
                            >
                                <code>{{ e }}</code>
                            </li>
                        </ul>
                    </div>
                </div>
                <p v-if="deleteUnknownS3Err" class="msg-error">
                    {{ deleteUnknownS3Err }}
                </p>
            </section>

            <!-- Section: Logout -->
            <section class="options-section options-section-logout">
                <button
                    class="btn-danger logout-btn shortcut-anchor"
                    :title="getShortcutLabel('logout')"
                    @click="doLogout"
                >
                    ⏻ Logout
                    <ShortcutHint
                        v-if="shortcutHintsVisible"
                        :label="getHintLabel('logout')"
                    />
                </button>
            </section>

            <KeyboardShortcutsHelpModal
                v-model="showHotkeys"
                :items="hotkeys"
            />
        </div>
    </div>
</template>

<script setup>
import { computed, onMounted, ref } from "vue";
import JobQueue from "../components/JobQueue.vue";
import ShortcutHint from "../components/ShortcutHint.vue";
import KeyboardShortcutsHelpModal from "../components/KeyboardShortcutsHelpModal.vue";
import {
    beginPasskeyRegistration,
    logout as apiLogout,
    triggerBackup as apiTriggerBackup,
    reindexNotes as apiReindexNotes,
    reindexOCR as apiReindexOCR,
    reindexAllOCR as apiReindexAllOCR,
    reindexSTT as apiReindexSTT,
    reindexAllSTT as apiReindexAllSTT,
    refreshAllAutoTags as apiRefreshAllAutoTags,
    recalculateRecipeCategories as apiRecalculateRecipeCategories,
    deleteUnknownS3Files as apiDeleteUnknownS3,
    fetchPrinterStatus,
    fetchAIStatus,
    fetchServerStats,
} from "../api.js";
import { useKeyboardShortcuts } from "../composables/useKeyboardShortcuts.js";

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
const emit = defineEmits(["logout", "back"]);

// Printer
const checkingPrinter = ref(false);
const printerStatus = ref(null);
const printerErr = ref("");

// AI
const checkingAI = ref(false);
const aiStatus = ref(null);
const aiErr = ref("");

// Passkey
const registeringPasskey = ref(false);
const regPasskeyErr = ref("");
const regPasskeyOk = ref(false);

// Backup
const backingUp = ref(false);
const backupErr = ref("");
const backupOk = ref("");

// Reindex
const reindexingNotes = ref(false);
const reindexNotesErr = ref("");
const reindexNotesOk = ref("");

const reindexingOCR = ref(false);
const reindexOCRErr = ref("");
const reindexOCROk = ref("");

const reindexingAllOCR = ref(false);
const reindexAllOCRErr = ref("");
const reindexAllOCROk = ref("");

const reindexingSTT = ref(false);
const reindexSTTErr = ref("");
const reindexSTTOk = ref("");

const reindexingAllSTT = ref(false);
const reindexAllSTTErr = ref("");
const reindexAllSTTOk = ref("");

const refreshingAllAutoTags = ref(false);
const refreshAllAutoTagsErr = ref("");
const refreshAllAutoTagsOk = ref("");

const recalculatingRecipeCategories = ref(false);
const recalculateRecipeCategoriesErr = ref("");
const recalculateRecipeCategoriesOk = ref("");

	// Delete unknown S3 files
	const deletingUnknownS3 = ref(false);
	const deleteUnknownS3Err = ref("");
	const deleteUnknownS3Result = ref(null);

	// Server stats
	const fetchingStats = ref(false);
	const serverStats = ref(null);
	const serverStatsErr = ref("");

function goBack() {
    if (showHotkeys.value) {
        showHotkeys.value = false;
        return;
    }
    emit("back");
}

function toggleHotkeysHelp() {
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
        id: "back-to-notes",
        description: "Back to notes",
        hintKey: "H",
        keys: ["Escape", "Mod+,"],
        allowInInput: true,
        handler: () => goBack(),
    },
    {
        id: "check-printer",
        description: "Check printer connection",
        hintKey: "P",
        allowInInput: true,
        enabled: () => !checkingPrinter.value,
        handler: () => checkPrinter(),
    },
    {
        id: "check-ai",
        description: "Test AI connection",
        hintKey: "A",
        allowInInput: true,
        enabled: () => !checkingAI.value,
        handler: () => checkAI(),
    },
    {
        id: "trigger-backup",
        description: "Create a backup now",
        hintKey: "B",
        allowInInput: true,
        enabled: () => !backingUp.value,
        handler: () => triggerBackup(),
    },
    {
        id: "register-passkey",
        description: "Register a passkey",
        hintKey: "R",
        allowInInput: true,
        enabled: () => !registeringPasskey.value,
        handler: () => registerPasskey(),
    },
    {
        id: "reindex-notes",
        description: "Re-index all notes",
        hintKey: "N",
        allowInInput: true,
        enabled: () => !reindexingNotes.value,
        handler: () => reindexNotes(),
    },
    {
        id: "reindex-ocr",
        description: "Re-index OCR files",
        hintKey: "O",
        allowInInput: true,
        enabled: () => !reindexingOCR.value,
        handler: () => reindexOCR(),
    },
    {
        id: "reindex-stt",
        description: "Re-index STT files",
        hintKey: "T",
        allowInInput: true,
        enabled: () => !reindexingSTT.value,
        handler: () => reindexSTT(),
    },
    {
        id: "refresh-auto-tags",
        description: "Refresh all auto tags",
        hintKey: "U",
        allowInInput: true,
        enabled: () => !refreshingAllAutoTags.value,
        handler: () => refreshAllAutoTags(),
    },
    {
        id: "recalculate-recipe-categories",
        description: "Recalculate all recipe ingredient categories",
        hintKey: "G",
        allowInInput: true,
        enabled: () => !recalculatingRecipeCategories.value,
        handler: () => recalculateRecipeCategories(),
    },
    {
        id: "delete-unknown-s3",
        description: "Delete unknown S3 files",
        hintKey: "D",
        allowInInput: true,
        enabled: () => !deletingUnknownS3.value,
        handler: () => deleteUnknownS3(),
    },
    {
        id: "logout",
        description: "Log out",
        hintKey: "L",
        allowInInput: true,
        handler: () => doLogout(),
    },
]);

const {
    showHelp: showHotkeys,
    hintOverlayVisible: shortcutHintsVisible,
    helpItems: hotkeys,
    getHintLabel,
    getShortcutLabel,
    isShortcutEnabled,
} = useKeyboardShortcuts(shortcutDefinitions);

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

async function checkPrinter() {
    printerErr.value = "";
    printerStatus.value = null;
    checkingPrinter.value = true;
    try {
        printerStatus.value = await fetchPrinterStatus(props.token);
    } catch (e) {
        printerErr.value = e.message || "Failed to check printer status";
    } finally {
        checkingPrinter.value = false;
    }
}

async function checkAI() {
    aiErr.value = "";
    aiStatus.value = null;
    checkingAI.value = true;
    try {
        aiStatus.value = await fetchAIStatus(props.token);
    } catch (e) {
        aiErr.value = e.message || "Failed to check AI status";
    } finally {
        checkingAI.value = false;
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
        }, 10000);
    } catch (e) {
        backupErr.value = e.message || "Backup failed";
    } finally {
        backingUp.value = false;
    }
}

async function reindexNotes() {
    reindexNotesErr.value = "";
    reindexNotesOk.value = "";
    reindexingNotes.value = true;
    try {
        const res = await apiReindexNotes(props.token);
        reindexNotesOk.value = res.message;
        setTimeout(() => {
            reindexNotesOk.value = "";
        }, 10000);
    } catch (e) {
        reindexNotesErr.value = e.message || "Re-index failed";
    } finally {
        reindexingNotes.value = false;
    }
}

async function reindexOCR() {
    reindexOCRErr.value = "";
    reindexOCROk.value = "";
    reindexingOCR.value = true;
    try {
        const res = await apiReindexOCR(props.token);
        reindexOCROk.value = res.message;
        setTimeout(() => {
            reindexOCROk.value = "";
        }, 10000);
    } catch (e) {
        reindexOCRErr.value = e.message || "Re-index OCR failed";
    } finally {
        reindexingOCR.value = false;
    }
}

async function reindexSTT() {
    reindexSTTErr.value = "";
    reindexSTTOk.value = "";
    reindexingSTT.value = true;
    try {
        const res = await apiReindexSTT(props.token);
        reindexSTTOk.value = res.message;
        setTimeout(() => {
            reindexSTTOk.value = "";
        }, 10000);
    } catch (e) {
        reindexSTTErr.value = e.message || "Re-index STT failed";
    } finally {
        reindexingSTT.value = false;
    }
}

async function reindexAllOCR() {
    reindexAllOCRErr.value = "";
    reindexAllOCROk.value = "";
    if (!confirm("⚠️ This will re-index ALL OCR files, even those already indexed. This may take a long time and consume API resources. Continue?")) {
        return;
    }
    reindexingAllOCR.value = true;
    try {
        const res = await apiReindexAllOCR(props.token);
        reindexAllOCROk.value = res.message;
        setTimeout(() => {
            reindexAllOCROk.value = "";
        }, 10000);
    } catch (e) {
        reindexAllOCRErr.value = e.message || "Re-index all OCR failed";
    } finally {
        reindexingAllOCR.value = false;
    }
}

async function reindexAllSTT() {
    reindexAllSTTErr.value = "";
    reindexAllSTTOk.value = "";
    if (!confirm("⚠️ This will re-index ALL STT files, even those already indexed. This may take a long time and consume API resources. Continue?")) {
        return;
    }
    reindexingAllSTT.value = true;
    try {
        const res = await apiReindexAllSTT(props.token);
        reindexAllSTTOk.value = res.message;
        setTimeout(() => {
            reindexAllSTTOk.value = "";
        }, 10000);
    } catch (e) {
        reindexAllSTTErr.value = e.message || "Re-index all STT failed";
    } finally {
        reindexingAllSTT.value = false;
    }
}

async function refreshAllAutoTags() {
    refreshAllAutoTagsErr.value = "";
    refreshAllAutoTagsOk.value = "";
    refreshingAllAutoTags.value = true;
    try {
        const res = await apiRefreshAllAutoTags(props.token);
        refreshAllAutoTagsOk.value =
            res.message ||
            `Auto-tag refresh queued (run #${res.run_id}). Check the job queue for progress.`;
        setTimeout(() => {
            refreshAllAutoTagsOk.value = "";
        }, 10000);
    } catch (e) {
        refreshAllAutoTagsErr.value = e.message || "Auto-tag refresh failed";
    } finally {
        refreshingAllAutoTags.value = false;
    }
}

async function recalculateRecipeCategories() {
    recalculateRecipeCategoriesErr.value = "";
    recalculateRecipeCategoriesOk.value = "";
    recalculatingRecipeCategories.value = true;
    try {
        const res = await apiRecalculateRecipeCategories(props.token);
        recalculateRecipeCategoriesOk.value = res.message;
        setTimeout(() => {
            recalculateRecipeCategoriesOk.value = "";
        }, 10000);
    } catch (e) {
        recalculateRecipeCategoriesErr.value =
            e.message || "Ingredient category recalculation failed";
    } finally {
        recalculatingRecipeCategories.value = false;
    }
}

async function deleteUnknownS3() {
    deleteUnknownS3Err.value = "";
    deleteUnknownS3Result.value = null;
    deletingUnknownS3.value = true;
    try {
        const res = await apiDeleteUnknownS3(props.token);
        deleteUnknownS3Result.value = res;
    } catch (e) {
        deleteUnknownS3Err.value = e.message || "S3 cleanup failed";
    } finally {
        deletingUnknownS3.value = false;
    }
}

async function doLogout() {
    try {
        await apiLogout();
    } finally {
        emit("logout");
    }
}

function formatBytes(bytes) {
    if (bytes == null || bytes < 0) return "N/A";
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const val = bytes / Math.pow(1024, i);
    return val.toFixed(val < 10 ? 1 : 0) + " " + units[i];
}

async function fetchStats() {
    serverStatsErr.value = "";
    serverStats.value = null;
    fetchingStats.value = true;
    try {
        serverStats.value = await fetchServerStats(props.token);
    } catch (e) {
        serverStatsErr.value = e.message || "Failed to load server stats";
    } finally {
        fetchingStats.value = false;
    }
}

onMounted(() => {
    fetchStats();
});
</script>

<style scoped>
.shortcut-anchor {
    position: relative;
}

.options-wrap {
    min-height: 100vh;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: 2rem 1rem;
}

.options-card {
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 12px;
    padding: 2rem;
    width: 100%;
    max-width: 700px;
}

.options-header {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    margin-bottom: 2rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid var(--border-color);
}

.options-header-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
}

.options-title {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--header-title-color);
    letter-spacing: 0.03em;
    margin: 0;
}

.back-btn {
    font-size: 0.85rem;
}

/* Sections */
.options-section {
    margin-bottom: 2rem;
    padding-bottom: 1.5rem;
    border-bottom: 1px solid var(--border-color);
}

.options-section:last-of-type {
    border-bottom: none;
    margin-bottom: 1rem;
}

.section-title {
    font-size: 1rem;
    font-weight: 600;
    color: var(--font-color);
    margin-bottom: 0.35rem;
}

.section-desc {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    margin-bottom: 0.75rem;
    line-height: 1.5;
}

/* Status block (used by Printer & AI API sections) */
.status-block {
    margin-top: 0.75rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.75rem 1rem;
}

.status-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--border-color);
}

.status-row:last-child {
	border-bottom: none;
}

.stats-total {
	border-top: 2px solid var(--border-color);
	margin-top: 0.3rem;
	padding-top: 0.6rem;
}

.stats-total .status-label {
	font-weight: 600;
	color: var(--font-color);
}

.status-label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--font-color-secondary);
    flex-shrink: 0;
    min-width: 100px;
}

.status-value {
    font-size: 0.8rem;
    color: var(--font-color);
    text-align: right;
    word-break: break-all;
}

.status-badge {
    display: inline-block;
    font-size: 0.75rem;
    font-weight: 600;
    padding: 2px 8px;
    border-radius: 4px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
}

.status-ok {
    background: rgba(74, 222, 128, 0.12);
    color: var(--accent-teal);
}

.status-err {
    background: rgba(248, 113, 113, 0.12);
    color: var(--heading-color);
}

.status-msg {
    font-size: 0.78rem;
    text-align: right;
    word-break: break-all;
    line-height: 1.5;
}

.status-err-msg {
    color: var(--heading-color);
}

/* Messages */
.msg-error {
    color: var(--heading-color);
    font-size: 0.8rem;
    margin: 0;
}

.msg-ok {
    color: var(--accent-teal);
    font-size: 0.8rem;
    margin: 0;
}

/* Job queue embedded in card */
.job-queue-embed {
    margin-top: 0.5rem;
}

/* Error list */
.error-list {
    margin: 0;
    padding-left: 1.25rem;
    font-size: 0.8rem;
}

.error-list li {
    margin: 0;
}

/* Reindex grid */
.reindex-grid {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    margin-top: 0.75rem;
}

.reindex-card {
    display: flex;
    flex-direction: column;
    gap: 0.65rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.75rem 1rem;
}

.reindex-card-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
}

.reindex-card-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-shrink: 0;
}

.reindex-card-header {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex: 1;
    min-width: 0;
}

.reindex-icon {
    font-size: 1.4rem;
    flex-shrink: 0;
}

.reindex-card h3 {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--font-color);
    margin: 0 0 2px;
}

.reindex-card-desc {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    margin: 0;
    line-height: 1.4;
}

.reindex-card-status {
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding-left: 2rem;
}

/* Logout */
.options-section-logout {
    text-align: center;
    border-bottom: none;
}

.logout-btn {
    padding: 0.6rem 2rem;
    font-size: 0.95rem;
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

@media (max-width: 600px) {
    .options-card {
        padding: 1.25rem;
    }

    .reindex-card-top {
        flex-direction: column;
        align-items: flex-start;
        gap: 0.5rem;
    }

    .reindex-card button {
        align-self: flex-end;
    }

    .reindex-card-status {
        padding-left: 0;
    }
}
</style>
