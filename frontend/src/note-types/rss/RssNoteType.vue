<template>
    <div class="rss-reader">
        <!-- ==================== Viewer ==================== -->
        <template v-if="!editing">
            <div class="rss-toolbar">
                <div class="rss-toolbar-group">
                    <button
                        class="btn-ghost btn-sm"
                        :disabled="refreshing"
                        @click="refreshNow"
                    >
                        {{ refreshing ? "Refreshing…" : "Refresh feeds" }}
                    </button>
                    <button
                        class="btn-ghost btn-sm"
                        :disabled="busy"
                        @click="reapplyRules"
                    >
                        Reapply rules
                    </button>
                </div>
                <div v-if="view" class="rss-toolbar-group rss-filters">
                    <select v-model="stateFilter" class="rss-select">
                        <option value="all">All states</option>
                        <option value="new">Unread</option>
                        <option value="read">Read</option>
                        <option value="starred">Starred</option>
                    </select>
                    <select
                        v-if="view.feeds && view.feeds.length > 1"
                        v-model="feedFilter"
                        class="rss-select"
                    >
                        <option value="0">All feeds</option>
                        <option v-for="f in view.feeds" :key="f.id" :value="f.id">
                            {{ f.name || f.url }}
                        </option>
                    </select>
                    <button
                        class="btn-ghost btn-sm"
                        @click="showRejected = !showRejected"
                    >
                        {{
                            showRejected
                                ? "Hide rejected"
                                : `Rejected (${view.counts?.rejected ?? 0})`
                        }}
                    </button>
                </div>
            </div>

            <p
                v-if="actionError"
                class="rss-error"
            >
                {{ actionError }}
            </p>
            <p
                v-if="actionInfo"
                class="rss-info"
            >
                {{ actionInfo }}
            </p>

            <div v-if="view" class="rss-meta">
                <span
                    v-if="view.rules_warning"
                    class="rss-chip rss-chip-warn"
                    :title="view.rules_warning"
                >
                    ⚠ rules body has unparsable lines
                </span>
                <span
                    v-if="view.prompt"
                    class="rss-chip"
                    :title="
                        view.prompt.default
                            ? 'No RSS Prompt note in scope — add a child note of type RSS Prompt to customize classification. Using the built-in default prompt.'
                            : 'The classification prompt is defined by the RSS Prompt note in scope.'
                    "
                >
                    {{
                        view.prompt.default
                            ? "🧠 default prompt"
                            : `🧠 prompt: ${view.prompt.title}`
                    }}
                </span>
                <span v-else class="rss-chip rss-chip-warn">
                    ⚠ no prompt in scope
                </span>
                <span
                    v-if="view.counts"
                    class="rss-chip"
                    :title="`${view.counts.total} fetched · ${view.counts.pending_classify} awaiting classification`"
                >
                    {{ view.counts.total }} articles ·
                    {{ view.counts.pending_classify }} classifying
                </span>
            </div>

            <div v-if="view && view.feeds.length" class="rss-feeds">
                <span
                    v-for="f in view.feeds"
                    :key="f.id"
                    class="rss-chip"
                    :class="feedChipClass(f)"
                    :title="feedChipTitle(f)"
                >
                    {{
                        (f.enabled ? "" : "⏸ ") +
                        (f.name || f.url) +
                        (f.new_count ? ` (${f.new_count})` : "")
                    }}
                </span>
            </div>

            <div v-if="!view || !view.items.length" class="rss-empty">
                No articles{{ refreshing ? " yet — fetching…" : "" }}. Hit
                “Refresh feeds”, then check back. Feeds are managed in edit
                mode.
            </div>

            <div v-else class="rss-items">
                <RssItemCard
                    v-for="item in filteredItems"
                    :key="item.id"
                    :item="item"
                    :busy="busy"
                    @mark="set_item_state(item, $event)"
                    @reclassify="reclassify(item)"
                    @save="saveAsNote(item)"
                />
            </div>

            <details v-if="showRejected && view && view.rejected.length" class="rss-rejected">
                <summary>
                    Rejected articles ({{ view.counts?.rejected ?? 0 }}) —
                    hidden by ban words / penalty threshold
                </summary>
                <div class="rss-items">
                    <RssItemCard
                        v-for="item in view.rejected"
                        :key="item.id"
                        :item="item"
                        :busy="busy"
                        dimmed
                        @mark="set_item_state(item, $event)"
                        @reclassify="reclassify(item)"
                        @save="saveAsNote(item)"
                    />
                </div>
            </details>
        </template>

        <!-- ==================== Editor ==================== -->
        <template v-else>
            <div class="rss-editor">
                <div class="rss-editor-section">
                    <h4>Feeds ({{ localConfig.feeds.length }})</h4>
                    <p class="rss-hint">
                        One RSS/Atom URL per feed. The feed's display name is
                        taken from your entry (or the feed's own title when
                        left empty).
                    </p>
                    <div
                        v-for="(feed, idx) in localConfig.feeds"
                        :key="idx"
                        class="rss-feed-row"
                    >
                        <input
                            v-model="feed.url"
                            class="rss-input rss-input-url"
                            type="url"
                            placeholder="https://example.com/feed.xml"
                        />
                        <input
                            v-model="feed.name"
                            class="rss-input rss-input-name"
                            type="text"
                            placeholder="Name (optional)"
                        />
                        <label class="rss-check">
                            <input v-model="feed.enabled" type="checkbox" />
                            enabled
                        </label>
                        <button
                            class="btn-ghost btn-sm"
                            @click="removeFeed(idx)"
                        >
                            ×
                        </button>
                    </div>
                    <button class="btn-ghost btn-sm" @click="addFeed">
                        + Add feed
                    </button>
                </div>

                <div class="rss-editor-section">
                    <h4>Rules (ban words &amp; penalties)</h4>
                    <p class="rss-hint">
                        Typed directly in this note's body — after saving the
                        body, press “Reapply rules” to re-score old articles
                        (new articles pick them up automatically). Format:
                    </p>
                    <pre class="rss-hint rss-hint-code">ban: crypto giveaway
