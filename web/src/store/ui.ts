// UI store: client-only navigation and ephemeral shell state. Server-derived
// session data lives in store/session.ts.

import { batch, createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { deriveAccentTokens, readToken, supportsColorMix } from "../lib/theme.js";
import { notify, type NoticeAction, type NoticeKind } from "./notifications";
import { buildHash, parseHash } from "../lib/router.js";
import {
  COLUMN_DEFINITIONS,
  DEFAULT_COLUMN_LAYOUT,
  DEFAULT_COLUMN_KEYS,
  type ColumnKey,
  type ColumnLayout,
  configToLayout,
  layoutToConfig,
} from "../lib/columns.js";
import type { SortKey } from "../lib/sort.js";
import { setFormatPrefs } from "../lib/format.js";
import {
  applyThemeId,
  labelChipStyle,
  loadThemeChoice,
  parseThemeChoice,
  prefersDarkScheme,
  resolveThemeId,
  saveThemeChoice,
  themeDef,
  THEME_IDS,
  type Density,
  type ThemeChoice,
  type ThemeId,
} from "../lib/themes.js";
import { customFileByName, effectiveCustomName, ensureCustomCss } from "./custom.js";
import { customThemeId, resolveCustomAccents } from "../lib/custom-themes.js";
import {
  DEFAULT_PEER_COLUMN_KEYS,
  DEFAULT_PEER_COLUMN_LAYOUT,
  PEER_ALWAYS_COLUMN,
  PEER_COLUMN_DEFINITIONS,
  type PeerColumnKey,
  type PeerColumnLayout,
  isValidPeerColumn,
} from "../lib/peerColumns.js";
import type { PeerSortKey } from "../lib/peerSort.js";

export type Route =
  "console" | "settings" | "stats" | "rss" | "history" | "attention" | "preservation";

const [route, setRoute] = createSignal<Route>("console");

/** Active Settings section (POL-8.6): owned by the router so deep links,
 * back/forward, and the nav share one source instead of mount-only state. */
const [settingsSection, setSettingsSection] = createSignal<string>("Connection");
export { settingsSection, setSettingsSection };

/** Focus hash waiting for a session that contains it (boot deep link or a
 * URL applied while disconnected). Plain state, not a signal: the snapshot
 * path consumes it exactly once. Any explicit selection supersedes it.
 * Declared up here so boot/apply code below never hits TDZ. */
let pendingFocusHash = "";
export function setPendingFocus(hash: string) {
  pendingFocusHash = hash;
}
export function consumePendingFocus(): string {
  const hash = pendingFocusHash;
  pendingFocusHash = "";
  return hash;
}

/** Canonical settings sections (mirrors SettingsPanel NAV; the router
 * validates slugs against this so bad URLs fall back instead of blanking). */
export const SETTINGS_SECTIONS = [
  "General",
  "Connection",
  "Bandwidth",
  "Seeding",
  "Scheduler",
  "Queue",
  "Directories",
  "Labels",
  "Automation",
  "Interface",
  "History",
  "Advanced",
  "About",
];

function validSection(section: string): string {
  return SETTINGS_SECTIONS.includes(section) ? section : "Connection";
}
const [query, setQuery] = createSignal("");
/** Set by the "+ Add torrent" top-bar button; the modal (Epic 7.1) consumes it. */
const [addOpen, setAddOpen] = createSignal(false);
const [moveHashes, setMoveHashes] = createSignal<string[]>([]);
const [queuedTorrentFiles, setQueuedTorrentFiles] = createSignal<File[]>([]);
const [queuedTorrentFileErrors, setQueuedTorrentFileErrors] = createSignal<string[]>([]);

export type SortColumn = ColumnKey;
export type SortDirection = "asc" | "desc";

/** Daemon-dependent controls hydrated from /api/settings (PAR-2.7). */
export type DaemonInfo = { renameSupported: boolean; openUrlTemplate: string };
const [daemonInfo, setDaemonInfo] = createSignal<DaemonInfo>({
  renameSupported: false,
  openUrlTemplate: "",
});
export function hydrateDaemonInfo(
  capabilities?: { rename?: boolean },
  directories?: { open_url_template?: string },
) {
  setDaemonInfo({
    renameSupported: capabilities?.rename === true,
    openUrlTemplate: directories?.open_url_template ?? "",
  });
}

type Filters = { status: string; label: string; tracker: string; throttle: string };
export type SavedFilter = Filters & { id: string; name: string; query: string };
const [filters, setFilters] = createSignal<Filters>({
  status: "",
  label: "",
  tracker: "",
  throttle: "",
});
const [sort, setSort] = createSignal<SortKey[]>(loadSort());
const [selectedHashes, setSelectedHashes] = createSignal<string[]>([]);
const [focusedHash, setFocusedHash] = createSignal("");
const [selectionAnchor, setSelectionAnchor] = createSignal("");
const [settingsDirty, setSettingsDirty] = createSignal(false);

/** Debounced filter query for the torrent view (PERF-7.2): the table input
 * writes `query` per keystroke; the ordered index consumes this 150ms-late
 * value so typing never triggers a refilter per keypress. */
const [debouncedQuery, setDebouncedQuery] = createSignal("");
createEffect(() => {
  const value = query().trim().toLowerCase();
  const timer = setTimeout(() => {
    if (value !== untrack(debouncedQuery)) setDebouncedQuery(value);
  }, 150);
  onCleanup(() => clearTimeout(timer));
});

const COLUMN_LAYOUT_STORAGE_KEY = "blackbird.table-columns.v1";
const SORT_STORAGE_KEY = "blackbird.table-sort.v2";
const LEGACY_SORT_STORAGE_KEY = "blackbird.table-sort";
const SAVED_FILTERS_STORAGE_KEY = "blackbird.saved-filters.v1";
function hasLocalStorage() {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}

export const moveOpen = createMemo(() => moveHashes().length > 0);
export function openMove(hashes: string[]) {
  setMoveHashes([...new Set(hashes)]);
}
export function closeMove() {
  setMoveHashes([]);
}
export { moveHashes };
const cloneLayout = (layout: ColumnLayout): ColumnLayout => ({
  order: [...layout.order],
  hidden: [...layout.hidden],
  widths: { ...layout.widths },
});

function validLayout(value: unknown): ColumnLayout | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Partial<ColumnLayout>;
  const known = new Set<ColumnKey>(DEFAULT_COLUMN_KEYS);
  const order = Array.isArray(raw.order)
    ? raw.order.filter(
        (key): key is ColumnKey => typeof key === "string" && known.has(key as ColumnKey),
      )
    : [];
  const hidden = Array.isArray(raw.hidden)
    ? raw.hidden.filter(
        (key): key is ColumnKey =>
          typeof key === "string" && known.has(key as ColumnKey) && key !== "name",
      )
    : [];
  const widths: Partial<Record<ColumnKey, number>> = { ...DEFAULT_COLUMN_LAYOUT.widths };
  if (raw.widths && typeof raw.widths === "object") {
    for (const key of DEFAULT_COLUMN_KEYS) {
      const width = (raw.widths as Record<string, unknown>)[key];
      const definition = COLUMN_DEFINITIONS.find((item) => item.key === key)!;
      if (typeof width === "number" && Number.isFinite(width))
        widths[key] = Math.max(definition.minWidth, Math.round(width));
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
    return [
      {
        id: typeof raw.id === "string" && raw.id ? raw.id : `saved-${index}-${name}`,
        name,
        query: typeof raw.query === "string" ? raw.query : "",
        status: typeof raw.status === "string" ? raw.status : "",
        label: typeof raw.label === "string" ? raw.label : "",
        tracker: typeof raw.tracker === "string" ? raw.tracker : "",
        throttle: typeof raw.throttle === "string" ? raw.throttle : "",
      },
    ];
  });
}
function loadSavedFilters() {
  if (!hasLocalStorage()) return [];
  try {
    return validSavedFilters(
      JSON.parse(window.localStorage.getItem(SAVED_FILTERS_STORAGE_KEY) ?? "[]"),
    );
  } catch {
    return [];
  }
}
const [savedFilters, setSavedFilters] = createSignal<SavedFilter[]>(loadSavedFilters());
function persistSavedFilters(next: SavedFilter[]) {
  if (hasLocalStorage())
    window.localStorage.setItem(SAVED_FILTERS_STORAGE_KEY, JSON.stringify(next));
}
export const visibleColumnKeys = createMemo(() =>
  columnLayout().order.filter((key) => key === "name" || !columnLayout().hidden.includes(key)),
);
export const columnDefinitions = COLUMN_DEFINITIONS;

