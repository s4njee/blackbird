// Session store: the normalized in-memory client model fed by the WebSocket
// delta stream (Epic 4.2). State is keyed by torrent hash so row identity is
// stable across reorders (Epic 5.2). Consumed by the shell (5.1) and, later,
// the table, detail panel and stats surfaces.

import { batch, createEffect, createMemo, createSignal, untrack } from "solid-js";
import { createStore } from "solid-js/store";
import { applyDeltaRows, applySnapshotRows, patchRows, restoreRows } from "./rows";
import { matchesStatus, parseQuery, type ParsedQuery } from "../lib/filter";
import { OrderedTorrentView, type ViewFilter } from "../lib/orderedView";
import { type SortKey } from "../lib/sort";
import { debouncedQuery, filters, sort } from "./ui";
import type {
  Aggregates,
  AggregatesPatch,
  BitfieldView,
  Delta,
  GeneralView,
  GlobalStats,
  LoggerView,
  RateSample,
  SessionSnapshot,
  SpeedView,
  StringMapPatch,
  TorrentDetail,
  Torrent,
  Volume,
  AutomationNotice,
  ServerNotice,
  WatchNotice,
  WsEnvelope,
} from "../lib/types";
import { TorrentSearchIndex } from "../lib/filter";
import { notify } from "./notifications";
import {
  consumePendingFocus,
  focusedHash,
  navigate,
  restoreSelection,
  selectedHashes,
  selectOnly,
  showToast,
} from "./ui";

export type Connection = "connecting" | "connected" | "disconnected";

const WS_URL = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;

const MAX_HISTORY = 256; // clamped client-side sparkline buffer

const [torrents, setTorrents] = createStore<Record<string, Torrent>>({});
const [globalStats, setGlobalStats] = createSignal<GlobalStats | null>(null);
const [volumes, setVolumes] = createSignal<Volume[]>([]);
const [connection, setConnection] = createSignal<Connection>("connecting");
const [lastError, setLastError] = createSignal("");
const [stale, setStale] = createSignal(false);
const [connectedSince, setConnectedSince] = createSignal("");
const [historySamples, setHistorySamples] = createSignal<RateSample[]>([]);
const [details, setDetails] = createSignal<Record<string, TorrentDetail>>({});
const [generalViews, setGeneralViews] = createSignal<Record<string, GeneralView>>({});
const [speedViews, setSpeedViews] = createSignal<Record<string, SpeedView>>({});
const [loggerViews, setLoggerViews] = createSignal<Record<string, LoggerView>>({});
const [bitfields, setBitfields] = createSignal<Record<string, string>>({}); // hash → bitfield hex
const emptyAggregates = (): Aggregates => ({ status: {}, labels: {}, trackers: {}, throttles: {} });
const [aggregates, setAggregates] = createSignal<Aggregates>(emptyAggregates());
const [loadingDetail, setLoadingDetail] = createSignal("");
const searchIndex = new TorrentSearchIndex();

/** True when the socket is up and the daemon reports connected. */
export const connected = createMemo(() => connection() === "connected");

/** All session rows in store insertion order (no per-tick sort: order
 * consumers use `visibleRows`). Filters, counts, and label lists are
 * order-insensitive, so this stays a cheap values array. */
export const torrentList = createMemo(() => Object.values(torrents));

/** Server-computed sidebar counts, shared with the table's category rules. */
export { aggregates };

/** Total torrent count (mirrors aggregates but handy as a scalar). */
export const torrentCount = createMemo(() => Object.keys(torrents).length);

// Remembered across disconnects (POL-8.6): the selection/focus present on
// the connected→disconnected edge, restored against the live session on
// reconnect. Module scope (not signals): only the snapshot path touches it.
let stashedSelection: string[] = [];
let stashedFocus = "";

function applySnapshot(snap: SessionSnapshot) {
  // A disconnected daemon may legitimately serialize its nil Go slice as
  // null. Treat it as an empty snapshot so a reconnect can recover the UI.
  const list = Array.isArray(snap.torrents) ? snap.torrents : [];
  const wasConnected = connection() === "connected";
  const nowConnected = snap.status === "connected";
  // Session continuity (POL-8.6): stash the selection on the
  // connected→disconnected edge (before pruning clears it) and restore
  // surviving hashes on reconnect after a server restart.
  if (wasConnected && !nowConnected) {
    stashedSelection = untrack(selectedHashes);
    stashedFocus = untrack(focusedHash);
  }
  // One flush per message: every signal write below notifies once (PERF-7.1).
  batch(() => {
    applySnapshotRows(setTorrents, list);
    searchIndex.replace(list);
    setGlobalStats(snap.global);
    setVolumes(Array.isArray(snap.volumes) ? snap.volumes : []);
    setAggregates(snap.aggregates ?? emptyAggregates());
    setConnection(nowConnected ? "connected" : "disconnected");
    setLastError(snap.lastError ?? "");
    setStale(snap.stale);
    setConnectedSince(snap.connectedSince ?? "");
    if (!wasConnected && nowConnected && stashedSelection.length) {
      restoreSelection(
        stashedSelection,
        stashedFocus,
        new Set(list.map((torrent) => torrent.hash)),
      );
      stashedSelection = [];
      stashedFocus = "";
    }
    const pending = consumePendingFocus();
    if (pending && list.some((torrent) => torrent.hash === pending)) {
      selectOnly(pending);
    }
    refreshViewFull();
  });
}

