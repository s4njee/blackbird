// Port-check store: the last user-initiated reachability verdict (PAR-5.5)
// for the status bar port item and Settings > Connection. Refreshes are
// silent GETs that never probe; only runPortCheck POSTs, always from an
// explicit user action.

import { createSignal } from "solid-js";
import { showToast } from "./ui";

export interface PortCheckResult {
  port: number;
  reachable: boolean;
  method: string;
  checkedAt: string; // RFC3339
}

const [enabled, setEnabled] = createSignal(false);
const [verdict, setVerdict] = createSignal<PortCheckResult | null>(null);
const [checking, setChecking] = createSignal(false);

export { checking, enabled, verdict };

export async function refreshPortCheck(): Promise<void> {
  try {
    const response = await fetch("/api/v1/port-check", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return;
    const body = (await response.json()) as { enabled?: boolean; result?: PortCheckResult };
    setEnabled(Boolean(body.enabled));
    setVerdict(body.result ?? null);
  } catch {
    /* leave the previous verdict intact */
  }
}

/** Runs one user-initiated probe and remembers the verdict. */
export async function runPortCheck(): Promise<void> {
  setChecking(true);
  try {
    const response = await fetch("/api/v1/port-check", { method: "POST" });
    const body = (await response.json().catch(() => ({}))) as {
      result?: PortCheckResult;
      error?: { message?: string };
      message?: string;
    };
    if (!response.ok) throw new Error(body.error?.message || body.message || "Port check failed");
    setEnabled(true);
    setVerdict(body.result ?? null);
    if (body.result) {
      showToast(
        body.result.reachable
          ? `Port ${body.result.port} is reachable.`
          : `Port ${body.result.port} is closed.`,
      );
    }
  } catch (error) {
    showToast(error instanceof Error ? error.message : "Port check failed.");
  } finally {
    setChecking(false);
  }
}
