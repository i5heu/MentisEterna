<template>
    <div class="login-wrap">
        <div class="login-card">
            <div class="brand">
                <span
                    class="brand-name shortcut-anchor"
                    :title="getShortcutLabel('show-shortcuts')"
                >
                    MentisEterna
                    <ShortcutHint
                        v-if="shortcutHintsVisible"
                        :label="getHintLabel('show-shortcuts')"
                    />
                </span>
            </div>

            <!-- Passkey login: primary, always visible -->
            <button
                class="btn-passkey btn-passkey-primary shortcut-anchor"
                :title="getShortcutLabel('login-passkey')"
                :disabled="passkeyLoading"
                @click="loginWithPasskey"
            >
                <span class="passkey-icon">&#128273;</span>
                {{
                    passkeyLoading ? "Authenticating…" : "Sign in with Passkey"
                }}
                <ShortcutHint
                    v-if="
                        shortcutHintsVisible &&
                        isShortcutEnabled('login-passkey')
                    "
                    :label="getHintLabel('login-passkey')"
                />
            </button>

            <p v-if="passkeyError" class="error">{{ passkeyError }}</p>

            <div class="divider">
                <span>or with password</span>
            </div>

            <!-- Password login: secondary, collapsible -->
            <details
                ref="passwordDetails"
                class="password-section"
                :open="mode === 'registerPasskey'"
            >
                <summary
                    class="password-toggle shortcut-anchor"
                    :title="getShortcutLabel('toggle-password-login')"
                >
                    Sign in with password
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('toggle-password-login')
                        "
                        :label="getHintLabel('toggle-password-login')"
                    />
                </summary>
                <form ref="passwordForm" @submit.prevent="submitWithPassword">
                    <div class="field shortcut-anchor">
                        <label>Username</label>
                        <input
                            ref="usernameInput"
                            v-model="username"
                            type="text"
                            placeholder="admin"
                            autocomplete="username"
                            :title="getShortcutLabel('focus-username')"
                            required
                        />
                        <ShortcutHint
                            v-if="shortcutHintsVisible"
                            :label="getHintLabel('focus-username')"
                        />
                    </div>
                    <div class="field shortcut-anchor">
                        <label>Password</label>
                        <input
                            ref="passwordInput"
                            v-model="password"
                            type="password"
                            placeholder="••••••••"
                            autocomplete="current-password"
                            :title="getShortcutLabel('focus-password')"
                            required
                        />
                        <ShortcutHint
                            v-if="shortcutHintsVisible"
                            :label="getHintLabel('focus-password')"
                        />
                    </div>
                    <p v-if="error" class="error">{{ error }}</p>
                    <button
                        type="submit"
                        class="btn-amber shortcut-anchor"
                        :title="getShortcutLabel('submit-password-login')"
                        :disabled="loading"
                    >
                        {{ loading ? "Signing in…" : "Sign in" }}
                        <ShortcutHint
                            v-if="
                                shortcutHintsVisible &&
                                isShortcutEnabled('submit-password-login')
                            "
                            :label="getHintLabel('submit-password-login')"
                        />
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
                    class="btn-passkey btn-passkey-register shortcut-anchor"
                    :title="getShortcutLabel('register-passkey')"
                    :disabled="passkeyLoading"
                    @click="registerPasskey"
                >
                    {{
                        passkeyLoading
                            ? "Registering…"
                            : "Register this Passkey"
                    }}
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('register-passkey')
                        "
                        :label="getHintLabel('register-passkey')"
                    />
                </button>
                <button
                    class="btn-ghost skip-btn shortcut-anchor"
                    :title="getShortcutLabel('skip-to-app')"
                    @click="skipToApp"
                >
                    Skip for now
                    <ShortcutHint
                        v-if="
                            shortcutHintsVisible &&
                            isShortcutEnabled('skip-to-app')
                        "
                        :label="getHintLabel('skip-to-app')"
                    />
                </button>
            </div>
            <KeyboardShortcutsHelpModal
                v-model="showHotkeys"
                :items="hotkeys"
            />
        </div>
    </div>
</template>

<script setup>
import { computed, ref } from "vue";
import { login, beginPasskeyLogin, beginPasskeyRegistration } from "../api.js";
import ShortcutHint from "../components/ShortcutHint.vue";
import KeyboardShortcutsHelpModal from "../components/KeyboardShortcutsHelpModal.vue";
import { useKeyboardShortcuts } from "../composables/useKeyboardShortcuts.js";

const emit = defineEmits(["logged-in"]);

