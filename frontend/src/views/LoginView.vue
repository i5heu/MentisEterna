<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="brand">
        <span class="brand-name">MentisEterna</span>
      </div>

      <!-- Passkey login: primary, always visible -->
      <button
        class="btn-passkey btn-passkey-primary"
        :disabled="passkeyLoading"
        @click="loginWithPasskey"
      >
        <span class="passkey-icon">&#128273;</span>
        {{ passkeyLoading ? 'Authenticating…' : 'Sign in with Passkey' }}
      </button>

      <p v-if="passkeyError" class="error">{{ passkeyError }}</p>

      <div class="divider">
        <span>or with password</span>
      </div>

      <!-- Password login: secondary, collapsible -->
      <details class="password-section" :open="mode === 'registerPasskey'">
        <summary class="password-toggle">
          Sign in with password
        </summary>
        <form @submit.prevent="submitWithPassword">
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
      </details>

      <!-- Post-password-login: register passkey prompt -->
      <div v-if="mode === 'registerPasskey'" class="register-passkey">
        <div class="divider">
          <span>stay passwordless</span>
        </div>
        <p class="register-hint">
          Register a passkey now to skip your password next time.
        </p>
        <button
          class="btn-passkey btn-passkey-register"
          :disabled="passkeyLoading"
          @click="registerPasskey"
        >
          {{ passkeyLoading ? 'Registering…' : 'Register this Passkey' }}
        </button>
        <button class="btn-ghost skip-btn" @click="skipToApp">
          Skip for now
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { login, beginPasskeyLogin, beginPasskeyRegistration } from '../api.js'

const emit = defineEmits(['logged-in'])

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

const passkeyLoading = ref(false)
const passkeyError = ref('')
const mode = ref(null) // null | 'registerPasskey'

async function submitWithPassword() {
  error.value = ''
  loading.value = true
  try {
    const data = await login(username.value, password.value)
    localStorage.setItem('me_token', data.token)
    mode.value = 'registerPasskey'
    passkeyError.value = ''
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function skipToApp() {
  emit('logged-in', localStorage.getItem('me_token') || '')
}

async function loginWithPasskey() {
  passkeyError.value = ''
  passkeyLoading.value = true
  try {
    const data = await beginPasskeyLogin()
    emit('logged-in', data.token)
  } catch (e) {
    if (e.name === 'NotAllowedError' || e.message?.includes('NotAllowed')) {
      passkeyError.value = 'Passkey cancelled or not available. Use password below.'
    } else {
      passkeyError.value = e.message || 'Passkey login failed. Use password below.'
    }
  } finally {
    passkeyLoading.value = false
  }
}

async function registerPasskey() {
  passkeyError.value = ''
  passkeyLoading.value = true
  try {
    const token = localStorage.getItem('me_token')
    await beginPasskeyRegistration(token)
    emit('logged-in', token)
  } catch (e) {
    if (e.name === 'NotAllowedError' || e.message?.includes('NotAllowed')) {
      passkeyError.value = 'Passkey registration cancelled.'
    } else {
      passkeyError.value = e.message || 'Passkey registration failed'
    }
  } finally {
    passkeyLoading.value = false
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
  background: var(--panel-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  padding: 2.5rem 2rem;
  width: 100%;
  max-width: 400px;
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

/* --- Passkey primary button --- */

.btn-passkey {
  width: 100%;
  padding: 0.7rem 1rem;
  font-size: 1rem;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  transition: background 0.15s, border-color 0.15s, transform 0.1s;
}
.btn-passkey:active:not(:disabled) { transform: scale(0.98); }

.btn-passkey-primary {
  background: var(--accent-teal);
  color: #fff;
  border: 1px solid var(--accent-teal);
  font-weight: 600;
}
.btn-passkey-primary:hover:not(:disabled) {
  background: var(--accent-teal-dim);
  border-color: var(--accent-teal-dim);
}

.btn-passkey-register {
  background: var(--category-bg-color);
  border: 1px solid var(--accent-teal);
  color: var(--accent-teal);
  font-weight: 600;
}
.btn-passkey-register:hover:not(:disabled) {
  background: var(--accent-teal);
  color: #fff;
}

.passkey-icon { font-size: 1.3rem; }

/* --- Divider --- */

.divider {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin: 1.25rem 0;
  color: var(--font-color-secondary);
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}
.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border-color);
}

/* --- Password section --- */

.password-section {
  border: 1px solid var(--border-color);
  border-radius: 8px;
  overflow: hidden;
}
.password-toggle {
  display: block;
  padding: 0.6rem 1rem;
  font-size: 0.85rem;
  color: var(--font-color-secondary);
  cursor: pointer;
  user-select: none;
  list-style: none;
}
.password-toggle::-webkit-details-marker { display: none; }
.password-toggle:hover { color: var(--font-color); }
.password-section[open] .password-toggle {
  border-bottom: 1px solid var(--border-color);
  color: var(--font-color);
}

.password-section form {
  padding: 1rem;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  margin-bottom: 1rem;
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
  padding: 0.6rem;
  font-size: 0.95rem;
}

/* --- Register passkey post-login --- */

.register-passkey {
  margin-top: 0.5rem;
}
.register-hint {
  font-size: 0.85rem;
  color: var(--font-color-secondary);
  margin-bottom: 0.75rem;
  line-height: 1.5;
  text-align: center;
}
.skip-btn {
  width: 100%;
  margin-top: 0.6rem;
  padding: 0.5rem;
  font-size: 0.85rem;
}
</style>
