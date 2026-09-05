// Seeding store: the /api/seeding policy (PAR-4.2) shared by the ratio-group
// context submenu and the Settings editor. Refreshes are silent.

import { createSignal } from "solid-js";

export interface SeedingGroupInfo {
  name: string;
}

export interface SeedingPolicy {
  customSlot: string;
  groups: SeedingGroupInfo[];
}

const [policy, setPolicy] = createSignal<SeedingPolicy>({ customSlot: "custom2", groups: [] });

export { policy };

export async function refreshSeeding(): Promise<void> {
  try {
    const response = await fetch("/api/v1/seeding", { headers: { Accept: "application/json" } });
    if (!response.ok) return;
    const body = (await response.json()) as {
      customSlot?: string;
      groups?: Array<{ name?: string }>;
    };
    setPolicy({
      customSlot:
        typeof body.customSlot === "string" && body.customSlot ? body.customSlot : "custom2",
      groups: Array.isArray(body.groups)
        ? body.groups.flatMap((g) =>
            g && typeof g.name === "string" && g.name ? [{ name: g.name }] : [],
          )
        : [],
    });
  } catch {
    /* leave the previous policy intact */
  }
}
