<template>
    <div class="want-note">
        <!-- Economics fields -->
        <div class="want-section">
            <div class="want-section-title">Economics</div>
            <div v-if="editing" class="want-fields">
                <label
                    v-for="field in fields"
                    :key="field.key"
                    class="want-field"
                >
                    <span class="want-field-label">{{ field.label }}</span>
                    <span class="want-field-input">
                        <input
                            :value="inputValue(draft[field.key])"
                            class="want-input"
                            type="number"
                            min="0"
                            step="any"
                            :placeholder="field.placeholder"
                            :aria-label="field.label"
                            @input="onFieldInput(field.key, $event)"
                        />
                        <span class="want-field-unit">{{ field.unit }}</span>
                    </span>
                </label>
            </div>
            <div v-else class="want-fields">
                <div
                    v-for="field in fields"
                    :key="field.key"
                    class="want-field want-field-readonly"
                    :class="{ 'want-field-unknown': draft[field.key] == null }"
                >
                    <span class="want-field-label">{{ field.label }}</span>
                    <span class="want-field-value">
                        {{ formatNumber(draft[field.key]) }}
                        <span class="want-field-unit">{{ field.unit }}</span>
                    </span>
                </div>
            </div>
        </div>

        <!-- Derived metrics (server-computed; refresh on save) -->
        <div v-if="metricRows.length" class="want-section">
            <div class="want-section-title">Derived Metrics</div>
            <div class="want-metrics">
                <div
                    v-for="metric in metricRows"
                    :key="metric.label"
                    class="want-metric"
                    :class="{ 'want-metric-gain': metric.gain }"
                >
                    <span class="want-metric-value">
                        {{ formatNumber(metric.value)
                        }}<span class="want-metric-unit">{{
                            metric.unit
                        }}</span>
                    </span>
                    <span class="want-metric-label">{{ metric.label }}</span>
                </div>
            </div>
        </div>

        <!-- Urge log -->
        <div class="want-section">
            <div class="want-log-header">
                <button
                    class="want-log-btn"
                    :disabled="logging"
                    title="Log one urge for today"
                    @click="logWant"
                >
                    {{ logging ? "Logging…" : "⚡ Today I wanted this" }}
                </button>
                <span class="want-log-stats">
                    ×{{ viewData.total }} wanted
                    <template v-if="viewData.wanted_days"
                        >· {{ viewData.wanted_days }}
                        {{ viewData.wanted_days === 1 ? "day" : "days" }}</template
                    >
                    <template v-if="viewData.today_count"
                        >· {{ viewData.today_count }} today</template
                    >
                </span>
                <button
                    v-if="viewData.entries.length"
                    class="want-clear-btn"
                    :disabled="logging"
                    @click="clearLog"
                >
                    Clear log
                </button>
            </div>
            <p v-if="actionError" class="want-error">{{ actionError }}</p>
            <ul v-if="viewData.entries.length" class="want-entries">
                <li
                    v-for="entry in viewData.entries"
                    :key="entry.id"
                    class="want-entry"
                >
                    <span class="want-entry-date">{{
                        formatDateTime(entry.created_at)
                    }}</span>
                    <button
                        class="want-entry-remove"
                        title="Remove this entry"
                        :disabled="logging"
                        @click="removeEntry(entry.id)"
                    >
                        ✕
                    </button>
                </li>
            </ul>
            <p v-else class="want-empty-log">
                Nothing logged yet — press the button whenever the want
                comes back.
            </p>
        </div>
    </div>
</template>

<script setup>
import { ref, computed, watch } from "vue";
import { pluginActionV2 } from "../../api.js";
import { formatNumber } from "../gridfinity/dashboard.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

// The six economic fields, in the order the user specified them.
const fields = [
    {
        key: "acquisition_cost",
        label: "Acquisition Cost",
        unit: "",
        placeholder: "e.g. 1200",
    },
    {
        key: "operating_expenses_per_month",
        label: "Operating Expenses / month",
        unit: "",
        placeholder: "e.g. 5.50",
    },
    {
        key: "disposal_costs",
        label: "Disposal Costs",
        unit: "",
        placeholder: "e.g. 30",
    },
    {
        key: "potential_profit_per_month",
        label: "Potential Profit / month",
        unit: "",
        placeholder: "e.g. 10",
    },
    {
        key: "durability_years",
        label: "Durability",
        unit: "years",
        placeholder: "e.g. 4",
    },
    {
        key: "space_requirements_m3",
        label: "Space Requirements",
        unit: "m³",
        placeholder: "e.g. 0.25",
    },
];

const FIELD_KEYS = fields.map((f) => f.key);

// Config draft (the six fields) — this is what gets emitted to the parent.
const draft = ref(emptyDraft());

// View data (log + metrics) arrives via the merged customData prop or the
// action responses and is display-only — never emitted.
const viewData = ref(emptyView());

const logging = ref(false);
const actionError = ref("");

