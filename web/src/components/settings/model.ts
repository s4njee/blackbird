// Settings model helpers (POL-8.8): defaults, normalization, and draft
// accessors shared by the shell and sections. No JSX here.
import { COLUMN_DEFINITIONS, type ColumnConfig } from "../../lib/columns";
import { nsToDuration } from "../../lib/duration";
import type {
  Draft,
  FeedDraft,
  FilterDraft,
  HistoryPrefs,
  Loaded,
  RuleDraft,
  ScheduleProfileDraft,
  SeedingGroupDraft,
  ThrottleDraft,
  UnpackRuleDraft,
  WatchDirDraft,
} from "./types";

export const COLUMNS = COLUMN_DEFINITIONS.map((column) => column.key);
export const NUMBER_TUNING = new Set([
  "dht_port",
  "http_max_open",
  "max_open_sockets",
  "max_open_files",
  "min_peers_normal",
  "max_peers_normal",
  "min_peers_seeded",
  "max_peers_seeded",
  "max_uploads",
  "global_down_rate_kb",
  "global_up_rate_kb",
  "max_downloads_global",
  "max_uploads_global",
]);

export const EMPTY: Loaded = {
  tuning: {},
  history: {
    action_log_entries: 200,
    action_log_retention: "24h",
    message_entries: 200,
    global_entries: null,
  },
  stats: { traffic_days: null },
  portcheck: { url: "", timeout: "10s" },
  network: { ipfilter: { path: "", url: "", refresh_interval: "" } },
  directories: { default: "", per_label: {}, watch: [] },
  automation: { on_complete: [], rss: { feeds: [], filters: [] }, unpack: { rules: [] } },
  seeding: { custom_slot: "custom2", groups: [] },
  schedule: { timezone: "", profiles: [], grid: {} },
  labels: [],
  ui: {
    accent: "",
    theme: "",
    columns: [],
    visible_columns: [],
    saved_filters: [],
    sort: { column: "added", dir: "desc", keys: [] },
    date_format: "local",
    rate_format: "binary",
    poll_interval: "2s",
  },
  daemon: {},
};
export const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
export const isInterface = (value: string) => value === "Interface";

/** Normalizes one API watch entry (poll_interval as ns) into the draft shape. */
export function normalizeWatchEntry(entry: any): WatchDirDraft {
  return {
    path: textValue(entry?.path),
    label: textValue(entry?.label),
    destination: textValue(entry?.destination),
    start: entry?.start === undefined ? true : Boolean(entry.start),
    delete_after_load: Boolean(entry?.delete_after_load),
    poll_interval: nsToDuration(entry?.poll_interval),
  };
}

/** Normalizes one API completion rule (private *bool → tri-state select). */
export function normalizeRule(entry: any): RuleDraft {
  return {
    name: textValue(entry?.name),
    label: textValue(entry?.label),
    tracker: textValue(entry?.tracker),
    name_regex: textValue(entry?.name_regex),
    min_size: Number(entry?.min_size) || 0,
    max_size: Number(entry?.max_size) || 0,
    private: entry?.private === true ? "true" : entry?.private === false ? "false" : "any",
    set_label: textValue(entry?.set_label),
    move_to: textValue(entry?.move_to),
    add_tracker: textValue(entry?.add_tracker),
    webhook: textValue(entry?.webhook),
  };
}

export function normalizeFeed(entry: any): FeedDraft {
  const headers =
    entry?.headers && typeof entry.headers === "object"
      ? Object.entries(entry.headers as Record<string, unknown>)
          .map(([key, value]) => `${key}: ${String(value ?? "")}`)
          .join("\n")
      : "";
  return {
    name: textValue(entry?.name),
    url: textValue(entry?.url),
    poll_interval: nsToDuration(entry?.poll_interval),
    label: textValue(entry?.label),
    destination: textValue(entry?.destination),
    cookies: textValue(entry?.cookies),
    headers,
  };
}

/** Normalizes one API filter. */
export function normalizeFilter(entry: any): FilterDraft {
  return {
    name: textValue(entry?.name),
    feed: textValue(entry?.feed),
    title_regex: textValue(entry?.title_regex),
    category: textValue(entry?.category),
    min_size: Number(entry?.min_size) || 0,
    max_size: Number(entry?.max_size) || 0,
    label: textValue(entry?.label),
    destination: textValue(entry?.destination),
    start: entry?.start === undefined ? true : Boolean(entry.start),
  };
}

/** Normalizes one API unpack rule. */
export function normalizeUnpackRule(entry: any): UnpackRuleDraft {
  return {
    name: textValue(entry?.name),
    label: textValue(entry?.label),
    destination: textValue(entry?.destination),
    delete_archives: Boolean(entry?.delete_archives),
  };
}

