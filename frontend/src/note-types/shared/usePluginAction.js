/**
 * Shared composable: usePluginAction
 *
 * Centralizes loading/error/success handling for plugin-action RPC calls.
 */

import { ref } from "vue";
import { pluginAction } from "../../api.js";

export function usePluginAction(tokenRef) {
    const loading = ref(false);
    const error = ref(null);
    const result = ref(null);

    async function execute(noteId, action, params) {
        loading.value = true;
        error.value = null;
        result.value = null;
        try {
            result.value = await pluginAction(
                tokenRef.value || tokenRef,
                noteId,
                action,
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
