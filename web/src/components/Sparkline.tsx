import { createMemo } from "solid-js";
import { historySamples } from "../store/session";

// 170×26 sparkline; SVG doubles the viewport for crisp 2px strokes at
// 0.5× device scale. Flat polylines, no axes — per the handoff.

const W = 340;
const H = 44;
const PAD = 4; // vertical padding so the stroke doesn't clip at the edges

function buildPoints(rates: number[]): string {
  const n = rates.length;
  if (n === 0) return `0,${H - PAD} ${W},${H - PAD}`;
  let max = 0;
  for (const r of rates) if (r > max) max = r;
  const span = H - 2 * PAD;
  return rates
    .map((rate, i) => {
      const x = n === 1 ? W - 1 : (i / (n - 1)) * W;
      const y = max > 0 ? H - PAD - (rate / max) * span : H - PAD;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

export function Sparkline() {
  const points = createMemo(() => {
    const samples = historySamples();
    return {
      down: buildPoints(samples.map((s) => s.downRate)),
      up: buildPoints(samples.map((s) => s.upRate)),
    };
  });

  return (
    <div class="sparkline" role="img" aria-label="Download and upload rate over time">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
        <polyline class="spark-down" points={points().down} />
        <polyline class="spark-up" points={points().up} />
      </svg>
    </div>
  );
}