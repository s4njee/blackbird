// @vitest-environment happy-dom
// Keyed-store identity and batching semantics (PERF-7.1). Runs in plain
// node: solid-js/store needs no DOM.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { batch, createComputed, createRoot } from "solid-js";
import { createStore } from "solid-js/store";
import { applyDeltaRows, applySnapshotRows, patchRows, restoreRows } from "../src/store/rows.js";

describe("rows", () => {
  const row = (hash, fields = {}) => ({
    hash,
    name: `t-${hash}`,
    sizeBytes: 1000,
    completedBytes: 100,
    downRate: 0,
    upRate: 0,
    label: "",
    ...fields,
  });

  it("keeps row identity across snapshots, deltas, and removals", () => {
    createRoot((dispose) => {
      const [store, setStore] = createStore({});

      // Snapshot stores rows; every row keeps identity across deltas.
      applySnapshotRows(setStore, [row("a"), row("b"), row("c")]);
      const before = { a: store.a, b: store.b, c: store.c };
      assert.ok(before.a && before.b && before.c);

      // A v2 single-field patch updates the value in place: same proxies.
      batch(() =>
        applyDeltaRows(store, setStore, {
          changedPatches: [{ hash: "a", fields: { upRate: 42 } }],
        }),
      );
      assert.equal(store.a, before.a, "changed row keeps identity");
      assert.equal(store.b, before.b, "untouched row keeps identity");
      assert.equal(store.c, before.c, "untouched row keeps identity");
      assert.equal(store.a.upRate, 42);
      assert.equal(store.b.upRate, 0);

      // A v1 whole-row change reconciles in place too.
      batch(() =>
        applyDeltaRows(store, setStore, {
          changed: [{ ...row("b"), downRate: 7, label: "iso" }],
        }),
      );
      assert.equal(store.b, before.b, "v1 row keeps identity");
      assert.equal(store.b.downRate, 7);
      assert.equal(store.b.label, "iso");

      // Patches for unknown hashes are ignored, never fabricated.
      batch(() =>
        applyDeltaRows(store, setStore, {
          changedPatches: [{ hash: "ghost", fields: { upRate: 1 } }],
        }),
      );
      assert.equal(store.ghost, undefined);

      // Removal drops only the removed key.
      const { removed } = applyDeltaRows(store, setStore, { removed: ["c"] });
      assert.deepEqual(removed, ["c"]);
      assert.equal(store.c, undefined);
      assert.equal(store.a, before.a);
      dispose();
    });
  });

  it("batches one flush per message and preserves identity on rollback", () => {
    createRoot((dispose) => {
      const [store, setStore] = createStore({});
      applySnapshotRows(setStore, [row("a"), row("b")]);

      // One flush per message: an effect spanning two rows runs exactly once
      // for a delta that touches both (unbatched writes would run it twice).
      let both = 0;
      let onlyA = 0;
      // createComputed re-runs eagerly (unlike effects, which flush on
      // microtask), so write batching is directly observable.
      createComputed(() => {
        void store.a.upRate;
        void store.b.upRate;
        both++;
      });
      createComputed(() => {
        void store.a.upRate;
        onlyA++;
      });
      both = 0;
      onlyA = 0;
      batch(() =>
        applyDeltaRows(store, setStore, {
          changedPatches: [
            { hash: "a", fields: { upRate: 1 } },
            { hash: "b", fields: { upRate: 2 } },
          ],
        }),
      );
      assert.equal(both, 1, `spanning effect ran ${both}x, want once`);
      assert.equal(onlyA, 1, `single-row effect ran ${onlyA}x, want once`);

      // Optimistic patch + rollback preserve identity throughout.
      const ref = store.a;
      patchRows(store, setStore, ["a"], { label: "iso" });
      assert.equal(store.a, ref);
      assert.equal(store.a.label, "iso");
      restoreRows(setStore, [row("a")]);
      assert.equal(store.a, ref);
      assert.equal(store.a.label, "");
      dispose();
    });
  });
});
