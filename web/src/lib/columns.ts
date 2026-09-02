/** The table's shared column catalogue. Keep this list in one place so the
 * table, settings editor, and persisted-layout migration cannot drift apart. */
export const COLUMN_DEFINITIONS = [
  { key: "name", label: "Name", class: "col-name", minWidth: 220, width: 260, fluid: true },
  { key: "sizeBytes", label: "Size", class: "col-size", minWidth: 74, width: 74 },
  { key: "percent", label: "Done", class: "col-done", minWidth: 112, width: 112 },
  { key: "leftBytes", label: "Remaining", class: "col-remaining", minWidth: 96, width: 96 },
  { key: "state", label: "Status", class: "col-status", minWidth: 96, width: 96 },
  { key: "seedsPeers", label: "Seeds/Peers", class: "col-peers", minWidth: 66, width: 66 },
  { key: "seeds", label: "Seeds", class: "col-peers-detail", minWidth: 58, width: 58 },
  { key: "peers", label: "Peers", class: "col-peers-detail", minWidth: 58, width: 58 },
  { key: "downRate", label: "Down", class: "col-rate", minWidth: 78, width: 78 },
  { key: "upRate", label: "Up", class: "col-rate", minWidth: 78, width: 78 },
  { key: "downloadedBytes", label: "Downloaded", class: "col-total", minWidth: 104, width: 104 },
  { key: "uploadedBytes", label: "Uploaded", class: "col-total", minWidth: 104, width: 104 },
  { key: "etaSeconds", label: "ETA", class: "col-eta", minWidth: 64, width: 64 },
  { key: "ratio", label: "Ratio", class: "col-ratio", minWidth: 54, width: 54 },
  { key: "label", label: "Label", class: "col-label", minWidth: 88, width: 88 },
  { key: "priority", label: "Priority", class: "col-priority", minWidth: 74, width: 74 },
  { key: "throttle", label: "Throttle", class: "col-throttle", minWidth: 90, width: 90 },
  { key: "ratioGroup", label: "Ratio group", class: "col-ratio-group", minWidth: 94, width: 94 },
  { key: "addedAt", label: "Added", class: "col-added", minWidth: 86, width: 86 },
  { key: "finishedAt", label: "Finished", class: "col-finished", minWidth: 86, width: 86 },
  { key: "creationDate", label: "Created", class: "col-created", minWidth: 86, width: 86 },
  { key: "seedingTime", label: "Seeding time", class: "col-seeding-time", minWidth: 96, width: 96 },
  { key: "trackerHost", label: "Tracker", class: "col-tracker", minWidth: 120, width: 120 },
  { key: "trackerStatus", label: "Tracker status", class: "col-tracker-status", minWidth: 104, width: 104 },
  { key: "directory", label: "Path", class: "col-path", minWidth: 180, width: 180 },
  { key: "hash", label: "Hash", class: "col-hash", minWidth: 140, width: 140 },
  { key: "message", label: "Message", class: "col-message", minWidth: 180, width: 180 },
] as const;

export type ColumnKey = (typeof COLUMN_DEFINITIONS)[number]["key"];
export const DEFAULT_COLUMN_KEYS = COLUMN_DEFINITIONS.map((column) => column.key) as ColumnKey[];

export type ColumnLayout = {
  order: ColumnKey[];
  hidden: ColumnKey[];
  widths: Partial<Record<ColumnKey, number>>;
};

export type ColumnConfig = { key: ColumnKey; visible: boolean; width: number };

export const DEFAULT_COLUMN_LAYOUT: ColumnLayout = {
  order: [...DEFAULT_COLUMN_KEYS],
  hidden: [],
  widths: Object.fromEntries(COLUMN_DEFINITIONS.map((column) => [column.key, column.width])) as Partial<Record<ColumnKey, number>>,
};

export function columnDefinition(key: ColumnKey) {
  return COLUMN_DEFINITIONS.find((column) => column.key === key)!;
}

export function layoutToConfig(layout: ColumnLayout): ColumnConfig[] {
  return layout.order.map((key) => ({
    key,
    visible: key === "name" || !layout.hidden.includes(key),
    width: Math.max(columnDefinition(key).minWidth, Math.round(layout.widths[key] ?? columnDefinition(key).width)),
  }));
}

export function configToLayout(config: unknown): ColumnLayout | null {
  if (!Array.isArray(config) || !config.length) return null;
  const known = new Set<ColumnKey>(DEFAULT_COLUMN_KEYS);
  const order: ColumnKey[] = [];
  const hidden: ColumnKey[] = [];
  const widths: Partial<Record<ColumnKey, number>> = { ...DEFAULT_COLUMN_LAYOUT.widths };
  for (const raw of config) {
    if (!raw || typeof raw !== "object" || !("key" in raw)) continue;
    const key = String((raw as { key: unknown }).key) as ColumnKey;
    if (!known.has(key) || order.includes(key)) continue;
    order.push(key);
    const item = raw as { visible?: unknown; width?: unknown };
    if (item.visible === false && key !== "name") hidden.push(key);
    if (typeof item.width === "number" && Number.isFinite(item.width)) widths[key] = Math.max(columnDefinition(key).minWidth, Math.round(item.width));
  }
  for (const key of DEFAULT_COLUMN_KEYS) if (!order.includes(key)) order.push(key);
  return { order, hidden, widths };
}
