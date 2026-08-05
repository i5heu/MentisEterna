import { jest } from "@jest/globals";
import { ref } from "vue";

// Composable ↔ api boundary test: mock only `pluginActionV2` (the collaborator)
// and exercise the real loading/error/result state machine + token resolution.
jest.mock("../api.js", () => ({
    pluginActionV2: jest.fn(),
}));
import { pluginActionV2 } from "../api.js";
import { usePluginAction } from "../note-types/shared/usePluginAction.js";

beforeEach(() => {
    pluginActionV2.mockReset();
});

test("resolves a function token and returns the result with loading cycled", async () => {
    pluginActionV2.mockResolvedValue("OK");
    const u = usePluginAction(() => "tok-fn");

    expect(u.loading.value).toBe(false);
    const p = u.execute(3, "run", { x: 1 });

    expect(u.loading.value).toBe(true);
    expect(u.error.value).toBeNull();
    expect(pluginActionV2).toHaveBeenCalledWith("tok-fn", 3, "run", { x: 1 });

    await expect(p).resolves.toBe("OK");
    expect(u.result.value).toBe("OK");
    expect(u.loading.value).toBe(false);
});

test("unwraps a ref token and passes a plain string through unchanged", async () => {
    pluginActionV2.mockResolvedValue(undefined);
    const uRef = usePluginAction(ref("tok-ref"));
    await uRef.execute(1, "a");
    expect(pluginActionV2).toHaveBeenLastCalledWith("tok-ref", 1, "a", undefined);

    const uPlain = usePluginAction("tok-plain");
    await uPlain.execute(2, "b");
    expect(pluginActionV2).toHaveBeenLastCalledWith("tok-plain", 2, "b", undefined);
});

test("on rejection records the error, rethrows, and resets loading", async () => {
    const boom = new Error("boom");
    pluginActionV2.mockRejectedValue(boom);
    const u = usePluginAction("tok");

    await expect(u.execute(5, "act", { z: 2 })).rejects.toBe(boom);
    expect(u.error.value).toBe(boom);
    expect(u.result.value).toBeNull();
    expect(u.loading.value).toBe(false);
});
