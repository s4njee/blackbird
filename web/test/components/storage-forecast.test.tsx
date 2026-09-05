import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import {
  StorageForecast,
  useStorageForecast,
  type StoragePlan,
} from "../../src/components/StorageForecast";
const fixture = (signature = "one"): StoragePlan => ({
  generatedAt: new Date().toISOString(),
  expiresAt: new Date(Date.now() + 30000).toISOString(),
  signature,
  status: "review",
  pools: [
    {
      id: "disk",
      paths: ["/data"],
      totalBytes: 1000,
      freeBytes: 500,
      reserveBytes: 0,
      additionalLowerBytes: 0,
      additionalUpperBytes: null,
      peakUsedBytes: null,
      freeAfterBytes: null,
      status: "unknown",
      peakCause: "Magnet metadata",
    },
  ],
  operations: [],
  unknown: [],
  coverage: ["Advisory, not a reservation."],
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("storage forecast", () => {
  it("refreshes before starting and requires review when the capacity verdict changes", async () => {
    let value = fixture();
    let starts = 0;
    const fetch = vi.fn(async () => new Response(JSON.stringify(value)));
    vi.stubGlobal("fetch", fetch);
    render(() => {
      const [destination, setDestination] = createSignal("/data");
      const m = useStorageForecast({ key: destination, body: () => new FormData() });
      return (
        <>
          <StorageForecast model={m} />
          <button
            onClick={() =>
              void m.ready().then((ok) => {
                if (ok) starts++;
              })
            }
          >
            Start operation
          </button>
          <button onClick={() => setDestination("/other")}>Change destination</button>
        </>
      );
    });
    await fireEvent.click(screen.getByRole("button", { name: "Start operation" }));
    await screen.findByText(/Review the refreshed forecast/);
    expect(starts).toBe(0);
    await fireEvent.click(screen.getByRole("button", { name: "Start operation" }));
    await waitFor(() => expect(starts).toBe(1));
    expect(fetch).toHaveBeenCalledTimes(2);
    value = fixture("risk-changed");
    await fireEvent.click(screen.getByRole("button", { name: "Start operation" }));
    await screen.findByText(/Review the refreshed forecast/);
    expect(starts).toBe(1);
    await fireEvent.click(screen.getByRole("button", { name: "Change destination" }));
    expect(screen.queryByText("Unknown demand")).toBeNull();
  });
  it("invalidates edited assumptions and does not approve failed inspections", async () => {
    let fail = false;
    let approved = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        fail
          ? new Response('{"error":{"message":"Inspection busy"}}', { status: 503 })
          : new Response(JSON.stringify(fixture())),
      ),
    );
    render(() => {
      const m = useStorageForecast({ key: () => "input", body: () => new FormData() });
      return (
        <>
          <StorageForecast model={m} />
          <button
            onClick={() =>
              void m.ready().then((ok) => {
                approved = ok;
              })
            }
          >
            Start operation
          </button>
        </>
      );
    });
    await fireEvent.click(screen.getByRole("button", { name: "Refresh forecast" }));
    await screen.findByText("Unknown demand");
    await fireEvent.input(screen.getByLabelText("Reserve per filesystem (GiB)"), {
      target: { value: "1" },
    });
    expect(screen.queryByText("Unknown demand")).toBeNull();
    fail = true;
    await fireEvent.click(screen.getByRole("button", { name: "Start operation" }));
    await screen.findByText("Inspection busy");
    expect(approved).toBe(false);
  });
});
