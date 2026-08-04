/**
 * Module-level event bus for task status changes across all components.
 *
 * When any component changes a task's status (via quick-status buttons,
 * overview actions, daily-task actions, or the thread sidebar), it emits
 * a status-changed event so every other component can refresh.
 *
 * NOTE: This is intentionally module-level (not per-component). All callers
 * of useTaskEventBus() share the same listener set, enabling communication
 * across unrelated parts of the component tree without prop drilling or
 * provide/inject ceremony.
 */

const listeners = new Set();
const subtaskListeners = new Set();

export function useTaskEventBus() {
    /**
     * Broadcast a status change to all listeners.
     * @param {string} noteId  - the note ID whose status changed
     * @param {string} status  - the new status ("todo" | "in_progress" | "done")
     */
    function emitStatusChange(noteId, status) {
        for (const fn of listeners) {
            try {
                fn(noteId, status);
            } catch {
                // never let one faulty listener break others
            }
        }
    }

    /**
     * Register a listener for status changes. Returns an unsubscribe function.
     * @param {(noteId: string, status: string) => void} fn
     * @returns {() => void} unsubscribe
     */
    function onStatusChange(fn) {
        listeners.add(fn);
        return () => listeners.delete(fn);
    }

    /**
     * Broadcast a subtask progress change to all listeners. Used when a
     * subtask is checked/unchecked from any task component (main view or
     * thread sidebar) so task overviews can update their progress bars.
     * @param {string} noteId - the task note ID whose subtasks changed
     * @param {number} done   - number of checked subtasks
     * @param {number} total  - total number of subtasks
     */
    function emitSubtaskChange(noteId, done, total) {
        for (const fn of subtaskListeners) {
            try {
                fn(noteId, done, total);
            } catch {
                // never let one faulty listener break others
            }
        }
    }

    /**
     * Register a listener for subtask progress changes. Returns an
     * unsubscribe function.
     * @param {(noteId: string, done: number, total: number) => void} fn
     * @returns {() => void} unsubscribe
     */
    function onSubtaskChange(fn) {
        subtaskListeners.add(fn);
        return () => subtaskListeners.delete(fn);
    }

    return {
        emitStatusChange,
        onStatusChange,
        emitSubtaskChange,
        onSubtaskChange,
    };
}
