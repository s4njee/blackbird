import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { durationToNs } from "../lib/duration";
import { torrentList } from "../store/session";
import { confirmDialog, promptDialog } from "../store/dialog";
import { refreshThrottles } from "../store/throttles";
import { refreshSeeding } from "../store/seeding";
import {
  applyAccent,
  applyResolvedTheme,
  clearAccentOverride,
  commitAppearancePreviews,
  discardAppearancePreviews,
  hydrateAppearance,
  previewsActive,
  SETTINGS_SECTIONS,
  setSettingsDirty,
  setSettingsSection,
  settingsSection,
  showToast,
} from "../store/ui";
import { commitCustomPreview, discardCustomPreview, previewCustom } from "../store/custom.js";

import type {
  Draft,
  FeedDraft,
  FilterDraft,
  HistoryPrefs,
  Loaded,
  Nav,
  RuleDraft,
  ScheduleProfileDraft,
  SeedingGroupDraft,
  UnpackRuleDraft,
  WatchDirDraft,
} from "./settings/types";
import {
  EMPTY,
  NUMBER_TUNING,
  SCHEDULE_DAYS,
  clone,
  daemonKey,
  normalize,
  parseHeaderLines,
  textValue,
  throttleList,
  valueFor,
} from "./settings/model";
import { SettingsSection } from "./settings/SettingsSection";
export { SettingRow } from "./settings/SettingRow";

const NAV: Nav[] = [...SETTINGS_SECTIONS] as Nav[];

