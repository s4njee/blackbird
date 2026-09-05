import type { Peer } from "./types";

/** Stable ip:port identity used to key peer rows (mirrors the Go Peer.Key).
 * IPv6 literals are bracketed so the port is unambiguous. */
export function peerKey(peer: Pick<Peer, "address" | "port">): string {
  const host =
    peer.address.includes(":") && !peer.address.startsWith("[")
      ? `[${peer.address}]`
      : peer.address;
  return `${host}:${peer.port}`;
}