press release: 6
webinar: 2
wonderful deep dive: -3
threshold: 8</pre>
                </div>

                <div class="rss-editor-section">
                    <h4>Settings</h4>
                    <div class="rss-settings-grid">
                        <label>
                            Refresh interval (minutes)
                            <input
                                v-model.number="localConfig.interval_minutes"
                                class="rss-input"
                                type="number"
                                min="15"
                                step="5"
                            />
                        </label>
                        <label>
                            Reject threshold (penalty points)
                            <input
                                v-model.number="localConfig.reject_threshold"
                                class="rss-input"
                                type="number"
                                min="0"
                                step="1"
                            />
                        </label>
                        <label>
                            Max items kept per feed
                            <input
                                v-model.number="localConfig.max_items_per_feed"
                                class="rss-input"
                                type="number"
                                min="10"
                                step="10"
                            />
                        </label>
                        <label>
                            Retention (days)
                            <input
                                v-model.number="localConfig.retention_days"
                                class="rss-input"
                                type="number"
                                min="7"
                                step="1"
                            />
                        </label>
                        <label class="rss-check">
                            <input
                                v-model="localConfig.auto_classify"
                                type="checkbox"
                            />
                            Classify new articles with the LLM
                        </label>
                        <label class="rss-check">
                            <input
                                v-model="localConfig.fetch_full_content"
                                type="checkbox"
                            />
                            Fetch full content when feeds give only snippets
                        </label>
                    </div>
                    <p class="rss-hint">
                        Classification prompt: place a note of type
                        <strong>RSS Prompt</strong> inside (or above) this
                        reader — its body is the prompt.
                    </p>
                </div>
            </div>
        </template>
    </div>
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { fetchNote, fetchNoteTypes } from "../../api.js";
import RssItemCard from "./RssItemCard.vue";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

