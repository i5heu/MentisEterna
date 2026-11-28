/**
 * Tests for the Gridfinity dashboard's pure logic: number formatting,
 * search/filter/sort behavior, and the unknown-value semantics (null ≠ 0).
 */
import {
    SORT_KEYS,
    filterAndSortItems,
    filtersActive,
    formatNumber,
    toNumberOrNull,
} from "../dashboard.js";
import { normalizeGridfinityData } from "../normalize.js";

// Two wants with full economics, one partial, one fresh — the shapes the
// server view produces (see pkg/notetype/gridfinity).
const FULL_A = {
    note_id: 1,
    title: "Espresso Machine",
    high_private: false,
    last_edited_at: "2026-01-02T10:00:00.000Z",
    last_wanted_at: "2026-02-10T08:00:00.000Z",
    total: 5,
    wanted_days: 3,
    last30_days: 2,
    today_count: 1,
    acquisition_cost: 1000,
    operating_expenses_per_month: 10,
    disposal_costs: 50,
    potential_profit_per_month: 30,
    durability_years: 5,
    space_requirements_m3: 0.25,
    metrics: {
        monthly_net: -20,
        lifetime_months: 60,
        total_cost: -150,
        cost_per_month: -2.5,
        payback_months: 35,
    },
};
const FULL_B = {
    note_id: 2,
    title: "Laser Cutter",
    high_private: false,
    last_wanted_at: "2026-01-05T08:00:00.000Z",
    total: 2,
    wanted_days: 2,
    last30_days: 0,
    today_count: 0,
    acquisition_cost: 2500,
    operating_expenses_per_month: 40,
    disposal_costs: 100,
    potential_profit_per_month: 10,
    durability_years: 8,
    space_requirements_m3: 0.6,
    metrics: {
        monthly_net: 30,
        lifetime_months: 96,
        total_cost: 5480,
        cost_per_month: 57.08,
        payback_months: 260,
    },
};
const PARTIAL = {
    note_id: 3,
    title: "Standing Desk",
    total: 0,
    acquisition_cost: 500,
    // no operating/profit/durability → most metrics unknown
    metrics: {},
};
const FRESH = {
    note_id: 4,
    title: "Fresh Want",
    // no config row, no log entries
    metrics: {},
};

// The sorter's contract is the NORMALIZED item shape (see normalize.js),
// so the fixtures go through the same pipeline the component uses.
const ITEMS = normalizeGridfinityData({
    items: [FULL_A, FULL_B, PARTIAL, FRESH],
}).items;

describe("toNumberOrNull", () => {
    it("treats empty and non-finite values as unknown (null)", () => {
        expect(toNumberOrNull(null)).toBeNull();
        expect(toNumberOrNull(undefined)).toBeNull();
        expect(toNumberOrNull("")).toBeNull();
        expect(toNumberOrNull("abc")).toBeNull();
        expect(toNumberOrNull(Number.NaN)).toBeNull();
    });

    it("keeps finite numbers, including zero and numeric strings", () => {
        expect(toNumberOrNull(0)).toBe(0);
        expect(toNumberOrNull("42.5")).toBe(42.5);
        expect(toNumberOrNull(-3)).toBe(-3);
    });
});

describe("formatNumber", () => {
    it("renders unknown values as a dash, never as zero", () => {
        expect(formatNumber(null)).toBe("—");
        expect(formatNumber(undefined)).toBe("—");
        expect(formatNumber("")).toBe("—");
        expect(formatNumber("nope")).toBe("—");
    });

    it("formats known numbers with up to two decimals", () => {
        expect(formatNumber(0)).toBe("0");
        expect(formatNumber(-20)).toBe("-20");
        expect(formatNumber(0.25)).toBe("0.25");
        expect(formatNumber(1234.567)).toBe("1,235"); // >=1000 rounds
    });
});

