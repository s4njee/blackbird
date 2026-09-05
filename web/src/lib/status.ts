import type { Torrent } from "./types";

/** Human-readable status text, including the pre-first-chunk check phase. */
export function torrentStateLabel(torrent: Torrent) {
  if (torrent.state === "checking" && torrent.checkingPercent <= 0) return "Queued";
  if (torrent.state === "checking") return `Checking ${torrent.checkingPercent.toFixed(0)}%`;
  if (torrent.state === "error") return torrent.message || "Tracker error";
  return torrent.state ? torrent.state[0].toUpperCase() + torrent.state.slice(1) : "—";
}
