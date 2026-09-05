import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";

// Test harness (POL-8.1). Separate from vite.config.ts so the production
// build never inherits test-only resolve conditions. Suites live in
// test/**/*.test.ts(x) and import straight from src (no compile step);
// happy-dom supplies the DOM globals the store tests need.
export default defineConfig({
  plugins: [solid()],
  resolve: {
    // Mirror the legacy runner's `--conditions=browser` so solid-js/store
    // resolve to the client build under test.
    conditions: ["development", "browser"],
  },
  test: {
    environment: "happy-dom",
    include: ["test/**/*.test.ts", "test/**/*.test.tsx"],
    coverage: {
      provider: "v8",
      include: ["src/lib/**/*.ts", "src/store/**/*.ts"],
      // Measured baselines (POL-8.1): lib is well covered; store fetch paths
      // are covered while session/WS lifecycle rides the Playwright suite.
      // Raise these when new suites land — never lower them.
      thresholds: {
        lines: 55,
        functions: 55,
        branches: 40,
        statements: 55,
      },
    },
  },
});
