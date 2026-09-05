// Incremental chart point strings (PERF-7.4).
//
// Sparklines and throughput graphs re-render every tick. Rebuilding the full
// SVG point string per tick is O(n) string work; worse, hover state used to
// share computations with the polylines so every mousemove re-ran the
// builders. This module keeps the pure builders plus a small cache: the
// cached string is reused whenever the series is unchanged (same length,
// same first/last keys, same scale max), so a quiet tick or a hover-only
// update builds nothing. Appends/drops still rebuild once — the rescale and
// x-shift touch every point — but exactly once per data change, never per
// hover or per unrelated render.

export type PointSample = { at: number | string; downRate: number; upRate: number };

const keyOf = (at: number | string): string => (typeof at === "number" ? String(at) : at);

/** Builds a normalized-rate polyline `points` string over a W×H box. Pure. */
export function buildSeriesPoints(rates: number[], width: number, height: number, pad = 4): string {
  const n = rates.length;
  if (n === 0) return `0,${height - pad} ${width},${height - pad}`;
  let max = 0;
  for (const r of rates) if (r > max) max = r;
  const span = height - 2 * pad;
  const parts = new Array<string>(n);
  for (let i = 0; i < n; i++) {
    const x = n === 1 ? width - 1 : (i / (n - 1)) * width;
    const y = max > 0 ? height - pad - (rates[i] / max) * span : height - pad;
    parts[i] = `${x.toFixed(1)},${y.toFixed(1)}`;
  }
  return parts.join(" ");
}

/** Builds a throughput polyline with an explicit scale max and left gutter.
 * Pure; mirrors the stats/speed chart geometry. */
export function buildScaledPoints(
  values: number[],
  max: number,
  width: number,
  height: number,
  gutter: number,
  padBottom: number,
): string {
  if (!values.length) return "";
  const span = height - padBottom - 8;
  const scale = Math.max(1, max);
  const parts = new Array<string>(values.length);
  for (let i = 0; i < values.length; i++) {
    const x =
      values.length === 1 ? width - 8 : gutter + (i / (values.length - 1)) * (width - gutter);
    const y = 4 + span - (values[i] / scale) * span;
    parts[i] = `${x.toFixed(1)},${y.toFixed(1)}`;
  }
  return parts.join(" ");
}

/** Caches one down/up point-string pair. `update` returns the cached object
 * (same identity) when the series key is unchanged, so downstream memos and
 * DOM bindings observe no update at all on quiet ticks or hover moves. */
export class SeriesPointsCache {
  private key = "";
  private cached: { down: string; up: string } = { down: "", up: "" };
  /** Number of full rebuilds performed (tests and diagnostics). */
  rebuilds = 0;

  update(
    samples: PointSample[],
    geometry: { width: number; height: number; pad?: number },
  ): { down: string; up: string } {
    const pad = geometry.pad ?? 4;
    let maxDown = 0;
    let maxUp = 0;
    for (const s of samples) {
      if (s.downRate > maxDown) maxDown = s.downRate;
      if (s.upRate > maxUp) maxUp = s.upRate;
    }
    const first = samples.length ? keyOf(samples[0].at) : "";
    const last = samples.length ? keyOf(samples[samples.length - 1].at) : "";
    const key = [
      "n",
      samples.length,
      first,
      last,
      maxDown,
      maxUp,
      geometry.width,
      geometry.height,
      pad,
    ].join("\n");
    if (key === this.key) return this.cached;
    const down = new Array<number>(samples.length);
    const up = new Array<number>(samples.length);
    for (let i = 0; i < samples.length; i++) {
      down[i] = samples[i].downRate;
      up[i] = samples[i].upRate;
    }
    this.cached = {
      down: buildSeriesPoints(down, geometry.width, geometry.height, pad),
      up: buildSeriesPoints(up, geometry.width, geometry.height, pad),
    };
    this.key = key;
    this.rebuilds++;
    return this.cached;
  }

  /** Throughput-graph variant with an explicit shared scale max and gutter.
   * Same identity-preserving contract as `update`. */
  updateScaled(
    samples: PointSample[],
    max: number,
    geometry: { width: number; height: number; gutter: number; padBottom: number },
  ): { down: string; up: string } {
    const first = samples.length ? keyOf(samples[0].at) : "";
    const last = samples.length ? keyOf(samples[samples.length - 1].at) : "";
    const key = [
      "s",
      samples.length,
      first,
      last,
      max,
      geometry.width,
      geometry.height,
      geometry.gutter,
      geometry.padBottom,
    ].join("\n");
    if (key === this.key) return this.cached;
    const down = new Array<number>(samples.length);
    const up = new Array<number>(samples.length);
    for (let i = 0; i < samples.length; i++) {
      down[i] = samples[i].downRate;
      up[i] = samples[i].upRate;
    }
    this.cached = {
      down: buildScaledPoints(
        down,
        max,
        geometry.width,
        geometry.height,
        geometry.gutter,
        geometry.padBottom,
      ),
      up: buildScaledPoints(
        up,
        max,
        geometry.width,
        geometry.height,
        geometry.gutter,
        geometry.padBottom,
      ),
    };
    this.key = key;
    this.rebuilds++;
    return this.cached;
  }
}
