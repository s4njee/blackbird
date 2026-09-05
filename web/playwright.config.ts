import { defineConfig, devices } from "@playwright/test";

// Browser suite (POL-8.1). Default: boots a real blackbird binary against
// fakertorrent via e2e/serve.sh (deterministic 50-torrent session, no auth).
// Point at anything else with E2E_BASE_URL — e.g. the Compose appliance for
// the QA-5.3 release smoke — and the webServer is skipped. E2E_PORT selects
// the local port (default 18223, chosen to avoid the dev default 8222).
const e2ePort = process.env.E2E_PORT ?? "18223";
const baseURL = process.env.E2E_BASE_URL ?? `http://127.0.0.1:${e2ePort}`;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  ...(process.env.E2E_BASE_URL
    ? {}
    : {
        webServer: {
          command: "./e2e/serve.sh",
          url: `http://127.0.0.1:${e2ePort}/api/health`,
          timeout: 180_000,
          // Never reuse: a squatting server embeds a stale web/dist, which
          // silently tests old code. serve.sh rebuilds every run instead.
          reuseExistingServer: false,
        },
      }),
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
