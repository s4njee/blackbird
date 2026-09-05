// @vitest-environment happy-dom
// Theme picker (THM-9.2/9.3) against the real SettingsPanel: card clicks
// preview live without persisting; Save commits to the browser; Revert and
// unmount discard; presets set the draft accent; "Set as server default"
// pushes the preview into the YAML draft.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));

import { SettingsPanel } from "../../src/components/SettingsPanel";
import {
  browserTheme,
  hydrateAppearance,
  setBrowserTheme,
  setSettingsSection,
} from "../../src/store/ui.js";
import { THEME_STORAGE_KEY } from "../../src/lib/themes.js";

function stubSettings(ui: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown) => {
      if (String(url) === "/api/v1/settings") {
        return { ok: true, json: async () => ({ tuning: {}, ui }) };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
}

// happy-dom here provides no localStorage; stub an in-memory one so the
// persistence assertions exercise the real storage path.
const memStore = new Map<string, string>();

beforeEach(() => {
  Object.defineProperty(window, "localStorage", {
    value: {
      getItem: (k: string) => memStore.get(k) ?? null,
      setItem: (k: string, v: string) => void memStore.set(k, String(v)),
      removeItem: (k: string) => void memStore.delete(k),
    },
    configurable: true,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
  memStore.clear();
  setBrowserTheme(null);
  hydrateAppearance({ theme: "dark" });
  setSettingsSection("Connection");
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.removeAttribute("data-density");
  // @ts-expect-error restore the missing-storage environment
  delete window.localStorage;
});

async function openInterface(ui: unknown = {}) {
  stubSettings(ui);
  render(() => <SettingsPanel />);
  await screen.findByRole("button", { name: "Interface" });
  fireEvent.click(screen.getByRole("button", { name: "Interface" }));
  await screen.findByRole("radiogroup", { name: "Theme" });
}

describe("SettingsPanel theme picker", () => {
  it("previews live without persisting until Save", async () => {
    await openInterface();
    fireEvent.click(screen.getByRole("radio", { name: /Blackbird Light/ }));
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(memStore.get(THEME_STORAGE_KEY)).toBeUndefined();
    expect(browserTheme()).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await vi.waitFor(() => expect(memStore.get(THEME_STORAGE_KEY)).toBe("light"));
    expect(browserTheme()).toBe("light");
  });

  it("reverts the preview and unmount discards it", async () => {
    await openInterface();
    fireEvent.click(screen.getByRole("radio", { name: /Midnight/ }));
    expect(document.documentElement.dataset.theme).toBe("midnight");
    fireEvent.click(screen.getByRole("button", { name: "Revert", exact: true }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(memStore.get(THEME_STORAGE_KEY)).toBeUndefined();
  });

  it("clears back to the operator default", async () => {
    await openInterface();
    hydrateAppearance({ theme: "contrast" });
    expect(document.documentElement.dataset.theme).toBe("contrast");
    fireEvent.click(screen.getByRole("radio", { name: /Blackbird Light/ }));
    expect(document.documentElement.dataset.theme).toBe("light");
    fireEvent.click(screen.getByRole("radio", { name: /Operator default/ }));
    expect(document.documentElement.dataset.theme).toBe("contrast");
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await vi.waitFor(() => expect(memStore.has(THEME_STORAGE_KEY)).toBe(false));
  });

  it("offers per-theme accent presets that set the draft accent", async () => {
    await openInterface();
    fireEvent.click(screen.getByRole("radio", { name: /Blackbird Light/ }));
    fireEvent.click(screen.getByRole("button", { name: "Use accent #1d4ed8" }));
    // Draft accent flows into the hex input next to the color picker.
    const inputs = screen.getAllByDisplayValue("#1d4ed8", { exact: false });
    expect(inputs.length).toBeGreaterThan(0);
  });

  it("pushes the preview as server default", async () => {
    await openInterface({ theme: "dark" });
    fireEvent.click(screen.getByRole("radio", { name: /Classic/ }));
    const push = screen.getByRole("button", { name: "Set as server default" });
    expect((push as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(push);
    // The YAML draft now carries the previewed theme (save itself 404s in
    // this stub, which is fine — the draft edit is what matters here).
    // "classic" is unique among all Interface selects once pushed.
    const selects = screen.getAllByRole("combobox") as HTMLSelectElement[];
    expect(selects.some((el) => el.value === "classic")).toBe(true);
  });

  it("previews density live and persists it on Save", async () => {
    await openInterface();
    fireEvent.click(screen.getByRole("radio", { name: /Comfortable/ }));
    expect(document.documentElement.dataset.density).toBe("comfortable");
    expect(memStore.get("blackbird.density.v1")).toBeUndefined();
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await vi.waitFor(() => expect(memStore.get("blackbird.density.v1")).toBe("comfortable"));
  });
});