function persistColumnLayout(layout: ColumnLayout) {
  if (hasLocalStorage())
    window.localStorage.setItem(COLUMN_LAYOUT_STORAGE_KEY, JSON.stringify(layout));
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
  updateColumnLayout((current) => ({
    ...current,
    hidden: current.hidden.includes(key)
      ? current.hidden.filter((item) => item !== key)
      : [...current.hidden, key],
  }));
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
  updateColumnLayout((current) => ({
    ...current,
    widths: { ...current.widths, [key]: Math.max(definition.minWidth, Math.round(width)) },
  }));
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

// ---- Peer columns + sort (PAR-2.4: browser-local, independent of the table) ----

const PEER_COLUMN_LAYOUT_STORAGE_KEY = "blackbird.peer-columns.v1";
const PEER_SORT_STORAGE_KEY = "blackbird.peer-sort.v1";

function clonePeerLayout(layout: PeerColumnLayout): PeerColumnLayout {
  return { order: [...layout.order], hidden: [...layout.hidden], widths: { ...layout.widths } };
}

function validPeerLayout(value: unknown): PeerColumnLayout | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Partial<PeerColumnLayout>;
  const order = Array.isArray(raw.order) ? raw.order.filter(isValidPeerColumn) : [];
  const hidden = Array.isArray(raw.hidden)
    ? raw.hidden.filter(
        (key): key is PeerColumnKey => isValidPeerColumn(key) && key !== PEER_ALWAYS_COLUMN,
      )
    : [];
  const widths: Partial<Record<PeerColumnKey, number>> = { ...DEFAULT_PEER_COLUMN_LAYOUT.widths };
  if (raw.widths && typeof raw.widths === "object") {
    for (const key of DEFAULT_PEER_COLUMN_KEYS) {
      const width = (raw.widths as Record<string, unknown>)[key];
      const definition = PEER_COLUMN_DEFINITIONS.find((c) => c.key === key)!;
      if (typeof width === "number" && Number.isFinite(width))
        widths[key] = Math.max(definition.minWidth, Math.round(width));
    }
  }
  const completeOrder = [...new Set([...order, ...DEFAULT_PEER_COLUMN_KEYS])];
  return { order: completeOrder, hidden: [...new Set(hidden)], widths };
}

function loadPeerColumnLayout(): PeerColumnLayout {
  if (hasLocalStorage()) {
    try {
      const stored = window.localStorage.getItem(PEER_COLUMN_LAYOUT_STORAGE_KEY);
      const parsed = stored ? validPeerLayout(JSON.parse(stored)) : null;
      if (parsed) return parsed;
    } catch {
      /* malformed preference falls through to defaults */
    }
  }
  return clonePeerLayout(DEFAULT_PEER_COLUMN_LAYOUT);
}

const [peerColumnLayout, setPeerColumnLayout] =
  createSignal<PeerColumnLayout>(loadPeerColumnLayout());

function persistPeerColumnLayout(layout: PeerColumnLayout) {
  if (hasLocalStorage())
    window.localStorage.setItem(PEER_COLUMN_LAYOUT_STORAGE_KEY, JSON.stringify(layout));
}

function updatePeerColumnLayout(update: (current: PeerColumnLayout) => PeerColumnLayout) {
  setPeerColumnLayout((current) => {
    const next = update(clonePeerLayout(current));
    persistPeerColumnLayout(next);
    return next;
  });
}

