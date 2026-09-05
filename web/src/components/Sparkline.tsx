import { createMemo } from "solid-js";
import { historySamples } from "../store/session";
import { SeriesPointsCache } from "../lib/chartPoints";

// 170×26 sparkline; SVG doubles the viewport for crisp 2px strokes at
// 0.5× device scale. Flat polylines, no axes — per the handoff.

const W = 340;
const H = 44;
const PAD = 4; // vertical padding so the stroke doesn't clip at the edges

export function Sparkline() {
  // Cached per data key (PERF-7.4): a quiet tick reuses the strings by
  // identity instead of rebuilding them; append/drop rebuilds exactly once.
  const cache = new SeriesPointsCache();
  const points = createMemo(() =>
    cache.update(historySamples(), { width: W, height: H, pad: PAD }),
  );

  return (
    <div class="sparkline" role="img" aria-label="Download and upload rate over time">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
        <polyline class="spark-down" points={points().down} />
        <polyline class="spark-up" points={points().up} />
      </svg>
    </div>
  );
}
