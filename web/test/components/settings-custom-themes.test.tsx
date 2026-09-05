// @vitest-environment happy-dom
// Custom themes picker (THM-9.4) against the real SettingsPanel: server
// list with cards + warnings, apply/save flow, import validation, delete,
// and custom.css status — all against stubbed API routes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  connected: () => true,
  globalStats: () => null,
  torrentList: () => [],
}));
vi.mock("../../src/store/dialog.js", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../src/store/dialog.js")>();
  return { ...mod, confirmDialog: async () => true };
});

import { SettingsPanel } from "../../src/components/SettingsPanel";
import { hydrateAppearance, setBrowserTheme, setSettingsSection } from "../../src/store/ui.js";
import { discardCustomPreview, setActiveCustom } from "../../src/store/custom.js";

const HARBOR = {
  name: "Harbor",
  description: "Cool dark blue",
  extends: "dark",
  dark: true,
  accents: ["#4c5fd5", "#f59e0b", "#e5484d", "#2f9dff", "#3fb950"],
  palette: { "bg-app": "#0d1117" },
};

const LOW = {
  name: "Washed",
  extends: "dark",
  dark: true,
  accents: ["#4c5fd5", "#f59e0b", "#e5484d", "#2f9dff", "#3fb950"],
  palette: { "bg-app": "#101214", "text-body": "#121417" },
};

let themes: unknown[] = [HARBOR, LOW];
let themeErrors: string[] = ["bad.yml:3: version must be 1"];
let cssState: "absent" | "ready" = "absent";

function stubApi() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: unknown, init?: RequestInit) => {
      const u = String(url);
      if (u === "/api/v1/settings") {
        return { ok: true, json: async () => ({ tuning: {}, ui: {} }) };
      }
      if (u === "/api/v1/themes" && (!init || init.method === undefined || init.method === "GET")) {
        return { ok: true, json: async () => ({ themes, errors: themeErrors }) };
      }
      if (u === "/api/v1/themes/import" && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { content?: string };
        if (!body.content?.includes("name:")) {
          return {
            ok: false,
            status: 400,
            json: async () => ({ error: { message: "import.yml:1: missing name" } }),
          };
        }
        themes = [...themes, HARBOR];
        return { ok: true, json: async () => ({ theme: HARBOR }) };
      }
      if (u.startsWith("/api/v1/themes/") && init?.method === "DELETE") {
        themes = themes.filter(
          (t) => (t as { name: string }).name !== decodeURIComponent(u.split("/").pop()!),
        );
        return { ok: true, json: async () => ({ ok: true }) };
      }
      if (u === "/api/v1/custom-css") {
        if (cssState === "absent") return { ok: true, status: 200, text: async () => "" };
        return { ok: true, text: async () => ".x{color:red}" };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
}

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
  themes = [HARBOR, LOW];
  themeErrors = ["bad.yml:3: version must be 1"];
  cssState = "absent";
  discardCustomPreview();
  setActiveCustom(null);
  setBrowserTheme(null);
  hydrateAppearance({ theme: "dark" });
  setSettingsSection("Connection");
  document.documentElement.removeAttribute("data-theme");
  document.getElementById("blackbird-custom-theme")?.remove();
  document.getElementById("blackbird-custom-css")?.remove();
  // @ts-expect-error restore the missing-storage environment
  delete window.localStorage;
});

async function openInterface() {
  stubApi();
  const { refreshCustomThemes, refreshCustomCss } = await import("../../src/store/custom.js");
  await refreshCustomThemes();
  await refreshCustomCss();
  render(() => <SettingsPanel />);
  await screen.findByRole("button", { name: "Interface" });
  fireEvent.click(screen.getByRole("button", { name: "Interface" }));
  await screen.findByRole("radiogroup", { name: "Custom themes" });
}

describe("custom themes picker", () => {
  it("lists server files with warnings and applies on click", async () => {
    await openInterface();
    // Skipped-file notice from the server.
    await screen.findByText(/1 theme file\(s\) skipped/);
    // Contrast warning badge on the low-contrast card.
    const washed = screen.getByRole("radio", { name: /Washed custom theme/ });
    expect(washed.textContent).toMatch(/⚠/);
    // Click previews the file theme (unpersisted).
    fireEvent.click(screen.getByRole("radio", { name: /Harbor custom theme/ }));
    expect(document.documentElement.dataset.theme).toBe("custom-harbor");
    expect(document.getElementById("blackbird-custom-theme")?.textContent).toContain(
      "--pal-bg-app: #0d1117;",
    );
    // Save persists; activating the low-contrast file shows its warnings.
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await vi.waitFor(() => expect(memStore.get("blackbird.custom-theme.v1")).toBe("Harbor"));
    fireEvent.click(screen.getByRole("radio", { name: /Washed custom theme/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await screen.findByText(/body contrast/);
  });

  it("rejects invalid imports inline and installs valid ones", async () => {
    await openInterface();
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const setFiles = (files: File[]) => {
      Object.defineProperty(input, "files", { value: files, configurable: true });
      fireEvent.change(input);
    };
    const bad = new File(["version: 9"], "bad.yml", { type: "text/yaml" });
    setFiles([bad]);
    await screen.findByText(/missing name/);
    const good = new File(['version: 1\nname: "Harbor"\n'], "ok.yml", { type: "text/yaml" });
    setFiles([good]);
    // Install appends a second Harbor card; the import preview applies it.
    await vi.waitFor(() =>
      expect(screen.getAllByRole("radio", { name: /Harbor custom theme/ })).toHaveLength(2),
    );
    expect(document.documentElement.dataset.theme).toBe("custom-harbor");
  });

  it("deletes the active custom theme with confirmation", async () => {
    await openInterface();
    fireEvent.click(screen.getByRole("radio", { name: /Harbor custom theme/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await vi.waitFor(() => expect(memStore.get("blackbird.custom-theme.v1")).toBe("Harbor"));
    fireEvent.click(screen.getByRole("button", { name: "Delete custom theme Harbor" }));
    await vi.waitFor(() =>
      expect(screen.queryByRole("radio", { name: /Harbor custom theme/ })).toBeNull(),
    );
    expect(document.documentElement.dataset.theme).not.toBe("custom-harbor");
  });

  it("reports custom.css status", async () => {
    await openInterface();
    await screen.findByText(/No custom\.css/);
    cssState = "ready";
    const { refreshCustomCss } = await import("../../src/store/custom.js");
    await refreshCustomCss();
    await screen.findByText(/custom\.css active/);
    expect(document.getElementById("blackbird-custom-css")?.textContent).toBe(".x{color:red}");
  });
});