function emptyDraft() {
    return {
        acquisition_cost: null,
        operating_expenses_per_month: null,
        disposal_costs: null,
        potential_profit_per_month: null,
        durability_years: null,
        space_requirements_m3: null,
    };
}

function emptyView() {
    return {
        total: 0,
        wanted_days: 0,
        last30_days: 0,
        today_count: 0,
        last_wanted_at: "",
        entries: [],
        metrics: emptyMetrics(),
    };
}

function emptyMetrics() {
    return {
        monthly_net: null,
        lifetime_months: null,
        total_cost: null,
        cost_per_month: null,
        payback_months: null,
    };
}

function toNumberOrNull(value) {
    if (value === null || value === undefined || value === "") return null;
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
}

// The derived metrics shown under the fields; a negative monthly net is a
// gain and gets the teal accent.
const metricRows = computed(() => {
    const m = viewData.value.metrics || emptyMetrics();
    const rows = [
        {
            label: "Monthly Net",
            value: m.monthly_net,
            unit: "/mo",
            gain: m.monthly_net !== null && m.monthly_net < 0,
        },
        { label: "Lifetime", value: m.lifetime_months, unit: "mo" },
        { label: "Total Cost of Ownership", value: m.total_cost, unit: "" },
        { label: "Cost / month", value: m.cost_per_month, unit: "/mo" },
        { label: "Payback", value: m.payback_months, unit: "mo" },
    ];
    return rows.filter((row) => row.value !== null);
});

// --- Draft editing ---

function inputValue(value) {
    return value === null || value === undefined ? "" : String(value);
}

function onFieldInput(key, event) {
    markLocal();
    const parsed = toNumberOrNull(event.target.value);
    if (draft.value[key] === parsed) {
        // No change to emit — but keep the raw input the user typed.
        return;
    }
    draft.value = { ...draft.value, [key]: parsed };
    emitConfig();
}

function emitConfig() {
    const payload = {};
    for (const key of FIELD_KEYS) {
        payload[key] = draft.value[key];
    }
    emit("update:customData", payload);
}

// --- Hydration (guard pattern against the echo-back loop) ---

let hydrating = false;
// Once the user edits a field or an action response arrives, the local
// state is strictly newer than anything the parent can echo back (the
// prop's embedded view is a snapshot from the last note fetch). Until the
// note identity changes, prop updates are then ignored.
let stateAuthoritative = false;

function markLocal() {
    stateAuthoritative = true;
}

function draftIsEmpty() {
    return FIELD_KEYS.every((key) => draft.value[key] === null);
}

function viewIsEmpty() {
    const v = viewData.value;
    return (
        v.entries.length === 0 &&
        !v.total &&
        Object.values(v.metrics).every((m) => m === null)
    );
}

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    const next = emptyDraft();
    if (cd && typeof cd === "object") {
        for (const key of FIELD_KEYS) {
            next[key] = toNumberOrNull(cd[key]);
        }
    }
    draft.value = next;

    const view = emptyView();
    if (cd && typeof cd === "object") {
        view.total = Number(cd.total) || 0;
        view.wanted_days = Number(cd.wanted_days) || 0;
        view.last30_days = Number(cd.last30_days) || 0;
        view.today_count = Number(cd.today_count) || 0;
        view.last_wanted_at =
            typeof cd.last_wanted_at === "string" ? cd.last_wanted_at : "";
        view.entries = Array.isArray(cd.entries) ? cd.entries : [];
        if (cd.metrics && typeof cd.metrics === "object") {
            view.metrics = {
                monthly_net: toNumberOrNull(cd.metrics.monthly_net),
                lifetime_months: toNumberOrNull(cd.metrics.lifetime_months),
                total_cost: toNumberOrNull(cd.metrics.total_cost),
                cost_per_month: toNumberOrNull(cd.metrics.cost_per_month),
                payback_months: toNumberOrNull(cd.metrics.payback_months),
            };
        }
    }
    viewData.value = view;
    actionError.value = "";
    stateAuthoritative = false;
    hydrating = false;
}

// Hydrate when the note identity changes.
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also catch async customData arrival (the merged view can land after
// mount). Once local edits/actions exist, prop echoes are ignored so a
// stale snapshot never clobbers newer local state (e.g. mid-typing field
// values or a log that was just cleared locally).
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (!cd || typeof cd !== "object") return;
        if (stateAuthoritative) return;
        if (!draftIsEmpty() || !viewIsEmpty()) return;
        hydrateFromProp();
    },
);

// --- Actions (the urge log) ---

