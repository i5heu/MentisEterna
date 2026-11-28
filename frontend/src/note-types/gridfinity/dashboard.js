/**
 * Gridfinity dashboard logic — pure, testable helpers for search, filter,
 * sort, and number formatting. GridfinityNoteType.vue wires these to its
 * local toolbar state; tests live in __tests__/dashboard.test.js.
 *
 * Item shape (produced by normalize.js from the server view):
 *   note_id, title, high_private, last_edited_at, last_wanted_at,
 *   wanted_count, wanted_days, last30_days, today_count,
 *   acquisition_cost, operating_expenses_per_month, disposal_costs,
 *   potential_profit_per_month, durability_years, space_requirements_m3,
 *   metrics { monthly_net, lifetime_months, total_cost, cost_per_month,
 *   payback_months }
 *
 * Unknown numbers are always null — they render as "—" and never
 * masquerade as zero.
 */

export const SORT_KEYS = [
    { value: "last_wanted_at", label: "Last wanted" },
    { value: "wanted_count", label: "Want count" },
    { value: "total_cost", label: "Total cost" },
    { value: "cost_per_month", label: "Cost / month" },
    { value: "monthly_net", label: "Monthly net" },
    { value: "acquisition_cost", label: "Acquisition" },
    { value: "durability_years", label: "Durability" },
    { value: "space_requirements_m3", label: "Space" },
    { value: "title", label: "Title" },
];

/** Numeric or null — empty strings and non-finite values are "unknown". */
export function toNumberOrNull(value) {
    if (value === null || value === undefined || value === "") return null;
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
}

/**
 * formatNumber renders costs and counts for the dashboard; null/undefined
 * render as "—" so unknown values stay visibly unknown.
 */
export function formatNumber(value, { decimals = 2 } = {}) {
    const n = toNumberOrNull(value);
    if (n === null) return "—";
    const rounded =
        Math.abs(n) >= 1000
            ? Math.round(n)
            : Math.round(n * 10 ** decimals) / 10 ** decimals;
    return rounded.toLocaleString("en-US", { maximumFractionDigits: decimals });
}

/** formatDate renders an ISO timestamp as a locale date; empty/invalid → "". */
export function formatDate(iso) {
    if (!iso) return "";
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString();
}

/** sortValue extracts the comparable value for a sort key (null = unknown). */
function sortValue(item, key) {
    switch (key) {
        case "last_wanted_at":
            return item.last_wanted_at || null;
        case "title":
            return item.title || "";
        case "wanted_count":
            return toNumberOrNull(item.wanted_count) ?? 0;
        case "total_cost":
            return toNumberOrNull(item.metrics?.total_cost);
        case "cost_per_month":
            return toNumberOrNull(item.metrics?.cost_per_month);
        case "monthly_net":
            return toNumberOrNull(item.metrics?.monthly_net);
        case "acquisition_cost":
            return toNumberOrNull(item.acquisition_cost);
        case "durability_years":
            return toNumberOrNull(item.durability_years);
        case "space_requirements_m3":
            return toNumberOrNull(item.space_requirements_m3);
        default:
            return null;
    }
}

/**
 * compareItems orders two items for one sort key. Unknown values (null)
 * always sort LAST, regardless of direction — they are the least
 * informative entries on the grid.
 */
function compareItems(a, b, key, dir) {
    if (key === "title") {
        const cmp = String(a.title || "").localeCompare(String(b.title || ""));
        return dir === "asc" ? cmp : -cmp;
    }
    const av = sortValue(a, key);
    const bv = sortValue(b, key);
    if (av === null && bv === null) return 0;
    if (av === null) return 1;
    if (bv === null) return -1;
    const cmp =
        typeof av === "string" ? av.localeCompare(String(bv)) : av - bv;
    return dir === "asc" ? cmp : -cmp;
}

/**
 * filterAndSortItems applies the dashboard toolbar: title search, the
 * total-cost ceiling, the quick filters (wanted today / last 30 days /
 * never wanted), and the sort key + direction. Returns a new array and
 * never mutates the input.
 */
export function filterAndSortItems(items, filters = {}) {
    const list = Array.isArray(items) ? items : [];
    const search = String(filters.search ?? "").trim().toLowerCase();
    const maxTotalCost = toNumberOrNull(filters.maxTotalCost);

    const result = list.filter((item) => {
        if (
            search &&
            !String(item.title || "").toLowerCase().includes(search)
        ) {
            return false;
        }
        if (maxTotalCost !== null) {
            // A cost ceiling hides wants whose total cost is unknown —
            // "affordable" must be established, not assumed.
            const total = toNumberOrNull(item.metrics?.total_cost);
            if (total === null || total > maxTotalCost) return false;
        }
        if (filters.wantedToday && !(Number(item.today_count) > 0)) {
            return false;
        }
        if (filters.last30 && !(Number(item.last30_days) > 0)) {
            return false;
        }
        if (filters.neverWanted && Number(item.wanted_count) !== 0) {
            return false;
        }
        return true;
    });

    const key = filters.sortKey || "last_wanted_at";
    const dir = filters.sortDir === "asc" ? "asc" : "desc";
    return result.sort((a, b) => compareItems(a, b, key, dir));
}

/**
 * filtersActive reports whether any toolbar filter is engaged — used to
 * show the "clear filters" affordance.
 */
export function filtersActive(filters = {}) {
    return Boolean(
        String(filters.search ?? "").trim() ||
            (filters.maxTotalCost !== "" && filters.maxTotalCost != null) ||
            filters.wantedToday ||
            filters.last30 ||
            filters.neverWanted,
    );
}
