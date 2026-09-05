import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { AttentionView, type Inbox } from "../../src/components/AttentionView";
vi.mock("../../src/components/WhyTab", () => ({
  WhyTab: (p: { hash: string }) => <div>Explanation for {p.hash}</div>,
}));
vi.mock("../../src/components/FlightRecorder", () => ({
  FlightRecorder: (p: { hash: string; from?: string }) => (
    <div>
      Recording for {p.hash || "session"} from {p.from}
    </div>
  ),
}));
const fixture = (): Inbox => ({
  state: {
    incidents: [
      {
        id: "one",
        kind: "tracker",
        title: "Tracker messages · tracker.example",
        evidence: "Shared symptoms, not a proven cause.",
        nextStep: "Inspect affected torrents.",
        hashes: ["abc"],
        affected: 100,
        firstSeen: "2026-09-03T11:00:00Z",
        lastSeen: "2026-09-03T11:01:00Z",
        episodeStarted: "2026-09-03T11:00:00Z",
        episode: 1,
        active: true,
        status: "open",
      },
    ],
    lastVisit: null,
    observedAt: "2026-09-03T11:01:00Z",
    savedAt: "2026-09-03T11:01:00Z",
    omitted: 0,
    pruned: 0,
    coverage: ["Bounded recording."],
  },
  since: "2026-09-02T11:00:00Z",
  generatedAt: "2026-09-03T11:02:00Z",
  completedCount: 1,
  completed: [{ id: "done", hash: "abc", action: "complete", at: "2026-09-03T10:00:00Z" }],
  summaryCoverage: "Retained outcomes only.",
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("AttentionView", () => {
  it("shows the return summary, saves visit once, persists acknowledgement, and opens evidence", async () => {
    const data = fixture();
    const actions: Record<string, unknown>[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === "POST") {
          const body = JSON.parse(String(init.body));
          actions.push(body);
          if (body.action === "acknowledge") data.state.incidents[0].status = "acknowledged";
          return new Response('{"ok":true}');
        }
        return new Response(JSON.stringify(data));
      }),
    );
    render(() => <AttentionView />);
    await screen.findByText(/100 affected torrents/);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Acknowledge" })).not.toHaveProperty(
        "disabled",
        true,
      ),
    );
    expect(screen.getByLabelText("Since your last visit").textContent).toContain(
      "1 important completed actions",
    );
    await fireEvent.click(screen.getByRole("button", { name: "Acknowledge" }));
    await screen.findByText("acknowledged", { selector: "span" });
    expect(actions.filter((a) => a.action === "visit")).toHaveLength(1);
    expect(actions.find((a) => a.action === "acknowledge")).toMatchObject({
      id: "one",
      episode: 1,
    });
    await fireEvent.click(screen.getByText("Affected torrents (1 listed)"));
    await fireEvent.click(screen.getByRole("button", { name: "Why?" }));
    expect(screen.getByText("Explanation for abc")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Recording", exact: true }));
    expect(screen.getByText(/Recording for abc from/)).toBeTruthy();
  });
  it("surfaces failed writes without pretending the incident was acknowledged", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === "POST") {
          const body = JSON.parse(String(init.body));
          if (body.action === "acknowledge")
            return new Response('{"error":{"message":"Change not saved"}}', { status: 503 });
          return new Response('{"ok":true}');
        }
        return new Response(JSON.stringify(fixture()));
      }),
    );
    render(() => <AttentionView />);
    await screen.findByText(/100 affected torrents/);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Acknowledge" })).not.toHaveProperty(
        "disabled",
        true,
      ),
    );
    await fireEvent.click(screen.getByRole("button", { name: "Acknowledge" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Change not saved");
    expect(screen.getByText("open", { selector: "span" })).toBeTruthy();
  });
});
