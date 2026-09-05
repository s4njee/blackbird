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
  throttles: Record<string, number>;
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

/** One poll cycle's change set pushed over the WebSocket (poller.Delta).
 * v1 carries whole changed rows; v2 carries field patches, filtered globals,
 * and aggregate patches. The client applies both (PERF-6.2 transition). */
export interface Delta {
  added?: Torrent[];
  changed?: Torrent[]; // v1 wholes
  changedPatches?: TorrentPatch[]; // v2 {hash, fields}
  removed?: string[];
  globalChanged?: boolean;
  global?: GlobalStats;
  status?: "connected" | "disconnected";
  aggregates?: Aggregates; // v1 full
  aggregatesPatch?: AggregatesPatch; // v2 diff
  at?: string; // RFC3339
}

/** One changed row as a field-level patch (poller.TorrentPatch). Fields are
 * Torrent JSON names merged onto the stored row with Object.assign. */
export interface TorrentPatch {
  hash: string;
  fields: Partial<Torrent>;
}

/** Updated/removed-key diff of one dynamic aggregates map. */
export interface StringMapPatch {
  updated?: Record<string, number>;
  removed?: string[];
}

/** Field-level aggregates diff (poller.AggregatesPatch). */
export interface AggregatesPatch {
  status?: Record<string, number>;
  labels?: StringMapPatch;
  trackers?: StringMapPatch;
  throttles?: StringMapPatch;
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
  downloadedBytes: number;
  uploadedBytes: number;
  isSnubbed: boolean;
  countryCode: string; // ISO 3166-1 alpha-2; "" when GeoIP is unavailable
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

/** One per-torrent action/message entry from the Logger view. */
export interface LogEntry {
  at: string; // RFC3339
  kind: "action" | "message" | "add" | "move" | "complete";
  actor?: string;
  action?: string;
  result?: string;
  message?: string;
  name?: string;
}

export interface TorrentExplanation {
  hash: string;
  name: string;
  generatedAt: string;
  observedAt: string | null;
  stale: boolean;
  staleAfterSeconds: number;
  findings: Array<{
    id: string;
    kind: "observation" | "recorded_action" | "constraint" | "hypothesis" | "unknown";
    title: string;
    summary: string;
    evidence: Array<{ source: string; value: string; observedAt: string | null }>;
    target?: { kind: "tab" | "settings"; name: string; label: string };
  }>;
  coverage: string[];
}

export interface LoggerView {
  hash: string;
  entries: LogEntry[];
}

export interface SpeedView {
  hash: string;
  samples: RateSample[]; // at: RFC3339 string from the server
}

export interface GeneralFact {
  label: string;
  value: string;
  copy?: boolean;
}

export interface GeneralView {
  hash: string;
  facts: GeneralFact[];
}

/** Versioned WebSocket envelope (ws.go wsEnvelope). */
export interface WsEnvelope {
  v: number;
  type: "snapshot" | "delta" | "detail" | "bitfield" | "watch" | "automation" | "notice" | "pong";
  hash?: string;
  data?: unknown;
}

/** Server-initiated watch-directory event (PAR-3.1, api.WatchNotice). */
export interface WatchNotice {
  watchDir: string;
  file: string;
  kind: "loaded" | "duplicate" | "malformed" | "load_error" | "watch_error";
  hash?: string;
  message?: string;
}

/** Completion-rule outcome (PAR-3.2, api.AutomationNotice). */
export interface AutomationNotice {
  hash: string;
  torrent?: string;
  rule: string;
  kind: "completed" | "failed";
  message?: string;
}

/** User-facing event (POL-8.3, api.Notice): completions and RSS loads. */
export interface ServerNotice {
  kind: "completed" | "rss-loaded";
  hash: string;
  title?: string;
  message?: string;
}

/** The focused torrent's piece bitfield (PAR-2.6). */
export interface BitfieldView {
  hex: string; // hex of the piece bitfield (MSB = earliest piece)
}
