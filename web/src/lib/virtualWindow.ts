// Virtualized table windowing (PERF-7.3).
//
// Pure logic over plain numbers (no signals/DOM): the table renders only the
// window slice rows[start..end) of the ordered view and reserves the rest
// with top/bottom spacer rows. Row height is the fixed handoff density
// (--h-table-row, 30px); overscan renders a margin above/below the viewport
// so fast scrolls never show blank space.
import type { Density } from "./themes.js";

/** Fixed handoff row height in px (must match `--h-table-row`). */
export const VIRTUAL_ROW_HEIGHT = 30;

/** Fixed row height for detail lists (peers/files rows are `--h-detail-file-row`). */
export const DETAIL_ROW_HEIGHT = 26;

/**
 * Density-aware row heights (THM-9.3): must match the
 * `html[data-density="comfortable"]` overrides in styles/tokens.css
 * (comfortable adds 4px to rows). Components pass resolvedDensity().
 */
export function tableRowHeight(density: Density): number {
  return density === "comfortable" ? VIRTUAL_ROW_HEIGHT + 4 : VIRTUAL_ROW_HEIGHT;
}

export function detailRowHeight(density: Density): number {
  return density === "comfortable" ? DETAIL_ROW_HEIGHT + 4 : DETAIL_ROW_HEIGHT;
}

/** Lists at or below this length render whole; longer lists window. */
export const VIRTUALIZE_ABOVE = 200;

/** Rows rendered above/below the viewport as scroll margin. */
export const VIRTUAL_OVERSCAN = 10;

export type VirtualWindow = {
  /** First visible-view index (inclusive). */
  start: number;
  /** End visible-view index (exclusive). */
  end: number;
  /** Spacer height above the window in px. */
  topPad: number;
  /** Spacer height below the window in px. */
  bottomPad: number;
};

/** Computes the row window for a scroll position. All inputs are clamped so
 * negative scrolls, zero viewports, and empty sessions return an empty (but
 * valid) window instead of NaN. */
export function computeWindow(
  total: number,
  scrollTop: number,
  viewportHeight: number,
  overscan: number = VIRTUAL_OVERSCAN,
  rowHeight: number = VIRTUAL_ROW_HEIGHT,
): VirtualWindow {
  const count = Math.max(0, Math.floor(total));
  const height = rowHeight > 0 ? rowHeight : VIRTUAL_ROW_HEIGHT;
  if (count === 0) return { start: 0, end: 0, topPad: 0, bottomPad: 0 };
  const top = Math.max(0, scrollTop || 0);
  const viewport = viewportHeight > 0 ? viewportHeight : height * 20;
  const over = Math.max(0, Math.floor(overscan));
  const start = Math.max(0, Math.floor(top / height) - over);
  const end = Math.min(count, Math.ceil((top + viewport) / height) + over);
  return {
    start,
    end: Math.max(start, end),
    topPad: start * height,
    bottomPad: (count - Math.max(start, end)) * height,
  };
}

/** Upper bound on rendered data rows for a viewport (used by tests and the
 * DOM-node budget: rows * (columns + 1 check cell) stays under 1,500 with
 * the default 27-column catalogue). The +1 covers the ceil/floor edge when
 * the viewport straddles a row boundary. */
export function maxWindowRows(
  viewportHeight: number,
  overscan: number = VIRTUAL_OVERSCAN,
  rowHeight: number = VIRTUAL_ROW_HEIGHT,
): number {
  const height = rowHeight > 0 ? rowHeight : VIRTUAL_ROW_HEIGHT;
  const viewport = viewportHeight > 0 ? viewportHeight : height * 20;
  return Math.ceil(viewport / height) + Math.max(0, Math.floor(overscan)) * 2 + 1;
}
