import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@solidjs/testing-library";
import { PreservationView } from "../../src/components/PreservationView";
vi.mock("../../src/store/session", () => ({ torrentList: () => [] }));
vi.mock("../../src/store/ui", () => ({
  navigate: vi.fn(),
  selectedHashes: () => [],
  setFocusedHash: vi.fn(),
  setQuery: vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("shows unknown evidence and preserves server pins when an edit conflicts", async () => {
  const watch = {
    hash: "A".repeat(40),
    name: "Archive",
    revision: 2,
    pinned: true,
    reason: "Keep",
    reviewDate: "",
    reviewDue: false,
    band: "unknown",
    evidence: "No eligible active observations.",
    coverage: 0,
    trackers: [],
    trackersOmitted: 0,
    latest: {
      at: "2026-09-03T12:00:00Z",
      seeds: null,
      complete: null,
      status: "disconnected or stale",
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, options) =>
      options?.method === "POST"
        ? new Response(
            JSON.stringify({ error: { message: "watch changed; refresh before saving" } }),
            { status: 409 },
          )
        : new Response(
            JSON.stringify({ watches: [watch], error: "", coverage: "Cached observations only." }),
          ),
    ),
  );
  render(() => <PreservationView />);
  await screen.findByText("Insufficient current evidence");
  expect(screen.getByText(/Connected seeds: Unknown/)).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Stop watching" }) as HTMLButtonElement).disabled,
  ).toBe(true);
  await fireEvent.click(screen.getByLabelText("Protect from cleanup"));
  await fireEvent.click(screen.getByRole("button", { name: "Save pin" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("watch changed");
  expect(
    (screen.getByRole("button", { name: "Stop watching" }) as HTMLButtonElement).disabled,
  ).toBe(true);
});
