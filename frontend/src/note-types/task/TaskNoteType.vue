<template>
    <div class="task-editor">
        <!-- Status -->
        <div class="task-field">
            <label class="task-label">Status</label>
            <select v-if="editing" v-model="localStatus" class="task-select">
                <option value="todo">To Do</option>
                <option value="in_progress">In Progress</option>
                <option value="done">Done</option>
            </select>
            <span
                v-else
                class="task-value status-badge"
                :class="'status-' + localStatus"
            >
                {{
                    localStatus === "todo"
                        ? "📋 To Do"
                        : localStatus === "in_progress"
                          ? "🔄 In Progress"
                          : "✅ Done"
                }}
            </span>
        </div>

        <!-- Quick status buttons (view mode only) -->
        <div v-if="!editing" class="quick-status-row">
            <button
                class="btn-ghost btn-sm quick-status-btn"
                :class="{ active: localStatus === 'todo' }"
                :disabled="setLoading"
                @click="quickSetStatus('todo')"
            >
                📋 Todo
            </button>
            <button
                class="btn-ghost btn-sm quick-status-btn"
                :class="{ active: localStatus === 'in_progress' }"
                :disabled="setLoading"
                @click="quickSetStatus('in_progress')"
            >
                🔄 Doing
            </button>
            <button
                class="btn-ghost btn-sm quick-status-btn"
                :class="{ active: localStatus === 'done' }"
                :disabled="setLoading"
                @click="quickSetStatus('done')"
            >
                ✅ Done
            </button>
            <span v-if="setError" class="quick-status-error">{{
                setError
            }}</span>
        </div>

        <!-- Daily generation inclusion -->
        <div class="task-field">
            <label class="task-label">Daily Task Generation</label>
            <label v-if="editing" class="task-checkbox-row">
                <input
                    v-model="localPendingDoesNotForceDailyInclusion"
                    type="checkbox"
                />
                <span>Don't auto-include this task while it is In Progress</span>
            </label>
            <span v-else class="task-value">
                {{
                    localPendingDoesNotForceDailyInclusion
                        ? "In-progress does not force daily inclusion"
                        : "In-progress will always be included in daily generation"
                }}
            </span>
        </div>

        <!-- Priority -->
        <div class="task-field">
            <label class="task-label">
                Priority: <strong>{{ localPriority }}</strong>
            </label>
            <input
                v-if="editing"
                type="range"
                v-model.number="localPriority"
                min="0"
                max="10"
                step="1"
                class="task-range"
            />
            <div v-else class="range-bar">
                <div
                    class="range-fill priority-fill"
                    :style="{ width: (localPriority / 10) * 100 + '%' }"
                ></div>
            </div>
        </div>

        <!-- Difficulty -->
        <div class="task-field">
            <label class="task-label">
                Difficulty: <strong>{{ localDifficulty }}</strong>
            </label>
            <input
                v-if="editing"
                type="range"
                v-model.number="localDifficulty"
                min="0"
                max="10"
                step="1"
                class="task-range"
            />
            <div v-else class="range-bar">
                <div
                    class="range-fill difficulty-fill"
                    :style="{ width: (localDifficulty / 10) * 100 + '%' }"
                ></div>
            </div>
        </div>

        <!-- Fun -->
        <div class="task-field">
            <label class="task-label">
                Fun: <strong :class="funClass">{{ localFun }}</strong>
            </label>
            <input
                v-if="editing"
                type="range"
                v-model.number="localFun"
                min="-5"
                max="5"
                step="1"
                class="task-range"
            />
            <div v-else class="range-bar fun-bar">
                <div
                    class="range-fill fun-fill"
                    :class="funClass"
                    :style="funBarStyle"
                ></div>
            </div>
        </div>

        <!-- Description -->
        <div class="task-field">
            <label class="task-label">Description</label>
            <textarea
                v-if="editing"
                v-model="localDescription"
                rows="4"
                class="task-textarea"
                placeholder="Detailed task description..."
            ></textarea>
            <p v-else class="task-value desc-value">
                {{ localDescription || "—" }}
            </p>
        </div>

        <!-- Due Date -->
        <div class="task-field">
            <label class="task-label">Due Date</label>
            <input
                v-if="editing"
                type="date"
                v-model="localDueDate"
                class="task-date"
            />
            <span v-else class="task-value">{{ localDueDate || "—" }}</span>
        </div>

        <!-- Time Estimation -->
        <div class="task-field">
            <label class="task-label">Time Estimation</label>
            <input
                v-if="editing"
                v-model="localTimeEstimation"
                placeholder="e.g. 2h, 30m, 1d"
                class="task-text"
            />
            <span v-else class="task-value">{{
                localTimeEstimation || "—"
            }}</span>
        </div>

        <!-- Time Used -->
        <div class="task-field">
            <label class="task-label">Time Used</label>
            <input
                v-if="editing"
                v-model="localTimeUsed"
                placeholder="e.g. 1h30m"
                class="task-text"
            />
            <span v-else class="task-value">{{ localTimeUsed || "—" }}</span>
        </div>

        <!-- Recurring -->
        <div class="task-field">
            <label class="task-label">Recurring</label>
            <select v-if="editing" v-model="localRecurring" class="task-select">
                <option value="none">None</option>
                <option value="daily">Daily</option>
                <option value="weekly">Weekly</option>
                <option value="monthly">Monthly</option>
                <option value="custom">Custom (days)</option>
            </select>
            <span v-else class="task-value">
                {{ localRecurring === "none" ? "—" : localRecurring }}
                <template v-if="localRecurring === 'custom'">
                    (every {{ localRecurringDays }} day{{
                        localRecurringDays !== 1 ? "s" : ""
                    }})</template
                >
            </span>
        </div>

        <!-- Custom Recurring Days -->
        <div v-if="localRecurring === 'custom'" class="task-field">
            <label class="task-label">Recurring Interval (days)</label>
            <input
                v-if="editing"
                type="number"
                v-model.number="localRecurringDays"
                min="1"
                class="task-text task-number"
            />
            <span v-else class="task-value"
                >Every {{ localRecurringDays }} day{{
                    localRecurringDays !== 1 ? "s" : ""
                }}</span
            >
        </div>

        <!-- Completed At (read-only) -->
        <div v-if="localCompletedAt" class="task-field">
            <label class="task-label">Completed</label>
            <span class="task-value completed-date">{{
                formatDate(localCompletedAt)
            }}</span>
        </div>
    </div>
