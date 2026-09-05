// @vitest-environment happy-dom
import assert from "node:assert/strict";
import { describe, it } from "vitest";
import { magnetFor } from "../src/lib/clipboard.js";

describe("clipboard", () => {
  it("builds a magnet link with an encoded name", () => {
    const magnet = magnetFor("abcd1234abcd1234abcd1234abcd1234abcd1234", "Ubuntu 24.04 ISO");
    assert.equal(
      magnet,
      "magnet:?xt=urn:btih:abcd1234abcd1234abcd1234abcd1234abcd1234&dn=Ubuntu%2024.04%20ISO",
    );
  });

  it("escapes names that need escaping", () => {
    // Name with characters that need escaping.
    const special = magnetFor("ff", "a/b?c=d");
    assert.ok(special.includes("dn=a%2Fb%3Fc%3Dd"), special);
  });
});
