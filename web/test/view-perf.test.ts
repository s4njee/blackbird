// @vitest-environment happy-dom
// View timings on the 5,000-row fixture (PERF-7.2): delta-driven ticks stay
// within an absolute budget with wide headroom. A rebuild-vs-ticks ratio
// guard lived here, but single-digit-ms baselines made any ratio
// load-unstable by construction; precise regression measurement belongs to
// the isolated Go PERF-6.6 bench.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { matchesStatus, parseQuery } from "../src/lib/filter.js";
import { OrderedTorrentView } from "../src/lib/orderedView.js";

describe("view-perf", () => {
  const row = (i, fields = {}) => ({
    hash: `h${String(i).padStart(6, "0")}`,
    name: `torrent-${String(i).padStart(6, "0")}-with-a-plausibly-long-name`,
    sizeBytes: 6474842112,
    state: i % 7 === 6 ? "seeding" : "downloading",
    label: i % 5 === 0 ? "iso" : "",
    trackerHost: i % 3 === 0 ? "a.com" : "b.org",
    throttle: "",
    downRate: 1000 + ((i * 37) % 40000),
    upRate: (i * 13) % 10000,
    addedAt: "2026-01-01T00:00:00.000Z",
    ...fields,
  });

  const FILTER = { status: "", label: "", tracker: "", throttle: "", query: "" };
  const KEYS = [{ column: "downRate", direction: "desc" }];
  const searchYes = () => true;
  const ms = () => Number(process.hrtime.bigint()) / 1e6;

  const all = {};
  for (let i = 0; i < 5000; i++) all[row(i).hash] = row(i);
  const parsed = parseQuery("");

  const view = new OrderedTorrentView();

  // Wall time under parallel workers spikes (GC/JIT/CPU contention), so
  // both sides warm up and compare medians of three, with a built-in retry
  // on breach — same reproduce-to-fail policy as TestPerfRegression.
  // Twenty ticks of 200 changed rows each (fresh objects, as deltas
  // deliver). Batches are pre-built once and reused every round, so no
  // round allocates: the measured work is the incremental merge itself, not
  // test-side object spreads or GC pressure from them. (Each hash is touched
  // exactly once across the 20 batches, so building upfront is identical to
  // building per tick.)
  const batches: Array<{
    hashes: string[];
    rows: Array<{ hash: string; row: (typeof all)[string] }>;
  }> = [];
  for (let tick = 0; tick < 20; tick++) {
    const hashes: string[] = [];
    const rows: Array<{ hash: string; row: (typeof all)[string] }> = [];
    for (let k = 0; k < 200; k++) {
      const i = (tick * 200 + k) % 5000;
      const hash = `h${String(i).padStart(6, "0")}`;
      hashes.push(hash);
      rows.push({ hash, row: { ...all[hash], downRate: all[hash].downRate + 997 } });
    }
    batches.push({ hashes, rows });
  }
  const roundOnce = () => {
    const start = ms();
    for (const batch of batches) {
      for (const { hash, row } of batch.rows) all[hash] = row;
      view.applyChanges(all, batch.hashes, [], FILTER, parsed, KEYS, matchesStatus, searchYes);
    }
    return ms() - start;
  };
  // Wall time under parallel workers spikes (GC/JIT/CPU contention), so one
  // warmup round settles V8 and a built-in retry fails only reproduced
  // breaches. The budget guards against pathological slowdowns; the Go
  // PERF-6.6 bench (isolated runner) owns precise regression measurement.
  it("serves 20 delta ticks within budget", { retry: 2 }, () => {
    roundOnce();
    const incrementalMs = roundOnce();
    console.log(`view/5000: 20 delta ticks ${incrementalMs.toFixed(1)}ms (budget 250ms)`);
    assert.equal(view.hashes.length, 5000);
    assert.ok(
      incrementalMs < 250,
      `20 delta ticks (${incrementalMs.toFixed(1)}ms) must fit in 250ms`,
    );
  });

  it("refilters by label to exactly the matching rows", () => {
    // Filter switches rebuild (label refilter over the same session).
    const labelMs = (() => {
      const started = ms();
      view.rebuild(all, { ...FILTER, label: "iso" }, parsed, KEYS, matchesStatus, searchYes);
      return ms() - started;
    })();
    console.log(`view/5000: label refilter ${labelMs.toFixed(1)}ms, visible ${view.hashes.length}`);
    assert.equal(view.hashes.length, 1000);
  });
});
