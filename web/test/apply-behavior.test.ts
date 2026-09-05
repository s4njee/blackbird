// @vitest-environment happy-dom
// Apply-behavior classification (POL-8.4, shared with backlog.md DOC-7.2):
// every Settings-editable surface applies live; only process-level keys
// need a restart.
import { describe, expect, it } from "vitest";
import { applyBehavior, RESTART_KEYS } from "../src/lib/applyBehavior.js";

describe("applyBehavior", () => {
  it("marks the process-level keys restart-required", () => {
    expect(RESTART_KEYS).toContain("server.listen");
    expect(RESTART_KEYS).toContain("rtorrent.scgi");
    expect(RESTART_KEYS).toContain("poll.interval");
    for (const key of RESTART_KEYS) {
      expect(applyBehavior(key)).toBe("restart");
    }
  });

  it("treats every Settings-editable surface as live", () => {
    for (const key of [
      "tuning",
      "tuning.throttles",
      "network.port_range",
      "network.ipfilter",
      "directories",
      "directories.watch",
      "automation.on_complete",
      "automation.rss",
      "seeding.groups",
      "schedule.bandwidth",
      "labels",
      "history.global_entries",
      "stats.traffic_days",
      "ui.accent",
      "ui.sort",
      "ui.date_format",
      "portcheck.url",
    ]) {
      expect(applyBehavior(key)).toBe("live");
    }
  });

  it("defaults unknown paths to live and trims input", () => {
    expect(applyBehavior("something.new")).toBe("live");
    expect(applyBehavior("  server.listen ")).toBe("restart");
  });
});
