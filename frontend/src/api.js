async function request(path, options = {}) {
    const res = await fetch(path, {
        credentials: "include",
        ...options,
    });
    if (res.status === 401) {
        window.dispatchEvent(new CustomEvent("auth:unauthorized"));
        throw new Error("unauthorized");
    }
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    return res.json();
}

function authHeaders(_token) {
    return {
        "Content-Type": "application/json",
    };
}

function authOnlyHeaders(_token) {
    return {};
}

let liveSocket = null;
let liveReconnectTimer = null;
let liveReconnectAttempt = 0;
let liveConnectionDesired = false;
let livePingTimer = null;
let livePingSentAt = null;
let liveWorker = null;

function dispatchWindowEvent(name, detail) {
    if (typeof window === "undefined") return;
    window.dispatchEvent(new CustomEvent(name, { detail }));
}

function buildLiveURL() {
    const scheme = window.location.protocol === "https:" ? "wss" : "ws";
    return `${scheme}://${window.location.host}/ws`;
}

function roundLatency(ms) {
    return Math.round(ms * 10) / 10;
}

function buildLatencyDetail(payload, fallbackClientSentAt) {
    const clientSentAt =
        typeof payload.client_sent_at_ms === "number"
            ? payload.client_sent_at_ms
            : fallbackClientSentAt;
    if (typeof clientSentAt !== "number") {
        return null;
    }

    const detail = {
        ms: roundLatency(performance.now() - clientSentAt),
        clientSentAtMs: clientSentAt,
    };

    if (
        typeof payload.server_received_at_us === "number" &&
        typeof payload.server_sent_at_us === "number" &&
        payload.server_sent_at_us >= payload.server_received_at_us
    ) {
        detail.serverReceivedAtUs = payload.server_received_at_us;
        detail.serverSentAtUs = payload.server_sent_at_us;
        detail.serverProcessingMs = roundLatency(
            (payload.server_sent_at_us - payload.server_received_at_us) / 1000,
        );
    }

    return detail;
}

function handleLiveWorkerMessage(event) {
    const data = event.data || {};
    if (data.type === "status") {
        dispatchWindowEvent("live:status", data.detail || {});
        return;
    }
    if (data.type === "latency") {
        dispatchWindowEvent("live:latency", data.detail || {});
        return;
    }
    if (data.type === "message") {
        dispatchWindowEvent("live:message", data.detail);
    }
}

function ensureLiveWorker() {
    if (typeof window === "undefined" || typeof Worker === "undefined") {
        return null;
    }
    if (liveWorker) {
        return liveWorker;
    }
    try {
        liveWorker = new Worker(new URL("./live.worker.js", import.meta.url), {
            type: "module",
        });
        liveWorker.onmessage = handleLiveWorkerMessage;
        liveWorker.onerror = (error) => {
            console.error("live worker failed", error);
        };
    } catch (error) {
        console.error("live worker unavailable", error);
        liveWorker = null;
    }
    return liveWorker;
}

function scheduleLiveReconnect() {
    if (!liveConnectionDesired || liveReconnectTimer) return;
    const delay = Math.min(1000 * 2 ** liveReconnectAttempt, 10000);
    liveReconnectAttempt += 1;
    liveReconnectTimer = window.setTimeout(() => {
        liveReconnectTimer = null;
        openLiveSocket();
    }, delay);
}

function openLiveSocket() {
    if (typeof window === "undefined" || typeof WebSocket === "undefined") {
        return;
    }
    if (
        liveSocket &&
        (liveSocket.readyState === WebSocket.OPEN ||
            liveSocket.readyState === WebSocket.CONNECTING)
    ) {
        return;
    }

    const socket = new WebSocket(buildLiveURL());
    liveSocket = socket;
    dispatchWindowEvent("live:status", { connected: false, connecting: true });

    socket.onopen = () => {
        if (liveSocket !== socket) {
            socket.close();
            return;
        }
        liveReconnectAttempt = 0;
        dispatchWindowEvent("live:status", {
            connected: true,
            connecting: false,
        });
        startLivePings(socket);
    };

    socket.onmessage = (event) => {
        try {
            const payload = JSON.parse(event.data);
            if (payload.type === "pong") {
                const detail = buildLatencyDetail(payload, livePingSentAt);
                livePingSentAt = null;
                if (detail) {
                    dispatchWindowEvent("live:latency", detail);
                }
                return;
            }
            dispatchWindowEvent("live:message", payload);
        } catch (error) {
            console.error("live message parse failed", error);
        }
    };

    socket.onerror = () => {
        socket.close();
    };

    socket.onclose = () => {
        if (liveSocket === socket) {
            liveSocket = null;
        }
        stopLivePings();
        dispatchWindowEvent("live:status", {
            connected: false,
            connecting: false,
        });
        scheduleLiveReconnect();
    };
}

