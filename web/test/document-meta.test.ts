// @vitest-environment happy-dom
// Document title and favicon builders (POL-8.3).
import { describe, expect, it } from "vitest";
import { buildTitle, faviconHref } from "../src/lib/documentMeta.js";

describe("documentMeta", () => {
  it("shows rates and the active count while connected", () => {
    expect(buildTitle({ connection: "connected", downRate: 1200, upRate: 300, active: 2 })).toBe(
      "↓ 1.17 KB/s ↑ 300 B/s · 2 active — Blackbird Console",
    );
  });

  it("falls back to the product name when not connected", () => {
    for (const connection of ["connecting", "disconnected"] as const) {
      expect(buildTitle({ connection, downRate: 9, upRate: 9, active: 9 })).toBe(
        "Blackbird Console",
      );
    }
  });

  it("maps connection state to distinct favicon dots", () => {
    const connected = faviconHref("connected");
    const connecting = faviconHref("connecting");
    const disconnected = faviconHref("disconnected");
    expect(new Set([connected, connecting, disconnected]).size).toBe(3);
    for (const href of [connected, connecting, disconnected]) {
      expect(href.startsWith("data:image/svg+xml,")).toBe(true);
      expect(decodeURIComponent(href)).toContain("<circle");
    }
    expect(decodeURIComponent(connected)).toContain("#3fb950");
    expect(decodeURIComponent(disconnected)).toContain("#e0705a");
  });
});
