// Throttle store: the /api/throttles channel list (PAR-4.1) shared by the
// toolbar/menu assignment controls and the Settings live-usage display.
// Refreshes are silent: assignment and settings-save flows surface their own
// errors, and an empty list simply means no channels are configured.

import { createSignal } from "solid-js";

export interface ThrottleChannel {
  name: string;
  upKB: number;
  downKB: number;
  upRateBps: number;
  downRateBps: number;
  inUse: number;
}

const [channels, setChannels] = createSignal<ThrottleChannel[]>([]);

export { channels };

export async function refreshThrottles(): Promise<void> {
  try {
    const response = await fetch("/api/v1/throttles", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return;
    const body = (await response.json()) as { channels?: ThrottleChannel[] };
    setChannels(Array.isArray(body.channels) ? body.channels : []);
  } catch {
    /* leave the previous list intact */
  }
}
