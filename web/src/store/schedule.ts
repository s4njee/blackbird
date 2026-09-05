// Schedule store: the /api/schedule status (PAR-4.3) for the status bar
// chip and the Scheduler settings section. Refreshes are silent.

import { createSignal } from "solid-js";
import { showToast } from "./ui";

export interface ScheduleStatus {
  activeProfile: string;
  overridden: boolean;
  overrideUntil?: string;
  overrideDownKb?: number;
  overrideUpKb?: number;
  timezone: string;
  nextProfile?: string;
  nextChange?: string;
}

const [scheduleStatus, setScheduleStatus] = createSignal<ScheduleStatus | null>(null);

export { scheduleStatus };

export async function refreshSchedule(): Promise<void> {
  try {
    const response = await fetch("/api/v1/schedule", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return;
    setScheduleStatus((await response.json()) as ScheduleStatus);
  } catch {
    /* leave the previous status intact */
  }
}

export async function setScheduleOverride(
  minutes: number,
  downKb: number,
  upKb: number,
): Promise<void> {
  const response = await fetch("/api/v1/schedule/override", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ minutes, downKb, upKb }),
  });
  const body = (await response.json().catch(() => ({}))) as { error?: { message?: string } };
  if (!response.ok) throw new Error(body.error?.message || "Could not set override");
  await refreshSchedule();
}

export async function clearScheduleOverride(): Promise<void> {
  try {
    await fetch("/api/v1/schedule/override", { method: "DELETE" });
    await refreshSchedule();
  } catch (error) {
    showToast(error instanceof Error ? error.message : "Could not clear override");
  }
}
