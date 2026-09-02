import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount, type JSX } from "solid-js";
import { formatBytes, formatDate, formatRatio, formatRate, formatSeedingTime, formatUptime } from "../lib/format";
import { COLUMN_DEFINITIONS, columnDefinition, type ColumnKey } from "../lib/columns";
import { matchesStatus, parseQuery } from "../lib/filter";
import { IncrementalTorrentSorter } from "../lib/sort";
import type { Torrent } from "../lib/types";
import { connection, searchMatches, torrentList } from "../store/session";
import {
  changeSort, clearFilters, columnLayout, filters, pruneSelection, query, reorderColumns, resetColumns, selectAllVisible, selectOnly,
  selectRange, selectedSet, setColumnWidth, setShownTorrentCount, setVisibleHashes, sort, toggleColumn, toggleSelection, openAdd,
  visibleColumnKeys,
} from "../store/ui";

export type ContextTarget = { x: number; y: number; hashes: string[]; torrent?: Torrent };

function stateLabel(t: Torrent) {
  if (t.state === "checking") return `Checking ${t.checkingPercent.toFixed(0)}%`;
  if (t.state === "error") return t.message || "Tracker error";
  return t.state ? t.state[0].toUpperCase() + t.state.slice(1) : "—";
}

function formatEta(seconds: number) {
  if (seconds < 0) return "∞";
  if (!Number.isFinite(seconds) || seconds === 0) return "—";
  return formatUptime(seconds);
}

function priorityLabel(priority: number) {
  return ["Off", "Low", "Normal", "High"][priority] ?? "—";
}

function rateCell(rate: number) {
  return rate > 0 ? formatRate(rate) : "—";
}

function numericColumn(key: ColumnKey) {
  return ["sizeBytes", "percent", "leftBytes", "seedsPeers", "seeds", "peers", "downRate", "upRate", "downloadedBytes", "uploadedBytes", "etaSeconds", "ratio"].includes(key);
}

function renderCell(row: Torrent, key: ColumnKey): JSX.Element | string {
  switch (key) {
    case "name": return row.name;
    case "sizeBytes": return formatBytes(row.sizeBytes);
    case "percent":
      return <div class="progress" title={`${row.percent.toFixed(1)}%`}><span classList={{ complete: row.percent >= 100, stopped: row.state === "stopped" && row.percent < 100 }} style={{ width: `${Math.max(0, Math.min(100, row.percent))}%` }} /><b>{row.percent >= 100 ? "100%" : `${row.percent.toFixed(1)}%`}</b></div>;
    case "leftBytes": return formatBytes(row.leftBytes);
    case "state": return stateLabel(row);
    case "seedsPeers": return `${row.seeds || "—"} / ${row.peers || "—"}`;
    case "seeds": return row.seeds || "—";
    case "peers": return row.peers || "—";
    case "downRate": return rateCell(row.downRate);
    case "upRate": return rateCell(row.upRate);
    case "downloadedBytes": return formatBytes(row.downloadedBytes);
    case "uploadedBytes": return formatBytes(row.uploadedBytes);
    case "etaSeconds": return formatEta(row.etaSeconds);
    case "ratio": return formatRatio(row.ratio);
    case "label": return <Show when={row.label} fallback={<span class="label-chip neutral">—</span>}><span class={`label-chip ${row.label}`}>{row.label}</span></Show>;
    case "priority": return priorityLabel(row.priority);
    case "throttle": return row.throttle || "—";
    case "ratioGroup": return row.ratioGroup || "—";
    case "addedAt": return formatDate(row.addedAt);
    case "finishedAt": return formatDate(row.finishedAt);
    case "creationDate": return formatDate(row.creationDate);
    case "seedingTime": return formatSeedingTime(row.finishedAt);
    case "trackerHost": return row.trackerHost || "—";
    case "trackerStatus": return row.trackerStatus || "—";
    case "directory": return row.directory || row.basePath || "—";
    case "hash": return row.hash;
    case "message": return row.message || "—";
    default: return "—";
  }
}

