import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { formatInteger, formatRatio, formatUptime } from "../lib/format";
import {
  aggregates,
  connected,
  connectedSince,
  globalStats,
  torrentCount,
} from "../store/session";

/** Status bar (26px): connection health, versions, counts, DHT, port, ratio, uptime. */
export function StatusBar() {
  const [now, setNow] = createSignal(Date.now());
  createEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    onCleanup(() => window.clearInterval(id));
  });

  const g = globalStats;
  const counts = aggregates;

  const versions = createMemo(() => {
    const s = g();
    const v = s?.version || "—";
    const lv = s?.libraryVersion || "—";
    return { v, lv };
  });

  const uptime = createMemo(() => {
    const since = connectedSince();
    if (!connected() || !since) return "—";
    const seconds = (now() - Date.parse(since)) / 1000;
    return formatUptime(seconds);
  });

  const dht = createMemo(() => (g() ? formatInteger(g()!.dhtNodes) : "—"));
  const ratio = createMemo(() => (g() ? formatRatio(g()!.sessionRatio) : "—"));

  return (
    <footer class="statusbar">
      <span class="sb-dot" classList={{ ok: connected() }} aria-label={connected() ? "Connected" : "Disconnected"} />
      <span class="sb-item versions">
        rtorrent <span class="tnum">{versions().v}</span> / libtorrent <span class="tnum">{versions().lv}</span>
      </span>
      <span class="sb-item counts tnum">
        {formatInteger(torrentCount())} torrents · {counts().status["seeding"] ?? 0} seeding ·{" "}
        {counts().status["downloading"] ?? 0} downloading
      </span>
      <span class="sb-item tnum">DHT {dht()} nodes</span>
      <Show when={g() && g()!.port > 0}>
        <span class="sb-item tnum">
          Port {formatInteger(g()!.port)} {connected() ? "open" : ""}
        </span>
      </Show>
      <span class="sb-spacer" />
      <span class="sb-item tnum">Session ratio {ratio()}</span>
      <span class="sb-item tnum">Uptime {uptime()}</span>
    </footer>
  );
}