function applyDelta(d: Delta) {
  // One flush per message: every signal write below notifies once (PERF-7.1).
  batch(() => {
    if (d.aggregates) setAggregates(d.aggregates);
    if (d.aggregatesPatch) applyAggregatesPatch(d.aggregatesPatch);
    if (d.status) {
      setConnection(d.status === "connected" ? "connected" : "disconnected");
    }
    if (d.global) {
      setGlobalStats(d.global);
      appendSample(d.global, d.at);
    }
    const { updated, removed } = applyDeltaRows(torrents, setTorrents, d);
    for (const torrent of updated) searchIndex.update(torrent);
    if (removed.length) {
      for (const hash of removed) searchIndex.remove(hash);
      pruneViews(removed);
    }
    refreshViewIncremental(updated, removed);
  });
}

/** Merges a v2 aggregates patch: whole status map replaces, dynamic maps
 * apply updated keys and drop removed ones. */
function applyAggregatesPatch(patch: AggregatesPatch) {
  setAggregates((prev) => {
    const next: Aggregates = {
      status: patch.status ?? prev.status,
      labels: { ...prev.labels },
      trackers: { ...prev.trackers },
      throttles: { ...prev.throttles },
    };
    const groups: Array<[Record<string, number>, StringMapPatch | undefined]> = [
      [next.labels, patch.labels],
      [next.trackers, patch.trackers],
      [next.throttles, patch.throttles],
    ];
    for (const [group, diff] of groups) {
      if (!diff) continue;
      for (const [key, value] of Object.entries(diff.updated ?? {})) group[key] = value;
      for (const key of diff.removed ?? []) delete group[key];
    }
    return next;
  });
}

/** Queries use the incrementally-updated lowercase index, not live row strings. */
export function searchMatches(torrent: Torrent, query: import("../lib/filter").ParsedQuery) {
  return searchIndex.matches(torrent, query);
}

/** The incrementally maintained sorted/filtered view (PERF-7.2): one
 * ordered index over the live session. Deltas update just their rows;
 * snapshots and filter/sort switches rebuild. `visibleRows`/`visibleHashes`
 * only ever change identity when the visible set does, so quiet ticks
 * allocate and notify nothing. */
const view = new OrderedTorrentView();
const [visibleRows, setVisibleRows] = createSignal<Torrent[]>([]);
const [visibleHashes, setVisibleHashes] = createSignal<string[]>([]);
let parsedQuery: { query: string; parsed: ParsedQuery } = { query: "", parsed: parseQuery("") };

function viewFilter(): { filter: ViewFilter; parsed: ParsedQuery; keys: SortKey[] } {
  const filter: ViewFilter = {
    status: filters().status,
    label: filters().label,
    tracker: filters().tracker,
    throttle: filters().throttle,
    query: debouncedQuery(),
  };
  if (filter.query !== parsedQuery.query) {
    parsedQuery = { query: filter.query, parsed: parseQuery(filter.query) };
  }
  return { filter, parsed: parsedQuery.parsed, keys: sort() };
}

/** Publishes the view arrays when they changed (fresh identities so
 * downstream memos and `<For>` observe the update exactly once). */
function publishView() {
  batch(() => {
    setVisibleRows([...view.rows]);
    setVisibleHashes([...view.hashes]);
  });
}

/** Delta-driven refresh: only the delta's rows are visited. Store reads run
 * untracked so row updates never subscribe the caller's computation. */
function refreshViewIncremental(updated: Torrent[], removed: string[]) {
  const { filter, parsed, keys } = viewFilter();
  const hashes = updated.map((row) => row.hash);
  const changed = untrack(() =>
    view.applyChanges(
      torrents,
      hashes,
      removed,
      filter,
      parsed,
      keys,
      matchesStatus,
      searchMatches,
    ),
  );
  if (changed) publishView();
}

/** Full refresh for snapshots and filter/sort/query switches. */
function refreshViewFull() {
  const { filter, parsed, keys } = viewFilter();
  untrack(() => view.rebuild(torrents, filter, parsed, keys, matchesStatus, searchMatches));
  publishView();
}

