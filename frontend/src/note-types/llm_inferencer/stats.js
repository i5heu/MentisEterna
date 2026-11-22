/**
 * Pure helpers for the llm_inferencer statistics block: routing labels,
 * the local↔remote split derived from the server's aggregate rows, and
 * number/date formatting. Deliberately framework-free so they can be
 * unit-tested (see __tests__/stats.test.js).
 *
 * The server (llminferencer.StatsView) provides rows (one per
 * usage × routing × model), a recent-calls list, and totals — everything
 * here renders that payload; nothing recomputes token sums the server
 * already guaranteed.
 */

/**
 * ROUTING describes how one aggregate row / call was routed:
 * - "remote": the llm_inferencer note bound the usage to its provider
 *   (source "note") — remote inference, real provider spend.
 * - "local": the config.toml feature/tier system served it — either the
 *   plain fallback (source "config") or the explicit {local: true} pin
 *   (source "local").
 */
export const ROUTING = {
  note: "remote",
  local: "local",
  config: "local",
};

export const SOURCE_LABELS = {
  note: "Remote · this note (provider)",
  local: "Local · pinned to config.toml",
  config: "Local · config.toml default",
};

/**
 * routingOf maps a source string to its routing kind ("remote" |
 * "local"); unknown sources (future ones) read as remote so they never
 * hide from the local↔remote split.
 */
export function routingOf(source) {
  return ROUTING[source] || "remote";
}

/**
 * sourceLabel renders a human label for a source string.
 */
export function sourceLabel(source) {
  return SOURCE_LABELS[source] || source || "unknown";
}

/**
 * usesProvider reports whether a source is provider-routed — used to
 * decide whether to show the provider name next to a row.
 */
export function usesProvider(source) {
  return source === "note";
}

/**
 * providerText renders the provider column: the provider name for
 * note-routed rows, "" otherwise.
 */
export function providerText(provider) {
  return (provider || "").trim();
}

/**
 * fmtNum: thousands-separated integer (used for token counts and calls).
 */
export function fmtNum(n) {
  const v = Number(n) || 0;
  return v.toLocaleString("en-US");
}

/**
 * fmtTokens: compact token rendering — 12345 → "12.3k", 1230000 →
 * "1.2M", 940 → "940".
 */
export function fmtTokens(n) {
  const v = Number(n) || 0;
  if (Math.abs(v) < 1000) return fmtNum(v);
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  return `${(v / 1000).toFixed(1)}k`;
}

/**
 * fmtDuration: milliseconds as "1.2 s" / "640 ms".
 */
export function fmtDuration(ms) {
  const v = Number(ms) || 0;
  if (v >= 1000) return `${(v / 1000).toFixed(1)} s`;
  return `${fmtNum(v)} ms`;
}

/**
 * fmtWhen renders a SQLite "YYYY-MM-DD HH:MM:SS" stamp (UTC) as a short
 * local date-time; anything unparsable passes through unchanged.
 */
export function fmtWhen(stamp) {
  if (!stamp) return "—";
  // SQLite CURRENT_TIMESTAMP is UTC without a zone marker.
  const d = new Date(stamp.replace(" ", "T") + "Z");
  if (Number.isNaN(d.getTime())) return stamp;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * percentOf renders the share of a value within a total ("42%") —
 * "" when the total is zero instead of a fake 0%.
 */
export function percentOf(value, total) {
  const v = Number(value) || 0;
  const t = Number(total) || 0;
  if (t <= 0) return "";
  return `${Math.round((v / t) * 100)}%`;
}

/**
 * analyticsRows derives the display rows (one per usage label, with
 * routing badge, model, and per-row token totals) from the server's
 * aggregate rows, sorted for a stable, readable table: by usage label,
 * then remote before local, then by calls descending.
 */
export function analyticsRows(rows) {
  const out = (rows || []).map((r) => ({
    key: `${r.usage}|${r.source}|${r.model}`,
    usage: r.usage || "(untagged)",
    label: r.label || r.usage || "(untagged)",
    source: r.source || "",
    routing: routingOf(r.source),
    provider: providerText(r.provider),
    model: r.model || "—",
    calls: Number(r.calls) || 0,
    errors: Number(r.errors) || 0,
    promptTokens: r.prompt_tokens,
    completionTokens: r.completion_tokens,
    totalTokens: r.total_tokens,
    avgDurationMS: r.avg_duration_ms,
    lastUsedAt: r.last_used_at || "",
  }));
  out.sort((a, b) => {
    if (a.label !== b.label) return a.label.localeCompare(b.label);
    if (a.routing !== b.routing) return a.routing === "remote" ? -1 : 1;
    return b.calls - a.calls;
  });
  return out;
}

/**
 * hasStats reports whether a stats payload carries anything worth
 * rendering (rows or recent calls) — gates the whole section.
 */
export function hasStats(stats) {
  if (!stats) return false;
  return (stats.rows?.length || 0) > 0 || (stats.recent?.length || 0) > 0;
}