/** Visible peer columns in layout order; the IP column is always shown. */
export const visiblePeerColumnKeys = createMemo(() =>
  peerColumnLayout().order.filter(
    (key) => key === PEER_ALWAYS_COLUMN || !peerColumnLayout().hidden.includes(key),
  ),
);

export function togglePeerColumn(key: PeerColumnKey) {
  if (key === PEER_ALWAYS_COLUMN) return;
  updatePeerColumnLayout((current) => ({
    ...current,
    hidden: current.hidden.includes(key)
      ? current.hidden.filter((item) => item !== key)
      : [...current.hidden, key],
  }));
}

export function reorderPeerColumns(source: PeerColumnKey, target: PeerColumnKey) {
  if (source === target) return;
  updatePeerColumnLayout((current) => {
    const order = current.order.filter((key) => key !== source);
    const index = order.indexOf(target);
    order.splice(index < 0 ? order.length : index, 0, source);
    return { ...current, order };
  });
}

export function resetPeerColumns() {
  if (hasLocalStorage()) window.localStorage.removeItem(PEER_COLUMN_LAYOUT_STORAGE_KEY);
  setPeerColumnLayout(clonePeerLayout(DEFAULT_PEER_COLUMN_LAYOUT));
}

export function validPeerSort(value: unknown): PeerSortKey[] | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as { keys?: unknown; column?: unknown; direction?: unknown };
  const candidates =
    Array.isArray(raw.keys) && raw.keys.length
      ? raw.keys
      : [{ column: raw.column, direction: raw.direction }];
  const keys: PeerSortKey[] = [];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object") continue;
    const item = candidate as { column?: unknown; direction?: unknown };
    const column = isValidPeerColumn(item.column) ? item.column : null;
    const direction = item.direction;
    if (
      !column ||
      (direction !== "asc" && direction !== "desc") ||
      keys.some((key) => key.column === column)
    )
      continue;
    keys.push({ column, direction });
  }
  return keys.length ? keys : null;
}

const DEFAULT_PEER_SORT: PeerSortKey[] = [{ column: "ip", direction: "asc" }];

function loadPeerSort(): PeerSortKey[] {
  if (hasLocalStorage()) {
    try {
      const parsed = validPeerSort(
        JSON.parse(window.localStorage.getItem(PEER_SORT_STORAGE_KEY) ?? "null"),
      );
      if (parsed) return parsed;
    } catch {
      /* malformed preference falls through to the default */
    }
  }
  return DEFAULT_PEER_SORT;
}

const [peerSort, setPeerSort] = createSignal<PeerSortKey[]>(loadPeerSort());
function persistPeerSort(keys: PeerSortKey[]) {
  if (hasLocalStorage())
    window.localStorage.setItem(PEER_SORT_STORAGE_KEY, JSON.stringify({ keys }));
}

/** Click a peer column header to sort; cycle asc→desc→default on repeat clicks. */
export function changePeerSort(column: PeerColumnKey) {
  setPeerSort((current) => {
    const [primary] = current;
    if (primary?.column === column) {
      // asc -> desc -> clear (back to default ip asc)
      if (primary.direction === "asc") return persistAndReturn([{ column, direction: "desc" }]);
      return persistAndReturn(DEFAULT_PEER_SORT);
    }
    return persistAndReturn([{ column, direction: "asc" }]);
  });
}

function persistAndReturn(next: PeerSortKey[]): PeerSortKey[] {
  persistPeerSort(next);
  return next;
}

// ---- Detail panel prefs (PAR-2.5): active tab, height, collapsed ----

export type DetailTab =
  "files" | "peers" | "trackers" | "general" | "speed" | "logger" | "transfer" | "pieces" | "why";
export const DETAIL_TABS: DetailTab[] = [
  "files",
  "peers",
  "trackers",
  "general",
  "speed",
  "logger",
  "transfer",
  "pieces",
  "why",
];
const DETAIL_PANEL_STORAGE_KEY = "blackbird.detail-panel.v1";
const MIN_PANEL_HEIGHT = 120;
const MAX_PANEL_HEIGHT = Math.max(
  600,
  (typeof window !== "undefined" ? window.innerHeight : 600) - 180,
);

export type DetailPanelPrefs = { tab: DetailTab; height: number; collapsed: boolean };
const DEFAULT_DETAIL_PREFS: DetailPanelPrefs = { tab: "files", height: 288, collapsed: false };

function validDetailPrefs(value: unknown): DetailPanelPrefs | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Partial<DetailPanelPrefs>;
  const tab = DETAIL_TABS.includes(raw.tab as DetailTab) ? (raw.tab as DetailTab) : null;
  if (!tab) return null;
  const height =
    typeof raw.height === "number" && Number.isFinite(raw.height)
      ? Math.min(MAX_PANEL_HEIGHT, Math.max(MIN_PANEL_HEIGHT, Math.round(raw.height)))
      : DEFAULT_DETAIL_PREFS.height;
  return { tab, height, collapsed: raw.collapsed === true };
}

function loadDetailPrefs(): DetailPanelPrefs {
  if (hasLocalStorage()) {
    try {
      const parsed = validDetailPrefs(
        JSON.parse(window.localStorage.getItem(DETAIL_PANEL_STORAGE_KEY) ?? "null"),
      );
      if (parsed) return parsed;
    } catch {
      /* malformed preference falls through to defaults */
    }
  }
  return { ...DEFAULT_DETAIL_PREFS };
}

const [detailPrefs, setDetailPrefs] = createSignal<DetailPanelPrefs>(loadDetailPrefs());
function persistDetailPrefs(next: DetailPanelPrefs) {
  if (hasLocalStorage())
    window.localStorage.setItem(DETAIL_PANEL_STORAGE_KEY, JSON.stringify(next));
}
function updateDetailPrefs(patch: Partial<DetailPanelPrefs>) {
  setDetailPrefs((current) => {
    const next = { ...current, ...patch };
    if (patch.height !== undefined) {
      next.height = Math.min(
        MAX_PANEL_HEIGHT,
        Math.max(MIN_PANEL_HEIGHT, Math.round(patch.height)),
      );
    }
    persistDetailPrefs(next);
    return next;
  });
}

