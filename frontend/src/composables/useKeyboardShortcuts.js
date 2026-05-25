import { computed, onMounted, onUnmounted, ref, unref } from "vue";

function getPlatform() {
    return navigator.platform || navigator.userAgentData?.platform || "";
}

export function isMacPlatform() {
    return /Mac|iPod|iPhone|iPad/.test(getPlatform());
}

export function isEditableElement(el) {
    if (!el) return false;
    if (el.isContentEditable) return true;
    const tag = el.tagName;
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

function normalizeKeyName(key) {
    if (!key) return "";
    switch (key) {
        case "Esc":
            return "Escape";
        case " ":
            return "Space";
        default:
            return key.length === 1 ? key.toUpperCase() : key;
    }
}

function parseShortcutCombo(combo) {
    const parts = String(combo)
        .split("+")
        .map((part) => part.trim())
        .filter(Boolean);

    const parsed = {
        raw: combo,
        key: "",
        alt: false,
        shift: false,
        ctrl: false,
        meta: false,
        mod: false,
    };

    for (const part of parts) {
        const token = part.toLowerCase();
        if (token === "alt" || token === "option") {
            parsed.alt = true;
            continue;
        }
        if (token === "shift") {
            parsed.shift = true;
            continue;
        }
        if (token === "ctrl" || token === "control") {
            parsed.ctrl = true;
            continue;
        }
        if (token === "cmd" || token === "command" || token === "meta") {
            parsed.meta = true;
            continue;
        }
        if (token === "mod") {
            parsed.mod = true;
            continue;
        }
        parsed.key = normalizeKeyName(part);
    }

    return parsed;
}

function matchesParsedShortcut(event, parsed) {
    const isMac = isMacPlatform();
    const expectedCtrl = parsed.ctrl || (parsed.mod && !isMac);
    const expectedMeta = parsed.meta || (parsed.mod && isMac);

    if (Boolean(event.ctrlKey) !== expectedCtrl) return false;
    if (Boolean(event.metaKey) !== expectedMeta) return false;
    if (Boolean(event.altKey) !== Boolean(parsed.alt)) return false;
    if (Boolean(event.shiftKey) !== Boolean(parsed.shift)) return false;

    return normalizeKeyName(event.key) === parsed.key;
}

function formatKeyDisplay(key) {
    switch (key) {
        case "ArrowUp":
            return "↑";
        case "ArrowDown":
            return "↓";
        case "ArrowLeft":
            return "←";
        case "ArrowRight":
            return "→";
        case "Escape":
            return "Esc";
        case "Backspace":
            return "Backspace";
        case "Delete":
            return "Delete";
        case "Space":
            return "Space";
        default:
            return key;
    }
}

export function formatShortcutCombo(combo) {
    const parsed =
        typeof combo === "string" ? parseShortcutCombo(combo) : combo || {};
    const isMac = isMacPlatform();
    const parts = [];

    if (parsed.mod) parts.push(isMac ? "⌘" : "Ctrl");
    if (parsed.ctrl) parts.push("Ctrl");
    if (parsed.meta) parts.push("⌘");
    if (parsed.alt) parts.push(isMac ? "⌥" : "Alt");
    if (parsed.shift) parts.push(isMac ? "⇧" : "Shift");
    if (parsed.key) parts.push(formatKeyDisplay(parsed.key));

    return isMac ? parts.join(" ") : parts.join("+");
}

function normalizeShortcutDefinition(definition, index) {
    const keyList = Array.isArray(definition.keys)
        ? definition.keys
        : definition.key
          ? [definition.key]
          : [];

    return {
        id: definition.id || `shortcut-${index}`,
        description:
            definition.description || definition.label || definition.id || "",
        group: definition.group || "General",
        keys: keyList,
        parsedKeys: keyList.map(parseShortcutCombo),
        hintKey: definition.hintKey
            ? normalizeKeyName(definition.hintKey)
            : "",
        includeInHelp: definition.includeInHelp !== false,
        preventDefault: definition.preventDefault !== false,
        allowInInput: Boolean(definition.allowInInput),
        visible:
            typeof definition.visible === "function"
                ? Boolean(definition.visible())
                : definition.visible !== false,
        enabled:
            typeof definition.enabled === "function"
                ? Boolean(definition.enabled())
                : definition.enabled !== false,
        handler: definition.handler,
    };
}

export function useKeyboardShortcuts(shortcutDefinitions) {
    const showHelp = ref(false);
    const hintOverlayVisible = ref(false);
    const altHeld = ref(false);

    const shortcuts = computed(() => {
        const definitions = unref(shortcutDefinitions) || [];
        return definitions
            .map((definition, index) =>
                normalizeShortcutDefinition(definition, index),
            )
            .filter((definition) => definition.visible);
    });

    const helpItems = computed(() =>
        shortcuts.value
            .filter((shortcut) => shortcut.includeInHelp)
            .map((shortcut) => {
                const badges = [];
                if (shortcut.hintKey) {
                    badges.push(`Alt+${formatKeyDisplay(shortcut.hintKey)}`);
                }
                if (shortcut.keys.length) {
                    badges.push(
                        shortcut.parsedKeys
                            .map((combo) => formatShortcutCombo(combo))
                            .join(" / "),
                    );
                }
                return {
                    id: shortcut.id,
                    description: shortcut.description,
                    keys: badges.join(" • "),
                    group: shortcut.group,
                    enabled: shortcut.enabled,
                };
            }),
    );

    function findShortcut(id) {
        return shortcuts.value.find((shortcut) => shortcut.id === id) || null;
    }

    function isShortcutEnabled(id) {
        return Boolean(findShortcut(id)?.enabled);
    }

    function getHintLabel(id) {
        const hintKey = findShortcut(id)?.hintKey;
        return hintKey ? formatKeyDisplay(hintKey) : "";
    }

    function getShortcutLabel(id) {
        const shortcut = findShortcut(id);
        if (!shortcut) return "";
        const parts = [];
        if (shortcut.hintKey) {
            parts.push(`Alt+${formatKeyDisplay(shortcut.hintKey)}`);
        }
        if (shortcut.parsedKeys.length) {
            parts.push(
                shortcut.parsedKeys
                    .map((combo) => formatShortcutCombo(combo))
                    .join(" / "),
            );
        }
        return parts.join(" • ");
    }

    function hideHintOverlay() {
        altHeld.value = false;
        hintOverlayVisible.value = false;
    }

    function canRunShortcut(shortcut) {
        if (!shortcut?.enabled) return false;
        if (!shortcut.allowInInput && isEditableElement(document.activeElement)) {
            return false;
        }
        return typeof shortcut.handler === "function";
    }

    function runShortcut(shortcut, event, source = "direct") {
        if (!canRunShortcut(shortcut)) return false;
        if (shortcut.preventDefault && event?.preventDefault) {
            event.preventDefault();
        }
        shortcut.handler(event, { source, shortcut });
        return true;
    }

    function onKeyDown(event) {
        if (event.defaultPrevented) return;

        if (event.key === "Alt" && !event.ctrlKey && !event.metaKey) {
            altHeld.value = true;
            hintOverlayVisible.value = true;
            event.preventDefault();
            return;
        }

        if (altHeld.value && !event.ctrlKey && !event.metaKey && event.key !== "Alt") {
            const hintedShortcut = shortcuts.value.find(
                (shortcut) =>
                    shortcut.hintKey &&
                    normalizeKeyName(event.key) === shortcut.hintKey,
            );
            if (hintedShortcut) {
                const didRun = runShortcut(hintedShortcut, event, "hint");
                if (didRun) {
                    showHelp.value = false;
                }
                hideHintOverlay();
                return;
            }
        }

        const directShortcut = shortcuts.value.find((shortcut) =>
            shortcut.parsedKeys.some((combo) =>
                matchesParsedShortcut(event, combo),
            ),
        );

        if (directShortcut) {
            runShortcut(directShortcut, event, "direct");
        }
    }

    function onKeyUp(event) {
        if (event.key === "Alt") {
            hideHintOverlay();
        }
    }

    function onWindowBlur() {
        hideHintOverlay();
    }

    onMounted(() => {
        window.addEventListener("keydown", onKeyDown);
        window.addEventListener("keyup", onKeyUp);
        window.addEventListener("blur", onWindowBlur);
    });

    onUnmounted(() => {
        window.removeEventListener("keydown", onKeyDown);
        window.removeEventListener("keyup", onKeyUp);
        window.removeEventListener("blur", onWindowBlur);
    });

    return {
        showHelp,
        hintOverlayVisible,
        shortcuts,
        helpItems,
        getHintLabel,
        getShortcutLabel,
        isShortcutEnabled,
        hideHintOverlay,
        isMacPlatform,
    };
}
