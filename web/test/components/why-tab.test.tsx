import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import type { TorrentExplanation } from "../../src/lib/types";

const controls = vi.hoisted(() => ({ navigate: vi.fn(), setDetailTab: vi.fn() }));
vi.mock("../../src/store/ui", () => ({
  ...controls,
  DETAIL_TABS: ["files", "speed", "logger", "trackers", "why"],
}));
vi.mock("../../src/store/session", () => ({ connected: () => true }));
vi.mock("../../src/store/ticker", () => ({
  tickerTick: () => 0,
  tickerNow: () => Date.now(),
  isTabHidden: () => false,
}));

import { WhyTab } from "../../src/components/WhyTab";

const fixture = (hash = "abc"): TorrentExplanation => ({
  hash,
  name: `Archive ${hash}`,
  generatedAt: new Date().toISOString(),
  observedAt: new Date().toISOString(),
  stale: false,
  staleAfterSeconds: 90,
  findings: [
    {
      id: "seeding",
      kind: "constraint",
      title: "Seeding condition is met: archive",
      summary: "A match is not evidence that the rule ran or caused a stop.",
      evidence: [{ source: "Saved seeding policy", value: "ratio 2.5 >= 2", observedAt: null }],
      target: { kind: "settings", name: "Seeding", label: "Review seeding group archive" },
    },
    {
      id: "stop-cause",
      kind: "unknown",
      title: "Why it stopped is not established",
      summary: "An external change may be unrecorded.",
      evidence: [
        { source: "Session poll", value: "state=stopped", observedAt: new Date().toISOString() },
      ],
      target: { kind: "tab", name: "logger", label: "Inspect torrent log" },
    },
  ],
  coverage: ["History is bounded and in memory."],
});

beforeEach(() => vi.clearAllMocks());
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("WhyTab", () => {
  it("separates evidence, uncertainty and controls; navigation never mutates a torrent", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(fixture())));
    vi.stubGlobal("fetch", fetch);
    render(() => <WhyTab hash="abc" />);
    await screen.findByText("Why it stopped is not established");
    expect(screen.getByText("Current control")).toBeTruthy();
    expect(screen.getByText("Unknown")).toBeTruthy();
    expect(screen.getByText(/Observation time unavailable/)).toBeTruthy();
    expect(screen.getByText("History is bounded and in memory.")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Review seeding group archive" }));
    expect(controls.navigate).toHaveBeenCalledWith("settings", "Seeding");
    await fireEvent.click(screen.getByRole("button", { name: "Inspect torrent log" }));
    expect(controls.setDetailTab).toHaveBeenCalledWith("logger");
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/torrents/abc?view=explanation");
    expect(fetch.mock.calls[0][1].method).toBeUndefined();
  });

  it("marks stale evidence and retains it explicitly after a failed refresh", async () => {
    const value = { ...fixture(), stale: true };
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(value)))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { message: "temporarily unavailable" } }), {
          status: 503,
        }),
      );
    vi.stubGlobal("fetch", fetch);
    render(() => <WhyTab hash="abc" />);
    await screen.findByText(/Stale or disconnected/);
    await fireEvent.click(screen.getByRole("button", { name: "Refresh explanations" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert").textContent).toContain("Earlier evidence is still displayed");
    expect(screen.getByText("Why it stopped is not established")).toBeTruthy();
  });

  it("shows a recoverable initial error", async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(new Response(JSON.stringify(fixture())));
    vi.stubGlobal("fetch", fetch);
    render(() => <WhyTab hash="abc" />);
    await screen.findByText("Explanations unavailable.");
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByText("Why it stopped is not established");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("aborts old focus requests and ignores late responses", async () => {
    let resolveOld!: (value: Response) => void;
    const fetch = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValueOnce(new Response(JSON.stringify(fixture("new"))));
    vi.stubGlobal("fetch", fetch);
    const [hash, setHash] = createSignal("old");
    const view = render(() => <WhyTab hash={hash()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    setHash("new");
    await screen.findByText("Archive new");
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    resolveOld(new Response(JSON.stringify(fixture("old"))));
    await waitFor(() => expect(screen.queryByText("Archive old")).toBeNull());
    view.unmount();
    expect(fetch.mock.calls[1][1].signal.aborted).toBe(true);
  });
});
