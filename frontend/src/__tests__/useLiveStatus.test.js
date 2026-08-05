import { jest } from "@jest/globals";

// Widget test for the useLiveStatus composable: a thin window-event →
// reactive-ref bridge. We mock `window` and dispatch synthetic `{ detail }`
// objects through the captured listeners — no DOM needed.
//
// `_initialized` is a module-level singleton, so each test gets a FRESH module
// via `jest.resetModules()` + lazy `require` (babel hoists static imports).

describe("useLiveStatus", () => {
    let listeners;

    function fresh() {
        listeners = {};
        global.window = {
            addEventListener: jest.fn((type, cb) => {
                listeners[type] = cb;
            }),
            removeEventListener: jest.fn(),
        };
        jest.resetModules();
        return require("../composables/useLiveStatus.js");
    }

    function dispatchLiveStatus(detail) {
        listeners["live:status"]({ detail });
    }

    function dispatchLiveLatency(detail) {
        listeners["live:latency"]({ detail });
    }

    test("registers live:status and live:latency listeners exactly once", () => {
        const { useLiveStatus } = fresh();
        useLiveStatus();
        useLiveStatus();
        useLiveStatus();

        expect(global.window.addEventListener).toHaveBeenCalledTimes(2);
        const types = global.window.addEventListener.mock.calls.map((c) => c[0]);
        expect(types).toEqual(["live:status", "live:latency"]);
    });

    test("live:status connected sets wsConnected", () => {
        const { useLiveStatus } = fresh();
        const { wsConnected } = useLiveStatus();

        dispatchLiveStatus({ connected: true, connecting: false });
        expect(wsConnected.value).toBe(true);
    });

    test("live:status disconnected clears latency details", () => {
        const { useLiveStatus } = fresh();
        const { wsConnected, wsLatency, wsLatencyDetail } = useLiveStatus();

        dispatchLiveLatency({ ms: 12.5, extra: 1 });
        expect(wsLatency.value).toBe(12.5);

        dispatchLiveStatus({ connected: false, connecting: false });
        expect(wsConnected.value).toBe(false);
        expect(wsLatency.value).toBeNull();
        expect(wsLatencyDetail.value).toBeNull();
    });

    test("live:latency sets wsLatency and wsLatencyDetail", () => {
        const { useLiveStatus } = fresh();
        const { wsLatency, wsLatencyDetail } = useLiveStatus();

        dispatchLiveLatency({ ms: 12.5, foo: "bar" });
        expect(wsLatency.value).toBe(12.5);
        expect(wsLatencyDetail.value).toEqual({ ms: 12.5, foo: "bar" });
    });
});
