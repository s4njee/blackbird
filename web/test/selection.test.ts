// @vitest-environment happy-dom
// Selection identity: pruneSelection and selectedSet are no-ops when
// nothing changed (PERF-7.1), so the per-tick prune call never invalidates
// row state. Runs under the DOM globals (ui.ts reads guarded storage).
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { createComputed, createRoot } from "solid-js";
import {
  focusedHash,
  pruneSelection,
  selectedSet,
  selectOnly,
  toggleSelection,
} from "../src/store/ui.js";

describe("selection", () => {
  it("pruneSelection is a no-op when nothing changed", () => {
    createRoot((dispose) => {
      selectOnly("a");
      toggleSelection("b");
      assert.deepEqual([...selectedSet()].sort(), ["a", "b"]);

      // Pruning against a superset writes nothing: same array, same memo Set.
      const before = selectedSet();
      pruneSelection(new Set(["a", "b", "c"]));
      assert.equal(selectedSet(), before, "selectedSet identity must survive a no-op prune");

      // Pruning a gone row updates once, then stabilizes.
      pruneSelection(new Set(["a"]));
      assert.deepEqual([...selectedSet()], ["a"]);
      assert.notEqual(selectedSet(), before);
      const stable = selectedSet();
      pruneSelection(new Set(["a"]));
      assert.equal(selectedSet(), stable);
      dispose();
    });
  });

  it("does not re-evaluate row-visibility effects on no-op prunes", () => {
    createRoot((dispose) => {
      // A row-visibility effect (the table's per-row selected check) must not
      // re-evaluate when an unrelated tick prunes nothing.
      selectOnly("c");
      let runs = 0;
      createComputed(() => {
        void selectedSet().has("c");
        runs++;
      });
      const initial = runs;
      pruneSelection(new Set(["a", "b", "c"]));
      pruneSelection(new Set(["a", "b", "c"]));
      assert.equal(runs, initial, `effect re-ran ${runs - initial}x on no-op prunes`);
      pruneSelection(new Set(["a", "b"]));
      assert.equal(runs, initial + 1, "effect must re-run exactly once when its row leaves");
      assert.equal(focusedHash(), "", "focus follows the pruned row");
      dispose();
    });
  });
});
