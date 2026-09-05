// @vitest-environment happy-dom
// Settings split (POL-8.8): every section renders its heading through the
// real shell + dispatcher, proving the one-file-per-section move kept all
// sections wired.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));

import { SettingsPanel } from "../../src/components/SettingsPanel";
import { SETTINGS_SECTIONS, setSettingsSection } from "../../src/store/ui.js";

function stubSettings() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown) => {
      if (String(url) === "/api/v1/settings") {
        return { ok: true, json: async () => ({ tuning: {}, ui: {} }) };
      }
      if (String(url) === "/api/v1/version") {
        return {
          ok: true,
          json: async () => ({
            blackbird: { version: "1.0.0", commit: "c", buildDate: "d" },
            rtorrent: { version: "", library: "" },
            api: { version: "v1" },
            ws: { min: 1, current: 2 },
            connection: "",
            torrents: 0,
          }),
        };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
  setSettingsSection("Connection");
});

describe("settings split", () => {
  it.each(SETTINGS_SECTIONS)("renders the %s section", async (section) => {
    stubSettings();
    render(() => <SettingsPanel />);
    await screen.findByRole("button", { name: new RegExp(section) });
    setSettingsSection(section);
    // Each section renders an h1 (Advanced's h1) or its content heading.
    const headings = await screen.findAllByRole("heading", { level: 1 });
    expect(headings.length).toBeGreaterThan(0);
  });
});
