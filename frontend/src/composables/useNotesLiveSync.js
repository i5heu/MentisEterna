import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const deviceId =
    typeof crypto !== "undefined" && crypto.randomUUID
        ? crypto.randomUUID()
        : `dev-${Math.random().toString(36).slice(2)}`;

const PRESENCE_TTL_MS = 30000;

export function useNotesLiveSync({
    selected,
    threadNote,
    isEditing,
    editBody,
    dirty,
    saving,
    hasSidebarSearch,
    activeUploads,
    sendLiveMessage,
    loadNotes,
    refreshSidebarSearch,
    refreshSelectedCollections,
    refreshSelectedInPlace,
    refreshThreadNoteInPlace,
    applyInlineUploadResolution,
}) {
    const liveRefreshPending = ref(false);
    const presence = ref({});
    const editingElsewhere = computed(() => {
        const id = selected.value?.id;
        if (!id) return false;
        const entry = presence.value[String(id)];
        return Boolean(entry && entry.editing && entry.device_id !== deviceId);
    });

    let liveRefreshTimer = null;
    let liveRefreshRunning = false;
    let liveRefreshQueued = false;
    let liveRefreshFullRequested = false;
    let liveRefreshSelectedRequested = false;
    let liveRefreshThreadRequested = false;
    let presenceTimer = null;
    let editBodySendTimer = null;
    let lastEditBodySent = {};

    watch([isEditing, () => selected.value?.id], ([editing, noteID]) => {
        if (!noteID) return;
        sendLiveMessage({
            type: "edit.sync",
            note_id: Number(noteID),
            editing: Boolean(editing),
            device_id: deviceId,
        });
    });

    watch(editBody, (body) => {
        const noteID = selected.value?.id;
        if (!noteID || !isEditing.value) return;
        const key = String(noteID);
        if (body === lastEditBodySent[key]) return;
        if (editBodySendTimer) clearTimeout(editBodySendTimer);
        editBodySendTimer = setTimeout(() => {
            editBodySendTimer = null;
            lastEditBodySent[key] = body;
            sendLiveMessage({ type: "edit.body", note_id: Number(noteID), body });
        }, 300);
    });

    function applyRemoteBody(noteID, body, fromDeviceID) {
        const target = selected.value;
        if (!target || target.id !== noteID || fromDeviceID === deviceId) return;
        if (editBody.value === body) return;
        lastEditBodySent[String(noteID)] = body;
        editBody.value = body;
        dirty.value = true;
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
                    await refreshSidebarSearch();
                }
            }
            const selectedNoteID = selected.value?.id;
            if (refreshSelected && selectedNoteID) {
                if (activeUploads.value && activeUploads.value.length > 0) {
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

    function scheduleLiveRefresh({
        full = false,
        selected: refreshSelected = false,
        thread: refreshThread = false,
    } = {}) {
        liveRefreshFullRequested = liveRefreshFullRequested || full;
        liveRefreshSelectedRequested =
            liveRefreshSelectedRequested || refreshSelected;
        liveRefreshThreadRequested = liveRefreshThreadRequested || refreshThread;
        if (liveRefreshTimer) return;
        liveRefreshTimer = window.setTimeout(() => {
            liveRefreshTimer = null;
            runLiveRefresh();
        }, 100);
    }

    function prunePresence() {
        const now = Date.now();
        for (const key of Object.keys(presence.value)) {
            const entry = presence.value[key];
            if (!entry || now - (entry.ts || 0) > PRESENCE_TTL_MS) {
                presence.value[key] = undefined;
            }
        }
    }

    function onLiveMessage(event) {
        const detail = event?.detail;
        if (!detail?.type) return;

        prunePresence();

        if (detail.type === "edit.sync") {
            const noteID = Number(detail.note_id);
            if (noteID > 0) {
                if (detail.editing) {
                    presence.value[String(noteID)] = {
                        editing: true,
                        device_id: detail.device_id || "",
                        ts: Date.now(),
                    };
                } else {
                    presence.value[String(noteID)] = undefined;
                }
            }
            return;
        }

        if (detail.type === "edit.body") {
            const noteID = Number(detail.note_id);
            if (noteID > 0) {
                applyRemoteBody(
                    noteID,
                    typeof detail.body === "string" ? detail.body : "",
                    detail.device_id || "",
                );
            }
            return;
        }

        if (detail.type === "live.ready") {
            scheduleLiveRefresh({
                full: true,
                selected: Boolean(selected.value?.id),
                thread: Boolean(threadNote.value?.id),
            });
            return;
        }

        if (detail.type !== "notes.changed") return;

        if (
            detail.reason === "inline_upload_resolved" &&
            detail.upload_resolution?.note_id
        ) {
            applyInlineUploadResolution(detail.upload_resolution);
            scheduleLiveRefresh({ full: true, selected: false, thread: false });
            return;
        }

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

    onMounted(() => {
        window.addEventListener("live:message", onLiveMessage);
        presenceTimer = window.setInterval(prunePresence, 10000);
    });

    onUnmounted(() => {
        window.removeEventListener("live:message", onLiveMessage);
        if (editBodySendTimer) {
            clearTimeout(editBodySendTimer);
            editBodySendTimer = null;
        }
        if (presenceTimer) {
            window.clearInterval(presenceTimer);
            presenceTimer = null;
        }
        if (liveRefreshTimer) {
            window.clearTimeout(liveRefreshTimer);
            liveRefreshTimer = null;
        }
    });

    return {
        editingElsewhere,
        liveRefreshPending,
        scheduleLiveRefresh,
    };
}
