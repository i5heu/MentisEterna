<template>
    <div class="infx">
        <!-- ==================== Viewer ==================== -->
        <template v-if="!editing">
            <div class="infx-header">
                <span class="infx-badge infx-badge-provider">{{
                    view?.provider_label || providerLabel(draft.provider)
                }}</span>
                <span
                    class="infx-badge"
                    :class="keySet ? 'infx-badge-ok' : 'infx-badge-warn'"
                >
                    {{ keySet ? "API key stored" : "No API key stored" }}
                </span>
                <span v-if="effectiveBaseURL" class="infx-base-url mono">{{
                    effectiveBaseURL
                }}</span>
            </div>
            <p v-if="verdict" class="infx-help">
                {{ verdict }}
            </p>

            <table class="infx-table">
                <thead>
                    <tr>
                        <th>Tier</th>
                        <th>Model</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="t in tierRows" :key="t.tier">
                        <td class="infx-tier-name">
                            {{ t.tier }}
                            <span
                                v-if="draft.default_tier === t.tier"
                                class="infx-tag"
                                >default</span
                            >
                        </td>
                        <td class="mono">
                            {{ t.model || "—" }}
                            <span v-if="t.base_url" class="infx-muted">
                                ({{ t.base_url }})</span
                            >
                        </td>
                    </tr>
                    <tr v-if="tierRows.length === 0">
                        <td colspan="2" class="infx-muted">
                            No models configured yet.
                        </td>
                    </tr>
                </tbody>
            </table>

            <h4 class="infx-sub">Usage mappings</h4>
            <table class="infx-table">
                <thead>
                    <tr>
                        <th>Usage</th>
                        <th>Binds to</th>
                        <th>Model</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="u in usageRows" :key="u.id">
                        <td>
                            {{ u.label }}
                            <span
                                class="infx-tag"
                                :class="u.wired ? 'infx-tag-ok' : 'infx-tag-mut'"
                                >{{ u.wired ? "active" : "pending wiring" }}</span
                            >
                        </td>
                        <td class="mono">{{ u.bindLabel }}</td>
                        <td class="mono">{{ u.model }}</td>
                    </tr>
                </tbody>
            </table>

            <ul v-if="warnings.length" class="infx-warnings">
                <li v-for="w in warnings" :key="w">{{ w }}</li>
            </ul>

            <div class="infx-toolbar">
                <button class="btn-ghost btn-sm" :disabled="busy" @click="runTest()">
                    {{ busy ? "Testing…" : "Test connection" }}
                </button>
            </div>
            <p v-if="testSummary" class="infx-info" :class="{ 'infx-err': testSummary.err }">
                {{ testSummary.text }}
            </p>
        </template>

        <!-- ==================== Editor ==================== -->
        <template v-else>
            <div class="infx-field">
                <label class="infx-label">Provider</label>
                <select v-model="draft.provider" class="infx-select">
                    <option value="openrouter">OpenRouter</option>
                    <option value="mistral">Mistral</option>
                    <option value="compatible">
                        OpenAI-compatible (custom endpoint)
                    </option>
                </select>
            </div>

            <div class="infx-field">
                <label class="infx-label">Base URL</label>
                <input
                    v-model="draft.base_url"
                    type="text"
                    class="infx-input mono"
                    :placeholder="providerDefaultBaseURL"
                />
                <p class="infx-help">
                    Empty uses the provider default ({{ providerDefaultBaseURL
                    }}). The client appends <span class="mono">/v1/chat/completions</span>.
                    <template v-if="draft.provider === 'compatible'">
                        <strong>Required.</strong>
                    </template>
                </p>
            </div>

            <div class="infx-field">
                <label class="infx-label">API Key (write-only)</label>
                <div class="infx-key-row">
                    <input
                        v-model="draft.api_key"
                        type="password"
                        class="infx-input mono"
                        autocomplete="off"
                        :placeholder="
                            keySet
                                ? 'Key stored — leave empty to keep, type to replace'
                                : 'Paste the provider API key'
                        "
                    />
                    <button
                        v-if="keySet"
                        class="btn-ghost btn-sm"
                        :disabled="busy"
                        @click="clearApiKey"
                    >
                        Clear stored key
                    </button>
                </div>
                <p class="infx-help">
                    The key is stored server-side and never displayed again —
                    note views return only whether one is set.
                </p>
            </div>

            <div class="infx-field">
                <label class="infx-label">Default tier</label>
                <select v-model="draft.default_tier" class="infx-select">
                    <option value="">None — only explicit usage mappings bind</option>
                    <option
                        v-for="tier in TIERS"
                        :key="tier"
                        :value="tier"
                    >
                        {{ tier }}
                    </option>
                </select>
                <p class="infx-help">
                    Usages without an explicit mapping fall back to this tier.
                </p>
            </div>

            <h4 class="infx-sub">
                Tier models
                <button
                    class="btn-ghost btn-sm infx-sub-btn"
                    :disabled="busy"
                    @click="loadModels"
                >
                    {{ modelsBtnLabel }}
                </button>
            </h4>
            <div v-for="tier in TIERS" :key="tier" class="infx-tier-row">
                <span class="infx-tier-name">{{
                    tier
                }}<span
                    v-if="draft.default_tier === tier"
                    class="infx-tag"
                    >default</span
                ></span>
                <input
                    v-model="draft.tiers[tier].model"
                    type="text"
                    class="infx-input mono infx-model-input"
                    list="infx-models"
                    placeholder="provider/model-id"
                />
                <button
                    class="btn-ghost btn-sm"
                    :disabled="!draft.tiers[tier].model || busy"
                    @click="runTest(tier)"
                >
                    Test
                </button>
                <p
                    v-if="testResults[tier]"
                    class="infx-test-result"
                    :class="{ 'infx-err': testResults[tier].err }"
                >
                    {{ testResults[tier].text }}
                </p>
            </div>
            <datalist id="infx-models">
                <option v-for="m in models" :key="m.id" :value="m.id">
                    {{ m.name }}
                </option>
            </datalist>

            <h4 class="infx-sub">Usage mappings</h4>
            <p class="infx-help">
                Usages resolve through the nearest inferencer note in the
                hierarchy (children → siblings → ancestors) and fall back to
                config.toml when nothing binds.
            </p>
            <div v-for="u in usageRows" :key="u.id" class="infx-usage-row">
                <div class="infx-usage-head">
                    <span class="infx-usage-label">{{ u.label }}</span>
                    <span
                        class="infx-tag"
                        :class="u.wired ? 'infx-tag-ok' : 'infx-tag-mut'"
                        :title="u.description"
                    >
                        {{ u.wired ? "active" : "pending backend wiring" }}
                    </span>
                </div>
                <div class="infx-usage-controls">
                    <select
                        :value="usageMode(u.id)"
                        class="infx-select"
                        @change="setUsageMode(u.id, $event.target.value)"
                    >
                        <option value="none">Not mapped</option>
                        <option value="tier">By tier</option>
                        <option value="model">Direct model</option>
                    </select>
                    <select
                        v-if="usageMode(u.id) === 'tier'"
                        :value="draft.usages[u.id]?.tier || ''"
                        class="infx-select"
                        @change="setUsageTier(u.id, $event.target.value)"
                    >
                        <option
                            v-for="tier in TIERS"
                            :key="tier"
                            :value="tier"
                        >
                            {{ tier }}
                        </option>
                    </select>
                    <input
                        v-if="usageMode(u.id) === 'model'"
                        :value="draft.usages[u.id]?.model || ''"
                        type="text"
                        class="infx-input mono"
                        list="infx-models"
                        placeholder="provider/model-id"
                        @input="setUsageModel(u.id, $event.target.value)"
                    />
                    <span v-if="u.resolvedLabel" class="infx-resolved mono">
                        → {{ u.resolvedLabel }}
                    </span>
                </div>
                <p
                    v-if="usageMode(u.id) === 'tier' && !u.resolvedModel"
                    class="infx-help infx-help-warn"
                >
                    That tier has no model yet — set it in Tier models above.
                </p>
            </div>

            <p v-if="actionError" class="infx-err">{{ actionError }}</p>
        </template>
    </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from "vue";

