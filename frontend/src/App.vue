<template>
    <LoginView v-if="!token" @logged-in="onLogin" />
    <OptionsView
        v-else-if="currentView === 'options'"
        :token="token"
        @logout="onLogout"
        @back="currentView = 'notes'"
    />
    <NotesView
        v-else
        :token="token"
        @logout="onLogout"
        @navigate-options="currentView = 'options'"
    />
</template>

<script setup>
import { ref } from "vue";
import LoginView from "./views/LoginView.vue";
import NotesView from "./views/NotesView.vue";
import OptionsView from "./views/OptionsView.vue";

const token = ref(localStorage.getItem("me_token") || "");
const currentView = ref("notes");

function onLogin(t) {
    token.value = t;
    localStorage.setItem("me_token", t);
    currentView.value = "notes";
}

function onLogout() {
    token.value = "";
    localStorage.removeItem("me_token");
    currentView.value = "notes";
}
</script>

<style>
*,
*::before,
*::after {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
}

:root {
    color-scheme: dark;

    /* Background Foundations */
    --html-bg: #01101f;
    --body-bg: #051b2e;

    /* Typography */
    --font-color: #e0e8e4;
    --font-color-secondary: #a5b0ad;
    --header-title-color: #ffbf59;
    --heading-color: #bf0604;
    --pre-text-color: #ffcf8a;
    --date-color: #6d9484;

    /* UI Elements & Accents */
    --border-color: #7e7567;
    --accent-teal: #6d9484;
    --category-bg-color: #162a22;
    --tag-bg-color: #ffbf59;
    --content-type-bg-color: #960c05;

    /* Effects */
    --shadow-color: rgba(0, 0, 0, 0.6);
    --showBorder-color: #01101f;

    /* Surface Layers */
    --panel-bg: #061320;
    --raised-bg: #0a1d2d;
    --accent-teal-dim: #4e6e61;

    font-size: 15px;
}

html {
    color-scheme: dark;
    background: var(--html-bg);
}

body {
    background: var(--body-bg);
    color: var(--font-color);
    font-family: "Segoe UI", system-ui, sans-serif;
    min-height: 100vh;
}

button {
    cursor: pointer;
    border: none;
    border-radius: 6px;
    font-size: 0.9rem;
    font-family: inherit;
    padding: 0.45rem 1rem;
    transition:
        background 0.15s,
        opacity 0.15s;
}

button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
}

input,
textarea {
    font-family: inherit;
    font-size: 0.95rem;
    background: var(--raised-bg);
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 0.5rem 0.75rem;
    outline: none;
    transition: border-color 0.15s;
}

input:focus,
textarea:focus {
    border-color: var(--accent-teal);
}

textarea {
    resize: vertical;
    line-height: 1.6;
}

.btn-primary {
    background: var(--accent-teal);
    color: #fff;
}
.btn-primary:hover:not(:disabled) {
    background: var(--accent-teal-dim);
}

.btn-amber {
    background: var(--tag-bg-color);
    color: var(--html-bg);
    font-weight: 600;
}
.btn-amber:hover:not(:disabled) {
    background: #e8ac47;
}

.btn-danger {
    background: var(--heading-color);
    color: #fff;
}
.btn-danger:hover:not(:disabled) {
    background: var(--content-type-bg-color);
}

.btn-ghost {
    background: transparent;
    color: var(--font-color-secondary);
    border: 1px solid var(--border-color);
}
.btn-ghost:hover:not(:disabled) {
    background: var(--raised-bg);
    color: var(--font-color);
}

.btn-sm {
    padding: 0.2rem 0.5rem;
    font-size: 0.85rem;
}
</style>
