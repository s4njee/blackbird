import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { formatBytes, formatRatio, formatRate, formatUptime } from "../lib/format";
import type { Torrent, TorrentDetail, TorrentFile, Tracker } from "../lib/types";
import {
  details,
  fetchDetail,
  globalStats,
  patchDetailFile,
  sendFocus,
  sendUnfocus,
  torrents,
} from "../store/session";
import { confirmDialog, promptDialog } from "../store/dialog";
import { tickerNow } from "../store/ticker";
import { VIRTUALIZE_ABOVE, computeWindow, detailRowHeight } from "../lib/virtualWindow";
import {
  detailPrefs,
  focusedHash,
  resolvedDensity,
  setDetailCollapsed,
  setDetailHeight,
  setDetailTab,
  showToast,
  type DetailTab,
} from "../store/ui";
import { PeersTab } from "./PeersTab";
import { GeneralTab, LoggerTab, SpeedTab } from "./DetailTabs";
import { PiecesTab } from "./PiecesTab";
import { WhyTab } from "./WhyTab";

const TABS: Array<{ id: DetailTab; label: string }> = [
  { id: "files", label: "Files" },
  { id: "peers", label: "Peers" },
  { id: "trackers", label: "Trackers" },
  { id: "general", label: "General" },
  { id: "speed", label: "Speed" },
  { id: "logger", label: "Logger" },
  { id: "transfer", label: "Transfer" },
  { id: "pieces", label: "Pieces" },
  { id: "why", label: "Why?" },
];

async function detailAction(action: string, hash: string, extra: Record<string, unknown> = {}) {
  const response = await fetch("/api/v1/torrents/action", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ action, hashes: [hash], ...extra }),
  });
  const body = (await response.json().catch(() => ({}))) as {
    message?: string;
    results?: Array<{ ok: boolean; error?: string }>;
  };
  if (!response.ok) throw new Error(body.message || "Action failed");
  const failure = body.results?.find((result) => !result.ok);
  if (failure) throw new Error(failure.error || "Action failed");
}

function filePercent(file: TorrentFile) {
  return file.sizeChunks ? Math.min(100, (file.completedChunks / file.sizeChunks) * 100) : 0;
}
function filePriorityLabel(priority: number) {
  return priority === 2 ? "High" : priority === 0 ? "Skip" : "Normal";
}

type TreeNode = {
  name: string;
  path: string;
  depth: number;
  file?: TorrentFile;
  children: TreeNode[];
  size: number;
  completed: number;
};
function fileTree(files: TorrentFile[]) {
  const root: TreeNode = { name: "", path: "", depth: -1, children: [], size: 0, completed: 0 };
  const byPath = new Map<string, TreeNode>([["", root]]);
  for (const file of files) {
    const parts = file.path.split("/").filter(Boolean);
    let parent = root;
    let key = "";
    parts.forEach((part, index) => {
      key += `/${part}`;
      let node = byPath.get(key);
      if (!node) {
        node = { name: part, path: key, depth: index, children: [], size: 0, completed: 0 };
        byPath.set(key, node);
        parent.children.push(node);
      }
      if (index === parts.length - 1) node.file = file;
      parent = node;
    });
  }
  const sum = (node: TreeNode): TreeNode => {
    for (const child of node.children) sum(child);
    if (node.file) {
      node.size = node.file.sizeBytes;
      node.completed = node.file.sizeChunks
        ? (node.file.completedChunks / node.file.sizeChunks) * node.file.sizeBytes
        : 0;
    } else {
      node.size = node.children.reduce((total, child) => total + child.size, 0);
      node.completed = node.children.reduce((total, child) => total + child.completed, 0);
    }
    return node;
  };
  sum(root);
  return root;
}

