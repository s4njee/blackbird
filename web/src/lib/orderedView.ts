// Incrementally maintained sorted/filtered torrent view (PERF-7.2).
//
// The view keeps one ordered row array (plus a parallel hash array) over the
// live session. Session rows are live store proxies with stable identity, so
// reference-diffing cannot detect change: updates are driven explicitly per
// delta (insert/remove/reposition only for torrents in the delta), while
// snapshots and filter/sort switches take a full rebuild. Membership is
// re-evaluated solely for delta rows; unchanged rows are never visited.
//
// Pure logic over plain data (no signals): the session owns one instance
// and publishes its arrays, and the unit tests drive it directly.

import { compareTorrent, type SortKey } from "./sort.js";
import type { ParsedQuery } from "./filter";
import type { Torrent } from "./types";

/** Filter inputs for one view sync (sidebar state + debounced query). */
export type ViewFilter = {
  status: string;
  label: string;
  tracker: string;
  throttle: string;
  query: string;
};

export function filterSignature(filter: ViewFilter): string {
  return `${filter.status}\n${filter.label}\n${filter.tracker}\n${filter.throttle}\n${filter.query}`;
}

/** Row predicate shared by rebuilds and delta updates (moved out of the
 * table so all paths — and the tests — use one definition). */
export function matchesRow(
  row: Torrent,
  filter: ViewFilter,
  query: ParsedQuery,
  matchesStatus: (row: Torrent, status: string) => boolean,
  searchMatches: (row: Torrent, query: ParsedQuery) => boolean,
): boolean {
  return (
    matchesStatus(row, filter.status) &&
    (!filter.label || (row.label || "unlabeled") === filter.label) &&
    (!filter.tracker || row.trackerHost === filter.tracker) &&
    (!filter.throttle || (row.throttle || "") === filter.throttle) &&
    searchMatches(row, query)
  );
}

export class OrderedTorrentView {
  rows: Torrent[] = [];
  hashes: string[] = [];
  private members = new Set<string>();
  private filterSig = "";
  private sortSig = "";
  /** Rebuilds performed (tests and diagnostics). */
  rebuilds = 0;

  private static signatures(
    filter: ViewFilter,
    sortKeys: SortKey[],
  ): { filterSig: string; sortSig: string } {
    return { filterSig: filterSignature(filter), sortSig: JSON.stringify(sortKeys) };
  }

  /** Full rebuild: re-filter every row and re-sort. Used for snapshots and
   * filter/sort switches, where no delta describes the change. */
  rebuild(
    all: Record<string, Torrent>,
    filter: ViewFilter,
    parsed: ParsedQuery,
    sortKeys: SortKey[],
    matchesStatus: (row: Torrent, status: string) => boolean,
    searchMatches: (row: Torrent, query: ParsedQuery) => boolean,
  ): void {
    const rows: Torrent[] = [];
    const members = new Set<string>();
    for (const hash of Object.keys(all)) {
      const row = all[hash];
      if (matchesRow(row, filter, parsed, matchesStatus, searchMatches)) {
        rows.push(row);
        members.add(hash);
      }
    }
    rows.sort((a, b) => compareTorrent(a, b, sortKeys));
    this.rows = rows;
    this.hashes = rows.map((row) => row.hash);
    this.members = members;
    const sigs = OrderedTorrentView.signatures(filter, sortKeys);
    this.filterSig = sigs.filterSig;
    this.sortSig = sigs.sortSig;
    this.rebuilds++;
  }

  /** Applies one delta: membership is re-evaluated only for the listed
   * hashes, and the order merges in O(n + k log k). Returns true when the
   * visible arrays changed. `updatedHashes` must cover every row whose
   * fields changed; `removedHashes` every row that left the session. A
   * filter/sort switch is detected by signature and rebuilds instead, so
   * callers cannot corrupt the index by mixing paths. */
  applyChanges(
    all: Record<string, Torrent>,
    updatedHashes: string[],
    removedHashes: string[],
    filter: ViewFilter,
    parsed: ParsedQuery,
    sortKeys: SortKey[],
    matchesStatus: (row: Torrent, status: string) => boolean,
    searchMatches: (row: Torrent, query: ParsedQuery) => boolean,
  ): boolean {
    const sigs = OrderedTorrentView.signatures(filter, sortKeys);
    if (sigs.filterSig !== this.filterSig || sigs.sortSig !== this.sortSig) {
      this.rebuild(all, filter, parsed, sortKeys, matchesStatus, searchMatches);
      return true;
    }
    const arrivals: Torrent[] = [];
    const leaving = new Set<string>();
    const seen = new Set<string>();
    for (const hash of updatedHashes) {
      if (seen.has(hash)) continue;
      seen.add(hash);
      const row = all[hash];
      if (row === undefined) {
        // Changed and removed in the same tick: removal wins.
        if (this.members.delete(hash)) leaving.add(hash);
        continue;
      }
      const wasMember = this.members.has(hash);
      const isMember = matchesRow(row, filter, parsed, matchesStatus, searchMatches);
      if (!wasMember && !isMember) continue;
      if (wasMember) leaving.add(hash);
      if (isMember) arrivals.push(row);
      else this.members.delete(hash);
    }
    for (const hash of removedHashes) {
      if (this.members.delete(hash)) leaving.add(hash);
    }
    if (!arrivals.length && !leaving.size) return false;
    arrivals.sort((a, b) => compareTorrent(a, b, sortKeys));
    const base = this.rows.filter((row) => !leaving.has(row.hash));
    const merged: Torrent[] = new Array(base.length + arrivals.length);
    let i = 0;
    let j = 0;
    let k = 0;
    while (i < base.length && j < arrivals.length) {
      if (compareTorrent(base[i], arrivals[j], sortKeys) <= 0) merged[k++] = base[i++];
      else merged[k++] = arrivals[j++];
    }
    while (i < base.length) merged[k++] = base[i++];
    while (j < arrivals.length) merged[k++] = arrivals[j++];
    for (const row of arrivals) this.members.add(row.hash);
    this.rows = merged;
    this.hashes = merged.map((row) => row.hash);
    return true;
  }
}
