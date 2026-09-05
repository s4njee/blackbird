// Global speed-limit popover (PAR-4.4): presets and custom limits applied
// immediately, with the current limit beside the live rate, scheduler
// override awareness, and an explicit save-as-default.

import { For, Show, createSignal, onCleanup, onMount } from "solid-js";
import { formatRate } from "../lib/format";
import {
  applyBandwidth,
  bandwidth,
  refreshBandwidth,
  saveBandwidthDefault,
} from "../store/bandwidth";
import { refreshSchedule, scheduleStatus } from "../store/schedule";
import { globalStats } from "../store/session";
import { showToast } from "../store/ui";

function limitText(kb: number): string {
  return kb > 0 ? `${kb.toLocaleString()} KB/s` : "unlimited";
}

export function LimitsPopover(props: { x: number; onClose: () => void }) {
  const [down, setDown] = createSignal("");
  const [up, setUp] = createSignal("");
  const [defDown, setDefDown] = createSignal(0);
  const [defUp, setDefUp] = createSignal(0);
  const [busy, setBusy] = createSignal(false);

  onMount(() => {
    void refreshBandwidth().then(() => {
      setDown(String(bandwidth().downKb));
      setUp(String(bandwidth().upKb));
    });
    void refreshSchedule();
    void fetch("/api/v1/settings", { headers: { Accept: "application/json" } })
      .then((response) => (response.ok ? response.json() : null))
      .then(
        (
          settings: null | {
            tuning?: { global_down_rate_kb?: unknown; global_up_rate_kb?: unknown };
          },
        ) => {
          if (!settings?.tuning) return;
          setDefDown(Number(settings.tuning.global_down_rate_kb) || 0);
          setDefUp(Number(settings.tuning.global_up_rate_kb) || 0);
        },
      )
      .catch(() => {
        /* defaults stay zero; presets fall back to current */
      });
    const close = (event: Event) => {
      if (
        !(event.target as HTMLElement).closest(".limits-popover") &&
        !(event.target as HTMLElement).closest(".sb-rates")
      )
        props.onClose();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") props.onClose();
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", key);
    onCleanup(() => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", key);
    });
  });

  // Preset base sticks to the saved default so repeated percentages never
  // compound; without a default it snapshots the daemon limit at open.
  const baseDown = () =>
    defDown() > 0 ? defDown() : bandwidth().downKb > 0 ? bandwidth().downKb : 0;
  const baseUp = () => (defUp() > 0 ? defUp() : bandwidth().upKb > 0 ? bandwidth().upKb : 0);
  const baseKindDown = () => (defDown() > 0 ? "default" : "current");
  const baseKindUp = () => (defUp() > 0 ? "default" : "current");

  async function apply(downKb: number, upKb: number) {
    if (downKb < 0 || upKb < 0) return;
    setBusy(true);
    try {
      await applyBandwidth(downKb, upKb);
      setDown(String(downKb));
      setUp(String(upKb));
      showToast("Limits applied.");
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not apply limits");
    } finally {
      setBusy(false);
    }
  }

  async function saveDefault() {
    setBusy(true);
    try {
      await saveBandwidthDefault(Number(down()) || 0, Number(up()) || 0);
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not save defaults");
    } finally {
      setBusy(false);
    }
  }

  const preset = (dir: "down" | "up", frac: number | null) => {
    const base = dir === "down" ? baseDown() : baseUp();
    const kb = frac === null ? 0 : Math.round(base * frac);
    if (dir === "down") void apply(kb, Number(up()) || 0);
    else void apply(Number(down()) || 0, kb);
  };

  const liveDown = () => globalStats()?.downRate ?? 0;
  const liveUp = () => globalStats()?.upRate ?? 0;
  const left = () => Math.max(6, Math.min(props.x - 150, window.innerWidth - 326));

  return (
    <div
      class="limits-popover"
      role="dialog"
      aria-label="Global speed limits"
      style={{ left: `${left()}px` }}
    >
      <div class="limits-title">
        <b>Global limits</b>
        <button type="button" aria-label="Close" onClick={props.onClose}>
          ×
        </button>
      </div>
      <div class="limits-row">
        <span>↓ Down</span>
        <input
          type="number"
          min="0"
          value={down()}
          aria-label="Download limit KB/s"
          onInput={(event) => setDown(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void apply(Number(down()) || 0, Number(up()) || 0);
          }}
        />
        <small>KB/s · 0 = unlimited</small>
      </div>
      <div class="limits-live tnum">
        live {formatRate(liveDown())} · limit{" "}
        {bandwidth().downKb > 0 ? formatRate(bandwidth().downKb * 1024) : "unlimited"}
      </div>
      <div class="limits-presets">
        <button type="button" disabled={busy()} onClick={() => preset("down", null)}>
          Unlimited
        </button>
        <For each={[0.25, 0.5, 0.75]}>
          {(frac) => (
            <button
              type="button"
              disabled={busy() || baseDown() <= 0}
              title={
                baseDown() > 0
                  ? `${frac * 100}% of ${baseKindDown()} ${baseDown().toLocaleString()} KB/s`
                  : "No base limit to take a percentage of"
              }
              onClick={() => preset("down", frac)}
            >
              {frac * 100}%
            </button>
          )}
        </For>
      </div>
      <div class="limits-row">
        <span>↑ Up</span>
        <input
          type="number"
          min="0"
          value={up()}
          aria-label="Upload limit KB/s"
          onInput={(event) => setUp(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void apply(Number(down()) || 0, Number(up()) || 0);
          }}
        />
        <small>KB/s · 0 = unlimited</small>
      </div>
      <div class="limits-live tnum">
        live {formatRate(liveUp())} · limit{" "}
        {bandwidth().upKb > 0 ? formatRate(bandwidth().upKb * 1024) : "unlimited"}
      </div>
      <div class="limits-presets">
        <button type="button" disabled={busy()} onClick={() => preset("up", null)}>
          Unlimited
        </button>
        <For each={[0.25, 0.5, 0.75]}>
          {(frac) => (
            <button
              type="button"
              disabled={busy() || baseUp() <= 0}
              title={
                baseUp() > 0
                  ? `${frac * 100}% of ${baseKindUp()} ${baseUp().toLocaleString()} KB/s`
                  : "No base limit to take a percentage of"
              }
              onClick={() => preset("up", frac)}
            >
              {frac * 100}%
            </button>
          )}
        </For>
      </div>
      <Show when={scheduleStatus()?.overridden}>
        <p class="limits-note">
          Scheduler override active until {scheduleStatus()!.overrideUntil} — changes update the
          override.
        </p>
      </Show>
      <Show when={!scheduleStatus()?.overridden && scheduleStatus()?.activeProfile}>
        <p class="limits-note">
          Scheduler profile “{scheduleStatus()!.activeProfile}” will re-apply on the next change.
        </p>
      </Show>
      <div class="limits-actions">
        <button
          type="button"
          disabled={busy()}
          onClick={() => void apply(Number(down()) || 0, Number(up()) || 0)}
        >
          Apply
        </button>
        <button
          type="button"
          disabled={busy()}
          title="Persist the values above as tuning.global_*_rate_kb"
          onClick={() => void saveDefault()}
        >
          Save as default
        </button>
      </div>
      <p class="limits-hint">
        Presets apply immediately
        {baseDown() > 0 || baseUp() > 0
          ? ` · ${limitText(baseDown())} / ${limitText(baseUp())} base`
          : ""}
        . Only “Save as default” writes YAML.
      </p>
    </div>
  );
}