function startLivePings(socket) {
    stopLivePings();
    livePingTimer = window.setInterval(() => {
        if (
            socket.readyState === WebSocket.OPEN &&
            livePingSentAt === null
        ) {
            livePingSentAt = performance.now();
            socket.send(
                JSON.stringify({
                    type: "ping",
                    client_sent_at_ms: livePingSentAt,
                }),
            );
        }
    }, 1000);
}

function stopLivePings() {
    livePingSentAt = null;
    if (livePingTimer) {
        window.clearInterval(livePingTimer);
        livePingTimer = null;
    }
}

export function startLiveUpdates() {
    liveConnectionDesired = true;
    const worker = ensureLiveWorker();
    if (worker) {
        worker.postMessage({ type: "start", url: buildLiveURL() });
        return;
    }
    openLiveSocket();
}

export function stopLiveUpdates() {
    liveConnectionDesired = false;
    liveReconnectAttempt = 0;
    if (liveWorker) {
        liveWorker.terminate();
        liveWorker = null;
    }
    stopLivePings();
    if (liveReconnectTimer) {
        window.clearTimeout(liveReconnectTimer);
        liveReconnectTimer = null;
    }
    if (liveSocket) {
        const socket = liveSocket;
        liveSocket = null;
        socket.onopen = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        if (
            socket.readyState === WebSocket.OPEN ||
            socket.readyState === WebSocket.CONNECTING
        ) {
            socket.close();
        }
    }
    dispatchWindowEvent("live:status", { connected: false, connecting: false });
}

export async function login(username, password) {
    return request("/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
    });
}

export async function fetchSession() {
    return request("/session");
}

export async function logout() {
    return request("/logout", {
        method: "POST",
    });
}

export async function fetchNotes(token) {
    return request("/notes", { headers: authHeaders(token) });
}

export async function fetchNote(token, id) {
    return request(`/notes/${id}`, { headers: authHeaders(token) });
}

export async function createNote(
    token,
    title,
    body,
    parentId,
    type,
    customData,
    tags,
) {
    return request("/notes", {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify({
            title,
            body,
            parent_id: parentId ?? null,
            type: type || "standard",
            custom_data: customData || null,
            tags: tags || [],
        }),
    });
}

export async function updateNote(
    token,
    id,
    title,
    body,
    parentId,
    type,
    customData,
    tags,
) {
    return request(`/notes/${id}`, {
        method: "PUT",
        headers: authHeaders(token),
        body: JSON.stringify({
            title,
            body,
            parent_id: parentId ?? null,
            type: type || "standard",
            custom_data: customData || null,
            tags: tags || [],
        }),
    });
}

export async function deleteNote(token, id) {
    return request(`/notes/${id}`, {
        method: "DELETE",
        headers: authHeaders(token),
    });
}

export async function fetchNoteHistory(token, id) {
    return request(`/notes/${id}/history`, { headers: authHeaders(token) });
}

export async function fetchChildren(token, id) {
    return request(`/notes/${id}/children`, { headers: authHeaders(token) });
}

export async function fetchAncestors(token, id) {
    return request(`/notes/${id}/ancestors`, { headers: authHeaders(token) });
}

