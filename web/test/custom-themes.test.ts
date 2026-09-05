// @vitest-environment happy-dom
// Custom file themes (THM-9.4): resolution, warnings, CSS generation,
// export, and the built-in palette records' drift from CSS.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { BUILTIN_PALETTES, PALETTE_KEYS } from "../src/lib/theme-palettes.js";
import {
  basePalette,
  customThemeCss,
  customThemeId,
  customThemeWarnings,
  exportThemeYaml,
  resolveCustomAccents,
  resolveCustomPalette,
  resolveCustomTheme,
} from "../src/lib/custom-themes.js";

const HEX = /^#[0-9a-f]{6}$/i;

function cssVars(path: string, selector: string): Record<string, string> {
  const css = readFileSync(join(process.cwd(), path), "utf8");
  const m = new RegExp(
    `${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([\\s\\S]*?)\\}`,
  ).exec(css);
  const out: Record<string, string> = {};
  if (!m) return out;
  for (const line of m[1].split("\n")) {
    const mm = /\s*--(pal-[\w-]+)\s*:\s*([^;]+);/.exec(line);
    if (mm) out[mm[1].slice(4)] = mm[2].trim();
  }
  return out;
}

describe("built-in palette records", () => {
  it("matches the CSS files exactly (regenerate, never hand-edit)", () => {
    const dark = cssVars("src/styles/palette.css", ":root");
    expect(BUILTIN_PALETTES["dark"]).toEqual(dark);
    for (const id of ["light", "midnight", "contrast", "classic"]) {
      expect(BUILTIN_PALETTES[id]).toEqual(
        cssVars(`src/styles/themes/${id}.css`, `html[data-theme="${id}"]`),
      );
    }
    expect(PALETTE_KEYS).toEqual(Object.keys(BUILTIN_PALETTES["dark"]).sort());
    expect(PALETTE_KEYS.length).toBe(42);
  });
});

const SUBSET = {
  name: "Harbor",
  extends: "dark",
  palette: { "bg-app": "#0d1117" },
};

describe("custom resolution", () => {
  it("derives css-safe ids", () => {
    expect(customThemeId("Harbor Nights!")).toBe("harbor-nights");
    expect(customThemeId("!!!")).toBe("custom");
  });

  it("merges subsets over the extends base", () => {
    expect(basePalette("dark")?.["bg-app"]).toBe("#101214");
    expect(basePalette("nope")).toBeNull();
    const merged = resolveCustomPalette(SUBSET);
    expect(merged["bg-app"]).toBe("#0d1117");
    expect(merged["text-body"]).toBe("#d6d9dd");
  });

  it("falls back to extends presets, then dark", () => {
    const presets = (id: string) =>
      id === "light" ? ["#111111", "#222222", "#333333", "#444444", "#555555"] : [];
    expect(
      resolveCustomAccents(
        { name: "x", accents: ["#111111", "#222222", "#333333", "#444444", "#555555"] },
        presets,
      ),
    ).toEqual(["#111111", "#222222", "#333333", "#444444", "#555555"]);
    expect(resolveCustomAccents({ name: "x", extends: "light" }, presets)).toEqual([
      "#111111",
      "#222222",
      "#333333",
      "#444444",
      "#555555",
    ]);
    expect(resolveCustomAccents({ name: "x" }, presets)[0]).toBe("#35418f");
  });

  it("resolves a full theme record", () => {
    const t = resolveCustomTheme(
      { ...SUBSET, dark: false, accent: "#123456", density: "comfortable" },
      () => [],
    );
    expect(t).toMatchObject({
      id: "harbor",
      name: "Harbor",
      dark: false,
      fileAccent: "#123456",
      fileDensity: "comfortable",
    });
    expect(t.palette["bg-app"]).toBe("#0d1117");
  });
});

describe("custom warnings", () => {
  it("flags incomplete, unknown, and low-contrast files", () => {
    expect(customThemeWarnings({ name: "bare", palette: { "bg-app": "#ffffff" } })).toEqual(
      expect.arrayContaining([expect.stringContaining("missing")]),
    );
    expect(
      customThemeWarnings({ name: "x", extends: "dark", palette: { "bogus-key": "#ffffff" } }),
    ).toEqual(expect.arrayContaining([expect.stringContaining('unknown palette key "bogus-key"')]));
    // Near-identical body/bg: body check fails.
    const low = customThemeWarnings({
      name: "x",
      extends: "dark",
      palette: { "bg-app": "#101214", "text-body": "#121417" },
    });
    expect(low).toEqual(expect.arrayContaining([expect.stringContaining("body contrast")]));
    // The complete dark base is clean.
    expect(customThemeWarnings({ name: "x", palette: { ...BUILTIN_PALETTES["dark"] } })).toEqual(
      [],
    );
  });
});

describe("custom css + export", () => {
  it("generates a data-theme block", () => {
    const css = customThemeCss("harbor", { "bg-app": "#0d1117" });
    expect(css).toContain('html[data-theme="custom-harbor"]');
    expect(css).toContain("--pal-bg-app: #0d1117;");
  });

  it("exports a valid round-trippable YAML document", () => {
    const yaml = exportThemeYaml({
      name: "Harbor",
      description: "d",
      dark: true,
      accents: ["#111111", "#222222", "#333333", "#444444", "#555555"],
      palette: { "bg-app": "#0d1117" },
      accent: "#123456",
      density: "comfortable",
    });
    expect(yaml).toContain("version: 1");
    expect(yaml).toContain('name: "Harbor"');
    expect(yaml).toContain('accent: "#123456"');
    expect(yaml).toContain('density: "comfortable"');
    for (const line of yaml.split("\n")) {
      expect(line.trim()).not.toBe("undefined");
    }
  });
});
