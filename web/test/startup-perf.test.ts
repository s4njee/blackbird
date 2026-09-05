// @vitest-environment happy-dom
// Startup data path (PERF-7.5): the first WebSocket snapshot on a 5,000-
// torrent session, through the keyed store, the ordered-view rebuild, and
// the virtual-window slice — the work between the socket message and the
// render commit. Skeleton rows already paint before this; the snapshot must
// resolve to data within 300ms.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { createStore } from "solid-js/store";
import { applySnapshotRows } from "../src/store/rows.js";
import { matchesStatus, parseQuery } from "../src/lib/filter.js";
import { OrderedTorrentView } from "../src/lib/orderedView.js";
import { computeWindow } from "../src/lib/virtualWindow.js";

describe("startup-perf", () => {
  const row = (i) => ({
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
  });

  const FILTER = { status: "", label: "", tracker: "", throttle: "", query: "" };
  const KEYS = [{ column: "downRate", direction: "desc" }];
  const searchYes = () => true;
  const ms = () => Number(process.hrtime.bigint()) / 1e6;

  const list = Array.from({ length: 5000 }, (_, i) => row(i));
  const parsed = parseQuery("");

  // Wall time under parallel workers spikes (GC/JIT/CPU contention): one
  // warmup pass, then retry-on-breach like the other perf guards.
  it("resolves the first snapshot to data within 300ms", { retry: 2 }, () => {
    const [store, setStore] = createStore({});

    applySnapshotRows(setStore, list.slice(0, 100));
    const view = new OrderedTorrentView();
    view.rebuild(
      Object.fromEntries(list.slice(0, 100).map((r) => [r.hash, r])),
      FILTER,
      parsed,
      KEYS,
      matchesStatus,
      searchYes,
    );

    let start = ms();
    applySnapshotRows(setStore, list);
    const snapshotMs = ms() - start;

    start = ms();
    view.rebuild(store, FILTER, parsed, KEYS, matchesStatus, searchYes);
    const viewMs = ms() - start;

    start = ms();
    const rows = [...view.rows];
    const win = computeWindow(rows.length, 0, 600);
    const firstPaint = rows.slice(win.start, win.end);
    const windowMs = ms() - start;

    const totalMs = snapshotMs + viewMs + windowMs;
    console.log(
      `startup/5000: snapshot ${snapshotMs.toFixed(1)}ms, view rebuild ${viewMs.toFixed(1)}ms, ` +
        `window slice ${windowMs.toFixed(1)}ms, total ${totalMs.toFixed(1)}ms (budget 300ms)`,
    );
    assert.equal(view.hashes.length, 5000, "all snapshot rows visible");
    assert.ok(firstPaint.length > 0 && firstPaint.length <= 60, "first paint renders one window");
    assert.ok(totalMs < 300, `snapshot-to-data (${totalMs.toFixed(1)}ms) must fit in 300ms`);
  });
});
