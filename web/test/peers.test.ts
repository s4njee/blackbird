// @vitest-environment happy-dom
import assert from "node:assert/strict";
import { describe, it } from "vitest";
import { comparePeer, PeerSorter } from "../src/lib/peerSort.js";
import { countryFlagEmoji, countryName, flagTooltip } from "../src/lib/peerMeta.js";

describe("peers", () => {
  function peer(overrides = {}) {
    return {
      id: "peer1",
      address: "10.0.0.5",
      port: 51413,
      client: "qBittorrent 5.0",
      completedPercent: 87,
      downRate: 2048,
      upRate: 1024,
      downloadedBytes: 1000,
      uploadedBytes: 7000,
      isSnubbed: false,
      countryCode: "US",
      flags: "EI",
      ...overrides,
    };
  }

  it("comparePeer sorts numeric columns with stable ip:port tiebreak", () => {
    // comparePeer sorts numeric columns; ties break by ip:port for stability.
    assert.ok(
      comparePeer(peer({ downRate: 100 }), peer({ address: "10.0.0.9", downRate: 200 }), [
        { column: "downRate", direction: "asc" },
      ]) < 0,
    );
    assert.ok(
      comparePeer(peer({ downRate: 100 }), peer({ address: "10.0.0.9", downRate: 200 }), [
        { column: "downRate", direction: "desc" },
      ]) > 0,
    );
    const sameRateLater = peer({ address: "10.0.0.9", port: 6881, downRate: 100 });
    assert.ok(
      comparePeer(peer({ downRate: 100 }), sameRateLater, [
        { column: "downRate", direction: "asc" },
      ]) < 0,
      "equal-rate rows order stably by ip:port",
    );
  });

  it("PeerSorter keeps a stable sorted array across small live deltas", () => {
    const sorter = new PeerSorter();
    const keys = [{ column: "downRate", direction: "desc" }];
    const rows = [
      peer({ address: "10.0.0.1", downRate: 300 }),
      peer({ address: "10.0.0.2", downRate: 200 }),
      peer({ address: "10.0.0.3", downRate: 100 }),
    ];
    const first = sorter.sort(rows, keys);
    assert.equal(first.length, 3);
    assert.equal(first[0].address, "10.0.0.1");
    assert.equal(
      sorter.sort(rows, keys),
      first,
      "unchanged input returns the same array (no rebuild)",
    );
    const changed = peer({ address: "10.0.0.3", downRate: 400 });
    const next = sorter.sort([...rows.slice(0, 2), changed], keys);
    assert.equal(next[0].address, "10.0.0.3", "a changed rate moves only the changed row");
    assert.equal(sorter.sort(rows.slice(0, 2), keys).length, 2, "removed peer is pruned");
  });

  it("decodes flag tooltips, marking unknown flags", () => {
    assert.equal(
      flagTooltip("EIS"),
      "Encrypted connection · Incoming connection · Snubbed — rTorrent has stopped uploading to this peer",
    );
    assert.ok(flagTooltip("Q").includes("Unknown: Q"));
    assert.equal(flagTooltip(""), "");
  });

  it("degrades country names and flag emoji", () => {
    // Country name + flag emoji degrade to an em dash / fall back to raw code.
    assert.equal(countryName("US"), "United States");
    assert.equal(countryName(""), "—");
    assert.equal(countryName("ZZ"), "ZZ");
    assert.equal(countryFlagEmoji("US").length, 4, "two regional-indicator surrogate pairs");
    assert.equal(countryFlagEmoji("us"), "—");
    assert.equal(countryFlagEmoji(""), "—");
  });
});
