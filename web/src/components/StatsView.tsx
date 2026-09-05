import { For, Show, createEffect, createMemo, createSignal, onMount } from "solid-js";
import { formatBytes, formatRate } from "../lib/format";
import { connection, historySamples } from "../store/session";
import { tickerTick } from "../store/ticker";
import { SeriesPointsCache } from "../lib/chartPoints";

type Card = { value: string; detail: string };
type Sample = { at: string; downRate: number; upRate: number };
type Volume = { path: string; totalBytes: number; freeBytes: number; usedPercent: number };
type LabelUsage = { label: string; sizeBytes: number; count: number };
type Stats = {
  cards: { download: Card; upload: Card; sessionRatio: Card; torrents: Card; diskFree: Card };
  volumes: Volume[];
  labelUsage: LabelUsage[];
  history: Sample[];
};
type TrafficDay = { day: string; down: number; up: number };
type TrafficHour = { hour: string; down: number; up: number };
type Host = {
  load1: number;
  load5: number;
  load15: number;
  loadOK: boolean;
  memTotal: number;
  memAvail: number;
  memOK: boolean;
  selfBytes: number;
  selfOK: boolean;
  heapBytes: number;
};

const EMPTY: Stats = {
  cards: {
    download: { value: "—", detail: "" },
    upload: { value: "—", detail: "" },
    sessionRatio: { value: "—", detail: "" },
    torrents: { value: "—", detail: "" },
    diskFree: { value: "—", detail: "" },
  },
  volumes: [],
  labelUsage: [],
  history: [],
};
const EMPTY_HOST: Host = {
  load1: 0,
  load5: 0,
  load15: 0,
  loadOK: false,
  memTotal: 0,
  memAvail: 0,
  memOK: false,
  selfBytes: 0,
  selfOK: false,
  heapBytes: 0,
};
const LABELS: Record<string, string> = {
  iso: "var(--label-iso)",
  archive: "var(--label-archive)",
  kernel: "var(--label-kernel)",
  apps: "var(--label-apps)",
  media: "var(--label-media)",
  unlabeled: "var(--state-disabled)",
};

/** YYYY-MM-DD for a Date in UTC (traffic buckets are UTC). */
function isoDay(date: Date) {
  return date.toISOString().slice(0, 10);
}
function daysAgo(n: number) {
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  d.setUTCDate(d.getUTCDate() - n);
  return isoDay(d);
}

