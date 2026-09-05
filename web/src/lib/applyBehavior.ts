// Settings apply behavior (POL-8.4, shared with backlog.md DOC-7.2): what
// happens after Save. Audited against the save handler
// (internal/api/rest.go) and the service wiring (cmd/blackbird/main.go):
// every field the Settings UI edits applies live — tuning setters and
// throttle channels (ApplySequential/ApplyChannels), directories.default
// (SetDefaultDirectory), the blocklist (immediate reload), traffic/history
// retention (live setters), and automation, seeding, schedule, RSS, watch,
// labels, and ui.* (live-config re-reads or client-side state).
//
// Restart is required only for process-level keys the Settings UI does not
// edit: the bind address, the daemon endpoint, credentials, and the base
// poll loop. They are listed here so the config reference and the UI badge
// share one source of truth.
export type ApplyBehavior = "live" | "reconnect" | "restart";

/** Config keys (dotted) that need a process restart to take effect. */
export const RESTART_KEYS = [
  "server.listen",
  "server.base_url",
  "rtorrent.scgi",
  "rtorrent.timeout",
  "rtorrent.max_response_bytes",
  "auth.username",
  "auth.password_hash",
  "poll.interval",
  "log.level",
];

/** Classifies a dotted config key or Settings hint path. Unknown paths are
 * live: the save handler applies or per-key errors every editable field. */
export function applyBehavior(key: string): ApplyBehavior {
  const normalized = key.trim();
  for (const restart of RESTART_KEYS) {
    if (normalized === restart || normalized.startsWith(`${restart}.`)) return "restart";
  }
  return "live";
}
