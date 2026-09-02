// UI store: client-only navigation and ephemeral shell state. Server-derived
// session data lives in store/session.ts.

import { createMemo, createSignal } from "solid-js";
import {
  COLUMN_DEFINITIONS,
  DEFAULT_COLUMN_LAYOUT,
  DEFAULT_COLUMN_KEYS,
  type ColumnKey,
  type ColumnLayout,
  configToLayout,
  layoutToConfig,
} from "../lib/columns";
import type { SortKey } from "../lib/sort";

export type Route = "console" | "settings" | "stats";

const [route, setRoute] = createSignal<Route>("console");
const [query, setQuery] = createSignal("");
/** Set by the "+ Add torrent" top-bar button; the modal (Epic 7.1) consumes it. */
const [addOpen, setAddOpen] = createSignal(false);
const [moveHashes, setMoveHashes] = createSignal<string[]>([]);
const [queuedTorrentFiles, setQueuedTorrentFiles] = createSignal<File[]>([]);
const [queuedTorrentFileErrors, setQueuedTorrentFileErrors] = createSignal<string[]>([]);

export type SortColumn = ColumnKey;
export type SortDirection = "asc" | "desc";

type Filters = { status: string; label: string; tracker: string };
export type SavedFilter = Filters & { id: string; name: string; query: string };
const [filters, setFilters] = createSignal<Filters>({ status: "", label: "", tracker: "" });
const [sort, setSort] = createSignal<SortKey[]>(loadSort());
const [selectedHashes, setSelectedHashes] = createSignal<string[]>([]);
const [shownTorrentCount, setShownTorrentCount] = createSignal(0);
const [visibleHashes, setVisibleHashes] = createSignal<string[]>([]);
const [focusedHash, setFocusedHash] = createSignal("");
const [selectionAnchor, setSelectionAnchor] = createSignal("");
const [toast, setToast] = createSignal("");
const [settingsDirty, setSettingsDirty] = createSignal(false);

const COLUMN_LAYOUT_STORAGE_KEY = "blackbird.table-columns.v1";
const SORT_STORAGE_KEY = "blackbird.table-sort.v2";
const LEGACY_SORT_STORAGE_KEY = "blackbird.table-sort";
const SAVED_FILTERS_STORAGE_KEY = "blackbird.saved-filters.v1";
function hasLocalStorage() { return typeof window !== "undefined" && typeof window.localStorage !== "undefined"; }

export const moveOpen = createMemo(() => moveHashes().length > 0);
export function openMove(hashes: string[]) { setMoveHashes([...new Set(hashes)]); }
export function closeMove() { setMoveHashes([]); }
export { moveHashes };
const cloneLayout = (layout: ColumnLayout): ColumnLayout => ({ order: [...layout.order], hidden: [...layout.hidden], widths: { ...layout.widths } });

function validLayout(value: unknown): ColumnLayout | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Partial<ColumnLayout>;
  const known = new Set<ColumnKey>(DEFAULT_COLUMN_KEYS);
  const order = Array.isArray(raw.order) ? raw.order.filter((key): key is ColumnKey => typeof key === "string" && known.has(key as ColumnKey)) : [];
  const hidden = Array.isArray(raw.hidden) ? raw.hidden.filter((key): key is ColumnKey => typeof key === "string" && known.has(key as ColumnKey) && key !== "name") : [];
  const widths: Partial<Record<ColumnKey, number>> = { ...DEFAULT_COLUMN_LAYOUT.widths };
  if (raw.widths && typeof raw.widths === "object") {
    for (const key of DEFAULT_COLUMN_KEYS) {
      const width = (raw.widths as Record<string, unknown>)[key];
      const definition = COLUMN_DEFINITIONS.find((item) => item.key === key)!;
      if (typeof width === "number" && Number.isFinite(width)) widths[key] = Math.max(definition.minWidth, Math.round(width));
    }
  }
  const completeOrder = [...new Set([...order, ...DEFAULT_COLUMN_KEYS])];
  return { order: completeOrder, hidden: [...new Set(hidden)], widths };
}