export function StatsView() {
  const [stats, setStats] = createSignal<Stats>(EMPTY);
  const [loading, setLoading] = createSignal(true);
  const [failed, setFailed] = createSignal(false);
  const [hover, setHover] = createSignal<number | null>(null);
  // PAR-5.2 transfer history and host telemetry.
  const [preset, setPreset] = createSignal<"7" | "30" | "90" | "custom">("30");
  const [granularity, setGranularity] = createSignal<"day" | "hour">("day");
  const [from, setFrom] = createSignal(daysAgo(29));
  const [to, setTo] = createSignal(isoDay(new Date()));
  const [hourDay, setHourDay] = createSignal(isoDay(new Date()));
  const [days, setDays] = createSignal<TrafficDay[]>([]);
  const [hours, setHours] = createSignal<TrafficHour[]>([]);
  const [retention, setRetention] = createSignal<number | null>(null);
  const [trafficEmpty, setTrafficEmpty] = createSignal(false);
  const [trafficFailed, setTrafficFailed] = createSignal(false);
  const [trafficLoading, setTrafficLoading] = createSignal(true);
  const [host, setHost] = createSignal<Host>(EMPTY_HOST);
  const [hostFailed, setHostFailed] = createSignal(false);
  const serverSamples = createMemo(() =>
    stats().history.map((sample) => ({ ...sample, at: sample.at })),
  );
  const liveSamples = createMemo(() => {
    const server = serverSamples();
    if (server.length) return server.slice(-180);
    return historySamples().map((sample) => ({
      at: new Date(sample.at).toISOString(),
      downRate: sample.downRate,
      upRate: sample.upRate,
    }));
  });
  const max = createMemo(() =>
    Math.max(1, ...liveSamples().flatMap((sample) => [sample.downRate, sample.upRate])),
  );
  const peak = createMemo(() =>
    Math.max(0, ...liveSamples().map((sample) => Math.max(sample.downRate, sample.upRate))),
  );
  // Throughput point strings are cached per data key (PERF-7.4): hover moves
  // and quiet ticks reuse them by identity instead of rebuilding.
  const chartCache = new SeriesPointsCache();
  const chart = createMemo(() =>
    chartCache.updateScaled(liveSamples(), max(), {
      width: 1180,
      height: 220,
      gutter: 0,
      padBottom: 8,
    }),
  );
  // Hover resolves through its own memos; moving the mouse never touches the
  // polyline strings above.
  const hoverIndex = createMemo(() => hover());
  const selected = createMemo(() =>
    hoverIndex() === null ? null : (liveSamples()[hoverIndex()!] ?? null),
  );
  const hoverX = createMemo(() => {
    const count = liveSamples().length;
    if (hoverIndex() === null || count < 2) return 1180;
    return (hoverIndex()! / (count - 1)) * 1180;
  });
  const cards = createMemo(() => [
    { label: "Download", data: stats().cards.download, class: "down" },
    { label: "Upload", data: stats().cards.upload, class: "up" },
    { label: "Session ratio", data: stats().cards.sessionRatio, class: "" },
    { label: "Torrents", data: stats().cards.torrents, class: "" },
    { label: "Disk free", data: stats().cards.diskFree, class: "" },
  ]);
  const labelMax = createMemo(() =>
    Math.max(1, ...stats().labelUsage.map((item) => item.sizeBytes)),
  );
  // PAR-5.2 host cards: load, memory, and the Blackbird process itself.
  // Volumes keep coming from /api/stats (existing statfs sampler).
  const hostCards = createMemo(() => {
    const h = host();
    const load = h.loadOK
      ? `${h.load1.toFixed(2)} / ${h.load5.toFixed(2)} / ${h.load15.toFixed(2)}`
      : "—";
    const mem = h.memOK
      ? `${formatBytes(h.memTotal - h.memAvail)} of ${formatBytes(h.memTotal)}`
      : "—";
    const memDetail = h.memOK
      ? `${Math.round(((h.memTotal - h.memAvail) / Math.max(1, h.memTotal)) * 100)}% used`
      : "unavailable on this platform";
    const self = h.selfOK ? formatBytes(h.selfBytes) : formatBytes(h.heapBytes);
    const selfDetail = h.selfOK
      ? `RSS · heap ${formatBytes(h.heapBytes)}`
      : "heap only · RSS unavailable";
    return [
      { label: "Load average", data: { value: load, detail: "1 / 5 / 15 min" }, class: "" },
      { label: "Memory", data: { value: mem, detail: memDetail }, class: "" },
      { label: "Blackbird", data: { value: self, detail: selfDetail }, class: "" },
    ];
  });
  const trafficRows = createMemo(() =>
    granularity() === "day"
      ? days().map((d) => ({ label: d.day.slice(5), title: d.day, down: d.down, up: d.up }))
      : hours().map((h) => ({
          label: `${h.hour.slice(11)}:00`,
          title: h.hour,
          down: h.down,
          up: h.up,
        })),
  );
  const trafficMax = createMemo(() =>
    Math.max(1, ...trafficRows().flatMap((row) => [row.down, row.up])),
  );
  const trafficTotals = createMemo(() =>
    trafficRows().reduce((acc, row) => ({ down: acc.down + row.down, up: acc.up + row.up }), {
      down: 0,
      up: 0,
    }),
  );
  const trafficQuery = createMemo(() =>
    granularity() === "day" ? `from=${from()}&to=${to()}` : `granularity=hour&day=${hourDay()}`,
  );
  const csvHref = createMemo(() => `/api/v1/traffic?${trafficQuery()}&format=csv`);
  async function load() {
    try {
      const response = await fetch("/api/v1/stats", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error();
      const data = (await response.json()) as Partial<Stats>;
      // Go's nil slices encode as null. Normalize them at the boundary so an
      // empty configured volume list remains a first-class dashboard state.
      setStats({
        ...EMPTY,
        ...data,
        cards: { ...EMPTY.cards, ...data.cards },
        volumes: Array.isArray(data.volumes) ? data.volumes : [],
        labelUsage: Array.isArray(data.labelUsage) ? data.labelUsage : [],
        history: Array.isArray(data.history) ? data.history : [],
      });
      setFailed(false);
    } catch {
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }
  async function loadHost() {
    try {
      const response = await fetch("/api/v1/host", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error();
      const data = (await response.json()) as Partial<Host>;
      setHost({ ...EMPTY_HOST, ...data });
      setHostFailed(false);
    } catch {
      setHostFailed(true);
    }
  }
  async function loadTraffic() {
    setTrafficLoading(true);
    try {
      const response = await fetch(`/api/v1/traffic?${trafficQuery()}`, {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error();
      const data = (await response.json()) as {
        days?: TrafficDay[];
        hours?: TrafficHour[];
        retentionDays?: number;
      };
      setDays(Array.isArray(data.days) ? data.days : []);
      setHours(Array.isArray(data.hours) ? data.hours : []);
      if (typeof data.retentionDays === "number") setRetention(data.retentionDays);
      setTrafficEmpty(false);
      setTrafficFailed(false);
    } catch {
      setTrafficEmpty(true);
      setTrafficFailed(true);
    } finally {
      setTrafficLoading(false);
    }
  }
  function applyPreset(days: "7" | "30" | "90") {
    setPreset(days);
    setGranularity("day");
    setTo(isoDay(new Date()));
    setFrom(daysAgo(Number(days) - 1));
  }
  async function exportCsv() {
    try {
      const response = await fetch(`/api/v1/traffic?${trafficQuery()}&format=csv`, {
        headers: { Accept: "text/csv" },
      });
      if (!response.ok) throw new Error();
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download =
        granularity() === "day"
          ? `traffic-${from()}-to-${to()}.csv`
          : `traffic-hours-${hourDay()}.csv`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
    } catch {
      /* export failure stays silent; the table already shows the data */
    }
  }
  onMount(() => {
    void load();
    void loadHost();
    void loadTraffic();
  });
  // Refreshes ride the shared 1s ticker (every 5th tick) instead of a local
  // interval; the ticker pauses while the tab is hidden, so stats polling
  // suspends with it (PERF-7.4).
  createEffect(() => {
    if (tickerTick() % 5 === 0) {
      void load();
      void loadHost();
    }
  });
  createEffect(() => {
    if (connection() === "connected") {
      void load();
      void loadHost();
    }
  });
  createEffect(() => {
    void loadTraffic();
  });
  const move = (event: MouseEvent) => {
    const samples = liveSamples();
    if (!samples.length) return;
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    setHover(
      Math.max(
        0,
        Math.min(
          samples.length - 1,
          Math.round(((event.clientX - rect.left) / rect.width) * (samples.length - 1)),
        ),
      ),
    );
  };
  return (
    <main class="stats-view">
      <section class="stats-cards">
        <For each={cards()}>
          {(card) => (
            <article class="stat-card">
              <span>{card.label}</span>
              <b class={`tnum ${card.class}`}>{card.data.value}</b>
              <small>{card.data.detail}</small>
            </article>
          )}
        </For>
      </section>
      <section class="throughput-panel">
        <header>
          <span>Throughput — last 60 min</span>
          <div class="throughput-legend">
            <i class="down">▼ download</i>
            <i class="up">▲ upload</i>
          </div>
          <b class="tnum">peak {formatRate(peak())}</b>
        </header>
        <div class="throughput-chart" onMouseMove={move} onMouseLeave={() => setHover(null)}>
          <svg
            viewBox="0 0 1180 220"
            preserveAspectRatio="none"
            role="img"
            aria-label="Download and upload throughput over the last sixty minutes"
          >
            <line x1="0" x2="1180" y1="55" y2="55" />
            <line x1="0" x2="1180" y1="110" y2="110" />
            <line x1="0" x2="1180" y1="165" y2="165" />
            <polygon class="throughput-area" points={`0,208 ${chart().down} 1180,208`} />
            <polyline class="throughput-down" points={chart().down} />
            <polyline class="throughput-up" points={chart().up} />
            <Show when={selected()}>
              <line class="hover-line" x1={hoverX()} x2={hoverX()} y1="0" y2="220" />
            </Show>
          </svg>
          <Show when={selected()}>
            {(sample) => (
              <div class="chart-readout">
                <span>
                  {new Date(sample().at).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                </span>
                <b class="down">▼ {formatRate(sample().downRate)}</b>
                <b class="up">▲ {formatRate(sample().upRate)}</b>
              </div>
            )}
          </Show>
        </div>
      </section>
      <section class="storage-panel traffic-panel">
        <h2>Traffic history</h2>
        <div class="traffic-controls">
          <div class="traffic-presets" role="group" aria-label="Range preset">
            <button
              type="button"
              classList={{ active: preset() === "7" && granularity() === "day" }}
              onClick={() => applyPreset("7")}
            >
              Week
            </button>
            <button
              type="button"
              classList={{ active: preset() === "30" && granularity() === "day" }}
              onClick={() => applyPreset("30")}
            >
              Month
            </button>
            <button
              type="button"
              classList={{ active: preset() === "90" && granularity() === "day" }}
              onClick={() => applyPreset("90")}
            >
              90 days
            </button>
            <button
              type="button"
              classList={{ active: granularity() === "hour" }}
              onClick={() => setGranularity("hour")}
            >
              Hours
            </button>
          </div>
          <Show
            when={granularity() === "day"}
            fallback={
              <label class="traffic-date">
                Day{" "}
                <input
                  type="date"
                  value={hourDay()}
                  onInput={(event) => setHourDay(event.currentTarget.value)}
                />
              </label>
            }
          >
            <label class="traffic-date">
              From{" "}
              <input
                type="date"
                value={from()}
                onInput={(event) => {
                  setFrom(event.currentTarget.value);
                  setPreset("custom");
                }}
              />
            </label>
            <label class="traffic-date">
              To{" "}
              <input
                type="date"
                value={to()}
                onInput={(event) => {
                  setTo(event.currentTarget.value);
                  setPreset("custom");
                }}
              />
            </label>
          </Show>
          <span class="traffic-totals tnum">
            ▼ {formatBytes(trafficTotals().down)} · ▲ {formatBytes(trafficTotals().up)}
          </span>
          <a
            class="traffic-export"
            href={csvHref()}
            onClick={(event) => {
              event.preventDefault();
              void exportCsv();
            }}
          >
            Export CSV
          </a>
        </div>
        <Show
          when={!trafficEmpty() && trafficRows().length}
          fallback={
            <Show
              when={!trafficLoading()}
              fallback={
                <div class="list-skeleton" aria-hidden="true">
                  <span />
                  <span />
                  <span />
                </div>
              }
            >
              <Show
                when={!trafficFailed()}
                fallback={
                  <p class="stats-empty">
                    Could not load transfer history.{" "}
                    <button type="button" onClick={() => void loadTraffic()}>
                      Retry
                    </button>
                  </p>
                }
              >
                <p class="stats-empty">
                  No transfer history for this range yet — totals accumulate as the daemon reports
                  them.
                </p>
              </Show>
            </Show>
          }
        >
          <For each={trafficRows()}>
            {(row) => (
              <div class="label-stat traffic-row">
                <i class="traffic-dot" />
                <span title={row.title}>{row.label}</span>
                <div>
                  <b
                    class="traffic-down-bar"
                    style={{ width: `${(row.down / trafficMax()) * 100}%` }}
                  />
                  <b
                    class="traffic-up-bar"
                    style={{ width: `${(row.up / trafficMax()) * 100}%` }}
                  />
                </div>
                <small class="tnum">
                  ▼ {formatBytes(row.down)} · ▲ {formatBytes(row.up)}
                </small>
              </div>
            )}
          </For>
        </Show>
        <Show when={retention() !== null}>
          <p class="traffic-retention">Kept {retention()} days · Settings &gt; History</p>
        </Show>
      </section>
      <section class="stats-cards host-cards">
        <For each={hostCards()}>
          {(card) => (
            <article class="stat-card">
              <span>{card.label}</span>
              <b class={`tnum ${card.class}`}>{card.data.value}</b>
              <small>{card.data.detail}</small>
            </article>
          )}
        </For>
      </section>
      <Show when={hostFailed()}>
        <p class="stats-empty">
          Host telemetry unavailable.{" "}
          <button type="button" onClick={() => void loadHost()}>
            Retry
          </button>
        </p>
      </Show>
      <section class="stats-bottom">
        <article class="storage-panel">
          <h2>Volumes</h2>
          <Show
            when={stats().volumes.length}
            fallback={
              <Show
                when={!loading()}
                fallback={
                  <div class="list-skeleton" aria-hidden="true">
                    <span />
                    <span />
                  </div>
                }
              >
                <p class="stats-empty">No watched volumes configured.</p>
              </Show>
            }
          >
            <For each={stats().volumes}>
              {(volume) => (
                <div class="volume-stat">
                  <div>
                    <span>{volume.path}</span>
                    <b class="tnum">
                      {formatBytes(volume.totalBytes - volume.freeBytes)} of{" "}
                      {formatBytes(volume.totalBytes)}
                    </b>
                  </div>
                  <div class="volume-stat-bar">
                    <i
                      classList={{
                        alert: volume.usedPercent >= 90,
                        scratch: volume.path.toLowerCase().includes("scratch"),
                      }}
                      style={{ width: `${Math.min(100, volume.usedPercent)}%` }}
                    />
                  </div>
                </div>
              )}
            </For>
          </Show>
        </article>
        <article class="storage-panel">
          <h2>Space by label</h2>
          <Show
            when={stats().labelUsage.length}
            fallback={<p class="stats-empty">No torrent data available.</p>}
          >
            <For each={stats().labelUsage}>
              {(item) => (
                <div class="label-stat">
                  <i style={{ background: LABELS[item.label] ?? "var(--bg-track)" }} />
                  <span title={item.label}>{item.label}</span>
                  <div>
                    <b
                      style={{
                        width: `${(item.sizeBytes / labelMax()) * 100}%`,
                        background: LABELS[item.label] ?? "var(--text-faint)",
                      }}
                    />
                  </div>
                  <small class="tnum">{formatBytes(item.sizeBytes)}</small>
                </div>
              )}
            </For>
          </Show>
        </article>
      </section>
      <Show when={loading()}>
        <div class="stats-loading">Loading session statistics…</div>
      </Show>
      <Show when={!loading() && failed()}>
        <div class="stats-loading">
          Could not load statistics.{" "}
          <button
            type="button"
            onClick={() => {
              setLoading(true);
              void load();
            }}
          >
            Retry
          </button>
        </div>
      </Show>
    </main>
  );
}
