let liveSocket = null;
let liveReconnectTimer = null;
let liveReconnectAttempt = 0;
let liveConnectionDesired = false;
let livePingTimer = null;
let livePingSentAt = null;
let liveURL = "";

function post(type, detail) {
    self.postMessage({ type, detail });
}

function roundLatency(ms) {
    return Math.round(ms * 10) / 10;
}

function scheduleLiveReconnect() {
    if (!liveConnectionDesired || liveReconnectTimer) return;
    const delay = Math.min(1000 * 2 ** liveReconnectAttempt, 10000);
    liveReconnectAttempt += 1;
    liveReconnectTimer = self.setTimeout(() => {
        liveReconnectTimer = null;
        openLiveSocket();
    }, delay);
}

function openLiveSocket() {
    if (typeof WebSocket === "undefined" || !liveURL) {
        return;
    }
    if (
        liveSocket &&
        (liveSocket.readyState === WebSocket.OPEN ||
            liveSocket.readyState === WebSocket.CONNECTING)
    ) {
        return;
    }

    const socket = new WebSocket(liveURL);
    liveSocket = socket;
    post("status", { connected: false, connecting: true });

    socket.onopen = () => {
        if (liveSocket !== socket) {
            socket.close();
            return;
        }
        liveReconnectAttempt = 0;
        post("status", { connected: true, connecting: false });
        startLivePings(socket);
    };

    socket.onmessage = (event) => {
        try {
            const payload = JSON.parse(event.data);
            if (payload.type === "pong" && livePingSentAt !== null) {
                const rtt = performance.now() - livePingSentAt;
                livePingSentAt = null;
                post("latency", { ms: roundLatency(rtt) });
                return;
            }
            post("message", payload);
        } catch (error) {
            console.error("live worker message parse failed", error);
        }
    };

    socket.onerror = () => {
        socket.close();
    };

    socket.onclose = () => {
        if (liveSocket === socket) {
            liveSocket = null;
        }
        stopLivePings();
        post("status", { connected: false, connecting: false });
        scheduleLiveReconnect();
    };
}

function startLivePings(socket) {
    stopLivePings();
    livePingTimer = self.setInterval(() => {
        if (
            socket.readyState === WebSocket.OPEN &&
            livePingSentAt === null
        ) {
            livePingSentAt = performance.now();
            socket.send(JSON.stringify({ type: "ping" }));
        }
    }, 1000);
}

function stopLivePings() {
    livePingSentAt = null;
    if (livePingTimer) {
        self.clearInterval(livePingTimer);
        livePingTimer = null;
    }
}

function stopLiveSocket() {
    liveConnectionDesired = false;
    liveReconnectAttempt = 0;
    stopLivePings();
    if (liveReconnectTimer) {
        self.clearTimeout(liveReconnectTimer);
        liveReconnectTimer = null;
    }
    if (liveSocket) {
        const socket = liveSocket;
        liveSocket = null;
        socket.onopen = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        if (
            socket.readyState === WebSocket.OPEN ||
            socket.readyState === WebSocket.CONNECTING
        ) {
            socket.close();
        }
    }
    post("status", { connected: false, connecting: false });
}

self.addEventListener("message", (event) => {
    const data = event.data || {};
    if (data.type === "start") {
        liveURL = data.url || "";
        liveConnectionDesired = true;
        openLiveSocket();
        return;
    }
    if (data.type === "stop") {
        stopLiveSocket();
    }
});