export function setDetailTab(tab: DetailTab) {
  updateDetailPrefs({ tab });
}
export function setDetailHeight(height: number) {
  updateDetailPrefs({ height });
}
export function setDetailCollapsed(collapsed: boolean) {
  updateDetailPrefs({ collapsed });
}

function normalizeSortColumn(value: unknown): SortColumn | null {
  const legacy =
    value === "added"
      ? "addedAt"
      : value === "finished"
        ? "finishedAt"
        : value === "created"
          ? "creationDate"
          : value;
  return typeof legacy === "string" && DEFAULT_COLUMN_KEYS.includes(legacy as SortColumn)
    ? (legacy as SortColumn)
    : null;
}
function validSort(value: unknown): SortKey[] | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as { keys?: unknown; column?: unknown; dir?: unknown; direction?: unknown };
  const candidates =
    Array.isArray(raw.keys) && raw.keys.length
      ? raw.keys
      : [{ column: raw.column, direction: raw.direction ?? raw.dir }];
  const keys: SortKey[] = [];
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object") continue;
    const item = candidate as { column?: unknown; direction?: unknown; dir?: unknown };
    const column = normalizeSortColumn(item.column);
    const direction = item.direction ?? item.dir;
    if (
      !column ||
      (direction !== "asc" && direction !== "desc") ||
      keys.some((key) => key.column === column)
    )
      continue;
    keys.push({ column, direction });
    if (keys.length === 2) break;
  }
  return keys.length ? keys : null;
}
function persistSort(keys: SortKey[]) {
  if (hasLocalStorage()) window.localStorage.setItem(SORT_STORAGE_KEY, JSON.stringify({ keys }));
}
function loadSort(): SortKey[] {
  if (hasLocalStorage()) {
    try {
      const current = validSort(
        JSON.parse(window.localStorage.getItem(SORT_STORAGE_KEY) ?? "null"),
      );
      if (current) return current;
      const legacy = validSort(
        JSON.parse(window.localStorage.getItem(LEGACY_SORT_STORAGE_KEY) ?? "null"),
      );
      if (legacy) {
        persistSort(legacy);
        return legacy;
      }
    } catch {
      /* malformed preferences fall through to the operator default */
    }
  }
  return [{ column: "addedAt", direction: "desc" }];
}

export const selectedSet = createMemo(() => new Set(selectedHashes()));

export function setFilter(kind: keyof Filters, value: string) {
  setFilters((current) => ({ ...current, [kind]: current[kind] === value ? "" : value }));
}

export function clearFilters() {
  setFilters({ status: "", label: "", tracker: "", throttle: "" });
  setQuery("");
}

export function hydrateSavedFiltersFromConfig(value: unknown) {
  if (hasLocalStorage() && window.localStorage.getItem(SAVED_FILTERS_STORAGE_KEY)) return;
  const next = validSavedFilters(value);
  if (next.length) setSavedFilters(next);
}

export function saveCurrentFilter(name?: string) {
  const current = filters();
  const fallback =
    query().trim() ||
    current.status ||
    current.label ||
    current.tracker ||
    current.throttle ||
    "Saved filter";
  const base = (name || fallback).trim() || "Saved filter";
  setSavedFilters((items) => {
    const used = new Set(items.map((item) => item.name));
    let unique = base;
    let suffix = 2;
    while (used.has(unique)) unique = `${base} ${suffix++}`;
    const next = [
      ...items,
      {
        id: `saved-${Date.now()}-${Math.random().toString(36).slice(2)}`,
        name: unique,
        query: query(),
        ...current,
      },
    ];
    persistSavedFilters(next);
    return next;
  });
}

export function applySavedFilter(saved: SavedFilter) {
  setFilters({
    status: saved.status,
    label: saved.label,
    tracker: saved.tracker,
    throttle: saved.throttle ?? "",
  });
  setQuery(saved.query);
}

export function removeSavedFilter(id: string) {
  setSavedFilters((items) => {
    const next = items.filter((item) => item.id !== id);
    persistSavedFilters(next);
    return next;
  });
}

export function savedFilterIsActive(saved: SavedFilter) {
  const current = filters();
  return (
    query() === saved.query &&
    current.status === saved.status &&
    current.label === saved.label &&
    current.tracker === saved.tracker
  );
}

export function hydrateSortFromConfig(value: unknown) {
  if (
    hasLocalStorage() &&
    (window.localStorage.getItem(SORT_STORAGE_KEY) ||
      window.localStorage.getItem(LEGACY_SORT_STORAGE_KEY))
  )
    return;
  const keys = validSort(value);
  if (keys) setSort(keys);
}

const HEX_COLOR = /^#[0-9a-f]{6}$/i;

/** Bumped on every accent/theme application so token readers (piece map,
 * theme-color meta, favicon) re-resolve computed values (THM-9.1). */
const [themeVersion, setThemeVersion] = createSignal(0);
export { themeVersion };

/* ---- Theme choice (THM-9.2) ----
 * Browser override (localStorage) wins; otherwise the operator default from
 * YAML (`ui.theme`, via the server-injected meta or /api/settings).
 * `system` resolves through prefers-color-scheme and follows it live. */
const [browserTheme, setBrowserThemeSignal] = createSignal<ThemeChoice | null>(null);
const [serverTheme, setServerTheme] = createSignal<ThemeChoice>("dark");
const [prefersDark, setPrefersDark] = createSignal(true);
const [lastAccent, setLastAccent] = createSignal("");
export { browserTheme, serverTheme, prefersDark };

/** Effective stored choice: browser override, else the operator default. */
export function effectiveThemeChoice(): ThemeChoice {
  return previewTheme() ?? browserTheme() ?? serverTheme();
}

/** Concrete theme id after system resolution, or `custom-<id>` when a
 * file theme is effectively active (staged preview wins, THM-9.4). */
export function resolvedTheme(): string {
  const custom = effectiveCustomName();
  if (custom !== null) return `custom-${customThemeId(custom)}`;
  return resolveThemeId(effectiveThemeChoice(), prefersDark());
}