const username = ref("");
const password = ref("");
const error = ref("");
const loading = ref(false);

const passkeyLoading = ref(false);
const passkeyError = ref("");
const mode = ref(null); // null | 'registerPasskey'
const passwordDetails = ref(null);
const passwordForm = ref(null);
const usernameInput = ref(null);
const passwordInput = ref(null);

function ensurePasswordSectionOpen() {
    if (mode.value === "registerPasskey") return;
    if (passwordDetails.value) {
        passwordDetails.value.open = true;
    }
}

function togglePasswordSection() {
    if (mode.value === "registerPasskey") return;
    if (passwordDetails.value) {
        passwordDetails.value.open = !passwordDetails.value.open;
    }
}

function focusUsernameField() {
    ensurePasswordSectionOpen();
    usernameInput.value?.focus();
}

function focusPasswordField() {
    ensurePasswordSectionOpen();
    passwordInput.value?.focus();
}

function submitPasswordForm() {
    ensurePasswordSectionOpen();
    passwordForm.value?.requestSubmit();
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
        id: "login-passkey",
        description: "Sign in with passkey",
        hintKey: "P",
        allowInInput: true,
        enabled: () => !passkeyLoading.value,
        handler: () => loginWithPasskey(),
    },
    {
        id: "toggle-password-login",
        description: "Open the password sign-in section",
        hintKey: "M",
        allowInInput: true,
        visible: () => mode.value !== "registerPasskey",
        handler: () => togglePasswordSection(),
    },
    {
        id: "focus-username",
        description: "Focus the username field",
        hintKey: "U",
        allowInInput: true,
        handler: () => focusUsernameField(),
    },
    {
        id: "focus-password",
        description: "Focus the password field",
        hintKey: "W",
        allowInInput: true,
        handler: () => focusPasswordField(),
    },
    {
        id: "submit-password-login",
        description: "Submit the password sign-in form",
        hintKey: "S",
        allowInInput: true,
        visible: () => mode.value !== "registerPasskey",
        enabled: () => !loading.value,
        handler: () => submitPasswordForm(),
    },
    {
        id: "register-passkey",
        description: "Register this passkey",
        hintKey: "R",
        allowInInput: true,
        visible: () => mode.value === "registerPasskey",
        enabled: () => !passkeyLoading.value,
        handler: () => registerPasskey(),
    },
    {
        id: "skip-to-app",
        description: "Skip passkey registration for now",
        hintKey: "N",
        allowInInput: true,
        visible: () => mode.value === "registerPasskey",
        handler: () => skipToApp(),
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

async function submitWithPassword() {
    error.value = "";
    loading.value = true;
    try {
        const data = await login(username.value, password.value);
        localStorage.setItem("me_token", data.token);
        mode.value = "registerPasskey";
        passkeyError.value = "";
    } catch (e) {
        error.value = e.message;
    } finally {
        loading.value = false;
    }
}

function skipToApp() {
    emit("logged-in", localStorage.getItem("me_token") || "");
}

async function loginWithPasskey() {
    passkeyError.value = "";
    passkeyLoading.value = true;
    try {
        const data = await beginPasskeyLogin();
        emit("logged-in", data.token);
    } catch (e) {
        if (e.name === "NotAllowedError" || e.message?.includes("NotAllowed")) {
            passkeyError.value =
                "Passkey cancelled or not available. Use password below.";
        } else {
            passkeyError.value =
                e.message || "Passkey login failed. Use password below.";
        }
    } finally {
        passkeyLoading.value = false;
    }
}

async function registerPasskey() {
    passkeyError.value = "";
    passkeyLoading.value = true;
    try {
        const token = localStorage.getItem("me_token");
        await beginPasskeyRegistration(token);
        emit("logged-in", token);
    } catch (e) {
        if (e.name === "NotAllowedError" || e.message?.includes("NotAllowed")) {
            passkeyError.value = "Passkey registration cancelled.";
        } else {
            passkeyError.value = e.message || "Passkey registration failed";
        }
    } finally {
        passkeyLoading.value = false;
    }
}
</script>

<style scoped>
.shortcut-anchor {
    position: relative;
}

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
    display: inline-block;
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
    transition:
        background 0.15s,
        border-color 0.15s,
        transform 0.1s;
}
.btn-passkey:active:not(:disabled) {
    transform: scale(0.98);
}

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

.passkey-icon {
    font-size: 1.3rem;
}

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
    content: "";
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
.password-toggle::-webkit-details-marker {
    display: none;
}
.password-toggle:hover {
    color: var(--font-color);
}
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
.field input {
    width: 100%;
}

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
