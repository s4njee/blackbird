import { For, Show, createEffect, createSignal, onCleanup, untrack } from "solid-js";
import { formatBytes } from "../lib/format";
import { tickerNow } from "../store/ticker";

export interface StoragePlan {
  generatedAt: string;
  expiresAt: string;
  signature: string;
  status: string;
  pools: {
    id: string;
    paths: string[];
    totalBytes: number;
    freeBytes: number;
    reserveBytes: number;
    additionalLowerBytes: number;
    additionalUpperBytes: number | null;
    peakUsedBytes: number | null;
    freeAfterBytes: number | null;
    status: string;
    peakCause: string;
  }[];
  operations: {
    pool: string;
    path: string;
    description: string;
    logicalBytes: number;
    allocatedBytes: number;
    lowerBytes: number;
    upperBytes: number | null;
    note: string;
  }[];
  unknown: string[];
  coverage: string[];
}
const bytes = (n: number | null) => (n === null ? "Unknown" : formatBytes(n));
const verdict: Record<string, string> = {
  within_bound: "Within modeled bound",
  at_risk: "May exceed headroom",
  insufficient: "Exceeds modeled headroom",
  unknown: "Unknown demand",
};
function savedReserve() {
  try {
    return localStorage.getItem("blackbird.storage.reserveGiB") || "0";
  } catch {
    return "0";
  }
}

