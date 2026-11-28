<template>
    <div class="modal-overlay" @click.self="close">
        <div class="archive-chat-modal">
            <div class="modal-header">
                <h2>
                    ⚡ Archive Chat
                    <span class="chat-header-sub">answers from your notes</span>
                </h2>
                <button class="btn-ghost modal-close-btn" @click="close">✕</button>
            </div>
            <div ref="feedEl" class="chat-body">
                <div v-if="!turns.length" class="chat-empty">
                    Ask a question — the archive chat searches your notes
                    semantically and answers from what it finds, with sources.
                </div>
                <div v-for="(turn, i) in turns" :key="i" class="chat-turn">
                    <div v-if="turn.question" class="turn-question">
                        <span class="turn-author">you</span>
                        <span class="turn-text">{{ turn.question }}</span>
                    </div>
                    <div v-if="turn.pending" class="turn-status">
                        <span class="status-dot" :class="turn.phase"></span>
                        {{ turn.statusText }}
                    </div>
                    <div v-if="turn.error" class="turn-error">
                        {{ turn.error }}
                    </div>
                    <template v-if="turn.sources && turn.sources.length">
                        <div class="turn-sources-label">
                            sources
                            <span
                                v-if="turn.privateExcluded"
                                class="turn-private-note"
                                :title="
                                    'Highly private notes are never sent to a remote provider. Pin the chat usage to the local LLM to include them.'
                                "
                            >
                                🔒 {{ turn.privateExcluded }} private
                                {{ turn.privateExcluded === 1 ? "note" : "notes" }}
                                excluded
                            </span>
                            <span
                                v-if="turn.routingLabel"
                                class="turn-routing"
                                >{{ turn.routingLabel }}</span
                            >
                        </div>
                        <div class="turn-sources">
                            <button
                                v-for="src in turn.sources"
                                :key="src.id"
                                class="source-chip"
                                :title="src.path || src.title"
                                @click="$emit('select-note', src.id)"
                            >
                                <span class="source-num">[{{ src.n }}]</span>
                                {{ src.title || "Untitled" }}
                                <span v-if="src.high_private" class="source-lock"
                                    >🔒</span
                                >
                            </button>
                        </div>
                    </template>
                    <div
                        v-if="turn.answer"
                        class="turn-answer"
                        v-html="renderAnswer(turn.answer)"
                    ></div>
                </div>
            </div>
            <div class="chat-composer">
                <input
                    ref="inputEl"
                    v-model="question"
                    type="text"
                    placeholder="Ask your archive…"
                    :disabled="busy"
                    @keydown.enter.prevent="ask"
                />
                <button
                    class="btn-amber ask-btn"
                    :disabled="busy || !question.trim()"
                    @click="ask"
                >
                    Ask
                </button>
            </div>
        </div>
    </div>
</template>

<script setup>
import { nextTick, ref } from "vue";
import { streamArchiveChat } from "../api";

const props = defineProps({
    token: { type: String, required: true },
});
const emit = defineEmits(["select-note", "close"]);

const question = ref("");
const turns = ref([]);
const busy = ref(false);
const feedEl = ref(null);
const inputEl = ref(null);

function close() {
    if (busy.value) return;
    emit("close");
}

function routingLabel(routing) {
    if (!routing) return "";
    if (routing.source === "note") {
        return `${routing.provider || "remote"} · ${routing.model}`;
    }
    if (routing.source === "local") {
        return `local · ${routing.model}`;
    }
    return `config · ${routing.model}`;
}

async function ask() {
    const q = question.value.trim();
    if (!q || busy.value) return;
    busy.value = true;
    question.value = "";

    const turn = {
        question: q,
        pending: true,
        phase: "retrieve",
        statusText: "Searching notes…",
        sources: [],
        routingLabel: "",
        privateExcluded: 0,
        answer: "",
        error: "",
    };
    turns.value.push(turn);
    await scrollFeed();

    try {
        await streamArchiveChat(props.token, q, {
            onStatus: (ev) => {
                turn.phase = ev.phase || "retrieve";
                turn.statusText = ev.message || "Working…";
                scrollFeed();
            },
            onSources: (ev) => {
                turn.pending = true;
                turn.phase = "answer";
                turn.statusText = "Answering…";
                turn.sources = (ev.sources || []).map((src, i) => ({
                    ...src,
                    n: i + 1,
                }));
                turn.routingLabel = routingLabel(ev.routing);
                turn.privateExcluded = ev.private_excluded || 0;
                scrollFeed();
            },
            onAnswer: (ev) => {
                turn.pending = false;
                turn.answer = ev.answer || "";
                scrollFeed();
            },
            onError: (ev) => {
                turn.pending = false;
                turn.error = ev.message || "The archive chat failed.";
            },
            onDone: () => {
                turn.pending = false;
            },
        });
    } catch (err) {
        turn.pending = false;
        turn.error = err.message || "The archive chat failed.";
    } finally {
        busy.value = false;
        nextTick(() => inputEl.value?.focus());
        scrollFeed();
    }
}

