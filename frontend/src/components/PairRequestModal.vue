<template>
    <div v-if="pendingPairRequests.length" class="modal-overlay">
        <div class="pair-modal">
            <div class="modal-header">
                <h2>Pairing request</h2>
            </div>
            <div class="modal-body">
                <template v-for="req in pendingPairRequests" :key="req.requestId">
                    <p class="pair-request-text">
                        <strong>{{ req.name }}</strong> wants to connect to this
                        device.
                    </p>
                    <div class="status-row">
                        <span class="status-label">Fingerprint</span>
                        <code class="status-value pair-fp">{{
                            shortFingerprint(req.id)
                        }}</code>
                    </div>
                    <p class="pair-hint">
                        Compare this fingerprint with the pairing code shown on
                        the other device. Accepting connects both devices both
                        ways.
                    </p>
                    <div class="pair-actions">
                        <button
                            class="btn-ghost"
                            @click="respond(req.requestId, false)"
                        >
                            Decline
                        </button>
                        <button
                            class="btn-primary"
                            @click="respond(req.requestId, true)"
                        >
                            Accept &amp; connect
                        </button>
                    </div>
                </template>
            </div>
        </div>
    </div>
</template>

<script setup>
import {
    pendingPairRequests,
    respondToPairRequest,
} from "../device/teleport.js";

function shortFingerprint(id) {
    if (!id) return "";
    return id.slice(0, 8) + "…" + id.slice(-8);
}

function respond(requestId, accepted) {
    respondToPairRequest(requestId, accepted).catch(() => {});
}
</script>

<style scoped>
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 2100;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
}

.pair-modal {
    background: var(--panel-bg, #061320);
    border: 1px solid var(--border-color, #7e7567);
    border-radius: 12px;
    width: 100%;
    max-width: 420px;
    box-shadow: 0 8px 30px var(--shadow-color, rgba(0, 0, 0, 0.6));
}

.modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border-color, #7e7567);
}

.modal-header h2 {
    font-size: 1.1rem;
    margin: 0;
}

.modal-body {
    padding: 16px 20px;
}

.pair-request-text {
    margin: 0 0 0.75rem;
}

.pair-fp {
    font-size: 0.85rem;
    word-break: break-all;
}

.pair-hint {
    margin: 0.75rem 0 1rem;
    font-size: 0.85rem;
    color: var(--font-color-secondary, #a5b0ad);
    line-height: 1.5;
}

.pair-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
}

.status-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    font-size: 0.85rem;
}

.status-label {
    color: var(--font-color-secondary, #a5b0ad);
}

.status-value {
    color: var(--font-color, #e0e8e4);
}
</style>
