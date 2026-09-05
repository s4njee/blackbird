// Bandwidth store: the /api/bandwidth global limits (PAR-4.4) for the
// status bar rates item and the limits popover. Refreshes are silent;
// actions surface their own errors.

import { createSignal } from "solid-js";
import { showToast } from "./ui";

export interface Bandwidth {
  downKb: number;
  upKb: number;
  downRateBps: number;
  upRateBps: number;
}

const [bandwidth, setBandwidth] = createSignal<Bandwidth>({
  downKb: 0,
  upKb: 0,
  downRateBps: 0,
  upRateBps: 0,
});

export { bandwidth };

export async function refreshBandwidth(): Promise<void> {
  try {
    const response = await fetch("/api/v1/bandwidth", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return;
    const body = (await response.json()) as Partial<Bandwidth>;
    setBandwidth({
      downKb: Number(body.downKb) || 0,
      upKb: Number(body.upKb) || 0,
      downRateBps: Number(body.downRateBps) || 0,
      upRateBps: Number(body.upRateBps) || 0,
    });
  } catch {
    /* leave the previous values intact */
  }
}

async function throwApiError(response: Response, fallback: string): Promise<never> {
  const body = (await response.json().catch(() => ({}))) as {
    error?: { message?: string };
    message?: string;
  };
  throw new Error(body.error?.message || body.message || fallback);
}

/** Applies limits to the daemon immediately (no YAML change). */
export async function applyBandwidth(downKb: number, upKb: number): Promise<void> {
  const response = await fetch("/api/v1/bandwidth", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ downKb, upKb }),
  });
  if (!response.ok) await throwApiError(response, "Could not apply limits");
  await refreshBandwidth();
}

interface TuningPatch {
  tuning: Record<string, unknown>;
}

/** Persists limits as the YAML defaults via the settings API (full tuning
 * round-trip so no other key is lost), applying them first. */
export async function saveBandwidthDefault(downKb: number, upKb: number): Promise<void> {
  await applyBandwidth(downKb, upKb);
  const loaded = await fetch("/api/v1/settings", { headers: { Accept: "application/json" } });
  if (!loaded.ok) throw new Error("Could not load settings");
  const settings = (await loaded.json()) as {
    tuning?: Record<string, unknown>;
    error?: { message?: string };
  };
  const tuning: Record<string, unknown> = { ...(settings.tuning ?? {}) };
  tuning.global_down_rate_kb = downKb;
  tuning.global_up_rate_kb = upKb;
  const saved = await fetch("/api/v1/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tuning } as TuningPatch),
  });
  const body = (await saved.json().catch(() => ({}))) as {
    saved?: boolean;
    error?: string | { message?: string };
    results?: Array<{ key?: string; error?: string }>;
  };
  if (!saved.ok) {
    const message = typeof body.error === "string" ? body.error : body.error?.message;
    throw new Error(message || "Could not save defaults");
  }
  if (!body.saved) throw new Error("Settings were not persisted");
  const failed = (body.results ?? []).filter((item) => item.error);
  if (failed.length) throw new Error(failed.map((item) => `${item.key}: ${item.error}`).join("; "));
  showToast("Saved as default limits.");
}
