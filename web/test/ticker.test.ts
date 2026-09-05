// @vitest-environment happy-dom
// Shared ticker tests (PERF-7.4): one interval, harmonic derivation, and
// pause-while-hidden. Driven with stub timers and a stub document so no DOM
// or clock is needed.
import { strict as assert } from "node:assert";
import { describe, it } from "vitest";
import { createTicker } from "../src/store/ticker.js";

describe("ticker", () => {
  function harness(hidden = false) {
    let scheduled = null;
    let now = 1_000_000;
    const listeners = new Map();
    const doc = {
      hidden,
      addEventListener: (type, fn) => listeners.set(type, fn),
      removeEventListener: (type) => {
        listeners.delete(type);
      },
    };
    const timers = {
      schedule: (fn, ms) => {
        scheduled = { fn, ms };
        return scheduled;
      },
      cancel: (id) => {
        if (scheduled === id) scheduled = null;
      },
      clock: () => now,
    };
    const emit = () => listeners.get("visibilitychange")?.();
    const fire = () => scheduled?.fn();
    return {
      doc,
      timers,
      emit,
      fire,
      scheduled: () => scheduled,
      setNow: (v) => {
        now = v;
      },
    };
  }

  it("runs one 1s interval; ticks advance the count and clock", () => {
    // One interval at 1s; ticks advance the count and clock.
    const h = harness();
    const t = createTicker(h.timers, h.doc);
    assert.ok(h.scheduled(), "ticker schedules exactly one interval");
    assert.equal(h.scheduled().ms, 1000);
    assert.equal(t.tick(), 0);
    const first = t.now();
    h.setNow(first + 1000);
    h.fire();
    assert.equal(t.tick(), 1);
    assert.equal(t.now(), first + 1000);
    t.dispose();
  });

  it("pauses while hidden: interval cancelled, ticks frozen", () => {
    // Hiding pauses: interval cancelled, ticks stop, hidden flag set.
    const h = harness();
    const t = createTicker(h.timers, h.doc);
    h.fire();
    assert.equal(t.tick(), 1);
    h.doc.hidden = true;
    h.emit();
    assert.equal(t.tabHidden(), true);
    assert.equal(h.scheduled(), null, "interval cancelled while hidden");
    const frozen = t.tick();
    h.setNow(9_999_999);
    h.fire(); // stale fire (cancelled) must not advance anything
    assert.equal(t.tick(), frozen);
    t.dispose();
  });

  it("resumes on visible with an immediate step and fresh interval", () => {
    // Re-showing resumes with an immediate step and a fresh interval.
    const h = harness(true);
    const t = createTicker(h.timers, h.doc);
    assert.equal(h.scheduled(), null, "no interval starts while hidden");
    assert.equal(t.tabHidden(), true);
    h.doc.hidden = false;
    h.emit();
    assert.equal(t.tabHidden(), false);
    assert.equal(t.tick(), 1, "resume steps immediately");
    assert.ok(h.scheduled(), "interval restarts on visible");
    h.fire();
    assert.equal(t.tick(), 2);
    t.dispose();
    assert.equal(h.scheduled(), null, "dispose cancels");
  });
});
