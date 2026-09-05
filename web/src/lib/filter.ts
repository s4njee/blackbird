import type { Torrent } from "./types";

type Comparison = { field: "ratio" | "size"; operator: ">" | ">=" | "<" | "<="; value: number };
export type ParsedQuery = {
  terms: string[];
  labels: string[];
  trackers: string[];
  paths: string[];
  statuses: string[];
  comparisons: Comparison[];
};

type SearchEntry = {
  name: string;
  hash: string;
  path: string;
  tracker: string;
  message: string;
  label: string;
};

const normalized = (value: string) => value.trim().toLocaleLowerCase();

export function matchesStatus(row: Torrent, status: string) {
  if (!status) return true;
  if (status === "completed") return row.complete;
  if (status === "active") return row.downRate > 0 || row.upRate > 0;
  if (status === "inactive") return row.downRate === 0 && row.upRate === 0 && row.isOpen;
  return row.state === status;
}

function parseSize(value: string) {
  const match = normalized(value).match(/^(\d+(?:\.\d+)?)\s*(b|kb|kib|mb|mib|gb|gib|tb|tib)?$/);
  if (!match) return null;
  const units: Record<string, number> = {
    b: 1,
    kb: 1024,
    kib: 1024,
    mb: 1024 ** 2,
    mib: 1024 ** 2,
    gb: 1024 ** 3,
    gib: 1024 ** 3,
    tb: 1024 ** 4,
    tib: 1024 ** 4,
  };
  return Number(match[1]) * (units[match[2] || "b"] ?? 1);
}

/** Parses AND-combined free-text terms and the documented filter prefixes. */
export function parseQuery(input: string): ParsedQuery {
  const result: ParsedQuery = {
    terms: [],
    labels: [],
    trackers: [],
    paths: [],
    statuses: [],
    comparisons: [],
  };
  for (const raw of input.trim().split(/\s+/)) {
    if (!raw) continue;
    const comparison = raw.match(/^(ratio|size)(>=|<=|>|<)(.+)$/i);
    if (comparison) {
      const value =
        comparison[1].toLocaleLowerCase() === "size"
          ? parseSize(comparison[3])
          : Number(comparison[3]);
      if (value !== null && Number.isFinite(value))
        result.comparisons.push({
          field: comparison[1].toLocaleLowerCase() as "ratio" | "size",
          operator: comparison[2] as Comparison["operator"],
          value,
        });
      continue;
    }
    const field = raw.match(/^(label|tracker|path|status):(.+)$/i);
    if (!field) {
      result.terms.push(normalized(raw));
      continue;
    }
    const value = normalized(field[2]);
    if (!value) continue;
    const target = field[1].toLocaleLowerCase();
    if (target === "label") result.labels.push(value);
    if (target === "tracker") result.trackers.push(value);
    if (target === "path") result.paths.push(value);
    if (target === "status") result.statuses.push(value);
  }
  return result;
}

function compare(left: number, operator: Comparison["operator"], right: number) {
  return operator === ">"
    ? left > right
    : operator === ">="
      ? left >= right
      : operator === "<"
        ? left < right
        : left <= right;
}

/** Incrementally-maintained lowercase index; searching never lowercases row fields. */
export class TorrentSearchIndex {
  private entries = new Map<string, SearchEntry>();

  replace(torrents: Torrent[]) {
    this.entries.clear();
    for (const torrent of torrents) this.update(torrent);
  }

  update(torrent: Torrent) {
    this.entries.set(torrent.hash, {
      name: normalized(torrent.name),
      hash: normalized(torrent.hash),
      path: normalized(`${torrent.basePath} ${torrent.directory}`),
      tracker: normalized(torrent.trackerHost),
      message: normalized(torrent.message),
      label: normalized(torrent.label),
    });
  }

  remove(hash: string) {
    this.entries.delete(hash);
  }

  matches(torrent: Torrent, query: ParsedQuery) {
    const entry = this.entries.get(torrent.hash);
    if (!entry) return false;
    const text = (term: string) =>
      entry.name.includes(term) ||
      entry.hash.startsWith(term) ||
      entry.path.includes(term) ||
      entry.tracker.includes(term) ||
      entry.message.includes(term);
    return (
      query.terms.every(text) &&
      query.labels.every((value) => entry.label.includes(value)) &&
      query.trackers.every((value) => entry.tracker.includes(value)) &&
      query.paths.every((value) => entry.path.includes(value)) &&
      query.statuses.every((value) => matchesStatus(torrent, value)) &&
      query.comparisons.every((rule) =>
        compare(
          rule.field === "ratio" ? torrent.ratio : torrent.sizeBytes,
          rule.operator,
          rule.value,
        ),
      )
    );
  }
}
