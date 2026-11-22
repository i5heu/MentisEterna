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
                        :disabled="busy || refreshing"
                        @click="reclassifyAll"
                        title="Re-run the LLM significance evaluation for every article of this reader"
                    >
                        Re-evaluate all
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
                    <select v-model="sortMode" class="rss-select" title="Timeline ordering">
                        <option value="score">★ Best points</option>
                        <option value="new">Newest first</option>
                        <option value="old">Oldest first</option>
                    </select>
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
                            ? 'No RSS Prompt note in scope — add a child note of type RSS Prompt to customize the evaluation. Using the built-in default significance-evaluation prompt.'
                            : 'The evaluation prompt is defined by the RSS Prompt note in scope.'
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
                    {{ view.counts.pending_classify }} evaluating
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
                    v-for="item in displayItems"
                    :key="item.id"
                    :item="item"
                    :busy="busy"
                    @open="openArticle(item)"
                    @mark="set_item_state(item, $event)"
                    @reclassify="reclassify(item)"
                    @save="saveAsNote(item)"
                />
            </div>

            <details v-if="showRejected && view && view.rejected.length" class="rss-rejected">
                <summary>
                    Rejected articles ({{ view.counts?.rejected ?? 0 }}) —
                    ban words / penalty threshold / below min_points
                </summary>
                <div class="rss-items">
                    <RssItemCard
                        v-for="item in view.rejected"
                        :key="item.id"
                        :item="item"
                        :busy="busy"
                        dimmed
                        @open="openArticle(item)"
                        @mark="set_item_state(item, $event)"
                        @reclassify="reclassify(item)"
                        @save="saveAsNote(item)"
                    />
                </div>
            </details>

            <!-- ==================== Article viewer ==================== -->
            <div
                v-if="activeItemId"
                class="rss-modal-overlay"
                @click.self="closeArticle"
            >
                <div class="rss-modal" role="dialog" aria-modal="true">
                    <div class="rss-modal-head">
                        <h3 class="rss-modal-title">
                            {{
                                modalMeta?.title ||
                                "(untitled)"
                            }}
                        </h3>
                        <a
                            v-if="activeItem?.url"
                            class="btn-ghost btn-sm"
                            :href="activeItem.url"
                            target="_blank"
                            rel="noopener noreferrer"
                            >Original ↗</a
                        >
                        <button
                            class="btn-ghost btn-sm"
                            @click="closeArticle"
                        >
                            Close
                        </button>
                    </div>

                    <div v-if="modalMeta" class="rss-modal-meta">
                        <span class="rss-chip">{{
                            modalMeta.feed_name || "unknown feed"
                        }}</span>
                        <span v-if="modalMeta.author" class="rss-chip">{{
                            modalMeta.author
                        }}</span>
                        <span v-if="publishedLabel" class="rss-chip">{{
                            publishedLabel
                        }}</span>
                        <span
                            v-if="modalMeta.evaluated"
                            class="rss-chip rss-chip-quality"
                            title="LLM evaluation points: 10 × (weights × significance, personal relevance, positivity, humans-affected) − rule penalties"
                            >★ {{ evalPoints }}/10</span
                        >
                        <span
                            v-if="modalMeta.evaluated"
                            class="rss-chip"
                            title="Global significance of the event (0.0–1.0)"
                            >significance {{ evalNum("significance") }}</span
                        >
                        <span
                            v-if="modalMeta.evaluated"
                            class="rss-chip"
                            title="0.0 = positive · 0.5 = neutral · 1.0 = negative for humanity"
                            >negativity {{ evalNum("negativity") }}</span
                        >
                        <span
                            v-if="modalMeta.evaluated"
                            class="rss-chip"
                            title="How relevant the article is for you (Germany/EU + personal context in the prompt)"
                            >relevance {{ evalNum("personal_relevance") }}</span
                        >
                        <span
                            v-if="modalMeta.category"
                            class="rss-chip"
                            >{{ modalMeta.category }}</span
                        >
                        <span
                            v-if="modalMeta.quality"
                            class="rss-chip"
                            :title="`LLM quality rating: ${modalMeta.quality}/10`"
                            >Q{{ modalMeta.quality }}</span
                        >
                        <span
                            v-if="modalMeta.penalty_score > 0"
                            class="rss-chip rss-chip-penalty"
                            >−{{ modalMeta.penalty_score }}</span
                        >
                        <span
                            v-if="modalMeta.banned"
                            class="rss-chip rss-chip-warn"
                            >banned</span
                        >
                    </div>

                    <p v-if="evaluatedHumans" class="rss-modal-meta-detail">
                        {{ evaluatedHumans }}
                    </p>
                    <p
                        v-if="article && article.classify_error && article.classify_status === 'done'"
                        class="rss-modal-meta-detail rss-hint"
                    >
                        ⚠ evaluation notes: {{ article.classify_error }}
                    </p>

                    <template v-if="article && (article.summary || article.reflection)">
                        <p
                            v-if="article.summary && !article.reflection"
                            class="rss-modal-summary"
                        >
                            {{ article.summary }}
                        </p>
                        <blockquote
                            v-if="article.reflection"
                            class="rss-modal-reflection"
                        >
                            {{ article.reflection }}
                        </blockquote>
                    </template>

                    <div class="rss-modal-body">
                        <p v-if="articleLoading" class="rss-modal-hint">
                            Loading article content…
                        </p>
                        <p v-else-if="articleError" class="rss-error">
                            {{ articleError }}
                        </p>
                        <template v-else-if="article">
                            <pre
                                v-if="article.content"
                                class="rss-modal-content"
                                >{{ article.content }}</pre
                            >
                            <p v-else class="rss-modal-hint">
                                No article text was stored for this item<span
                                    v-if="activeItem && activeItem.url"
                                >
                                    —
                                    <a
                                        :href="activeItem.url"
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        >open the original</a
                                    ></span
                                >.
                            </p>
                            <p
                                v-if="article.content_source === 'feed'"
                                class="rss-modal-hint"
                            >
                                Showing the feed-provided snippet; the full
                                page could not be extracted.
                            </p>
                        </template>
                    </div>

                    <div
                        v-if="activeItem"
                        class="rss-modal-actions"
                    >
                        <button
                            class="btn-ghost btn-sm"
                            :disabled="busy"
                            @click="set_item_state(activeItem, 'starred')"
                        >
                            {{ activeItem.state === "starred" ? "★" : "☆" }}
                            Star
                        </button>
                        <button
                            class="btn-ghost btn-sm"
                            :disabled="busy"
                            @click="
                                set_item_state(
                                    activeItem,
                                    activeItem.state === 'read' ? 'new' : 'read',
                                )
                            "
                        >
                            {{
                                activeItem.state === "read"
                                    ? "Mark unread"
                                    : "Mark read"
                            }}
                        </button>
                        <button
                            class="btn-ghost btn-sm"
                            :disabled="busy"
                            @click="saveAsNote(activeItem)"
                            :title="
                                activeItem.saved_note_id
                                    ? 'Already saved as a note'
                                    : 'Save this article as a real child note'
                            "
                        >
                            {{ activeItem.saved_note_id ? "Saved" : "Save as Note" }}
                        </button>
                        <button
                            class="btn-ghost btn-sm"
                            :disabled="busy"
                            @click="reclassify(activeItem)"
                            title="Re-run the LLM significance evaluation"
                        >
                            Re-evaluate
                        </button>
                    </div>
                </div>
            </div>
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
                    <h4>Rules (ban words, penalties &amp; points)</h4>
                    <p class="rss-hint">
                        Typed directly in this note's body — after saving the
                        body, press “Reapply rules” to re-score old articles
                        (new articles pick them up automatically). Penalty
                        points subtract from the LLM evaluation points
                        (★ shown on each card). Format:
                    </p>
                    <pre class="rss-hint rss-hint-code">ban: crypto giveaway