/**
 * LLM Inferencer note-type component.
 *
 * Editor: provider/base URL/write-only API key, four tier models
 * (big/middle/small/tiny), and per-usage bindings (tier or direct model).
 * The draft is emitted via update:customData; an empty api_key keeps the
 * stored key (write-only semantics), an explicit value replaces it.
 *
 * Viewer: resolved bindings and status from the server view (the parent
 * routes plugin.view into the uiSchema prop).
 */

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

const TIERS = ["big", "middle", "small", "tiny"];

const PROVIDER_DEFAULTS = {
    openrouter: "https://openrouter.ai/api",
    mistral: "https://api.mistral.ai",
    compatible: "",
};

const PROVIDER_LABELS = {
    openrouter: "OpenRouter",
    mistral: "Mistral",
    compatible: "OpenAI-compatible",
};

// --- server view (routed into uiSchema by the parent) ---
const view = ref(null);
watch(
    () => props.uiSchema,
    (v) => {
        if (v && (v.usages !== undefined || v.tiers !== undefined)) {
            view.value = v;
        }
    },
    { immediate: true },
);

// --- draft (config channel) ---
function emptyTier() {
    return { model: "", base_url: "" };
}

function emptyDraft() {
    return {
        provider: "openrouter",
        base_url: "",
        api_key: "",
        api_key_set: false,
        default_tier: "",
        tiers: {
            big: emptyTier(),
            middle: emptyTier(),
            small: emptyTier(),
            tiny: emptyTier(),
        },
        usages: {},
    };
}

