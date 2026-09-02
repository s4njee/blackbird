// Formatting helpers. Values are deliberately mirrored from the Go backend's
// formatters (internal/api/rest.go) so REST and WS data read identically.
// Numeric rendering always emits tabular figures via the .tnum class at the
// call site.

const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"] as const;

function trimFloat(v: number): string {
  const s = v.toFixed(2);
  return s.replace(/\.?0+$/, "");
}

/** Splits bytes into a value + unit pair, e.g. 41_200_000 → { value: "41.2", unit: "MB" }. */
export function splitBytes(bytes: number): { value: string; unit: string } {
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < BYTE_UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return { value: trimFloat(v), unit: BYTE_UNITS[i] };
}

/** "6.03 GB" (same algorithm as the backend). */
export function formatBytes(bytes: number): string {
  const { value, unit } = splitBytes(bytes);
  return `${value} ${unit}`;
}

/** Splits a byte rate into value + unit/s, e.g. → { value: "41.2", unit: "MB/s" }. */
export function splitRate(bytesPerSec: number): { value: string; unit: string } {
  if (bytesPerSec <= 0) return { value: "0", unit: "B/s" };
  const { value, unit } = splitBytes(bytesPerSec);
  return { value, unit: `${unit}/s` };
}

/** "41.2 MB/s". */
export function formatRate(bytesPerSec: number): string {
  const { value, unit } = splitRate(bytesPerSec);
  return `${value} ${unit}`;
}

/** Session ratio, two decimals ("2.41"). */
export function formatRatio(ratio: number): string {
  if (!Number.isFinite(ratio)) return "—";
  return ratio.toFixed(2);
}

/** Thousands-separated count ("1,204"). */
export function formatInteger(n: number): string {
  return Math.round(n).toLocaleString("en-US");
}

/** Compact date used by table cells: "12:04 today" or "Sep 2". */
export function formatDate(value: string | number | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return "—";
  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    return `${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })} today`;
  }
  return date.toLocaleDateString([], { month: "short", day: "numeric" });
}

/**
 * Uptime in the compact handoff forms: "41d 06h", "2d 04h", "4m 12s", "0s".
 */
export function formatUptime(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds < 1) return "0s";
  const s = Math.floor(totalSeconds);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${String(h).padStart(2, "0")}h`;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${s}s`;
}

/** Elapsed time since completion; unfinished/invalid torrents show a dash. */
export function formatSeedingTime(finishedAt: string, now = Date.now()): string {
  const finished = Date.parse(finishedAt);
  if (Number.isNaN(finished) || finished <= 0) return "—";
  return formatUptime(Math.max(0, (now - finished) / 1000));
}
