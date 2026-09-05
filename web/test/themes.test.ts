// @vitest-environment happy-dom
// Theme mechanism (THM-9.2): ids, presets, system resolution, persistence,
// and contrast helpers.
import { describe, expect, it } from "vitest";
import {
  THEME_IDS,
  THEMES,
  THEME_CHOICES,
  THEME_STORAGE_KEY,
  applyThemeId,
  contrastRatio,
  labelChipStyle,
  loadThemeChoice,
  luminance,
  mixCss,
  parseCssColor,
  parseHexColor,
  parseThemeChoice,
  readableTextOn,
  resolveThemeId,
  saveThemeChoice,
  themeDef,
} from "../src/lib/themes.js";

const HEX = /^#[0-9a-f]{6}$/i;

describe("theme catalogue", () => {
  it("defines five themes plus system", () => {
    expect(THEME_IDS).toEqual(["dark", "light", "midnight", "contrast", "classic"]);
    expect(THEME_CHOICES).toContain("system");
    expect(THEMES.map((t) => t.id)).toEqual(THEME_IDS);
  });

  it("gives every theme five valid accent presets", () => {
    for (const theme of THEMES) {
      expect(theme.accents).toHaveLength(5);
      for (const preset of theme.accents) expect(preset).toMatch(HEX);
    }
    expect(themeDef("dark").accents[0]).toBe("#35418f");
  });

  it("parses choices strictly", () => {
    expect(parseThemeChoice("light")).toBe("light");
    expect(parseThemeChoice("system")).toBe("system");
    expect(parseThemeChoice("nope")).toBeNull();
    expect(parseThemeChoice("")).toBeNull();
    expect(parseThemeChoice(undefined)).toBeNull();
  });
});

describe("system resolution", () => {
  it("resolves explicit themes directly and system by preference", () => {
    expect(resolveThemeId("midnight", true)).toBe("midnight");
    expect(resolveThemeId("light", true)).toBe("light");
    expect(resolveThemeId("system", true)).toBe("dark");
    expect(resolveThemeId("system", false)).toBe("light");
  });

  it("applies the id to <html> dataset", () => {
    applyThemeId(document, "contrast");
    expect(document.documentElement.dataset.theme).toBe("contrast");
    applyThemeId(document, "dark");
  });

  it("persists the browser choice, null clears to operator default", () => {
    const store = new Map<string, string>();
    const storage = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    } as unknown as Storage;
    expect(loadThemeChoice(storage)).toBeNull();
    saveThemeChoice(storage, "midnight");
    expect(storage.getItem(THEME_STORAGE_KEY)).toBe("midnight");
    expect(loadThemeChoice(storage)).toBe("midnight");
    saveThemeChoice(storage, null);
    expect(loadThemeChoice(storage)).toBeNull();
    expect(loadThemeChoice(null)).toBeNull();
  });
});

describe("contrast helpers", () => {
  it("parses hex and computed rgb()", () => {
    expect(parseHexColor("#ffffff")).toEqual({ r: 255, g: 255, b: 255 });
    expect(parseCssColor("rgb(16, 18, 20)")).toEqual({ r: 16, g: 18, b: 20 });
    expect(parseCssColor("rgba(16, 18, 20, 0.5)")).toEqual({ r: 16, g: 18, b: 20 });
    expect(parseCssColor("nope")).toBeNull();
  });

  it("computes WCAG ratios", () => {
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 1);
    expect(contrastRatio("#000000", "#000000")).toBeCloseTo(1, 5);
    expect(Number.isNaN(contrastRatio("nope", "#fff"))).toBe(true);
    expect(luminance({ r: 255, g: 255, b: 255 })).toBeCloseTo(1, 5);
  });

  it("picks readable text on any fill", () => {
    expect(readableTextOn("#000000")).toBe("#ffffff");
    expect(readableTextOn("#ffffff")).toBe("#0b0c0d");
    expect(readableTextOn("nope")).toBe("#ffffff");
  });

  it("mixes two css colors", () => {
    expect(mixCss("#000000", "#ffffff", 1)).toBe("#ffffff");
    expect(mixCss("#123456", "#ffffff", 0)).toBe("#123456");
    expect(mixCss("nope", "#fff", 0.5)).toBe("nope");
  });

  it("styles custom labels with contrast-safe text", () => {
    const lookup = (name: string) => (name === "iso" ? "#f59e0b" : undefined);
    expect(labelChipStyle("other", lookup, "#101214")).toBeUndefined();
    const style = labelChipStyle("iso", lookup, "#101214")!;
    expect(style.background).toMatch(HEX);
    // Tinted amber over near-black: light text wins with AA margin.
    expect(contrastRatio(style.color, style.background)).toBeGreaterThan(4.5);
    const light = labelChipStyle("iso", lookup, "#ffffff")!;
    expect(contrastRatio(light.color, light.background)).toBeGreaterThan(4.5);
  });
});

describe("theme preview excerpts", () => {
  it("matches the palette files (dark from palette.css, rest from themes/)", async () => {
    const { readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const read = (p: string) => readFileSync(join(process.cwd(), p), "utf8");
    const vars = (css: string) => {
      const out: Record<string, string> = {};
      for (const line of css.split("\n")) {
        const m = /--([\w-]+)\s*:\s*(#[0-9a-fA-F]{6})/.exec(line);
        if (m) out[m[1]] = m[2].toLowerCase();
      }
      return out;
    };
    const sources: Record<string, string> = {
      dark: "src/styles/palette.css",
      light: "src/styles/themes/light.css",
      midnight: "src/styles/themes/midnight.css",
      contrast: "src/styles/themes/contrast.css",
      classic: "src/styles/themes/classic.css",
    };
    for (const theme of THEMES) {
      const pal = vars(read(sources[theme.id]));
      expect({ ...pal, ...{} }["pal-bg-app"]).toBeTruthy();
      expect(theme.preview.bg).toBe(pal["pal-bg-app"]);
      expect(theme.preview.panel).toBe(pal["pal-bg-panel"]);
      expect(theme.preview.text).toBe(pal["pal-text-body"]);
      expect(theme.preview.accent).toBe(pal["pal-accent"]);
      expect(theme.preview.progress).toBe(pal["pal-progress-active"]);
    }
  });
});
