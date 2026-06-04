import { computed, onMounted, onUnmounted, ref, unref } from "vue";

const RESERVED_HINT_KEYS = new Set(["C", "F"]);

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
    const normalizedHintKey = definition.hintKey
        ? normalizeKeyName(definition.hintKey)
        : "";

    return {
        id: definition.id || `shortcut-${index}`,
        description:
            definition.description || definition.label || definition.id || "",
        group: definition.group || "General",
        keys: keyList,
        parsedKeys: keyList.map(parseShortcutCombo),
        hintKey: RESERVED_HINT_KEYS.has(normalizedHintKey)
            ? ""
            : normalizedHintKey,
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
    const hintModifierDown = ref(false);
    const hintInterceptionActive = ref(false);
    const hintModifierKey = "Control";
    const hintModifierLabel = "Ctrl";
    const hintInterceptionMs = 2000;
    let hintInterceptionTimer = null;

    const shortcuts = computed(() => {
        const definitions = unref(shortcutDefinitions) || [];
        return definitions
            .map((definition, index) =>
                normalizeShortcutDefinition(definition, index),
            )
            .filter((definition) => definition.visible);
    });

    function clearHintInterceptionTimer() {
        if (hintInterceptionTimer) {
            clearTimeout(hintInterceptionTimer);
            hintInterceptionTimer = null;
        }
    }

    function formatHintCombo(key) {
        return `${hintModifierLabel}+${formatKeyDisplay(key)}`;
    }

    function uniqueLabels(labels) {
        return [...new Set(labels.filter(Boolean))];
    }

    const helpItems = computed(() =>
        shortcuts.value
            .filter((shortcut) => shortcut.includeInHelp)
            .map((shortcut) => {
                const badges = uniqueLabels([
                    shortcut.hintKey ? formatHintCombo(shortcut.hintKey) : "",
                    shortcut.keys.length
                        ? shortcut.parsedKeys
                              .map((combo) => formatShortcutCombo(combo))
                              .join(" / ")
                        : "",
                ]);
                if (badges.length === 0) return null;
                return {
                    id: shortcut.id,
                    description: shortcut.description,
                    keys: badges.join(" • "),
                    group: shortcut.group,
                    enabled: shortcut.enabled,
                };
            })
            .filter(Boolean),
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
        return uniqueLabels([
            shortcut.hintKey ? formatHintCombo(shortcut.hintKey) : "",
            shortcut.parsedKeys.length
                ? shortcut.parsedKeys
                      .map((combo) => formatShortcutCombo(combo))
                      .join(" / ")
                : "",
        ]).join(" • ");
    }

    function hideHintOverlay() {
        hintModifierDown.value = false;
        hintInterceptionActive.value = false;
        hintOverlayVisible.value = false;
        clearHintInterceptionTimer();
    }

    function startHintInterception() {
        hintModifierDown.value = true;
        hintInterceptionActive.value = true;
        hintOverlayVisible.value = true;
        clearHintInterceptionTimer();
        hintInterceptionTimer = setTimeout(() => {
            hintInterceptionActive.value = false;
            hintOverlayVisible.value = false;
            hintInterceptionTimer = null;
        }, hintInterceptionMs);
    }

    function canRunShortcut(shortcut) {
        if (!shortcut?.enabled) return false;
        if (
            !shortcut.allowInInput &&
            isEditableElement(document.activeElement)
        ) {
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

    function findDirectShortcut(event) {
        return (
            shortcuts.value.find((shortcut) =>
                shortcut.parsedKeys.some((combo) =>
                    matchesParsedShortcut(event, combo),
                ),
            ) || null
        );
    }

    function onKeyDown(event) {
        if (event.defaultPrevented) return;

        const active = document.activeElement;
        const activeIsEditable = isEditableElement(active);

        if (activeIsEditable) {
            hideHintOverlay();
            const directShortcut = findDirectShortcut(event);
            const normalizedKey = normalizeKeyName(event.key);

            if (
                directShortcut &&
                canRunShortcut(directShortcut) &&
                (event.ctrlKey || event.metaKey || normalizedKey === "Escape")
            ) {
                event.preventDefault();
                runShortcut(directShortcut, event, "direct");
                return;
            }

            if (
                normalizedKey === "Escape" &&
                !event.ctrlKey &&
                !event.altKey &&
                !event.metaKey
            ) {
                event.preventDefault();
                active?.blur?.();
            }
            return;
        }

        if (event.key === hintModifierKey && !event.altKey && !event.metaKey) {
            if (!hintModifierDown.value && !event.repeat) {
                startHintInterception();
            }
            return;
        }

        if (
            hintModifierDown.value &&
            hintInterceptionActive.value &&
            event.ctrlKey &&
            !event.altKey &&
            !event.metaKey
        ) {
            const normalizedKey = normalizeKeyName(event.key);
            const hintedShortcut = shortcuts.value.find(
                (shortcut) =>
                    shortcut.hintKey && normalizedKey === shortcut.hintKey,
            );
            const directShortcut = findDirectShortcut(event);

            if (!hintedShortcut && !directShortcut) {
                hideHintOverlay();
                return;
            }

            event.preventDefault();
            event.stopPropagation();

            if (hintedShortcut) {
                const didRun = runShortcut(hintedShortcut, event, "hint");
                if (didRun) {
                    showHelp.value = false;
                }
                return;
            }

            runShortcut(directShortcut, event, "direct");
            return;
        }

        const directShortcut = findDirectShortcut(event);
        if (directShortcut) {
            runShortcut(directShortcut, event, "direct");
        }
    }

    function onKeyUp(event) {
        if (event.key !== hintModifierKey) return;
        hideHintOverlay();
    }

    function onWindowBlur() {
        hideHintOverlay();
    }

    function onPointerDown() {
        if (hintOverlayVisible.value || hintModifierDown.value) {
            hideHintOverlay();
        }
    }

    onMounted(() => {
        window.addEventListener("keydown", onKeyDown, true);
        window.addEventListener("keyup", onKeyUp, true);
        window.addEventListener("blur", onWindowBlur);
        window.addEventListener("pointerdown", onPointerDown, true);
    });

    onUnmounted(() => {
        window.removeEventListener("keydown", onKeyDown, true);
        window.removeEventListener("keyup", onKeyUp, true);
        window.removeEventListener("blur", onWindowBlur);
        window.removeEventListener("pointerdown", onPointerDown, true);
        clearHintInterceptionTimer();
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
        hintModifierLabel,
        isMacPlatform,
    };
}
