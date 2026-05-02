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

export async function createNote(token, title, body) {
  return request('/notes', {
    method: 'POST',
    headers: authHeaders(token),
    body: JSON.stringify({ title, body }),
  })
}

export async function updateNote(token, id, title, body) {
  return request(`/notes/${id}`, {
    method: 'PUT',
    headers: authHeaders(token),
    body: JSON.stringify({ title, body }),
  })
}

export async function deleteNote(token, id) {
  return request(`/notes/${id}`, {
    method: 'DELETE',
    headers: authHeaders(token),
  })
}
