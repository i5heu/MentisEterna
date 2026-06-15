<template>
    <div class="overview-dashboard">
        <!-- Stats -->
        <div class="stats-row">
            <div class="stat-card">
                <span class="stat-number">{{
                    viewData.stats?.total || 0
                }}</span>
                <span class="stat-label">Total</span>
            </div>
            <div class="stat-card stat-todo">
                <span class="stat-number">{{ viewData.stats?.todo || 0 }}</span>
                <span class="stat-label">To Do</span>
            </div>
            <div class="stat-card stat-progress">
                <span class="stat-number">{{
                    viewData.stats?.in_progress || 0
                }}</span>
                <span class="stat-label">In Progress</span>
            </div>
            <div class="stat-card stat-done">
                <span class="stat-number">{{ viewData.stats?.done || 0 }}</span>
                <span class="stat-label">Done</span>
            </div>
            <div
                class="stat-card stat-overdue"
                v-if="viewData.stats?.overdue > 0"
            >
                <span class="stat-number">{{ viewData.stats?.overdue }}</span>
                <span class="stat-label">Overdue</span>
            </div>
        </div>

        <!-- Averages -->
        <div class="averages">
            <span v-if="viewData.stats?.avg_priority">
                Avg Priority: {{ viewData.stats.avg_priority.toFixed(1) }}
            </span>
            <span v-if="viewData.stats?.avg_difficulty">
                Avg Difficulty: {{ viewData.stats.avg_difficulty.toFixed(1) }}
            </span>
            <span v-if="viewData.stats?.avg_fun">
                Avg Fun: {{ viewData.stats.avg_fun.toFixed(1) }}
            </span>
            <span v-if="viewData.stats?.total_time_used">
                Total Time: {{ viewData.stats.total_time_used }}
            </span>
        </div>

        <!-- Daily Tasks -->
        <div class="section">
            <h4>🎯 Daily Tasks (3 Random Picks)</h4>
            <button
                class="btn-ghost btn-sm"
                @click="generateDailyTasks"
                :disabled="loadingDaily"
            >
                {{ loadingDaily ? "Loading..." : "🔄 Refresh" }}
            </button>
            <div v-if="dailyTasks.length === 0" class="empty-hint">
                No tasks available. Create some tasks first!
            </div>
            <div v-else class="daily-tasks-grid">
                <div
                    v-for="t in dailyTasks"
                    :key="t.note_id"
                    class="daily-task-card"
                    @click="$emit('selectNote', t.note_id)"
                >
                    <div class="daily-task-title">
                        {{ t.title || "Untitled" }}
                    </div>
                    <div class="daily-task-meta">
                        <span :class="'status-dot status-' + t.status"></span>
                        <span
                            >P{{ t.priority }} D{{ t.difficulty }} F{{
                                t.fun > 0 ? "+" : ""
                            }}{{ t.fun }}</span
                        >
                        <span v-if="t.due_date">📅 {{ t.due_date }}</span>
                    </div>
                    <div v-if="t.status !== 'done'" class="daily-quick-actions">
                        <button
                            class="btn-ghost btn-sm overview-action-btn"
                            :disabled="statusLoading[t.note_id]"
                            @click.stop="
                                quickSetStatus(t.note_id, 'in_progress')
                            "
                            title="Set Doing"
                        >
                            ▶ Doing
                        </button>
                        <button
                            class="btn-ghost btn-sm overview-action-btn"
                            :disabled="statusLoading[t.note_id]"
                            @click.stop="quickSetStatus(t.note_id, 'done')"
                            title="Set Done"
                        >
                            ✓ Done
                        </button>
                    </div>
                </div>
            </div>
        </div>

        <!-- All Tasks with Filters (collapsible) -->
        <div class="section">
            <h4 class="collapsible-header" @click="tasksOpen = !tasksOpen">
                <span class="collapse-arrow">{{ tasksOpen ? "▼" : "▶" }}</span>
                📋 All Tasks ({{ filteredTasks.length }})
            </h4>
            <div v-if="tasksOpen">
                <div class="filters">
                    <select v-model="filterStatus" class="filter-select">
                        <option value="">All Statuses</option>
                        <option value="todo">To Do</option>
                        <option value="in_progress">In Progress</option>
                        <option value="done">Done</option>
                    </select>
                    <input
                        v-model="filterText"
                        type="text"
                        placeholder="Filter by title..."
                        class="filter-text"
                    />
                    <label class="sort-label">
                        Sort:
                        <select
                            v-model="sortBy"
                            class="filter-select filter-sort"
                        >
                            <option value="priority">Priority</option>
                            <option value="due_date">Due Date</option>
                            <option value="difficulty">Difficulty</option>
                            <option value="fun">Fun</option>
                            <option value="title">Title</option>
                        </select>
                    </label>
                </div>
                <div v-if="filteredTasks.length === 0" class="empty-hint">
                    No tasks match your filters.
                </div>
                <div v-else class="task-list">
                    <div
                        v-for="t in filteredTasks"
                        :key="t.note_id"
                        class="task-row"
                        @click="$emit('selectNote', t.note_id)"
                    >
                        <span :class="'status-dot status-' + t.status"></span>
                        <span class="task-row-title">{{
                            t.title || "Untitled"
                        }}</span>
                        <span class="task-row-meta">
                            <span>P{{ t.priority }}</span>
                            <span>D{{ t.difficulty }}</span>
                            <span
                                :class="
                                    t.fun > 0
                                        ? 'fun-positive'
                                        : t.fun < 0
                                          ? 'fun-negative'
                                          : 'fun-neutral'
                                "
                            >
                                F{{ t.fun > 0 ? "+" : "" }}{{ t.fun }}
                            </span>
                            <span v-if="t.due_date" class="task-row-due"
                                >📅 {{ t.due_date }}</span
                            >
                            <span v-if="t.time_estimation"
                                >⏱ {{ t.time_estimation }}</span
                            >
                        </span>
                        <span
                            v-if="t.recurring !== 'none'"
                            class="recurring-badge"
                            >🔄 {{ t.recurring }}</span
                        >
                        <span
                            v-if="t.status !== 'done'"
                            class="overview-quick-actions"
                        >
                            <button
                                class="btn-ghost btn-sm overview-action-btn"
                                :disabled="statusLoading[t.note_id]"
                                @click.stop="
                                    quickSetStatus(t.note_id, 'in_progress')
                                "
                                title="Set Doing"
                            >
                                ▶
                            </button>
                            <button
                                class="btn-ghost btn-sm overview-action-btn"
                                :disabled="statusLoading[t.note_id]"
                                @click.stop="quickSetStatus(t.note_id, 'done')"
                                title="Set Done"
                            >
                                ✓
                            </button>
                        </span>
                    </div>
                </div>
            </div>
        </div>

        <!-- Daily History -->
        <div class="section" v-if="dailyHistory.length > 0">
            <h4 class="collapsible-header" @click="historyOpen = !historyOpen">
                <span class="collapse-arrow">{{
                    historyOpen ? "▼" : "▶"
                }}</span>
                📜 Daily History ({{ dailyHistory.length }})
            </h4>
            <div v-if="historyOpen" class="history-list">
                <div
                    v-for="entry in dailyHistory"
                    :key="entry.generated_at"
                    class="history-day"
                >
                    <div class="history-date">
                        {{ formatHistoryDate(entry.generated_at) }}
                    </div>
                    <div class="history-tasks">
                        <span
                            v-for="t in entry.tasks"
                            :key="t.note_id"
                            class="history-task"
                            :class="'status-' + t.status"
                            @click="$emit('selectNote', t.note_id)"
                            :title="t.title"
                        >
                            {{ t.status === "done" ? "✅" : "🔲" }}
                            {{ t.title || "Untitled" }}
                        </span>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<script setup>
