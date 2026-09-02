// Session store: the normalized in-memory client model fed by the WebSocket
// delta stream (Epic 4.2). State is keyed by torrent hash so row identity is
// stable across reorders (Epic 5.2). Consumed by the shell (5.1) and, later,
// the table, detail panel and stats surfaces.

import { createMemo, createSignal } from "solid-js";
import type {
  Aggregates,
  Delta,
  GlobalStats,
  RateSample,
  SessionSnapshot,
  TorrentDetail,
  Torrent,
  Volume,
  WsEnvelope,
} from "../lib/types";
import { TorrentSearchIndex } from "../lib/filter";

export type Connection = "connecting" | "connected" | "disconnected";

const WS_URL = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;

const MAX_HISTORY = 256; // clamped client-side sparkline buffer

const [torrents, setTorrents] = createSignal<Record<string, Torrent>>({});
const [globalStats, setGlobalStats] = createSignal<GlobalStats | null>(null);
const [volumes, setVolumes] = createSignal<Volume[]>([]);
const [connection, setConnection] = createSignal<Connection>("connecting");
const [lastError, setLastError] = createSignal("");
const [stale, setStale] = createSignal(false);
const [connectedSince, setConnectedSince] = createSignal("");
const [historySamples, setHistorySamples] = createSignal<RateSample[]>([]);
const [details, setDetails] = createSignal<Record<string, TorrentDetail>>({});
const emptyAggregates = (): Aggregates => ({ status: {}, labels: {}, trackers: {} });
const [aggregates, setAggregates] = createSignal<Aggregates>(emptyAggregates());
const [loadingDetail, setLoadingDetail] = createSignal("");
const searchIndex = new TorrentSearchIndex();

/** True when the socket is up and the daemon reports connected. */
export const connected = createMemo(() => connection() === "connected");

/** Torrent list as an array sorted by hash (stable identity). */
export const torrentList = createMemo(() => {
  const map = torrents();
  return Object.keys(map)
    .sort()
    .map((h) => map[h]);
});

/** Server-computed sidebar counts, shared with the table's category rules. */
export { aggregates };

/** Total torrent count (mirrors aggregates but handy as a scalar). */
export const torrentCount = createMemo(() => Object.keys(torrents()).length);

function applySnapshot(snap: SessionSnapshot) {
  const byHash: Record<string, Torrent> = {};
  // A disconnected daemon may legitimately serialize its nil Go slice as
  // null. Treat it as an empty snapshot so a reconnect can recover the UI.
  const list = Array.isArray(snap.torrents) ? snap.torrents : [];
  for (const t of list) byHash[t.hash] = t;
  searchIndex.replace(list);
  setTorrents(byHash);
  setGlobalStats(snap.global);
  setVolumes(Array.isArray(snap.volumes) ? snap.volumes : []);
  setAggregates(snap.aggregates ?? emptyAggregates());
  setConnection(snap.status === "connected" ? "connected" : "disconnected");
  setLastError(snap.lastError ?? "");
  setStale(snap.stale);
  setConnectedSince(snap.connectedSince ?? "");
}

function applyDelta(d: Delta) {
	if (d.aggregates) setAggregates(d.aggregates);
  if (d.status) {
    setConnection(d.status === "connected" ? "connected" : "disconnected");
  }
  if (d.global) {
    setGlobalStats(d.global);
    appendSample(d.global, d.at);
  }
  const removed = d.removed;
  if (Array.isArray(removed) && removed.length) {
    for (const hash of removed) searchIndex.remove(hash);
    setTorrents((prev) => {
      const next = { ...prev };
      for (const h of removed) delete next[h];
      return next;
    });
  }
  const added = d.added;
  const changed = d.changed;
  if ((Array.isArray(added) && added.length) || (Array.isArray(changed) && changed.length)) {
    for (const torrent of [...(added ?? []), ...(changed ?? [])]) searchIndex.update(torrent);
    setTorrents((prev) => {
      const next = { ...prev };
      for (const t of added ?? []) next[t.hash] = t;
      for (const t of changed ?? []) next[t.hash] = t;
      return next;
    });
  }
}

/** Queries use the incrementally-updated lowercase index, not live row strings. */
export function searchMatches(torrent: Torrent, query: import("../lib/filter").ParsedQuery) {
  return searchIndex.matches(torrent, query);
}

function appendSample(g: GlobalStats, at?: string) {
  const ts = at ? Date.parse(at) : Date.now();
  if (Number.isNaN(ts)) return;
  setHistorySamples((prev) => [...prev.slice(-(MAX_HISTORY - 1)), { at: ts, downRate: g.downRate, upRate: g.upRate }]);
}

