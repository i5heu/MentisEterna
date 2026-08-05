import { jest } from "@jest/globals";

// Pseudo-integration test for api.js live streaming (startLiveUpdates /
// stopLiveUpdates), driving the real module through a fake `WebSocket` boundary.
// We deliberately do NOT define global.Worker, so ensureLiveWorker() returns
// null and startLiveUpdates falls back to the in-module WebSocket path
// (openLiveSocket + ping/retry state machine) — that is the branch under test.
//
// The live state (liveWorker/liveSocket/timers) lives at api.js module scope,
// so each test gets a FRESH module via jest.resetModules() + lazy `require`.

// Fake WebSocket: captures instances, exposes send/close and the event slots
// the module assigns (onopen/onmessage/onclose/onerror).
class FakeWebSocket {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSING = 2;
    static CLOSED = 3;
    constructor(url) {
        this.url = url;
        this.readyState = FakeWebSocket.CONNECTING;
        this.send = jest.fn();
        this.close = jest.fn(() => {
            this.readyState = FakeWebSocket.CLOSED;
        });
        this.onopen = null;
        this.onmessage = null;
        this.onclose = null;
        this.onerror = null;
        FakeWebSocket.instances.push(this);
    }
}
FakeWebSocket.instances = [];
global.WebSocket = FakeWebSocket;

// window provides location (for buildLiveURL), dispatchEvent (for live:* events)
// and the timer functions the ping/reconnect state machine schedules through.
global.window = {
    location: { protocol: "http:", host: "localhost:8080" },
    dispatchEvent: jest.fn(),
    setInterval: (...a) => global.setInterval(...a),
    clearInterval: (...a) => global.clearInterval(...a),
    setTimeout: (...a) => global.setTimeout(...a),
    clearTimeout: (...a) => global.clearTimeout(...a),
};

function freshApi() {
    jest.resetModules();
    return require("../api.js");
}

// Find the passed detail for a given live:* event type, or undefined.
function eventDetail(type) {
    const call = global.window.dispatchEvent.mock.calls.findLast(
        ([ev]) => ev.type === type,
    );
    return call ? call[0].detail : undefined;
}

function eventCount(type) {
    return global.window.dispatchEvent.mock.calls.filter(
        ([ev]) => ev.type === type,
    ).length;
}

beforeEach(() => {
    jest.useFakeTimers({ doNotFake: ["performance", "Date"] });
    FakeWebSocket.instances.length = 0;
});
afterEach(() => {
    jest.useRealTimers();
});

test("startLiveUpdates opens a WebSocket at buildLiveURL() and signals connecting", () => {
    const api = freshApi();
    api.startLiveUpdates();

    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(FakeWebSocket.instances[0].url).toBe("ws://localhost:8080/ws");
    expect(eventDetail("live:status")).toEqual({
        connected: false,
        connecting: true,
    });
});

test("buildLiveURL uses wss for an https origin", () => {
    global.window.location.protocol = "https:";
    global.window.location.host = "example.com";
    const api = freshApi();
    api.startLiveUpdates();
    expect(FakeWebSocket.instances[0].url).toBe("wss://example.com/ws");
});

test("onopen dispatches connected status and starts sending pings", () => {
    const api = freshApi();
    api.startLiveUpdates();
    const socket = FakeWebSocket.instances[0];
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen();

    expect(eventDetail("live:status")).toEqual({
        connected: true,
        connecting: false,
    });

    jest.advanceTimersByTime(1000);
    expect(socket.send).toHaveBeenCalled();
    const ping = JSON.parse(socket.send.mock.calls[0][0]);
    expect(ping.type).toBe("ping");
    expect(typeof ping.client_sent_at_ms).toBe("number");
});

test("pong builds the rounded latency detail and dispatches live:latency", () => {
    const api = freshApi();
    api.startLiveUpdates();
    const socket = FakeWebSocket.instances[0];
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen();

    // Send a ping to fix client_sent_at_ms (matches a real ping round-trip).
    jest.advanceTimersByTime(1000);
    const clientSentAtMs = JSON.parse(socket.send.mock.calls[0][0])
        .client_sent_at_ms;

    socket.onmessage({
        data: JSON.stringify({
            type: "pong",
            client_sent_at_ms: clientSentAtMs,
            server_received_at_us: 1000,
            server_sent_at_us: 2500,
        }),
    });

    const detail = eventDetail("live:latency");
    expect(detail).toBeTruthy();
    expect(detail.clientSentAtMs).toBe(clientSentAtMs);
    // roundLatency → 10th-precision, finite elapsed ms.
    expect(typeof detail.ms).toBe("number");
    expect(Math.round(detail.ms * 10) / 10).toBe(detail.ms);
    // buildLatencyDetail server-side merge (server_sent > server_received).
    expect(detail.serverReceivedAtUs).toBe(1000);
    expect(detail.serverSentAtUs).toBe(2500);
    expect(detail.serverProcessingMs).toBe(
        Math.round(((2500 - 1000) / 1000) * 10) / 10,
    );
});

test("a pong without a client_sent_at_ms reference dispatches no latency", () => {
    const api = freshApi();
    api.startLiveUpdates();
    const socket = FakeWebSocket.instances[0];
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen();

    socket.onmessage({
        data: JSON.stringify({ type: "pong", server_sent_at_us: 2000 }),
    });
    expect(eventDetail("live:latency")).toBeUndefined();
});

test("onclose clears pings, dispatches disconnected, and schedules a reconnect", () => {
    const api = freshApi();
    api.startLiveUpdates();
    const socket = FakeWebSocket.instances[0];
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen();

    jest.advanceTimersByTime(1000); // one ping scheduled

    socket.onclose();
    expect(eventDetail("live:status")).toEqual({
        connected: false,
        connecting: false,
    });

    // Ping interval cleared: advancing timers must not send more pings.
    const sentBefore = socket.send.mock.calls.length;
    jest.advanceTimersByTime(3000);
    expect(socket.send.mock.calls.length).toBe(sentBefore);

    // Reconnect scheduled (liveConnectionDesired still true): opens a 2nd socket.
    jest.advanceTimersByTime(1000);
    expect(FakeWebSocket.instances).toHaveLength(2);
});

test("stopLiveUpdates closes the socket, stops pings, and cancels reconnect", () => {
    const api = freshApi();
    api.startLiveUpdates();
    const socket = FakeWebSocket.instances[0];
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen();

    api.stopLiveUpdates();

    expect(socket.close).toHaveBeenCalled();
    expect(eventDetail("live:status")).toEqual({
        connected: false,
        connecting: false,
    });

    // liveConnectionDesired now false → no reconnect, no new sockets.
    jest.advanceTimersByTime(10000);
    expect(FakeWebSocket.instances).toHaveLength(1);
});