/** Accent presets for the visible theme: custom file presets when a file
 * theme is effectively active, else the built-in presets. */
export function activeAccents(): string[] {
  const custom = effectiveCustomName();
  if (custom !== null) {
    const file = customFileByName(custom);
    if (file) return resolveCustomAccents(file, (id) => themeDef(id as ThemeId).accents);
  }
  const id = resolvedTheme();
  return THEME_IDS.includes(id as ThemeId)
    ? themeDef(id as ThemeId).accents
    : themeDef("dark").accents;
}

/** Whether the visible theme is dark-surfaced (custom files carry their
 * own flag; unknown ids default dark). Drives color-scheme. */
export function resolvedThemeDark(): boolean {
  const custom = effectiveCustomName();
  if (custom !== null) return customFileByName(custom)?.dark ?? true;
  const id = resolvedTheme();
  return THEME_IDS.includes(id as ThemeId) ? themeDef(id as ThemeId).dark : true;
}
/** Label for the visible theme (custom files show their file name). */
export function activeThemeLabel(): string {
  const custom = effectiveCustomName();
  if (custom !== null) return customFileByName(custom)?.name ?? custom;
  const id = resolvedTheme();
  return THEME_IDS.includes(id as ThemeId) ? themeDef(id as ThemeId).label : id;
}

function hasWindowForTheme() {
  return typeof window !== "undefined" && typeof window.document !== "undefined";
}

/** Applies the resolved theme + last accent (fallback inks re-derive per
 * theme for browsers without color-mix). Idempotent. */
export function applyResolvedTheme() {
  if (!hasWindowForTheme()) return;
  const custom = effectiveCustomName();
  if (custom !== null) ensureCustomCss(custom);
  const id = applyThemeId(document, resolvedTheme());
  void id;
  // Native controls follow theme darkness (THM-9.5); the stylesheet holds
  // the static fallback for pre-JS paint.
  document.documentElement.style.colorScheme = resolvedThemeDark() ? "dark" : "light";
  const accent = lastAccent();
  if (accent) applyAccent(accent);
  else setThemeVersion((v) => v + 1);
}

let mediaCleanup: (() => void) | null = null;

/** Boot wiring (call once from App): meta/server default, stored choice,
 * live system tracking. Safe to call repeatedly (tests, HMR). */
export function initTheme() {
  if (hasWindowForTheme()) {
    try {
      const meta = document.querySelector('meta[name="blackbird-theme-default"]');
      const fromMeta = parseThemeChoice(meta?.getAttribute("content"));
      if (fromMeta) setServerTheme(fromMeta);
    } catch {
      /* headless: keep dark */
    }
    setBrowserThemeSignal(loadThemeChoice(hasLocalStorage() ? window.localStorage : null));
    try {
      setPrefersDark(prefersDarkScheme(window));
      const mq = window.matchMedia("(prefers-color-scheme: dark)");
      mediaCleanup?.();
      const onChange = (event: MediaQueryListEvent) => {
        setPrefersDark(event.matches);
        applyResolvedTheme();
      };
      mq.addEventListener("change", onChange);
      mediaCleanup = () => mq.removeEventListener("change", onChange);
    } catch {
      /* matchMedia absent (tests): resolution defaults to dark */
    }
  }
  applyResolvedTheme();
}

/** Persist a browser theme override; null clears back to the operator default. */
export function setBrowserTheme(choice: ThemeChoice | null) {
  if (hasWindowForTheme()) saveThemeChoice(window.localStorage, choice);
  setBrowserThemeSignal(choice);
  applyResolvedTheme();
}

export type { Density };
const DENSITY_STORAGE_KEY = "blackbird.density.v1";

function parseDensity(value: unknown): Density | null {
  return value === "dense" || value === "comfortable" ? value : null;
}

const [browserDensity, setBrowserDensitySignal] = createSignal<Density>("dense");
export { browserDensity };

/* ---- Appearance preview (THM-9.3) ----
 * The Interface picker previews browser theme/density choices live without
 * persisting: Save commits them, Revert (or leaving Settings) discards.
 * `previewTheme/Density` null = no pending preview. */
const [previewTheme, setPreviewThemeSignal] = createSignal<ThemeChoice | null>(null);
const [previewDensity, setPreviewDensitySignal] = createSignal<Density | null>(null);
export { previewTheme, previewDensity };

export function previewsActive(): boolean {
  return previewTheme() !== null || previewDensity() !== null;
}

/** Stage a browser theme preview (live, unpersisted). */
export function previewBrowserTheme(choice: ThemeChoice | null) {
  setPreviewThemeSignal(choice);
  applyResolvedTheme();
}

/** Stage a density preview (live, unpersisted). */
export function previewDensityChoice(density: Density | null) {
  setPreviewDensitySignal(density);
  applyDensity();
}

/** Persist staged previews to this browser. Returns whether any existed. */
export function commitAppearancePreviews(): boolean {
  const theme = previewTheme();
  const density = previewDensity();
  if (theme !== null) setBrowserTheme(theme);
  if (density !== null) setBrowserDensity(density);
  setPreviewThemeSignal(null);
  setPreviewDensitySignal(null);
  return theme !== null || density !== null;
}

/** Drop staged previews, restoring the committed appearance. */
export function discardAppearancePreviews() {
  if (!previewsActive()) return;
  setPreviewThemeSignal(null);
  setPreviewDensitySignal(null);
  applyResolvedTheme();
  applyDensity();
}

/** Persist a density override; null is unused (dense is the default). */
export function setBrowserDensity(density: Density) {
  if (hasWindowForTheme()) {
    try {
      window.localStorage.setItem(DENSITY_STORAGE_KEY, density);
    } catch {
      /* private mode */
    }
  }
  setBrowserDensitySignal(density);
  applyDensity();
}

/** Effective density: staged preview, else the browser choice. */
export function resolvedDensity(): Density {
  return previewDensity() ?? browserDensity();
}

/** Applies the resolved density to <html>. Idempotent. */
export function applyDensity() {
  if (!hasWindowForTheme()) return;
  document.documentElement.dataset.density = resolvedDensity();
}

