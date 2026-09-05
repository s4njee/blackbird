// @vitest-environment happy-dom
// Hash routing (POL-8.6): parse/build round-trips for routes, sections, and
// the filter/focus query state.
import { describe, expect, it } from "vitest";
import { buildHash, parseHash } from "../src/lib/router.js";

describe("router", () => {
  it("parses an empty hash as the console", () => {
    expect(parseHash("")).toEqual({ route: "console", section: "", filter: "", focus: "" });
    expect(parseHash("#/")).toEqual({ route: "console", section: "", filter: "", focus: "" });
  });

  it("parses routes with sections and query state", () => {
    expect(parseHash("#/settings/Bandwidth")).toEqual({
      route: "settings",
      section: "Bandwidth",
      filter: "",
      focus: "",
    });
    expect(parseHash("#/stats")).toMatchObject({ route: "stats" });
    expect(parseHash("#/?filter=iso&focus=abc123")).toEqual({
      route: "console",
      section: "",
      filter: "iso",
      focus: "abc123",
    });
    expect(parseHash("#/settings/Connection?filter=x")).toMatchObject({
      route: "settings",
      section: "Connection",
    });
  });

  it("falls back to console on unknown routes", () => {
    expect(parseHash("#/nope")).toMatchObject({ route: "console" });
    expect(parseHash("#/settings")).toMatchObject({ route: "settings", section: "" });
  });

  it("builds hashes that re-parse identically", () => {
    const states = [
      { route: "console" as const },
      { route: "console" as const, filter: "iso", focus: "abc" },
      { route: "settings" as const, section: "Bandwidth" },
      { route: "stats" as const },
      { route: "rss" as const },
      { route: "history" as const },
    ];
    for (const state of states) {
      const hash = buildHash(state);
      expect(hash.startsWith("#/")).toBe(true);
      const parsed = parseHash(hash);
      expect(parsed.route).toBe(state.route);
      expect(parsed.section).toBe(state.section ?? "");
      expect(parsed.filter).toBe(state.filter ?? "");
      expect(parsed.focus).toBe(state.focus ?? "");
    }
  });

  it("keeps query state off non-console routes", () => {
    expect(buildHash({ route: "stats", filter: "x", focus: "y" })).toBe("#/stats");
    expect(buildHash({ route: "console" })).toBe("#/");
  });
});
