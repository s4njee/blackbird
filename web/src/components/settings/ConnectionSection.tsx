// Connection section + probe editors (POL-8.8).
import { For, Show, onMount } from "solid-js";
import { SettingRow } from "./SettingRow";
import { connected, globalStats } from "../../store/session";
import {
  enabled as portCheckEnabled,
  checking as portChecking,
  verdict as portVerdict,
  refreshPortCheck,
  runPortCheck,
} from "../../store/portcheck";
import { makeFields } from "./fields";
import {
  reloading as ipfilterReloading,
  status as ipfilterStatus,
  refreshIPFilter,
  reloadIPFilter,
} from "../../store/ipfilter";
import { textValue } from "./model";
import type { Draft, SectionProps } from "./types";

/** Reachability probe editor + user-initiated check (PAR-5.5). The check
 * runs against the saved probe configuration, never automatically. */
export function PortCheckPanel(props: {
  draft: Draft;
  errors: Record<string, string>;
  updatePortCheck: (patch: Partial<Draft["portcheck"]>) => void;
}) {
  onMount(() => void refreshPortCheck());
  const checkedAt = () => {
    const v = portVerdict();
    if (!v?.checkedAt) return "";
    return new Date(v.checkedAt).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };
  return (
    <>
      <div class="settings-fields">
        <SettingRow
          label="Probe URL"
          hint="portcheck.url · {port} is replaced · empty disables"
          error={props.errors.portcheck_url}
        >
          <input
            value={props.draft.portcheck.url}
            placeholder="https://probe.example/check?port={port}"
            onInput={(event) => props.updatePortCheck({ url: event.currentTarget.value })}
            spellcheck={false}
          />
        </SettingRow>
        <SettingRow
          label="Probe timeout"
          hint="portcheck.timeout · e.g. 10s, 1m"
          error={props.errors.portcheck_timeout}
        >
          <input
            value={props.draft.portcheck.timeout}
            placeholder="10s"
            onInput={(event) => props.updatePortCheck({ timeout: event.currentTarget.value })}
          />
        </SettingRow>
      </div>
      <div class="portcheck-run">
        <button type="button" disabled={portChecking()} onClick={() => void runPortCheck()}>
          {portChecking() ? "Checking…" : "Check now"}
        </button>
        <Show
          when={portVerdict()}
          fallback={<span>{portCheckEnabled() ? "Never checked." : "No probe configured."}</span>}
        >
          {(v) => (
            <span>
              Port {v().port}{" "}
              <b classList={{ "sb-open": v().reachable, "sb-closed": !v().reachable }}>
                {v().reachable ? "open" : "closed"}
              </b>{" "}
              · {v().method} · {checkedAt()}
            </span>
          )}
        </Show>
      </div>
      <p class="settings-intro">
        The check uses the saved probe configuration — save first after editing. Blackbird never
        probes automatically.
      </p>
    </>
  );
}

/** Blocklist editor + status (PAR-5.6). The source edits as YAML-backed
 * settings; rule count, last load, and errors come from GET /api/ipfilter
 * and Reload now POSTs an immediate re-fetch/re-load. */
