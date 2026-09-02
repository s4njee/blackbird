import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { formatBytes, formatRate } from "../lib/format";
import { connection, historySamples } from "../store/session";

type Card = { value: string; detail: string };
type Sample = { at: string; downRate: number; upRate: number };
type Volume = { path: string; totalBytes: number; freeBytes: number; usedPercent: number };
type LabelUsage = { label: string; sizeBytes: number; count: number };
type Stats = { cards: { download: Card; upload: Card; sessionRatio: Card; torrents: Card; diskFree: Card }; volumes: Volume[]; labelUsage: LabelUsage[]; history: Sample[] };

const EMPTY: Stats = { cards: { download: { value: "—", detail: "" }, upload: { value: "—", detail: "" }, sessionRatio: { value: "—", detail: "" }, torrents: { value: "—", detail: "" }, diskFree: { value: "—", detail: "" } }, volumes: [], labelUsage: [], history: [] };
const LABELS: Record<string, string> = { iso: "var(--label-iso)", archive: "var(--label-archive)", kernel: "var(--label-kernel)", apps: "var(--label-apps)", media: "var(--label-media)", unlabeled: "var(--state-disabled)" };

function pointList(samples: Sample[], field: "downRate" | "upRate", max: number, width = 1180, height = 220) {
  if (!samples.length) return `0,${height - 8} ${width},${height - 8}`;
  return samples.map((sample, index) => `${samples.length === 1 ? width : (index / (samples.length - 1)) * width},${height - 8 - (sample[field] / Math.max(1, max)) * (height - 16)}`).join(" ");
}

export function StatsView() {
  const [stats, setStats] = createSignal<Stats>(EMPTY);
  const [loading, setLoading] = createSignal(true);
  const [hover, setHover] = createSignal<number | null>(null);
  const serverSamples = createMemo(() => stats().history.map((sample) => ({ ...sample, at: sample.at }))); 
  const liveSamples = createMemo(() => {
    const server = serverSamples();
    if (server.length) return server.slice(-180);
    return historySamples().map((sample) => ({ at: new Date(sample.at).toISOString(), downRate: sample.downRate, upRate: sample.upRate }));
  });
  const max = createMemo(() => Math.max(1, ...liveSamples().flatMap((sample) => [sample.downRate, sample.upRate])));
  const peak = createMemo(() => Math.max(0, ...liveSamples().map((sample) => Math.max(sample.downRate, sample.upRate))));
  const selected = createMemo(() => hover() === null ? null : liveSamples()[hover()!]);
  const cards = createMemo(() => [
    { label: "Download", data: stats().cards.download, class: "down" }, { label: "Upload", data: stats().cards.upload, class: "up" },
    { label: "Session ratio", data: stats().cards.sessionRatio, class: "" }, { label: "Torrents", data: stats().cards.torrents, class: "" }, { label: "Disk free", data: stats().cards.diskFree, class: "" },
  ]);
  const labelMax = createMemo(() => Math.max(1, ...stats().labelUsage.map((item) => item.sizeBytes)));
  async function load() {
    try {
      const response = await fetch("/api/stats", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error();
      const data = await response.json() as Partial<Stats>;
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
    } finally { setLoading(false); }
  }
  onMount(() => {
    void load();
    const interval = window.setInterval(() => void load(), 5000);
    onCleanup(() => window.clearInterval(interval));
  });
  createEffect(() => { if (connection() === "connected") void load(); });
  const move = (event: MouseEvent) => {
    const samples = liveSamples(); if (!samples.length) return;
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    setHover(Math.max(0, Math.min(samples.length - 1, Math.round(((event.clientX - rect.left) / rect.width) * (samples.length - 1)))));
  };
  return <main class="stats-view">
    <section class="stats-cards"><For each={cards()}>{(card) => <article class="stat-card"><span>{card.label}</span><b class={`tnum ${card.class}`}>{card.data.value}</b><small>{card.data.detail}</small></article>}</For></section>
    <section class="throughput-panel"><header><span>Throughput — last 60 min</span><div class="throughput-legend"><i class="down">▼ download</i><i class="up">▲ upload</i></div><b class="tnum">peak {formatRate(peak())}</b></header><div class="throughput-chart" onMouseMove={move} onMouseLeave={() => setHover(null)}>
      <svg viewBox="0 0 1180 220" preserveAspectRatio="none" role="img" aria-label="Download and upload throughput over the last sixty minutes"><line x1="0" x2="1180" y1="55" y2="55" /><line x1="0" x2="1180" y1="110" y2="110" /><line x1="0" x2="1180" y1="165" y2="165" /><polygon class="throughput-area" points={`0,212 ${pointList(liveSamples(), "downRate", max())} 1180,212`} /><polyline class="throughput-down" points={pointList(liveSamples(), "downRate", max())} /><polyline class="throughput-up" points={pointList(liveSamples(), "upRate", max())} /><Show when={selected()}><line class="hover-line" x1={liveSamples().length < 2 ? 1180 : (hover()! / (liveSamples().length - 1)) * 1180} x2={liveSamples().length < 2 ? 1180 : (hover()! / (liveSamples().length - 1)) * 1180} y1="0" y2="220" /></Show></svg>
      <Show when={selected()}>{(sample) => <div class="chart-readout"><span>{new Date(sample().at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span><b class="down">▼ {formatRate(sample().downRate)}</b><b class="up">▲ {formatRate(sample().upRate)}</b></div>}</Show>
    </div></section>
    <section class="stats-bottom"><article class="storage-panel"><h2>Volumes</h2><Show when={stats().volumes.length} fallback={<p class="stats-empty">No watched volumes configured.</p>}><For each={stats().volumes}>{(volume) => <div class="volume-stat"><div><span>{volume.path}</span><b class="tnum">{formatBytes(volume.totalBytes - volume.freeBytes)} of {formatBytes(volume.totalBytes)}</b></div><div class="volume-stat-bar"><i classList={{ alert: volume.usedPercent >= 90, scratch: volume.path.toLowerCase().includes("scratch") }} style={{ width: `${Math.min(100, volume.usedPercent)}%` }} /></div></div>}</For></Show></article>
      <article class="storage-panel"><h2>Space by label</h2><Show when={stats().labelUsage.length} fallback={<p class="stats-empty">No torrent data available.</p>}><For each={stats().labelUsage}>{(item) => <div class="label-stat"><i style={{ background: LABELS[item.label] ?? "var(--bg-track)" }} /><span title={item.label}>{item.label}</span><div><b style={{ width: `${(item.sizeBytes / labelMax()) * 100}%`, background: LABELS[item.label] ?? "var(--text-faint)" }} /></div><small class="tnum">{formatBytes(item.sizeBytes)}</small></div>}</For></Show></article></section>
    <Show when={loading()}><div class="stats-loading">Loading session statistics…</div></Show>
  </main>;
}
