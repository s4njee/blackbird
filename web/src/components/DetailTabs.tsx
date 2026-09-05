import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { formatRate } from "../lib/format";
import { copyText } from "../lib/clipboard";
import type { GeneralFact, GeneralView, LogEntry, LoggerView, SpeedView } from "../lib/types";
import { FlightRecorder } from "./FlightRecorder";
import {
  fetchGeneral,
  fetchLogger,
  fetchSpeed,
  generalViews,
  loggerViews,
  speedViews,
  viewFailed,
} from "../store/session";
import { tickerTick } from "../store/ticker";
import { SeriesPointsCache } from "../lib/chartPoints";
import { showToast } from "../store/ui";

const copyValue = async (value: string) => {
  const ok = await copyText(value);
  showToast(ok ? "Copied." : "Unable to copy — your browser blocked clipboard access.");
};

// ---- General ----

export function GeneralTab(props: { hash: string }) {
  const view = createMemo(() => generalViews()[props.hash] as GeneralView | undefined);
  // Refetches cancel in flight when the focus moves (PERF-7.4).
  createEffect(() => {
    const h = props.hash;
    if (!h) return;
    const controller = new AbortController();
    void fetchGeneral(h, controller.signal);
    onCleanup(() => controller.abort());
  });
  const facts = createMemo(() => view()?.facts ?? []);
  const failed = createMemo(() => viewFailed(props.hash, "general"));
  return (
    <div class="general-tab">
      <Show
        when={!failed()}
        fallback={
          <div class="detail-empty-rows">
            Could not load details.{" "}
            <button type="button" class="copy-button" onClick={() => void fetchGeneral(props.hash)}>
              Retry
            </button>
          </div>
        }
      >
        <Show
          when={view()}
          fallback={
            <div class="detail-skeleton" aria-hidden="true">
              <span />
              <span />
              <span />
            </div>
          }
        >
          <Show
            when={facts().length}
            fallback={<div class="detail-empty-rows">This torrent exposes no details.</div>}
          >
            <For each={facts()}>
              {(fact: GeneralFact) => (
                <div class="general-fact">
                  <span class="general-label">{fact.label}</span>
                  <span class="general-value" title={fact.value}>
                    {fact.value || "—"}
                  </span>
                  <Show when={fact.copy}>
                    <button
                      type="button"
                      class="copy-button"
                      aria-label={`Copy ${fact.label}`}
                      onClick={() => void copyValue(fact.value)}
                    >
                      Copy
                    </button>
                  </Show>
                </div>
              )}
            </For>
          </Show>
        </Show>
      </Show>
    </div>
  );
}

// ---- Speed ----

const W = 900;
const H = 180;
const PAD_X = 46; // left gutter for rate labels
const PAD_BOTTOM = 20;

function rateTicks(maxRate: number): number[] {
  // 0, 25%, 50%, 75%, 100% of max — good enough for the compact panel.
  const step = Math.max(1, maxRate / 4);
  return [0, 1, 2, 3, 4].map((i) => Math.round(i * step));
}