/** Reads the stored density (boot + inline-script parity). */
export function loadBrowserDensity(storage?: Storage | null): Density {
  try {
    return parseDensity(storage?.getItem(DENSITY_STORAGE_KEY)) ?? "dense";
  } catch {
    return "dense";
  }
}

/** Boot wiring for density (call once from App alongside initTheme). */
export function initDensity() {
  if (hasWindowForTheme() && hasLocalStorage()) {
    try {
      setBrowserDensitySignal(loadBrowserDensity(window.localStorage));
    } catch {
      /* keep dense */
    }
  }
  applyDensity();
}

const DERIVED_PROPS = [
  "--accent-tint",
  "--accent-tint-strong",
  "--accent-text",
  "--focus-ring",
] as const;

/** Applies an accent hex to the document root (single tweakable token).
 * Derived tokens (--accent-tint*, --accent-text, --focus-ring) resolve from
 * it via color-mix() in CSS; browsers without color-mix() get the same
 * values set explicitly from the JS mirror (lib/theme.ts). */
export function applyAccent(accent: string) {
  if (typeof document === "undefined" || !HEX_COLOR.test(accent)) return;
  setLastAccent(accent);
  const root = document.documentElement;
  root.style.setProperty("--accent", accent);
  if (supportsColorMix(document)) {
    // CSS owns the derivations; drop any fallback values a previous
    // application may have set inline so they cannot go stale.
    for (const prop of DERIVED_PROPS) root.style.removeProperty(prop);
  } else {
    // No color-mix(): mirror the derivations explicitly, reading the
    // theme's ink endpoint so light themes stay readable.
    const ink = readToken("--accent-ink") || "#ffffff";
    const derived = deriveAccentTokens(accent, ink);
    root.style.setProperty("--accent-tint", derived.tint);
    root.style.setProperty("--accent-tint-strong", derived.tintStrong);
    root.style.setProperty("--accent-text", derived.text);
    root.style.setProperty("--focus-ring", derived.ring);
  }
  setThemeVersion((v) => v + 1);
}

/** Removes an explicit accent override so the theme default shows again
 * (empty accent draft, Settings revert). */
export function clearAccentOverride() {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.style.removeProperty("--accent");
  for (const prop of DERIVED_PROPS) root.style.removeProperty(prop);
  setLastAccent("");
  setThemeVersion((v) => v + 1);
}

/** Display preferences + accent + theme from the operator YAML (POL-8.4):
 * applied at boot from /api/settings, so the console never waits for
 * Settings to mount. Invalid values keep the built-in defaults. */
export function hydrateAppearance(ui: {
  accent?: unknown;
  theme?: unknown;
  date_format?: unknown;
  rate_format?: unknown;
}) {
  if (typeof ui.accent === "string") applyAccent(ui.accent);
  const theme = parseThemeChoice(ui.theme);
  if (theme) {
    setServerTheme(theme);
    // Re-resolve when following the operator default.
    if (browserTheme() === null) applyResolvedTheme();
  }
  setFormatPrefs({
    ...(ui.date_format === "local" || ui.date_format === "iso"
      ? { dateFormat: ui.date_format }
      : {}),
    ...(ui.rate_format === "binary" || ui.rate_format === "decimal"
      ? { rateFormat: ui.rate_format }
      : {}),
  });
}

export function changeSort(column: SortColumn, addSecondary = false) {
  setSort((current) => {
    const index = current.findIndex((key) => key.column === column);
    let next: SortKey[];
    if (!addSecondary) {
      const direction: SortDirection =
        index === 0 && current[0].direction === "asc" ? "desc" : "asc";
      next = [{ column, direction }];
    } else if (index >= 0) {
      next = current.map((key, keyIndex) =>
        keyIndex === index ? { ...key, direction: key.direction === "asc" ? "desc" : "asc" } : key,
      );
    } else {
      next = [...current.slice(0, 1), { column, direction: "asc" }];
    }
    persistSort(next);
    return next;
  });
}

export function selectOnly(hash: string) {
  pendingFocusHash = "";
  setSelectedHashes([hash]);
  setFocusedHash(hash);
  setSelectionAnchor(hash);
}

export function toggleSelection(hash: string) {
  pendingFocusHash = "";
  setSelectedHashes((current) =>
    current.includes(hash) ? current.filter((item) => item !== hash) : [...current, hash],
  );
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
  pendingFocusHash = "";
  setSelectedHashes(visibleHashes.slice(Math.min(start, end), Math.max(start, end) + 1));
  setFocusedHash(hash);
}

export function selectAllVisible(hashes: string[]) {
  pendingFocusHash = "";
  setSelectedHashes(hashes);
  if (!focusedHash() && hashes[0]) setFocusedHash(hashes[0]);
  if (hashes[0]) setSelectionAnchor(hashes[0]);
}

/** Moves the keyboard focus through the currently filtered table rows. */
export function moveSelection(direction: -1 | 1, hashes: string[], extend: boolean) {
  moveSelectionBy(direction, hashes, extend);
}

/** Moves the keyboard focus by an arbitrary offset (PERF-7.3: PageUp/PageDown
 * move by roughly one viewport of 30px rows; the table scrolls the focused
 * row into view). */
export function moveSelectionBy(offset: number, hashes: string[], extend: boolean) {
  if (!hashes.length || !offset) return;
  const current = focusedHash();
  const selected = selectedHashes();
  let index = current ? hashes.indexOf(current) : -1;
  if (index < 0 && selected.length) index = hashes.indexOf(selected[0]);
  const target =
    index < 0
      ? offset > 0
        ? 0
        : hashes.length - 1
      : Math.max(0, Math.min(hashes.length - 1, index + Math.trunc(offset)));
  if (extend) selectRange(hashes[target], hashes);
  else selectOnly(hashes[target]);
}

/** Jumps the keyboard focus to the first or last visible row (Home/End). */
export function jumpSelection(edge: "first" | "last", hashes: string[], extend: boolean) {
  if (!hashes.length) return;
  const hash = edge === "first" ? hashes[0] : hashes[hashes.length - 1];
  if (extend) selectRange(hash, hashes);
  else selectOnly(hash);
}