export function DetailPanel() {
  const hash = focusedHash;
  const torrent = createMemo(() => torrents[hash()]);
  const detail = createMemo(() => (hash() ? details()[hash()] : undefined));
  const prefs = detailPrefs;
  const collapsed = () => prefs().collapsed;
  const panelHeight = () => prefs().height;

  // Focusing subscribes to live detail; unfocusing (hash change, deselect,
  // unmount) cancels the in-flight fetch and tells the server to stop the
  // per-client detail stream for the old hash.
  createEffect(() => {
    const value = hash();
    if (!value) return;
    const controller = new AbortController();
    sendFocus(value);
    void fetchDetail(value, controller.signal);
    onCleanup(() => {
      controller.abort();
      sendUnfocus();
    });
  });

  // Drag-to-resize from the panel's top edge.
  const beginResize = (event: MouseEvent) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = panelHeight();
    const move = (moveEvent: MouseEvent) => {
      // Dragging up grows the panel (height delta is negative of mouse delta).
      setDetailHeight(startHeight + (startY - moveEvent.clientY));
    };
    const end = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", end);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", end, { once: true });
  };

  return (
    <section
      class="detail-panel"
      classList={{ collapsed: collapsed() }}
      style={{ height: collapsed() ? undefined : `${panelHeight()}px` }}
      aria-label="Torrent detail"
    >
      <div class="detail-resize-handle" title="Drag to resize" onMouseDown={beginResize} />
      <header class="detail-tabs">
        <div
          class="detail-tab-list"
          role="tablist"
          aria-label="Torrent detail"
          onKeyDown={(event) => {
            const current = TABS.findIndex((item) => item.id === prefs().tab);
            let next: number | null = null;
            if (event.key === "ArrowRight") next = (current + 1) % TABS.length;
            else if (event.key === "ArrowLeft") next = (current - 1 + TABS.length) % TABS.length;
            else if (event.key === "Home") next = 0;
            else if (event.key === "End") next = TABS.length - 1;
            if (next === null) return;
            event.preventDefault();
            // Owned here: keep tablist motion from reaching global shortcuts
            // (Home/End would otherwise also jump the table selection).
            event.stopPropagation();
            setDetailTab(TABS[next].id);
            (event.currentTarget.querySelectorAll('[role="tab"]')[next] as HTMLElement)?.focus();
          }}
        >
          <For each={TABS}>
            {(item) => (
              <button
                class="detail-tab"
                classList={{ active: prefs().tab === item.id }}
                type="button"
                role="tab"
                aria-selected={prefs().tab === item.id}
                tabindex={prefs().tab === item.id ? 0 : -1}
                onClick={() => setDetailTab(item.id)}
              >
                {item.label}
              </button>
            )}
          </For>
        </div>
        <span class="detail-title" title={torrent()?.name}>
          {torrent()?.name || "Select a torrent to inspect its details"}
        </span>
        <button
          type="button"
          class="detail-collapse"
          aria-label={collapsed() ? "Expand detail panel" : "Collapse detail panel"}
          onClick={() => setDetailCollapsed(!collapsed())}
        >
          {collapsed() ? "▲" : "▼"}
        </button>
      </header>
      <Show
        when={!collapsed()}
        fallback={<div class="detail-empty">Detail panel collapsed — click ▲ to expand.</div>}
      >
        <Show
          when={torrent()}
          fallback={
            <div class="detail-empty">
              Select a torrent to inspect files, peers, trackers, transfer totals, and pieces.
            </div>
          }
        >
          {(current) => (
            <div class="detail-body">
              <div
                class="detail-content"
                role="tabpanel"
                aria-label={TABS.find((item) => item.id === prefs().tab)?.label ?? "Details"}
              >
                <Show when={prefs().tab === "why"}>
                  <WhyTab hash={current().hash} />
                </Show>
                <Show when={prefs().tab !== "why"}>
                  <Show when={detail()} fallback={<DetailSkeleton />}>
                    {(data) => (
                      <>
                        <Show when={prefs().tab === "files"}>
                          <FilesTab hash={current().hash} detail={data()} />
                        </Show>
                        <Show when={prefs().tab === "peers"}>
                          <PeersTab hash={current().hash} detail={data()} />
                        </Show>
                        <Show when={prefs().tab === "trackers"}>
                          <TrackersTab hash={current().hash} detail={data()} />
                        </Show>
                        <Show when={prefs().tab === "general"}>
                          <GeneralTab hash={current().hash} />
                        </Show>
                        <Show when={prefs().tab === "speed"}>
                          <SpeedTab hash={current().hash} />
                        </Show>
                        <Show when={prefs().tab === "logger"}>
                          <LoggerTab hash={current().hash} />
                        </Show>
                        <Show when={prefs().tab === "transfer"}>
                          <TransferTab torrent={current()} detail={data()} />
                        </Show>
                        <Show when={prefs().tab === "pieces"}>
                          <PiecesTab hash={current().hash} detail={data()} />
                        </Show>
                      </>
                    )}
                  </Show>
                </Show>
              </div>
              <Facts torrent={current()} detail={detail()} />
            </div>
          )}
        </Show>
      </Show>
    </section>
  );
}

