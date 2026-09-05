// @vitest-environment happy-dom
// Torrent table semantics (POL-8.5): labelled table, keyboard-reachable sort
// buttons with aria-sort, roving tabindex, aria-selected/rowindex rows, and
// a live selection announcement.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@solidjs/testing-library";
import type { Torrent } from "../../src/lib/types.js";

vi.mock("../../src/store/session.js", () => {
  const rows = [
    {
      hash: "a",
      name: "alpha",
      sizeBytes: 1000,
      percent: 50,
      leftBytes: 500,
      state: "downloading",
      checkingPercent: 0,
      seeds: 3,
      peers: 5,
      downRate: 100,
      upRate: 10,
      downloadedBytes: 500,
      uploadedBytes: 100,
      etaSeconds: 60,
      ratio: 0.5,
      label: "",
      priority: 2,
      throttle: "",
      ratioGroup: "",
      addedAt: "2026-01-01T00:00:00.000Z",
      finishedAt: "",
      creationDate: "2026-01-01T00:00:00.000Z",
      trackerHost: "a.com",
      trackerStatus: "Working",
      directory: "/d",
      basePath: "/d",
      message: "",
      sequential: false,
      superseeding: false,
    },
    {
      hash: "b",
      name: "beta",
      sizeBytes: 2000,
      percent: 100,
      leftBytes: 0,
      state: "seeding",
      checkingPercent: 0,
      seeds: 0,
      peers: 9,
      downRate: 0,
      upRate: 200,
      downloadedBytes: 2000,
      uploadedBytes: 4000,
      etaSeconds: 0,
      ratio: 2.5,
      label: "iso",
      priority: 3,
      throttle: "",
      ratioGroup: "",
      addedAt: "2026-01-02T00:00:00.000Z",
      finishedAt: "2026-01-03T00:00:00.000Z",
      creationDate: "2026-01-02T00:00:00.000Z",
      trackerHost: "b.org",
      trackerStatus: "Working",
      directory: "/d",
      basePath: "/d",
      message: "",
      sequential: false,
      superseeding: false,
    },
  ] as Torrent[];
  return {
    connection: () => "connected",
    torrentList: () => rows,
    torrents: Object.fromEntries(rows.map((r) => [r.hash, r])),
    visibleHashes: () => rows.map((r) => r.hash),
    visibleRows: () => rows,
  };
});

import { TorrentTable } from "../../src/components/TorrentTable";

afterEach(cleanup);

describe("TorrentTable semantics", () => {
  it("labels the table and exposes sort buttons with aria-sort", () => {
    const { container } = render(() => <TorrentTable onContextMenu={() => {}} />);
    expect(container.querySelector("table")?.getAttribute("aria-label")).toBe("Torrents");
    const nameSort = screen.getByRole("button", { name: "Sort by Name" });
    expect(nameSort.tagName).toBe("BUTTON");
    const addedHeader = nameSort.closest("thead")!.querySelector("th.col-added")!;
    expect(addedHeader.getAttribute("aria-sort")).toBe("descending");
  });

  it("roves tabindex with aria-selected and absolute row indexes", () => {
    const { container } = render(() => <TorrentTable onContextMenu={() => {}} />);
    const rows = Array.from(container.querySelectorAll("tbody tr[data-hash]"));
    expect(rows.length).toBe(2);
    expect(rows[0].getAttribute("tabindex")).toBe("0");
    expect(rows[1].getAttribute("tabindex")).toBe("-1");
    expect(rows[0].getAttribute("aria-selected")).toBe("false");
    expect(rows[0].getAttribute("aria-rowindex")).toBe("1");
    expect(rows[1].getAttribute("aria-rowindex")).toBe("2");
  });

  it("announces the selection model in a live region", () => {
    const { container } = render(() => <TorrentTable onContextMenu={() => {}} />);
    const live = container.querySelector('[aria-live="polite"]')!;
    expect(live.getAttribute("role")).toBe("status");
    expect(live.textContent).toBe("No torrents selected");
  });
});