function buildSearchParams(query, options = {}) {
    const params = new URLSearchParams({ q: query });
    const types = Array.isArray(options.types)
        ? [
              ...new Set(
                  options.types.map((t) => String(t).trim()).filter(Boolean),
              ),
          ]
        : null;
    if (types && types.length > 0) {
        params.set("types", types.join(","));
    }
    if (options.stream) {
        params.set("stream", "1");
    }
    if (options.tagOnly) {
        params.set("tag_only", "1");
    }
    return params;
}

export async function searchNotes(token, query, options = {}) {
    const params = buildSearchParams(query, options);
    return request(`/notes/search?${params.toString()}`, {
        headers: authHeaders(token),
    });
}

export async function streamSearchNotes(token, query, options = {}) {
    const params = buildSearchParams(query, { ...options, stream: true });
    const res = await fetch(`/notes/search?${params.toString()}`, {
        credentials: "include",
        headers: {
            Accept: "application/x-ndjson",
        },
        signal: options.signal,
    });
    if (res.status === 401) {
        window.dispatchEvent(new CustomEvent("auth:unauthorized"));
        throw new Error("unauthorized");
    }
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `HTTP ${res.status}`);
    }
    if (!res.body) {
        options.onDone?.({ type: "done", total: 0 });
        return;
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    const dispatchEvent = (event) => {
        options.onEvent?.(event);
        if (event.type === "status") {
            options.onStatus?.(event);
        } else if (event.type === "section") {
            options.onSection?.(event.section, event);
        } else if (event.type === "error") {
            options.onError?.(event);
        } else if (event.type === "done") {
            options.onDone?.(event);
        }
    };

    const processLine = (line) => {
        const trimmed = line.trim();
        if (!trimmed) return;
        dispatchEvent(JSON.parse(trimmed));
    };

    while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let newlineIndex = buffer.indexOf("\n");
        while (newlineIndex >= 0) {
            processLine(buffer.slice(0, newlineIndex));
            buffer = buffer.slice(newlineIndex + 1);
            newlineIndex = buffer.indexOf("\n");
        }
    }

    buffer += decoder.decode();
    if (buffer.trim()) {
        processLine(buffer);
    }
}

export async function setNotePin(token, id, pinned) {
    return request(`/notes/${id}/pin`, {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify({ pinned }),
    });
}

export async function fetchTags(token, query) {
    const q = query ? `?q=${encodeURIComponent(query)}` : "";
    return request(`/tags${q}`, { headers: authHeaders(token) });
}

