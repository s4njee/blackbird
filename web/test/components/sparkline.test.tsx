// @vitest-environment happy-dom
// Component proof for the POL-8.1 harness: renders a Solid component with
// @solidjs/testing-library against a mocked store and asserts on the DOM.
import { describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/testing-library";

vi.mock("../../src/store/session.js", () => ({
  historySamples: () => [
    { at: 1000, downRate: 0, upRate: 0 },
    { at: 2000, downRate: 50, upRate: 100 },
  ],
}));

// Mocked above, so importing the component never opens a WebSocket.
import { Sparkline } from "../../src/components/Sparkline";

describe("Sparkline", () => {
  it("renders normalized down/up polylines from the mocked samples", () => {
    const { container } = render(() => <Sparkline />);
    // happy-dom's tag selector skips SVG-namespaced elements, so assert
    // through classes and markup instead of querySelector("svg").
    expect(container.innerHTML).toContain("viewBox");
    expect(container.querySelector(".sparkline")?.getAttribute("role")).toBe("img");
    // n=2, W=340: x positions 0 and 340; max normalizes the nonzero sample
    // to the top pad (H=44, pad=4 -> y=4) and zero to the baseline (y=40).
    expect(container.querySelector(".spark-down")?.getAttribute("points")).toBe(
      "0.0,40.0 340.0,4.0",
    );
    expect(container.querySelector(".spark-up")?.getAttribute("points")).toBe("0.0,40.0 340.0,4.0");
  });
});
