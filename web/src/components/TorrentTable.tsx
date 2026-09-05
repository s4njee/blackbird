import {
  For,
  Show,
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  onMount,
  untrack,
  type JSX,
} from "solid-js";
import {
  formatBytes,
  formatDate,
  formatRatio,
  formatRate,
  formatSeedingTime,
  formatUptime,
} from "../lib/format";
import { COLUMN_DEFINITIONS, columnDefinition, type ColumnKey } from "../lib/columns";
import { computeWindow, tableRowHeight } from "../lib/virtualWindow";
import type { Torrent } from "../lib/types";
import { torrentStateLabel } from "../lib/status";
import { connection, torrentList, torrents, visibleHashes, visibleRows } from "../store/session";
import {
  changeSort,
  clearFilters,
  columnLayout,
  customLabelStyle,
  focusedHash,
  navigate,
  pruneSelection,
  reorderColumns,
  resetColumns,
  resolvedDensity,
  selectAllVisible,
  selectOnly,
  selectRange,
  selectedHashes,
  selectedSet,
  setColumnWidth,
  setFocusedHash,
  sort,
  toggleColumn,
  toggleSelection,
  openAdd,
  visibleColumnKeys,
} from "../store/ui";

export type ContextTarget = { x: number; y: number; hashes: string[]; torrent?: Torrent };

/** Shimmer placeholders between first paint and the first snapshot: the
 * skeleton-to-data path measured by the PERF-7.5 startup benchmark. */
const SKELETON_ROWS = Array.from({ length: 20 });

function progressPercent(t: Torrent) {
  return t.state === "checking" ? t.checkingPercent : t.percent;
}

function formatEta(seconds: number) {
  if (seconds < 0) return "∞";
  if (!Number.isFinite(seconds) || seconds === 0) return "—";
  return formatUptime(seconds);
}

function priorityLabel(priority: number) {
  return ["Off", "Low", "Normal", "High"][priority] ?? "—";
}

/** Label chip: user-configured colors render inline (contrast-safe text),
 * built-in names keep class-driven themed rendering. */
function LabelChip(props: { label: string }) {
  const custom = customLabelStyle(props.label);
  return custom ? (
    <span class="label-chip" style={{ background: custom.background, color: custom.color }}>
      {props.label}
    </span>
  ) : (
    <span class={`label-chip ${props.label}`}>{props.label}</span>
  );
}

function rateCell(rate: number) {
  return rate > 0 ? formatRate(rate) : "—";
}

function numericColumn(key: ColumnKey) {
  return [
    "sizeBytes",
    "percent",
    "leftBytes",
    "seedsPeers",
    "seeds",
    "peers",
    "downRate",
    "upRate",
    "downloadedBytes",
    "uploadedBytes",
    "etaSeconds",
    "ratio",
  ].includes(key);
}

type Column = ReturnType<typeof columnDefinition>;

/** Effective pixel width: fluid columns keep their minimum, fixed columns
 * take the layout override when wider. */
function effectiveWidth(column: Column): number {
  if ("fluid" in column && column.fluid) return column.minWidth;
  return Math.max(column.minWidth, columnLayout().widths[column.key] ?? column.width);
}

/** Cell classes for one row/column: numeric alignment plus the status, rate,
 * and ratio accents. */
function cellClass(row: Torrent, column: Column): string {
  const parts: string[] = [column.class];
  if (numericColumn(column.key)) parts.push("numeric tnum");
  if (column.key === "name") parts.push("torrent-name");
  if (column.key === "state") parts.push(`torrent-state ${row.state}`);
  if (column.key === "downRate") parts.push("rate-down");
  if (column.key === "upRate") parts.push("rate-up");
  if (column.key === "ratio") parts.push(row.ratio >= 2 ? "ratio bright" : "ratio");
  return parts.join(" ");
}

