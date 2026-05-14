/**
 * Shared composable: usePluginAction
 *
 * Centralizes loading/error/success handling for plugin-action RPC calls.
 */

import { ref } from "vue";
import { pluginActionV2 } from "../../api.js";

export function usePluginAction(tokenRef) {
    const loading = ref(false);
    const error = ref(null);
    const result = ref(null);

    async function execute(noteId, actionID, params) {
        loading.value = true;
        error.value = null;
        result.value = null;
        try {
            // Resolve token: if tokenRef is a function, call it; if it's a ref, unwrap .value; otherwise use as-is.
            const token =
                typeof tokenRef === "function"
                    ? tokenRef()
                    : (tokenRef?.value ?? tokenRef);
            result.value = await pluginActionV2(
                token,
                noteId,
                actionID,
                params,
            );
            return result.value;
        } catch (e) {
            error.value = e;
            throw e;
        } finally {
            loading.value = false;
        }
    }

    return { loading, error, result, execute };
}
