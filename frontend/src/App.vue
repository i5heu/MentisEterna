<template>
  <LoginView v-if="!token" @logged-in="onLogin" />
  <NotesView v-else :token="token" @logout="onLogout" />
</template>

<script setup>
import { ref } from 'vue'
import LoginView from './views/LoginView.vue'
import NotesView from './views/NotesView.vue'

const token = ref(localStorage.getItem('me_token') || '')

function onLogin(t) {
  token.value = t
  localStorage.setItem('me_token', t)
}

function onLogout() {
  token.value = ''
  localStorage.removeItem('me_token')
}
</script>

<style>
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

:root {
  --navy:   #01101f;
  --surface: #071828;
  --surface2: #0d2438;
  --border:  #1a3a52;
  --teal:   #6d9484;
  --teal-dim: #4e6e61;
  --amber:  #ffbf59;
  --crimson: #bf0604;
  --garnet: #960c05;
  --text:   #dce8e0;
  --text-muted: #7a9a8a;
  font-size: 15px;
}

body {
  background: var(--navy);
  color: var(--text);
  font-family: 'Segoe UI', system-ui, sans-serif;
  min-height: 100vh;
}

button {
  cursor: pointer;
  border: none;
  border-radius: 6px;
  font-size: 0.9rem;
  font-family: inherit;
  padding: 0.45rem 1rem;
  transition: background 0.15s, opacity 0.15s;
}

button:disabled { opacity: 0.5; cursor: not-allowed; }

input, textarea {
  font-family: inherit;
  font-size: 0.95rem;
  background: var(--surface2);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 0.5rem 0.75rem;
  outline: none;
  transition: border-color 0.15s;
}

input:focus, textarea:focus {
  border-color: var(--teal);
}

textarea { resize: vertical; line-height: 1.6; }

.btn-primary {
  background: var(--teal);
  color: #fff;
}
.btn-primary:hover:not(:disabled) { background: var(--teal-dim); }

.btn-amber {
  background: var(--amber);
  color: var(--navy);
  font-weight: 600;
}
.btn-amber:hover:not(:disabled) { background: #e8ac47; }

.btn-danger {
  background: var(--crimson);
  color: #fff;
}
.btn-danger:hover:not(:disabled) { background: var(--garnet); }

.btn-ghost {
  background: transparent;
  color: var(--text-muted);
  border: 1px solid var(--border);
}
.btn-ghost:hover:not(:disabled) { background: var(--surface2); color: var(--text); }
</style>