function DetailSkeleton() {
  return (
    <div class="detail-skeleton">
      <span />
      <span />
      <span />
      <span />
    </div>
  );
}

function Facts(props: { torrent: Torrent; detail?: TorrentDetail }) {
  const transfer = () => props.detail?.transfer;
  const facts = createMemo(() => [
    ["Hash", props.torrent.hash],
    ["Downloaded", formatBytes(transfer()?.downloadedBytes ?? props.torrent.completedBytes)],
    ["Uploaded", formatBytes(transfer()?.uploadedBytes ?? 0)],
    ["Ratio", formatRatio(props.torrent.ratio)],
    [
      "Pieces",
      transfer()
        ? `${transfer()!.chunksDone.toLocaleString()} / ${transfer()!.chunkCount.toLocaleString()}`
        : "—",
    ],
    ["Peers", `${props.torrent.seeds || "—"} / ${props.torrent.peers || "—"}`],
    ["Down rate", formatRate(props.torrent.downRate), "down"],
    ["Up rate", formatRate(props.torrent.upRate), "up"],
    ["Path", transfer()?.directory || props.torrent.basePath],
    ["Added", new Date(props.torrent.addedAt).toLocaleString()],
    ["Private", props.torrent.isPrivate ? "Yes" : "No"],
  ]);
  return (
    <aside class="detail-facts">
      <For each={facts()}>
        {(fact) => (
          <div class="fact">
            <span>{fact[0]}</span>
            <b
              classList={{ "rate-down": fact[2] === "down", "rate-up": fact[2] === "up" }}
              title={String(fact[1])}
            >
              {fact[1]}
            </b>
          </div>
        )}
      </For>
    </aside>
  );
}

function FilesTab(props: { hash: string; detail: TorrentDetail }) {
  const [collapsed, setCollapsed] = createSignal(new Set<string>());
  const root = createMemo(() => fileTree(props.detail.files));
  const flattened = createMemo(() => {
    const rows: TreeNode[] = [];
    const visit = (node: TreeNode) => {
      for (const child of node.children) {
        rows.push(child);
        if (child.children.length && !collapsed().has(child.path)) visit(child);
      }
    };
    visit(root());
    return rows;
  });
  // Virtualized above 200 rows (PERF-7.4): the tree order from `flattened`
  // is already view order, so the window slices it directly. Small torrents
  // render whole with no spacers.
  let rowsRef: HTMLDivElement | undefined;
  const [scrollTop, setScrollTop] = createSignal(0);
  const [viewportHeight, setViewportHeight] = createSignal(300);
  const virtualized = createMemo(() => flattened().length > VIRTUALIZE_ABOVE);
  const windowed = createMemo(() =>
    virtualized()
      ? computeWindow(
          flattened().length,
          scrollTop(),
          viewportHeight(),
          undefined,
          detailRowHeight(resolvedDensity()),
        )
      : { start: 0, end: flattened().length, topPad: 0, bottomPad: 0 },
  );
  const windowRows = createMemo(() => flattened().slice(windowed().start, windowed().end));
  const setPriority = async (files: TorrentFile[], current?: number) => {
    const next = current === 2 ? 1 : current === 1 ? 0 : 2;
    for (const file of files) patchDetailFile(props.hash, file.index, next);
    try {
      await Promise.all(
        files.map((file) =>
          detailAction("file_priority", props.hash, { fileIndex: file.index, priority: next }),
        ),
      );
      showToast(`Priority set to ${filePriorityLabel(next)}.`);
    } catch (error) {
      void fetchDetail(props.hash);
      showToast(error instanceof Error ? error.message : "Could not set priority");
    }
  };
  const descendants = (node: TreeNode): TorrentFile[] =>
    node.file ? [node.file] : node.children.flatMap(descendants);
  onMount(() => {
    const el = rowsRef;
    if (!el) return;
    setViewportHeight(el.clientHeight || 300);
    let ticking = false;
    const onScroll = () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => {
        ticking = false;
        if (rowsRef) setScrollTop(rowsRef.scrollTop);
      });
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    const observer = new ResizeObserver(() => {
      if (rowsRef) setViewportHeight(rowsRef.clientHeight || 300);
    });
    observer.observe(el);
    onCleanup(() => {
      el.removeEventListener("scroll", onScroll);
      observer.disconnect();
    });
  });
  return (
    <div class="detail-table files-table">
      <div class="detail-header file-grid">
        <span>File</span>
        <span>Size</span>
        <span>Progress</span>
        <span>Done</span>
        <span>Priority</span>
      </div>
      <div class="detail-rows" ref={rowsRef} classList={{ "is-virtualized": virtualized() }}>
        <Show
          when={props.detail.files.length}
          fallback={
            <div class="detail-empty-rows">
              This torrent does not expose a file list yet.{" "}
              <button
                type="button"
                class="copy-button"
                onClick={() => void fetchDetail(props.hash)}
              >
                Retry
              </button>
            </div>
          }
        >
          <Show when={windowed().topPad > 0}>
            <div
              class="virtual-row-spacer"
              aria-hidden="true"
              style={{ height: `${windowed().topPad}px` }}
            />
          </Show>
          <For each={windowRows()}>
            {(node) => {
              const percent = node.file
                ? filePercent(node.file)
                : node.size
                  ? (node.completed / node.size) * 100
                  : 0;
              const isDir = node.children.length > 0;
              return (
                <div
                  class="file-grid detail-row"
                  classList={{ skipped: node.file?.priority === 0 }}
                >
                  <button
                    class="file-name"
                    type="button"
                    style={{ "--depth": String(node.depth) }}
                    onClick={() =>
                      isDir
                        ? setCollapsed((old) => {
                            const next = new Set(old);
                            if (next.has(node.path)) next.delete(node.path);
                            else next.add(node.path);
                            return next;
                          })
                        : undefined
                    }
                  >
                    <i>{isDir ? (collapsed().has(node.path) ? "▸" : "▾") : "·"}</i>
                    {node.name}
                  </button>
                  <span class="numeric">{formatBytes(node.size)}</span>
                  <Progress percent={percent} />
                  <span class="numeric">{formatBytes(node.completed)}</span>
                  <button
                    class={`priority-chip p${node.file?.priority ?? 1}`}
                    type="button"
                    onClick={() => void setPriority(descendants(node), node.file?.priority)}
                  >
                    {node.file ? filePriorityLabel(node.file.priority) : "Set all"}
                  </button>
                </div>
              );
            }}
          </For>
          <Show when={windowed().bottomPad > 0}>
            <div
              class="virtual-row-spacer"
              aria-hidden="true"
              style={{ height: `${windowed().bottomPad}px` }}
            />
          </Show>
        </Show>
      </div>
    </div>
  );
}

