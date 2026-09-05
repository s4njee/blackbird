// Shared Settings draft types (POL-8.8): one home for the shapes the
// shell, sections, and field helpers all thread through.
import type { ColumnConfig } from "../../lib/columns";

export type Label = { name: string; color: string };
export type SavedFilter = {
  name: string;
  query: string;
  status: string;
  label: string;
  tracker: string;
};
export type HistoryPrefs = {
  recorder_bytes?: number;
  action_log_entries: number;
  action_log_retention: string;
  message_entries: number;
  global_entries: number | null;
};
/** One watch-directory entry (PAR-3.1). poll_interval is edited as a human duration and converted to ns on save. */
export type WatchDirDraft = {
  path: string;
  label: string;
  destination: string;
  start: boolean;
  delete_after_load: boolean;
  poll_interval: string;
};
/** One completion rule (PAR-3.2). private is edited as a tri-state select. */
export type RuleDraft = {
  name: string;
  label: string;
  tracker: string;
  name_regex: string;
  min_size: number;
  max_size: number;
  private: "any" | "true" | "false";
  set_label: string;
  move_to: string;
  add_tracker: string;
  webhook: string;
};
/** One unpack rule (PAR-3.4). Empty destination extracts in place. */
export type UnpackRuleDraft = {
  name: string;
  label: string;
  destination: string;
  delete_archives: boolean;
};
/** One seeding group (PAR-4.2). max_seeding_time is a human duration string. */
export type SeedingGroupDraft = {
  name: string;
  min_ratio: number;
  max_ratio: number;
  min_upload_bytes: number;
  max_seeding_time: string;
  action: "stop" | "stop_and_set_label" | "erase" | "erase_with_data";
  label: string;
};
/** One bandwidth profile (PAR-4.3); ThrottleDraft is reused for channel caps. */
export type ScheduleProfileDraft = {
  name: string;
  color: string;
  down_kb: number;
  up_kb: number;
  throttles: ThrottleDraft[];
};
export type Draft = {
  tuning: Record<string, unknown>;
  history: HistoryPrefs;
  stats: { traffic_days: number | null };
  portcheck: { url: string; timeout: string };
  network: { ipfilter: { path: string; url: string; refresh_interval: string } };
  directories: {
    default: string;
    per_label: Record<string, string>;
    watch: WatchDirDraft[];
    session?: string;
    open_url_template?: string;
  };
  automation: {
    on_complete: RuleDraft[];
    rss: { feeds: FeedDraft[]; filters: FilterDraft[] };
    unpack: { rules: UnpackRuleDraft[] };
  };
  seeding: { custom_slot: string; groups: SeedingGroupDraft[] };
  schedule: { timezone: string; profiles: ScheduleProfileDraft[]; grid: Record<string, string[]> };
  labels: Label[];
  ui: {
    accent: string;
    theme: string;
    columns: ColumnConfig[];
    visible_columns: any;
    saved_filters: SavedFilter[];
    sort: {
      column: string;
      dir: "asc" | "desc";
      keys: Array<{ column: string; dir: "asc" | "desc" }>;
    };
    date_format: string;
    rate_format: string;
    poll_interval: string;
  };
};
export type Loaded = Draft & { daemon: Record<string, string> };
export type Nav =
  | "General"
  | "Connection"
  | "Bandwidth"
  | "Seeding"
  | "Scheduler"
  | "Queue"
  | "Directories"
  | "Labels"
  | "Automation"
  | "Interface"
  | "History"
  | "Advanced"
  | "About";

/** One RSS feed (PAR-3.3). Secrets display masked; "***" keeps the stored value. */
export type FeedDraft = {
  name: string;
  url: string;
  poll_interval: string;
  label: string;
  destination: string;
  cookies: string;
  headers: string;
};
/** One RSS filter (PAR-3.3). */
export type FilterDraft = {
  name: string;
  feed: string;
  title_regex: string;
  category: string;
  min_size: number;
  max_size: number;
  label: string;
  destination: string;
  start: boolean;
};

/** Named throttle channels in the tuning draft (PAR-4.1). */
export type ThrottleDraft = { name: string; up_kb: number; down_kb: number };

export type SectionProps = {
  active: string;
  draft: Draft;
  daemon: Record<string, string>;
  errors: Record<string, string>;
  updateTuning: (key: string, value: unknown) => void;
  updateDirectory: (key: string, value: string) => void;
  updateWatch: (index: number, patch: Partial<WatchDirDraft>) => void;
  addWatch: () => void;
  removeWatch: (index: number) => void;
  updateRule: (index: number, patch: Partial<RuleDraft>) => void;
  addRule: () => void;
  removeRule: (index: number) => void;
  updateUnpackRule: (index: number, patch: Partial<UnpackRuleDraft>) => void;
  addUnpackRule: () => void;
  removeUnpackRule: (index: number) => void;
  updateFeed: (index: number, patch: Partial<FeedDraft>) => void;
  addFeed: () => void;
  removeFeed: (index: number) => void;
  updateFilter: (index: number, patch: Partial<FilterDraft>) => void;
  addFilter: () => void;
  removeFilter: (index: number) => void;
  updateSeedingGroup: (index: number, patch: Partial<SeedingGroupDraft>) => void;
  addSeedingGroup: () => void;
  removeSeedingGroup: (index: number) => void;
  updateScheduleProfile: (index: number, patch: Partial<ScheduleProfileDraft>) => void;
  addScheduleProfile: () => void;
  removeScheduleProfile: (index: number) => void;
  paintScheduleCell: (day: string, hour: number, profile: string) => void;
  updateHistory: (patch: Partial<HistoryPrefs>) => void;
  updateStats: (patch: Partial<Draft["stats"]>) => void;
  updatePortCheck: (patch: Partial<Draft["portcheck"]>) => void;
  updateIPFilter: (patch: Partial<Draft["network"]["ipfilter"]>) => void;
  setDraft: (fn: (value: Draft) => Draft) => void;
  deleteLabel: (name: string) => void;
  rawMethod: string;
  setRawMethod: (value: string) => void;
  rawParams: string;
  setRawParams: (value: string) => void;
  executeRaw: () => void;
  /** Shell save (THM-9.3 "Set as server default" fills the draft first). */
  saveSettings: () => void;
};
