import type { ColumnKey } from "./columns";
import type { Torrent } from "./types";

export type SortDirection = "asc" | "desc";
export type SortKey = { column: ColumnKey; direction: SortDirection };

const STATE_ORDER: Record<string, number> = {
  downloading: 0,
  // An active hash check is work in progress, so keep it with the
  // downloading/seeding states instead of isolating it after queued rows.
  checking: 1,
  seeding: 2,
  queued: 3,
  stopped: 4,
  error: 5,
};
const TRACKER_STATUS_ORDER: Record<string, number> = {
  working: 0,
  updating: 1,
  "not contacted": 2,
  disabled: 3,
  failed: 4,
};
function dateValue(value: string) {
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function stateSortValue(torrent: Torrent) {
  // rTorrent reports a newly queued check as "checking" before it has
  // hashed its first chunk. Treat that phase like queued for status sort.
  if (torrent.state === "checking" && torrent.checkingPercent <= 0)
    return STATE_ORDER.queued;
  return STATE_ORDER[torrent.state] ?? 99;
}

/** Compares one semantic column: numeric, date, enum, or locale-aware text. */
export function compareColumn(a: Torrent, b: Torrent, column: ColumnKey) {
  const numeric = (left: number, right: number) => left - right;
  switch (column) {
    case "sizeBytes":
      return numeric(a.sizeBytes, b.sizeBytes);
    case "percent":
      return numeric(a.percent, b.percent);
    case "leftBytes":
      return numeric(a.leftBytes, b.leftBytes);
    case "seedsPeers":
      return numeric(a.seeds + a.peers, b.seeds + b.peers);
    case "seeds":
      return numeric(a.seeds, b.seeds);
    case "peers":
      return numeric(a.peers, b.peers);
    case "downRate":
      return numeric(a.downRate, b.downRate);
    case "upRate":
      return numeric(a.upRate, b.upRate);
    case "downloadedBytes":
      return numeric(a.downloadedBytes, b.downloadedBytes);
    case "uploadedBytes":
      return numeric(a.uploadedBytes, b.uploadedBytes);
    case "etaSeconds":
      return numeric(
        a.etaSeconds < 0 ? Infinity : a.etaSeconds,
        b.etaSeconds < 0 ? Infinity : b.etaSeconds,
      );
    case "ratio":
      return numeric(a.ratio, b.ratio);
    case "priority":
      return numeric(a.priority, b.priority);
    case "addedAt":
      return numeric(dateValue(a.addedAt), dateValue(b.addedAt));
    case "finishedAt":
      return numeric(dateValue(a.finishedAt), dateValue(b.finishedAt));
    case "creationDate":
      return numeric(dateValue(a.creationDate), dateValue(b.creationDate));
    case "seedingTime":
      return numeric(
        dateValue(a.finishedAt) ? Date.now() - dateValue(a.finishedAt) : -1,
        dateValue(b.finishedAt) ? Date.now() - dateValue(b.finishedAt) : -1,
      );
    case "state":
      return numeric(stateSortValue(a), stateSortValue(b));
    case "trackerStatus":
      return numeric(
        TRACKER_STATUS_ORDER[a.trackerStatus.toLocaleLowerCase()] ?? 99,
        TRACKER_STATUS_ORDER[b.trackerStatus.toLocaleLowerCase()] ?? 99,
      );
    case "name":
      return a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true });
    case "label":
      return a.label.localeCompare(b.label, undefined, { sensitivity: "base", numeric: true });
    case "throttle":
      return a.throttle.localeCompare(b.throttle, undefined, {
        sensitivity: "base",
        numeric: true,
      });
    case "ratioGroup":
      return a.ratioGroup.localeCompare(b.ratioGroup, undefined, {
        sensitivity: "base",
        numeric: true,
      });
    case "trackerHost":
      return a.trackerHost.localeCompare(b.trackerHost, undefined, {
        sensitivity: "base",
        numeric: true,
      });
    case "directory":
      return `${a.directory || a.basePath}`.localeCompare(
        `${b.directory || b.basePath}`,
        undefined,
        { sensitivity: "base", numeric: true },
      );
    case "hash":
      return a.hash.localeCompare(b.hash, undefined, { sensitivity: "base", numeric: true });
    case "message":
      return a.message.localeCompare(b.message, undefined, { sensitivity: "base", numeric: true });
  }
}

export function compareTorrent(a: Torrent, b: Torrent, keys: SortKey[]) {
  for (const key of keys) {
    const result = compareColumn(a, b, key.column);
    if (result) return key.direction === "asc" ? result : -result;
  }
  // A stable identity tie-breaker prevents rows from jumping between ticks.
  return a.hash.localeCompare(b.hash);
}

/** Keeps a sorted row list and performs binary insertions for small deltas. */
export class IncrementalTorrentSorter {
  private keys = "";
  private rows: Torrent[] = [];
  private prior = new Map<string, Torrent>();

  sort(input: Torrent[], sortKeys: SortKey[]) {
    const signature = JSON.stringify(sortKeys);
    if (signature !== this.keys) return this.fullSort(input, sortKeys, signature);
    const current = new Map(input.map((torrent) => [torrent.hash, torrent]));
    const changed = input.filter((torrent) => this.prior.get(torrent.hash) !== torrent);
    const removed = this.rows.length + changed.length - input.length;
    if (changed.length + Math.max(0, removed) > Math.max(32, input.length / 4))
      return this.fullSort(input, sortKeys, signature);
    if (!changed.length && !removed) return this.rows;
    const changedHashes = new Set(changed.map((torrent) => torrent.hash));
    this.rows = this.rows.filter(
      (torrent) => current.has(torrent.hash) && !changedHashes.has(torrent.hash),
    );
    for (const torrent of changed) this.insert(torrent, sortKeys);
    this.prior = current;
    return this.rows;
  }

  private fullSort(input: Torrent[], sortKeys: SortKey[], signature: string) {
    this.keys = signature;
    this.rows = [...input].sort((a, b) => compareTorrent(a, b, sortKeys));
    this.prior = new Map(input.map((torrent) => [torrent.hash, torrent]));
    return this.rows;
  }

  private insert(torrent: Torrent, sortKeys: SortKey[]) {
    let low = 0;
    let high = this.rows.length;
    while (low < high) {
      const mid = (low + high) >>> 1;
      if (compareTorrent(torrent, this.rows[mid], sortKeys) > 0) low = mid + 1;
      else high = mid;
    }
    this.rows.splice(low, 0, torrent);
  }
}