</template>

<script setup>
import { ref, watch, computed, onBeforeUnmount } from "vue";
import { usePluginAction } from "../shared/usePluginAction.js";
import { useTaskEventBus } from "../shared/useTaskEventBus.js";

const props = defineProps({
    note: { type: Object, default: null },
    token: { type: String, required: true },
    editing: { type: Boolean, default: false },
    customData: { type: Object, default: null },
    uiSchema: { type: Object, default: null },
});

const emit = defineEmits(["selectNote", "update:customData"]);

const {
    loading: setLoading,
    error: setError,
    execute: execSetStatus,
} = usePluginAction(() => props.token);

const { emitStatusChange, onStatusChange } = useTaskEventBus();

// Local copies
const localStatus = ref("todo");
const localDifficulty = ref(0);
const localFun = ref(0);
const localPriority = ref(0);
const localDescription = ref("");
const localDueDate = ref("");
const localTimeEstimation = ref("");
const localTimeUsed = ref("");
const localRecurring = ref("none");
const localRecurringDays = ref(0);
const localCompletedAt = ref("");
const localPendingDoesNotForceDailyInclusion = ref(false);

let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData || {};
    localStatus.value = cd.status || "todo";
    localDifficulty.value = cd.difficulty ?? 0;
    localFun.value = cd.fun ?? 0;
    localPriority.value = cd.priority ?? 0;
    localDescription.value = cd.description || "";
    localDueDate.value = cd.due_date || "";
    localTimeEstimation.value = cd.time_estimation || "";
    localTimeUsed.value = cd.time_used || "";
    localRecurring.value = cd.recurring || "none";
    localRecurringDays.value = cd.recurring_days ?? 0;
    localCompletedAt.value = cd.completed_at || "";
    localPendingDoesNotForceDailyInclusion.value =
        cd.pending_does_not_force_daily_inclusion ?? false;
    hydrating = false;
}

// Hydrate on note identity change.
watch(() => props.note?.id, hydrateFromProp, { immediate: true });

// Also hydrate if customData arrives after the note.
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (cd && (cd.status || cd.description)) {
            hydrateFromProp();
        }
    },
);

// Emit custom data on change.
function emitCustomData() {
    emit("update:customData", {
        status: localStatus.value,
        difficulty: localDifficulty.value,
        fun: localFun.value,
        priority: localPriority.value,
        description: localDescription.value,
        due_date: localDueDate.value,
        time_estimation: localTimeEstimation.value,
        time_used: localTimeUsed.value,
        recurring: localRecurring.value,
        recurring_days: localRecurringDays.value,
        completed_at: localCompletedAt.value,
        pending_does_not_force_daily_inclusion:
            localPendingDoesNotForceDailyInclusion.value,
    });
}

watch(
    [
        localStatus,
        localDifficulty,
        localFun,
        localPriority,
        localDescription,
        localDueDate,
        localTimeEstimation,
        localTimeUsed,
        localRecurring,
        localRecurringDays,
        localPendingDoesNotForceDailyInclusion,
    ],
    emitCustomData,
    { deep: false },
);

