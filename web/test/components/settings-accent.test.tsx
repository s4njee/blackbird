// @vitest-environment happy-dom
// Accent boot/preview/revert (POL-8.4) against the real SettingsPanel:
// committed accents apply, draft edits preview live, Revert and unmount
// restore the committed value.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));

import { SettingsPanel } from "../../src/components/SettingsPanel";

const COMMITTED = "#112233";

function stubSettings() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown) => {
      if (String(url) === "/api/v1/settings") {
        return { ok: true, json: async () => ({ tuning: {}, ui: { accent: COMMITTED } }) };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
}

const accentVar = () => document.documentElement.style.getPropertyValue("--accent");

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
  document.documentElement.style.removeProperty("--accent");
});

describe("SettingsPanel accent", () => {
  it("previews draft accents live and restores the committed value", async () => {
    stubSettings();
    const rendered = render(() => <SettingsPanel />);
    await screen.findByRole("button", { name: "Interface" });
    // Load applies the committed accent through the draft preview effect.
    await waitFor(() => expect(accentVar()).toBe(COMMITTED));

    fireEvent.click(screen.getByRole("button", { name: "Interface" }));
    const [color] = screen.getAllByDisplayValue(COMMITTED, { exact: false });
    fireEvent.input(color, { target: { value: "#ff0000" } });
    await waitFor(() => expect(accentVar()).toBe("#ff0000"));

    // Revert restores the committed accent without saving.
    fireEvent.click(screen.getByRole("button", { name: "Revert" }));
    await waitFor(() => expect(accentVar()).toBe(COMMITTED));

    // Navigation away (unmount) with a dirty draft also restores it.
    fireEvent.input(screen.getAllByDisplayValue(COMMITTED, { exact: false })[0], {
      target: { value: "#00ff00" },
    });
    await waitFor(() => expect(accentVar()).toBe("#00ff00"));
    rendered.unmount();
    expect(accentVar()).toBe(COMMITTED);
  });

  // An empty accent is the shipped default and is explicitly valid
  // server-side ("follow the theme"). Rejecting it in the client made Save
  // a no-op — silently, since a failed validation shows no toast — so a
  // default install, or anyone clicking "Use theme default accent", could
  // never save any setting again.
  it("saves when the accent is cleared to the theme default", async () => {
    const posts: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: unknown, init?: { method?: string }) => {
        const u = String(url);
        if (init?.method === "POST") {
          posts.push(u);
          return { ok: true, json: async () => ({ results: [], saved: true }) };
        }
        if (u === "/api/v1/settings") {
          return { ok: true, json: async () => ({ tuning: {}, ui: { accent: COMMITTED } }) };
        }
        return { ok: false, status: 404, json: async () => ({}) };
      }),
    );

    render(() => <SettingsPanel />);
    fireEvent.click(await screen.findByRole("button", { name: "Interface" }));
    fireEvent.click(await screen.findByRole("button", { name: "Use theme default accent" }));

    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await waitFor(() => expect(posts.some((u) => u.includes("/settings"))).toBe(true));
  });

  it("renders live session content in General instead of prose", async () => {
    stubSettings();
    render(() => <SettingsPanel />);
    fireEvent.click(await screen.findByRole("button", { name: "General" }));
    expect(await screen.findByText("Connected · port —")).toBeTruthy();
    expect(screen.getByText(/rTorrent .* \/ libtorrent/)).toBeTruthy();
  });
});