describe("filterAndSortItems", () => {
    it("returns everything with no filters engaged", () => {
        expect(filterAndSortItems(ITEMS, {})).toHaveLength(4);
    });

    it("degrades gracefully for non-array input", () => {
        expect(filterAndSortItems(null, {})).toEqual([]);
        expect(filterAndSortItems(undefined, {})).toEqual([]);
    });

    it("searches titles case-insensitively", () => {
        const got = filterAndSortItems(ITEMS, { search: "  LASER " });
        expect(got.map((i) => i.note_id)).toEqual([2]);
    });

    it("a total-cost ceiling hides wants with unknown total cost", () => {
        // Cap between FULL_A (-150) and FULL_B (5480): only FULL_A passes;
        // PARTIAL and FRESH have unknown totals and must be hidden.
        const got = filterAndSortItems(ITEMS, { maxTotalCost: 1000 });
        expect(got.map((i) => i.note_id)).toEqual([1]);
    });

    it("quick filters select by urge-log activity", () => {
        const today = filterAndSortItems(ITEMS, { wantedToday: true });
        expect(today.map((i) => i.note_id)).toEqual([1]);

        const last30 = filterAndSortItems(ITEMS, { last30: true });
        expect(last30.map((i) => i.note_id)).toEqual([1]);

        const never = filterAndSortItems(ITEMS, { neverWanted: true });
        expect(never.map((i) => i.note_id)).toEqual([3, 4]);
    });

    it("combines search with quick filters", () => {
        const got = filterAndSortItems(ITEMS, {
            search: "want",
            neverWanted: true,
        });
        expect(got.map((i) => i.note_id)).toEqual([4]);
    });

    it("sorts numerically ascending and descending", () => {
        // Ascending acquisition: 500 (3) < 1000 (1) < 2500 (2), unknown (4) last.
        const asc = filterAndSortItems(ITEMS, {
            sortKey: "acquisition_cost",
            sortDir: "asc",
        });
        expect(asc.map((i) => i.note_id)).toEqual([3, 1, 2, 4]);

        const desc = filterAndSortItems(ITEMS, {
            sortKey: "acquisition_cost",
            sortDir: "desc",
        });
        expect(desc.map((i) => i.note_id)).toEqual([2, 1, 3, 4]);
    });

    it("always sorts unknown values last, regardless of direction", () => {
        // PARTIAL and FRESH have no total_cost.
        const desc = filterAndSortItems(ITEMS, {
            sortKey: "total_cost",
            sortDir: "desc",
        });
        expect(desc.map((i) => i.note_id)).toEqual([2, 1, 3, 4]);

        const asc = filterAndSortItems(ITEMS, {
            sortKey: "total_cost",
            sortDir: "asc",
        });
        expect(asc.map((i) => i.note_id)).toEqual([1, 2, 3, 4]);
    });

    it("sorts by want count and last-wanted date", () => {
        const byCount = filterAndSortItems(ITEMS, {
            sortKey: "wanted_count",
            sortDir: "desc",
        });
        expect(byCount.map((i) => i.note_id)).toEqual([1, 2, 3, 4]);

        const byLast = filterAndSortItems(ITEMS, {
            sortKey: "last_wanted_at",
            sortDir: "desc",
        });
        // FRESH has never been wanted → last.
        expect(byLast.map((i) => i.note_id)).toEqual([1, 2, 3, 4]);
    });

    it("sorts titles alphabetically in both directions", () => {
        const asc = filterAndSortItems(ITEMS, {
            sortKey: "title",
            sortDir: "asc",
        });
        expect(asc.map((i) => i.title)).toEqual([
            "Espresso Machine",
            "Fresh Want",
            "Laser Cutter",
            "Standing Desk",
        ]);
        const desc = filterAndSortItems(ITEMS, {
            sortKey: "title",
            sortDir: "desc",
        });
        expect(desc.map((i) => i.title)).toEqual([
            "Standing Desk",
            "Laser Cutter",
            "Fresh Want",
            "Espresso Machine",
        ]);
    });

    it("never mutates the input array", () => {
        const original = [...ITEMS];
        filterAndSortItems(ITEMS, {
            sortKey: "title",
            sortDir: "desc",
            search: "e",
        });
        expect(ITEMS).toEqual(original);
    });
});

describe("filtersActive", () => {
    it("is false only when nothing is engaged", () => {
        expect(filtersActive({})).toBe(false);
        expect(
            filtersActive({
                search: "",
                maxTotalCost: "",
                wantedToday: false,
                last30: false,
                neverWanted: false,
            }),
        ).toBe(false);
    });

    it("detects each filter individually", () => {
        expect(filtersActive({ search: "abc" })).toBe(true);
        expect(filtersActive({ maxTotalCost: 500 })).toBe(true);
        expect(filtersActive({ wantedToday: true })).toBe(true);
        expect(filtersActive({ last30: true })).toBe(true);
        expect(filtersActive({ neverWanted: true })).toBe(true);
    });
});

describe("SORT_KEYS", () => {
    it("only contains unique keys the sorter understands", () => {
        const values = SORT_KEYS.map((k) => k.value);
        expect(new Set(values).size).toBe(values.length);
        // Every key must produce a filter/sort result without throwing.
        for (const key of SORT_KEYS) {
            expect(() =>
                filterAndSortItems(ITEMS, { sortKey: key.value }),
            ).not.toThrow();
        }
    });
});

describe("normalizeGridfinityData", () => {
    it("degrades any input into the stable client shape", () => {
        expect(normalizeGridfinityData(null)).toEqual({
            items: [],
            totals: {
                wants: 0,
                wanted_today: 0,
                wanted_last30: 0,
                never_wanted: 0,
                sum_acquisition: null,
                sum_monthly_net: null,
                sum_total_cost: null,
                sum_space_m3: null,
            },
        });
    });

    it("maps server log counts onto wanted_count and keeps unknown costs null", () => {
        const data = normalizeGridfinityData({
            items: [FULL_A, PARTIAL],
            totals: { wants: 2, sum_total_cost: -150 },
        });
        expect(data.items[0].wanted_count).toBe(5);
        expect(data.items[0].today_count).toBe(1);
        expect(data.items[0].metrics.total_cost).toBe(-150);
        expect(data.items[1].metrics.total_cost).toBeNull();
        expect(data.items[1].acquisition_cost).toBe(500);
        expect(data.totals.sum_total_cost).toBe(-150);
        expect(data.totals.sum_monthly_net).toBeNull();
    });
});
