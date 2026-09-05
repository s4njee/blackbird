// History section (POL-8.8).
import { SettingRow } from "./SettingRow";
import { textValue } from "./model";
import type { SectionProps } from "./types";

export function HistorySection(props: SectionProps) {
  return (
    <section>
      <h1>History &amp; Logger</h1>
      <p class="settings-intro">
        Bounds the per-torrent Logger tab (PAR-2.5): the Blackbird-side action log and the torrent's{" "}
        <code>d.message</code> history. Saving writes YAML atomically.
      </p>
      <div class="settings-fields">
        <SettingRow
          label="Flight recorder storage (bytes)"
          hint="history.recorder_bytes · 0 = 16 MiB; 1–128 MiB"
          needs="restart"
          error={props.errors.recorder_bytes}
        >
          <input
            type="number"
            min="0"
            value={props.draft.history.recorder_bytes ?? 0}
            onInput={(event) =>
              props.updateHistory({ recorder_bytes: Number(event.currentTarget.value) })
            }
          />
        </SettingRow>
        <SettingRow
          label="Action log entries per torrent"
          hint="history.action_log_entries · 0 disables"
          error={props.errors.action_log_entries}
        >
          <input
            type="number"
            value={textValue(props.draft.history.action_log_entries)}
            onInput={(event) =>
              props.updateHistory({
                action_log_entries: Math.max(0, Math.floor(Number(event.currentTarget.value) || 0)),
              })
            }
          />
        </SettingRow>
        <SettingRow
          label="Action log retention"
          hint="history.action_log_retention · e.g. 24h, 90m, 3600s"
          error={props.errors.action_log_retention}
        >
          <input
            value={props.draft.history.action_log_retention}
            onInput={(event) =>
              props.updateHistory({ action_log_retention: event.currentTarget.value })
            }
          />
        </SettingRow>
        <SettingRow
          label="Message history entries per torrent"
          hint="history.message_entries · 0 disables"
          error={props.errors.message_entries}
        >
          <input
            type="number"
            value={textValue(props.draft.history.message_entries)}
            onInput={(event) =>
              props.updateHistory({
                message_entries: Math.max(0, Math.floor(Number(event.currentTarget.value) || 0)),
              })
            }
          />
        </SettingRow>
        <SettingRow
          label="Global history events"
          hint="history.global_entries · History view ring · empty = 5000-event default"
          error={props.errors.global_entries}
        >
          <input
            type="number"
            min="0"
            placeholder="5000"
            value={props.draft.history.global_entries ?? ""}
            onInput={(event) => {
              const raw = event.currentTarget.value;
              props.updateHistory({
                global_entries: raw === "" ? null : Math.max(0, Math.floor(Number(raw) || 0)),
              });
            }}
          />
        </SettingRow>
        <SettingRow
          label="Transfer history retention"
          hint="stats.traffic_days · days of per-day/hour totals on Stats · 0 disables · empty = 90-day default"
          error={props.errors.traffic_days}
        >
          <input
            type="number"
            min="0"
            placeholder="90"
            value={props.draft.stats.traffic_days ?? ""}
            onInput={(event) => {
              const raw = event.currentTarget.value;
              props.updateStats({
                traffic_days: raw === "" ? null : Math.max(0, Math.floor(Number(raw) || 0)),
              });
            }}
          />
        </SettingRow>
      </div>
    </section>
  );
}
