export interface FlightEvent {
  id: string;
  seq: number;
  at: string;
  hash: string;
  phase: string;
  actor?: string;
  action?: string;
  result?: string;
  name?: string;
  message?: string;
  causeId?: string;
  revision?: string;
  before?: Record<string, string>;
  after?: Record<string, string>;
}

export interface Recording {
  version: number;
  events: FlightEvent[];
  status: {
    enabled: boolean;
    error?: string;
    dropped: number;
    pruned: number;
    pending: number;
    durableThrough: number;
    lastPersistedAt: string | null;
    maxBytes: number;
    retentionSeconds: number;
  };
  coverage: string[];
}

// Only sampled observations establish state. Any intervening coverage gap
// invalidates it until another complete sample arrives. Intents/results are
// never applied to the replay as if they were successful state transitions.
export function observedAt(
  events: FlightEvent[],
  index: number,
  hash: string,
): FlightEvent | undefined {
  let latest: FlightEvent | undefined;
  for (const e of events.slice(0, index + 1)) {
    if (e.phase === "gap") latest = undefined;
    else if (
      hash &&
      e.hash === hash &&
      (e.phase === "checkpoint" || e.phase === "observation") &&
      e.after?.state
    )
      latest = e;
  }
  return latest;
}
