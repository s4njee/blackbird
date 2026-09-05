// General section: live session overview (POL-8.8).
import { formatBytes } from "../../lib/format";
import { connected } from "../../store/session";
import { globalStats } from "../../store/session";
import { torrentList } from "../../store/session";
import { SettingRow } from "./SettingRow";

export function GeneralSection() {
  return (
    <section>
      <h1>General</h1>
      <p class="settings-intro">
        Live session overview — read directly from rTorrent. Saving writes YAML atomically and
        reports any daemon-side failures below.
      </p>
      <div class="settings-fields">
        <SettingRow label="Connection" hint="live daemon state">
          <span>
            {connected() ? `Connected · port ${globalStats()?.port ?? "—"}` : "Disconnected"}
          </span>
        </SettingRow>
        <SettingRow label="Daemon" hint="live daemon versions">
          <span>
            rTorrent {globalStats()?.version ?? "—"} / libtorrent{" "}
            {globalStats()?.libraryVersion ?? "—"}
          </span>
        </SettingRow>
        <SettingRow label="Session" hint="live session totals">
          <span>
            {torrentList().length} torrents · ▼ {formatBytes(globalStats()?.sessionDownTotal ?? 0)}{" "}
            · ▲ {formatBytes(globalStats()?.sessionUpTotal ?? 0)}
          </span>
        </SettingRow>
      </div>
    </section>
  );
}