export function useStorageForecast(source: { key: () => string; body: () => FormData }) {
  const [plan, setPlan] = createSignal<StoragePlan>();
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [reserve, setReserve] = createSignal(savedReserve());
  const [unknown, setUnknown] = createSignal("");
  const [expansion, setExpansion] = createSignal("");
  let request: AbortController | undefined;
  const key = () => JSON.stringify([source.key(), reserve(), unknown(), expansion()]);
  createEffect(() => {
    key();
    untrack(() => {
      request?.abort();
      setBusy(false);
      setPlan(undefined);
      setError("");
    });
  });
  onCleanup(() => request?.abort());
  async function refresh() {
    request?.abort();
    const active = new AbortController();
    request = active;
    const current = key();
    setBusy(true);
    setError("");
    const timeout = setTimeout(() => active.abort(), 15000);
    try {
      const body = source.body();
      for (const [field, value] of [
        ["reserve_bytes", reserve()],
        ["unknown_bytes", unknown()],
        ["extraction_bytes", expansion()],
      ]) {
        if (value === "") continue;
        const n = Number(value) * 2 ** 30;
        if (!Number.isFinite(n) || n < 0 || n > 2 ** 52)
          throw new Error("Storage assumptions must be nonnegative GiB values up to 4,194,304.");
        body.set(field, String(Math.ceil(n)));
      }
      const response = await fetch("/api/v1/storage/forecast", {
        method: "POST",
        body,
        signal: active.signal,
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error?.message || "Storage forecast unavailable");
      if (active.signal.aborted || current !== key()) return;
      setPlan(result as StoragePlan);
      try {
        localStorage.setItem("blackbird.storage.reserveGiB", reserve());
      } catch {
        /* session preference */
      }
      return result as StoragePlan;
    } catch (e) {
      if (current === key() && request === active)
        setError(
          active.signal.aborted
            ? "Storage inspection interrupted or timed out. Refresh to try again."
            : e instanceof Error
              ? e.message
              : "Storage forecast unavailable",
        );
    } finally {
      clearTimeout(timeout);
      if (request === active) setBusy(false);
    }
  }
  async function ready() {
    const previous = plan();
    const next = await refresh();
    if (!next) return false;
    if (!previous || previous.signature !== next.signature) {
      setError(
        "Review the refreshed forecast, then submit again to continue. " +
          "Unknown sizes are advisory, not a guarantee of available space.",
      );
      return false;
    }
    return true;
  }
  return {
    plan,
    busy,
    error,
    reserve,
    setReserve,
    unknown,
    setUnknown,
    expansion,
    setExpansion,
    refresh,
    ready,
  };
}
export function StorageForecast(props: { model: ReturnType<typeof useStorageForecast> }) {
  const m = props.model;
  let panel: HTMLElement | undefined;
  createEffect(() => {
    if (m.error()) queueMicrotask(() => panel?.scrollIntoView?.({ block: "start" }));
  });
  return (
    <section ref={panel} class="storage-forecast" aria-label="Storage forecast">
      <header>
        <h3>Storage forecast</h3>
        <button type="button" disabled={m.busy()} onClick={() => void m.refresh()}>
          {m.busy() ? "Inspecting…" : "Refresh forecast"}
        </button>
      </header>
      <div class="storage-assumptions">
        <label>
          Reserve per filesystem (GiB)
          <input
            type="number"
            min="0"
            step="any"
            value={m.reserve()}
            onInput={(e) => m.setReserve(e.currentTarget.value)}
          />
        </label>
        <label>
          Unknown batch downloads (GiB, optional)
          <input
            type="number"
            min="0"
            step="any"
            value={m.unknown()}
            onInput={(e) => m.setUnknown(e.currentTarget.value)}
            placeholder="Unknown"
          />
        </label>
        <label>
          Remaining extraction per filesystem (GiB, optional)
          <input
            type="number"
            min="0"
            step="any"
            value={m.expansion()}
            onInput={(e) => m.setExpansion(e.currentTarget.value)}
            placeholder="Unknown"
          />
        </label>
      </div>
      <p>
        Optional values are your upper-bound assumptions. Preview does not fetch magnet metadata or
        remote torrent URLs. Submission refreshes disk evidence.
      </p>
      <Show when={m.error()}>
        <p role="status">{m.error()}</p>
      </Show>
      <Show when={m.plan()}>
        {(plan) => (
          <>
            <p>
              Inspected {new Date(plan().generatedAt).toLocaleTimeString()} ·{" "}
              <Show
                when={tickerNow() > Date.parse(plan().expiresAt)}
                fallback="Valid for 30 seconds"
              >
                Expired — refresh required before starting
              </Show>
            </p>
            <For each={plan().pools}>
              {(pool) => (
                <article class="storage-pool">
                  <h4>{verdict[pool.status] || pool.status}</h4>
                  <p class="storage-paths">
                    Filesystem {pool.id}: {pool.paths.join(" · ")}
                  </p>
                  <dl>
                    <dt>Free now</dt>
                    <dd>{bytes(pool.freeBytes)}</dd>
                    <dt>Reserve</dt>
                    <dd>{bytes(pool.reserveBytes)}</dd>
                    <dt>Additional data growth</dt>
                    <dd>
                      {bytes(pool.additionalLowerBytes)} – {bytes(pool.additionalUpperBytes)}
                    </dd>
                    <dt>Peak projected usage</dt>
                    <dd>
                      {bytes(pool.peakUsedBytes)} / {bytes(pool.totalBytes)}
                    </dd>
                    <dt>Free after modeled peak</dt>
                    <dd>{bytes(pool.freeAfterBytes)}</dd>
                  </dl>
                  <p>
                    Largest peak contributor: {pool.peakCause || "No modeled writes"}. All listed
                    operations may overlap.
                  </p>
                </article>
              )}
            </For>
            <For each={plan().unknown}>
              {(line) => <p class="storage-unknown">Unknown: {line}</p>}
            </For>
            <details>
              <summary>Operations and allocation evidence</summary>
              <For each={plan().operations}>
                {(op) => (
                  <div class="storage-operation">
                    <strong>{op.description}</strong>
                    <p>{op.path}</p>
                    <p>
                      Logical {bytes(op.logicalBytes)} · existing allocation{" "}
                      {bytes(op.allocatedBytes)} · additional {bytes(op.lowerBytes)} –{" "}
                      {bytes(op.upperBytes)}
                    </p>
                    <p>{op.note}</p>
                  </div>
                )}
              </For>
            </details>
            <details>
              <summary>Assumptions and limits</summary>
              <For each={plan().coverage}>{(line) => <p>{line}</p>}</For>
            </details>
          </>
        )}
      </Show>
    </section>
  );
}
