// API types mirroring the Go backend's JSON contract (internal/rtorrent,
// internal/poller). Any shape change on the Go side must be reflected here.

/** One normalized torrent row (rtorrent.Torrent). */
export interface Torrent {
  hash: string;
  name: string;
  sizeBytes: number;
  completedBytes: number;
  leftBytes: number;
  downloadedBytes: number;
  uploadedBytes: number;
  percent: number;
  complete: boolean;
  isOpen: boolean;
  state: string; // downloading | seeding | stopped | queued | checking | error
  message: string;
  checkingPercent: number;
  seeds: number;
  peers: number;
  downRate: number;
  upRate: number;
  etaSeconds: number;
  ratio: number;
  label: string;
  custom2: string;
  custom3: string;
  custom4: string;
  custom5: string;
  ratioGroup: string;
  throttle: string;
  tiedToFile: string;
  skippedBytes: number;
  peersAccounted: number;
  chunksHashed: number;
  isMultiFile: boolean;
  directory: string;
  connection: string;
  addedAt: string; // RFC3339
  finishedAt: string; // RFC3339, empty until complete
  creationDate: string; // RFC3339
  trackerHost: string;
  trackerStatus: string;
  isPrivate: boolean;
  basePath: string;
  priority: number;
  superseeding: boolean;
  sequential: boolean;
}

/** Global session stats for the status bar and top bar (rtorrent.GlobalStats). */
export interface GlobalStats {
  downRate: number;
  upRate: number;
  sessionUpTotal: number;
  sessionDownTotal: number;
  sessionRatio: number;
  version: string;
  libraryVersion: string;
  port: number;
  dhtNodes: number;
}

/** One configured mount's statfs snapshot (poller.Volume). */
export interface Volume {
  path: string;
  totalBytes: number;
  freeBytes: number;
}

/** Sidebar filter counts (poller.Aggregates). */
export interface Aggregates {
  status: Record<string, number>;
  labels: Record<string, number>;
  trackers: Record<string, number>;
}

/** Full session snapshot (poller.Snapshot). */
export interface SessionSnapshot {
  generatedAt: string;
  torrents: Torrent[];
  global: GlobalStats;
  aggregates: Aggregates;
  volumes: Volume[];
  status: "connected" | "disconnected";
  lastError?: string;
  stale: boolean;
  connectedSince: string; // RFC3339
}

/** One poll cycle's change set pushed over the WebSocket (poller.Delta). */
export interface Delta {
  added?: Torrent[];
  changed?: Torrent[];
  removed?: string[];
  globalChanged?: boolean;
  global?: GlobalStats;
  status?: "connected" | "disconnected";
  aggregates?: Aggregates;
  at?: string; // RFC3339
}

/** One history sample backing the sparkline and throughput graph. */
export interface RateSample {
  at: number; // epoch ms (client-side), or time string from /api/stats
  downRate: number;
  upRate: number;
}

export interface TorrentFile {
  index: number;
  path: string;
  sizeBytes: number;
  completedChunks: number;
  sizeChunks: number;
  priority: number;
}

export interface Peer {
  id: string;
  address: string;
  port: number;
  client: string;
  completedPercent: number;
  downRate: number;
  upRate: number;
  flags: string;
}

export interface Tracker {
  index: number;
  url: string;
  isEnabled: boolean;
  group: number;
  seeds: number;
  leechers: number;
  nextAnnounceAt: string;
  latestEvent: string;
  failedCount: number;
  successCount: number;
  newPeers: number;
}

export interface Transfer {
  downloadedBytes: number;
  uploadedBytes: number;
  chunkSize: number;
  chunkCount: number;
  chunksDone: number;
  directory: string;
}

/** Lazily-polled, focused-torrent data from GET /api/torrents/:hash and WS. */
export interface TorrentDetail {
  hash: string;
  files: TorrentFile[];
  peers: Peer[];
  trackers: Tracker[];
  transfer: Transfer;
}

/** Versioned WebSocket envelope (ws.go wsEnvelope). */
export interface WsEnvelope {
  v: number;
  type: "snapshot" | "delta" | "detail" | "pong";
  hash?: string;
  data?: unknown;
}