// Filter/sort/query changes arrive outside deltas: rebuild on any of them.
// Deltas refresh imperatively above.
createEffect(() => {
  filters();
  sort();
  debouncedQuery();
  refreshViewFull();
});

function appendSample(g: GlobalStats, at?: string) {
  const ts = at ? Date.parse(at) : Date.now();
  if (Number.isNaN(ts)) return;
  setHistorySamples((prev) => [
    ...prev.slice(-(MAX_HISTORY - 1)),
    { at: ts, downRate: g.downRate, upRate: g.upRate },
  ]);
}

/** Seeds the sparkline from the server's authoritative 60-minute history. */
async function fetchHistory() {
  try {
    const res = await fetch("/api/v1/stats", { headers: { Accept: "application/json" } });
    if (!res.ok) return;
    const data = (await res.json()) as {
      history?: Array<{ at?: string; downRate?: number; upRate?: number }>;
    };
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
      if (env.hash && env.data)
        setDetails((current) => ({ ...current, [env.hash!]: env.data as TorrentDetail }));
      break;
    case "bitfield":
      if (env.hash && env.data) {
        const view = env.data as BitfieldView;
        if (typeof view.hex === "string" && view.hex)
          setBitfields((current) => ({ ...current, [env.hash!]: view.hex }));
      }
      break;
    case "watch":
      notifyWatch(env.data as WatchNotice | undefined);
      break;
    case "automation":
      notifyAutomation(env.data as AutomationNotice | undefined);
      break;
    case "notice":
      notifyServer(env.data as ServerNotice | undefined);
      break;
    case "pong":
      break;
  }
}

/** Toasts one watch-directory event (PAR-3.1) pushed by the server. */
function notifyWatch(notice: WatchNotice | undefined) {
  if (!notice?.file) return;
  switch (notice.kind) {
    case "loaded":
      showToast(`Watch: ${notice.file} added to session`, { kind: "success" });
      break;
    case "duplicate":
      showToast(`Watch: ${notice.file} is already in the session`);
      break;
    case "malformed":
      showToast(`Watch: ${notice.file} rejected — moved to failed/`, { kind: "error" });
      break;
    case "load_error":
      showToast(`Watch: ${notice.file} failed to load: ${notice.message ?? "unknown error"}`, {
        kind: "error",
      });
      break;
    default:
      showToast(`Watch: ${notice.watchDir}: ${notice.message ?? notice.kind}`, { kind: "warning" });
  }
}

/**
 * Toasts one completion-rule outcome (PAR-3.2). Failures are surfaced per
 * the acceptance criteria; successes land in the torrent's Logger tab only.
 */
function notifyAutomation(notice: AutomationNotice | undefined) {
  if (!notice || notice.kind !== "failed") return;
  const what = notice.torrent || notice.hash;
  showToast(`Rule "${notice.rule}" failed for ${what}: ${notice.message ?? "unknown error"}`, {
    kind: "error",
    browser: { title: "Rule failed", body: `${notice.rule}: ${what}` },
  });
}

/** Surfaces a user-facing server event (POL-8.3): completion toasts carry a
 * View action focusing the torrent; RSS loads record silently (their home
 * is the RSS view) but still raise a browser notification when enabled. */
