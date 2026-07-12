<template>
    <div class="overview-dashboard">
        <!-- Stats -->
        <div class="stats-row">
            <div class="stat-card">
                <span class="stat-number">{{ viewData.stats?.total || 0 }}</span>
                <span class="stat-label">Total</span>
            </div>
            <div class="stat-card stat-todo">
                <span class="stat-number">{{ viewData.stats?.todo || 0 }}</span>
                <span class="stat-label">To Do</span>
            </div>
            <div class="stat-card stat-progress">
                <span class="stat-number">{{ viewData.stats?.in_progress || 0 }}</span>
                <span class="stat-label">In Progress</span>
            </div>
            <div class="stat-card stat-done">
                <span class="stat-number">{{ viewData.stats?.done || 0 }}</span>
                <span class="stat-label">Done</span>
            </div>
            <div
                v-if="viewData.stats?.overdue > 0"
                class="stat-card stat-overdue"
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

        <div v-if="editing" class="section config-section-card">
            <h4>⚙ Daily Task Scoring</h4>
            <div class="config-grid">
                <label class="config-field">
                    <span>Base Daily Task Count</span>
                    <input
                        v-model.number="localDailyTaskCount"
                        type="number"
                        min="1"
                        max="50"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Force Include Due Within Days</span>
                    <input
                        v-model.number="localUrgentDueDays"
                        type="number"
                        min="0"
                        max="30"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Due Urgency Weight</span>
                    <input
                        v-model.number="localDueUrgencyWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Priority Weight</span>
                    <input
                        v-model.number="localPriorityWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Difficulty Weight</span>
                    <input
                        v-model.number="localDifficultyWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Fun Weight</span>
                    <input
                        v-model.number="localFunWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Time Estimation Weight</span>
                    <input
                        v-model.number="localTimeEstimationWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
                <label class="config-field">
                    <span>Fun × Time Weight</span>
                    <input
                        v-model.number="localFunTimeWeight"
                        type="number"
                        step="0.25"
                        class="config-input"
                    />
                </label>
            </div>
            <p class="config-hint">
                Tasks due within {{ localUrgentDueDays }}
                {{ localUrgentDueDays === 1 ? "day" : "days" }} are always
                included. In-progress tasks are also always included unless the
                task disables that behavior. If forced tasks exceed
                {{ localDailyTaskCount }}, daily generation grows automatically.
                Save your changes, then refresh Daily Tasks to persist a new
                generated list.
            </p>
        </div>

        <div v-if="editing" class="section">
            <h4>📈 Open Task Scores ({{ liveScoredOpenTasks.length }})</h4>
            <div v-if="liveScoredOpenTasks.length === 0" class="empty-hint">
                No open tasks to score.
            </div>
            <div v-else class="score-preview-list">
                <div
                    v-for="(task, index) in liveScoredOpenTasks"
                    :key="task.note_id"
                    class="score-preview-card"
                    @click="$emit('selectNote', task.note_id)"
                >
                    <div class="score-preview-header">
                        <span class="score-rank">#{{ index + 1 }}</span>
                        <span class="score-preview-title">{{
                            task.title || "Untitled"
                        }}</span>
                        <span class="score-pill">
                            {{ formatScore(task.generation_score) }}
                        </span>
                    </div>
                    <div class="score-preview-meta">
                        <span :class="'status-dot status-' + task.status"></span>
                        <span>P{{ task.priority }}</span>
                        <span>D{{ task.difficulty }}</span>
                        <span>F{{ task.fun > 0 ? "+" : "" }}{{ task.fun }}</span>
                        <span v-if="task.time_estimation"
                            >⏱ {{ task.time_estimation }}</span
                        >
                        <span v-if="task.due_date">📅 {{ task.due_date }}</span>
                        <span
                            v-for="reason in task.generation_forced_reasons || []"
                            :key="reason"
                            class="force-badge"
                            :class="'force-' + reason"
                        >
                            {{ forceReasonLabel(reason) }}
                        </span>
                    </div>
                    <div class="score-breakdown">
                        Due {{ signedNumber(task.generation_score_breakdown?.due_urgency) }} ·
                        Priority
                        {{ signedNumber(task.generation_score_breakdown?.priority) }}
                        · Difficulty
                        {{
                            signedNumber(
                                task.generation_score_breakdown?.difficulty,
                            )
                        }}
                        · Fun
                        {{ signedNumber(task.generation_score_breakdown?.fun) }} ·
                        Time
                        {{
                            signedNumber(
                                task.generation_score_breakdown?.time_estimation,
                            )
                        }}
                        · Fun×Time
                        {{
                            signedNumber(task.generation_score_breakdown?.fun_time)
                        }}
                    </div>
                </div>
            </div>
        </div>

        <!-- Daily Tasks -->
        <div class="section">
            <div class="section-header-row">
                <h4>🎯 Daily Tasks</h4>
                <button
                    class="btn-ghost btn-sm"
                    @click="generateDailyTasks"
                    :disabled="loadingDaily"
                >
                    {{ loadingDaily ? "Loading..." : "🔄 Refresh" }}
                </button>
            </div>
            <p class="config-hint daily-hint">
                Base target: {{ localDailyTaskCount }} · currently selected:
                {{ dailyTasks.length }}
            </p>
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
                        <span>P{{ t.priority }} D{{ t.difficulty }}</span>
                        <span>F{{ t.fun > 0 ? "+" : "" }}{{ t.fun }}</span>
                        <span v-if="t.due_date">📅 {{ t.due_date }}</span>
                        <span v-if="t.generation_score || t.generation_score === 0">
                            ★ {{ formatScore(t.generation_score) }}
                        </span>
                    </div>
                    <div
                        v-if="
                            t.generation_forced_reasons &&
                            t.generation_forced_reasons.length > 0
                        "
                        class="daily-task-flags"
                    >
                        <span
                            v-for="reason in t.generation_forced_reasons"
                            :key="reason"
                            class="force-badge"
                            :class="'force-' + reason"
                        >
                            {{ forceReasonLabel(reason) }}
                        </span>
                    </div>
                    <div v-if="t.status !== 'done'" class="daily-quick-actions">
                        <button
                            class="btn-ghost btn-sm overview-action-btn"
                            :disabled="statusLoading[t.note_id]"
                            @click.stop="quickSetStatus(t.note_id, 'in_progress')"
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
                        <select v-model="sortBy" class="filter-select filter-sort">
                            <option value="generation_score">Daily Score</option>
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
                        <span class="task-row-title">{{ t.title || "Untitled" }}</span>
                        <span class="task-row-meta">
                            <span v-if="scoreById[t.note_id]"
                                >★ {{ formatScore(scoreById[t.note_id].generation_score) }}</span
                            >
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
                            v-if="
                                scoreById[t.note_id]?.generation_forced_reasons?.length
                            "
                            class="task-row-flags"
                        >
                            <span
                                v-for="reason in scoreById[t.note_id]
                                    .generation_forced_reasons"
                                :key="reason"
                                class="force-badge"
                                :class="'force-' + reason"
                            >
                                {{ forceReasonLabel(reason) }}
                            </span>
                        </span>
                        <span
                            v-if="t.status !== 'done'"
                            class="overview-quick-actions"
                        >
                            <button
                                class="btn-ghost btn-sm overview-action-btn"
                                :disabled="statusLoading[t.note_id]"
                                @click.stop="quickSetStatus(t.note_id, 'in_progress')"
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
                <span class="collapse-arrow">{{ historyOpen ? "▼" : "▶" }}</span>
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
import { computed, onBeforeUnmount, ref, watch } from "vue";
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
const { execute: execQuickStatus } = usePluginAction(() => props.token);
const statusLoading = ref({});