import { ref, computed, watch, onBeforeUnmount } from "vue";
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

const { loading: loadingDaily, execute: execDailyTasks } = usePluginAction(
    () => props.token,
);

// Separate plugin action for quick status changes.
const { execute: execQuickStatus } = usePluginAction(() => props.token);
const statusLoading = ref({});

const { emitStatusChange, onStatusChange } = useTaskEventBus();

// View data from the server (BuildView result)
const viewData = ref({
    tasks: [],
    daily_tasks: [],
    daily_history: [],
    stats: {},
});

let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    if (cd && typeof cd === "object") {
        viewData.value = {
            tasks: Array.isArray(cd.tasks) ? cd.tasks : [],
            daily_tasks: Array.isArray(cd.daily_tasks) ? cd.daily_tasks : [],
            daily_history: Array.isArray(cd.daily_history)
                ? cd.daily_history
                : [],
            stats: cd.stats || {},
        };
    }
    hydrating = false;
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (cd && cd.tasks) {
            hydrateFromProp();
        }
    },
);

// Filters
const filterStatus = ref("");
const filterText = ref("");
const sortBy = ref("priority");

const filteredTasks = computed(() => {
    let tasks = viewData.value.tasks || [];

    if (filterStatus.value) {
        tasks = tasks.filter((t) => t.status === filterStatus.value);
    }
    if (filterText.value) {
        const q = filterText.value.toLowerCase();
        tasks = tasks.filter((t) => (t.title || "").toLowerCase().includes(q));
    }

    const sorted = [...tasks];
    sorted.sort((a, b) => {
        switch (sortBy.value) {
            case "priority":
                return (b.priority || 0) - (a.priority || 0);
            case "due_date":
                const da = a.due_date || "9999";
                const db = b.due_date || "9999";
                return da.localeCompare(db);
            case "difficulty":
                return (b.difficulty || 0) - (a.difficulty || 0);
            case "fun":
                return (b.fun || 0) - (a.fun || 0);
            case "title":
                return (a.title || "").localeCompare(b.title || "");
            default:
                return 0;
        }
    });
    return sorted;
});

