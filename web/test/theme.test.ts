// @vitest-environment happy-dom
// Theme tokens (THM-9.1): color math, DOM-less fallbacks, and the static
// contract of the two token layers (imported raw, no cascade needed).
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  connectionDotColors,
  deriveAccentTokens,
  mixWithWhite,
  parseHex,
  pieceMapColors,
  readToken,
  supportsColorMix,
  themeColor,
  toHex,
  withAlpha,
} from "../src/lib/theme.js";

const tokensCss = readFileSync(join(process.cwd(), "src/styles/tokens.css"), "utf8");
const paletteCss = readFileSync(join(process.cwd(), "src/styles/palette.css"), "utf8");

describe("theme color math", () => {
  it("round-trips hex through parse/toHex", () => {
    expect(toHex(parseHex("#35418f")!)).toBe("#35418f");
    expect(parseHex("not-a-color")).toBeNull();
    expect(parseHex("#fff")).toBeNull();
  });

  it("mixes toward white like color-mix(in srgb)", () => {
    // Manual: 53*.55+255*.45=143.9->144, 65*.55+114.75=150.5->151, 143*.55+114.75=193.4->193.
    expect(mixWithWhite("#35418f", 0.45)).toBe("#9097c1");
    expect(mixWithWhite("#000000", 1)).toBe("#ffffff");
    expect(mixWithWhite("#123456", 0)).toBe("#123456");
    expect(mixWithWhite("bogus", 0.5)).toBe("bogus");
  });

  it("scales alpha like color-mix with transparent", () => {
    expect(withAlpha("#35418f", 0.22)).toBe("rgba(53, 65, 143, 0.22)");
    expect(withAlpha("#35418f", 0.3)).toBe("rgba(53, 65, 143, 0.3)");
    expect(withAlpha("bogus", 0.5)).toBe("bogus");
  });

  it("derives the accent family mirroring tokens.css", () => {
    expect(deriveAccentTokens("#35418f")).toEqual({
      tint: "rgba(53, 65, 143, 0.22)",
      tintStrong: "rgba(53, 65, 143, 0.3)",
      text: mixWithWhite("#35418f", 0.45),
      ring: mixWithWhite("#35418f", 0.55),
    });
  });
});

describe("theme token reads without a cascade", () => {
  const noDom = {} as Document;

  it("detects color-mix support from the CSS object", () => {
    expect(supportsColorMix(noDom)).toBe(false);
    const modern = { defaultView: { CSS: { supports: () => true } } } as unknown as Document;
    expect(supportsColorMix(modern)).toBe(true);
    expect(readToken("--accent", noDom)).toBe("");
  });

  it("falls back to the handoff literals", () => {
    expect(pieceMapColors()).toEqual({
      done: "#3fb950",
      working: "#2f9dff",
      missing: "#2a2d33",
      highlight: "#f59e0b",
    });
    expect(connectionDotColors()).toEqual({
      connected: "#3fb950",
      connecting: "#7c828a",
      disconnected: "#e0705a",
    });
    expect(themeColor()).toBe("#101214");
  });
});

describe("token layer contract", () => {
  it("keeps literals in the palette layer only", () => {
    expect(paletteCss).toMatch(/#35418f/);
    expect(tokensCss).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(tokensCss).not.toMatch(/rgba?\(/);
  });

  it("derives accent tokens with color-mix, never progress", () => {
    for (const name of [
      "--accent-tint:",
      "--accent-tint-strong:",
      "--accent-text:",
      "--focus-ring:",
    ]) {
      expect(tokensCss).toContain(name);
    }
    const bare = tokensCss.replace(/\/\*[\s\S]*?\*\//g, "");
    const mixes = bare.match(/color-mix\([^;]+?\)/gs) ?? [];
    expect(mixes.length).toBe(4);
    expect(mixes.every((m) => m.includes("var(--accent)"))).toBe(true);
    const progressLines = tokensCss
      .split("\n")
      .filter((l) => l.includes("--progress-active:") || l.includes("--progress-complete:"));
    expect(progressLines.length).toBe(2);
    expect(progressLines.every((l) => !l.includes("--accent"))).toBe(true);
  });

  it("aliases every semantic color to the palette", () => {
    for (const name of [
      "--bg-app:",
      "--text-body:",
      "--border-default:",
      "--rate-up:",
      "--status-error:",
    ]) {
      const line = tokensCss.split("\n").find((l) => l.includes(name));
      expect(line).toMatch(/var\(--pal-/);
    }
  });
});

describe("applyAccent integration (color-mix path)", () => {
  it("sets --accent, leaves derivations to CSS, and bumps the theme version", async () => {
    const ui = await import("../src/store/ui.js");
    const before = ui.themeVersion();
    ui.applyAccent("not-a-color");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("");
    expect(ui.themeVersion()).toBe(before);
    ui.applyAccent("#ff0000");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("#ff0000");
    // happy-dom reports color-mix support: no inline derivations.
    expect(document.documentElement.style.getPropertyValue("--accent-tint")).toBe("");
    expect(ui.themeVersion()).toBe(before + 1);
    document.documentElement.style.removeProperty("--accent");
  });
});
