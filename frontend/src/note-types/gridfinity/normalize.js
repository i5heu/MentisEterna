/**
 * Server-payload normalization for the gridfinity dashboard.
 *
 * The backend BuildView returns { items, totals } (see
 * pkg/notetype/gridfinity). This module converts it into a stable client
 * shape: counts default to 0, unknown cost/metric values become null,
 * and malformed entries degrade gracefully instead of throwing.
 */
import { toNumberOrNull } from "./dashboard.js";

/** count coerces a server count field to a non-negative integer. */
function count(value) {
    const n = Number(value);
    return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0;
}

/** str coerces to a display string. */
function str(value) {
    return typeof value === "string" ? value : "";
}

export function normalizeItem(raw) {
    const r = raw && typeof raw === "object" ? raw : {};
    return {
        note_id: Number(r.note_id) || 0,
        title: str(r.title) || "Untitled",
        high_private: !!r.high_private,
        last_edited_at: str(r.last_edited_at),
        last_wanted_at: str(r.last_wanted_at),
        wanted_count: count(r.total),
        wanted_days: count(r.wanted_days),
        last30_days: count(r.last30_days),
        today_count: count(r.today_count),
        acquisition_cost: toNumberOrNull(r.acquisition_cost),
        operating_expenses_per_month: toNumberOrNull(
            r.operating_expenses_per_month,
        ),
        disposal_costs: toNumberOrNull(r.disposal_costs),
        potential_profit_per_month: toNumberOrNull(
            r.potential_profit_per_month,
        ),
        durability_years: toNumberOrNull(r.durability_years),
        space_requirements_m3: toNumberOrNull(r.space_requirements_m3),
        metrics: {
            monthly_net: toNumberOrNull(r.metrics?.monthly_net),
            lifetime_months: toNumberOrNull(r.metrics?.lifetime_months),
            total_cost: toNumberOrNull(r.metrics?.total_cost),
            cost_per_month: toNumberOrNull(r.metrics?.cost_per_month),
            payback_months: toNumberOrNull(r.metrics?.payback_months),
        },
    };
}

export function normalizeTotals(raw) {
    const r = raw && typeof raw === "object" ? raw : {};
    return {
        wants: count(r.wants),
        wanted_today: count(r.wanted_today),
        wanted_last30: count(r.wanted_last30),
        never_wanted: count(r.never_wanted),
        sum_acquisition: toNumberOrNull(r.sum_acquisition),
        sum_monthly_net: toNumberOrNull(r.sum_monthly_net),
        sum_total_cost: toNumberOrNull(r.sum_total_cost),
        sum_space_m3: toNumberOrNull(r.sum_space_m3),
    };
}

/** normalizeGridfinityData converts a raw view payload (or anything else)
 * into the client shape used by the dashboard component. */
export function normalizeGridfinityData(raw) {
    const r = raw && typeof raw === "object" ? raw : {};
    return {
        items: Array.isArray(r.items) ? r.items.map(normalizeItem) : [],
        totals: normalizeTotals(r.totals),
    };
}
