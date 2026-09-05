import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { formatBytes, formatRate } from "../lib/format";
import { copyText } from "../lib/clipboard";
import {
  PEER_ALWAYS_COLUMN,
  PEER_COLUMN_DEFINITIONS,
  peerColumnDefinition,
  type PeerColumnKey,
} from "../lib/peerColumns";
import { countryFlagEmoji, countryName, flagTooltip } from "../lib/peerMeta";
import { PeerSorter } from "../lib/peerSort";
import type { Peer, TorrentDetail } from "../lib/types";
import { peerKey } from "../lib/peerKey";
import { fetchDetail } from "../store/session";
import { confirmDialog } from "../store/dialog";
import { VIRTUALIZE_ABOVE, computeWindow, detailRowHeight } from "../lib/virtualWindow";
import {
  changePeerSort,
  peerColumnLayout,
  peerSort,
  resetPeerColumns,
  reorderPeerColumns,
  resolvedDensity,
  showToast,
  togglePeerColumn,
  visiblePeerColumnKeys,
} from "../store/ui";

/** Sends a peer moderation action and refreshes the detail on success. */
async function peerAction(
  action: "peer_ban" | "peer_snub" | "peer_unsnub" | "peer_disconnect",
  hash: string,
  peerIds: string[],
) {
  await Promise.all(
    peerIds.map(async (peerId) => {
      const response = await fetch("/api/v1/torrents/action", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ action, hashes: [hash], peerId }),
      });
      const body = (await response.json().catch(() => ({}))) as {
        message?: string;
        results?: Array<{ ok: boolean; error?: string }>;
      };
      if (!response.ok) throw new Error(body.message || "Action failed");
      const failure = body.results?.find((result) => !result.ok);
      if (failure) throw new Error(failure.error || "Action failed");
    }),
  );
  void fetchDetail(hash);
}

const COUNT_LABEL = (count: number) => `${count} connected`;

/** Structural equality for peer rows; used to preserve object identity when a
 * live detail tick carries an unchanged peer. */
function fieldsEqual(a: Peer, b: Peer): boolean {
  return (
    a.id === b.id &&
    a.address === b.address &&
    a.port === b.port &&
    a.client === b.client &&
    a.completedPercent === b.completedPercent &&
    a.downRate === b.downRate &&
    a.upRate === b.upRate &&
    a.downloadedBytes === b.downloadedBytes &&
    a.uploadedBytes === b.uploadedBytes &&
    a.isSnubbed === b.isSnubbed &&
    a.countryCode === b.countryCode &&
    a.flags === b.flags
  );
}

function numeric(cell: string) {
  return (
    cell === "port" ||
    cell === "downRate" ||
    cell === "upRate" ||
    cell === "downloadedBytes" ||
    cell === "uploadedBytes" ||
    cell === "have"
  );
}

/** Effective pixel width of a peer column: layout override or definition. */
function peerColumnWidth(key: PeerColumnKey): string {
  const def = peerColumnDefinition(key);
  return `${Math.max(def.minWidth, peerColumnLayout().widths[key] ?? def.width)}px`;
}

function cellFor(peer: Peer, key: string) {
  switch (key) {
    case "ip":
      return `${peer.address}:${peer.port}`;
    case "port":
      return peer.port;
    case "country":
      return peer.countryCode ? (
        <span title={`${countryName(peer.countryCode)} (${peer.countryCode})`}>
          <i class="peer-flag">{countryFlagEmoji(peer.countryCode)}</i>{" "}
          {countryName(peer.countryCode)}
        </span>
      ) : (
        "—"
      );
    case "client":
      return peer.client || "—";
    case "flags":
      return (
        <span
          class="peer-flags"
          classList={{ snubbed: peer.isSnubbed }}
          title={peer.flags ? flagTooltip(peer.flags) : undefined}
        >
          {peer.flags || "—"}
        </span>
      );
    case "have":
      return (
        <div class="peer-have-cell">
          <span class="peer-have">
            <span style={{ width: `${Math.min(100, peer.completedPercent)}%` }} />
          </span>
          <span class="tnum">
            {peer.completedPercent >= 100 ? "100%" : `${peer.completedPercent.toFixed(0)}%`}
          </span>
        </div>
      );
    case "downRate":
      return peer.downRate > 0 ? formatRate(peer.downRate) : "—";
    case "upRate":
      return peer.upRate > 0 ? formatRate(peer.upRate) : "—";
    case "downloadedBytes":
      return formatBytes(peer.downloadedBytes);
    case "uploadedBytes":
      return formatBytes(peer.uploadedBytes);
    case "id":
      return peer.id || "—";
    default:
      return "—";
  }
}

