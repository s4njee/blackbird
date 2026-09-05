/** Duration helpers for the Settings UI.

 * The settings API reports Go `time.Duration` values as integer nanoseconds
 * (Go's default JSON encoding). The editor presents them as human duration
 * strings ("24h", "90m", "3600s") and converts back to ns on save.
 */

/** Converts a Go nanoseconds integer to a compact "Nh"/"Nm"/"Ns" string. */
export function nsToDuration(value: unknown): string {
  const ns = Number(value);
  if (!Number.isFinite(ns) || ns <= 0) return "";
  const hours = ns / 3.6e12;
  if (Number.isInteger(hours)) return `${hours}h`;
  const minutes = ns / 6e10;
  if (Number.isInteger(minutes)) return `${minutes}m`;
  return `${ns / 1e9}s`;
}

/** Parses "24h", "90m", "3600s" (with optional decimals) into nanoseconds. */
export function durationToNs(value: string): number | null {
  const match = /^\s*(\d+(?:\.\d+)?)\s*(h|m|s)\s*$/.exec(value);
  if (!match) return null;
  const amount = Number(match[1]);
  const unit = match[2];
  const multiplier = unit === "h" ? 3.6e12 : unit === "m" ? 6e10 : 1e9;
  return Math.round(amount * multiplier);
}