press release: 6
webinar: 2
wonderful deep dive: -3
threshold: 8
min_points: 2
weights: significance=0.5, relevance=0.25, positivity=0.15, humans=0.10</pre>
                    <p class="rss-hint">
                        <strong>threshold</strong> rejects articles whose rule
                        penalties reach it (0 = off) ·
                        <strong>min_points</strong> rejects LLM-evaluated
                        articles scoring below it (0 = off) ·
                        <strong>weights</strong> shape how significance,
                        personal relevance, positivity, and humans-affected
                        combine into the 0–10 points.
                    </p>
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
                            Evaluate new articles with the LLM
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
                        Evaluation prompt: place a note of type
                        <strong>RSS Prompt</strong> inside (or above) this
                        reader — its body is the prompt.
                    </p>
                </div>
            </div>
        </template>
    </div>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from "vue";
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
const sortMode = ref("score"); // score | new | old
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

// The backend orders by date; the points ranking is applied here so the
// sort mode is instantly switchable without a re-fetch.
const displayItems = computed(() => {
    const items = filteredItems.value.slice();
    if (sortMode.value === "score") {
        items.sort(
            (a, b) =>
                (Number(b.points) || 0) - (Number(a.points) || 0) ||
                String(b.published_at || "").localeCompare(
                    String(a.published_at || ""),
                ),
        );
    } else if (sortMode.value === "old") {
        items.reverse();
    }
    return items;
});

