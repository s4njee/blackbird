import { For, Show, createMemo } from "solid-js";
import { formatBytes } from "../lib/format";
import { aggregates, torrentCount, volumes } from "../store/session";
import { applySavedFilter, filters, removeSavedFilter, savedFilterIsActive, savedFilters, setFilter } from "../store/ui";

const STATUS_LABELS: Record<string, string> = {
  downloading: "Downloading", seeding: "Seeding", completed: "Completed", active: "Active", inactive: "Inactive", stopped: "Stopped", queued: "Queued", checking: "Checking", error: "Error",
};
const STATUS_ITEMS = ["downloading", "seeding", "completed", "active", "inactive", "stopped", "queued", "checking", "error"];
const LABEL_COLORS: Record<string, string> = {
  iso: "iso", archive: "archive", kernel: "kernel", apps: "apps", media: "media",
};

function FilterItem(props: { label: string; count?: number; active: boolean; dot?: string; onClick: () => void }) {
  return (
    <button class="sidebar-item" classList={{ active: props.active }} type="button" aria-label={props.label} data-short={props.label.slice(0, 1).toUpperCase()} onClick={props.onClick}>
      <Show when={props.dot}><span class={`label-dot ${props.dot}`} aria-hidden="true" /></Show>
      <span class="sidebar-item-label" title={props.label}>{props.label}</span>
      <Show when={props.count !== undefined}><span class="sidebar-count tnum">{props.count}</span></Show>
    </button>
  );
}

export function Sidebar() {
  const a = aggregates;
  const labelItems = createMemo(() => Object.keys(a().labels).sort((x, y) => x.localeCompare(y)));
  const trackerItems = createMemo(() => Object.keys(a().trackers).sort((x, y) => a().trackers[y] - a().trackers[x] || x.localeCompare(y)));
  const volume = createMemo(() => volumes()[0]);
  const used = createMemo(() => {
    const v = volume();
    return v && v.totalBytes ? Math.min(100, Math.max(0, ((v.totalBytes - v.freeBytes) / v.totalBytes) * 100)) : 0;
  });

  return (
    <aside class="sidebar" aria-label="Torrent filters">
      <div class="sidebar-scroll">
        <section class="sidebar-group">
          <div class="sidebar-caption">Status</div>
          <FilterItem label="All" count={a().status.all ?? torrentCount()} active={!filters().status} onClick={() => setFilter("status", "")} />
          <For each={STATUS_ITEMS}>{(status) => (
            <FilterItem label={STATUS_LABELS[status]} count={a().status[status] ?? 0} active={filters().status === status} onClick={() => setFilter("status", status)} />
          )}</For>
        </section>
        <Show when={savedFilters().length}>
          <section class="sidebar-group">
            <div class="sidebar-caption">Saved</div>
            <For each={savedFilters()}>{(saved) => (
              <div class="saved-filter-row"><FilterItem label={saved.name} active={savedFilterIsActive(saved)} onClick={() => applySavedFilter(saved)} /><button type="button" aria-label={`Remove saved filter ${saved.name}`} title="Remove saved filter" onClick={() => removeSavedFilter(saved.id)}>×</button></div>
            )}</For>
          </section>
        </Show>
        <section class="sidebar-group">
          <div class="sidebar-caption">Labels</div>
          <FilterItem label="All" count={torrentCount()} active={!filters().label} onClick={() => setFilter("label", "")} />
          <For each={labelItems()}>{(label) => (
            <FilterItem label={label} count={a().labels[label]} active={filters().label === label} dot={LABEL_COLORS[label] ?? "neutral"} onClick={() => setFilter("label", label)} />
          )}</For>
        </section>
        <section class="sidebar-group">
          <div class="sidebar-caption">Trackers</div>
          <FilterItem label="All" count={torrentCount()} active={!filters().tracker} onClick={() => setFilter("tracker", "")} />
          <For each={trackerItems()}>{(tracker) => (
            <FilterItem label={tracker} count={a().trackers[tracker]} active={filters().tracker === tracker} onClick={() => setFilter("tracker", tracker)} />
          )}</For>
        </section>
      </div>
      <Show when={volume()}>{(v) => (
        <footer class="volume-footer">
          <div class="volume-label"><span title={v().path}>{v().path}</span><span class="tnum">{formatBytes(v().totalBytes - v().freeBytes)} / {formatBytes(v().totalBytes)}</span></div>
          <div class="volume-track"><span style={{ width: `${used()}%` }} /></div>
        </footer>
      )}</Show>
    </aside>
  );
}