/** Removes selections whose keyed row has disappeared from the live session.
 * A no-op (no signal write, same array identity) when nothing changed, so
 * the per-tick call from the table never invalidates row state (PERF-7.1). */
export function pruneSelection(available: Set<string>) {
  const current = untrack(selectedHashes);
  const focused = untrack(focusedHash);
  if (current.every((hash) => available.has(hash)) && (!focused || available.has(focused))) return;
  setSelectedHashes(current.filter((hash) => available.has(hash)));
  if (focused && !available.has(focused)) setFocusedHash("");
}

/** Single-slot era is over (POL-8.3): feedback goes through the queued
 * notification system. Plain-string callers keep working unchanged; the full
 * notify surface (kind, action, duration, silent record, browser raise) is
 * available through the same call. */
export function showToast(
  message: string,
  options?: {
    kind?: NoticeKind;
    durationMs?: number;
    sticky?: boolean;
    action?: NoticeAction;
    silent?: boolean;
    browser?: { title: string; body?: string };
  },
): number {
  return notify(message, options);
}

/** Configured label definitions (name + #rrggbb) hydrated at boot from
 * /api/settings; table chips and sidebar dots render them with a
 * contrast-safe text color (THM-9.2). */
export type ConfiguredLabel = { name: string; color: string };
const [configuredLabels, setConfiguredLabels] = createSignal<ConfiguredLabel[]>([]);
export { configuredLabels };

export function hydrateLabels(labels: unknown) {
  if (!Array.isArray(labels)) return;
  const next: ConfiguredLabel[] = [];
  for (const entry of labels) {
    if (!entry || typeof entry !== "object") continue;
    const { name, color } = entry as { name?: unknown; color?: unknown };
    if (typeof name !== "string" || !name.trim()) continue;
    if (typeof color !== "string" || !HEX_COLOR.test(color)) continue;
    next.push({ name: name.trim(), color: color.toLowerCase() });
  }
  setConfiguredLabels(next);
}

/** Configured color for a label name, or undefined for built-ins/unset. */
export function configuredLabelColor(name: string): string | undefined {
  return configuredLabels().find((l) => l.name === name)?.color;
}

/** Inline chip/dot style for a user-configured label (THM-9.2); undefined
 * keeps the class-driven built-in rendering. Tracks the theme version so a
 * theme switch re-composites against the new surface. */
export function customLabelStyle(name: string): { background: string; color: string } | undefined {
  themeVersion();
  const hex = configuredLabelColor(name);
  if (!hex) return undefined;
  const surface = readToken("--bg-app") || "#101214";
  return labelChipStyle(name, configuredLabelColor, surface);
}

/** Context-menu open state (POL-8.5): global shortcuts stand down while the
 * menu owns the keyboard. Mirrored from ConsoleView's local menu target. */
const [menuOpen, setMenuOpen] = createSignal(false);
export { menuOpen, setMenuOpen };

/** Shortcut help overlay (POL-8.5): open state for the `?` binding. */
const [helpOpen, setHelpOpen] = createSignal(false);
export { helpOpen, setHelpOpen };

/** Opens intake with any .torrent files dropped on the console table. */
export function openAdd(files: File[] = []) {
  const accepted = files.filter((file) => file.name.toLowerCase().endsWith(".torrent"));
  const rejected = files
    .filter((file) => !file.name.toLowerCase().endsWith(".torrent"))
    .map((file) => `${file.name}: not a .torrent file`);
  if (accepted.length) setQueuedTorrentFiles((current) => [...current, ...accepted]);
  if (rejected.length) setQueuedTorrentFileErrors((current) => [...current, ...rejected]);
  setAddOpen(true);
}

export function closeAdd() {
  setAddOpen(false);
  setQueuedTorrentFiles([]);
  setQueuedTorrentFileErrors([]);
}

/** Create-.torrent dialog (PAR-5.4). */
const [createOpen, setCreateOpen] = createSignal(false);

export function openCreate() {
  setCreateOpen(true);
}

export function closeCreate() {
  setCreateOpen(false);
}

export {
  addOpen,
  columnLayout,
  createOpen,
  daemonInfo,
  debouncedQuery,
  detailPrefs,
  filters,
  focusedHash,
  peerColumnLayout,
  peerSort,
  query,
  queuedTorrentFiles,
  queuedTorrentFileErrors,
  route,
  savedFilters,
  selectedHashes,
  selectionAnchor,
  settingsDirty,
  sort,
  setAddOpen,
  setFocusedHash,
  setQuery,
  setRoute,
  setSettingsDirty,
  setQueuedTorrentFileErrors,
  setQueuedTorrentFiles,
};

// ---- Hash routing (POL-8.6) ----

const LAST_ROUTE_KEY = "blackbird.route.v1";

function hasWindow() {
  return typeof window !== "undefined" && typeof window.location !== "undefined";
}

/** Applies a parsed route to the stores (boot + back/forward). Selections
 * only change when the URL names a different focus hash. Focus also arms a
 * pending restore: if the session is empty (boot, reconnect), the next
 * snapshot selects it when present instead of pruning it away unseen. */
export function applyRouteState(state: {
  route: Route;
  section: string;
  filter: string;
  focus: string;
}) {
  if (state.route !== route()) setRoute(state.route);
  if (state.route === "settings" && state.section) {
    const valid = validSection(state.section);
    if (valid !== settingsSection()) setSettingsSection(valid);
  }
  if (state.filter !== query()) setQuery(state.filter);
  if (state.focus && state.focus !== focusedHash()) {
    selectOnly(state.focus);
    setPendingFocus(state.focus);
  }
}

/** Navigates to a route (and settings section), persisting the last route.
 * The URL sync effect below turns this into a history push. */
export function navigate(to: Route, section?: string) {
  if (to === "settings") setSettingsSection(validSection(section ?? settingsSection()));
  if (to !== route()) setRoute(to);
  if (hasWindow()) {
    try {
      window.localStorage.setItem(LAST_ROUTE_KEY, to);
    } catch {
      /* private mode: last route stays in memory for the session */
    }
  }
}

