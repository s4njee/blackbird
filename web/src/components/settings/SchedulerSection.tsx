// Scheduler section (POL-8.8).
import { For, Show, createSignal, onCleanup, onMount } from "solid-js";
import type { Draft } from "./types";
import { SettingRow } from "./SettingRow";
import { SCHEDULE_DAYS } from "./model";
import type { ScheduleProfileDraft } from "./types";
import { clearScheduleOverride } from "../../store/schedule";
import { refreshSchedule } from "../../store/schedule";
import { scheduleStatus } from "../../store/schedule";
import { setScheduleOverride } from "../../store/schedule";
import { showToast } from "../../store/ui";

/** Bandwidth scheduler editor (PAR-4.3): profiles, paint-to-fill weekly
 * grid, timezone, and the manual override. */
export function SchedulerSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  setDraft: (fn: (value: Draft) => Draft) => void;
  updateScheduleProfile: (index: number, patch: Partial<ScheduleProfileDraft>) => void;
  addScheduleProfile: () => void;
  removeScheduleProfile: (index: number) => void;
  paintScheduleCell: (day: string, hour: number, profile: string) => void;
}) {
  const [paint, setPaint] = createSignal<string | null>(null);
  const [painting, setPainting] = createSignal(false);
  const [ovMinutes, setOvMinutes] = createSignal("60");
  const [ovDown, setOvDown] = createSignal("0");
  const [ovUp, setOvUp] = createSignal("0");
  const [ovBusy, setOvBusy] = createSignal(false);

  onMount(() => {
    void refreshSchedule();
    const stop = () => setPainting(false);
    window.addEventListener("mouseup", stop);
    onCleanup(() => window.removeEventListener("mouseup", stop));
  });

  const profiles = () => props.draft.schedule.profiles;
  const profileByName = (name: string) => profiles().find((profile) => profile.name === name);
  const activePaint = () => paint() ?? profiles()[0]?.name ?? "";

  const stroke = (day: string, hour: number) => {
    if (activePaint() === undefined) return;
    props.paintScheduleCell(day, hour, activePaint());
  };

  async function setOverride() {
    setOvBusy(true);
    try {
      await setScheduleOverride(
        Math.max(1, Math.floor(Number(ovMinutes()) || 0)),
        Math.max(0, Math.floor(Number(ovDown()) || 0)),
        Math.max(0, Math.floor(Number(ovUp()) || 0)),
      );
      showToast("Override set.");
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not set override");
    } finally {
      setOvBusy(false);
    }
  }

  const HOURS = Array.from({ length: 24 }, (_, h) => h);
  const DAY_LABELS: Record<string, string> = {
    mon: "Mon",
    tue: "Tue",
    wed: "Wed",
    thu: "Thu",
    fri: "Fri",
    sat: "Sat",
    sun: "Sun",
  };

  return (
    <section>
      <h1>Scheduler</h1>
      <p class="settings-intro">
        Bandwidth profiles apply on the minute boundary and after reconnect. Paint hours with a
        profile; empty cells leave the daemon limits alone. The status bar shows the active profile.
      </p>
      <div class="settings-fields">
        <SettingRow
          label="Time zone"
          hint="schedule.timezone · empty = server local"
          error={props.errors.timezone}
        >
          <input
            value={props.draft.schedule.timezone}
            placeholder="Local"
            onInput={(event) =>
              props.setDraft((current) => ({
                ...current,
                schedule: { ...current.schedule, timezone: event.currentTarget.value },
              }))
            }
          />
        </SettingRow>
      </div>
      <h2>Profiles</h2>
      <Show
        when={profiles().length}
        fallback={<p class="settings-intro">No profiles. Add one to start painting.</p>}
      >
        <div class="watch-entries">
          <For each={profiles()}>
            {(profile, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Name"
                    hint="schedule.bandwidth.profiles[].name"
                    error={props.errors[`schedule-${index()}`]}
                  >
                    <input
                      value={profile.name}
                      placeholder="day"
                      onInput={(event) =>
                        props.updateScheduleProfile(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Color" hint="grid swatch">
                    <div class="accent-control">
                      <input
                        type="color"
                        value={profile.color}
                        onInput={(event) =>
                          props.updateScheduleProfile(index(), { color: event.currentTarget.value })
                        }
                      />
                      <input
                        value={profile.color}
                        onInput={(event) =>
                          props.updateScheduleProfile(index(), { color: event.currentTarget.value })
                        }
                      />
                    </div>
                  </SettingRow>
                  <SettingRow label="Download limit" hint="KB/s · 0 = unlimited">
                    <input
                      type="number"
                      value={profile.down_kb || ""}
                      placeholder="0"
                      onInput={(event) =>
                        props.updateScheduleProfile(index(), {
                          down_kb: Math.max(0, Math.floor(Number(event.currentTarget.value) || 0)),
                        })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Upload limit" hint="KB/s · 0 = unlimited">
                    <input
                      type="number"
                      value={profile.up_kb || ""}
                      placeholder="0"
                      onInput={(event) =>
                        props.updateScheduleProfile(index(), {
                          up_kb: Math.max(0, Math.floor(Number(event.currentTarget.value) || 0)),
                        })
                      }
                    />
                  </SettingRow>
                </div>
                <h3 class="watch-entry-heading">
                  Channel limits (created on the daemon if missing)
                </h3>
                <div class="watch-entry-fields">
                  <For each={profile.throttles}>
                    {(channel, ci) => (
                      <div class="sort-control">
                        <input
                          value={channel.name}
                          placeholder="channel"
                          aria-label="Channel name"
                          onInput={(event) =>
                            props.updateScheduleProfile(index(), {
                              throttles: profile.throttles.map((ch, i) =>
                                i === ci() ? { ...ch, name: event.currentTarget.value } : ch,
                              ),
                            })
                          }
                        />
                        <input
                          type="number"
                          value={channel.up_kb || ""}
                          placeholder="up KB/s"
                          aria-label="Upload KB/s"
                          onInput={(event) =>
                            props.updateScheduleProfile(index(), {
                              throttles: profile.throttles.map((ch, i) =>
                                i === ci()
                                  ? {
                                      ...ch,
                                      up_kb: Math.max(
                                        0,
                                        Math.floor(Number(event.currentTarget.value) || 0),
                                      ),
                                    }
                                  : ch,
                              ),
                            })
                          }
                        />
                        <input
                          type="number"
                          value={channel.down_kb || ""}
                          placeholder="down KB/s"
                          aria-label="Download KB/s"
                          onInput={(event) =>
                            props.updateScheduleProfile(index(), {
                              throttles: profile.throttles.map((ch, i) =>
                                i === ci()
                                  ? {
                                      ...ch,
                                      down_kb: Math.max(
                                        0,
                                        Math.floor(Number(event.currentTarget.value) || 0),
                                      ),
                                    }
                                  : ch,
                              ),
                            })
                          }
                        />
                        <button
                          type="button"
                          class="settings-add-row"
                          onClick={() =>
                            props.updateScheduleProfile(index(), {
                              throttles: profile.throttles.filter((_, i) => i !== ci()),
                            })
                          }
                        >
                          ×
                        </button>
                      </div>
                    )}
                  </For>
                  <div>
                    <button
                      type="button"
                      class="settings-add-row"
                      onClick={() =>
                        props.updateScheduleProfile(index(), {
                          throttles: [...profile.throttles, { name: "", up_kb: 0, down_kb: 0 }],
                        })
                      }
                    >
                      + Channel limit
                    </button>
                  </div>
                </div>
                <div class="watch-entry-flags">
                  <button type="button" onClick={() => props.removeScheduleProfile(index())}>
                    Remove profile
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addScheduleProfile()}>
        + Add profile
      </button>
      <h2>Weekly grid</h2>
      <Show when={props.errors["schedule-grid"]}>
        <p class="settings-intro" style={{ color: "var(--status-error)" }}>
          {props.errors["schedule-grid"]}
        </p>
      </Show>
      <div class="sched-palette">
        <For each={profiles()}>
          {(profile) => (
            <button
              type="button"
              classList={{ active: activePaint() === profile.name }}
              onClick={() => setPaint(profile.name)}
            >
              <span class="sched-swatch" style={{ background: profile.color }} />
              {profile.name || "(unnamed)"}
            </button>
          )}
        </For>
        <button
          type="button"
          classList={{ active: activePaint() === "" }}
          onClick={() => setPaint("")}
        >
          Clear
        </button>
      </div>
      <div class="sched-grid" onMouseLeave={() => setPainting(false)}>
        <div class="sched-row sched-head">
          <span />
          <For each={HOURS}>{(h) => <span>{h % 2 === 0 ? h : ""}</span>}</For>
        </div>
        <For each={SCHEDULE_DAYS}>
          {(day) => (
            <div class="sched-row">
              <span>{DAY_LABELS[day]}</span>
              <For each={HOURS}>
                {(h) => {
                  const cell = () => props.draft.schedule.grid[day]?.[h] ?? "";
                  const color = () => profileByName(cell())?.color ?? "transparent";
                  return (
                    <button
                      type="button"
                      class="sched-cell"
                      title={`${DAY_LABELS[day]} ${h}:00 — ${cell() || "skip"}`}
                      style={{ background: color() }}
                      onMouseDown={(event) => {
                        event.preventDefault();
                        setPainting(true);
                        stroke(day, h);
                      }}
                      onMouseEnter={() => {
                        if (painting()) stroke(day, h);
                      }}
                    />
                  );
                }}
              </For>
            </div>
          )}
        </For>
      </div>
      <h2>Manual override</h2>
      <Show
        when={scheduleStatus()?.overridden}
        fallback={<p class="settings-intro">No override active. The grid controls the daemon.</p>}
      >
        <p class="settings-intro">
          Override active until {scheduleStatus()!.overrideUntil} — schedule paused.
        </p>
      </Show>
      <div class="settings-fields">
        <SettingRow label="Duration" hint="minutes · 1–1440">
          <input
            type="number"
            value={ovMinutes()}
            onInput={(event) => setOvMinutes(event.currentTarget.value)}
          />
        </SettingRow>
        <SettingRow label="Download limit" hint="KB/s · 0 = unlimited">
          <input
            type="number"
            value={ovDown()}
            onInput={(event) => setOvDown(event.currentTarget.value)}
          />
        </SettingRow>
        <SettingRow label="Upload limit" hint="KB/s · 0 = unlimited">
          <input
            type="number"
            value={ovUp()}
            onInput={(event) => setOvUp(event.currentTarget.value)}
          />
        </SettingRow>
      </div>
      <div class="sort-control">
        <button
          type="button"
          class="settings-add-row"
          disabled={ovBusy()}
          onClick={() => void setOverride()}
        >
          {ovBusy() ? "Setting…" : "Set override"}
        </button>
        <Show when={scheduleStatus()?.overridden}>
          <button
            type="button"
            class="settings-add-row"
            onClick={() => void clearScheduleOverride()}
          >
            Clear override
          </button>
        </Show>
      </div>
    </section>
  );
}