// --- Viewer state ---
const refreshing = ref(false);
const busy = ref(false);
const actionError = ref("");
const actionInfo = ref("");
const stateFilter = ref("all");
const feedFilter = ref(0);
const showRejected = ref(false);
const view = ref(null);

// Hydrate the view from the uiSchema prop (the parent routes plugin.view
// into it) — re-sync whenever it changes after a note re-fetch.
watch(
    () => props.uiSchema,
    (v) => {
        if (v && (v.items !== undefined || v.feeds !== undefined)) {
            view.value = v;
        }
    },
    { immediate: true },
);

const filteredItems = computed(() => {
    if (!view.value || !Array.isArray(view.value.items)) return [];
    return view.value.items.filter((it) => {
        if (stateFilter.value !== "all" && it.state !== stateFilter.value) {
            return false;
        }
        if (feedFilter.value && it.feed_id !== feedFilter.value) {
            return false;
        }
        return true;
    });
});

// --- Editor state (config channel) ---
const defaultConfig = () => ({
    feeds: [],
    interval_minutes: 60,
    auto_classify: true,
    fetch_full_content: true,
    max_items_per_feed: 100,
    retention_days: 90,
    reject_threshold: 5,
});

const localConfig = ref(defaultConfig());
let hydrating = false;

function hydrateConfig() {
    hydrating = true;
    const raw = props.customData;
    localConfig.value = {
        feeds: Array.isArray(raw?.feeds)
            ? raw.feeds.map((f) => ({
                  url: f.url || "",
                  name: f.name || "",
                  enabled: f.enabled !== false,
              }))
            : [],
        interval_minutes: Number(raw?.interval_minutes) || 60,
        auto_classify: raw ? !!raw.auto_classify : true,
        fetch_full_content: raw ? !!raw.fetch_full_content : true,
        max_items_per_feed: Number(raw?.max_items_per_feed) || 100,
        retention_days: Number(raw?.retention_days) || 90,
        reject_threshold: Number(raw?.reject_threshold) || 5,
    };
    hydrating = false;
}

watch(() => props.note?.id, hydrateConfig, { immediate: true });

watch(
    localConfig,
    (val) => {
        if (hydrating) return;
        emit("update:customData", JSON.parse(JSON.stringify(val)));
    },
    { deep: true },
);

function addFeed() {
    localConfig.value.feeds.push({ url: "", name: "", enabled: true });
}

function removeFeed(idx) {
    localConfig.value.feeds.splice(idx, 1);
}

// --- Actions ---

async function runAction(actionID, params, { info = "" } = {}) {
    busy.value = true;
    actionError.value = "";
    actionInfo.value = info;
    try {
        const { pluginActionV2 } = await import("../../api.js");
        return await pluginActionV2(props.token, props.note.id, actionID, params);
    } catch (e) {
        actionError.value = e?.message || String(e);
        return null;
    } finally {
        busy.value = false;
    }
}

async function refreshView() {
    try {
        const fresh = await fetchNote(props.token, props.note.id);
        const v = fresh?.plugin?.view;
        if (v && (v.items !== undefined || v.feeds !== undefined)) {
            view.value = v;
        }
    } catch {
        /* transient fetch problems surface on the next interaction */
    }
}

async function refreshNow() {
    const res = await runAction("refresh", {}, { info: "Refresh scheduled…" });
    if (!res) return;
    refreshing.value = true;
    actionInfo.value = "Refreshing feeds…";
    // Poll until the scheduled job run has completed (bounded).
    let tries = 0;
    const timer = setInterval(async () => {
        tries++;
        await refreshView();
        if (tries >= 12) {
            clearInterval(timer);
            refreshing.value = false;
            actionInfo.value = "";
        }
    }, 2500);
}

async function reapplyRules() {
    const res = await runAction("reapply_rules", {});
    if (!res) return;
    actionInfo.value = `Rules reapplied: ${res.applied ?? 0} scored, ${res.rejected ?? 0} rejected (threshold ${res.threshold ?? "?"})`;
    await refreshView();
}

