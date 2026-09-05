import { For, Show, createEffect, createSignal, onCleanup, untrack } from "solid-js";
import type { TorrentExplanation } from "../lib/types";
import { connected } from "../store/session";
import { isTabHidden, tickerNow, tickerTick } from "../store/ticker";
import { DETAIL_TABS, navigate, setDetailTab, type DetailTab } from "../store/ui";

const kindLabels: Record<string, string> = {
  observation: "Observation",
  recorded_action: "Recorded action",
  constraint: "Current control",
  hypothesis: "Possible contributor",
  unknown: "Unknown",
};

const date = (value: string | null) =>
  value ? new Date(value).toLocaleString() : "Observation time unavailable";

export function WhyTab(props: { hash: string }) {
  const [data, setData] = createSignal<TorrentExplanation>();
  const [error, setError] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  const [receivedAt, setReceivedAt] = createSignal(0);
  let controller: AbortController | undefined;

  const refresh = async (hash: string) => {
    controller?.abort();
    const request = new AbortController();
    controller = request;
    if (data()?.hash !== hash) setData(undefined);
    setLoading(true);
    setError("");
    try {
      const response = await fetch(
        `/api/v1/torrents/${encodeURIComponent(hash)}?view=explanation`,
        {
          signal: request.signal,
          headers: { Accept: "application/json" },
        },
      );
      const body = await response.json();
      if (!response.ok) throw new Error(body.error?.message || "Could not load explanations");
      if (!request.signal.aborted) {
        setReceivedAt(Date.now());
        setData(body as TorrentExplanation);
      }
    } catch (cause) {
      if (!request.signal.aborted)
        setError(cause instanceof Error ? cause.message : "Could not load explanations");
    } finally {
      if (!request.signal.aborted) setLoading(false);
    }
  };

  // Shared ticker suspends while hidden. A memo-sized bucket is tracked
  // below so only one request per ten visible seconds is made.
  let lastBucket = -1;
  let lastHash = "";
  createEffect(() => {
    const hash = props.hash;
    const hidden = isTabHidden();
    const bucket = Math.floor(tickerTick() / 10);
    if (hidden) {
      controller?.abort();
      lastBucket = -1;
      return;
    }
    if (hash === lastHash && bucket === lastBucket) return;
    lastHash = hash;
    lastBucket = bucket;
    untrack(() => void refresh(hash));
  });
  onCleanup(() => controller?.abort());

  const stale = () => {
    const value = data();
    if (!value) return false;
    if (value.stale || !connected() || !value.observedAt) return true;
    // Use server-relative sample age so browser/server clock skew cannot
    // make old evidence appear fresh.
    const age = Date.parse(value.generatedAt) - Date.parse(value.observedAt);
    return age + Math.max(0, tickerNow() - receivedAt()) > value.staleAfterSeconds * 1000;
  };

  return (
    <div class="why-tab" aria-label="Torrent explanations" aria-busy={loading()}>
      <header class="why-header">
        <div>
          <h3>
            Why? <span>{data()?.name}</span>
          </h3>
          <p>Contributing factors and the evidence behind them.</p>
        </div>
        <button
          class="copy-button"
          type="button"
          disabled={loading()}
          onClick={() => void refresh(props.hash)}
        >
          {loading() ? "Refreshing…" : "Refresh explanations"}
        </button>
      </header>
      <Show when={error()}>
        <p class="why-warning" role="alert">
          {error()}. <Show when={data()}>Earlier evidence is still displayed.</Show>{" "}
          <button class="copy-button" type="button" onClick={() => void refresh(props.hash)}>
            Retry
          </button>
        </p>
      </Show>
      <Show
        when={data()}
        fallback={
          <p role="status">{error() ? "Explanations unavailable." : "Loading evidence…"}</p>
        }
      >
        {(view) => (
          <>
            <p class="why-freshness" classList={{ "why-warning": stale() }} role="status">
              {stale()
                ? "Stale or disconnected — cached evidence only. "
                : "Cached session evidence. "}
              Observed: {date(view().observedAt)}. Evaluated: {date(view().generatedAt)}.
            </p>
            <For each={view().findings}>
              {(finding) => (
                <article class="why-finding">
                  <div class="why-finding-heading">
                    <span class="why-kind">{kindLabels[finding.kind] ?? finding.kind}</span>
                    <h4>{finding.title}</h4>
                  </div>
                  <p>{finding.summary}</p>
                  <For each={finding.evidence}>
                    {(evidence) => (
                      <p class="why-evidence">
                        <b>{evidence.source}</b> · {date(evidence.observedAt)}
                        <br />
                        {evidence.value}
                      </p>
                    )}
                  </For>
                  <Show when={finding.target}>
                    {(target) => (
                      <button
                        type="button"
                        class="copy-button"
                        title={`For ${view().name}: ${target().label}`}
                        onClick={() => {
                          if (target().kind === "settings") navigate("settings", target().name);
                          else if (DETAIL_TABS.includes(target().name as DetailTab))
                            setDetailTab(target().name as DetailTab);
                        }}
                      >
                        {target().label}
                      </button>
                    )}
                  </Show>
                </article>
              )}
            </For>
            <aside class="why-coverage" aria-label="Evidence coverage">
              <h4>What is not established</h4>
              <ul>
                <For each={view().coverage}>{(note) => <li>{note}</li>}</For>
              </ul>
            </aside>
          </>
        )}
      </Show>
    </div>
  );
}