function renderCell(row: Torrent, key: ColumnKey): JSX.Element | string {
  switch (key) {
    case "name":
      return row.name;
    case "sizeBytes":
      return formatBytes(row.sizeBytes);
    case "percent":
      const percent = progressPercent(row);
      return (
        <div class="progress-wrap" title={`${percent.toFixed(1)}%`}>
          <div class="progress" aria-hidden="true">
            <span
              classList={{
                complete: percent >= 100,
                stopped: row.state === "stopped" && percent < 100,
              }}
              style={{ width: `${Math.max(0, Math.min(100, percent))}%` }}
            />
          </div>
          <b class="progress-pct tnum">
            {percent >= 100 ? "100%" : `${percent.toFixed(1)}%`}
          </b>
        </div>
      );
    case "leftBytes":
      return formatBytes(row.leftBytes);
    case "state":
      return torrentStateLabel(row);
    case "seedsPeers":
      return `${row.seeds || "—"} / ${row.peers || "—"}`;
    case "seeds":
      return row.seeds || "—";
    case "peers":
      return row.peers || "—";
    case "downRate":
      return rateCell(row.downRate);
    case "upRate":
      return rateCell(row.upRate);
    case "downloadedBytes":
      return formatBytes(row.downloadedBytes);
    case "uploadedBytes":
      return formatBytes(row.uploadedBytes);
    case "etaSeconds":
      return formatEta(row.etaSeconds);
    case "ratio":
      return formatRatio(row.ratio);
    case "label":
      return (
        <Show when={row.label} fallback={<span class="label-chip neutral">—</span>}>
          <LabelChip label={row.label} />
        </Show>
      );
    case "priority":
      return priorityLabel(row.priority);
    case "throttle":
      return row.throttle || "—";
    case "ratioGroup":
      return row.ratioGroup || "—";
    case "addedAt":
      return formatDate(row.addedAt);
    case "finishedAt":
      return formatDate(row.finishedAt);
    case "creationDate":
      return formatDate(row.creationDate);
    case "seedingTime":
      return formatSeedingTime(row.finishedAt);
    case "trackerHost":
      return row.trackerHost || "—";
    case "trackerStatus":
      return row.trackerStatus || "—";
    case "directory":
      return row.directory || row.basePath || "—";
    case "hash":
      return row.hash;
    case "message":
      return row.message || "—";
    default:
      return "—";
  }
}

