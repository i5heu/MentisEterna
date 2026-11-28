<template>
    <div class="gridfinity">
        <div class="gf-toolbar">
            <input
                v-model="search"
                class="gf-search"
                type="search"
                placeholder="Search wants…"
                aria-label="Search wants"
            />
            <select
                v-model="sortKey"
                class="gf-select"
                aria-label="Sort wants by"
            >
                <option
                    v-for="key in sortKeys"
                    :key="key.value"
                    :value="key.value"
                >
                    {{ key.label }}
                </option>
            </select>
            <button
                class="gf-btn gf-dir"
                :title="
                    sortDir === 'desc' ? 'Sort descending' : 'Sort ascending'
                "
                @click="sortDir = sortDir === 'desc' ? 'asc' : 'desc'"
            >
                {{ sortDir === "desc" ? "↓" : "↑" }}
            </button>
            <input
                v-model="maxTotalCost"
                class="gf-maxcost"
                type="number"
                min="0"
                step="any"
                placeholder="Max total cost"
                aria-label="Maximum total cost"
            />
            <div class="gf-chips">
                <button
                    class="gf-chip"
                    :class="{ 'gf-chip-active': wantedToday }"
                    @click="wantedToday = !wantedToday"
                >
                    Wanted today
                </button>
                <button
                    class="gf-chip"
                    :class="{ 'gf-chip-active': last30 }"
                    @click="last30 = !last30"
                >
                    Last 30 days
                </button>
                <button
                    class="gf-chip"
                    :class="{ 'gf-chip-active': neverWanted }"
                    @click="neverWanted = !neverWanted"
                >
                    Never wanted
                </button>
            </div>
            <button
                v-if="anyFilters"
                class="gf-chip gf-chip-clear"
                title="Clear search and filters"
                @click="clearFilters"
            >
                ✕ Clear
            </button>
            <button
                class="gf-btn gf-refresh"
                :disabled="refreshing"
                title="Reload the dashboard from the server"
                @click="refresh"
            >
                {{ refreshing ? "…" : "↻" }}
            </button>
        </div>

        <p v-if="refreshError" class="gf-error">{{ refreshError }}</p>

        <div class="gf-summary">
            <span class="gf-summary-item"
                >{{ totals.wants }} want{{ totals.wants === 1 ? "" : "s" }}</span
            >
            <span class="gf-summary-item"
                >Σ total
                {{ formatNumber(totals.sum_total_cost) }}</span
            >
            <span class="gf-summary-item"
                >Σ net {{ formatNumber(totals.sum_monthly_net) }}/mo</span
            >
            <span class="gf-summary-item"
                >Σ space {{ formatNumber(totals.sum_space_m3) }} m³</span
            >
            <span class="gf-summary-item"
                >⚡ {{ totals.wanted_today }} today</span
            >
            <span class="gf-summary-item gf-summary-shown"
                >{{ visibleItems.length }} shown</span
            >
        </div>

        <div v-if="visibleItems.length" class="gf-grid">
            <button
                v-for="item in visibleItems"
                :key="item.note_id"
                class="gf-card"
                :class="{ 'gf-card-private': item.high_private }"
                @click="$emit('selectNote', item.note_id)"
            >
                <div class="gf-card-head">
                    <span class="gf-card-title" :title="item.title">{{
                        item.title
                    }}</span>
                    <span
                        v-if="item.high_private"
                        class="gf-card-lock"
                        title="Highly private note"
                        >🔒</span
                    >
                </div>
                <div class="gf-card-metric">
                    <span
                        class="gf-metric-value"
                        :class="{ 'gf-metric-gain': isNetGain(item) }"
                        >{{ formatNumber(item.metrics.total_cost) }}</span
                    >
                    <span class="gf-metric-label">total cost</span>
                </div>
                <div class="gf-card-rows">
                    <span class="gf-row">
                        <span class="gf-row-label">Acquisition</span>
                        <span class="gf-row-value">{{
                            formatNumber(item.acquisition_cost)
                        }}</span>
                    </span>
                    <span class="gf-row">
                        <span class="gf-row-label">Net</span>
                        <span
                            class="gf-row-value"
                            :class="{ 'gf-metric-gain': isNetGain(item) }"
                            >{{ formatNumber(item.metrics.monthly_net)
                            }}<template v-if="item.metrics.monthly_net !== null"
                                >/mo</template
                            ></span
                        >
                    </span>
                    <span class="gf-row">
                        <span class="gf-row-label">Durability</span>
                        <span class="gf-row-value"
                            >{{ formatNumber(item.durability_years) }}<template
                                v-if="item.durability_years !== null"
                                >y</template
                            ></span
                        >
                    </span>
                    <span class="gf-row">
                        <span class="gf-row-label">Space</span>
                        <span class="gf-row-value"
                            >{{ formatNumber(item.space_requirements_m3)
                            }}<template
                                v-if="item.space_requirements_m3 !== null"
                                >m³</template
                            ></span
                        >
                    </span>
                </div>
                <div class="gf-card-log">
                    <span class="gf-log-count"
                        >⚡ {{ item.wanted_count }}×</span
                    >
                    <span
                        v-if="item.today_count > 0"
                        class="gf-log-today"
                        title="Wanted today"
                        >{{ item.today_count }} today</span
                    >
                    <span
                        v-if="item.last_wanted_at"
                        class="gf-log-last"
                        title="Last wanted"
                        >last {{ formatDate(item.last_wanted_at) }}</span
                    >
                </div>
            </button>
        </div>

        <p v-else-if="hasItems" class="empty-hint">
            No wants match the current search and filters.
        </p>
        <p v-else class="empty-hint">
            No want notes yet. Create a note of type "Wants &amp; Wishes" and
            it will show up here.
        </p>
    </div>