// Daily tasks
const dailyTasks = computed(() => viewData.value.daily_tasks || []);

// Collapse state
const tasksOpen = ref(false);

// Daily history
const dailyHistory = computed(() => viewData.value.daily_history || []);
const historyOpen = ref(false);

function formatHistoryDate(isoStr) {
    if (!isoStr) return "";
    try {
        const d = new Date(isoStr + (isoStr.includes("Z") ? "" : "Z"));
        const now = new Date();
        const today = now.toISOString().slice(0, 10);
        const datePart = isoStr.slice(0, 10);
        const timePart = d.toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
            timeZone: "UTC",
        });

        const fmt = d.toLocaleDateString(undefined, {
            weekday: "short",
            month: "short",
            day: "numeric",
            timeZone: "UTC",
        });

        if (datePart === today) return `Today ${timePart} (${fmt})`;

        const yesterday = new Date(now);
        yesterday.setDate(yesterday.getDate() - 1);
        if (datePart === yesterday.toISOString().slice(0, 10))
            return `Yesterday ${timePart} (${fmt})`;

        return `${fmt} ${timePart}`;
    } catch {
        return isoStr;
    }
}

async function generateDailyTasks() {
    try {
        const result = await execDailyTasks(props.note.id, "daily_tasks", {
            count: 3,
        });
        if (result && result.daily_tasks) {
            viewData.value.daily_tasks = result.daily_tasks;
        }
    } catch {
        // keep current daily tasks
    }
}

async function quickSetStatus(taskNoteId, status) {
    statusLoading.value = { ...statusLoading.value, [taskNoteId]: true };
    try {
        await execQuickStatus(props.note.id, "quick_set_status", {
            task_note_id: taskNoteId,
            status,
        });
        // Optimistically update the task in the local view.
        const tasks = viewData.value.tasks || [];
        const idx = tasks.findIndex((t) => t.note_id === taskNoteId);
        if (idx >= 0) {
            tasks[idx] = { ...tasks[idx], status };
            viewData.value = { ...viewData.value, tasks: [...tasks] };
        }
        // Also update daily_tasks if the changed task is in there.
        const dailies = viewData.value.daily_tasks || [];
        const didx = dailies.findIndex((t) => t.note_id === taskNoteId);
        if (didx >= 0) {
            dailies[didx] = { ...dailies[didx], status };
            viewData.value = { ...viewData.value, daily_tasks: [...dailies] };
        }
        // Broadcast so other components refresh.
        emitStatusChange(taskNoteId, status);
    } catch {
        // Error handled silently
    } finally {
        statusLoading.value = { ...statusLoading.value, [taskNoteId]: false };
    }
}

// Listen for external status changes so we stay in sync when, e.g.,
// the user changes status from a TaskNoteType or the thread sidebar.
const unsubOverview = onStatusChange((noteId, status) => {
    let changed = false;
    const tasks = viewData.value.tasks || [];
    const idx = tasks.findIndex((t) => t.note_id === noteId);
    if (idx >= 0) {
        tasks[idx] = { ...tasks[idx], status };
        changed = true;
    }
    const dailies = viewData.value.daily_tasks || [];
    const didx = dailies.findIndex((t) => t.note_id === noteId);
    if (didx >= 0) {
        dailies[didx] = { ...dailies[didx], status };
        changed = true;
    }
    if (changed) {
        viewData.value = {
            ...viewData.value,
            tasks: tasks === viewData.value.tasks ? [...tasks] : tasks,
            daily_tasks:
                dailies === viewData.value.daily_tasks ? [...dailies] : dailies,
        };
    }
});

onBeforeUnmount(unsubOverview);
</script>

<style scoped>
.overview-dashboard {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
}

.stats-row {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
}

.stat-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.75rem 1.25rem;
    border-radius: 8px;
    background: var(--raised-bg);
    border: 1px solid var(--border-color);
    min-width: 5rem;
}