const { emitStatusChange, onStatusChange } = useTaskEventBus();

const viewData = ref({
    tasks: [],
    scored_open_tasks: [],
    daily_tasks: [],
    daily_history: [],
    stats: {},
});

const localDailyTaskCount = ref(3);
const localUrgentDueDays = ref(3);
const localPriorityWeight = ref(4);
const localDueUrgencyWeight = ref(6);
const localDifficultyWeight = ref(-1);
const localFunWeight = ref(0.75);
const localTimeEstimationWeight = ref(-0.5);
const localFunTimeWeight = ref(0.1);

let hydrating = false;

function hydrateFromProp() {
    hydrating = true;
    const cd = props.customData;
    if (cd && typeof cd === "object") {
        viewData.value = {
            tasks: Array.isArray(cd.tasks) ? cd.tasks : [],
            scored_open_tasks: Array.isArray(cd.scored_open_tasks)
                ? cd.scored_open_tasks
                : [],
            daily_tasks: Array.isArray(cd.daily_tasks) ? cd.daily_tasks : [],
            daily_history: Array.isArray(cd.daily_history)
                ? cd.daily_history
                : [],
            stats: cd.stats || {},
        };
        localDailyTaskCount.value = cd.daily_task_count ?? 3;
        localUrgentDueDays.value = cd.urgent_due_days ?? 3;
        localPriorityWeight.value = cd.priority_weight ?? 4;
        localDueUrgencyWeight.value = cd.due_urgency_weight ?? 6;
        localDifficultyWeight.value = cd.difficulty_weight ?? -1;
        localFunWeight.value = cd.fun_weight ?? 0.75;
        localTimeEstimationWeight.value = cd.time_estimation_weight ?? -0.5;
        localFunTimeWeight.value = cd.fun_time_weight ?? 0.1;
    }
    hydrating = false;
}