/** Normalizes one API seeding group (max_seeding_time as ns → string). */
export function normalizeSeedingGroup(entry: any): SeedingGroupDraft {
  const action = textValue(entry?.action);
  return {
    name: textValue(entry?.name),
    min_ratio: Number(entry?.min_ratio) || 0,
    max_ratio: Number(entry?.max_ratio) || 0,
    min_upload_bytes: Number(entry?.min_upload_bytes) || 0,
    max_seeding_time: nsToDuration(entry?.max_seeding_time),
    action:
      action === "stop_and_set_label" || action === "erase" || action === "erase_with_data"
        ? action
        : "stop",
    label: textValue(entry?.label),
  };
}

/** Normalizes one API bandwidth profile. */
export function normalizeScheduleProfile(entry: any): ScheduleProfileDraft {
  return {
    name: textValue(entry?.name),
    color: /^#[0-9a-f]{6}$/i.test(textValue(entry?.color)) ? textValue(entry.color) : "#64748b",
    down_kb: Number(entry?.down_kb) || 0,
    up_kb: Number(entry?.up_kb) || 0,
    throttles: Array.isArray(entry?.throttles)
      ? entry.throttles.map((ch: any) => ({
          name: textValue(ch?.name),
          up_kb: Number(ch?.up_kb) || 0,
          down_kb: Number(ch?.down_kb) || 0,
        }))
      : [],
  };
}

/** Weekday keys in grid order. */
export const SCHEDULE_DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];

/** Normalizes the grid to a full 7×24 of profile names (missing → skip). */
export function normalizeGrid(grid: unknown): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  const raw = (grid && typeof grid === "object" ? grid : {}) as Record<string, unknown>;
  for (const day of SCHEDULE_DAYS) {
    const cells = Array.isArray(raw[day]) ? (raw[day] as unknown[]) : [];
    out[day] = Array.from({ length: 24 }, (_, h) =>
      typeof cells[h] === "string" ? (cells[h] as string) : "",
    );
  }
  return out;
}

/** Parses the headers textarea ("Name: value" per line) into a map. */
export function parseHeaderLines(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const at = line.indexOf(":");
    if (at < 0) continue;
    const name = line.slice(0, at).trim();
    const value = line.slice(at + 1).trim();
    if (name) out[name] = value;
  }
  return out;
}

export function normalize(input: Partial<Loaded>): Loaded {
  const base = clone(EMPTY);
  const legacyColumns = input.ui?.visible_columns ?? [];
  const columns = input.ui?.columns?.length
    ? input.ui.columns
    : legacyColumns.map((key: string) => ({
        key: key as ColumnConfig["key"],
        visible: true,
        width: 0,
      }));
  const rawHistory: Partial<HistoryPrefs> = input.history ?? {};
  // The API reports action_log_retention as integer nanoseconds (Go's JSON
  // default for time.Duration); the editor edits a human duration string.
  const retention =
    typeof rawHistory.action_log_retention === "number"
      ? nsToDuration(rawHistory.action_log_retention)
      : textValue(rawHistory.action_log_retention) || "24h";
  // history.global_entries is null when the operator uses the server default
  // (5000 events); the editor shows the default as a placeholder and saves
  // null for "default".
  const globalEntries =
    typeof rawHistory.global_entries === "number" && Number.isInteger(rawHistory.global_entries)
      ? rawHistory.global_entries
      : null;
  // stats.traffic_days is null when the operator uses the server default
  // (90 days); the editor shows the default as a placeholder, keeps 0 as a
  // real value (persistence disabled), and saves null for "default".
  const rawTraffic = (input as { stats?: { traffic_days?: unknown } }).stats?.traffic_days;
  const trafficDays =
    typeof rawTraffic === "number" && Number.isInteger(rawTraffic) ? rawTraffic : null;
  // portcheck.timeout edits as a human duration; empty means the server
  // default (10s). The probe URL edits as plain text; empty disables.
  const rawPortCheck =
    (input as { portcheck?: { url?: unknown; timeout?: unknown } }).portcheck ?? {};
  const portcheckTimeout = nsToDuration(rawPortCheck.timeout) || "10s";
  // network.ipfilter edits as plain text plus a human refresh duration;
  // empty refresh means the server default (24h for URL sources).
  const rawIPFilter =
    (
      input as {
        network?: { ipfilter?: { path?: unknown; url?: unknown; refresh_interval?: unknown } };
      }
    ).network?.ipfilter ?? {};
  return {
    ...base,
    ...input,
    tuning: normalizeTuning(input.tuning),
    daemon: input.daemon ?? {},
    labels: input.labels ?? [],
    history: {
      ...base.history,
      ...rawHistory,
      action_log_retention: retention,
      global_entries: globalEntries,
    },
    stats: { traffic_days: trafficDays },
    portcheck: { url: textValue(rawPortCheck.url), timeout: portcheckTimeout },
    network: {
      ipfilter: {
        path: textValue(rawIPFilter.path),
        url: textValue(rawIPFilter.url),
        refresh_interval: nsToDuration(rawIPFilter.refresh_interval),
      },
    },
    directories: {
      ...base.directories,
      ...(input.directories ?? {}),
      per_label: input.directories?.per_label ?? {},
      watch: Array.isArray(input.directories?.watch)
        ? input.directories!.watch.map(normalizeWatchEntry)
        : [],
    },
    automation: {
      on_complete: Array.isArray(input.automation?.on_complete)
        ? input.automation!.on_complete.map(normalizeRule)
        : [],
      rss: {
        feeds: Array.isArray(input.automation?.rss?.feeds)
          ? input.automation!.rss!.feeds.map(normalizeFeed)
          : [],
        filters: Array.isArray(input.automation?.rss?.filters)
          ? input.automation!.rss!.filters.map(normalizeFilter)
          : [],
      },
      unpack: {
        rules: Array.isArray(input.automation?.unpack?.rules)
          ? input.automation!.unpack!.rules.map(normalizeUnpackRule)
          : [],
      },
    },
    seeding: {
      custom_slot: textValue(input.seeding?.custom_slot) || "custom2",
      groups: Array.isArray(input.seeding?.groups)
        ? input.seeding!.groups.map(normalizeSeedingGroup)
        : [],
    },
    schedule: {
      timezone: textValue(input.schedule?.timezone),
      profiles: Array.isArray((input.schedule as any)?.bandwidth?.profiles)
        ? (input.schedule as any).bandwidth.profiles.map(normalizeScheduleProfile)
        : [],
      grid: normalizeGrid((input.schedule as any)?.bandwidth?.grid),
    },
    ui: {
      ...base.ui,
      ...(input.ui ?? {}),
      columns,
      visible_columns: [],
      saved_filters: input.ui?.saved_filters ?? [],
      sort: { ...base.ui.sort, ...(input.ui?.sort ?? {}), keys: input.ui?.sort?.keys ?? [] },
    },
  };
}