export function SettingsPanel() {
  const [initial, setInitial] = createSignal<Loaded>(clone(EMPTY));
  const [draft, setDraft] = createSignal<Draft>(clone(EMPTY));
  const [loading, setLoading] = createSignal(true);
  const [loadFailed, setLoadFailed] = createSignal(false);
  const [saving, setSaving] = createSignal(false);
  const [errors, setErrors] = createSignal<Record<string, string>>({});
  const [results, setResults] = createSignal<Array<{ key: string; error?: string }>>([]);
  const [reassign, setReassign] = createSignal<Record<string, string>>({});
  const [rawMethod, setRawMethod] = createSignal("");
  const [rawParams, setRawParams] = createSignal("");
  // Draft dirtiness (YAML) plus staged appearance previews (THM-9.3):
  // previewing a theme/density enables Save/Revert without touching YAML.
  const draftDirty = createMemo(
    () =>
      JSON.stringify(draft()) !== JSON.stringify(stripDaemon(initial())) ||
      Object.keys(reassign()).length > 0,
  );
  const dirty = createMemo(() => draftDirty() || previewsActive() || previewCustom() !== undefined);
  createEffect(() => setSettingsDirty(dirty()));
  // Unsaved accent drafts preview live across the app; an empty draft
  // previews the theme default. Leaving Settings without saving (Revert
  // resets the draft, navigation unmounts) restores the committed value.
  createEffect(() => {
    const accent = draft().ui.accent;
    if (accent) applyAccent(accent);
    else clearAccentOverride();
  });
  onCleanup(() => {
    discardAppearancePreviews();
    discardCustomPreview();
    applyResolvedTheme();
    const committed = initial().ui.accent;
    if (committed) applyAccent(committed);
    else clearAccentOverride();
    hydrateAppearance(initial().ui);
  });
  onMount(() => {
    void load();
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (dirty()) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    onCleanup(() => {
      window.removeEventListener("beforeunload", beforeUnload);
      setSettingsDirty(false);
    });
  });
  function stripDaemon(value: Loaded): Draft {
    const { daemon: _daemon, ...settings } = value;
    return settings;
  }
  async function load() {
    setLoading(true);
    setLoadFailed(false);
    try {
      const response = await fetch("/api/v1/settings", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error("Could not load settings");
      const loaded = normalize((await response.json()) as Partial<Loaded>);
      setInitial(loaded);
      setDraft(clone(stripDaemon(loaded)));
      setErrors({});
      setResults([]);
      setReassign({});
    } catch (error) {
      setLoadFailed(true);
      showToast(error instanceof Error ? error.message : "Could not load settings");
    } finally {
      setLoading(false);
    }
  }
  function updateTuning(key: string, value: unknown) {
    setDraft((current) => ({ ...current, tuning: { ...current.tuning, [key]: value } }));
  }
  function updateDirectory(key: string, value: string) {
    setDraft((current) => ({ ...current, directories: { ...current.directories, [key]: value } }));
  }
  function updateWatch(index: number, patch: Partial<WatchDirDraft>) {
    setDraft((current) => ({
      ...current,
      directories: {
        ...current.directories,
        watch: current.directories.watch.map((entry, i) =>
          i === index ? { ...entry, ...patch } : entry,
        ),
      },
    }));
  }
  function addWatch() {
    setDraft((current) => ({
      ...current,
      directories: {
        ...current.directories,
        watch: [
          ...current.directories.watch,
          {
            path: "",
            label: "",
            destination: "",
            start: true,
            delete_after_load: false,
            poll_interval: "",
          },
        ],
      },
    }));
  }
  function removeWatch(index: number) {
    setDraft((current) => ({
      ...current,
      directories: {
        ...current.directories,
        watch: current.directories.watch.filter((_, i) => i !== index),
      },
    }));
  }
  function updateRule(index: number, patch: Partial<RuleDraft>) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        on_complete: current.automation.on_complete.map((rule, i) =>
          i === index ? { ...rule, ...patch } : rule,
        ),
      },
    }));
  }
  function addRule() {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        on_complete: [
          ...current.automation.on_complete,
          {
            name: "",
            label: "",
            tracker: "",
            name_regex: "",
            min_size: 0,
            max_size: 0,
            private: "any",
            set_label: "",
            move_to: "",
            add_tracker: "",
            webhook: "",
          },
        ],
      },
    }));
  }
  function removeRule(index: number) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        on_complete: current.automation.on_complete.filter((_, i) => i !== index),
      },
    }));
  }
  function updateFeed(index: number, patch: Partial<FeedDraft>) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          feeds: current.automation.rss.feeds.map((feed, i) =>
            i === index ? { ...feed, ...patch } : feed,
          ),
        },
      },
    }));
  }
  function addFeed() {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          feeds: [
            ...current.automation.rss.feeds,
            {
              name: "",
              url: "",
              poll_interval: "",
              label: "",
              destination: "",
              cookies: "",
              headers: "",
            },
          ],
        },
      },
    }));
  }
  function removeFeed(index: number) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          feeds: current.automation.rss.feeds.filter((_, i) => i !== index),
        },
      },
    }));
  }
  function updateFilter(index: number, patch: Partial<FilterDraft>) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          filters: current.automation.rss.filters.map((filter, i) =>
            i === index ? { ...filter, ...patch } : filter,
          ),
        },
      },
    }));
  }
  function addFilter() {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          filters: [
            ...current.automation.rss.filters,
            {
              name: "",
              feed: "",
              title_regex: "",
              category: "",
              min_size: 0,
              max_size: 0,
              label: "",
              destination: "",
              start: true,
            },
          ],
        },
      },
    }));
  }
  function removeFilter(index: number) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        rss: {
          ...current.automation.rss,
          filters: current.automation.rss.filters.filter((_, i) => i !== index),
        },
      },
    }));
  }
  function updateUnpackRule(index: number, patch: Partial<UnpackRuleDraft>) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        unpack: {
          rules: current.automation.unpack.rules.map((rule, i) =>
            i === index ? { ...rule, ...patch } : rule,
          ),
        },
      },
    }));
  }
  function addUnpackRule() {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        unpack: {
          rules: [
            ...current.automation.unpack.rules,
            { name: "", label: "", destination: "", delete_archives: false },
          ],
        },
      },
    }));
  }
  function removeUnpackRule(index: number) {
    setDraft((current) => ({
      ...current,
      automation: {
        ...current.automation,
        unpack: { rules: current.automation.unpack.rules.filter((_, i) => i !== index) },
      },
    }));
  }
  function updateSeedingGroup(index: number, patch: Partial<SeedingGroupDraft>) {
    setDraft((current) => ({
      ...current,
      seeding: {
        ...current.seeding,
        groups: current.seeding.groups.map((group, i) =>
          i === index ? { ...group, ...patch } : group,
        ),
      },
    }));
  }
  function addSeedingGroup() {
    setDraft((current) => ({
      ...current,
      seeding: {
        ...current.seeding,
        groups: [
          ...current.seeding.groups,
          {
            name: "",
            min_ratio: 0,
            max_ratio: 0,
            min_upload_bytes: 0,
            max_seeding_time: "",
            action: "stop",
            label: "",
          },
        ],
      },
    }));
  }
  function removeSeedingGroup(index: number) {
    setDraft((current) => ({
      ...current,
      seeding: { ...current.seeding, groups: current.seeding.groups.filter((_, i) => i !== index) },
    }));
  }
  function updateScheduleProfile(index: number, patch: Partial<ScheduleProfileDraft>) {
    setDraft((current) => ({
      ...current,
      schedule: {
        ...current.schedule,
        profiles: current.schedule.profiles.map((profile, i) =>
          i === index ? { ...profile, ...patch } : profile,
        ),
      },
    }));
  }
  function addScheduleProfile() {
    const palette = ["#f59e0b", "#2f9dff", "#3fb950", "#e5484d", "#a78bfa", "#22d3ee"];
    const used = new Set(draft().schedule.profiles.map((profile) => profile.color));
    const color = palette.find((c) => !used.has(c)) ?? "#64748b";
    setDraft((current) => ({
      ...current,
      schedule: {
        ...current.schedule,
        profiles: [
          ...current.schedule.profiles,
          { name: "", color, down_kb: 0, up_kb: 0, throttles: [] },
        ],
      },
    }));
  }
  function removeScheduleProfile(index: number) {
    const removed = draft().schedule.profiles[index]?.name;
    setDraft((current) => {
      const profiles = current.schedule.profiles.filter((_, i) => i !== index);
      const grid: Record<string, string[]> = {};
      for (const day of SCHEDULE_DAYS)
        grid[day] = (current.schedule.grid[day] ?? []).map((cell) =>
          removed && cell === removed ? "" : cell,
        );
      return { ...current, schedule: { ...current.schedule, profiles, grid } };
    });
  }
  function paintScheduleCell(day: string, hour: number, profile: string) {
    setDraft((current) => {
      const grid = { ...current.schedule.grid };
      const row = [...((grid[day] ?? Array(24).fill("")) as string[])];
      while (row.length < 24) row.push("");
      row[hour] = profile;
      grid[day] = row.slice(0, 24);
      return { ...current, schedule: { ...current.schedule, grid } };
    });
  }
  function updateHistory(patch: Partial<HistoryPrefs>) {
    setDraft((current) => ({ ...current, history: { ...current.history, ...patch } }));
  }
  function updatePortCheck(patch: Partial<Draft["portcheck"]>) {
    setDraft((current) => ({ ...current, portcheck: { ...current.portcheck, ...patch } }));
  }
  function updateIPFilter(patch: Partial<Draft["network"]["ipfilter"]>) {
    setDraft((current) => ({
      ...current,
      network: { ipfilter: { ...current.network.ipfilter, ...patch } },
    }));
  }
  function updateStats(patch: Partial<Draft["stats"]>) {
    setDraft((current) => ({ ...current, stats: { ...current.stats, ...patch } }));
  }
  function validate() {
    const next: Record<string, string> = {};
    const t = draft().tuning;
    const ports = textValue(
      t.port_range || valueFor(draft(), initial().daemon, "port_range", "network.port_range"),
    );
    if (
      ports &&
      (!/^\d{1,5}(-\d{1,5})?$/.test(ports) ||
        ports.split("-").some((part) => Number(part) < 1 || Number(part) > 65535))
    )
      next.port_range = "Enter a port or range from 1–65535.";
    // The daemon map is empty when rTorrent is unreachable; Number("") is 0,
    // which used to fail this check and make Settings unsavable while the
    // daemon was down. Skip an unset value, as the tuning loop below does.
    const dhtRaw = valueFor(draft(), initial().daemon, "dht_port", "dht.port");
    const dhtPort = Number(dhtRaw);
    if (dhtRaw !== "" && (!Number.isInteger(dhtPort) || dhtPort < 1 || dhtPort > 65535))
      next.dht_port = "Port must be 1–65535.";
    for (const field of NUMBER_TUNING) {
      const raw = valueFor(draft(), initial().daemon, field, daemonKey(field));
      if (raw !== "" && (!Number.isFinite(Number(raw)) || Number(raw) < 0))
        next[field] = "Must be a non-negative number.";
    }
    const seenThrottles = new Set<string>();
    throttleList(draft()).forEach((channel, index) => {
      if (!channel.name.trim()) next[`throttle-${index}`] = "Name is required.";
      else if (channel.name === "NULL") next[`throttle-${index}`] = "NULL is reserved by rTorrent.";
      else if (seenThrottles.has(channel.name)) next[`throttle-${index}`] = "Names must be unique.";
      seenThrottles.add(channel.name);
      if (!next[`throttle-${index}`] && (channel.up_kb < 0 || channel.down_kb < 0))
        next[`throttle-${index}`] = "Rates must be ≥ 0 (0 = unlimited).";
    });
    // Empty is the shipped default and is explicitly allowed server-side
    // (it means "follow the active theme"), so only validate a set value.
    if (draft().ui.accent && !/^#[0-9a-f]{6}$/i.test(draft().ui.accent))
      next.accent = "Use a #rrggbb color.";
    const h = draft().history;
    for (const field of ["action_log_entries", "message_entries"] as const) {
      const raw = h[field];
      if (!Number.isInteger(raw) || raw < 0)
        next[field] = "Must be a non-negative integer (0 disables).";
    }
    if (durationToNs(h.action_log_retention) === null)
      next.action_log_retention = "Use a duration like 24h, 90m, or 3600s.";
    if (
      h.recorder_bytes !== undefined &&
      h.recorder_bytes !== 0 &&
      (!Number.isInteger(h.recorder_bytes) ||
        h.recorder_bytes < 1048576 ||
        h.recorder_bytes > 134217728)
    )
      next.recorder_bytes = "Use 0 for the default, or 1048576–134217728 bytes.";
    if (h.global_entries !== null && (!Number.isInteger(h.global_entries) || h.global_entries < 0))
      next.global_entries = "Must be a non-negative integer (empty = 5000-event default).";
    const trafficDays = draft().stats.traffic_days;
    if (trafficDays !== null && (!Number.isInteger(trafficDays) || trafficDays < 0))
      next.traffic_days = "Must be a non-negative integer (0 disables, empty = 90-day default).";
    const probeUrl = draft().portcheck.url.trim();
    if (probeUrl) {
      const schemeEnd = probeUrl.indexOf("://");
      const scheme = schemeEnd < 0 ? "" : probeUrl.slice(0, schemeEnd).toLowerCase();
      if (
        (scheme !== "http" && scheme !== "https") ||
        !probeUrl
          .slice(schemeEnd + 3)
          .split(/[\/?#]/)[0]
          .trim()
      ) {
        next.portcheck_url = "Use an absolute http(s) URL.";
      } else if (!probeUrl.includes("{port}")) {
        next.portcheck_url = "The URL must contain a {port} placeholder.";
      }
    }
    if (draft().portcheck.timeout.trim() && durationToNs(draft().portcheck.timeout) === null)
      next.portcheck_timeout = "Use a duration like 10s, 1m, or 300s.";
    const blocklist = draft().network.ipfilter;
    if (blocklist.path.trim() && blocklist.url.trim()) {
      next.ipfilter_source = "Set exactly one of a local file or a URL.";
    } else if (blocklist.path.trim() && !blocklist.path.startsWith("/")) {
      next.ipfilter_source = "The file path must be absolute.";
    } else if (blocklist.url.trim() && !/^https?:\/\/.+/.test(blocklist.url.trim())) {
      next.ipfilter_source = "The URL must be an absolute http(s) address.";
    }
    if (blocklist.refresh_interval.trim() && durationToNs(blocklist.refresh_interval) === null)
      next.ipfilter_refresh = "Use a duration like 24h, 12h, or 3600s.";
    draft().directories.watch.forEach((entry, index) => {
      if (!entry.path.trim()) next[`watch-${index}`] = "Path is required.";
      else if (!entry.path.startsWith("/")) next[`watch-${index}`] = "Path must be absolute.";
      else if (entry.poll_interval.trim() && durationToNs(entry.poll_interval) === null)
        next[`watch-${index}`] = "Use a duration like 5s, 30s, or 2m.";
    });
    draft().automation.on_complete.forEach((rule, index) => {
      if (!rule.name.trim()) next[`rule-${index}`] = "Rule name is required.";
      else if (rule.name_regex.trim()) {
        try {
          new RegExp(rule.name_regex);
        } catch {
          next[`rule-${index}`] = "Invalid regular expression.";
        }
      }
      if (
        !next[`rule-${index}`] &&
        (rule.min_size < 0 ||
          rule.max_size < 0 ||
          (rule.min_size > 0 && rule.max_size > 0 && rule.min_size > rule.max_size))
      ) {
        next[`rule-${index}`] = "Size range must be non-negative with max ≥ min.";
      }
      if (
        !next[`rule-${index}`] &&
        !(
          rule.set_label.trim() ||
          rule.move_to.trim() ||
          rule.add_tracker.trim() ||
          rule.webhook.trim()
        )
      ) {
        next[`rule-${index}`] = "Define at least one action.";
      }
    });
    const feedNames = new Set(
      draft()
        .automation.rss.feeds.map((feed) => feed.name.trim())
        .filter(Boolean),
    );
    draft().automation.rss.feeds.forEach((feed, index) => {
      if (!feed.name.trim()) next[`feed-${index}`] = "Feed name is required.";
      else if (!/^https?:\/\/.+/.test(feed.url.trim()))
        next[`feed-${index}`] = "URL must be an absolute http(s) address.";
      else if (feed.poll_interval.trim() && durationToNs(feed.poll_interval) === null)
        next[`feed-${index}`] = "Use a duration like 15m, 1h, or 300s.";
    });
    draft().automation.rss.filters.forEach((filter, index) => {
      if (!filter.name.trim()) next[`rssfilter-${index}`] = "Filter name is required.";
      else if (filter.feed && !feedNames.has(filter.feed))
        next[`rssfilter-${index}`] = "Feed is not in the list above.";
      else if (filter.title_regex.trim()) {
        try {
          new RegExp(filter.title_regex);
        } catch {
          next[`rssfilter-${index}`] = "Invalid regular expression.";
        }
      }
      if (
        !next[`rssfilter-${index}`] &&
        (filter.min_size < 0 ||
          filter.max_size < 0 ||
          (filter.min_size > 0 && filter.max_size > 0 && filter.min_size > filter.max_size))
      ) {
        next[`rssfilter-${index}`] = "Size range must be non-negative with max ≥ min.";
      }
    });
    draft().automation.unpack.rules.forEach((rule, index) => {
      if (!rule.name.trim()) next[`unpack-${index}`] = "Rule name is required.";
      else if (rule.destination.trim() && !rule.destination.startsWith("/"))
        next[`unpack-${index}`] = "Destination must be an absolute path.";
    });
    const seenGroups = new Set<string>();
    draft().seeding.groups.forEach((group, index) => {
      if (!group.name.trim()) next[`seeding-${index}`] = "Group name is required.";
      else if (seenGroups.has(group.name)) next[`seeding-${index}`] = "Names must be unique.";
      seenGroups.add(group.name);
      if (
        !next[`seeding-${index}`] &&
        (group.min_ratio < 0 || group.max_ratio < 0 || group.min_upload_bytes < 0)
      )
        next[`seeding-${index}`] = "Thresholds must be ≥ 0.";
      else if (
        !next[`seeding-${index}`] &&
        group.min_ratio > 0 &&
        group.max_ratio > 0 &&
        group.min_ratio > group.max_ratio
      )
        next[`seeding-${index}`] = "Max ratio must be ≥ min ratio.";
      else if (
        !next[`seeding-${index}`] &&
        group.max_seeding_time.trim() &&
        durationToNs(group.max_seeding_time) === null
      )
        next[`seeding-${index}`] = "Use a duration like 72h, 90m, or 3600s.";
      else if (
        !next[`seeding-${index}`] &&
        group.action === "stop_and_set_label" &&
        !group.label.trim()
      )
        next[`seeding-${index}`] = "Label is required for stop and set label.";
    });
    const seenProfiles = new Set<string>();
    draft().schedule.profiles.forEach((profile, index) => {
      if (!profile.name.trim()) next[`schedule-${index}`] = "Profile name is required.";
      else if (seenProfiles.has(profile.name)) next[`schedule-${index}`] = "Names must be unique.";
      seenProfiles.add(profile.name);
      if (!next[`schedule-${index}`] && !/^#[0-9a-f]{6}$/i.test(profile.color))
        next[`schedule-${index}`] = "Use a #rrggbb color.";
      else if (!next[`schedule-${index}`] && (profile.down_kb < 0 || profile.up_kb < 0))
        next[`schedule-${index}`] = "Rates must be ≥ 0 (0 = unlimited).";
      const seenChannels = new Set<string>();
      profile.throttles.forEach((channel) => {
        if (!channel.name.trim()) next[`schedule-${index}`] = "Channel names are required.";
        else if (seenChannels.has(channel.name))
          next[`schedule-${index}`] = "Channel names must be unique per profile.";
        seenChannels.add(channel.name);
        if (!next[`schedule-${index}`] && (channel.up_kb < 0 || channel.down_kb < 0))
          next[`schedule-${index}`] = "Channel rates must be ≥ 0.";
      });
    });
    if (draft().schedule.timezone.trim()) {
      try {
        const zones = (Intl as any).supportedValuesOf?.("timeZone") as string[] | undefined;
        if (zones && !zones.includes(draft().schedule.timezone.trim()))
          next.timezone = "Unknown IANA time zone.";
      } catch {
        /* older runtimes skip client-side zone validation; the server validates on save */
      }
    }
    {
      const names = new Set(draft().schedule.profiles.map((profile) => profile.name));
      for (const day of SCHEDULE_DAYS) {
        for (const cell of draft().schedule.grid[day] ?? []) {
          if (cell && !names.has(cell)) {
            next["schedule-grid"] = `Grid references unknown profile ${cell}.`;
            break;
          }
        }
        if (next["schedule-grid"]) break;
      }
    }
    const names = new Set<string>();
    draft().labels.forEach((item, index) => {
      if (!item.name.trim()) next[`label-${index}`] = "Name is required.";
      else if (names.has(item.name)) next[`label-${index}`] = "Names must be unique.";
      names.add(item.name);
      if (!/^#[0-9a-f]{6}$/i.test(item.color)) next[`color-${index}`] = "Use #rrggbb.";
    });
    setErrors(next);
    return Object.keys(next).length === 0;
  }
  async function save() {
    if (saving()) return;
    // Browser appearance previews commit first (THM-9.3/9.4): Save persists
    // them per browser even when the YAML draft is otherwise clean.
    const hadThemePreviews = commitAppearancePreviews();
    const hadCustomPreview = commitCustomPreview();
    const hadPreviews = hadThemePreviews || hadCustomPreview;
    applyResolvedTheme();
    if (!draftDirty()) {
      if (hadPreviews) showToast("Browser appearance saved.");
      return;
    }
    if (!validate()) return;
    setSaving(true);
    setResults([]);
    try {
      for (const [oldLabel, replacement] of Object.entries(reassign())) {
        const hashes = torrentList()
          .filter((torrent) => torrent.label === oldLabel)
          .map((torrent) => torrent.hash);
        if (!hashes.length) continue;
        const response = await fetch("/api/v1/torrents/action", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action: "set_label", hashes, label: replacement }),
        });
        if (!response.ok) throw new Error(`Could not reassign torrents from ${oldLabel}`);
      }
      const payload = {
        ...draft(),
        history: {
          ...draft().history,
          action_log_retention: durationToNs(draft().history.action_log_retention),
          global_entries: draft().history.global_entries,
        },
        stats: {
          traffic_days: draft().stats.traffic_days,
        },
        portcheck: {
          url: draft().portcheck.url.trim(),
          timeout: durationToNs(draft().portcheck.timeout) ?? 0,
        },
        network: {
          ipfilter: {
            path: draft().network.ipfilter.path.trim(),
            url: draft().network.ipfilter.url.trim(),
            refresh_interval: durationToNs(draft().network.ipfilter.refresh_interval) ?? 0,
          },
        },
        directories: {
          ...draft().directories,
          watch: draft().directories.watch.map((entry) => ({
            ...entry,
            poll_interval: durationToNs(entry.poll_interval) ?? 0,
          })),
        },
        automation: {
          on_complete: draft().automation.on_complete.map((rule) => ({
            ...rule,
            private: rule.private === "true" ? true : rule.private === "false" ? false : null,
          })),
          rss: {
            feeds: draft().automation.rss.feeds.map((feed) => ({
              ...feed,
              poll_interval: durationToNs(feed.poll_interval) ?? 0,
              headers: parseHeaderLines(feed.headers),
            })),
            filters: draft().automation.rss.filters.map((filter) => ({ ...filter })),
          },
          unpack: {
            rules: draft().automation.unpack.rules.map((rule) => ({ ...rule })),
          },
        },
        seeding: {
          custom_slot: draft().seeding.custom_slot,
          groups: draft().seeding.groups.map((group) => ({
            ...group,
            max_seeding_time: durationToNs(group.max_seeding_time) ?? 0,
          })),
        },
        schedule: {
          timezone: draft().schedule.timezone.trim(),
          bandwidth: {
            profiles: draft().schedule.profiles,
            grid: draft().schedule.grid,
          },
        },
      };
      const response = await fetch("/api/v1/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(payload),
      });
      const body = (await response.json().catch(() => ({}))) as {
        message?: string;
        results?: Array<{
          key?: string;
          Key?: string;
          error?: string;
          err?: unknown;
          Err?: unknown;
        }>;
        saved?: boolean;
        error?: string | { message?: string };
      };
      const failure =
        body.message ||
        (typeof body.error === "string" ? body.error : body.error?.message) ||
        "Could not save settings";
      if (!response.ok) throw new Error(failure);
      const runtime = (body.results ?? []).map((item) => ({
        key: item.key ?? item.Key ?? "setting",
        error: item.error || String(item.err ?? item.Err ?? "") || undefined,
      }));
      setResults(runtime);
      if (!body.saved)
        throw new Error(
          typeof body.error === "string"
            ? body.error
            : body.error?.message || "Settings were not persisted",
        );
      setInitial({ ...draft(), daemon: initial().daemon });
      setReassign({});
      showToast(
        runtime.some((item) => item.error)
          ? "Settings saved; some runtime updates failed."
          : "Settings saved.",
      );
      void refreshThrottles();
      void refreshSeeding();
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not save settings");
    } finally {
      setSaving(false);
    }
  }
  function revert() {
    discardAppearancePreviews();
    discardCustomPreview();
    applyResolvedTheme();
    setDraft(clone(stripDaemon(initial())));
    setErrors({});
    setResults([]);
    setReassign({});
  }
  async function deleteLabel(name: string) {
    const confirmed = await confirmDialog({
      title: `Delete label "${name}"`,
      body: "Affected torrents can be cleared or reassigned on Save.",
      confirmLabel: "Delete",
      skipKey: "delete-label",
    });
    if (!confirmed) return;
    const replacement = await promptDialog({
      title: `Delete label "${name}"`,
      label: "Reassign affected torrents to this label (leave blank to clear)",
      confirmLabel: "Delete",
    });
    if (typeof replacement !== "string") return;
    setDraft((current) => ({
      ...current,
      labels: current.labels.filter((item) => item.name !== name),
      directories: {
        ...current.directories,
        per_label: Object.fromEntries(
          Object.entries(current.directories.per_label).filter(([key]) => key !== name),
        ),
      },
    }));
    setReassign((current) => ({ ...current, [name]: replacement.trim() }));
  }
  async function executeRaw() {
    if (!rawMethod()) return;
    const confirmed = await confirmDialog({
      title: "Execute method",
      body: `Execute XML-RPC method ${rawMethod()}? This is an operator escape hatch.`,
      confirmLabel: "Execute",
      danger: true,
    });
    if (!confirmed) return;
    try {
      const response = await fetch("/api/v1/settings/execute", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          method: rawMethod(),
          params: rawParams().split("\n").filter(Boolean),
        }),
      });
      const body = (await response.json().catch(() => ({}))) as { message?: string };
      if (!response.ok) throw new Error(body.message || "Method failed");
      showToast("XML-RPC method completed.");
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Method failed");
    }
  }
  return (
    <div class="settings-panel">
      <nav class="settings-nav" aria-label="Settings sections">
        <For each={NAV}>
          {(section) => (
            <button
              classList={{ active: settingsSection() === section }}
              type="button"
              onClick={() => setSettingsSection(section)}
            >
              {section}
              {section === "Advanced" && <small>.rtorrent.rc</small>}
            </button>
          )}
        </For>
      </nav>
      <main class="settings-content">
        <Show when={!loading()} fallback={<div class="settings-loading">Loading settings…</div>}>
          <Show
            when={!loadFailed()}
            fallback={
              <div class="settings-loading">
                Could not load settings.{" "}
                <button type="button" onClick={() => void load()}>
                  Retry
                </button>
              </div>
            }
          >
            <SettingsSection
              active={settingsSection()}
              draft={draft()}
              daemon={initial().daemon}
              errors={errors()}
              updateTuning={updateTuning}
              updateDirectory={updateDirectory}
              updateWatch={updateWatch}
              addWatch={addWatch}
              removeWatch={removeWatch}
              updateRule={updateRule}
              addRule={addRule}
              removeRule={removeRule}
              updateFeed={updateFeed}
              addFeed={addFeed}
              removeFeed={removeFeed}
              updateFilter={updateFilter}
              addFilter={addFilter}
              removeFilter={removeFilter}
              updateUnpackRule={updateUnpackRule}
              addUnpackRule={addUnpackRule}
              removeUnpackRule={removeUnpackRule}
              updateSeedingGroup={updateSeedingGroup}
              addSeedingGroup={addSeedingGroup}
              removeSeedingGroup={removeSeedingGroup}
              updateScheduleProfile={updateScheduleProfile}
              addScheduleProfile={addScheduleProfile}
              removeScheduleProfile={removeScheduleProfile}
              paintScheduleCell={paintScheduleCell}
              updateHistory={updateHistory}
              updateStats={updateStats}
              updatePortCheck={updatePortCheck}
              updateIPFilter={updateIPFilter}
              setDraft={setDraft}
              deleteLabel={deleteLabel}
              rawMethod={rawMethod()}
              setRawMethod={setRawMethod}
              rawParams={rawParams()}
              setRawParams={setRawParams}
              executeRaw={executeRaw}
              saveSettings={() => void save()}
            />
            <Show when={results().length}>
              <div class="settings-results">
                <For each={results()}>
                  {(result) => (
                    <div classList={{ failure: Boolean(result.error) }}>
                      <span>{result.key}</span>
                      <b>{result.error || "Applied"}</b>
                    </div>
                  )}
                </For>
              </div>
            </Show>
            <footer class="settings-footer">
              <button type="button" disabled={!dirty() || saving()} onClick={revert}>
                Revert
              </button>
              <button
                class="settings-save"
                type="button"
                disabled={!dirty() || saving()}
                onClick={() => void save()}
              >
                {saving() ? "Saving…" : "Save"}
              </button>
            </footer>
          </Show>
        </Show>
      </main>
    </div>
  );
}