function loadColumnLayout(): ColumnLayout {
  if (hasLocalStorage()) {
    try {
      const stored = window.localStorage.getItem(COLUMN_LAYOUT_STORAGE_KEY);
      const parsed = stored ? validLayout(JSON.parse(stored)) : null;
      if (parsed) return parsed;
    } catch {
      // A malformed preference should never stop the console from starting.
    }
  }
  return cloneLayout(DEFAULT_COLUMN_LAYOUT);
}

const [columnLayout, setColumnLayout] = createSignal<ColumnLayout>(loadColumnLayout());
function validSavedFilters(value: unknown): SavedFilter[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item, index) => {
    if (!item || typeof item !== "object") return [];
    const raw = item as Record<string, unknown>;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name) return [];
    return [{ id: typeof raw.id === "string" && raw.id ? raw.id : `saved-${index}-${name}`, name, query: typeof raw.query === "string" ? raw.query : "", status: typeof raw.status === "string" ? raw.status : "", label: typeof raw.label === "string" ? raw.label : "", tracker: typeof raw.tracker === "string" ? raw.tracker : "" }];
  });
}
function loadSavedFilters() {
  if (!hasLocalStorage()) return [];
  try { return validSavedFilters(JSON.parse(window.localStorage.getItem(SAVED_FILTERS_STORAGE_KEY) ?? "[]")); } catch { return []; }
}
const [savedFilters, setSavedFilters] = createSignal<SavedFilter[]>(loadSavedFilters());
function persistSavedFilters(next: SavedFilter[]) { if (hasLocalStorage()) window.localStorage.setItem(SAVED_FILTERS_STORAGE_KEY, JSON.stringify(next)); }
export const visibleColumnKeys = createMemo(() => columnLayout().order.filter((key) => key === "name" || !columnLayout().hidden.includes(key)));
export const columnDefinitions = COLUMN_DEFINITIONS;

function persistColumnLayout(layout: ColumnLayout) {
  if (hasLocalStorage()) window.localStorage.setItem(COLUMN_LAYOUT_STORAGE_KEY, JSON.stringify(layout));
}

function updateColumnLayout(update: (current: ColumnLayout) => ColumnLayout) {
  setColumnLayout((current) => {
    const next = update(cloneLayout(current));
    persistColumnLayout(next);
    return next;
  });
}

export function toggleColumn(key: ColumnKey) {
  if (key === "name") return;
  updateColumnLayout((current) => ({ ...current, hidden: current.hidden.includes(key) ? current.hidden.filter((item) => item !== key) : [...current.hidden, key] }));
}

export function reorderColumns(source: ColumnKey, target: ColumnKey) {
  if (source === target) return;
  updateColumnLayout((current) => {
    const order = current.order.filter((key) => key !== source);
    const index = order.indexOf(target);
    order.splice(index < 0 ? order.length : index, 0, source);
    return { ...current, order };
  });
}

export function setColumnWidth(key: ColumnKey, width: number) {
  const definition = COLUMN_DEFINITIONS.find((item) => item.key === key)!;
  updateColumnLayout((current) => ({ ...current, widths: { ...current.widths, [key]: Math.max(definition.minWidth, Math.round(width)) } }));
}

export function resetColumns() {
  if (hasLocalStorage()) window.localStorage.removeItem(COLUMN_LAYOUT_STORAGE_KEY);
  setColumnLayout(cloneLayout(DEFAULT_COLUMN_LAYOUT));
}

export function hydrateColumnsFromConfig(config: unknown) {
  if (hasLocalStorage() && window.localStorage.getItem(COLUMN_LAYOUT_STORAGE_KEY)) return;
  const layout = configToLayout(config);
  if (layout) setColumnLayout(layout);
}

export function columnLayoutConfig() {
  return layoutToConfig(columnLayout());
}

