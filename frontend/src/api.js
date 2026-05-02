async function request(path, options = {}) {
  const res = await fetch(path, options)
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text.trim() || `HTTP ${res.status}`)
  }
  if (res.status === 204) return null
  return res.json()
}

function authHeaders(token) {
  return {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`,
  }
}

export async function login(username, password) {
  return request('/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
}

export async function fetchNotes(token) {
  return request('/notes', { headers: authHeaders(token) })
}

export async function createNote(token, title, body, parentId) {
  return request('/notes', {
    method: 'POST',
    headers: authHeaders(token),
    body: JSON.stringify({ title, body, parent_id: parentId ?? null }),
  })
}

export async function updateNote(token, id, title, body, parentId) {
  return request(`/notes/${id}`, {
    method: 'PUT',
    headers: authHeaders(token),
    body: JSON.stringify({ title, body, parent_id: parentId ?? null }),
  })
}

export async function deleteNote(token, id) {
  return request(`/notes/${id}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
}

export async function fetchNoteHistory(token, id) {
  return request(`/notes/${id}/history`, { headers: authHeaders(token) })
}

export async function fetchChildren(token, id) {
  return request(`/notes/${id}/children`, { headers: authHeaders(token) })
}

export async function searchNotes(token, query) {
  return request(`/notes/search?q=${encodeURIComponent(query)}`, { headers: authHeaders(token) })
}