function Progress(props: { percent: number }) {
  return (
    <div class="progress-wrap small">
      <div class="small-progress" aria-hidden="true">
        <span
          classList={{ complete: props.percent >= 100 }}
          style={{ width: `${Math.min(100, props.percent)}%` }}
        />
      </div>
      <b class="progress-pct tnum">
        {props.percent >= 100 ? "100%" : `${props.percent.toFixed(1)}%`}
      </b>
    </div>
  );
}

function TrackersTab(props: { hash: string; detail: TorrentDetail }) {
  // Countdown clock comes from the shared 1s ticker (PERF-7.4), not a local
  // interval, so hidden tabs cost nothing here.
  const next = (tracker: Tracker) =>
    tracker.nextAnnounceAt
      ? formatUptime(Math.max(0, (Date.parse(tracker.nextAnnounceAt) - tickerNow()) / 1000))
      : "—";
  const add = async () => {
    const result = await promptDialog({
      title: "Add tracker",
      fields: [
        { key: "url", label: "Tracker URL", placeholder: "https://…" },
        { key: "tier", label: "Tier (0 is primary)", initial: "0" },
      ],
      confirmLabel: "Add tracker",
    });
    if (result === null || typeof result === "string") return;
    const url = result["url"]?.trim() ?? "";
    const group = Number(result["tier"]);
    if (!url) return;
    try {
      const parsed = new URL(url);
      if (!/^https?:$/.test(parsed.protocol) || !Number.isInteger(group) || group < 0)
        throw new Error();
    } catch {
      showToast("Enter a valid http(s) tracker URL and tier.");
      return;
    }
    void detailAction("tracker_add", props.hash, { trackerUrl: url, trackerGroup: group })
      .then(() => {
        showToast("Tracker added.");
        void fetchDetail(props.hash);
      })
      .catch((error: Error) => showToast(error.message));
  };
  const announce = () =>
    void detailAction("reannounce", props.hash)
      .then(() => showToast("Reannounce requested."))
      .catch((error: Error) => showToast(error.message));
  const remove = async (tracker: Tracker) => {
    const confirmed = await confirmDialog({
      title: "Remove tracker",
      body: `Remove tracker ${tracker.url} from this torrent?`,
      confirmLabel: "Remove",
      danger: true,
    });
    if (!confirmed) return;
    void detailAction("tracker_remove", props.hash, { trackerIndex: tracker.index })
      .then(() => {
        showToast("Tracker removed.");
        void fetchDetail(props.hash);
      })
      .catch((error: Error) => showToast(error.message));
  };
  const toggle = (tracker: Tracker) =>
    void detailAction("tracker_enable", props.hash, {
      trackerIndex: tracker.index,
      enabled: !tracker.isEnabled,
    })
      .then(() => {
        showToast(tracker.isEnabled ? "Tracker disabled." : "Tracker enabled.");
        void fetchDetail(props.hash);
      })
      .catch((error: Error) => showToast(error.message));
  const status = (tracker: Tracker) =>
    !tracker.isEnabled
      ? "Disabled"
      : tracker.failedCount
        ? `Failed${tracker.latestEvent ? `: ${tracker.latestEvent}` : ""}`
        : tracker.successCount
          ? "Working"
          : tracker.latestEvent
            ? "Updating"
            : "Not contacted";
  const trackerRows = () => props.detail.trackers;
  return (
    <div class="trackers-tab">
      <div class="tracker-list">
        <Show
          when={trackerRows().length}
          fallback={
            <div class="detail-empty-rows">
              No trackers on this torrent yet — add one below.{" "}
              <button
                type="button"
                class="copy-button"
                onClick={() => void fetchDetail(props.hash)}
              >
                Retry
              </button>
            </div>
          }
        >
          <For each={trackerRows()}>
            {(tracker) => (
              <div class="tracker-row">
                <i classList={{ off: !tracker.isEnabled }} />
                <span class="tracker-url" title={tracker.url}>
                  T{tracker.group} · {tracker.url}
                </span>
                <span class="tracker-status" classList={{ off: !tracker.isEnabled }}>
                  {status(tracker)}
                </span>
                <span class="tnum">
                  {tracker.seeds} / {tracker.leechers}
                </span>
                <span class="tnum">{next(tracker)}</span>
                <button class="tracker-remove" type="button" onClick={() => toggle(tracker)}>
                  {tracker.isEnabled ? "Disable" : "Enable"}
                </button>
                <button class="tracker-remove" type="button" onClick={() => remove(tracker)}>
                  Remove
                </button>
              </div>
            )}
          </For>
        </Show>
      </div>
      <footer class="tracker-actions">
        <button type="button" onClick={add}>
          Add tracker
        </button>
        <button type="button" onClick={announce}>
          Force reannounce
        </button>
      </footer>
    </div>
  );
}