export function throttleList(draft: Draft): ThrottleDraft[] {
  const raw = (draft.tuning as Record<string, unknown>)?.throttles;
  if (!Array.isArray(raw)) return [];
  return raw.map((entry: any) => ({
    name: textValue(entry?.name),
    up_kb: Number(entry?.up_kb) || 0,
    down_kb: Number(entry?.down_kb) || 0,
  }));
}

export function setThrottleList(
  setDraft: (fn: (value: Draft) => Draft) => void,
  next: ThrottleDraft[],
) {
  setDraft((current) => ({ ...current, tuning: { ...current.tuning, throttles: next } }));
}

/** Normalizes the tuning section, keeping the throttles list in editor
 * shape. An absent list stays absent so saves leave daemon channels alone. */
export function normalizeTuning(input: unknown): Record<string, unknown> {
  if (!input || typeof input !== "object") return {};
  const tuning = { ...(input as Record<string, unknown>) };
  if (Array.isArray((input as Record<string, unknown>).throttles)) {
    tuning.throttles = ((input as Record<string, unknown>).throttles as any[]).map((entry) => ({
      name: textValue(entry?.name),
      up_kb: Number(entry?.up_kb) || 0,
      down_kb: Number(entry?.down_kb) || 0,
    }));
  }
  return tuning;
}

export function valueFor(
  draft: Draft,
  daemon: Record<string, string>,
  field: string,
  daemonKey: string,
) {
  const declared = draft.tuning[field];
  return declared === null || declared === undefined ? (daemon[daemonKey] ?? "") : declared;
}

export function textValue(value: unknown) {
  return value === null || value === undefined ? "" : String(value);
}

export function daemonKey(field: string) {
  return (
    (
      {
        port_range: "network.port_range",
        port_random: "network.port_random",
        encryption: "protocol.encryption",
        dht_mode: "dht.mode",
        dht_port: "dht.port",
        use_udp: "trackers.use_udp",
        pex: "protocol.pex",
        local_address: "network.local_address",
        bind_address: "network.bind_address",
        http_max_open: "network.http.max_open",
        max_open_sockets: "network.max_open_sockets",
        max_open_files: "network.max_open_files",
        min_peers_normal: "throttle.min_peers.normal",
        max_peers_normal: "throttle.max_peers.normal",
        min_peers_seeded: "throttle.min_peers.seeded",
        max_peers_seeded: "throttle.max_peers.seeded",
        max_uploads: "throttle.max_uploads",
        global_down_rate_kb: "throttle.global_down.max_rate",
        global_up_rate_kb: "throttle.global_up.max_rate",
        max_downloads_global: "throttle.max_downloads.global",
        max_uploads_global: "throttle.max_uploads.global",
      } as Record<string, string>
    )[field] ?? field
  );
}

/** Reachability probe editor + user-initiated check (PAR-5.5). The check
 * runs against the saved probe configuration, never automatically. */