function normalizeUsages(raw) {
    const out = {};
    if (!raw || typeof raw !== "object") return out;
    for (const [id, b] of Object.entries(raw)) {
        if (!b || typeof b !== "object") continue;
        const tier = typeof b.tier === "string" ? b.tier : "";
        const model = typeof b.model === "string" ? b.model : "";
        if (tier) out[id] = { tier };
        else if (model) out[id] = { model };
    }
    return out;
}

function normalize(raw) {
    const d = emptyDraft();
    d.provider = raw?.provider || "openrouter";
    d.base_url = raw?.base_url || "";
    d.api_key = ""; // write-only: never round-trips from the server
    d.api_key_set = !!raw?.api_key_set;
    d.default_tier = raw?.default_tier || "";
    for (const tier of TIERS) {
        const t = raw?.tiers?.[tier];
        if (t && typeof t === "object") {
            d.tiers[tier] = { model: t.model || "", base_url: t.base_url || "" };
        }
    }
    d.usages = normalizeUsages(raw?.usages);
    return d;
}

const draft = reactive(emptyDraft());
let hydrating = false;

function hydrate() {
    hydrating = true;
    const d = normalize(props.customData);
    Object.keys(draft).forEach((k) => delete draft[k]);
    Object.assign(draft, d);
    hydrating = false;
}

watch(() => props.note?.id, hydrate, { immediate: true });

// Emit only meaningful fields: the backend rejects tier entries without a
// model, the local draft always renders all four tiers, and the write-only
// API key is sent only when the user actually typed one (empty = keep).
watch(
    draft,
    (val) => {
        if (hydrating) return;
        const payload = {
            provider: val.provider,
            base_url: val.base_url.trim(),
            default_tier: val.default_tier,
            api_key_set: val.api_key_set,
            usages: JSON.parse(JSON.stringify(val.usages)),
            tiers: {},
        };
        if (val.api_key.trim()) {
            payload.api_key = val.api_key.trim();
        }
        for (const tier of TIERS) {
            const t = val.tiers[tier];
            if (t && t.model && t.model.trim()) {
                payload.tiers[tier] = {
                    model: t.model.trim(),
                    base_url: (t.base_url || "").trim(),
                };
            }
        }
        emit("update:customData", payload);
    },
    { deep: true },
);

// --- derived state ---
const providerDefaultBaseURL = computed(
    () => PROVIDER_DEFAULTS[draft.provider] || "",
);

const effectiveBaseURL = computed(() => {
    if (view.value) return view.value.base_url || view.value.default_base_url || "";
    return draft.base_url || providerDefaultBaseURL.value;
});

const keySet = computed(() => {
    if (view.value) return !!view.value.api_key_set;
    return !!draft.api_key_set;
});