function emitConfig() {
    if (hydrating) return;
    emit("update:customData", {
        daily_task_count: clampNumber(localDailyTaskCount.value, 1, 50, 3),
        urgent_due_days: clampNumber(localUrgentDueDays.value, 0, 30, 3),
        priority_weight: numberOr(localPriorityWeight.value, 4),
        due_urgency_weight: numberOr(localDueUrgencyWeight.value, 6),
        difficulty_weight: numberOr(localDifficultyWeight.value, -1),
        fun_weight: numberOr(localFunWeight.value, 0.75),
        time_estimation_weight: numberOr(localTimeEstimationWeight.value, -0.5),
        fun_time_weight: numberOr(localFunTimeWeight.value, 0.1),
    });
}

watch(() => props.note?.id, hydrateFromProp, { immediate: true });
watch(
    () => props.customData,
    (cd) => {
        if (hydrating) return;
        if (cd && typeof cd === "object") {
            hydrateFromProp();
        }
    },
);
watch(
    [
        localDailyTaskCount,
        localUrgentDueDays,
        localPriorityWeight,
        localDueUrgencyWeight,
        localDifficultyWeight,
        localFunWeight,
        localTimeEstimationWeight,
        localFunTimeWeight,
    ],
    emitConfig,
);

const effectiveConfig = computed(() => ({
    daily_task_count: clampNumber(localDailyTaskCount.value, 1, 50, 3),
    urgent_due_days: clampNumber(localUrgentDueDays.value, 0, 30, 3),
    priority_weight: numberOr(localPriorityWeight.value, 4),
    due_urgency_weight: numberOr(localDueUrgencyWeight.value, 6),
    difficulty_weight: numberOr(localDifficultyWeight.value, -1),
    fun_weight: numberOr(localFunWeight.value, 0.75),
    time_estimation_weight: numberOr(localTimeEstimationWeight.value, -0.5),
    fun_time_weight: numberOr(localFunTimeWeight.value, 0.1),
}));

// Filters
const filterStatus = ref("");
const filterText = ref("");
const sortBy = ref("generation_score");

const liveScoredOpenTasks = computed(() =>
    scoreOpenTasks(viewData.value.tasks || [], effectiveConfig.value),
);

const scoreById = computed(() => {
    const map = {};
    for (const task of liveScoredOpenTasks.value) {
        map[task.note_id] = task;
    }
    return map;
});

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
            case "generation_score":
                return scoreValue(b.note_id) - scoreValue(a.note_id);
            case "priority":
                return (b.priority || 0) - (a.priority || 0);
            case "due_date": {
                const da = a.due_date || "9999-12-31";
                const db = b.due_date || "9999-12-31";
                return da.localeCompare(db);
            }
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

const dailyTasks = computed(() =>
    (viewData.value.daily_tasks || []).map((task) => {
        const liveScore = scoreById.value[task.note_id];
        return liveScore ? { ...task, ...liveScore } : task;
    }),
);

const tasksOpen = ref(false);
const dailyHistory = computed(() => viewData.value.daily_history || []);
const historyOpen = ref(false);

function scoreValue(noteId) {
    return scoreById.value[noteId]?.generation_score ?? -1000000;
}

function numberOr(value, fallback) {
    return Number.isFinite(Number(value)) ? Number(value) : fallback;
}

function clampNumber(value, min, max, fallback) {
    const numeric = numberOr(value, fallback);
    return Math.min(max, Math.max(min, numeric));
}