export function TorrentTable(props: { onContextMenu: (target: ContextTarget) => void }) {
  const [draggingFiles, setDraggingFiles] = createSignal(false);
  const [columnMenu, setColumnMenu] = createSignal<{ x: number; y: number } | null>(null);
  const [draggedColumn, setDraggedColumn] = createSignal<ColumnKey | null>(null);
  const [debouncedQuery, setDebouncedQuery] = createSignal("");
  const sorter = new IncrementalTorrentSorter();
  let tableRef: HTMLTableElement | undefined;
  const columns = createMemo(() => visibleColumnKeys().map((key) => columnDefinition(key)));
  // Name deliberately has no fixed column width so it absorbs extra space.
  // The table itself must still reserve its minimum width; otherwise the
  // neighbouring fixed columns can consume the full table and collapse Name
  // to zero pixels.
  const tableMinWidth = createMemo(() => `${26 + columns().reduce((total, column) => total + ("fluid" in column ? column.minWidth : Math.max(column.minWidth, columnLayout().widths[column.key] ?? column.width)), 0)}px`);

  onMount(() => {
    const closeMenu = () => setColumnMenu(null);
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") closeMenu(); };
    document.addEventListener("click", closeMenu);
    document.addEventListener("keydown", closeOnEscape);
    onCleanup(() => { document.removeEventListener("click", closeMenu); document.removeEventListener("keydown", closeOnEscape); });
  });

  const openColumnMenu = (event: MouseEvent) => {
    event.preventDefault();
    setColumnMenu({ x: Math.min(event.clientX, window.innerWidth - 280), y: Math.min(event.clientY, window.innerHeight - 360) });
  };
  const beginResize = (event: MouseEvent, key: ColumnKey) => {
    event.preventDefault();
    event.stopPropagation();
    const definition = columnDefinition(key);
    const startWidth = columnLayout().widths[key] ?? definition.width;
    const startX = event.clientX;
    const move = (moveEvent: MouseEvent) => setColumnWidth(key, startWidth + moveEvent.clientX - startX);
    const end = () => { window.removeEventListener("mousemove", move); window.removeEventListener("mouseup", end); };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", end, { once: true });
  };
  const autoFit = (event: MouseEvent, key: ColumnKey) => {
    event.preventDefault();
    event.stopPropagation();
    const definition = columnDefinition(key);
    const cells = tableRef ? Array.from(tableRef.querySelectorAll<HTMLElement>(`.${definition.class}`)) : [];
    const contentWidth = cells.reduce((max, cell) => Math.max(max, cell.scrollWidth), definition.label.length * 8 + 24);
    setColumnWidth(key, contentWidth + 8);
  };
  const moveColumn = (key: ColumnKey, delta: -1 | 1) => {
    const order = columnLayout().order;
    const index = order.indexOf(key);
    const target = order[index + delta];
    if (target) reorderColumns(key, target);
  };
  createEffect(() => {
    const value = query();
    const timer = window.setTimeout(() => setDebouncedQuery(value.trim().toLowerCase()), 150);
    onCleanup(() => window.clearTimeout(timer));
  });
  const rows = createMemo(() => {
    const f = filters();
    const q = parseQuery(debouncedQuery());
    const filtered = torrentList().filter((t) => matchesStatus(t, f.status) && (!f.label || (t.label || "unlabeled") === f.label) && (!f.tracker || t.trackerHost === f.tracker) && searchMatches(t, q));
    return sorter.sort(filtered, sort());
  });
  const hashes = createMemo(() => rows().map((row) => row.hash));
  createEffect(() => { const next = rows().map((row) => row.hash); setShownTorrentCount(next.length); setVisibleHashes(next); });
  createEffect(() => pruneSelection(new Set(torrentList().map((item) => item.hash))));

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
    <div class="table-area" classList={{ "table-file-drag": draggingFiles() }} onDragOver={(event) => { event.preventDefault(); setDraggingFiles(true); }} onDragLeave={(event) => { if (event.currentTarget === event.target) setDraggingFiles(false); }} onDrop={(event) => { event.preventDefault(); setDraggingFiles(false); openAdd(Array.from(event.dataTransfer?.files ?? [])); }}>
      <div class="torrent-table-wrap">
        <table class="torrent-table" ref={tableRef} style={{ "min-width": tableMinWidth() }}>
          <colgroup><col class="check-col" /><For each={columns()}>{(column) => <col class={column.class} style={{ width: "fluid" in column ? undefined : `${columnLayout().widths[column.key] ?? column.width}px` }} />}</For></colgroup>
          <thead><tr><th class="check-head" aria-label="Select all"><button type="button" class="row-checkbox" classList={{ checked: rows().length > 0 && rows().every((r) => selectedSet().has(r.hash)) }} onClick={() => selectAllVisible(hashes())} /></th>
            <For each={columns()}>{(column) => { const sortKey = () => sort().find((key) => key.column === column.key); const sortIndex = () => sort().findIndex((key) => key.column === column.key); return <th class={column.class} draggable="true" title="Click to sort; Shift-click for a secondary sort" aria-sort={sortIndex() === 0 ? (sortKey()?.direction === "asc" ? "ascending" : "descending") : undefined} classList={{ sorted: sortIndex() >= 0 }} onContextMenu={openColumnMenu} onClick={(event) => changeSort(column.key, event.shiftKey)} onDragStart={(event) => { setDraggedColumn(column.key); event.dataTransfer?.setData("text/column", column.key); if (event.dataTransfer) event.dataTransfer.effectAllowed = "move"; }} onDragOver={(event) => { event.preventDefault(); if (event.dataTransfer) event.dataTransfer.dropEffect = "move"; }} onDrop={(event) => { event.preventDefault(); const source = draggedColumn() ?? event.dataTransfer?.getData("text/column") as ColumnKey; if (source) reorderColumns(source, column.key); setDraggedColumn(null); }} onDragEnd={() => setDraggedColumn(null)}><span class="column-header-label">{column.label}<Show when={sortKey()}>{(key) => <span class="sort-caret">{key().direction === "asc" ? "▲" : "▼"}{sortIndex() > 0 ? <sup>{sortIndex() + 1}</sup> : ""}</span>}</Show></span><span class="column-resize-handle" title="Drag to resize; double-click to auto-fit" onMouseDown={(event) => beginResize(event, column.key)} onDblClick={(event) => autoFit(event, column.key)} /></th>; }}</For>
          </tr></thead>
          <tbody>
            <For each={rows()}>{(row) => <tr classList={{ selected: selectedSet().has(row.hash) }} onClick={(event) => selectRow(event, row)} onContextMenu={(event) => context(event, row)}>
              <td class="check-cell"><button type="button" aria-label={`Select ${row.name}`} class="row-checkbox" classList={{ checked: selectedSet().has(row.hash) }} onClick={(event) => { event.stopPropagation(); toggleSelection(row.hash); }} /></td>
              <For each={columns()}>{(column) => <td class={`${column.class} ${numericColumn(column.key) ? "numeric tnum" : ""} ${column.key === "name" ? "torrent-name" : ""} ${column.key === "state" ? `torrent-state ${row.state}` : ""} ${column.key === "downRate" ? "rate-down" : ""} ${column.key === "upRate" ? "rate-up" : ""} ${column.key === "ratio" && row.ratio >= 2 ? "ratio bright" : column.key === "ratio" ? "ratio" : ""}`} title={column.key === "name" ? row.name : undefined}>{renderCell(row, column.key)}</td>}</For>
            </tr>}</For>
          </tbody>
        </table>
        <Show when={columnMenu()}>{(menu) => <div class="column-picker" style={{ left: `${menu().x}px`, top: `${menu().y}px` }} onClick={(event) => event.stopPropagation()} onContextMenu={(event) => event.preventDefault()}>
          <div class="column-picker-title"><b>Table columns</b><button type="button" onClick={() => setColumnMenu(null)} aria-label="Close column picker">×</button></div>
          <p>Drag to reorder · resize from a header edge</p>
          <div class="column-picker-list"><For each={COLUMN_DEFINITIONS}>{(column) => <label draggable="true" onDragStart={(event) => { setDraggedColumn(column.key); event.dataTransfer?.setData("text/column", column.key); if (event.dataTransfer) event.dataTransfer.effectAllowed = "move"; }} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); const source = draggedColumn() ?? event.dataTransfer?.getData("text/column") as ColumnKey; if (source) reorderColumns(source, column.key); setDraggedColumn(null); }}><input type="checkbox" checked={column.key === "name" || !columnLayout().hidden.includes(column.key)} disabled={column.key === "name"} onChange={() => toggleColumn(column.key)} /><span>{column.label}</span><button type="button" class="column-move" aria-label={`Move ${column.label} left`} disabled={columnLayout().order.indexOf(column.key) <= 0} onClick={(event) => { event.stopPropagation(); moveColumn(column.key, -1); }}>←</button><button type="button" class="column-move" aria-label={`Move ${column.label} right`} disabled={columnLayout().order.indexOf(column.key) >= columnLayout().order.length - 1} onClick={(event) => { event.stopPropagation(); moveColumn(column.key, 1); }}>→</button></label>}</For></div>
          <button type="button" class="column-picker-reset" onClick={resetColumns}>Reset to defaults</button>
        </div>}</Show>
        <Show when={connection() !== "connecting" && rows().length === 0}><div class="empty-table">No torrents match this filter <button type="button" onClick={clearFilters}>Clear filters</button></div></Show>
        <Show when={draggingFiles()}><div class="table-drop-hint">Drop .torrent files to add</div></Show>
      </div>
    </div>
  );
}