</template>

<script setup>
import { computed, ref, watch } from "vue";
import { fetchNote } from "../../api.js";
import {
    SORT_KEYS,
    filterAndSortItems,
    filtersActive,
    formatNumber,
    formatDate,
} from "./dashboard.js";
import { normalizeGridfinityData } from "./normalize.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

defineEmits(["selectNote", "update:customData"]);

const sortKeys = SORT_KEYS;

// Dashboard state (view data comes from the server; this note has no
// config of its own, so update:customData is never emitted).
const dashboardData = ref(normalizeGridfinityData(null));
const refreshing = ref(false);
const refreshError = ref("");

// Toolbar state.
const search = ref("");
const sortKey = ref("last_wanted_at");
const sortDir = ref("desc");
const maxTotalCost = ref("");
const wantedToday = ref(false);
const last30 = ref(false);
const neverWanted = ref(false);

const items = computed(() => dashboardData.value.items);
const totals = computed(() => dashboardData.value.totals);
const hasItems = computed(() => items.value.length > 0);

const visibleItems = computed(() =>
    filterAndSortItems(items.value, {
        search: search.value,
        sortKey: sortKey.value,
        sortDir: sortDir.value,
        maxTotalCost: maxTotalCost.value,
        wantedToday: wantedToday.value,
        last30: last30.value,
        neverWanted: neverWanted.value,
    }),
);

const anyFilters = computed(() =>
    filtersActive({
        search: search.value,
        maxTotalCost: maxTotalCost.value,
        wantedToday: wantedToday.value,
        last30: last30.value,
        neverWanted: neverWanted.value,
    }),
);

function clearFilters() {
    search.value = "";
    maxTotalCost.value = "";
    wantedToday.value = false;
    last30.value = false;
    neverWanted.value = false;
}

// A negative monthly net means the want pays for itself every month it
// exists — highlight it.
function isNetGain(item) {
    return item.metrics?.monthly_net !== null && item.metrics?.monthly_net < 0;
}

// Guard to break the echo-back loop (standard note-type pattern).
let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    refreshError.value = "";
    dashboardData.value = normalizeGridfinityData(props.customData);
    hydrating = false;
}

// Hydrate when the note identity changes.
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also catch async customData arrival (view payload lands after mount).
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (cd && typeof cd === "object" && items.value.length === 0) {
            hydrateFromProp();
        }
    },
);