function parseTimeToMinutes(value) {
    const s = String(value || "").trim();
    if (!s) return 0;

    let remaining = s;
    let total = 0;

    const dayMatch = remaining.match(/^(\d+)d/i);
    if (dayMatch) {
        total += Number(dayMatch[1]) * 8 * 60;
        remaining = remaining.slice(dayMatch[0].length);
    }

    const hourMatch = remaining.match(/^(\d+)h/i);
    if (hourMatch) {
        total += Number(hourMatch[1]) * 60;
        remaining = remaining.slice(hourMatch[0].length);
    }

    const minuteMatch = remaining.match(/^(\d+)m/i);
    if (minuteMatch) {
        total += Number(minuteMatch[1]);
    }

    return total;
}

function computeDueInDays(dueDate) {
    if (!dueDate) return null;
    const due = new Date(`${dueDate}T00:00:00Z`);
    if (Number.isNaN(due.getTime())) return null;
    const now = new Date();
    const startOfToday = Date.UTC(
        now.getUTCFullYear(),
        now.getUTCMonth(),
        now.getUTCDate(),
    );
    return Math.round((due.getTime() - startOfToday) / 86400000);
}

function computeDueUrgencyUnits(config, dueInDays) {
    if (dueInDays == null) return 0;
    if (dueInDays > config.urgent_due_days) return 0;
    return config.urgent_due_days + 1 - dueInDays;
}

function forcedReasons(task, config, dueInDays) {
    const reasons = [];
    if (dueInDays != null && dueInDays <= config.urgent_due_days) {
        reasons.push("due_soon");
    }
    if (
        task.status === "in_progress" &&
        !task.pending_does_not_force_daily_inclusion
    ) {
        reasons.push("pending");
    }
    return reasons;
}

function generationForceRank(task) {
    const reasons = task.generation_forced_reasons || [];
    if (reasons.includes("due_soon")) return 2;
    if (reasons.includes("pending")) return 1;
    return 0;
}

function scoreOpenTasks(tasks, config) {
    return (tasks || [])
        .filter((task) => task.status !== "done")
        .map((task) => {
            const estimatedHours = parseTimeToMinutes(task.time_estimation) / 60;
            const dueInDays = computeDueInDays(task.due_date);
            const generation_forced_reasons = forcedReasons(
                task,
                config,
                dueInDays,
            );
            const generation_score_breakdown = {
                due_urgency:
                    computeDueUrgencyUnits(config, dueInDays) *
                    config.due_urgency_weight,
                priority: (task.priority || 0) * config.priority_weight,
                difficulty: (task.difficulty || 0) * config.difficulty_weight,
                fun: (task.fun || 0) * config.fun_weight,
                time_estimation:
                    estimatedHours * config.time_estimation_weight,
                fun_time: estimatedHours * (task.fun || 0) * config.fun_time_weight,
                estimated_hours: estimatedHours,
                total: 0,
            };
            generation_score_breakdown.total =
                generation_score_breakdown.due_urgency +
                generation_score_breakdown.priority +
                generation_score_breakdown.difficulty +
                generation_score_breakdown.fun +
                generation_score_breakdown.time_estimation +
                generation_score_breakdown.fun_time;

            return {
                ...task,
                due_in_days: dueInDays,
                generation_forced_reasons,
                generation_score_breakdown,
                generation_score: generation_score_breakdown.total,
            };
        })
        .sort((a, b) => {
            const rankDiff = generationForceRank(b) - generationForceRank(a);
            if (rankDiff !== 0) return rankDiff;
            if (a.generation_score !== b.generation_score) {
                return b.generation_score - a.generation_score;
            }
            const dueA = a.due_in_days == null ? Number.MAX_SAFE_INTEGER : a.due_in_days;
            const dueB = b.due_in_days == null ? Number.MAX_SAFE_INTEGER : b.due_in_days;
            if (dueA !== dueB) return dueA - dueB;
            if ((a.priority || 0) !== (b.priority || 0)) {
                return (b.priority || 0) - (a.priority || 0);
            }
            return (a.note_id || 0) - (b.note_id || 0);
        });
}

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
        if (datePart === yesterday.toISOString().slice(0, 10)) {
            return `Yesterday ${timePart} (${fmt})`;
        }

        return `${fmt} ${timePart}`;
    } catch {
        return isoStr;
    }
}

function forceReasonLabel(reason) {
    switch (reason) {
        case "due_soon":
            return `Due ≤ ${localUrgentDueDays.value}d`;
        case "pending":
            return "Pending";
        default:
            return reason;
    }
}