/** Reads the persisted last route, if any. Exported for tests. */
export function loadLastRoute(): Route | null {
  if (!hasWindow()) return null;
  try {
    const value = window.localStorage.getItem(LAST_ROUTE_KEY);
    return value === "console" ||
      value === "settings" ||
      value === "stats" ||
      value === "rss" ||
      value === "history" ||
      value === "attention"
      ? value
      : null;
  } catch {
    return null;
  }
}

// One effect owns URL writes: route/section switches push a history entry
// (back/forward work); filter/focus motion replaces in place so typing never
// spams history. Skips writes already reflected to avoid echo loops. Nothing
// writes before boot applies the incoming URL (routerReady gate).
let lastSyncedKey: string | null = null;
let routerReady = false;
function syncKeyOf(snapshot: { route: Route; section: string; filter: string; focus: string }) {
  return `${snapshot.route}\n${snapshot.section}\n${snapshot.filter}\n${snapshot.focus}`;
}
function snapshotRoute() {
  return {
    route: route(),
    section: route() === "settings" ? settingsSection() : "",
    filter: route() === "console" ? query() : "",
    focus: route() === "console" ? focusedHash() : "",
  };
}
createEffect(() => {
  if (!hasWindow()) return;
  const snapshot = snapshotRoute();
  if (!routerReady) return;
  const hash = buildHash(snapshot);
  if (window.location.hash === hash) {
    lastSyncedKey = syncKeyOf(snapshot);
    return;
  }
  const moved =
    lastSyncedKey === null ||
    lastSyncedKey.split("\n").slice(0, 2).join("\n") !== `${snapshot.route}\n${snapshot.section}`;
  if (moved) {
    window.location.hash = hash;
  } else {
    window.history.replaceState(null, "", hash);
  }
  lastSyncedKey = syncKeyOf(snapshot);
});

if (hasWindow()) {
  window.addEventListener("hashchange", () => {
    applyRouteState(parseHash(window.location.hash));
  });
  // Boot: the URL wins; otherwise the persisted last route; otherwise console.
  // The first URL write below always replaces so boot never pushes history.
  const booted = parseHash(window.location.hash);
  if (window.location.hash) {
    applyRouteState(booted);
  } else {
    const last = loadLastRoute();
    if (last && last !== "console") setRoute(last);
  }
  if (booted.focus) {
    selectOnly(booted.focus);
    setPendingFocus(booted.focus);
  }
  routerReady = true;
  const snapshot = snapshotRoute();
  const hash = buildHash(snapshot);
  if (window.location.hash !== hash) {
    window.history.replaceState(null, "", hash);
  }
  lastSyncedKey = syncKeyOf(snapshot);
}

// ---- Sidebar width (POL-8.6) ----

const SIDEBAR_WIDTH_KEY = "blackbird.sidebar.v1";
export const DEFAULT_SIDEBAR_WIDTH = 196;
const MIN_SIDEBAR_WIDTH = 140;
const MAX_SIDEBAR_WIDTH = 340;

function loadSidebarWidth(): number {
  if (!hasWindow()) return DEFAULT_SIDEBAR_WIDTH;
  try {
    const raw = window.localStorage.getItem(SIDEBAR_WIDTH_KEY);
    if (raw === null || raw.trim() === "") return DEFAULT_SIDEBAR_WIDTH;
    const parsed = Number(raw);
    if (Number.isFinite(parsed)) {
      return Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, Math.round(parsed)));
    }
  } catch {
    /* malformed preference falls through to the default */
  }
  return DEFAULT_SIDEBAR_WIDTH;
}

const [sidebarWidth, setSidebarWidthSignal] = createSignal<number>(loadSidebarWidth());
export { sidebarWidth };

export function setSidebarWidth(width: number) {
  const next = Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, Math.round(width)));
  setSidebarWidthSignal(next);
  if (hasWindow()) {
    try {
      window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(next));
    } catch {
      /* private mode */
    }
  }
}

export function resetSidebarWidth() {
  if (hasWindow()) {
    try {
      window.localStorage.removeItem(SIDEBAR_WIDTH_KEY);
    } catch {
      /* private mode */
    }
  }
  setSidebarWidthSignal(DEFAULT_SIDEBAR_WIDTH);
}

// ---- Session continuity (POL-8.6) ----

/** Restores a remembered selection/focus against the live session,
 * returning what survived. Used after reconnects (hashes may be gone). */
export function restoreSelection(
  selected: string[],
  focus: string,
  available: Set<string>,
): { selected: string[]; focus: string } {
  const kept = selected.filter((hash) => available.has(hash));
  const keptFocus = focus && available.has(focus) ? focus : (kept[0] ?? "");
  batch(() => {
    setSelectedHashes(kept);
    setFocusedHash(keptFocus);
    setSelectionAnchor(keptFocus);
  });
  return { selected: kept, focus: keptFocus };
}

/** Clears every persisted layout key and restores defaults (POL-8.6 "Reset
 * layout"). Saved filters, notification prefs, and dialog skips are user
 * data, not layout, and are left alone. */
export function resetLayout() {
  resetColumns();
  setSort([{ column: "addedAt", direction: "desc" }]);
  try {
    if (hasWindow()) {
      window.localStorage.removeItem(SORT_STORAGE_KEY);
      window.localStorage.removeItem(LEGACY_SORT_STORAGE_KEY);
    }
  } catch {
    /* private mode */
  }
  resetPeerColumns();
  try {
    if (hasWindow()) window.localStorage.removeItem(DETAIL_PANEL_STORAGE_KEY);
  } catch {
    /* private mode */
  }
  setDetailPrefs({ ...DEFAULT_DETAIL_PREFS });
  resetSidebarWidth();
  try {
    if (hasWindow()) window.localStorage.removeItem(LAST_ROUTE_KEY);
  } catch {
    /* private mode */
  }
  if (route() !== "console") setRoute("console");
}
