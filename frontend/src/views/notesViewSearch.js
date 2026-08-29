export function normalizeSearchTypes(types) {
    const normalized = [
        ...new Set((types || []).map((type) => String(type).trim())),
    ].filter(Boolean);
    return normalized.length > 0 ? normalized : ["standard"];
}

export function parseSearchMode(rawQuery, selectedTypes) {
    const tokens = String(rawQuery || "")
        .trim()
        .split(/\s+/)
        .filter(Boolean);
    const cleaned = [];
    let includeAllTypes = false;
    let useTypePicker = false;
    let tagOnly = false;

    for (const token of tokens) {
        if (token === ".a") {
            includeAllTypes = true;
            continue;
        }
        if (token === ".i") {
            useTypePicker = true;
            continue;
        }
        if (token === ".t") {
            tagOnly = true;
            continue;
        }
        cleaned.push(token);
    }

    return {
        query: cleaned.join(" ").trim(),
        includeAllTypes,
        useTypePicker: useTypePicker && !includeAllTypes,
        tagOnly,
        types: includeAllTypes
            ? null
            : useTypePicker
              ? normalizeSearchTypes(selectedTypes)
              : ["standard"],
    };
}

export function noteTypeLabel(type, typeOptions) {
    return typeOptions.find((option) => option.value === type)?.label || type;
}

export function formatSearchTags(tags) {
    return tags.map((tag) => `#${tag}`).join(" ");
}

export function relevancePct(distance) {
    if (distance == null) return "";
    const pct = Math.max(0, Math.round((1 - distance / 2) * 100));
    return `${pct}% match`;
}

export function flattenVisibleSectionGroups(groups) {
    return (groups || []).flatMap((section) =>
        section?.collapsed ? [] : (section?.items || []).map((item) => item.result),
    );
}

export function buildSectionGroups(
    sections,
    { includeFlatIndex = true, collapsedMap = {} } = {},
) {
    let flatIndex = 0;
    return (sections || []).map((section, sectionIndex) => {
        const collapsed = Boolean(collapsedMap?.[section?.key]);
        const items = (section?.results || []).map((result, itemIndex) => {
            const currentFlatIndex = includeFlatIndex ? flatIndex : itemIndex;
            if (!collapsed && includeFlatIndex) {
                flatIndex += 1;
            }
            return {
                key: `${section?.key || sectionIndex}:${result.id}:${itemIndex}`,
                result,
                flatIndex: currentFlatIndex,
            };
        });
        return {
            ...section,
            collapsed,
            items,
        };
    });
}

export function setCollapsedSection(collapsedRef, key, collapsed) {
    collapsedRef.value = {
        ...collapsedRef.value,
        [key]: collapsed,
    };
}

export function toggleCollapsedSection(collapsedRef, key) {
    setCollapsedSection(collapsedRef, key, !collapsedRef.value?.[key]);
}

export function sectionKeyForFlatIndex(groups, flatIndex) {
    if (flatIndex == null || flatIndex < 0) return "";
    for (const section of groups || []) {
        if (section?.collapsed) continue;
        for (const item of section?.items || []) {
            if (item.flatIndex === flatIndex) {
                return section.key || "";
            }
        }
    }
    return "";
}

function normalizeSearchSection(section, { filterResult = null } = {}) {
    const results = Array.isArray(section?.results)
        ? section.results.filter((result) =>
              filterResult ? filterResult(result) : true,
          )
        : [];
    if (results.length === 0) {
        return null;
    }
    return {
        key: section?.key || `section-${results[0]?.id || "results"}`,
        label: section?.label || "Results",
        description: section?.description || "",
        results,
    };
}

export function abortSearchRequest(store) {
    if (store?.controller) {
        store.controller.abort();
        store.controller = null;
    }
}

export async function runStreamedSearch({
    token,
    streamSearchNotes,
    query,
    types = null,
    tagOnly = false,
    sectionsRef,
    statusRef,
    searchingRef,
    requestStore,
    errorRef = null,
    filterResult = null,
    onFirstResult = null,
    onReset = null,
    onDone = null,
}) {
    const trimmed = String(query || "").trim();
    abortSearchRequest(requestStore);

    if (!trimmed) {
        sectionsRef.value = [];
        statusRef.value = "";
        searchingRef.value = false;
        if (errorRef) {
            errorRef.value = "";
        }
        onReset?.();
        return;
    }

    const controller = new AbortController();
    requestStore.controller = controller;
    sectionsRef.value = [];
    statusRef.value = "Searching exact matches…";
    if (errorRef) {
        errorRef.value = "";
    }
    searchingRef.value = true;
    let firstResultDelivered = false;

    try {
        await streamSearchNotes(token, trimmed, {
            types,
            tagOnly,
            signal: controller.signal,
            onStatus(event) {
                if (requestStore.controller !== controller) return;
                statusRef.value = event?.message || "";
            },
            onSection(section) {
                if (requestStore.controller !== controller) return;
                const normalized = normalizeSearchSection(section, {
                    filterResult,
                });
                if (!normalized) return;
                sectionsRef.value = [...sectionsRef.value, normalized];
                if (!firstResultDelivered) {
                    firstResultDelivered = true;
                    onFirstResult?.();
                }
            },
            onError(event) {
                if (requestStore.controller !== controller || !errorRef) return;
                errorRef.value = event?.message || "Search failed";
            },
            onDone(event) {
                if (requestStore.controller !== controller) return;
                statusRef.value = event?.message || "";
                onDone?.(event);
            },
        });
    } catch (error) {
        if (error?.name === "AbortError") {
            return;
        }
        sectionsRef.value = [];
        statusRef.value = "";
        if (errorRef) {
            errorRef.value = error?.message || "Search failed";
        }
        onReset?.();
    } finally {
        if (requestStore.controller === controller) {
            requestStore.controller = null;
            searchingRef.value = false;
        }
    }
}
