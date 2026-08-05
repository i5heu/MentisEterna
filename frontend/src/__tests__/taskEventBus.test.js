import { useTaskEventBus } from "../note-types/shared/useTaskEventBus.js";

// useTaskEventBus returns a module-level shared bus. Each test subscribes
// its own listeners and unsubscribes when done so listeners don't leak
// across tests.
describe("useTaskEventBus", () => {
    test("onStatusChange listener receives (noteId, status) from emitStatusChange", () => {
        const bus = useTaskEventBus();
        const listener = jest.fn();
        const unsubscribe = bus.onStatusChange(listener);

        bus.emitStatusChange("note-1", "done");

        expect(listener).toHaveBeenCalledWith("note-1", "done");
        unsubscribe();
    });

    test("unsubscribe stops future notifications", () => {
        const bus = useTaskEventBus();
        const listener = jest.fn();
        const unsubscribe = bus.onStatusChange(listener);

        bus.emitStatusChange("note-1", "todo");
        unsubscribe();
        bus.emitStatusChange("note-1", "done");

        expect(listener).toHaveBeenCalledTimes(1);
        expect(listener).toHaveBeenCalledWith("note-1", "todo");
    });

    test("multiple listeners all fire on one emit", () => {
        const bus = useTaskEventBus();
        const a = jest.fn();
        const b = jest.fn();
        const c = jest.fn();
        const unsubA = bus.onStatusChange(a);
        const unsubB = bus.onStatusChange(b);
        const unsubC = bus.onStatusChange(c);

        bus.emitStatusChange("note-2", "in_progress");

        expect(a).toHaveBeenCalledWith("note-2", "in_progress");
        expect(b).toHaveBeenCalledWith("note-2", "in_progress");
        expect(c).toHaveBeenCalledWith("note-2", "in_progress");
        unsubA();
        unsubB();
        unsubC();
    });

    test("a throwing listener does not prevent healthy listeners from firing", () => {
        const bus = useTaskEventBus();
        const bad = jest.fn(() => {
            throw new Error("boom");
        });
        const good = jest.fn();
        const unsubBad = bus.onStatusChange(bad);
        const unsubGood = bus.onStatusChange(good);

        // Must not throw despite the faulty listener.
        bus.emitStatusChange("note-3", "done");

        expect(bad).toHaveBeenCalled();
        expect(good).toHaveBeenCalledWith("note-3", "done");
        unsubBad();
        unsubGood();
    });

    test("status and subtask buses are independent", () => {
        const bus = useTaskEventBus();
        const status = jest.fn();
        const subtask = jest.fn();
        const unsubStatus = bus.onStatusChange(status);
        const unsubSubtask = bus.onSubtaskChange(subtask);

        // Subtask emit must not trigger status listeners.
        bus.emitSubtaskChange("note-4", 3, 5);
        expect(status).not.toHaveBeenCalled();
        expect(subtask).toHaveBeenCalledWith("note-4", 3, 5);

        // Status emit must not trigger subtask listeners.
        bus.emitStatusChange("note-4", "done");
        expect(subtask).toHaveBeenCalledTimes(1);
        expect(status).toHaveBeenCalledWith("note-4", "done");

        unsubStatus();
        unsubSubtask();
    });
});