// --- Article viewer: extracted content, fetched on demand via get_item ---
// The view payload deliberately omits item content (it would dwarf the
// response); the modal loads it when an article is opened.
const activeItemId = ref(0);
const article = ref(null);
const articleLoading = ref(false);
const articleError = ref("");
let openSeq = 0;

const activeItem = computed(() => {
    const id = activeItemId.value;
    if (!id || !view.value) return null;
    return (
        (view.value.items || []).find((it) => it.id === id) ||
        (view.value.rejected || []).find((it) => it.id === id) ||
        null
    );
});

// Article detail once loaded; falls back to the live list item while loading,
// so the modal can render its header immediately.
const modalMeta = computed(() => article.value ?? activeItem.value);

const publishedLabel = computed(() => {
    const s = modalMeta.value?.published_at;
    if (!s) return "";
    const d = new Date(s);
    if (Number.isNaN(d.getTime())) return s;
    return d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
    });
});

// --- Evaluation display helpers (modal) ---

const evalPoints = computed(() =>
    Number(modalMeta.value?.points ?? 0).toFixed(1),
);

const evalNum = (field) =>
    Number(modalMeta.value?.[field] ?? 0).toFixed(2);

const fmtMillions = (millions) => {
    const n = Number(millions) || 0;
    if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}B`;
    return `${n}M`;
};

const evaluatedHumans = computed(() => {
    const m = modalMeta.value;
    if (!m || !m.evaluated) return "";
    const nowD = Number(m.affected_now_direct ?? 0);
    const nowI = Number(m.affected_now_indirect ?? 0);
    const futD = Number(m.affected_future_direct ?? 0);
    const futI = Number(m.affected_future_indirect ?? 0);
    if (!nowD && !nowI && !futD && !futI) return "";
    return `Humans affected: now ${fmtMillions(nowD)} direct / ${fmtMillions(nowI)} indirect · future ${fmtMillions(futD)} direct / ${fmtMillions(futI)} indirect`;
});

// If the item leaves the view lists (e.g. purged by retention after a
// refresh), close the modal — its footer actions would otherwise dangle.
watch(activeItem, (it) => {
    if (!it && activeItemId.value) closeArticle();
});

async function openArticle(item) {
    const seq = ++openSeq;
    activeItemId.value = item.id;
    article.value = null;
    articleError.value = "";
    articleLoading.value = true;
    try {
        const { pluginActionV2 } = await import("../../api.js");
        const res = await pluginActionV2(
            props.token,
            props.note.id,
            "get_item",
            { item_id: item.id },
        );
        if (seq === openSeq) article.value = res;
    } catch (e) {
        if (seq === openSeq) articleError.value = e?.message || String(e);
    } finally {
        if (seq === openSeq) articleLoading.value = false;
    }
}

function closeArticle() {
    activeItemId.value = 0;
    article.value = null;
    articleError.value = "";
}

function onArticleKeydown(e) {
    if (e.key === "Escape") closeArticle();
}
watch(activeItemId, (id) => {
    if (id) window.addEventListener("keydown", onArticleKeydown);
    else window.removeEventListener("keydown", onArticleKeydown);
});
onBeforeUnmount(() =>
    window.removeEventListener("keydown", onArticleKeydown),
);

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
    actionInfo.value = `Rules reapplied: ${res.applied ?? 0} scored, ${res.rejected ?? 0} rejected (penalty threshold ${res.threshold ?? "?"} · min_points ${res.min_points ?? "?"})`;
    await refreshView();
}

// Queue every article for a fresh LLM evaluation, then poll until the
// scheduled refresh job has drained the queue.
async function reclassifyAll() {
    const res = await runAction("reclassify_all", {}, {
        info: "Queuing re-evaluation…",
    });
    if (!res) return;
    refreshing.value = true;
    actionInfo.value = `Re-evaluating ${res.reset ?? 0} articles…`;
    let tries = 0;
    const timer = setInterval(async () => {
        tries++;
        await refreshView();
        const pending = view.value?.counts?.pending_classify ?? 0;
        if ((pending === 0 && tries >= 2) || tries >= 24) {
            clearInterval(timer);
            refreshing.value = false;
            actionInfo.value =
                pending === 0 ? "Re-evaluation finished" : "";
        }
    }, 2500);
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
    actionInfo.value = "Re-evaluating…";
    const res = await runAction("reclassify", { item_id: item.id });
    if (!res) return;
    actionInfo.value = `Evaluated: ${Number(res.points ?? 0).toFixed(1)}/10 points (significance ${Number(res.significance ?? 0).toFixed(2)})`;
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

/* --- Article viewer modal ---
   Dark-mode only: this app has a single dark theme (App.vue :root,
   color-scheme: dark) — use its surface tokens, never light fallbacks
   (there is no --bg-color variable at all). */
.rss-modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.6);
    z-index: 2050;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1.5rem;
}

.rss-modal {
    background: var(--panel-bg, #061320);
    color: var(--font-color, #e0e8e4);
    border: 1px solid var(--border-color, #7e7567);
    border-radius: 10px;
    width: min(760px, 100%);
    max-height: min(86vh, 100%);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0 12px 40px var(--shadow-color, rgba(0, 0, 0, 0.6));
}

.rss-modal-head {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    padding: 0.8rem 1rem 0.4rem;
}

.rss-modal-title {
    margin: 0;
    font-size: 1.12rem;
    flex: 1;
}

.rss-modal-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
    padding: 0 1rem 0.6rem;
    border-bottom: 1px solid var(--border-color, #7e7567);
}

.rss-chip-quality {
    background: rgba(92, 184, 92, 0.18);
}

.rss-chip-penalty {
    background: rgba(240, 173, 78, 0.22);
}

.rss-modal-summary {
    margin: 0.7rem 1rem 0;
    font-style: italic;
    color: var(--font-color-secondary);
    font-size: 0.92rem;
}

.rss-modal-reflection {
    margin: 0.7rem 1rem 0;
    padding: 0.4rem 0.8rem;
    border-left: 3px solid var(--accent-teal, #6d9484);
    font-style: italic;
    color: var(--font-color-secondary);
    font-size: 0.92rem;
}

.rss-modal-meta-detail {
    margin: 0.35rem 1rem 0;
    font-size: 0.85rem;
    color: var(--font-color-secondary);
}

.rss-modal-body {
    flex: 1;
    overflow-y: auto;
    padding: 0.85rem 1rem;
}

.rss-modal-content {
    margin: 0;
    white-space: pre-wrap;
    overflow-wrap: break-word;
    font-family: inherit;
    font-size: 0.95rem;
    line-height: 1.6;
    color: var(--font-color, #e0e8e4);
}

.rss-modal-hint a,
.rss-modal-body a {
    color: var(--accent-teal, #6d9484);
}

.rss-modal-hint {
    font-size: 0.88rem;
    font-style: italic;
    color: var(--font-color-secondary);
}

.rss-modal-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    padding: 0.6rem 1rem;
    border-top: 1px solid var(--border-color, #7e7567);
}
</style>