export function SpeedTab(props: { hash: string }) {
  const [hover, setHover] = createSignal<number | null>(null);
  const view = createMemo(() => speedViews()[props.hash] as SpeedView | undefined);
  const samples = createMemo(() => view()?.samples ?? []);
  const speedFailed = createMemo(() => viewFailed(props.hash, "speed"));
  // Refreshes ride the shared 1s ticker (every 2nd tick) instead of a local
  // interval, so hidden tabs fetch nothing (PERF-7.4). In-flight refreshes
  // cancel when the focus moves or the tab unmounts.
  let controller: AbortController | undefined;
  const refresh = (h: string) => {
    controller?.abort();
    controller = new AbortController();
    void fetchSpeed(h, controller.signal);
  };
  createEffect(() => {
    const h = props.hash;
    if (!h) return;
    if (tickerTick() % 2 === 0) refresh(h);
    onCleanup(() => controller?.abort());
  });
  const max = createMemo(() => Math.max(1, ...samples().flatMap((s) => [s.downRate, s.upRate])));
  const peak = createMemo(() =>
    Math.max(0, ...samples().map((s) => Math.max(s.downRate, s.upRate))),
  );
  // Point strings are cached per data key (PERF-7.4): quiet ticks and hover
  // moves reuse the string by identity instead of rebuilding it.
  const cache = new SeriesPointsCache();
  const chart = createMemo(() =>
    cache.updateScaled(samples(), max(), {
      width: W,
      height: H,
      gutter: PAD_X,
      padBottom: PAD_BOTTOM,
    }),
  );
  // Hover resolves through its own memos; moving the mouse never touches the
  // polyline strings above.
  const hoverIndex = createMemo(() => hover());
  const selected = createMemo(() =>
    hoverIndex() === null ? null : (samples()[hoverIndex()!] ?? null),
  );
  const hoverX = createMemo(() =>
    hoverIndex() === null ? 0 : xForHover(hoverIndex()!, samples().length),
  );
  const move = (event: MouseEvent) => {
    const list = samples();
    if (!list.length) return;
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    const frac = (event.clientX - rect.left) / rect.width;
    setHover(Math.max(0, Math.min(list.length - 1, Math.round(frac * (list.length - 1)))));
  };
  return (
    <div class="speed-tab">
      <header class="speed-header">
        <span class="speed-title">Rate — last 60 min</span>
        <span class="speed-peak">peak {formatRate(peak())}</span>
      </header>
      <div class="speed-chart" onMouseMove={move} onMouseLeave={() => setHover(null)}>
        <svg
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="none"
          role="img"
          aria-label="Per-torrent download and upload rate over the last hour"
        >
          <For each={rateTicks(max())}>
            {(tick) => (
              <g>
                <line
                  class="speed-grid"
                  x1={PAD_X}
                  x2={W}
                  y1={yForTick(tick, max())}
                  y2={yForTick(tick, max())}
                />
              </g>
            )}
          </For>
          <polyline class="speed-down" points={chart().down} />
          <polyline class="speed-up" points={chart().up} />
          <Show when={hoverIndex() !== null}>
            <line class="speed-hover" x1={hoverX()} x2={hoverX()} y1="0" y2={H} />
          </Show>
        </svg>
        <Show when={selected()}>
          {(s) => (
            <div class="speed-readout">
              <span>
                {new Date(s().at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
              </span>
              <b class="down">▼ {formatRate(s().downRate)}</b>
              <b class="up">▲ {formatRate(s().upRate)}</b>
            </div>
          )}
        </Show>
        <Show when={!samples().length}>
          <Show
            when={!speedFailed()}
            fallback={
              <div class="detail-empty-rows">
                Could not load rate history.{" "}
                <button
                  type="button"
                  class="copy-button"
                  onClick={() => void fetchSpeed(props.hash)}
                >
                  Retry
                </button>
              </div>
            }
          >
            <Show
              when={view()}
              fallback={
                <div class="detail-skeleton" aria-hidden="true">
                  <span />
                  <span />
                </div>
              }
            >
              <div class="detail-empty-rows">
                Focus this torrent to start collecting rate history.
              </div>
            </Show>
          </Show>
        </Show>
      </div>
      <footer class="speed-footer">
        <span class="tnum">samples {samples().length}</span>
      </footer>
    </div>
  );
}

function yForTick(tick: number, max: number): number {
  const span = H - PAD_BOTTOM - 8;
  return 4 + span - (tick / Math.max(1, max)) * span;
}
function xForHover(index: number, count: number): number {
  if (count < 2) return W - 10;
  return PAD_X + (index / (count - 1)) * (W - PAD_X);
}

// ---- Logger ----

const KIND_LABEL: Record<string, string> = {
  action: "Action",
  message: "Message",
  add: "Add",
  move: "Move",
  complete: "Completed",
};

export function LoggerTab(props: { hash: string }) {
  const [flight, setFlight] = createSignal(false);
  const view = createMemo(() => loggerViews()[props.hash] as LoggerView | undefined);
  createEffect(() => {
    const h = props.hash;
    if (!h) return;
    const controller = new AbortController();
    void fetchLogger(h, controller.signal);
    onCleanup(() => controller.abort());
  });
  const entries = createMemo(() => view()?.entries ?? []);
  const failed = createMemo(() => viewFailed(props.hash, "logger"));
  return (
    <div class="logger-tab">
      <header class="logger-header">
        <span>Activity log</span>
        <button type="button" class="copy-button" onClick={() => setFlight(!flight())}>
          {flight() ? "Activity log" : "Flight recorder"}
        </button>
        <button type="button" class="copy-button" onClick={() => void fetchLogger(props.hash)}>
          Refresh
        </button>
      </header>
      <Show when={flight()}>
        <FlightRecorder hash={props.hash} />
      </Show>
      <Show when={!flight()}>
        <div class="logger-list">
          <Show
            when={!failed()}
            fallback={
              <div class="detail-empty-rows">
                Could not load activity.{" "}
                <button
                  type="button"
                  class="copy-button"
                  onClick={() => void fetchLogger(props.hash)}
                >
                  Retry
                </button>
              </div>
            }
          >
            <Show
              when={view()}
              fallback={
                <div class="detail-skeleton" aria-hidden="true">
                  <span />
                  <span />
                  <span />
                </div>
              }
            >
              <Show
                when={entries().length}
                fallback={
                  <div class="detail-empty-rows">No activity recorded for this torrent yet.</div>
                }
              >
                <For each={entries()}>
                  {(entry: LogEntry) => (
                    <div class="logger-row" classList={{ [`kind-${entry.kind}`]: true }}>
                      <span class="logger-time">{formatLogTime(entry.at)}</span>
                      <span class="logger-kind">{KIND_LABEL[entry.kind] ?? entry.kind}</span>
                      <span class="logger-actor">{entry.actor || "—"}</span>
                      <span class="logger-action">{entry.action || entry.message || "—"}</span>
                      <Show when={entry.result && entry.result !== "info"}>
                        <span
                          class="logger-result"
                          classList={{ failed: entry.result === "failed" }}
                        >
                          {entry.result}
                        </span>
                      </Show>
                    </div>
                  )}
                </For>
              </Show>
            </Show>
          </Show>
        </div>
      </Show>
    </div>
  );
}

function formatLogTime(at: string): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