function TransferTab(props: { torrent: Torrent; detail: TorrentDetail }) {
  const t = () => props.detail.transfer;
  const g = globalStats;
  return (
    <div class="transfer-grid">
      <div>
        <span>Downloaded</span>
        <b>{formatBytes(t().downloadedBytes)}</b>
      </div>
      <div>
        <span>Uploaded</span>
        <b>{formatBytes(t().uploadedBytes)}</b>
      </div>
      <div>
        <span>Chunk size</span>
        <b>{formatBytes(t().chunkSize)}</b>
      </div>
      <div>
        <span>Chunks</span>
        <b class="tnum">
          {t().chunksDone.toLocaleString()} / {t().chunkCount.toLocaleString()}
        </b>
      </div>
      <div>
        <span>Directory</span>
        <b title={t().directory}>{t().directory || "—"}</b>
      </div>
      <div>
        <span>Torrent priority</span>
        <b>
          {props.torrent.priority === 3
            ? "High"
            : props.torrent.priority === 0
              ? "Off"
              : props.torrent.priority === 1
                ? "Low"
                : "Normal"}
        </b>
      </div>
      <div>
        <span>Session downloaded</span>
        <b>{formatBytes(g()?.sessionDownTotal ?? 0)}</b>
      </div>
      <div>
        <span>Session uploaded</span>
        <b>{formatBytes(g()?.sessionUpTotal ?? 0)}</b>
      </div>
    </div>
  );
}
