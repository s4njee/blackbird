// Row-level store operations for the session torrent map (PERF-7.1).
//
// Kept free of WebSocket/DOM side effects so the identity and batching
// semantics are unit-testable in node: the store holds one stable entry per
// hash, deltas merge in place, and unchanged rows keep object identity so
// `<For>` never recreates their DOM.

import { produce, reconcile, type SetStoreFunction } from "solid-js/store";
import type { Delta, Torrent } from "../lib/types";

/** The keyed row map backing the session store. */
export type RowMap = Record<string, Torrent>;

/** Builds a plain snapshot map; the caller reconciles it into the store. */
export function snapshotRows(list: Torrent[]): RowMap {
  const byHash: RowMap = {};
  for (const t of list) byHash[t.hash] = t;
  return byHash;
}

/** Reconciles a full snapshot list into the store, preserving row identity
 * for unchanged torrents (reconnects and tab un-hides keep every DOM row). */
export function applySnapshotRows(setTorrents: SetStoreFunction<RowMap>, list: Torrent[]): void {
  setTorrents(reconcile(snapshotRows(list)));
}

export type DeltaResult = { updated: Torrent[]; removed: string[] };

/** Merges one delta into the store:
 * - removed hashes are deleted (unknown hashes ignored),
 * - added wholes are stored as new rows,
 * - v1 changed wholes reconcile in place (unchanged fields keep identity),
 * - v2 patches merge only their fields; patches for unknown hashes are
 *   ignored (the server always sends adds whole and first).
 * Returns merged plain rows for the search index and the removed hashes for
 * view pruning. The caller must wrap the surrounding message handling in
 * `batch()` so one WebSocket message flushes once. */
export function applyDeltaRows(
  state: RowMap,
  setTorrents: SetStoreFunction<RowMap>,
  delta: Delta,
): DeltaResult {
  const updated: Torrent[] = [];
  const removed = Array.isArray(delta.removed) ? delta.removed : [];
  if (removed.length) {
    setTorrents(
      produce((rows) => {
        for (const hash of removed) delete rows[hash];
      }),
    );
  }
  if (Array.isArray(delta.added)) {
    for (const torrent of delta.added) {
      setTorrents(torrent.hash, torrent);
      updated.push(torrent);
    }
  }
  if (Array.isArray(delta.changed)) {
    for (const torrent of delta.changed) {
      if (state[torrent.hash] === undefined) continue;
      setTorrents(torrent.hash, reconcile(torrent));
      updated.push(torrent);
    }
  }
  if (Array.isArray(delta.changedPatches)) {
    for (const patch of delta.changedPatches) {
      const row = state[patch.hash];
      if (!row) continue;
      setTorrents(patch.hash, patch.fields);
      updated.push({ ...row, ...patch.fields });
    }
  }
  return { updated, removed };
}

/** Applies an optimistic local patch to known rows only. */
export function patchRows(
  state: RowMap,
  setTorrents: SetStoreFunction<RowMap>,
  hashes: string[],
  patch: Partial<Torrent>,
): void {
  for (const hash of hashes) {
    if (state[hash] !== undefined) setTorrents(hash, patch);
  }
}

/** Restores exact rows after a failed optimistic request. */
export function restoreRows(setTorrents: SetStoreFunction<RowMap>, rows: Torrent[]): void {
  for (const row of rows) setTorrents(row.hash, reconcile(row));
}