// Computed styles
const funClass = computed(() => {
    if (localFun.value > 0) return "fun-positive";
    if (localFun.value < 0) return "fun-negative";
    return "fun-neutral";
});

const funBarStyle = computed(() => {
    const val = localFun.value;
    // Fun scale: -5 to 5, map to 0%-100%
    const pct = ((val + 5) / 10) * 100;
    return { width: pct + "%" };
});

function formatDate(dateStr) {
    if (!dateStr) return "—";
    try {
        return new Date(dateStr).toLocaleDateString(undefined, {
            year: "numeric",
            month: "short",
            day: "numeric",
        });
    } catch {
        return dateStr;
    }
}

async function quickSetStatus(status) {
    if (!props.note?.id) return;
    const noteId = props.note.id;
    try {
        const result = await execSetStatus(props.note.id, "set_status", {
            status,
        });
        // Optimistically update local state.
        localStatus.value = status;
        if (status === "done" && result?.completed_at) {
            localCompletedAt.value = result.completed_at;
        } else if (status !== "done") {
            localCompletedAt.value = "";
        }
        // Emit the full updated config so the parent syncs on save.
        emitCustomData();
        // Broadcast to all other task components so they refresh.
        emitStatusChange(noteId, status);
    } catch {
        // error is already captured in setError
    }
}

// Listen for external status changes so we stay in sync when, e.g.,
// the user changes status from the task overview or thread sidebar.
const unsubStatus = onStatusChange((noteId, status) => {
    if (noteId !== props.note?.id) return;
    localStatus.value = status;
    if (status === "done") {
        // completed_at is harder to know remotely; just clear and let
        // the next full data load populate it.
        localCompletedAt.value = new Date().toISOString();
    } else if (localCompletedAt.value !== "") {
        localCompletedAt.value = "";
    }
    emitCustomData();
});

onBeforeUnmount(unsubStatus);
</script>

<style scoped>
.task-editor {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
}

.task-field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
}

.task-label {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--font-color-secondary);
}

.task-value {
    font-size: 0.9rem;
    color: var(--font-color);
}

.task-textarea {
    width: 100%;
    padding: 0.4rem 0.5rem;
    font-size: 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    resize: vertical;
    font-family: inherit;
}

.task-textarea:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.task-text {
    width: 100%;
    max-width: 16rem;
    padding: 0.3rem 0.5rem;
    font-size: 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
}

.task-text:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.task-checkbox-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.9rem;
    color: var(--font-color);
}

.task-number {
    max-width: 6rem;
}

.task-date {
    width: 100%;
    max-width: 12rem;
    padding: 0.3rem 0.5rem;
    font-size: 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
}

.task-date:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.task-select {
    width: 100%;
    max-width: 14rem;
    padding: 0.3rem 0.5rem;
    font-size: 0.9rem;
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--raised-bg);
}

.task-select:focus {
    border-color: var(--accent-teal);
    outline: none;
}

/* Quick status buttons */
.quick-status-row {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
    align-items: center;
    margin-top: 0.3rem;
}

.quick-status-btn {
    padding: 0.25rem 0.6rem;
    font-size: 0.78rem;
    border-radius: 4px;
    transition: all 0.15s;
}

.quick-status-btn.active {
    background: var(--accent-teal);
    color: var(--font-color);
    border-color: var(--accent-teal);
}

.quick-status-btn:hover:not(:disabled):not(.active) {
    background: var(--panel-bg);
    color: var(--font-color);
}

.quick-status-error {
    font-size: 0.75rem;
    color: var(--heading-color);
    margin-left: 0.5rem;
}

.task-range {
    width: 100%;
    max-width: 16rem;
    accent-color: var(--accent-teal);
}

.range-bar {
    width: 100%;
    max-width: 16rem;
    height: 6px;
    background: var(--border-color);
    border-radius: 3px;
    overflow: hidden;
}

.range-fill {
    height: 100%;
    border-radius: 3px;
    transition: width 0.2s ease;
}

.priority-fill {
    background: var(--heading-color);
}

.difficulty-fill {
    background: var(--header-title-color);
}

.fun-bar {
    background: linear-gradient(
        to right,
        var(--heading-color),
        var(--font-color-secondary),
        var(--accent-teal)
    );
}

.fun-fill {
    background: transparent;
}

.status-badge {
    display: inline-block;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    font-size: 0.85rem;
    font-weight: 600;
}

.fun-positive {
    color: var(--accent-teal);
}

.fun-negative {
    color: var(--heading-color);
}

.fun-neutral {
    color: var(--font-color-secondary);
}

.desc-value {
    white-space: pre-wrap;
    line-height: 1.5;
}

.completed-date {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    font-style: italic;
}
</style>

<style>
@import "./status-colors.css";
</style>
