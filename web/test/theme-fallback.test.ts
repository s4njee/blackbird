// @vitest-environment happy-dom
// Accent fallback (THM-9.1): without color-mix(), applyAccent sets the
// derived tokens inline from the JS mirror.
import { describe, expect, it, vi } from "vitest";

vi.mock("../src/lib/theme.js", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../src/lib/theme.js")>();
  return { ...mod, supportsColorMix: () => false };
});

import { applyAccent, themeVersion } from "../src/store/ui.js";
import { deriveAccentTokens } from "../src/lib/theme.js";

describe("applyAccent fallback", () => {
  it("writes derived tokens inline when color-mix() is unavailable", () => {
    const before = themeVersion();
    applyAccent("#00ff00");
    const style = document.documentElement.style;
    const expected = deriveAccentTokens("#00ff00");
    expect(style.getPropertyValue("--accent")).toBe("#00ff00");
    expect(style.getPropertyValue("--accent-tint")).toBe(expected.tint);
    expect(style.getPropertyValue("--accent-tint-strong")).toBe(expected.tintStrong);
    expect(style.getPropertyValue("--accent-text")).toBe(expected.text);
    expect(style.getPropertyValue("--focus-ring")).toBe(expected.ring);
    expect(themeVersion()).toBe(before + 1);
    style.removeProperty("--accent");
  });
});