function notifyServer(notice: ServerNotice | undefined) {
  if (!notice) return;
  if (notice.kind === "completed") {
    const name = notice.title || notice.hash;
    showToast(`${name} completed.`, {
      kind: "success",
      action: {
        label: "View",
        run: () => {
          selectOnly(notice.hash);
          navigate("console");
        },
      },
      browser: { title: "Download complete", body: name },
    });
    return;
  }
  notify(`${notice.title || "Item"} loaded from RSS feed ${notice.message || ""}.`.trim(), {
    kind: "success",
    silent: true,
    browser: { title: "RSS match loaded", body: notice.title },
  });
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
    // Negotiate the v2 delta protocol (PERF-6.2): field patches, filtered
    // globals, aggregate patches. Old servers ignore unknown inbound types,
    // so this is a no-op against them and v1 deltas keep flowing.
    socket.send(JSON.stringify({ type: "hello", version: 2 }));
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

/** Fetches a focused torrent immediately; subsequent WS messages keep it fresh.
 * Takes an optional AbortSignal so focus changes cancel in-flight detail
 * requests instead of letting stale responses overwrite the new focus. */
export async function fetchDetail(hash: string, signal?: AbortSignal) {
  if (!hash) return;
  setLoadingDetail(hash);
  try {
    const response = await fetch(`/api/v1/torrents/${encodeURIComponent(hash)}`, {
      headers: { Accept: "application/json" },
      signal,
    });
    if (!response.ok) throw new Error("detail request failed");
    const detail = (await response.json()) as TorrentDetail;
    if (signal?.aborted) return;
    setDetails((current) => ({ ...current, [hash]: detail }));
  } catch {
    // Aborts and failures both leave older detail in place; live WebSocket
    // retries continue.
  } finally {
    if (!signal?.aborted && loadingDetail() === hash) setLoadingDetail("");
  }
}

/** Optimistic per-file updates, reverted by the next lazy detail tick on error. */
export function patchDetailFile(hash: string, index: number, priority: number) {
  setDetails((current) => {
    const detail = current[hash];
    if (!detail) return current;
    return {
      ...current,
      [hash]: {
        ...detail,
        files: detail.files.map((file) => (file.index === index ? { ...file, priority } : file)),
      },
    };
  });
}

/** Fetch helpers for the PAR-2.5 detail views (General / Speed / Logger).
 * Abortable like `fetchDetail` so tab switches and refocuses cancel.
 * Failures are recorded per view+hash (POL-8.7) so tabs can offer an inline
 * retry instead of showing "Loading…" forever; success clears the flag. */
const [viewErrors, setViewErrors] = createSignal<Record<string, boolean>>({});
export { viewErrors };

function viewErrorKey(hash: string, view: string) {
  return `${view}:${hash}`;
}

export function viewFailed(hash: string, view: "general" | "speed" | "logger"): boolean {
  return viewErrors()[viewErrorKey(hash, view)] === true;
}

async function fetchView<T>(
  hash: string,
  view: "general" | "speed" | "logger",
  apply: (data: T) => void,
  signal?: AbortSignal,
) {
  if (!hash) return;
  const key = viewErrorKey(hash, view);
  try {
    const response = await fetch(`/api/v1/torrents/${encodeURIComponent(hash)}?view=${view}`, {
      headers: { Accept: "application/json" },
      signal,
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    if (signal?.aborted) return;
    apply((await response.json()) as T);
    setViewErrors((cur) => (cur[key] ? { ...cur, [key]: false } : cur));
  } catch (error) {
    if (signal?.aborted) return;
    if (error instanceof DOMException && error.name === "AbortError") return;
    setViewErrors((cur) => ({ ...cur, [key]: true }));
  }
}

export function fetchGeneral(hash: string, signal?: AbortSignal) {
  return fetchView<GeneralView>(
    hash,
    "general",
    (data) => setGeneralViews((cur) => ({ ...cur, [hash]: data })),
    signal,
  );
}
export function fetchSpeed(hash: string, signal?: AbortSignal) {
  return fetchView<SpeedView>(
    hash,
    "speed",
    (data) => setSpeedViews((cur) => ({ ...cur, [hash]: data })),
    signal,
  );
}
export function fetchLogger(hash: string, signal?: AbortSignal) {
  return fetchView<LoggerView>(
    hash,
    "logger",
    (data) => setLoggerViews((cur) => ({ ...cur, [hash]: data })),
    signal,
  );
}

/** Drops per-torrent views for hashes removed from the session. */
export function pruneViews(removed: string[]) {
  if (!removed.length) return;
  setGeneralViews((cur) => {
    const next = { ...cur };
    for (const h of removed) delete next[h];
    return next;
  });
  setSpeedViews((cur) => {
    const next = { ...cur };
    for (const h of removed) delete next[h];
    return next;
  });
  setLoggerViews((cur) => {
    const next = { ...cur };
    for (const h of removed) delete next[h];
    return next;
  });
  setBitfields((cur) => {
    const next = { ...cur };
    for (const h of removed) delete next[h];
    return next;
  });
  setViewErrors((cur) => {
    const next = { ...cur };
    for (const h of removed) {
      delete next[`general:${h}`];
      delete next[`speed:${h}`];
      delete next[`logger:${h}`];
    }
    return next;
  });
}

/** Tab-visibility signal: pauses detail subscriptions while hidden. */
export function setHidden(hidden: boolean) {
  ws?.send(JSON.stringify({ type: "hidden", value: hidden }));
}

/** Applies a local optimistic patch until the next authoritative poll arrives. */
export function patchTorrents(hashes: string[], patch: Partial<Torrent>) {
  batch(() => patchRows(torrents, setTorrents, hashes, patch));
}

/** Replaces exact rows after a failed optimistic request. */
export function restoreTorrents(rows: Torrent[]) {
  batch(() => restoreRows(setTorrents, rows));
}

document.addEventListener("visibilitychange", () => setHidden(document.hidden));

connectWs();

export {
  bitfields,
  connectedSince,
  generalViews,
  globalStats,
  historySamples,
  lastError,
  loggerViews,
  speedViews,
  stale,
  torrents,
  visibleHashes,
  visibleRows,
  volumes,
  connection,
  details,
  loadingDetail,
};