const verdict = computed(() => {
    if (!view.value) return "";
    const bound = view.value.usages?.some?.((u) => u.binding) || false;
    if (!bound) {
        return "Nothing mapped yet — map a usage below or set a default tier so this inferencer serves it.";
    }
    const unwired =
        view.value.usages?.filter?.((u) => u.binding && !u.wired) || [];
    if (unwired.length) {
        return `Configured mappings for ${unwired.length} usage(s) take effect once the backend routes them through inferencer notes.`;
    }
    return "";
});

const tierRows = computed(() => {
    if (view.value && Array.isArray(view.value.tiers)) {
        return view.value.tiers.map((t) => ({
            tier: t.tier,
            model: t.model,
            base_url: t.base_url || "",
        }));
    }
    return TIERS.filter((t) => draft.tiers[t].model).map((t) => ({
        tier: t,
        model: draft.tiers[t].model,
        base_url: draft.tiers[t].base_url || "",
    }));
});

// The usage catalog: from the server view when present, else the mapped
// draft keys (the full catalog is server metadata).
const usageRows = computed(() => {
    if (view.value && Array.isArray(view.value.usages)) {
        return view.value.usages.map((u) => {
            const local = draft.usages[u.id];
            const resolvedModel = local?.model || u.binding?.model || "";
            const resolvedLabel = local?.tier
                ? `tier ${local.tier} → ${resolvedModel}`
                : local?.model
                  ? `model ${local.model}`
                  : u.binding
                    ? `tier ${u.binding.tier} → ${u.binding.model}`
                    : "";
            return {
                id: u.id,
                label: u.label,
                description: u.description,
                wired: u.wired,
                incomplete: !!u.incomplete,
                bindLabel: u.binding
                    ? u.binding.tier
                        ? `tier ${u.binding.tier}`
                        : "direct model"
                    : "—",
                model: u.binding?.model || "",
                resolvedModel,
                resolvedLabel,
            };
        });
    }
    return Object.keys(draft.usages).map((id) => ({
        id,
        label: id,
        description: "",
        wired: false,
        bindLabel: draft.usages[id].tier ? `tier ${draft.usages[id].tier}` : "direct model",
        model: draft.usages[id].model || "",
        resolvedModel: draft.usages[id].model || "",
        resolvedLabel: "",
    }));
});

const warnings = computed(() => view.value?.warnings || []);

// --- usage mapping helpers ---
function usageMode(id) {
    const b = draft.usages[id];
    if (!b) return "none";
    return b.tier ? "tier" : "model";
}

function setUsageMode(id, mode) {
    if (mode === "none") {
        delete draft.usages[id];
    } else if (mode === "tier") {
        draft.usages[id] = { tier: "small" };
    } else {
        draft.usages[id] = { model: "" };
    }
}

function setUsageTier(id, tier) {
    draft.usages[id] = { tier };
}

function setUsageModel(id, model) {
    draft.usages[id] = { model: model.trim() };
    if (!draft.usages[id].model) delete draft.usages[id];
}

// --- actions ---
const busy = ref(false);
const actionError = ref("");
const models = ref([]);
const modelsLoaded = ref(0);
const modelsBtnLabel = computed(() =>
    modelsLoaded.value
        ? `Reload models (${modelsLoaded.value})`
        : "Load models from provider",
);

const testSummary = ref(null);
const testResults = reactive({});