/** Seeds the sparkline from the server's authoritative 60-minute history. */
async function fetchHistory() {
  try {
    const res = await fetch("/api/stats", { headers: { Accept: "application/json" } });
    if (!res.ok) return;
    const data = (await res.json()) as { history?: Array<{ at?: string; downRate?: number; upRate?: number }> };
    const list = (data.history ?? [])
      .map((s) => ({
        at: s.at ? Date.parse(s.at) : Date.now(),
        downRate: s.downRate ?? 0,
        upRate: s.upRate ?? 0,
      }))
      .filter((s) => !Number.isNaN(s.at));
    setHistorySamples((prev) => (prev.length ? prev : list.slice(-MAX_HISTORY)));
  } catch {
    /* keep whatever samples have accumulated; the WS will refill */
  }
}

function handleMessage(raw: string) {
  let env: WsEnvelope;
  try {
    env = JSON.parse(raw) as WsEnvelope;
  } catch {
    return;
  }
  switch (env.type) {
    case "snapshot":
      applySnapshot(env.data as SessionSnapshot);
      break;
    case "delta":
      applyDelta(env.data as Delta);
      break;
    case "detail":
      if (env.hash && env.data) setDetails((current) => ({ ...current, [env.hash!]: env.data as TorrentDetail }));
      break;
    case "pong":
      break;
  }
}

// ---- WebSocket lifecycle ----

let ws: WebSocket | null = null;
let reconnectTimer: number | null = null;
let retryDelay = 250;
const MAX_RETRY = 15000;

function scheduleReconnect() {
  if (reconnectTimer != null) return;
  const delay = retryDelay;
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null;
    retryDelay = Math.min(retryDelay * 2, MAX_RETRY);
    connectWs();
  }, delay);
}

function connectWs() {
  const socket = new WebSocket(WS_URL);
  ws = socket;
  socket.onopen = () => {
    // A stale socket can finish opening after a newer connection replaced it.
    if (ws !== socket) {
      socket.close();
      return;
    }
    retryDelay = 250;
    if (reconnectTimer != null) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    void fetchHistory();
  };
  socket.onmessage = (ev) => {
    if (ws === socket) handleMessage(String(ev.data));
  };
  socket.onerror = () => {
    if (ws === socket) socket.close();
  };
  socket.onclose = () => {
    if (ws !== socket) return;
    ws = null;
    setConnection("disconnected");
    scheduleReconnect();
  };
}

// ---- Client→server WS commands (protocol v1) ----

/** Subscribe to detailed updates for a focused hash. */
export function sendFocus(hash: string) {
  ws?.send(JSON.stringify({ type: "focus", hash }));
}

/** Unsubscribe from the focused hash. */
export function sendUnfocus() {
  ws?.send(JSON.stringify({ type: "unfocus" }));
}

/** Fetches a focused torrent immediately; subsequent WS messages keep it fresh. */
export async function fetchDetail(hash: string) {
  if (!hash) return;
  setLoadingDetail(hash);
  try {
    const response = await fetch(`/api/torrents/${encodeURIComponent(hash)}`, { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("detail request failed");
    const detail = await response.json() as TorrentDetail;
    setDetails((current) => ({ ...current, [hash]: detail }));
  } catch {
    // The panel retains any older detail and live WebSocket retries continue.
  } finally {
    if (loadingDetail() === hash) setLoadingDetail("");
  }
}

/** Optimistic per-file updates, reverted by the next lazy detail tick on error. */
export function patchDetailFile(hash: string, index: number, priority: number) {
  setDetails((current) => {
    const detail = current[hash];
    if (!detail) return current;
    return { ...current, [hash]: { ...detail, files: detail.files.map((file) => file.index === index ? { ...file, priority } : file) } };
  });
}

/** Tab-visibility signal: pauses detail subscriptions while hidden. */
export function setHidden(hidden: boolean) {
  ws?.send(JSON.stringify({ type: "hidden", value: hidden }));
}

/** Applies a local optimistic patch until the next authoritative poll arrives. */
export function patchTorrents(hashes: string[], patch: Partial<Torrent>) {
  const wanted = new Set(hashes);
  setTorrents((previous) => {
    const next = { ...previous };
    for (const hash of wanted) {
      if (next[hash]) next[hash] = { ...next[hash], ...patch };
    }
    return next;
  });
}

/** Replaces exact rows after a failed optimistic request. */
export function restoreTorrents(rows: Torrent[]) {
  setTorrents((previous) => {
    const next = { ...previous };
    for (const row of rows) next[row.hash] = row;
    return next;
  });
}

document.addEventListener("visibilitychange", () => setHidden(document.hidden));

connectWs();

export {
  connectedSince,
  globalStats,
  historySamples,
  lastError,
  stale,
  torrents,
  volumes,
  connection,
  details,
  loadingDetail,
};
