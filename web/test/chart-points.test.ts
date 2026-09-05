// @vitest-environment happy-dom
// Chart point-string cache tests (PERF-7.4): pure builder geometry plus the
// identity-preserving contract (quiet ticks and hover moves build nothing,
// data changes rebuild exactly once).
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { SeriesPointsCache, buildScaledPoints, buildSeriesPoints } from "../src/lib/chartPoints.js";

describe("chart-points", () => {
  it("collapses empty series to the baseline", () => {
    // Empty series collapse to the baseline.
    assert.equal(buildSeriesPoints([], 340, 44, 4), "0,40 340,40");
    assert.equal(buildScaledPoints([], 100, 1180, 220, 0, 8), "");
  });

  it("scales normalized series from baseline to top pad", () => {
    // Normalized scaling: the max maps to the top pad, zero to the baseline.
    const pts = buildSeriesPoints([0, 50, 100], 100, 44, 4).split(" ");
    assert.equal(pts.length, 3);
    const ys = pts.map((p) => Number(p.split(",")[1]));
    assert.equal(ys[0], 40); // zero -> baseline (H - pad)
    assert.equal(ys[2], 4); // max -> top pad
    assert.ok(ys[1] > ys[2] && ys[1] < ys[0], "mid value sits between");
    const xs = pts.map((p) => Number(p.split(",")[0]));
    assert.deepEqual(xs, [0, 50, 100]);
  });

  it("pins scaled geometry to the throughput chart contract", () => {
    // Scaled geometry pins the throughput chart contract (gutter + padBottom).
    const pts = buildScaledPoints([0, 50, 100], 100, 1180, 220, 0, 8).split(" ");
    assert.equal(pts.length, 3);
    assert.equal(pts[0], "0.0,208.0");
    assert.equal(pts[2], "1180.0,4.0");
  });

  it("reuses the cached object on identical re-updates", () => {
    // Cache: first update builds, identical re-update reuses by identity.
    const cache = new SeriesPointsCache();
    const samples = [
      { at: 1000, downRate: 10, upRate: 5 },
      { at: 2000, downRate: 20, upRate: 15 },
    ];
    const first = cache.update(samples, { width: 340, height: 44, pad: 4 });
    assert.equal(cache.rebuilds, 1);
    const second = cache.update(samples, { width: 340, height: 44, pad: 4 });
    assert.equal(cache.rebuilds, 1, "quiet tick must not rebuild");
    assert.ok(first === second, "quiet tick must reuse the cached object");
  });

  it("rebuilds exactly once per append/drop and never on hover", () => {
    // Append + drop (steady-state tick) rebuilds exactly once.
    const cache = new SeriesPointsCache();
    const tick = (base) => [
      { at: base, downRate: 10, upRate: 5 },
      { at: base + 1000, downRate: 20, upRate: 15 },
    ];
    cache.update(tick(1000), { width: 340, height: 44, pad: 4 });
    assert.equal(cache.rebuilds, 1);
    const next = cache.update(tick(2000), { width: 340, height: 44, pad: 4 });
    assert.equal(cache.rebuilds, 2, "append/drop rebuilds once");
    assert.ok(typeof next.down === "string" && typeof next.up === "string");
    // Hover-only update (same samples object) builds nothing.
    cache.update(tick(2000), { width: 340, height: 44, pad: 4 });
    assert.equal(cache.rebuilds, 2, "hover must not rebuild polylines");
  });

  it("rescales on max change in the scaled variant", () => {
    // A max change rescales (new key); scaled variant honors the explicit max.
    const cache = new SeriesPointsCache();
    const a = cache.updateScaled([{ at: "d1", downRate: 10, upRate: 5 }], 100, {
      width: 900,
      height: 180,
      gutter: 46,
      padBottom: 20,
    });
    assert.equal(cache.rebuilds, 1);
    const b = cache.updateScaled([{ at: "d1", downRate: 10, upRate: 5 }], 100, {
      width: 900,
      height: 180,
      gutter: 46,
      padBottom: 20,
    });
    assert.ok(a === b, "same scaled key reuses");
    cache.updateScaled([{ at: "d1", downRate: 10, upRate: 5 }], 200, {
      width: 900,
      height: 180,
      gutter: 46,
      padBottom: 20,
    });
    assert.equal(cache.rebuilds, 2, "max change rebuilds");
  });
});
