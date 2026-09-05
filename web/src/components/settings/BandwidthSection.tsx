// Bandwidth section (POL-8.8).
import { For, Show, onMount } from "solid-js";
import type { Draft } from "./types";
import { SettingRow } from "./SettingRow";
import { type JSX } from "solid-js";
import type { ThrottleDraft } from "./types";
import { channels } from "../../store/throttles";
import { formatRate } from "../../lib/format";
import { refreshThrottles } from "../../store/throttles";
import { setThrottleList } from "./model";
import { throttleList } from "./model";

/** Bandwidth section (PAR-4.1): global limits plus the named throttle-channel
 * editor with live per-channel usage from the daemon. */
export function BandwidthSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  setDraft: (fn: (value: Draft) => Draft) => void;
  input: (field: string, label: string, hint: string, type?: "number" | "text") => JSX.Element;
}) {
  onMount(() => void refreshThrottles());
  const list = () => throttleList(props.draft);
  const liveFor = (name: string) => channels().find((channel) => channel.name === name);
  const patch = (index: number, patch: Partial<ThrottleDraft>) =>
    setThrottleList(
      props.setDraft,
      list().map((channel, i) => (i === index ? { ...channel, ...patch } : channel)),
    );
  return (
    <section>
      <h1>Bandwidth</h1>
      <div class="settings-fields">
        {props.input(
          "global_down_rate_kb",
          "Global download limit",
          "throttle.global_down.max_rate · KB/s · 0 = unlimited",
        )}
        {props.input(
          "global_up_rate_kb",
          "Global upload limit",
          "throttle.global_up.max_rate · KB/s · 0 = unlimited",
        )}
      </div>
      <h2>Throttle channels</h2>
      <p class="settings-intro">
        Named up/down limits assignable per torrent from the toolbar or the Throttle context
        submenu. Assigning a running torrent stops it briefly and restarts it afterwards. Removing a
        channel still in use is refused until its torrents are unassigned.
      </p>
      <Show when={list().length} fallback={<p class="settings-intro">No channels configured.</p>}>
        <div class="watch-entries">
          <For each={list()}>
            {(channel, index) => {
              const live = () => liveFor(channel.name);
              return (
                <div class="watch-entry">
                  <div class="watch-entry-fields">
                    <SettingRow
                      label="Name"
                      hint="tuning.throttles[].name"
                      error={props.errors[`throttle-${index()}`]}
                    >
                      <input
                        value={channel.name}
                        placeholder="slow"
                        onInput={(event) => patch(index(), { name: event.currentTarget.value })}
                      />
                    </SettingRow>
                    <SettingRow
                      label="Upload limit"
                      hint="tuning.throttles[].up_kb · KB/s · 0 = unlimited"
                    >
                      <input
                        type="number"
                        value={channel.up_kb || ""}
                        placeholder="0"
                        onInput={(event) =>
                          patch(index(), {
                            up_kb: Math.max(0, Math.floor(Number(event.currentTarget.value) || 0)),
                          })
                        }
                      />
                    </SettingRow>
                    <SettingRow
                      label="Download limit"
                      hint="tuning.throttles[].down_kb · KB/s · 0 = unlimited"
                    >
                      <input
                        type="number"
                        value={channel.down_kb || ""}
                        placeholder="0"
                        onInput={(event) =>
                          patch(index(), {
                            down_kb: Math.max(
                              0,
                              Math.floor(Number(event.currentTarget.value) || 0),
                            ),
                          })
                        }
                      />
                    </SettingRow>
                  </div>
                  <div class="watch-entry-flags">
                    <Show
                      when={live()}
                      fallback={<small>Not on the daemon yet — save to create.</small>}
                    >
                      <small>
                        ↑ {formatRate(live()!.upRateBps)} · ↓ {formatRate(live()!.downRateBps)}
                      </small>
                      <small>
                        {live()!.inUse} torrent{live()!.inUse === 1 ? "" : "s"} assigned
                      </small>
                    </Show>
                    <button
                      type="button"
                      onClick={() =>
                        setThrottleList(
                          props.setDraft,
                          list().filter((_, i) => i !== index()),
                        )
                      }
                    >
                      Remove
                    </button>
                  </div>
                </div>
              );
            }}
          </For>
        </div>
      </Show>
      <button
        class="settings-add-row"
        type="button"
        onClick={() =>
          setThrottleList(props.setDraft, [...list(), { name: "", up_kb: 0, down_kb: 0 }])
        }
      >
        + Add channel
      </button>
    </section>
  );
}
