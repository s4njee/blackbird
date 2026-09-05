import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@solidjs/testing-library";
import { FlightRecorder } from "../../src/components/FlightRecorder";
import type { Recording } from "../../src/lib/flight";

const fixture: Recording = {
  version: 1,
  status: {
    enabled: true,
    dropped: 2,
    pruned: 1,
    pending: 0,
    durableThrough: 3,
    lastPersistedAt: "2026-09-03T12:00:00Z",
    maxBytes: 10000,
    retentionSeconds: 3600,
  },
  coverage: ["Coverage is bounded."],
  events: [
    {
      id: "a",
      seq: 1,
      at: "2026-09-03T11:00:00Z",
      hash: "abc",
      phase: "checkpoint",
      action: "checkpoint",
      after: { state: "stopped" },
    },
    {
      id: "b",
      seq: 2,
      at: "2026-09-03T11:01:00Z",
      hash: "abc",
      phase: "intent",
      action: "start",
      after: { state: "downloading" },
    },
    {
      id: "c",
      seq: 3,
      at: "2026-09-03T11:02:00.123456789Z",
      hash: "abc",
      phase: "rpc_result",
      action: "start",
      result: "ok",
      causeId: "b",
    },
  ],
};
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("FlightRecorder", () => {
  it("scrubs evidence, follows only explicit causal links, and previews before download", async () => {
    const fetch = vi
      .fn()
      .mockImplementation(() => Promise.resolve(new Response(JSON.stringify(fixture))));
    vi.stubGlobal("fetch", fetch);
    render(() => <FlightRecorder hash="abc" />);
    await screen.findByRole("button", { name: "Inspect linked intent" });
    expect(screen.getByText(/stopped · sampled/)).toBeTruthy();
    expect(screen.getByText(/2 dropped/)).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Inspect linked intent" }));
    expect((screen.getByRole("slider") as HTMLInputElement).value).toBe("1");
    expect(screen.queryByRole("button", { name: "Download reviewed bundle" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Preview incident export" }));
    await screen.findByRole("button", { name: "Download reviewed bundle" });
    expect(fetch.mock.calls[1][0]).toContain("export=1");
    const query = new URL(fetch.mock.calls[1][0], "http://localhost").searchParams;
    expect(query.get("to")).toBe("2026-09-03T11:02:00.124Z");
    expect(screen.getByLabelText("Incident export preview").textContent).toContain('"version": 1');
  });
  it("shows missing recorder errors with retry", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({ error: { message: "flight recorder is not configured" } }),
            { status: 503 },
          ),
        ),
    );
    render(() => <FlightRecorder />);
    await screen.findByRole("alert");
    expect(screen.getByRole("alert").textContent).toContain("not configured");
  });
});
