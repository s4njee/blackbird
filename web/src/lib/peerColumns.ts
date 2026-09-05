/** Peers-tab column catalogue. Mirrors the torrent table's columns.ts
 * contract so visibility/order/widths persist per browser under
 * `blackbird.peer-columns.v1`. */

export const PEER_COLUMN_DEFINITIONS = [
  { key: "ip", label: "IP", class: "peer-col-ip", minWidth: 118, width: 132, always: true },
  { key: "port", label: "Port", class: "peer-col-port", minWidth: 52, width: 58 },
  { key: "country", label: "Country", class: "peer-col-country", minWidth: 64, width: 72 },
  {
    key: "client",
    label: "Client",
    class: "peer-col-client",
    minWidth: 150,
    width: 190,
    fluid: true,
  },
  { key: "flags", label: "Flags", class: "peer-col-flags", minWidth: 56, width: 62 },
  { key: "have", label: "Have", class: "peer-col-have", minWidth: 88, width: 110 },
  { key: "downRate", label: "Down", class: "peer-col-down", minWidth: 74, width: 78 },
  { key: "upRate", label: "Up", class: "peer-col-up", minWidth: 74, width: 78 },
  {
    key: "downloadedBytes",
    label: "Downloaded",
    class: "peer-col-down-total",
    minWidth: 96,
    width: 104,
  },
  { key: "uploadedBytes", label: "Uploaded", class: "peer-col-up-total", minWidth: 96, width: 104 },
  { key: "id", label: "Peer ID", class: "peer-col-id", minWidth: 148, width: 200 },
] as const;

export type PeerColumnKey = (typeof PEER_COLUMN_DEFINITIONS)[number]["key"];
export const DEFAULT_PEER_COLUMN_KEYS = PEER_COLUMN_DEFINITIONS.map(
  (c) => c.key,
) as PeerColumnKey[];

export type PeerColumnLayout = {
  order: PeerColumnKey[];
  hidden: PeerColumnKey[];
  widths: Partial<Record<PeerColumnKey, number>>;
};

/** Hidden by default per the PAR-2.4 spec: the Peer ID column is available but off. */
export const DEFAULT_PEER_COLUMN_LAYOUT: PeerColumnLayout = {
  order: [...DEFAULT_PEER_COLUMN_KEYS],
  hidden: ["id"],
  widths: Object.fromEntries(PEER_COLUMN_DEFINITIONS.map((c) => [c.key, c.width])) as Partial<
    Record<PeerColumnKey, number>
  >,
};

export function peerColumnDefinition(key: PeerColumnKey) {
  return PEER_COLUMN_DEFINITIONS.find((c) => c.key === key)!;
}

export function isValidPeerColumn(value: unknown): value is PeerColumnKey {
  return typeof value === "string" && (DEFAULT_PEER_COLUMN_KEYS as string[]).includes(value);
}

/** The "always on" column: IP anchors peer identity and cannot be hidden. */
export const PEER_ALWAYS_COLUMN: PeerColumnKey = "ip";