.stat-number {
    font-size: 1.5rem;
    font-weight: 700;
    color: var(--font-color);
}

.stat-label {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.05em;
}

.stat-todo {
    border-left: 3px solid #e8e8e8;
}

.stat-progress {
    border-left: 3px solid #22c55e;
}

.stat-done {
    border-left: 3px solid #3b82f6;
}

.stat-overdue {
    border-left: 3px solid var(--heading-color);
}

.averages {
    display: flex;
    gap: 1.5rem;
    flex-wrap: wrap;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.section h4 {
    font-size: 1rem;
    margin: 0;
    color: var(--font-color);
}

.collapsible-header {
    cursor: pointer;
    user-select: none;
}

.collapsible-header:hover {
    color: var(--accent-teal);
}

.collapse-arrow {
    font-size: 0.7rem;
    margin-right: 0.3rem;
    vertical-align: middle;
}

.daily-tasks-grid {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
}

.daily-task-card {
    padding: 0.75rem 1rem;
    border: 1px solid var(--border-color);
    border-radius: 8px;
    cursor: pointer;
    background: var(--raised-bg);
    min-width: 12rem;
    flex: 1;
    transition:
        border-color 0.15s,
        background 0.15s;
}

.daily-quick-actions {
    display: flex;
    gap: 0.25rem;
    margin-top: 0.5rem;
}

.daily-quick-actions .overview-action-btn {
    padding: 0.2rem 0.5rem;
    font-size: 0.72rem;
}

.daily-quick-actions .overview-action-btn:hover:not(:disabled) {
    background: var(--accent-teal);
    color: var(--font-color);
    border-color: var(--accent-teal);
}

.daily-task-card:hover {
    border-color: var(--accent-teal);
    background: var(--panel-bg);
}

.daily-task-title {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--font-color);
    margin-bottom: 0.3rem;
}

.daily-task-meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    flex-wrap: wrap;
}

.status-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    margin-right: 2px;
}

.status-todo.status-dot {
    background: #6b7280;
}

.status-in_progress.status-dot {
    background: #3b82f6;
}

.status-done.status-dot {
    background: #22c55e;
}

.filters {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
    align-items: center;
}

.filter-select {
    padding: 0.3rem 0.5rem;
    font-size: 0.8rem;
    color: var(--font-color);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--raised-bg);
}

.filter-text {
    padding: 0.3rem 0.5rem;
    font-size: 0.8rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    flex: 1;
    min-width: 10rem;
    max-width: 16rem;
}

.filter-text:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.sort-label {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    display: flex;
    align-items: center;
    gap: 0.25rem;
}

.filter-sort {
    max-width: 7rem;
}

.task-list {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
}

.task-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.6rem;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.1s;
}

.task-row:hover {
    background: var(--panel-bg);
}

.task-row-title {
    font-size: 0.9rem;
    color: var(--font-color);
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.task-row-meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    flex-shrink: 0;
}

.task-row-due {
    color: var(--heading-color);
}

.recurring-badge {
    font-size: 0.7rem;
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background: var(--accent-teal-dim);
    color: var(--font-color);
    flex-shrink: 0;
}

/* Quick status buttons on overview rows */
.overview-quick-actions {
    display: flex;
    gap: 0.15rem;
    flex-shrink: 0;
    margin-left: 0.25rem;
    opacity: 0;
    transition: opacity 0.15s;
}

.task-row:hover .overview-quick-actions {
    opacity: 1;
}

.overview-action-btn {
    padding: 0.15rem 0.35rem;
    font-size: 0.7rem;
    line-height: 1;
    border-radius: 3px;
}

.overview-action-btn:hover:not(:disabled) {
    background: var(--accent-teal);
    color: var(--font-color);
    border-color: var(--accent-teal);
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

.empty-hint {
    font-size: 0.85rem;
    color: var(--font-color-secondary);
    font-style: italic;
    padding: 0.5rem 0;
}

/* Daily History */
.history-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.history-day {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    padding: 0.35rem 0;
    border-bottom: 1px solid var(--border-color);
}

.history-day:last-child {
    border-bottom: none;
}

.history-date {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--font-color-secondary);
    white-space: nowrap;
    min-width: 120px;
    padding-top: 0.15rem;
}

.history-tasks {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem 0.6rem;
}

.history-task {
    font-size: 0.8rem;
    cursor: pointer;
    color: var(--font-color);
    transition: color 0.15s;
}

.history-task:hover {
    color: var(--accent-teal);
}

.history-task.status-done {
    opacity: 0.6;
    text-decoration: line-through;
}
</style>

<style>
@import "../task/status-colors.css";
</style>
