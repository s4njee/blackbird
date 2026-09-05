// IP-filter store: the blocklist state (PAR-5.6) for Settings > Connection.
// Refreshes are silent GETs; only reloadNow POSTs, always from an explicit
// user action.

import { createSignal } from "solid-js";
import { showToast } from "./ui";

export interface IPFilterStatus {
  enabled: boolean;
  source: string; // file | url | ""
  path?: string;
  rules: number;
  lastLoad?: string; // RFC3339
  lastError?: string;
}

const [status, setStatus] = createSignal<IPFilterStatus | null>(null);
const [reloading, setReloading] = createSignal(false);

export { reloading, status };

export async function refreshIPFilter(): Promise<void> {
  try {
    const response = await fetch("/api/v1/ipfilter", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return;
    const body = (await response.json()) as IPFilterStatus;
    setStatus(body);
  } catch {
    /* leave the previous status intact */
  }
}

/** Re-fetches (URL sources) and re-loads the daemon table now. */
export async function reloadIPFilter(): Promise<void> {
  setReloading(true);
  try {
    const response = await fetch("/api/v1/ipfilter/reload", { method: "POST" });
    const body = (await response.json().catch(() => ({}))) as IPFilterStatus & {
      error?: { message?: string };
      message?: string;
    };
    if (!response.ok) {
      await refreshIPFilter();
      throw new Error(body.error?.message || body.message || "Blocklist reload failed");
    }
    setStatus(body);
    showToast(`Blocklist loaded: ${body.rules} rules.`);
  } catch (error) {
    showToast(error instanceof Error ? error.message : "Blocklist reload failed.");
  } finally {
    setReloading(false);
  }
}