async function runAction(actionID, params) {
    busy.value = true;
    actionError.value = "";
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

function testText(res) {
    if (!res) return { err: true, text: actionError.value || "Action failed" };
    if (res.ok) {
        return {
            err: false,
            text: `✓ ${res.latency_ms} ms — "${String(res.reply || "").slice(0, 40)}"`,
        };
    }
    return { err: true, text: `✕ ${res.error || "provider error"}` };
}

async function runTest(tier) {
    if (tier) {
        const res = await runAction("test", { tier });
        testResults[tier] = testText(res);
        return;
    }
    const res = await runAction("test", {});
    testSummary.value = testText(res);
}

async function loadModels() {
    const res = await runAction("list_models", {});
    if (!res) return;
    models.value = res.models || [];
    modelsLoaded.value = res.total || models.value.length;
}

async function clearApiKey() {
    const res = await runAction("clear_api_key", {});
    if (!res) return;
    draft.api_key = "";
    draft.api_key_set = false;
    if (view.value) view.value.api_key_set = false;
    // Drop the local server-view copy so the viewer/editor state agrees.
    actionError.value = "";
}
</script>

<style scoped>
.infx {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
}
.infx-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
}
.infx-badge {
    font-size: 0.72rem;
    padding: 2px 10px;
    border-radius: 999px;
    background: rgba(127, 127, 127, 0.14);
    white-space: nowrap;
}
.infx-badge-provider {
    background: rgba(64, 120, 192, 0.25);
}
.infx-badge-ok {
    background: rgba(60, 150, 90, 0.22);
    color: #4fc27d;
}
.infx-badge-warn {
    background: rgba(190, 130, 40, 0.2);
    color: #d8a04a;
}
.infx-base-url {
    font-size: 0.75rem;
    opacity: 0.7;
    word-break: break-all;
}
.infx-field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
}
.infx-label {
    font-size: 0.78rem;
    opacity: 0.85;
}
.infx-help {
    font-size: 0.75rem;
    opacity: 0.65;
    margin: 0;
}
.infx-help-warn {
    color: #d8a04a;
    opacity: 1;
}
.infx-input,
.infx-select {
    background: var(--bg-2, #1d1f24);
    border: 1px solid rgba(127, 127, 127, 0.3);
    border-radius: 6px;
    color: inherit;
    padding: 6px 10px;
    font-size: 0.85rem;
    min-width: 0;
}
.infx-key-row {
    display: flex;
    gap: 0.5rem;
    align-items: center;
}
.infx-key-row .infx-input {
    flex: 1;
}
.infx-sub {
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    opacity: 0.8;
    margin: 0.9rem 0 0.2rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
}
.infx-tier-row {
    display: grid;
    grid-template-columns: 84px 1fr auto;
    gap: 0.5rem;
    align-items: center;
    padding: 0.2rem 0;
    position: relative;
}
.infx-model-input {
    width: 100%;
}
.infx-test-result {
    grid-column: 1 / -1;
    font-size: 0.75rem;
    margin: 0;
    opacity: 0.85;
}
.infx-tier-name {
    font-size: 0.82rem;
    text-transform: capitalize;
    display: flex;
    align-items: center;
    gap: 0.35rem;
}
.infx-tag {
    font-size: 0.65rem;
    padding: 1px 7px;
    border-radius: 999px;
    background: rgba(127, 127, 127, 0.16);
    text-transform: none;
}
.infx-tag-ok {
    background: rgba(60, 150, 90, 0.22);
    color: #4fc27d;
}
.infx-tag-mut {
    background: rgba(127, 127, 127, 0.16);
    opacity: 0.75;
}
.infx-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.82rem;
}
.infx-table th {
    text-align: left;
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    opacity: 0.65;
    padding: 0.25rem 0.5rem 0.25rem 0;
    border-bottom: 1px solid rgba(127, 127, 127, 0.2);
}
.infx-table td {
    padding: 0.3rem 0.5rem 0.3rem 0;
    border-bottom: 1px solid rgba(127, 127, 127, 0.1);
    vertical-align: top;
}
.infx-usage-row {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 0.45rem 0;
    border-bottom: 1px solid rgba(127, 127, 127, 0.1);
}
.infx-usage-head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
}
.infx-usage-label {
    font-size: 0.85rem;
}
.infx-usage-controls {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
}
.infx-usage-controls .infx-input {
    flex: 1;
    min-width: 200px;
}
.infx-resolved {
    font-size: 0.75rem;
    opacity: 0.7;
}
.infx-warnings {
    font-size: 0.78rem;
    color: #d8a04a;
    margin: 0.5rem 0 0;
    padding-left: 1rem;
}
.infx-toolbar {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.4rem;
}
.infx-info {
    font-size: 0.8rem;
    margin: 0.25rem 0 0;
}
.infx-err {
    color: #e06c60;
    font-size: 0.8rem;
    margin: 0.25rem 0 0;
    white-space: pre-wrap;
    word-break: break-word;
}
.mono {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
}
.infx-muted {
    opacity: 0.6;
}
</style>
