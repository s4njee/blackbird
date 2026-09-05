// @vitest-environment happy-dom
import assert from "node:assert/strict";
import { describe, it } from "vitest";
import { durationToNs, nsToDuration } from "../src/lib/duration.js";

describe("duration", () => {
  it("round-trips ns through hour/minute/second strings", () => {
    // Round-trip: ns -> "24h" -> ns.
    const day = 24 * 3600 * 1e9;
    assert.equal(nsToDuration(day), "24h");
    assert.equal(durationToNs("24h"), day);
    assert.equal(nsToDuration(90 * 60 * 1e9), "90m");
    assert.equal(durationToNs("90m"), 90 * 60 * 1e9);
    // Exactly an hour formats as "1h"; sub-hour seconds use "Ns".
    assert.equal(nsToDuration(3600 * 1e9), "1h");
    assert.equal(nsToDuration(90 * 1e9), "90s");
    assert.equal(durationToNs("90s"), 90 * 1e9);
  });

  it("tolerates fractional hours and surrounding whitespace", () => {
    // Fractional hours fall back to minutes/seconds representations.
    assert.equal(durationToNs("1.5h"), Math.round(1.5 * 3.6e12));
    assert.equal(durationToNs(" 2h "), 2 * 3.6e12); // whitespace tolerated
  });

  it("rejects invalid input", () => {
    assert.equal(durationToNs(""), null);
    assert.equal(durationToNs("24"), null);
    assert.equal(durationToNs("24d"), null);
    assert.equal(durationToNs("abc"), null);
  });

  it("renders zero/invalid ns as an empty display string", () => {
    // Zero / invalid ns produce an empty display string.
    assert.equal(nsToDuration(0), "");
    assert.equal(nsToDuration("not-a-number"), "");
    assert.equal(nsToDuration(null), "");
  });
});