function normalizeSortColumn(value: unknown): SortColumn | null {
  const legacy = value === "added" ? "addedAt" : value === "finished" ? "finishedAt" : value === "created" ? "creationDate" : value;
  return typeof legacy === "string" && DEFAULT_COLUMN_KEYS.includes(legacy as SortColumn) ? legacy as SortColumn : null;
}
function validSort(value: unknown): SortKey[] | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as { keys?: unknown; column?: unknown; dir?: unknown; direction?: unknown };
  const candidates = Array.isArray(raw.keys) && raw.keys.length ? raw.keys : [{ column: raw.column, direction: raw.direction ?? raw.dir }];
  const keys: SortKey[] = [];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object") continue;
    const item = candidate as { column?: unknown; direction?: unknown; dir?: unknown };
    const column = normalizeSortColumn(item.column);
    const direction = item.direction ?? item.dir;
    if (!column || (direction !== "asc" && direction !== "desc") || keys.some((key) => key.column === column)) continue;
    keys.push({ column, direction });
    if (keys.length === 2) break;
  }
  return keys.length ? keys : null;
}
function persistSort(keys: SortKey[]) { if (hasLocalStorage()) window.localStorage.setItem(SORT_STORAGE_KEY, JSON.stringify({ keys })); }
function loadSort(): SortKey[] {
  if (hasLocalStorage()) {
    try {
      const current = validSort(JSON.parse(window.localStorage.getItem(SORT_STORAGE_KEY) ?? "null"));
      if (current) return current;
      const legacy = validSort(JSON.parse(window.localStorage.getItem(LEGACY_SORT_STORAGE_KEY) ?? "null"));
      if (legacy) { persistSort(legacy); return legacy; }
    } catch { /* malformed preferences fall through to the operator default */ }
  }
  return [{ column: "addedAt", direction: "desc" }];
}

export const selectedSet = createMemo(() => new Set(selectedHashes()));

export function setFilter(kind: keyof Filters, value: string) {
  setFilters((current) => ({ ...current, [kind]: current[kind] === value ? "" : value }));
}

export function clearFilters() {
  setFilters({ status: "", label: "", tracker: "" });
  setQuery("");
}

export function hydrateSavedFiltersFromConfig(value: unknown) {
  if (hasLocalStorage() && window.localStorage.getItem(SAVED_FILTERS_STORAGE_KEY)) return;
  const next = validSavedFilters(value);
  if (next.length) setSavedFilters(next);
}

export function saveCurrentFilter(name?: string) {
  const current = filters();
  const fallback = query().trim() || current.status || current.label || current.tracker || "Saved filter";
  const base = (name || fallback).trim() || "Saved filter";
  setSavedFilters((items) => {
    const used = new Set(items.map((item) => item.name));
    let unique = base; let suffix = 2;
    while (used.has(unique)) unique = `${base} ${suffix++}`;
    const next = [...items, { id: `saved-${Date.now()}-${Math.random().toString(36).slice(2)}`, name: unique, query: query(), ...current }];
    persistSavedFilters(next); return next;
  });
}

export function applySavedFilter(saved: SavedFilter) {
  setFilters({ status: saved.status, label: saved.label, tracker: saved.tracker });
  setQuery(saved.query);
}

export function removeSavedFilter(id: string) {
  setSavedFilters((items) => { const next = items.filter((item) => item.id !== id); persistSavedFilters(next); return next; });
}

export function savedFilterIsActive(saved: SavedFilter) {
  const current = filters();
  return query() === saved.query && current.status === saved.status && current.label === saved.label && current.tracker === saved.tracker;
}

export function hydrateSortFromConfig(value: unknown) {
  if (hasLocalStorage() && (window.localStorage.getItem(SORT_STORAGE_KEY) || window.localStorage.getItem(LEGACY_SORT_STORAGE_KEY))) return;
  const keys = validSort(value);
  if (keys) setSort(keys);
}

