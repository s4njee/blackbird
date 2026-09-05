import { describe, expect, it } from "vitest";
import { compareColumn } from "../src/lib/sort.js";
import { torrentStateLabel } from "../src/lib/status.js";
import type { Torrent } from "../src/lib/types.js";

const row = (state: string, checkingPercent = 0) =>
  ({ state, checkingPercent } as Torrent);

describe("torrent status sorting", () => {
  it("treats a zero-progress check as queued", () => {
    const checking = row("checking", 0);
    expect(torrentStateLabel(checking)).toBe("Queued");
    expect(compareColumn(checking, row("queued"), "state")).toBe(0);
  });

  it("keeps an active check beside in-progress states", () => {
    const checking = row("checking", 25);
    expect(torrentStateLabel(checking)).toBe("Checking 25%");
    expect(compareColumn(row("downloading"), checking, "state")).toBeLessThan(0);
    expect(compareColumn(checking, row("seeding"), "state")).toBeLessThan(0);
    expect(compareColumn(checking, row("stopped"), "state")).toBeLessThan(0);
  });
});
