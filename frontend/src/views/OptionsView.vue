<template>
    <div class="options-wrap">
        <div class="options-card">
            <div class="options-header">
                <h1 class="options-title">Options</h1>
                <button
                    class="btn-ghost back-btn"
                    title="Back to Notes"
                    @click="$emit('back')"
                >
                    ← Back to Notes
                </button>
            </div>

            <!-- Section: Job Queue -->
            <section class="options-section">
                <h2 class="section-title">Job Queue</h2>
                <p class="section-desc">
                    View and manage background jobs (embeddings, OCR, STT,
                    backups).
                </p>
                <div class="job-queue-embed">
                    <JobQueue :token="token" @job-done="() => {}" />
                </div>
            </section>

            <!-- Section: Backup -->
            <section class="options-section">
                <h2 class="section-title">Database Backup</h2>
                <p class="section-desc">
                    Create an AES-256-GCM encrypted backup and upload it to all
                    configured S3 endpoints.
                </p>
                <button
                    class="btn-amber"
                    :disabled="backingUp"
                    @click="triggerBackup"
                >
                    {{ backingUp ? "Enqueuing…" : "Create Backup Now" }}
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
                    class="btn-ghost"
                    :disabled="registeringPasskey"
                    @click="registerPasskey"
                >
                    &#128273;
                    {{
                        registeringPasskey
                            ? "Registering…"
                            : "Register Passkey"
                    }}
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
                        <div class="reindex-card-header">
                            <span class="reindex-icon">📝</span>
                            <div>
                                <h3>Re-Index Notes</h3>
                                <p class="reindex-card-desc">
                                    Re-generate vector embeddings for all notes
                                    missing them.
                                </p>
                            </div>
                        </div>
                        <button
                            class="btn-amber btn-sm"
                            :disabled="reindexingNotes"
                            @click="reindexNotes"
                        >
                            {{
                                reindexingNotes
                                    ? "Enqueuing…"
                                    : "Re-Index All Notes"
                            }}
                        </button>
                        <p v-if="reindexNotesErr" class="msg-error">
                            {{ reindexNotesErr }}
                        </p>
                        <p v-if="reindexNotesOk" class="msg-ok">
                            {{ reindexNotesOk }}
                        </p>
                    </div>

                    <!-- Reindex OCR -->
                    <div class="reindex-card">
                        <div class="reindex-card-header">
                            <span class="reindex-icon">🖼</span>
                            <div>
                                <h3>Re-Index OCR</h3>
                                <p class="reindex-card-desc">
                                    Re-generate embeddings for OCR-scanned file
                                    contents missing them.
                                </p>
                            </div>
                        </div>
                        <button
                            class="btn-amber btn-sm"
                            :disabled="reindexingOCR"
                            @click="reindexOCR"
                        >
                            {{
                                reindexingOCR
                                    ? "Enqueuing…"
                                    : "Re-Index OCR Files"
                            }}
                        </button>
                        <p v-if="reindexOCRErr" class="msg-error">
                            {{ reindexOCRErr }}
                        </p>
                        <p v-if="reindexOCROk" class="msg-ok">
                            {{ reindexOCROk }}
                        </p>
                    </div>

                    <!-- Reindex STT -->
                    <div class="reindex-card">
                        <div class="reindex-card-header">
                            <span class="reindex-icon">🎤</span>
                            <div>
                                <h3>Re-Index STT</h3>
                                <p class="reindex-card-desc">
                                    Re-generate embeddings for speech-to-text
                                    transcriptions missing them.
                                </p>
                            </div>
                        </div>
                        <button
                            class="btn-amber btn-sm"
                            :disabled="reindexingSTT"
                            @click="reindexSTT"
                        >
                            {{
                                reindexingSTT
                                    ? "Enqueuing…"
                                    : "Re-Index STT Files"
                            }}
                        </button>
                        <p v-if="reindexSTTErr" class="msg-error">
                            {{ reindexSTTErr }}
                        </p>
                        <p v-if="reindexSTTOk" class="msg-ok">
                            {{ reindexSTTOk }}
                        </p>
                    </div>
                </div>
            </section>

            <!-- Section: Logout -->
            <section class="options-section options-section-logout">
                <button class="btn-danger logout-btn" @click="doLogout">
                    ⏻ Logout
                </button>
            </section>
        </div>
    </div>
</template>

<script setup>
import { ref } from "vue";
import JobQueue from "../components/JobQueue.vue";
import {
    beginPasskeyRegistration,
    triggerBackup as apiTriggerBackup,
    reindexNotes as apiReindexNotes,
    reindexOCR as apiReindexOCR,
    reindexSTT as apiReindexSTT,
} from "../api.js";

const props = defineProps({ token: String });
const emit = defineEmits(["logout", "back"]);

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

const reindexingSTT = ref(false);
const reindexSTTErr = ref("");
const reindexSTTOk = ref("");

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

function doLogout() {
    emit("logout");
}
</script>

<style scoped>
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
    align-items: center;
    justify-content: space-between;
    margin-bottom: 2rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid var(--border-color);
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

/* Messages */
.msg-error {
    color: var(--heading-color);
    font-size: 0.8rem;
    margin-top: 0.5rem;
}

.msg-ok {
    color: var(--accent-teal);
    font-size: 0.8rem;
    margin-top: 0.5rem;
}

/* Job queue embedded in card */
.job-queue-embed {
    margin-top: 0.5rem;
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
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 0.75rem 1rem;
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

/* Logout */
.options-section-logout {
    text-align: center;
    border-bottom: none;
}

.logout-btn {
    padding: 0.6rem 2rem;
    font-size: 0.95rem;
}

@media (max-width: 600px) {
    .options-card {
        padding: 1.25rem;
    }

    .reindex-card {
        flex-direction: column;
        align-items: flex-start;
        gap: 0.5rem;
    }

    .reindex-card button {
        align-self: flex-end;
    }
}
</style>