export function changeSort(column: SortColumn, addSecondary = false) {
  setSort((current) => {
    const index = current.findIndex((key) => key.column === column);
    let next: SortKey[];
    if (!addSecondary) {
      const direction: SortDirection = index === 0 && current[0].direction === "asc" ? "desc" : "asc";
      next = [{ column, direction }];
    } else if (index >= 0) {
      next = current.map((key, keyIndex) => keyIndex === index ? { ...key, direction: key.direction === "asc" ? "desc" : "asc" } : key);
    } else {
      next = [...current.slice(0, 1), { column, direction: "asc" }];
    }
    persistSort(next);
    return next;
  });
}

export function selectOnly(hash: string) {
  setSelectedHashes([hash]);
  setFocusedHash(hash);
  setSelectionAnchor(hash);
}

export function toggleSelection(hash: string) {
  setSelectedHashes((current) => current.includes(hash) ? current.filter((item) => item !== hash) : [...current, hash]);
  setFocusedHash(hash);
  setSelectionAnchor(hash);
}

export function selectRange(hash: string, visibleHashes: string[]) {
  const anchor = selectionAnchor();
  const start = visibleHashes.indexOf(anchor);
  const end = visibleHashes.indexOf(hash);
  if (start < 0 || end < 0) {
    selectOnly(hash);
    return;
  }
  setSelectedHashes(visibleHashes.slice(Math.min(start, end), Math.max(start, end) + 1));
  setFocusedHash(hash);
}

export function selectAllVisible(hashes: string[]) {
  setSelectedHashes(hashes);
  if (!focusedHash() && hashes[0]) setFocusedHash(hashes[0]);
  if (hashes[0]) setSelectionAnchor(hashes[0]);
}

/** Moves the keyboard focus through the currently filtered table rows. */
export function moveSelection(direction: -1 | 1, hashes: string[], extend: boolean) {
  if (!hashes.length) return;
  const current = focusedHash();
  const selected = selectedHashes();
  let index = current ? hashes.indexOf(current) : -1;
  if (index < 0 && selected.length) index = hashes.indexOf(selected[0]);
  const target = index < 0
    ? (direction > 0 ? 0 : hashes.length - 1)
    : Math.max(0, Math.min(hashes.length - 1, index + direction));
  if (extend) selectRange(hashes[target], hashes);
  else selectOnly(hashes[target]);
}

/** Removes selections whose keyed row has disappeared from the live session. */
export function pruneSelection(available: Set<string>) {
  setSelectedHashes((current) => current.filter((hash) => available.has(hash)));
  if (focusedHash() && !available.has(focusedHash())) setFocusedHash("");
}

let toastTimer: number | undefined;
export function showToast(message: string) {
  setToast(message);
  if (toastTimer) window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => setToast(""), 4200);
}

/** Opens intake with any .torrent files dropped on the console table. */
export function openAdd(files: File[] = []) {
  const accepted = files.filter((file) => file.name.toLowerCase().endsWith(".torrent"));
  const rejected = files.filter((file) => !file.name.toLowerCase().endsWith(".torrent")).map((file) => `${file.name}: not a .torrent file`);
  if (accepted.length) setQueuedTorrentFiles((current) => [...current, ...accepted]);
  if (rejected.length) setQueuedTorrentFileErrors((current) => [...current, ...rejected]);
  setAddOpen(true);
}

export function closeAdd() {
  setAddOpen(false);
  setQueuedTorrentFiles([]);
  setQueuedTorrentFileErrors([]);
}

export {
  addOpen, columnLayout, filters, focusedHash, query, queuedTorrentFileErrors, queuedTorrentFiles, route, savedFilters, selectedHashes, selectionAnchor, settingsDirty, shownTorrentCount, sort, toast, visibleHashes,
  setAddOpen, setFocusedHash, setQuery, setRoute, setSettingsDirty, setShownTorrentCount, setQueuedTorrentFileErrors, setQueuedTorrentFiles,
  setVisibleHashes,
};
