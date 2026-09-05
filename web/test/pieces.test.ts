// @vitest-environment happy-dom
import assert from "node:assert/strict";
import { describe, it } from "vitest";
import { bitfieldHexLength, bucketize, filePieceRanges, isPieceDone } from "../src/lib/pieces.js";

describe("pieces", () => {
  it("reads per-piece completion bits, false out of range", () => {
    // 0xff = 8 done pieces; 0xf0 = first 4 done; 0x0f = last 4 done.
    assert.equal(isPieceDone("ff", 0), true);
    assert.equal(isPieceDone("ff", 7), true);
    assert.equal(isPieceDone("f0", 0), true);
    assert.equal(isPieceDone("f0", 4), false);
    assert.equal(isPieceDone("0f", 4), true);
    assert.equal(isPieceDone("0f", 0), false);
    // Out of range -> false.
    assert.equal(isPieceDone("ff", 8), false);
    assert.equal(isPieceDone("", 0), false);
  });

  it("sizes bitfield hex strings", () => {
    assert.equal(bitfieldHexLength(8), 2);
    assert.equal(bitfieldHexLength(9), 4);
    assert.equal(bitfieldHexLength(0), 0);
  });

  it("buckets pieces into columns, including odd counts", () => {
    // Bucketing: 16 pieces where the first 8 bytes (hex "ff") are done then 0.
    const hex = "ff00";
    const buckets = bucketize(hex, 16, 4);
    assert.equal(buckets.length, 4);
    assert.equal(buckets[0].done, 1);
    assert.equal(buckets[1].done, 1);
    assert.equal(buckets[2].done, 0);
    assert.equal(buckets[3].done, 0);
    // Each bucket covers 4 pieces.
    assert.equal(buckets[0].start, 0);
    assert.equal(buckets[0].end, 4);

    // Odd piece counts: 10 pieces (bytes "ff 00": pieces 0-7 done, 8-9 missing),
    // bucketed into 2 columns of 5 pieces each.
    const mixed = bucketize("ff00", 10, 2);
    assert.equal(mixed.length, 2);
    assert.equal(mixed[0].done, 1); // pieces 0-4 all done
    assert.equal(mixed[1].done, 3 / 5); // pieces 5-9: 5,6,7 done; 8,9 missing
  });

  it("maps file byte offsets to piece ranges", () => {
    // File piece ranges from byte offsets.
    const ranges = filePieceRanges([0, 1000, 1500], 2000, 500, 4);
    assert.deepEqual(ranges[0], { index: 0, start: 0, end: 2 }); // bytes 0..999 -> pieces 0..1
    assert.deepEqual(ranges[1], { index: 1, start: 2, end: 3 }); // bytes 1000..1499 -> piece 2
    assert.deepEqual(ranges[2], { index: 2, start: 3, end: 4 }); // bytes 1500..1999 -> piece 3
  });
});