function applyView(result) {
    if (!result || typeof result !== "object") return;
    markLocal(); // action responses are newer than the prop's view snapshot
    viewData.value = {
        total: Number(result.total) || 0,
        wanted_days: Number(result.wanted_days) || 0,
        last30_days: Number(result.last30_days) || 0,
        today_count: Number(result.today_count) || 0,
        last_wanted_at:
            typeof result.last_wanted_at === "string"
                ? result.last_wanted_at
                : "",
        entries: Array.isArray(result.entries)
            ? result.entries.filter(
                  (e) => e && Number.isFinite(Number(e.id)),
              )
            : [],
        metrics: {
            monthly_net: toNumberOrNull(result.metrics?.monthly_net),
            lifetime_months: toNumberOrNull(result.metrics?.lifetime_months),
            total_cost: toNumberOrNull(result.metrics?.total_cost),
            cost_per_month: toNumberOrNull(result.metrics?.cost_per_month),
            payback_months: toNumberOrNull(result.metrics?.payback_months),
        },
    };
}

async function runAction(actionID, params) {
    logging.value = true;
    actionError.value = "";
    try {
        const result = await pluginActionV2(
            props.token,
            props.note.id,
            actionID,
            params,
        );
        // Every log action returns the fresh view — update in place.
        applyView(result);
        return true;
    } catch (e) {
        actionError.value =
            (e && e.message) || `Action "${actionID}" failed.`;
        return false;
    } finally {
        logging.value = false;
    }
}

function logWant() {
    return runAction("log_want", null);
}

function removeEntry(entryID) {
    return runAction("remove_want_log", { entry_id: entryID });
}

function clearLog() {
    if (!window.confirm("Clear every urge-log entry of this want?")) {
        return;
    }
    return runAction("clear_want_log", null);
}

function formatDateTime(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
</script>

<style scoped>
.want-note {
    display: flex;
    flex-direction: column;
    gap: 0.9rem;
}

.want-section {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
}

.want-section-title {
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--font-color-secondary);
}

.want-fields {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(13rem, 1fr));
    gap: 0.5rem 0.9rem;
}

.want-field {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
}

.want-field-label {
    font-size: 0.72rem;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--font-color-secondary);
}

.want-field-input {
    display: flex;
    align-items: center;
    gap: 0.35rem;
}

.want-input {
    width: 100%;
    padding: 0.28rem 0.45rem;
    font-family: var(--font-mono);
    font-size: 0.88rem;
    color: var(--font-color);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    outline: none;
}

.want-input:focus {
    border-color: var(--accent-teal);
}

.want-field-unit {
    flex-shrink: 0;
    min-width: 2.2rem;
    font-size: 0.72rem;
    color: var(--font-color-secondary);
}

.want-field-readonly {
    flex-direction: row;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.2rem 0.4rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
}

.want-field-value {
    font-variant-numeric: tabular-nums;
    font-size: 0.88rem;
}

.want-field-unknown .want-field-value {
    color: var(--font-color-secondary);
}

.want-metrics {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr));
    gap: 0.4rem;
}

.want-metric {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.35rem 0.5rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
}

.want-metric-value {
    font-size: 1rem;
    font-weight: 700;
    color: var(--accent-amber, #d9a441);
    font-variant-numeric: tabular-nums;
}

.want-metric-gain .want-metric-value {
    color: var(--accent-teal);
}

.want-metric-unit {
    font-size: 0.7rem;
    font-weight: 400;
    color: var(--font-color-secondary);
    margin-left: 0.2rem;
}

.want-metric-label {
    font-size: 0.68rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--font-color-secondary);
}

.want-log-header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
}

.want-log-btn {
    padding: 0.4rem 0.8rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--accent-amber, #d9a441);
    background: var(--raised-bg);
    border: 1px solid var(--accent-amber, #d9a441);
    border-radius: 3px;
    cursor: pointer;
}

.want-log-btn:hover:not(:disabled) {
    box-shadow: 0 0 0 1px var(--accent-amber, #d9a441);
}

.want-log-btn:disabled {
    opacity: 0.55;
    cursor: default;
}

.want-log-stats {
    font-size: 0.78rem;
    color: var(--font-color-secondary);
}

.want-clear-btn {
    margin-left: auto;
    padding: 0.22rem 0.55rem;
    font-family: var(--font-mono);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    color: var(--font-color-secondary);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 2px;
    cursor: pointer;
}

.want-clear-btn:hover:not(:disabled) {
    color: #d9534f;
    border-color: #d9534f;
}

.want-error {
    margin: 0;
    font-size: 0.8rem;
    color: #d9534f;
}

.want-entries {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    margin: 0;
    padding: 0;
    list-style: none;
}

.want-entry {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.2rem 0.45rem;
    font-size: 0.8rem;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 2px;
}

.want-entry-date {
    color: var(--font-color);
    font-variant-numeric: tabular-nums;
}

.want-entry-remove {
    padding: 0 0.35rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    background: transparent;
    border: none;
    border-radius: 2px;
    cursor: pointer;
}

.want-entry-remove:hover:not(:disabled) {
    color: #d9534f;
}

.want-empty-log {
    margin: 0;
    font-size: 0.8rem;
    font-style: italic;
    color: var(--font-color-secondary);
}
</style>
