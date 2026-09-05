// Shared 1-second ticker (PERF-7.4).
//
// TrackersTab, StatusBar, SpeedTab, Sidebar, and StatsView each ran their own
// 1s (or harmonic 2s/5s/60s) interval. They now share this single signal: one
// interval per console, with slower refreshes derived from the tick count.
// The ticker pauses while the tab is hidden, which also suspends stats/speed
// polling structurally — no component needs its own hidden check.
//
// `createTicker` takes its timers and document so unit tests can drive it
// with fake clocks; the module singleton below wires the real ones lazily so
// importing this module never touches `window`/`document` (node-safe).

import { createSignal } from "solid-js";

export type TickerTimers = {
  schedule: (fn: () => void, ms: number) => unknown;
  cancel: (id: unknown) => void;
  clock: () => number;
};

export type TickerDoc = {
  readonly hidden: boolean;
  addEventListener: (type: "visibilitychange", listener: () => void) => void;
  removeEventListener?: (type: "visibilitychange", listener: () => void) => void;
};

export type Ticker = {
  now: () => number;
  tick: () => number;
  tabHidden: () => boolean;
  dispose: () => void;
};

export function createTicker(timers: TickerTimers, doc: TickerDoc): Ticker {
  const [now, setNow] = createSignal(timers.clock());
  const [tick, setTick] = createSignal(0);
  const [tabHidden, setTabHidden] = createSignal(doc.hidden === true);
  let id: unknown = null;

  const step = () => {
    setNow(timers.clock());
    setTick((t) => t + 1);
  };
  const start = () => {
    if (id !== null || doc.hidden === true) return;
    id = timers.schedule(step, 1000);
  };
  const stop = () => {
    if (id !== null) {
      timers.cancel(id);
      id = null;
    }
  };
  const onVisibility = () => {
    if (doc.hidden === true) {
      setTabHidden(true);
      stop();
    } else {
      setTabHidden(false);
      step();
      start();
    }
  };
  doc.addEventListener("visibilitychange", onVisibility);
  start();

  return {
    now,
    tick,
    tabHidden,
    dispose: () => {
      stop();
      doc.removeEventListener?.("visibilitychange", onVisibility);
    },
  };
}

let shared: Ticker | null = null;

/** Lazily-created module singleton wired to the real clock and document. */
export function sharedTicker(): Ticker {
  if (!shared) {
    shared = createTicker(
      {
        schedule: (fn, ms) => window.setInterval(fn, ms),
        cancel: (id) => window.clearInterval(id as number),
        clock: () => Date.now(),
      },
      document,
    );
  }
  return shared;
}

/** Milliseconds of the last tick; pauses while the tab is hidden. */
export function tickerNow(): number {
  return sharedTicker().now();
}

/** Monotonic tick count (one per second while visible). Slower refreshes
 * derive from this (`tick() % 5 === 0`); they suspend with the ticker. */
export function tickerTick(): number {
  return sharedTicker().tick();
}

/** True while the tab is hidden (the ticker and all derived polling pause). */
export function isTabHidden(): boolean {
  return sharedTicker().tabHidden();
}
