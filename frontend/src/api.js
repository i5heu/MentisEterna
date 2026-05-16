async function request(path, options = {}) {
    const res = await fetch(path, options);
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text.trim() || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    return res.json();
}

function authHeaders(token) {
    return {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
    };
}

function authOnlyHeaders(token) {
    return { Authorization: `Bearer ${token}` };
}

export async function login(username, password) {
    return request("/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
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

export async function searchNotes(token, query) {
    return request(`/notes/search?q=${encodeURIComponent(query)}`, {
        headers: authHeaders(token),
    });
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
 * Begin passkey registration. Requires an existing session (Bearer token).
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
        credentials: "include",
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

/**
 * Begin passkey login (discoverable / usernameless).
 */
export async function beginPasskeyLogin() {
    const pubKeyOpts = await request("/webauthn/login/begin");

    const prepared = prepareRequestOptions(pubKeyOpts);
    const credential = await navigator.credentials.get(prepared);

    const body = encodeAssertionResponse(credential);
    return request("/webauthn/login/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        credentials: "include",
    });
}
