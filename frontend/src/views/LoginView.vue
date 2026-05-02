<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand">
        <span class="brand-name">MentisEterna</span>
      </div>
      <form @submit.prevent="submit">
        <div class="field">
          <label>Username</label>
          <input v-model="username" type="text" placeholder="admin" autocomplete="username" required />
        </div>
        <div class="field">
          <label>Password</label>
          <input v-model="password" type="password" placeholder="••••••••" autocomplete="current-password" required />
        </div>
        <p v-if="error" class="error">{{ error }}</p>
        <button type="submit" class="btn-amber" :disabled="loading">
          {{ loading ? 'Signing in…' : 'Sign in' }}
        </button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { login } from '../api.js'

const emit = defineEmits(['logged-in'])

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  try {
    const data = await login(username.value, password.value)
    emit('logged-in', data.token)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}

.login-card {
  background: var(--surface);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  padding: 2.5rem 2rem;
  width: 100%;
  max-width: 360px;
}

.brand {
  text-align: center;
  margin-bottom: 2rem;
}

.brand-name {
  font-size: 1.6rem;
  font-weight: 700;
  color: var(--header-title-color);
  letter-spacing: 0.03em;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  margin-bottom: 1.1rem;
}

.field label {
  font-size: 0.8rem;
  color: var(--font-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.field input { width: 100%; }

.error {
  color: var(--heading-color);
  font-size: 0.85rem;
  margin-bottom: 0.75rem;
}

button[type="submit"] {
  width: 100%;
  padding: 0.65rem;
  font-size: 1rem;
  margin-top: 0.25rem;
}
</style>