/** The row context-menu target: the clicked peer plus its screen position. */
type PeerTarget = { peer: Peer; x: number; y: number };

export function PeersTab(props: { hash: string; detail: TorrentDetail }) {
  const [selectedKeys, setSelectedKeys] = createSignal<Set<string>>(new Set());
  const [lastKey, setLastKey] = createSignal("");
  const [menu, setMenu] = createSignal<PeerTarget | null>(null);
  const [columnMenu, setColumnMenu] = createSignal<{ x: number; y: number } | null>(null);
  const sorter = new PeerSorter();

  // Incoming detail peers are fresh objects every tick. Reconcile them into a
  // stable map keyed by ip:port so unchanged peers keep their object identity:
  // Solid reuses the DOM rows and PeerSorter only touches rows whose values
  // actually changed (no full-list rebuild / scroll jump under live 1s ticks).
  const byKey = new Map<string, Peer>();
  const stablePeers = createMemo<Peer[]>(() => {
    const incoming = props.detail.peers ?? [];
    const currentKeys = new Set<string>();
    for (const peer of incoming) {
      const key = peerKey(peer);
      currentKeys.add(key);
      const prior = byKey.get(key);
      byKey.set(key, prior && fieldsEqual(prior, peer) ? prior : peer);
    }
    for (const key of byKey.keys()) if (!currentKeys.has(key)) byKey.delete(key);
    return [...currentKeys].map((key) => byKey.get(key)!);
  });
  const peers = createMemo(() => sorter.sort(stablePeers(), peerSort()));
  // Virtualized above 200 rows (PERF-7.4): peer order from the sorter is
  // already view order, so the window slices it directly. Small swarms
  // render whole with no spacers and no scroll tracking overhead.
  let rowsRef: HTMLDivElement | undefined;
  const [scrollTop, setScrollTop] = createSignal(0);
  const [viewportHeight, setViewportHeight] = createSignal(300);
  const virtualized = createMemo(() => peers().length > VIRTUALIZE_ABOVE);
  const windowed = createMemo(() =>
    virtualized()
      ? computeWindow(
          peers().length,
          scrollTop(),
          viewportHeight(),
          undefined,
          detailRowHeight(resolvedDensity()),
        )
      : { start: 0, end: peers().length, topPad: 0, bottomPad: 0 },
  );
  const windowPeers = createMemo(() => peers().slice(windowed().start, windowed().end));
  const visibleKeys = createMemo(() => visiblePeerColumnKeys());
  const selectedPeers = createMemo(() => {
    const set = selectedKeys();
    return [...set].map((key) => byKey.get(key)).filter((peer): peer is Peer => Boolean(peer));
  });
  const allVisibleSelected = createMemo(
    () => peers().length > 0 && peers().every((peer) => selectedKeys().has(peerKey(peer))),
  );

  const syncSelectedToRows = () => {
    setSelectedKeys((prev) => {
      const available = new Set(peers().map((peer) => peerKey(peer)));
      const next = new Set<string>();
      for (const key of prev) if (available.has(key)) next.add(key);
      return next;
    });
  };
  onMount(syncSelectedToRows);

  // When detail updates, drop keys that disappeared (peer disconnected).
  createMemo(syncSelectedToRows);

  const isSelected = (key: string) => selectedKeys().has(key);
  const toggleKey = (key: string) => {
    setSelectedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  const selectPeer = (event: MouseEvent, peer: Peer) => {
    const key = peerKey(peer);
    const visible = peers().map((p) => peerKey(p));
    if (event.shiftKey) {
      const anchor = lastKey();
      const start = visible.indexOf(anchor !== "" ? anchor : key);
      const end = visible.indexOf(key);
      if (start >= 0 && end >= 0) {
        setSelectedKeys(new Set(visible.slice(Math.min(start, end), Math.max(start, end) + 1)));
      } else {
        setSelectedKeys(new Set([key]));
      }
    } else if (event.metaKey || event.ctrlKey) {
      toggleKey(key);
    } else {
      setSelectedKeys(new Set([key]));
    }
    setLastKey(key);
  };
  const selectAll = () => {
    if (allVisibleSelected()) setSelectedKeys(new Set<string>());
    else setSelectedKeys(new Set(peers().map((peer) => peerKey(peer))));
  };

  const openMenu = (event: MouseEvent, peer: Peer) => {
    event.preventDefault();
    const key = peerKey(peer);
    if (!isSelected(key)) {
      setSelectedKeys(new Set([key]));
      setLastKey(key);
    }
    setMenu({ peer, x: event.clientX, y: event.clientY });
  };
  const runAction = async (
    action: "peer_ban" | "peer_snub" | "peer_unsnub" | "peer_disconnect",
    peersToAct: Peer[],
  ) => {
    if (!peersToAct.length) return;
    setMenu(null);
    const ids = [...new Set(peersToAct.map((peer) => peer.id))];
    const verbs: Record<string, string> = {
      peer_ban: ids.length === 1 ? "Peer banned" : `${ids.length} peers banned`,
      peer_snub: ids.length === 1 ? "Peer snubbed" : `${ids.length} peers snubbed`,
      peer_unsnub: ids.length === 1 ? "Peer unsnubbed" : `${ids.length} peers unsnubbed`,
      peer_disconnect: ids.length === 1 ? "Peer disconnected" : `${ids.length} peers disconnected`,
    };
    if (action === "peer_ban") {
      const n = peersToAct.length;
      const confirmed = await confirmDialog({
        title: n === 1 ? "Ban peer" : "Ban peers",
        body: `Banning is permanent until rTorrent restarts. Ban ${n === 1 ? "1 peer" : `${n} peers`}?`,
        details: [...new Set(peersToAct.map((peer) => peer.address))],
        confirmLabel: "Ban",
        danger: true,
      });
      if (!confirmed) return;
    }
    try {
      await peerAction(action, props.hash, ids);
      showToast(verbs[action]);
    } catch (error) {
      void fetchDetail(props.hash);
      showToast(error instanceof Error ? error.message : "Peer action failed");
    }
  };

  const copyIp = async (peersToCopy: Peer[]) => {
    setMenu(null);
    const text = [...new Set(peersToCopy.map((peer) => peer.address))].join("\n");
    const ok = await copyText(text);
    showToast(
      ok
        ? peersToCopy.length === 1
          ? "IP address copied."
          : `${peersToCopy.length} IP addresses copied.`
        : "Unable to copy IP address.",
    );
  };

  // Context menu helpers for single-peer quick actions.
  const single = (peer: Peer) => [peer];
  const closeMenus = () => {
    setMenu(null);
    setColumnMenu(null);
  };

  onMount(() => {
    const close = () => {
      closeMenus();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    const pointer = (event: PointerEvent) => {
      if (!(event.target as HTMLElement).closest(".peers-table")) closeMenus();
    };
    document.addEventListener("pointerdown", pointer);
    document.addEventListener("keydown", key);
    const el = rowsRef;
    if (el) {
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
    }
    onCleanup(() => {
      document.removeEventListener("pointerdown", pointer);
      document.removeEventListener("keydown", key);
    });
  });

  const sortKeyFor = (column: string) => peerSort().find((s) => s.column === column);
  const primarySortColumn = () => peerSort()[0]?.column ?? PEER_ALWAYS_COLUMN;

  const moveColumn = (key: string, delta: -1 | 1) => {
    const order = peerColumnLayout().order;
    const index = order.indexOf(key as never);
    const target = order[index + delta];
    if (target) reorderPeerColumns(key as never, target);
  };

  return (
    <div
      class="peers-table"
      onContextMenu={(event) => {
        if (!(event.target as HTMLElement).closest(".peers-context")) event.preventDefault();
      }}
    >
      <div class="peers-toolbar">
        <button
          type="button"
          class="peers-select-all"
          aria-label="Select all peers"
          onClick={selectAll}
          classList={{ checked: allVisibleSelected() }}
        >
          <i>{allVisibleSelected() ? "☑" : "☐"}</i>
        </button>
        <span class="peers-count">{COUNT_LABEL(peers().length)}</span>
        {selectedKeys().size > 0 && (
          <div class="peers-bulk">
            <span class="tnum">{selectedKeys().size} selected</span>
            <button
              type="button"
              class="peer-action peer-snub"
              onClick={() => void runAction("peer_snub", selectedPeers())}
            >
              Snub
            </button>
            <button
              type="button"
              class="peer-action peer-unsnub"
              onClick={() => void runAction("peer_unsnub", selectedPeers())}
            >
              Unsnub
            </button>
            <button
              type="button"
              class="peer-action peer-disconnect"
              onClick={() => void runAction("peer_disconnect", selectedPeers())}
            >
              Disconnect
            </button>
            <button
              type="button"
              class="peer-action peer-ban"
              onClick={() => void runAction("peer_ban", selectedPeers())}
            >
              Ban…
            </button>
            <button type="button" class="peer-action" onClick={() => void copyIp(selectedPeers())}>
              Copy IP
            </button>
          </div>
        )}
        <button
          type="button"
          class="peers-columns-toggle"
          aria-label="Choose columns"
          onClick={(event) => {
            event.stopPropagation();
            setColumnMenu(
              columnMenu()
                ? null
                : {
                    x: event.currentTarget.getBoundingClientRect().right - 260,
                    y: event.currentTarget.getBoundingClientRect().bottom + 4,
                  },
            );
          }}
        >
          ⚙ Columns
        </button>
      </div>
      <div
        class="peers-head"
        style={{
          "grid-template-columns": `24px ${visibleKeys().map(peerColumnWidth).join(" ")}`,
        }}
      >
        <span class="peers-head-check" />
        <For each={visibleKeys()}>
          {(key) => {
            const def = () => peerColumnDefinition(key);
            const sort = () => sortKeyFor(key);
            const isPrimary = () => primarySortColumn() === key;
            const label = () => def().label;
            return (
              <button
                type="button"
                class="peers-head-cell"
                classList={{ sorted: !!sort(), primary: isPrimary() }}
                title={`Sort by ${label()}`}
                onClick={() => changePeerSort(key)}
              >
                {label()}
                <Show when={sort()}>
                  {(active) => (
                    <span class="sort-caret">{active().direction === "asc" ? "▲" : "▼"}</span>
                  )}
                </Show>
              </button>
            );
          }}
        </For>
      </div>
      <div class="peers-rows" ref={rowsRef} classList={{ "is-virtualized": virtualized() }}>
        <Show when={windowed().topPad > 0}>
          <div
            class="virtual-row-spacer"
            aria-hidden="true"
            style={{ height: `${windowed().topPad}px` }}
          />
        </Show>
        <For each={windowPeers()}>
          {(peer) => {
            const key = peerKey(peer);
            const selected = () => selectedKeys().has(key);
            return (
              <div
                class="peers-row"
                classList={{ selected: selected(), snubbed: peer.isSnubbed }}
                style={{
                  "grid-template-columns": `24px ${visibleKeys().map(peerColumnWidth).join(" ")}`,
                }}
                onClick={(event) => selectPeer(event, peer)}
                onContextMenu={(event) => openMenu(event, peer)}
              >
                <button
                  type="button"
                  class="row-checkbox"
                  classList={{ checked: selected() }}
                  aria-label={`Select peer ${peer.address}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    toggleKey(key);
                  }}
                />
                <For each={visibleKeys()}>
                  {(column) => (
                    <span
                      class={`peers-cell ${numeric(column) ? "numeric tnum" : ""} peer-${column}`}
                      classList={{
                        "rate-down": column === "downRate" && peer.downRate > 0,
                        "rate-up": column === "upRate" && peer.upRate > 0,
                      }}
                      title={column === "ip" ? `${peer.address}:${peer.port}` : undefined}
                    >
                      {cellFor(peer, column)}
                    </span>
                  )}
                </For>
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
        <Show when={!peers().length}>
          <div class="detail-empty-rows">
            No peers are connected right now.{" "}
            <button type="button" class="copy-button" onClick={() => void fetchDetail(props.hash)}>
              Retry
            </button>
          </div>
        </Show>
      </div>

      {/* Row context menu */}
      <Show when={menu()}>
        {(target) => (
          <div
            class="peers-context"
            style={{
              left: `${Math.min(target().x, window.innerWidth - 210)}px`,
              top: `${Math.min(target().y, window.innerHeight - 240)}px`,
            }}
            onClick={(event) => event.stopPropagation()}
          >
            <button
              type="button"
              class="menu-item"
              onClick={() => void runAction("peer_snub", single(target().peer))}
            >
              Snub
            </button>
            <button
              type="button"
              class="menu-item"
              onClick={() => void runAction("peer_unsnub", single(target().peer))}
            >
              Unsnub
            </button>
            <button
              type="button"
              class="menu-item destructive"
              onClick={() => void runAction("peer_ban", single(target().peer))}
            >
              Ban…
            </button>
            <button
              type="button"
              class="menu-item"
              onClick={() => void runAction("peer_disconnect", single(target().peer))}
            >
              Disconnect
            </button>
            <div class="menu-divider" />
            <button
              type="button"
              class="menu-item"
              onClick={() => void copyIp(single(target().peer))}
            >
              Copy IP
            </button>
          </div>
        )}
      </Show>

      {/* Column picker */}
      <Show when={columnMenu()}>
        {(menu) => (
          <div
            class="peers-context peers-column-picker"
            style={{ left: `${menu().x}px`, top: `${menu().y}px` }}
            onClick={(event) => event.stopPropagation()}
            onContextMenu={(event) => event.preventDefault()}
          >
            <div class="column-picker-title">
              <b>Peer columns</b>
              <button
                type="button"
                onClick={() => setColumnMenu(null)}
                aria-label="Close column picker"
              >
                ×
              </button>
            </div>
            <div class="column-picker-list">
              <For each={PEER_COLUMN_DEFINITIONS}>
                {(column) => (
                  <label>
                    <input
                      type="checkbox"
                      checked={
                        column.key === PEER_ALWAYS_COLUMN ||
                        !peerColumnLayout().hidden.includes(column.key)
                      }
                      disabled={column.key === PEER_ALWAYS_COLUMN}
                      onChange={() => togglePeerColumn(column.key)}
                    />
                    <span>{column.label}</span>
                    <button
                      type="button"
                      class="column-move"
                      aria-label={`Move ${column.label} left`}
                      disabled={peerColumnLayout().order.indexOf(column.key) <= 0}
                      onClick={(event) => {
                        event.stopPropagation();
                        moveColumn(column.key, -1);
                      }}
                    >
                      ←
                    </button>
                    <button
                      type="button"
                      class="column-move"
                      aria-label={`Move ${column.label} right`}
                      disabled={
                        peerColumnLayout().order.indexOf(column.key) >=
                        peerColumnLayout().order.length - 1
                      }
                      onClick={(event) => {
                        event.stopPropagation();
                        moveColumn(column.key, 1);
                      }}
                    >
                      →
                    </button>
                  </label>
                )}
              </For>
            </div>
            <button type="button" class="column-picker-reset" onClick={resetPeerColumns}>
              Reset to defaults
            </button>
          </div>
        )}
      </Show>
    </div>
  );
}
