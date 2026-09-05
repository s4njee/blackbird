import type { PeerColumnKey } from "./peerColumns";
import type { Peer } from "./types";
import { peerKey } from "./peerKey.js";

export type PeerSortKey = { column: PeerColumnKey; direction: "asc" | "desc" };

/** Compares one peer column. Text uses locale-aware compare; numbers numeric. */
export function comparePeerColumn(a: Peer, b: Peer, column: PeerColumnKey): number {
  const numeric = (left: number, right: number) => left - right;
  switch (column) {
    case "ip":
      return peerKey(a).localeCompare(peerKey(b), undefined, {
        sensitivity: "base",
        numeric: true,
      });
    case "port":
      return numeric(a.port, b.port);
    case "country":
      return (a.countryCode || "—").localeCompare(b.countryCode || "—", undefined, {
        sensitivity: "base",
      });
    case "client":
      return a.client.localeCompare(b.client, undefined, { sensitivity: "base", numeric: true });
    case "flags":
      return a.flags.localeCompare(b.flags, undefined, { sensitivity: "base" });
    case "have":
      return numeric(a.completedPercent, b.completedPercent);
    case "downRate":
      return numeric(a.downRate, b.downRate);
    case "upRate":
      return numeric(a.upRate, b.upRate);
    case "downloadedBytes":
      return numeric(a.downloadedBytes, b.downloadedBytes);
    case "uploadedBytes":
      return numeric(a.uploadedBytes, b.uploadedBytes);
    case "id":
      return a.id.localeCompare(b.id, undefined, { sensitivity: "base" });
  }
}

/** Stable ip:port tie-breaker prevents rows jumping between live ticks. */
export function comparePeer(a: Peer, b: Peer, keys: PeerSortKey[]): number {
  for (const key of keys) {
    const result = comparePeerColumn(a, b, key.column);
    if (result) return key.direction === "asc" ? result : -result;
  }
  return peerKey(a).localeCompare(peerKey(b), undefined, { sensitivity: "base", numeric: true });
}

/**
 * Keeps a sorted peer row list, doing binary insert/removal for small deltas
 * so a 200-peer list under a 1s detail tick doesn't rebuild/reflow every time
 * (PAR-2.4: "updates in place keyed by ip:port with no scroll jump").
 */
export class PeerSorter {
  private rows: Peer[] = [];
  private prior = new Map<string, Peer>();
  private signature = "";

  sort(input: Peer[], keys: PeerSortKey[]): Peer[] {
    const sig = JSON.stringify(keys);
    if (sig !== this.signature) return this.fullSort(input, keys, sig);
    const current = new Map(input.map((peer) => [peerKey(peer), peer]));
    const changed = input.filter((peer) => this.prior.get(peerKey(peer)) !== peer);
    const removedCount = this.rows.length + changed.length - input.length;
    if (changed.length + Math.max(0, removedCount) > Math.max(24, input.length / 4))
      return this.fullSort(input, keys, sig);
    if (!changed.length && removedCount === 0) return this.rows;
    const changedKeys = new Set(changed.map((peer) => peerKey(peer)));
    this.rows = this.rows.filter(
      (peer) => current.has(peerKey(peer)) && !changedKeys.has(peerKey(peer)),
    );
    for (const peer of changed) this.insert(peer, keys);
    this.prior = current;
    return this.rows;
  }

  private fullSort(input: Peer[], keys: PeerSortKey[], sig: string): Peer[] {
    this.signature = sig;
    this.rows = [...input].sort((a, b) => comparePeer(a, b, keys));
    this.prior = new Map(input.map((peer) => [peerKey(peer), peer]));
    return this.rows;
  }

  private insert(peer: Peer, keys: PeerSortKey[]) {
    let low = 0;
    let high = this.rows.length;
    while (low < high) {
      const mid = (low + high) >>> 1;
      if (comparePeer(peer, this.rows[mid], keys) > 0) low = mid + 1;
      else high = mid;
    }
    this.rows.splice(low, 0, peer);
  }
}