export async function generateAutoTags(token, noteId) {
    return request(`/notes/${noteId}/auto-tags`, {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function pluginAction(token, noteId, action, params) {
    return request(`/notes/${noteId}/action`, {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify({ action, params: params || null }),
    });
}

export async function pluginActionV2(token, noteId, actionID, params) {
    return request(`/notes/${noteId}/actions/${actionID}`, {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify({ params: params || null }),
    });
}

export async function fetchNoteTypes(token) {
    return request("/note-types", { headers: authHeaders(token) });
}

// --- File Attachments API ---

export async function uploadAttachment(token, noteId, file) {
    const formData = new FormData();
    formData.append("file", file);
    return request(`/notes/${noteId}/files`, {
        method: "POST",
        headers: authOnlyHeaders(token),
        body: formData,
    });
}

export async function uploadInlineFile(token, noteId, file) {
    const formData = new FormData();
    formData.append("file", file);
    return request(`/notes/${noteId}/files/inline`, {
        method: "POST",
        headers: authOnlyHeaders(token),
        body: formData,
    });
}

export async function deleteAttachment(token, noteId, fileId) {
    return request(`/notes/${noteId}/files/${fileId}`, {
        method: "DELETE",
        headers: authHeaders(token),
    });
}

// --- STT / Transcription API ---

export async function fetchSTTResult(token, fileId) {
    return request(`/files/${fileId}/stt`, {
        headers: authHeaders(token),
    });
}

// --- WebAuthn / Passkey API ---

function toBase64Url(buffer) {
    return btoa(String.fromCharCode(...new Uint8Array(buffer)))
        .replace(/=/g, "")
        .replace(/\+/g, "-")
        .replace(/\//g, "_");
}

function fromBase64Url(base64url) {
    base64url = base64url.replace(/-/g, "+").replace(/_/g, "/");
    while (base64url.length % 4) base64url += "=";
    return Uint8Array.from(atob(base64url), (c) => c.charCodeAt(0));
}

function prepareCreationOptions(publicKey) {
    return {
        publicKey: {
            ...publicKey,
            challenge: fromBase64Url(publicKey.challenge),
            user: {
                ...publicKey.user,
                id: fromBase64Url(publicKey.user.id),
            },
            excludeCredentials: publicKey.excludeCredentials?.map((c) => ({
                ...c,
                id: fromBase64Url(c.id),
            })),
        },
    };
}

function prepareRequestOptions(publicKey) {
    return {
        publicKey: {
            ...publicKey,
            challenge: fromBase64Url(publicKey.challenge),
            allowCredentials: publicKey.allowCredentials?.map((c) => ({
                ...c,
                id: fromBase64Url(c.id),
            })),
        },
    };
}

function encodeAttestationResponse(response) {
    return {
        id: response.id,
        rawId: toBase64Url(response.rawId),
        type: response.type,
        response: {
            clientDataJSON: toBase64Url(response.response.clientDataJSON),
            attestationObject: toBase64Url(response.response.attestationObject),
            transports: response.response.getTransports?.() || [],
        },
    };
}

function encodeAssertionResponse(response) {
    return {
        id: response.id,
        rawId: toBase64Url(response.rawId),
        type: response.type,
        response: {
            clientDataJSON: toBase64Url(response.response.clientDataJSON),
            authenticatorData: toBase64Url(response.response.authenticatorData),
            signature: toBase64Url(response.response.signature),
            userHandle: response.response.userHandle
                ? toBase64Url(response.response.userHandle)
                : null,
        },
    };
}

/**
 * Begin passkey registration. Requires an existing session cookie.
 */
export async function beginPasskeyRegistration(token) {
    const pubKeyOpts = await request("/webauthn/register/begin", {
        headers: authHeaders(token),
    });

    const prepared = prepareCreationOptions(pubKeyOpts);
    const credential = await navigator.credentials.create(prepared);

    const body = encodeAttestationResponse(credential);
    return request("/webauthn/register/finish", {
        method: "POST",
        headers: { ...authHeaders(token), "Content-Type": "application/json" },
        body: JSON.stringify(body),
    });
}

// --- Job Queue API ---

export async function fetchJobs(token) {
    return request("/jobs", { headers: authHeaders(token) });
}

export async function retryJob(token, runId) {
    return request(`/jobs/${runId}/retry`, {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function cancelJob(token, runId) {
    return request(`/jobs/${runId}/cancel`, {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function triggerBackup(token) {
    return request("/backup/trigger", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function reindexNotes(token) {
    return request("/maintenance/reindex", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function reindexOCR(token) {
    return request("/maintenance/reindex-ocr", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function reindexSTT(token) {
    return request("/maintenance/reindex-stt", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function refreshAllAutoTags(token) {
    return request("/maintenance/refresh-auto-tags", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function recalculateRecipeCategories(token) {
    return request("/maintenance/recalculate-recipe-categories", {
        method: "POST",
        headers: authHeaders(token),
    });
}

export async function deleteUnknownS3Files(token) {
    return request("/maintenance/delete-unknown-s3-files", {
        method: "POST",
        headers: authHeaders(token),
    });
}

/**
 * Begin passkey login (discoverable / usernameless).
 */
// --- System Status API ---

export async function fetchPrinterStatus(token) {
    return request("/system/printer-status", { headers: authHeaders(token) });
}

export async function fetchAIStatus(token) {
    return request("/system/ai-status", { headers: authHeaders(token) });
}

export async function fetchServerStats(token) {
    return request("/system/stats", { headers: authHeaders(token) });
}

export async function beginPasskeyLogin() {
    const pubKeyOpts = await request("/webauthn/login/begin");

    const prepared = prepareRequestOptions(pubKeyOpts);
    const credential = await navigator.credentials.get(prepared);

    const body = encodeAssertionResponse(credential);
    return request("/webauthn/login/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
    });
}