async function set_item_state(item, state) {
    const res = await runAction("set_item_state", {
        item_id: item.id,
        state,
    });
    if (!res) return;
    item.state = state;
    await refreshView();
}

async function reclassify(item) {
    actionInfo.value = "Reclassifying…";
    const res = await runAction("reclassify", { item_id: item.id });
    if (!res) return;
    actionInfo.value = `Reclassified as ${res.category ?? "?"} (quality ${res.quality ?? "?"})`;
    await refreshView();
}

async function saveAsNote(item) {
    const res = await runAction("save_as_note", { item_id: item.id });
    if (!res) return;
    actionInfo.value = "Saved as note";
    await refreshView();
    if (res.note_id) {
        emit("selectNote", res.note_id);
    }
}

function feedChipClass(f) {
    return {
        "rss-chip-feed-error": f.last_status === "error",
        "rss-chip-feed-off": !f.enabled,
    };
}

function feedChipTitle(f) {
    const status = !f.enabled ? "disabled" : f.last_status || "never fetched";
    return `${f.url}\nlast status: ${status}` + (f.last_error ? `\nerror: ${f.last_error}` : "");
}
</script>

<style scoped>
.rss-reader {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
}

.rss-toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    justify-content: space-between;
    align-items: center;
}

.rss-toolbar-group {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    align-items: center;
}

.rss-select {
    padding: 0.25rem 0.4rem;
    font-size: 0.85rem;
}

.rss-error {
    color: #d9534f;
    font-size: 0.9rem;
    margin: 0;
}

.rss-info {
    color: var(--font-color-secondary);
    font-size: 0.85rem;
    margin: 0;
}

.rss-meta,
.rss-feeds {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
}

.rss-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    font-size: 0.78rem;
    padding: 0.15rem 0.5rem;
    border-radius: 99px;
    background: var(--bg-color-secondary, rgba(127, 127, 127, 0.15));
    color: var(--font-color-secondary);
    cursor: default;
}

.rss-chip-warn {
    background: rgba(217, 83, 79, 0.15);
}

.rss-chip-feed-error {
    background: rgba(217, 83, 79, 0.25);
}

.rss-chip-feed-off {
    opacity: 0.6;
}

.rss-empty {
    font-size: 0.9rem;
    color: var(--font-color-secondary);
    font-style: italic;
    padding: 1rem 0;
}

.rss-items {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.rss-rejected summary {
    cursor: pointer;
    color: var(--font-color-secondary);
    font-size: 0.85rem;
    margin: 0.5rem 0;
}

.rss-rejected .rss-items {
    margin-top: 0.5rem;
    opacity: 0.8;
}

/* --- Editor --- */
.rss-editor {
    display: flex;
    flex-direction: column;
    gap: 1rem;
}

.rss-editor-section h4 {
    margin: 0 0 0.3rem 0;
    font-size: 1rem;
}

.rss-feed-row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    align-items: center;
    margin-bottom: 0.4rem;
}

.rss-input {
    padding: 0.3rem 0.45rem;
    font-size: 0.9rem;
}

.rss-input-url {
    flex: 2 1 260px;
    min-width: 200px;
}

.rss-input-name {
    flex: 1 1 140px;
}

.rss-check {
    display: inline-flex;
    gap: 0.3rem;
    align-items: center;
    font-size: 0.85rem;
    color: var(--font-color-secondary);
}

.rss-hint {
    font-size: 0.83rem;
    color: var(--font-color-secondary);
    margin: 0.2rem 0 0.5rem 0;
}

.rss-hint-code {
    white-space: pre;
    font-family: var(--font-mono, monospace);
    background: var(--bg-color-secondary, rgba(127, 127, 127, 0.1));
    padding: 0.5rem;
    border-radius: 6px;
    font-size: 0.78rem;
}

.rss-settings-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.6rem;
}

.rss-settings-grid label {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.83rem;
    color: var(--font-color-secondary);
}
</style>