function signedNumber(value) {
    const numeric = Number(value || 0);
    return `${numeric >= 0 ? "+" : ""}${formatScore(numeric)}`;
}

function formatScore(value) {
    const numeric = Number(value || 0);
    return numeric.toFixed(2).replace(/\.00$/, "").replace(/(\.\d)0$/, "$1");
}

async function generateDailyTasks() {
    try {
        const result = await execDailyTasks(props.note.id, "daily_tasks", {});
        if (result && result.daily_tasks) {
            viewData.value = {
                ...viewData.value,
                daily_tasks: result.daily_tasks,
                scored_open_tasks: Array.isArray(result.scored_open_tasks)
                    ? result.scored_open_tasks
                    : viewData.value.scored_open_tasks,
            };
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
        updateLocalTaskStatus(taskNoteId, status);
        emitStatusChange(taskNoteId, status);
    } catch {
        // Error handled silently
    } finally {
        statusLoading.value = { ...statusLoading.value, [taskNoteId]: false };
    }
}

function updateLocalTaskStatus(taskNoteId, status) {
    const tasks = (viewData.value.tasks || []).map((task) =>
        task.note_id === taskNoteId ? { ...task, status } : task,
    );
    const daily = (viewData.value.daily_tasks || []).map((task) =>
        task.note_id === taskNoteId ? { ...task, status } : task,
    );
    viewData.value = {
        ...viewData.value,
        tasks,
        daily_tasks: daily,
    };
}

const unsubOverview = onStatusChange((noteId, status) => {
    updateLocalTaskStatus(noteId, status);
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

.section-header-row {
    display: flex;
    justify-content: space-between;
    gap: 0.75rem;
    align-items: center;
}

.config-section-card {
    padding: 0.9rem;
    border-radius: 8px;
    border: 1px solid var(--border-color);
    background: var(--raised-bg);
}

.config-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: 0.75rem;
}

.config-field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-size: 0.85rem;
    color: var(--font-color-secondary);
}

.config-input {
    padding: 0.4rem 0.5rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--panel-bg);
    color: var(--font-color);
}

.config-input:focus,
.filter-text:focus,
.filter-select:focus {
    border-color: var(--accent-teal);
    outline: none;
}

.config-hint {
    margin: 0;
    font-size: 0.8rem;
    color: var(--font-color-secondary);
}

.daily-hint {
    margin-top: -0.25rem;
}

.score-preview-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
}

.score-preview-card {
    padding: 0.75rem 0.9rem;
    border: 1px solid var(--border-color);
    border-radius: 8px;
    background: var(--raised-bg);
    cursor: pointer;
    transition:
        border-color 0.15s,
        background 0.15s;
}

.score-preview-card:hover,
.daily-task-card:hover {
    border-color: var(--accent-teal);
    background: var(--panel-bg);
}

.score-preview-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
}

.score-rank {
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    min-width: 2rem;
}

.score-preview-title {
    flex: 1;
    font-weight: 600;
    color: var(--font-color);
}

.score-pill {
    padding: 0.15rem 0.5rem;
    border-radius: 999px;
    background: var(--accent-teal-dim);
    color: var(--font-color);
    font-size: 0.75rem;
    font-weight: 600;
}

.score-preview-meta,
.daily-task-meta {
    display: flex;
    gap: 0.5rem;
    font-size: 0.75rem;
    color: var(--font-color-secondary);
    flex-wrap: wrap;
}

.score-breakdown {
    font-size: 0.74rem;
    color: var(--font-color-secondary);
    line-height: 1.4;
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

.daily-task-title {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--font-color);
    margin-bottom: 0.3rem;
}

.daily-task-flags,
.task-row-flags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
}

.force-badge {
    padding: 0.1rem 0.45rem;
    border-radius: 999px;
    font-size: 0.68rem;
    font-weight: 600;
}

.force-due_soon {
    background: color-mix(in srgb, var(--heading-color) 16%, transparent);
    color: var(--heading-color);
}

.force-pending {
    background: color-mix(in srgb, var(--accent-teal) 18%, transparent);
    color: var(--accent-teal);
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

.sort-label {
    font-size: 0.8rem;
    color: var(--font-color-secondary);
    display: flex;
    align-items: center;
    gap: 0.25rem;
}

.filter-sort {
    max-width: 9rem;
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
