// @vitest-environment happy-dom
// DOM-level proof for the keyed store (PERF-7.1): rows render from stable
// per-hash references, so a single-field tick updates exactly one text node
// and an unrelated tick touches nothing.
//
// The renderer mirrors TorrentTable row-for-row: one <tr> per hash reused by
// identity (exactly what Solid's `<For>` guarantees — it reconciles list
// items by reference), with one reactive text binding per cell reading a
// single field, like the table's per-column cells.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { batch, createEffect, createRoot } from "solid-js";
import { createStore } from "solid-js/store";
import { applyDeltaRows, applySnapshotRows } from "../src/store/rows.js";

describe("rows-dom", () => {
  const row = (hash, fields = {}) => ({
    hash,
    name: `t-${hash}`,
    sizeBytes: 1000,
    downRate: 0,
    upRate: 0,
    ...fields,
  });

  const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

  // Keyed row renderer with per-cell reactive bindings. Each row's text
  // effect subscribes only to its own row's field: a tick that leaves the
  // field untouched cannot reach the text node.
  function renderKeyedRows(store, rowsMemo, tbody) {
    const nodes = new Map();
    const sync = () => {
      const seen = new Set();
      for (const item of rowsMemo()) {
        seen.add(item.hash);
        let entry = nodes.get(item.hash);
        if (!entry) {
          const tr = document.createElement("tr");
          tr.dataset.hash = item.hash;
          const td = document.createElement("td");
          td.className = "rate";
          const text = document.createTextNode("");
          td.appendChild(text);
          tr.appendChild(td);
          entry = { tr, text, item };
          nodes.set(item.hash, entry);
          tbody.appendChild(tr);
          createEffect(() => {
            const next = String(entry.item.upRate);
            if (entry.text.textContent !== next) entry.text.textContent = next;
          });
        }
        entry.item = item;
      }
      for (const [hash, entry] of nodes) {
        if (!seen.has(hash)) {
          entry.tr.remove();
          nodes.delete(hash);
        }
      }
    };
    return { nodes, sync };
  }

  it("updates exactly one text node per single-field tick", async () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const tbody = document.createElement("tbody");
    container.appendChild(tbody);

    let store;
    let setStore;
    let rowsMemo;
    const dispose = createRoot((d) => {
      [store, setStore] = createStore({});
      // Mirrors torrentList: sorted array of stable per-hash references.
      rowsMemo = () =>
        Object.keys(store)
          .sort()
          .map((hash) => store[hash]);
      return d;
    });
    const { sync } = renderKeyedRows(store, rowsMemo, tbody);

    applySnapshotRows(setStore, [row("a"), row("b")]);
    sync();
    await tick();
    const trA = tbody.querySelector('[data-hash="a"]');
    const trB = tbody.querySelector('[data-hash="b"]');
    assert.ok(trA && trB, "rows rendered");

    const seen = [];
    const observer = new MutationObserver((records) => seen.push(...records));
    observer.observe(tbody, { childList: true, characterData: true, subtree: true });
    await tick();
    seen.length = 0;

    // Unrelated tick: row b's rate changes. Row b's own cell must update with
    // exactly one text write; row a must stay silent and both <tr> nodes must
    // survive by identity (no row re-evaluation on an unrelated tick).
    batch(() =>
      applyDeltaRows(store, setStore, {
        changedPatches: [{ hash: "b", fields: { upRate: 9 } }],
      }),
    );
    sync();
    await tick();
    // Compare record types/targets only: deep-equal on live DOM records would
    // walk the whole document graph.
    const firstBatch = seen.splice(0);
    assert.deepEqual(
      firstBatch.map((record) => [
        record.type,
        record.target === trB.querySelector(".rate").firstChild,
      ]),
      [["characterData", true]],
      "unrelated tick must touch only row b's text node",
    );
    assert.equal(tbody.querySelector('[data-hash="a"]'), trA, "row a node replaced");
    assert.equal(tbody.querySelector('[data-hash="b"]'), trB, "row b node replaced");
    assert.equal(trB.querySelector(".rate").textContent, "9");

    // Single-field tick on row a: exactly one text-node update.
    batch(() =>
      applyDeltaRows(store, setStore, {
        changedPatches: [{ hash: "a", fields: { upRate: 42 } }],
      }),
    );
    sync();
    await tick();
    const tickKinds = seen.splice(0).map((record) => record.type);
    assert.deepEqual(
      tickKinds,
      ["characterData"],
      `expected 1 text update, got ${JSON.stringify(tickKinds)}`,
    );
    assert.equal(trA.querySelector(".rate").textContent, "42");
    assert.equal(tbody.querySelectorAll("tr").length, 2, "row count changed");

    dispose();
    observer.disconnect();
    container.remove();
  });
});
