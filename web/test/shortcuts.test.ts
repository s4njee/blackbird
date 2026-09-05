// @vitest-environment happy-dom
// Shortcut binding table (POL-8.5): matching semantics and help completeness.
// The help overlay renders from this same table, so coverage here guards
// against hint/behavior drift.
import { describe, expect, it } from "vitest";
import { matchShortcut, SHORTCUTS } from "../src/lib/shortcuts.js";

type KeyLike = {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
};

const key = (overrides: Partial<KeyLike> & { key: string }): KeyLike => ({
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  shiftKey: false,
  ...overrides,
});

describe("shortcuts", () => {
  it("matches every documented binding", () => {
    expect(matchShortcut(key({ key: "/" }))?.id).toBe("focus-filter");
    expect(matchShortcut(key({ key: "a", ctrlKey: true }))?.id).toBe("select-all");
    expect(matchShortcut(key({ key: "a", metaKey: true }))?.id).toBe("select-all");
    expect(matchShortcut(key({ key: "A" }))?.id).toBe("add");
    expect(matchShortcut(key({ key: "r" }))?.id).toBe("recheck");
    expect(matchShortcut(key({ key: "?" }))?.id).toBe("help");
    expect(matchShortcut(key({ key: "f" }))?.id).toBe("toggle-detail");
    expect(matchShortcut(key({ key: "5" }))?.id).toBe("detail-tab");
    expect(matchShortcut(key({ key: "9" }))?.id).toBe("detail-tab");
    expect(matchShortcut(key({ key: "0" }))).toBeNull();
    expect(matchShortcut(key({ key: "ArrowUp", shiftKey: true }))?.id).toBe("move-up");
    expect(matchShortcut(key({ key: "PageDown" }))?.id).toBe("page-down");
    expect(matchShortcut(key({ key: "Home" }))?.id).toBe("home");
    expect(matchShortcut(key({ key: " " }))?.id).toBe("toggle-start");
    expect(matchShortcut(key({ key: "Backspace" }))?.id).toBe("remove");
    expect(matchShortcut(key({ key: "Escape" }))?.id).toBe("blur");
    expect(matchShortcut(key({ key: "x" }))).toBeNull();
  });

  it("requires clean modifiers for letters but not for motion keys", () => {
    expect(matchShortcut(key({ key: "a", ctrlKey: true, altKey: true }))).toBeNull();
    expect(matchShortcut(key({ key: "r", metaKey: true }))).toBeNull();
    expect(matchShortcut(key({ key: "ArrowDown", ctrlKey: true }))?.id).toBe("move-down");
  });

  it("documents every binding exactly once for the help overlay", () => {
    const ids = SHORTCUTS.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const binding of SHORTCUTS) {
      expect(binding.keys.length).toBeGreaterThan(0);
      expect(binding.label.length).toBeGreaterThan(0);
    }
    expect(ids).toContain("help");
  });
});
