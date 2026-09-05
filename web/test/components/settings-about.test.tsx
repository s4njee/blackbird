// @vitest-environment happy-dom
// About panel (POL-8.7) against the real SettingsPanel: versions render,
// fetch failures offer a retry, and the opt-in update check stays idle
// until enabled with an endpoint.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));

import { SettingsPanel } from "../../src/components/SettingsPanel";
import { setSettingsSection } from "../../src/store/ui.js";
import { setUpdateCheckEnabled, setUpdateCheckEndpoint } from "../../src/store/version.js";

function stubFetch(versionImpl: () => unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown) => {
      if (String(url) === "/api/v1/settings") {
        return { ok: true, json: async () => ({ tuning: {}, ui: {} }) };
      }
      if (String(url) === "/api/v1/version") return versionImpl();
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
}

const VERSION = {
  blackbird: { version: "1.2.3", commit: "abc123", buildDate: "2026-09-03" },
  rtorrent: { version: "0.16.18", library: "2.0" },
  connection: "connected",
  torrents: 4,
};

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
  setSettingsSection("Connection");
  setUpdateCheckEnabled(false);
  setUpdateCheckEndpoint("");
});

async function openAbout() {
  render(() => <SettingsPanel />);
  await screen.findByRole("button", { name: "About" });
  fireEvent.click(screen.getByRole("button", { name: "About" }));
}

describe("SettingsPanel About", () => {
  it("shows build and daemon versions", async () => {
    stubFetch(() => ({ ok: true, json: async () => VERSION }));
    await openAbout();
    await screen.findByText(/1\.2\.3/);
    await screen.findByText(/0\.16\.18/);
    expect(screen.queryByText(/Could not load version info/)).toBeNull();
  });

  it("offers a retry when the version fetch fails", async () => {
    let calls = 0;
    stubFetch(() => {
      calls += 1;
      if (calls === 1) return { ok: false, status: 500, json: async () => ({}) };
      return { ok: true, json: async () => VERSION };
    });
    await openAbout();
    await screen.findByText(/Could not load version info/);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByText(/1\.2\.3/);
  });

  it("keeps the release check idle until opted in", async () => {
    stubFetch(() => ({ ok: true, json: async () => VERSION }));
    await openAbout();
    await screen.findByText(/1\.2\.3/);
    // Off by default: no endpoint field, no network check.
    expect(screen.queryByPlaceholderText(/releases\/latest/)).toBeNull();
    fireEvent.click(screen.getByLabelText(/Enable release check/));
    await screen.findByPlaceholderText(/releases\/latest/);
    expect(screen.queryByText(/Up to date/)).toBeNull();
  });
});