// Reload the view from the server (wants change while the dashboard is
// open — the aggregated view is only rebuilt on note selection).
async function refresh() {
    if (!props.note?.id || refreshing.value) return;
    refreshing.value = true;
    refreshError.value = "";
    try {
        const detail = await fetchNote(props.token, props.note.id);
        dashboardData.value = normalizeGridfinityData(detail?.plugin?.view);
    } catch (e) {
        refreshError.value =
            (e && e.message) || "Failed to refresh the dashboard.";
    } finally {
        refreshing.value = false;
    }
}
</script>

<style scoped>
.gridfinity {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
}

.gf-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.4rem;
}

.gf-search {
    flex: 1 1 12rem;
    min-width: 10rem;
    padding: 0.3rem 0.5rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    color: var(--font-color);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    outline: none;
}

.gf-search:focus,
.gf-maxcost:focus {
    border-color: var(--accent-teal);
}

.gf-maxcost {
    width: 8.5rem;
    padding: 0.3rem 0.5rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    color: var(--font-color);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    outline: none;
}

.gf-select {
    padding: 0.3rem 0.4rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    color: var(--font-color);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    outline: none;
}

.gf-select:focus {
    border-color: var(--accent-teal);
}

.gf-btn {
    padding: 0.28rem 0.55rem;
    font-family: var(--font-mono);
    font-size: 0.85rem;
    color: var(--font-color);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
    cursor: pointer;
}

.gf-btn:hover:not(:disabled) {
    border-color: var(--accent-teal);
    color: var(--accent-teal);
}

.gf-btn:disabled {
    opacity: 0.5;
    cursor: default;
}

.gf-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
}

.gf-chip {
    padding: 0.22rem 0.5rem;
    font-family: var(--font-mono);
    font-size: 0.72rem;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    color: var(--font-color-secondary);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 2px;
    cursor: pointer;
}

.gf-chip:hover {
    color: var(--font-color);
    border-color: var(--accent-teal);
}

.gf-chip-active {
    color: var(--accent-teal);
    border-color: var(--accent-teal);
    background: var(--accent-teal-dim, rgba(63, 224, 207, 0.12));
}

.gf-chip-clear {
    color: var(--accent-amber, #d9a441);
}

.gf-error {
    font-size: 0.8rem;
    color: #d9534f;
    margin: 0;
}

.gf-summary {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem 0.9rem;
    padding: 0.35rem 0.5rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    border-radius: 3px;
}

.gf-summary-shown {
    margin-left: auto;
}

.gf-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr));
    gap: 0.5rem;
}

.gf-card {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.55rem 0.65rem;
    text-align: left;
    font-family: var(--font-mono);
    color: var(--font-color);
    background: var(--panel-bg);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    cursor: pointer;
    transition:
        border-color 0.1s,
        box-shadow 0.1s;
}

.gf-card:hover {
    border-color: var(--accent-teal);
    box-shadow: 0 0 0 1px var(--accent-teal);
}

.gf-card-private {
    border-style: dashed;
}

.gf-card-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem;
}

.gf-card-title {
    overflow: hidden;
    font-weight: 600;
    font-size: 0.9rem;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.gf-card-lock {
    flex-shrink: 0;
    font-size: 0.8rem;
}

.gf-card-metric {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
}

.gf-metric-value {
    font-size: 1.15rem;
    font-weight: 700;
    color: var(--accent-amber, #d9a441);
}

.gf-metric-gain {
    color: var(--accent-teal);
}

.gf-metric-label {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--font-color-secondary);
}

.gf-card-rows {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.15rem 0.6rem;
}

.gf-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.35rem;
    font-size: 0.78rem;
}

.gf-row-label {
    color: var(--font-color-secondary);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.03em;
}

.gf-row-value {
    font-variant-numeric: tabular-nums;
}

.gf-card-log {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.25rem 0.55rem;
    padding-top: 0.3rem;
    font-size: 0.72rem;
    border-top: 1px solid var(--border-color);
    color: var(--font-color-secondary);
}

.gf-log-count {
    color: var(--font-color);
}

.gf-log-today {
    padding: 0.05rem 0.35rem;
    color: var(--accent-amber, #d9a441);
    border: 1px solid var(--accent-amber, #d9a441);
    border-radius: 2px;
}

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>
