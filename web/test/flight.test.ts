import { describe, expect, it } from "vitest";
import { observedAt, type FlightEvent } from "../src/lib/flight";

const event = (phase: string, id: string, after?: Record<string, string>): FlightEvent => ({
  phase,
  id,
  seq: Number(id),
  at: "2026-09-03T00:00:00Z",
  hash: "abc",
  after,
});

describe("flight replay", () => {
  it("does not apply intended or acknowledged changes to observed state", () => {
    const events = [
      event("checkpoint", "1", { state: "stopped" }),
      event("intent", "2", { state: "downloading" }),
      event("rpc_result", "3", { state: "downloading" }),
    ];
    expect(observedAt(events, 2, "abc")?.after?.state).toBe("stopped");
    events.push(event("observation", "4", { state: "downloading" }));
    expect(observedAt(events, 3, "abc")?.after?.state).toBe("downloading");
    expect(observedAt(events, 3, "other")).toBeUndefined();
  });
  it("invalidates observations across gaps until a new sample arrives", () => {
    const events = [
      event("checkpoint", "1", { state: "downloading" }),
      event("gap", "2"),
      event("rpc_result", "3"),
    ];
    expect(observedAt(events, 2, "abc")).toBeUndefined();
    events.push(event("checkpoint", "4", { state: "stopped" }));
    expect(observedAt(events, 3, "abc")?.id).toBe("4");
    expect(observedAt(events, 0, "abc")?.id).toBe("1");
  });
});
