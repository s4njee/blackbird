// @vitest-environment happy-dom
// Virtual window unit tests (PERF-7.3): clamping, spacer math, overscan,
// and the DOM-node budget (window rows * full catalogue width < 1,500).
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import {
  VIRTUAL_OVERSCAN,
  VIRTUAL_ROW_HEIGHT,
  computeWindow,
  detailRowHeight,
  maxWindowRows,
  tableRowHeight,
  DETAIL_ROW_HEIGHT,
  VIRTUALIZE_ABOVE,
} from "../src/lib/virtualWindow.js";

describe("virtual-window", () => {
  it("pins the row height to --h-table-row", () => {
    assert.equal(VIRTUAL_ROW_HEIGHT, 30, "row height must match --h-table-row");
  });

  it("scales row heights by density (THM-9.3, +4px comfortable)", () => {
    assert.equal(tableRowHeight("dense"), 30);
    assert.equal(tableRowHeight("comfortable"), 34);
    assert.equal(detailRowHeight("dense"), 26);
    assert.equal(detailRowHeight("comfortable"), 30);
  });

  it("returns a valid empty window for an empty session", () => {
    // Empty session: valid empty window, no NaN pads.
    assert.deepEqual(computeWindow(0, 0, 600), { start: 0, end: 0, topPad: 0, bottomPad: 0 });
    assert.deepEqual(computeWindow(0, 500, 600).topPad, 0);
  });

  it("opens at the top with overscan below only", () => {
    // Top of a 20,000-row session with a 600px viewport: first 30 rows
    // (20 in view + 10 overscan below), nothing above.
    const w = computeWindow(20000, 0, 600);
    assert.deepEqual(w, { start: 0, end: 30, topPad: 0, bottomPad: (20000 - 30) * 30 });
  });

  it("centers mid-list with symmetric overscan and exact pads", () => {
    // Mid-list scroll: symmetric overscan, pads sum to the hidden rows.
    const w = computeWindow(20000, 3000, 600);
    assert.equal(w.start, 100 - VIRTUAL_OVERSCAN);
    assert.equal(w.end, 120 + VIRTUAL_OVERSCAN);
    assert.equal(w.topPad, w.start * 30);
    assert.equal(w.bottomPad, (20000 - w.end) * 30);
    assert.equal(w.topPad + w.bottomPad, (20000 - (w.end - w.start)) * 30);
  });

  it("clamps at the bottom with no bottom pad", () => {
    // Bottom clamp: window ends at the last row with no bottom pad.
    const w = computeWindow(20000, 20000 * 30, 600);
    assert.equal(w.end, 20000);
    assert.equal(w.bottomPad, 0);
    assert.ok(w.start < w.end, "bottom window is non-empty");
  });

  it("renders small sessions whole with no spacers", () => {
    // Small sessions render whole: no spacers.
    const w = computeWindow(5, 0, 600);
    assert.deepEqual(w, { start: 0, end: 5, topPad: 0, bottomPad: 0 });
  });

  it("clamps hostile inputs instead of producing NaN", () => {
    // Hostile inputs clamp instead of producing NaN.
    let w = computeWindow(100, -50, -10);
    assert.ok(
      w.start === 0 && w.end > 0 && Number.isFinite(w.topPad + w.bottomPad),
      "clamps negatives",
    );
    w = computeWindow(100, NaN, NaN);
    assert.ok(w.start === 0 && w.end > 0, "clamps NaN scroll/viewport");
  });

  it("keeps the DOM-node budget under 1,500 for any viewport", () => {
    // DOM-node budget: worst-case window rows * (27 catalogue columns + check
    // cell + 2 spacer rows) stays under 1,500 regardless of session size.
    for (const viewport of [400, 600, 800, 900]) {
      const rows = maxWindowRows(viewport);
      const nodes = rows * 28 + 2 + 60; // cells + spacer rows + header chrome
      assert.ok(
        nodes < 1500,
        `viewport ${viewport}px renders ${rows} rows (~${nodes} nodes), budget is 1500`,
      );
    }
    // 20,000-row session at 800px: window slice stays at ~47 rows.
    const w = computeWindow(20000, 150000, 800);
    assert.ok(w.end - w.start <= maxWindowRows(800), "window slice bounded");
    assert.equal(
      computeWindow(20000, 150000, 800).end - computeWindow(20000, 150000, 800).start,
      w.end - w.start,
    );
  });

  it("slices a 20k-row window within one frame on average", () => {
    // Window slicing cost on 20k rows: 1,000 slices must stay within one frame
    // each on average (16ms); measures the slice primitive the table runs per
    // scroll/resize, not full DOM rendering (which needs the browser harness).
    const rows = Array.from({ length: 20000 }, (_, i) => i);
    const ms = () => Number(process.hrtime.bigint()) / 1e6;
    const start = ms();
    for (let i = 0; i < 1000; i++) {
      const win = computeWindow(20000, (i * 53) % (20000 * 30), 800);
      const slice = rows.slice(win.start, win.end);
      assert.ok(slice.length <= maxWindowRows(800));
    }
    const elapsed = ms() - start;
    console.log(`virtual-window/20000: 1000 window slices in ${elapsed.toFixed(1)}ms`);
    assert.ok(elapsed / 1000 < 16, "average slice must fit in one frame");
  });

  it("uses detail-list geometry for peers/files rows", () => {
    // Detail-list geometry (PERF-7.4): peers/files rows are 26px, and lists at
    // or below 200 rows render whole with no windowing.
    assert.equal(DETAIL_ROW_HEIGHT, 26, "detail row height must match --h-detail-file-row");
    assert.equal(VIRTUALIZE_ABOVE, 200, "windowing threshold");
    const w = computeWindow(500, 260, 260, VIRTUAL_OVERSCAN, DETAIL_ROW_HEIGHT);
    assert.equal(w.start, 0, "floor(260/26) - overscan clamps at top");
    assert.equal(w.end, 30, "ceil(520/26) + overscan");
    assert.equal(w.topPad, 0);
    assert.equal(w.bottomPad, (500 - 30) * 26);
    const mid = computeWindow(500, 2600, 260, VIRTUAL_OVERSCAN, DETAIL_ROW_HEIGHT);
    assert.equal(mid.topPad, mid.start * 26);
    assert.equal(mid.bottomPad, (500 - mid.end) * 26);
    assert.ok(mid.end - mid.start <= maxWindowRows(260, VIRTUAL_OVERSCAN, DETAIL_ROW_HEIGHT));
  });
});