export function TorrentTable(props: { onContextMenu: (target: ContextTarget) => void }) {
  const [draggingFiles, setDraggingFiles] = createSignal(false);
  const [columnMenu, setColumnMenu] = createSignal<{ x: number; y: number } | null>(null);
  const [draggedColumn, setDraggedColumn] = createSignal<ColumnKey | null>(null);
  let tableRef: HTMLTableElement | undefined;
  let wrapRef: HTMLDivElement | undefined;
  const columns = createMemo(() => visibleColumnKeys().map((key) => columnDefinition(key)));
  // The visible rows come from the session's incrementally maintained
  // ordered index (PERF-7.2): already filtered and sorted, stable by
  // identity across ticks. Filter/sort inputs live in the ui store and the
  // index refreshes on deltas and on input changes alike.
  const rows = visibleRows;
  const hashes = visibleHashes;
  // Virtualization (PERF-7.3): the scroll container's position drives a row
  // window over the ordered view. Only the window renders; top/bottom spacer
  // rows reserve the hidden height so scrollTop is never touched by ticks or
  // filter changes (scroll position is preserved structurally).
  const [scrollTop, setScrollTop] = createSignal(0);
  const [viewportHeight, setViewportHeight] = createSignal(600);
  const rowHeight = () => tableRowHeight(resolvedDensity());
  const windowed = createMemo(() =>
    computeWindow(rows().length, scrollTop(), viewportHeight(), undefined, rowHeight()),
  );
  const windowRows = createMemo(() => rows().slice(windowed().start, windowed().end));
  const windowStart = createMemo(() => windowed().start);
  const columnCount = createMemo(() => columns().length + 1);
  // Roving tabindex (POL-8.5): the focused row carries the tab stop; when it
  // scrolls out of the window, the first rendered row does instead.
  const tabStop = createMemo(() => {
    const focus = focusedHash();
    const rendered = windowRows();
    if (focus && rendered.some((row) => row.hash === focus)) return focus;
    return rendered[0]?.hash;
  });
  // Selection model announcement: count plus the focused torrent's name.
  const [liveText, setLiveText] = createSignal("");
  createEffect(() => {
    const selected = selectedHashes();
    const focus = focusedHash();
    const name = focus ? torrents[focus]?.name : undefined;
    setLiveText(
      name
        ? `${name}, ${selected.length} selected`
        : selected.length
          ? `${selected.length} selected`
          : "No torrents selected",
    );
  });
  // Name deliberately has no fixed column width so it absorbs extra space.
  // The table itself must still reserve its minimum width; otherwise the
  // neighbouring fixed columns can consume the full table and collapse Name
  // to zero pixels.
  const tableMinWidth = createMemo(
    () => `${26 + columns().reduce((total, column) => total + effectiveWidth(column), 0)}px`,
  );

  onMount(() => {
    const closeMenu = () => setColumnMenu(null);
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeMenu();
    };
    document.addEventListener("click", closeMenu);
    document.addEventListener("keydown", closeOnEscape);
    const wrap = wrapRef;
    if (wrap) {
      setViewportHeight(wrap.clientHeight || 600);
      setScrollTop(wrap.scrollTop);
      let ticking = false;
      const onScroll = () => {
        if (ticking) return;
        ticking = true;
        requestAnimationFrame(() => {
          ticking = false;
          if (wrapRef) setScrollTop(wrapRef.scrollTop);
        });
      };
      wrap.addEventListener("scroll", onScroll, { passive: true });
      const observer = new ResizeObserver(() => {
        if (wrapRef) setViewportHeight(wrapRef.clientHeight || 600);
      });
      observer.observe(wrap);
      onCleanup(() => {
        wrap.removeEventListener("scroll", onScroll);
        observer.disconnect();
      });
    }
    onCleanup(() => {
      document.removeEventListener("click", closeMenu);
      document.removeEventListener("keydown", closeOnEscape);
    });
  });

  // Keyboard navigation target stays visible: when the focused hash moves
  // (arrow/page/home/end keys), scroll just enough to reveal its row.
  createEffect(() => {
    const hash = focusedHash();
    if (!hash) return;
    const index = untrack(hashes).indexOf(hash);
    if (index >= 0) scrollIndexIntoView(index);
  });

  const openColumnMenu = (event: MouseEvent) => {
    event.preventDefault();
    setColumnMenu({
      x: Math.min(event.clientX, window.innerWidth - 280),
      y: Math.min(event.clientY, window.innerHeight - 360),
    });
  };
  const beginResize = (event: MouseEvent, key: ColumnKey) => {
    event.preventDefault();
    event.stopPropagation();
    const definition = columnDefinition(key);
    const startWidth = columnLayout().widths[key] ?? definition.width;
    const startX = event.clientX;
    const move = (moveEvent: MouseEvent) =>
      setColumnWidth(key, startWidth + moveEvent.clientX - startX);
    const end = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", end);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", end, { once: true });
  };
  const autoFit = (event: MouseEvent, key: ColumnKey) => {
    event.preventDefault();
    event.stopPropagation();
    const definition = columnDefinition(key);
    const cells = tableRef
      ? Array.from(tableRef.querySelectorAll<HTMLElement>(`.${definition.class}`))
      : [];
    const contentWidth = cells.reduce(
      (max, cell) => Math.max(max, cell.scrollWidth),
      definition.label.length * 8 + 24,
    );
    setColumnWidth(key, contentWidth + 8);
  };
  const moveColumn = (key: ColumnKey, delta: -1 | 1) => {
    const order = columnLayout().order;
    const index = order.indexOf(key);
    const target = order[index + delta];
    if (target) reorderColumns(key, target);
  };
  createEffect(() => pruneSelection(new Set(torrentList().map((item) => item.hash))));

  /** Scrolls a visible-view index into the window without disturbing the
   * scroll position otherwise (keyboard navigation target only). */
  const scrollIndexIntoView = (index: number) => {
    const wrap = wrapRef;
    if (!wrap || index < 0) return;
    const top = index * rowHeight();
    const bottom = top + rowHeight();
    const viewTop = wrap.scrollTop;
    const viewBottom = viewTop + wrap.clientHeight;
    if (top < viewTop) wrap.scrollTop = top;
    else if (bottom > viewBottom) wrap.scrollTop = bottom - wrap.clientHeight;
  };

  const selectRow = (event: MouseEvent, row: Torrent) => {
    if (event.shiftKey) selectRange(row.hash, hashes());
    else if (event.metaKey || event.ctrlKey) toggleSelection(row.hash);
    else selectOnly(row.hash);
  };
  const context = (event: MouseEvent, row: Torrent) => {
    event.preventDefault();
    const selected = selectedSet();
    const active = selected.has(row.hash) ? [...selected] : [row.hash];
    if (!selected.has(row.hash)) selectOnly(row.hash);
    props.onContextMenu({ x: event.clientX, y: event.clientY, hashes: active, torrent: row });
  };

  return (
    <div
      class="table-area"
      classList={{ "table-file-drag": draggingFiles() }}
      onDragOver={(event) => {
        event.preventDefault();
        setDraggingFiles(true);
      }}
      onDragLeave={(event) => {
        if (event.currentTarget === event.target) setDraggingFiles(false);
      }}
      onDrop={(event) => {
        event.preventDefault();
        setDraggingFiles(false);
        openAdd(Array.from(event.dataTransfer?.files ?? []));
      }}
    >
      <div class="torrent-table-wrap" ref={wrapRef}>
        <table
          class="torrent-table"
          ref={tableRef}
          aria-label="Torrents"
          style={{ "min-width": tableMinWidth() }}
        >
          <colgroup>
            <col class="check-col" />
            <For each={columns()}>
              {(column) => (
                <col
                  class={column.class}
                  style={{
                    width:
                      "fluid" in column
                        ? undefined
                        : `${columnLayout().widths[column.key] ?? column.width}px`,
                  }}
                />
              )}
            </For>
          </colgroup>
          <thead>
            <tr>
              <th class="check-head">
                <button
                  type="button"
                  class="row-checkbox"
                  aria-label="Select all visible torrents"
                  classList={{
                    checked: rows().length > 0 && rows().every((r) => selectedSet().has(r.hash)),
                  }}
                  onClick={() => selectAllVisible(hashes())}
                />
              </th>
              <For each={columns()}>
                {(column) => {
                  const sortKey = () => sort().find((key) => key.column === column.key);
                  const sortIndex = () => sort().findIndex((key) => key.column === column.key);
                  return (
                    <th
                      class={column.class}
                      draggable="true"
                      title="Click to sort; Shift-click for a secondary sort"
                      aria-sort={
                        sortIndex() === 0
                          ? sortKey()?.direction === "asc"
                            ? "ascending"
                            : "descending"
                          : undefined
                      }
                      classList={{ sorted: sortIndex() >= 0 }}
                      onContextMenu={openColumnMenu}
                      onDragStart={(event) => {
                        setDraggedColumn(column.key);
                        event.dataTransfer?.setData("text/column", column.key);
                        if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                      }}
                      onDragOver={(event) => {
                        event.preventDefault();
                        if (event.dataTransfer) event.dataTransfer.dropEffect = "move";
                      }}
                      onDrop={(event) => {
                        event.preventDefault();
                        const source =
                          draggedColumn() ??
                          (event.dataTransfer?.getData("text/column") as ColumnKey);
                        if (source) reorderColumns(source, column.key);
                        setDraggedColumn(null);
                      }}
                      onDragEnd={() => setDraggedColumn(null)}
                    >
                      <button
                        type="button"
                        class="column-header-button"
                        aria-label={`Sort by ${column.label}`}
                        title="Click to sort; Shift-click for a secondary sort"
                        onClick={(event) => changeSort(column.key, event.shiftKey)}
                      >
                        <span class="column-header-label">
                          {column.label}
                          <Show when={sortKey()}>
                            {(key) => (
                              <span class="sort-caret">
                                {key().direction === "asc" ? "▲" : "▼"}
                                {sortIndex() > 0 ? <sup>{sortIndex() + 1}</sup> : ""}
                              </span>
                            )}
                          </Show>
                        </span>
                      </button>
                      <span
                        class="column-resize-handle"
                        title="Drag to resize; double-click to auto-fit"
                        onMouseDown={(event) => beginResize(event, column.key)}
                        onDblClick={(event) => autoFit(event, column.key)}
                      />
                    </th>
                  );
                }}
              </For>
            </tr>
          </thead>
          <tbody>
            <Show
              when={connection() === "connecting"}
              fallback={
                <>
                  <Show when={windowed().topPad > 0}>
                    <tr class="virtual-spacer" aria-hidden="true">
                      <td
                        colspan={columnCount()}
                        style={{ height: `${windowed().topPad}px`, padding: "0", border: "0" }}
                      />
                    </tr>
                  </Show>
                  <For each={windowRows()}>
                    {(row, index) => {
                      // Absolute view index drives striping (spacer rows would shift
                      // :nth-child parity, so striping is explicit per row).
                      const absolute = () => windowStart() + index();
                      return (
                        <tr
                          class="row-enter"
                          data-hash={row.hash}
                          tabindex={tabStop() === row.hash ? 0 : -1}
                          aria-selected={selectedSet().has(row.hash)}
                          aria-rowindex={absolute() + 1}
                          classList={{
                            selected: selectedSet().has(row.hash),
                            "row-alt": absolute() % 2 === 1,
                          }}
                          onClick={(event) => selectRow(event, row)}
                          onContextMenu={(event) => context(event, row)}
                          onFocus={() => setFocusedHash(row.hash)}
                        >
                          <td class="check-cell">
                            <button
                              type="button"
                              aria-label={`Select ${row.name}`}
                              class="row-checkbox"
                              classList={{ checked: selectedSet().has(row.hash) }}
                              onClick={(event) => {
                                event.stopPropagation();
                                toggleSelection(row.hash);
                              }}
                            />
                          </td>
                          <For each={columns()}>
                            {(column) => (
                              <td
                                class={cellClass(row, column)}
                                title={column.key === "name" ? row.name : undefined}
                              >
                                {renderCell(row, column.key)}
                              </td>
                            )}
                          </For>
                        </tr>
                      );
                    }}
                  </For>
                  <Show when={windowed().bottomPad > 0}>
                    <tr class="virtual-spacer" aria-hidden="true">
                      <td
                        colspan={columnCount()}
                        style={{ height: `${windowed().bottomPad}px`, padding: "0", border: "0" }}
                      />
                    </tr>
                  </Show>
                </>
              }
            >
              <For each={SKELETON_ROWS}>
                {() => (
                  <tr class="skeleton-row" aria-hidden="true">
                    <td class="check-cell" />
                    <For each={columns()}>
                      {() => (
                        <td>
                          <span />
                        </td>
                      )}
                    </For>
                  </tr>
                )}
              </For>
            </Show>
          </tbody>
        </table>
        <Show when={columnMenu()}>
          {(menu) => (
            <div
              class="column-picker"
              style={{ left: `${menu().x}px`, top: `${menu().y}px` }}
              onClick={(event) => event.stopPropagation()}
              onContextMenu={(event) => event.preventDefault()}
            >
              <div class="column-picker-title">
                <b>Table columns</b>
                <button
                  type="button"
                  onClick={() => setColumnMenu(null)}
                  aria-label="Close column picker"
                >
                  ×
                </button>
              </div>
              <p>Drag to reorder · resize from a header edge</p>
              <div class="column-picker-list">
                <For each={COLUMN_DEFINITIONS}>
                  {(column) => (
                    <label
                      draggable="true"
                      onDragStart={(event) => {
                        setDraggedColumn(column.key);
                        event.dataTransfer?.setData("text/column", column.key);
                        if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                      }}
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => {
                        event.preventDefault();
                        const source =
                          draggedColumn() ??
                          (event.dataTransfer?.getData("text/column") as ColumnKey);
                        if (source) reorderColumns(source, column.key);
                        setDraggedColumn(null);
                      }}
                    >
                      <input
                        type="checkbox"
                        checked={
                          column.key === "name" || !columnLayout().hidden.includes(column.key)
                        }
                        disabled={column.key === "name"}
                        onChange={() => toggleColumn(column.key)}
                      />
                      <span>{column.label}</span>
                      <button
                        type="button"
                        class="column-move"
                        aria-label={`Move ${column.label} left`}
                        disabled={columnLayout().order.indexOf(column.key) <= 0}
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
                          columnLayout().order.indexOf(column.key) >=
                          columnLayout().order.length - 1
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
              <button type="button" class="column-picker-reset" onClick={resetColumns}>
                Reset to defaults
              </button>
            </div>
          )}
        </Show>
        <Show when={connection() !== "connecting" && rows().length === 0}>
          <Show
            when={torrentList().length > 0}
            fallback={
              <div class="empty-session">
                <h2>No torrents yet</h2>
                <p>Add your first torrent, or let Blackbird feed itself:</p>
                <div class="empty-session-actions">
                  <button type="button" onClick={() => openAdd()}>
                    + Add torrent
                  </button>
                  <button type="button" onClick={() => navigate("settings", "Directories")}>
                    Set up a watch directory
                  </button>
                  <button type="button" onClick={() => navigate("rss")}>
                    Configure RSS
                  </button>
                </div>
                <p class="empty-session-docs">
                  New to Blackbird? Start with the{" "}
                  <button type="button" onClick={() => navigate("settings", "Directories")}>
                    watch directory guide
                  </button>
                  , the{" "}
                  <button type="button" onClick={() => navigate("settings", "Automation")}>
                    automation rules
                  </button>
                  , or the{" "}
                  <button type="button" onClick={() => navigate("settings", "About")}>
                    About page
                  </button>{" "}
                  (see also <code>docs/user-guide.md</code> in the repo).
                </p>
              </div>
            }
          >
            <div class="empty-table">
              No torrents match this filter{" "}
              <button type="button" onClick={clearFilters}>
                Clear filters
              </button>
            </div>
          </Show>
        </Show>
        <Show when={draggingFiles()}>
          <div class="table-drop-hint">Drop .torrent files to add</div>
        </Show>
        <div class="sr-only" role="status" aria-live="polite">
          {liveText()}
        </div>
      </div>
    </div>
  );
}
