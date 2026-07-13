import { ref } from "vue";

// Module-level singletons — one listener set per page lifetime.
let _initialized = false;
const wsConnected = ref(false);
const wsLatency = ref(null);

function onLiveStatus(event) {
    wsConnected.value = !!event.detail.connected;
    if (!event.detail.connected && !event.detail.connecting) {
        wsLatency.value = null;
    }
}

function onLiveLatency(event) {
    wsLatency.value = event.detail.ms;
}

export function useLiveStatus() {
    if (!_initialized) {
        _initialized = true;
        window.addEventListener("live:status", onLiveStatus);
        window.addEventListener("live:latency", onLiveLatency);
    }

    return {
        wsConnected,
        wsLatency,
    };
}
