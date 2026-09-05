// @vitest-environment happy-dom
// Ordered-view correctness (PERF-7.2): rebuilds and delta-driven updates
// must always agree with a naive full filter+sort. Includes a seeded fuzz
// that checks every step.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { matchesStatus, parseQuery } from "../src/lib/filter.js";
import { compareTorrent } from "../src/lib/sort.js";
import { matchesRow, OrderedTorrentView } from "../src/lib/orderedView.js";

describe("ordered-view", () => {
  const row = (hash, fields = {}) => ({
    hash,
    name: `t-${hash}`,
    sizeBytes: 1000,
    state: "downloading",
    label: "",
    trackerHost: "a.com",
    throttle: "",
    downRate: 0,
    upRate: 0,
    addedAt: "2026-01-01T00:00:00.000Z",
    ...fields,
  });

  const FILTER = { status: "", label: "", tracker: "", throttle: "", query: "" };
  const byName = [{ column: "name", direction: "asc" }];
  const searchYes = () => true;
  const parsed = parseQuery("");

  function naiveView(all, filter, parsedQ, keys) {
    const rows = Object.values(all).filter((r) =>
      matchesRow(r, filter, parsedQ, matchesStatus, searchYes),
    );
    rows.sort((a, b) => compareTorrent(a, b, keys));
    return rows.map((r) => r.hash);
  }

  function mapOf(rows) {
    const all = {};
    for (const r of rows) all[r.hash] = r;
    return all;
  }

  function check(view, all, filter, parsedQ, keys, label) {
    assert.deepEqual(view.hashes, naiveView(all, filter, parsedQ, keys), label);
    assert.deepEqual(
      view.rows.map((r) => r.hash),
      view.hashes,
      `${label}: rows/hashes drift`,
    );
  }

  it("rebuild filters, orders, and tracks membership", () => {
    // Rebuild filters, orders, and tracks membership.
    const view = new OrderedTorrentView();
    const all = mapOf([row("b"), row("a", { label: "iso" }), row("c", { state: "stopped" })]);
    view.rebuild(all, FILTER, parsed, byName, matchesStatus, searchYes);
    assert.deepEqual(view.hashes, ["a", "b", "c"]);
    assert.equal(view.rebuilds, 1);
  });

  it("applies delta updates: add, reposition, leave, remove", () => {
    // Delta updates: add, reposition, leave, remove — each exactly once.
    const view = new OrderedTorrentView();
    const all = mapOf([row("a", { downRate: 1 }), row("b", { downRate: 2 })]);
    const keys = [{ column: "downRate", direction: "desc" }];
    view.rebuild(all, FILTER, parsed, keys, matchesStatus, searchYes);
    assert.deepEqual(view.hashes, ["b", "a"]);

    // Reposition: a jumps ahead.
    all.a = { ...all.a, downRate: 3 };
    assert.equal(
      view.applyChanges(all, ["a"], [], FILTER, parsed, keys, matchesStatus, searchYes),
      true,
    );
    assert.deepEqual(view.hashes, ["a", "b"]);

    // Irrelevant change: same order, still reports change (fields moved) but
    // order holds; a fully quiet delta reports false.
    all.a = { ...all.a, upRate: 9 };
    assert.equal(
      view.applyChanges(all, ["a"], [], FILTER, parsed, keys, matchesStatus, searchYes),
      true,
    );
    assert.deepEqual(view.hashes, ["a", "b"]);
    assert.equal(
      view.applyChanges(all, [], [], FILTER, parsed, keys, matchesStatus, searchYes),
      false,
      "empty delta must be free",
    );

    // Leave and join via the label filter.
    all.b = { ...all.b, label: "iso" };
    assert.equal(
      view.applyChanges(
        all,
        ["b"],
        [],
        { ...FILTER, label: "iso" },
        parsed,
        keys,
        matchesStatus,
        searchYes,
      ),
      true,
    );
    check(view, all, { ...FILTER, label: "iso" }, parsed, keys, "label leave");

    // Removal wins, including changed-and-removed in one tick.
    all.a = { ...all.a, downRate: 99 };
    delete all.b;
    assert.equal(
      view.applyChanges(all, ["a", "b"], ["b"], FILTER, parsed, keys, matchesStatus, searchYes),
      true,
    );
    check(view, all, FILTER, parsed, keys, "removal");
  });

  it("seeded fuzz: every delta-driven step equals a naive recompute", () => {
    // Seeded fuzz: every delta-driven step must equal a naive recompute.
    let seed = 42;
    const rand = (n) => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) % n;
    const all = {};
    for (let i = 0; i < 200; i++) {
      const hash = `h${String(i).padStart(4, "0")}`;
      all[hash] = row(hash, { downRate: rand(1000), label: rand(3) === 0 ? "iso" : "" });
    }
    const view = new OrderedTorrentView();
    const sorts = [
      [{ column: "name", direction: "asc" }],
      [{ column: "downRate", direction: "desc" }],
      [
        { column: "label", direction: "asc" },
        { column: "downRate", direction: "desc" },
      ],
    ];
    let keys = sorts[0];
    let filter = FILTER;
    view.rebuild(all, filter, parsed, keys, matchesStatus, searchYes);
    for (let step = 0; step < 300; step++) {
      const op = rand(100);
      const updated = [];
      const removed = [];
      if (op < 60) {
        // Change a few live rows (fresh objects, as deltas deliver).
        const n = 1 + rand(5);
        for (let k = 0; k < n; k++) {
          const hash = `h${String(rand(200)).padStart(4, "0")}`;
          if (all[hash]) {
            all[hash] = { ...all[hash], downRate: rand(1000), label: rand(2) ? "iso" : "" };
            updated.push(hash);
          }
        }
      } else if (op < 75) {
        const hash = `new-${step}`;
        all[hash] = row(hash, { downRate: rand(100) });
        updated.push(hash);
        if (rand(2)) {
          delete all[hash];
          removed.push(hash);
        }
      } else if (op < 85) {
        const hash = `h${String(rand(200)).padStart(4, "0")}`;
        delete all[hash];
        removed.push(hash);
      } else if (op < 92) {
        filter = rand(2) ? FILTER : { ...FILTER, label: "iso" };
        view.rebuild(all, filter, parsed, keys, matchesStatus, searchYes);
      } else if (op < 97) {
        keys = sorts[rand(sorts.length)];
        view.rebuild(all, filter, parsed, keys, matchesStatus, searchYes);
      } else {
        // No-op tick.
      }
      if (op < 85) {
        view.applyChanges(all, updated, removed, filter, parsed, keys, matchesStatus, searchYes);
      }
      check(view, all, filter, parsed, keys, `fuzz step ${step}`);
    }
  });
});