export function IPFilterPanel(props: {
  draft: Draft;
  errors: Record<string, string>;
  updateIPFilter: (patch: Partial<Draft["network"]["ipfilter"]>) => void;
}) {
  onMount(() => void refreshIPFilter());
  const loadedAt = () => {
    const v = ipfilterStatus();
    if (!v?.lastLoad) return "";
    return new Date(v.lastLoad).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };
  return (
    <>
      <h2>IP blocklist</h2>
      <div class="settings-fields">
        <SettingRow
          label="Blocklist file"
          hint="network.ipfilter.path · local P2P/DAT file · readable by rTorrent"
          error={props.errors.ipfilter_source}
        >
          <input
            value={props.draft.network.ipfilter.path}
            placeholder="/data/filters/ipfilter.dat"
            onInput={(event) => props.updateIPFilter({ path: event.currentTarget.value })}
            spellcheck={false}
          />
        </SettingRow>
        <SettingRow
          label="Blocklist URL"
          hint="network.ipfilter.url · fetched to a cache, then loaded · plain or .gz"
        >
          <input
            value={props.draft.network.ipfilter.url}
            placeholder="https://example.com/ipfilter.dat"
            onInput={(event) => props.updateIPFilter({ url: event.currentTarget.value })}
            spellcheck={false}
          />
        </SettingRow>
        <SettingRow
          label="Refresh interval"
          hint="network.ipfilter.refresh_interval · empty = 24h default for URLs"
          error={props.errors.ipfilter_refresh}
        >
          <input
            value={props.draft.network.ipfilter.refresh_interval}
            placeholder="24h"
            onInput={(event) =>
              props.updateIPFilter({ refresh_interval: event.currentTarget.value })
            }
          />
        </SettingRow>
      </div>
      <div class="portcheck-run">
        <button type="button" disabled={ipfilterReloading()} onClick={() => void reloadIPFilter()}>
          {ipfilterReloading() ? "Reloading…" : "Reload now"}
        </button>
        <Show when={ipfilterStatus()} fallback={<span>No blocklist configured.</span>}>
          {(v) => (
            <Show when={v().enabled} fallback={<span>No blocklist configured.</span>}>
              <span>
                {v().rules} rules · {v().source === "url" ? "URL" : "file"} ·{" "}
                {v().lastLoad ? `loaded ${loadedAt()}` : "never loaded"}
                {v().lastError ? ` · error: ${v().lastError}` : ""}
              </span>
            </Show>
          )}
        </Show>
      </div>
      <p class="settings-intro">
        Ranges load into rTorrent's ipv4_filter table on connect and on refresh. The reload uses the
        saved configuration — save first after editing.
      </p>
    </>
  );
}

export function ConnectionSection(props: SectionProps) {
  const { value, input, check } = makeFields(props);
  return (
    <section>
      <h1>Connection &amp; network</h1>
      <p class="port-indicator" classList={{ disconnected: !connected() }}>
        ● Port {globalStats()?.port ?? "—"} {connected() ? "open" : "connection unavailable"}
      </p>
      <PortCheckPanel
        draft={props.draft}
        errors={props.errors}
        updatePortCheck={props.updatePortCheck}
      />
      <IPFilterPanel
        draft={props.draft}
        errors={props.errors}
        updateIPFilter={props.updateIPFilter}
      />
      <div class="settings-fields">
        {input("port_range", "Listening port", "network.port_range · TCP + uTP", "text")}
        {check("port_random", "Randomize port", "network.port_random")}
        <SettingRow label="Encryption" hint="protocol.encryption.set">
          <select
            value={textValue(value("encryption"))}
            onChange={(event) => props.updateTuning("encryption", event.currentTarget.value)}
          >
            <option value="none">None</option>
            <option value="allow_incoming,try_outgoing">Allow</option>
            <option value="require">Require</option>
            <option value="require,require_RC4">Require RC4</option>
          </select>
        </SettingRow>
        <SettingRow label="DHT mode" hint="dht.mode.set">
          <select
            value={textValue(value("dht_mode"))}
            onChange={(event) => props.updateTuning("dht_mode", event.currentTarget.value)}
          >
            <For each={["auto", "on", "off", "disable"]}>
              {(item) => <option value={item}>{item}</option>}
            </For>
          </select>
        </SettingRow>
        {input("dht_port", "DHT port", "dht.port · 1–65535")}
        {check("use_udp", "UDP trackers", "trackers.use_udp.set")}
        {check("pex", "Peer exchange", "protocol.pex.set")}
        {input("local_address", "Report IP address", "network.local_address", "text")}
        {input("bind_address", "Bind address", "network.bind_address", "text")}
        {input("http_max_open", "Max open HTTP", "network.http.max_open")}
        {input("max_open_sockets", "Global socket cap", "network.max_open_sockets")}
        {input("max_open_files", "Max open files", "network.max_open_files")}
        {input("min_peers_normal", "Min peers (normal)", "throttle.min_peers.normal")}
        {input("max_peers_normal", "Max peers (normal)", "throttle.max_peers.normal")}
        {input("min_peers_seeded", "Min peers (seeded)", "throttle.min_peers.seeded")}
        {input("max_peers_seeded", "Max peers (seeded)", "throttle.max_peers.seeded")}
        {input("max_uploads", "Max uploads per torrent", "throttle.max_uploads")}
      </div>
    </section>
  );
}