async function scrollFeed() {
    await nextTick();
    if (feedEl.value) {
        feedEl.value.scrollTop = feedEl.value.scrollHeight;
    }
}

// Minimal inline-[N] citation + markdown-lite rendering for answers.
function renderAnswer(text) {
    const escaped = String(text)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
    return escaped
        .replace(/`([^`]+)`/g, '<code class="ans-code">$1</code>')
        .replace(/\*\*([^*]+)\*\*/g, '<strong class="ans-strong">$1</strong>')
        .replace(/\[(\d+)\]/g, '<span class="ans-cite">[$1]</span>')
        .replace(/\n/g, "<br />");
}

defineExpose({ focus: () => inputEl.value?.focus() });
</script>

<style scoped>
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 2050;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
}

.archive-chat-modal {
    background: var(--panel-bg, #041019);
    border: 1px solid var(--border-color, #143c46);
    border-radius: 4px;
    width: 100%;
    max-width: 680px;
    max-height: min(82vh, 760px);
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 30px var(--shadow-color, rgba(0, 0, 0, 0.6));
}

.modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-color, #143c46);
}

.modal-header h2 {
    font-size: 1rem;
    margin: 0;
    display: flex;
    align-items: baseline;
    gap: 8px;
}

.chat-header-sub {
    font-size: 0.72rem;
    color: var(--font-color-secondary, #7aa695);
    text-transform: uppercase;
    letter-spacing: 0.08em;
}

.modal-close-btn {
    font-size: 1.1rem;
    padding: 4px 8px;
}

.chat-body {
    padding: 16px 18px;
    overflow-y: auto;
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 18px;
}

.chat-empty {
    color: var(--font-color-secondary, #7aa695);
    font-size: 0.85rem;
    line-height: 1.5;
    padding: 12px 0;
}

.chat-turn {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.turn-question {
    display: flex;
    gap: 10px;
    align-items: baseline;
}

.turn-author {
    color: var(--accent-teal, #3fe0cf);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
}

.turn-text {
    color: var(--font-color, #cbe8dc);
    font-size: 0.95rem;
}

.turn-status {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--font-color-secondary, #7aa695);
    font-size: 0.8rem;
}

.status-dot {
    width: 6px;
    height: 6px;
    border-radius: 2px;
    background: var(--accent-teal, #3fe0cf);
    animation: pulse 1.2s ease-in-out infinite;
}

.status-dot.answer {
    background: var(--accent-amber, #ffb454);
}

@keyframes pulse {
    0%,
    100% {
        opacity: 0.3;
    }
    50% {
        opacity: 1;
    }
}

.turn-error {
    color: #e28b7d;
    font-size: 0.85rem;
    border: 1px solid rgba(217, 83, 79, 0.4);
    border-radius: 3px;
    padding: 8px 10px;
    background: rgba(217, 83, 79, 0.12);
}

.turn-sources-label {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--font-color-secondary, #7aa695);
}

.turn-private-note {
    color: var(--accent-amber, #ffb454);
    cursor: help;
}

.turn-routing {
    margin-left: auto;
    color: var(--font-color-secondary, #7aa695);
}

.turn-sources {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
}

.source-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: var(--raised-bg, #082029);
    border: 1px solid var(--border-color, #143c46);
    border-radius: 2px;
    color: var(--font-color, #cbe8dc);
    font-family: var(--font-mono, monospace);
    font-size: 0.78rem;
    padding: 4px 8px;
    cursor: pointer;
    max-width: 240px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.source-chip:hover {
    border-color: var(--accent-teal, #3fe0cf);
    color: var(--accent-teal, #3fe0cf);
}

.source-num {
    color: var(--accent-teal, #3fe0cf);
}

.source-lock {
    font-size: 0.7rem;
}

.turn-answer {
    color: var(--font-color, #cbe8dc);
    font-size: 0.88rem;
    line-height: 1.55;
    border-left: 2px solid var(--border-color, #143c46);
    padding-left: 12px;
    white-space: normal;
    word-break: break-word;
}

.ans-cite {
    color: var(--accent-teal, #3fe0cf);
}

.ans-code {
    background: var(--raised-bg, #082029);
    border: 1px solid var(--border-color, #143c46);
    border-radius: 2px;
    padding: 0 4px;
    font-size: 0.82rem;
}

.ans-strong {
    color: var(--accent-teal, #3fe0cf);
}

.chat-composer {
    display: flex;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border-color, #143c46);
}

.chat-composer input {
    flex: 1;
    background: var(--raised-bg, #082029);
    border: 1px solid var(--border-color, #143c46);
    border-radius: 3px;
    color: var(--font-color, #cbe8dc);
    font-family: var(--font-mono, monospace);
    font-size: 0.88rem;
    padding: 8px 10px;
    outline: none;
}

.chat-composer input:focus {
    border-color: var(--accent-teal, #3fe0cf);
}

.ask-btn {
    border-radius: 3px;
    padding: 8px 16px;
    font-size: 0.85rem;
}

.ask-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
}
</style>
