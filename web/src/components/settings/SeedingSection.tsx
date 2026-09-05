// Seeding section (POL-8.8).
import { For, Show, createSignal } from "solid-js";
import type { Draft } from "./types";
import { SettingRow } from "./SettingRow";
import type { SeedingGroupDraft } from "./types";
import { durationToNs } from "../../lib/duration";
import { showToast } from "../../store/ui";

/** Seeding-policy editor (PAR-4.2): ratio groups with a dry-run preview of
 * what would act on the current session right now. */
export function SeedingSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  setDraft: (fn: (value: Draft) => Draft) => void;
  updateSeedingGroup: (index: number, patch: Partial<SeedingGroupDraft>) => void;
  addSeedingGroup: () => void;
  removeSeedingGroup: (index: number) => void;
}) {
  type DryRunMatch = {
    hash: string;
    name: string;
    group: string;
    condition: string;
    action: string;
    detail: string;
  };
  type DryRunResult = { matches: DryRunMatch[]; evaluated: number };
  const [result, setResult] = createSignal<DryRunResult | null>(null);
  const [testing, setTesting] = createSignal(false);

  async function testGroups() {
    setTesting(true);
    try {
      const response = await fetch("/api/v1/seeding/dry-run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          custom_slot: props.draft.seeding.custom_slot,
          groups: props.draft.seeding.groups.map((group) => ({
            ...group,
            max_seeding_time: durationToNs(group.max_seeding_time) ?? 0,
          })),
        }),
      });
      const body = (await response.json().catch(() => ({}))) as {
        matches?: DryRunMatch[];
        evaluated?: number;
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(body.error?.message || "Dry run failed");
      setResult({ matches: body.matches ?? [], evaluated: body.evaluated ?? 0 });
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Dry run failed");
    } finally {
      setTesting(false);
    }
  }

  return (
    <section>
      <h1>Seeding</h1>
      <p class="settings-intro">
        Ratio groups stop, label, or erase seeding torrents once a group's conditions are met. Each
        torrent triggers a group at most once, even across restarts; moving a torrent to another
        group re-arms it there. Assign groups from the Ratio group context submenu or the Ratio
        group column; enforcement runs in Blackbird's poller, not in daemon schedules.
      </p>
      <div class="settings-fields">
        <SettingRow
          label="Assignment slot"
          hint="seeding.custom_slot · custom field holding the group"
        >
          <select
            value={props.draft.seeding.custom_slot}
            onChange={(event) =>
              props.setDraft((current) => ({
                ...current,
                seeding: { ...current.seeding, custom_slot: event.currentTarget.value },
              }))
            }
          >
            <option value="custom2">custom2</option>
            <option value="custom3">custom3</option>
            <option value="custom4">custom4</option>
            <option value="custom5">custom5</option>
          </select>
        </SettingRow>
      </div>
      <h2>Ratio groups</h2>
      <Show
        when={props.draft.seeding.groups.length}
        fallback={
          <p class="settings-intro">No groups configured. Without a group, nothing is enforced.</p>
        }
      >
        <div class="watch-entries">
          <For each={props.draft.seeding.groups}>
            {(group, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Group name"
                    hint="seeding.groups[].name · assigned via custom field"
                    error={props.errors[`seeding-${index()}`]}
                  >
                    <input
                      value={group.name}
                      placeholder="archive"
                      onInput={(event) =>
                        props.updateSeedingGroup(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Min ratio" hint="seed until at least this ratio · 0 = unset">
                    <input
                      type="number"
                      value={group.min_ratio || ""}
                      placeholder="0"
                      onInput={(event) =>
                        props.updateSeedingGroup(index(), {
                          min_ratio: Math.max(0, Number(event.currentTarget.value) || 0),
                        })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Max ratio" hint="hard ratio ceiling · 0 = unset">
                    <input
                      type="number"
                      value={group.max_ratio || ""}
                      placeholder="0"
                      onInput={(event) =>
                        props.updateSeedingGroup(index(), {
                          max_ratio: Math.max(0, Number(event.currentTarget.value) || 0),
                        })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Min upload" hint="bytes uploaded before acting · 0 = unset">
                    <input
                      type="number"
                      value={group.min_upload_bytes || ""}
                      placeholder="0"
                      onInput={(event) =>
                        props.updateSeedingGroup(index(), {
                          min_upload_bytes: Math.max(
                            0,
                            Math.floor(Number(event.currentTarget.value) || 0),
                          ),
                        })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Max seeding time" hint="since finished · empty = unset">
                    <input
                      value={group.max_seeding_time}
                      placeholder="72h"
                      onInput={(event) =>
                        props.updateSeedingGroup(index(), {
                          max_seeding_time: event.currentTarget.value,
                        })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Action" hint="runs once per torrent">
                    <select
                      value={group.action}
                      onChange={(event) =>
                        props.updateSeedingGroup(index(), {
                          action: event.currentTarget.value as SeedingGroupDraft["action"],
                        })
                      }
                    >
                      <option value="stop">Stop</option>
                      <option value="stop_and_set_label">Stop and set label</option>
                      <option value="erase">Erase from session</option>
                      <option value="erase_with_data">Erase with data</option>
                    </select>
                  </SettingRow>
                  <Show when={group.action === "stop_and_set_label"}>
                    <SettingRow label="Label" hint="d.custom1.set after stopping">
                      <input
                        value={group.label}
                        placeholder="done"
                        onInput={(event) =>
                          props.updateSeedingGroup(index(), { label: event.currentTarget.value })
                        }
                      />
                    </SettingRow>
                  </Show>
                </div>
                <div class="watch-entry-flags">
                  <button type="button" onClick={() => props.removeSeedingGroup(index())}>
                    Remove group
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addSeedingGroup()}>
        + Add group
      </button>
      <h2>Dry run</h2>
      <p class="settings-intro">
        Evaluates the draft groups above against the current session without saving. Each torrent is
        listed under the group and condition that would act on it now.
      </p>
      <button
        type="button"
        class="settings-add-row"
        disabled={testing() || !props.draft.seeding.groups.length}
        onClick={() => void testGroups()}
      >
        {testing() ? "Testing…" : "Preview enforcement"}
      </button>
      <Show when={result()}>
        <div class="automation-dryrun">
          <Show when={result()!.matches.length === 0}>
            <p class="settings-intro">
              No seeding torrents would be acted on. ({result()!.evaluated} evaluated.)
            </p>
          </Show>
          <For each={result()!.matches}>
            {(match) => (
              <div>
                <span>
                  {match.group} · {match.condition}
                </span>
                <b>{match.name}</b>
                <small>
                  {match.action} — {match.detail}
                </small>
              </div>
            )}
          </For>
        </div>
      </Show>
    </section>
  );
}
