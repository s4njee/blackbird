import { createEffect, createMemo, createSignal, Show } from "solid-js";
import { formatInteger, formatRate, formatRatio, formatUptime } from "../lib/format";
import { aggregates, connected, connectedSince, globalStats, torrentCount } from "../store/session";
import { bandwidth, refreshBandwidth } from "../store/bandwidth";
import { refreshPortCheck, verdict } from "../store/portcheck";
import { refreshSchedule, scheduleStatus } from "../store/schedule";
import { navigate } from "../store/ui";
import { tickerNow, tickerTick } from "../store/ticker";
import { LimitsPopover } from "./LimitsPopover";

/** Schedule chip for the status bar (PAR-4.3): active profile, override
 * countdown, and next change. Clicking opens Settings > Scheduler. */
function ScheduleChip(props: { now: number }) {
  const status = scheduleStatus;
  const overrideLeft = createMemo(() => {
    const until = status()?.overrideUntil;
    if (!status()?.overridden || !until) return "";
    const secs = Math.max(0, Math.floor((Date.parse(until) - props.now) / 1000));
    if (secs <= 0) return "expiring";
    const mins = Math.floor(secs / 60);
    return mins > 0 ? `${mins}m left` : `${secs}s left`;
  });
  const nextIn = createMemo(() => {
    const at = status()?.nextChange;
    const next = status()?.nextProfile;
    if (!at || !next) return "";
    const secs = Math.max(0, Math.floor((Date.parse(at) - props.now) / 1000));
    if (secs <= 0) return "";
    const mins = Math.floor(secs / 60);
    const hours = Math.floor(mins / 60);
    const when = hours > 0 ? `${hours}h${mins % 60 ? ` ${mins % 60}m` : ""}` : `${mins}m`;
    return ` → ${next} in ${when}`;
  });
  return (
    <button
      class="sb-item sb-schedule tnum"
      type="button"
      title={
        status()?.overridden
          ? "Manual override active — open Scheduler settings"
          : "Open Scheduler settings"
      }
      onClick={() => {
        navigate("settings", "Scheduler");
      }}
    >
      ⏱ {status()?.activeProfile || "no schedule"}
      <Show when={status()?.overridden}>
        <span class="sb-override"> · override {overrideLeft()}</span>
      </Show>
      <Show when={!status()?.overridden && nextIn()}>
        <span class="sb-next">{nextIn()}</span>
      </Show>
    </button>
  );
}

/** Port item (PAR-5.5): the live daemon port with its last user-initiated
 * reachability verdict. Unchecked ports carry no "open" claim; clicking
 * opens Settings > Connection where the check runs. */
function PortItem(props: { port: number }) {
  const checked = createMemo(() => {
    const v = verdict();
    return v && v.port === props.port ? v : null;
  });
  const title = createMemo(() => {
    const v = checked();
    if (!v) return "Reachability unverified — open Settings to run a check";
    const when = v.checkedAt
      ? new Date(v.checkedAt).toLocaleString([], {
          month: "short",
          day: "numeric",
          hour: "2-digit",
          minute: "2-digit",
        })
      : "";
    return `Verified ${v.reachable ? "reachable" : "closed"} ${when} via ${v.method} — open Settings`;
  });
  return (
    <button
      class="sb-item sb-port tnum"
      type="button"
      title={title()}
      onClick={() => {
        navigate("settings", "Connection");
      }}
    >
      Port {formatInteger(props.port)}{" "}
      <Show when={checked()} fallback={"unverified"}>
        {(v) => (
          <span classList={{ "sb-open": v().reachable, "sb-closed": !v().reachable }}>
            {v().reachable ? "open" : "closed"}
          </span>
        )}
      </Show>
    </button>
  );
}

/** Status bar (26px): connection health, versions, counts, DHT, port, ratio, uptime. */
export function StatusBar() {
  // The clock and the 60s refresh both ride the shared 1s ticker (PERF-7.4):
  // one interval per console, paused while the tab is hidden.
  const now = tickerNow;
  const [limitsOpen, setLimitsOpen] = createSignal<number | null>(null);
  createEffect(() => {
    if (tickerTick() % 60 === 0) {
      void refreshSchedule();
      void refreshBandwidth();
      void refreshPortCheck();
    }
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
      <span
        class="sb-dot"
        role="img"
        classList={{ ok: connected() }}
        aria-label={connected() ? "Connected" : "Disconnected"}
      />
      <span class="sb-item versions">
        rtorrent <span class="tnum">{versions().v}</span> / libtorrent{" "}
        <span class="tnum">{versions().lv}</span>
      </span>
      <span class="sb-item counts tnum">
        {formatInteger(torrentCount())} torrents · {counts().status["seeding"] ?? 0} seeding ·{" "}
        {counts().status["downloading"] ?? 0} downloading
      </span>
      <span class="sb-item tnum">DHT {dht()} nodes</span>
      <button
        class="sb-item sb-rates tnum"
        type="button"
        disabled={!connected()}
        title={connected() ? "Global speed limits" : "Disconnected"}
        onClick={(event) => setLimitsOpen(event.clientX)}
      >
        ↓ {formatRate(g()?.downRate ?? 0)} /{" "}
        {bandwidth().downKb > 0 ? formatRate(bandwidth().downKb * 1024) : "∞"} · ↑{" "}
        {formatRate(g()?.upRate ?? 0)} /{" "}
        {bandwidth().upKb > 0 ? formatRate(bandwidth().upKb * 1024) : "∞"}
      </button>
      <Show when={g() && g()!.port > 0}>
        <PortItem port={g()!.port} />
      </Show>
      <span class="sb-spacer" />
      <ScheduleChip now={now()} />
      <span class="sb-item tnum">Session ratio {ratio()}</span>
      <span class="sb-item tnum">Uptime {uptime()}</span>
      <Show when={limitsOpen() !== null}>
        <LimitsPopover x={limitsOpen()!} onClose={() => setLimitsOpen(null)} />
      </Show>
    </footer>
  );
}
