# Frontend testing (POL-8.1)

## Unit suites (Vitest)

`npm test` runs Vitest (`vitest.config.ts`, separate from the production Vite
config): suites in `test/**/*.test.ts(x)` import straight from `src`, no
compile step, happy-dom globals, Solid client build. `npm run test:coverage`
adds the v8 coverage gate over `src/lib` and `src/store` (lines 55,
functions 55, branches 40, statements 55 — raise, never lower).

Component tests use `@solidjs/testing-library`; stores with network calls are
tested with stubbed `fetch` (see `test/stores-fetch.test.ts`). Mock the
session store (`vi.mock`) when rendering components — importing it for real
opens a WebSocket.

Settings live one section per file under `src/components/settings/`
(`types.ts` drafts, `model.ts` pure helpers, `fields.tsx` tuning rows,
`SettingsSection.tsx` dispatcher, `SettingsPanel.tsx` shell); styles are
split per component under `src/styles/app/` with the cascade order pinned
by the `@import` list in `src/styles/app.css` (tokens stay in `tokens.css`,
responsive overrides stay last).

## Lint and format

- `npm run lint` — ESLint (TypeScript recommended, Prettier-compatible).
- `npm run lint:css` — stylelint: `color-no-hex` plus an `rgb()`-family ban
  everywhere except `src/styles/palette.css` (and `src/styles/themes/` for
  THM-9.2); components reference semantic tokens only.
- `npm run check-contrast` — parses the theme palettes from CSS and fails
  below WCAG AA (4.5:1) for body/muted/chip text on every theme (THM-9.2).
  `no-alert` bans `window.confirm`/`prompt`/`alert` in new code; the legacy
  call sites carry `eslint-disable-next-line` markers pointing at POL-8.2,
  which migrates them to the in-app dialog system. `max-len` caps lines at
  120 (Prettier `printWidth` is 100).
- `npm run format` / `npm run format:check` — Prettier write/check.

## Browser suite (Playwright)

`npm run e2e` runs `e2e/*.spec.ts` in Chromium. By default Playwright boots
`e2e/serve.sh`: a real `blackbird` binary against `fakertorrent` with a
deterministic 25-torrent session (seed 7) on port 18223 (`E2E_PORT`),
no auth, temp state. Build `web/dist` first — the server embeds it
(`make e2e` does build + run).

- Every test fails on console errors, page errors, failed requests, and
  non-OK API responses. Traces/screenshots keep on failure (`test-results/`).
- Against the Compose appliance instead (QA-5.3 release smoke):
  `E2E_BASE_URL=http://<host>:8222 npm run e2e` (webServer skipped).
- Login flow is not covered against fakertorrent (auth disabled there);
  cover it via the Compose run, where bootstrap credentials apply.
- `e2e/screenshots.spec.ts` (POL-8.7) captures README shots — console,
  console-detail, empty-filter, stats, settings-about, settings-general, rss,
  history — into `docs/screenshots/` for `backlog.md` DOC-7.1 to embed. The
  spec asserts no console/page/network problems but never fails on pixels.
  Run it alone with `npx playwright test e2e/screenshots.spec.ts`.
  THM-9.2 runs every shot in Dark and Light (`-<theme>.png`); the committed
  pairs are the visual regression baselines for both surfaces.
- `e2e/appliance-theme.spec.ts` (THM-9.5) boots the Compose appliance
  (`./deploy/theme-smoke.sh`, or `make theme-smoke`) and asserts dark and
  light load the console and stats with zero console/page/network errors.
  It skips without `E2E_BASE_URL`/`E2E_USER`/`E2E_PASSWORD`.

## CI

`compose-smoke.yml`: the frontend job runs typecheck, unit tests with
coverage, lint, format check, build, and the bundle budget; the `go-checks`
job runs `go vet`, `go test -race`, and staticcheck (pinned v0.8.1, also
`make staticcheck` locally); the `e2e` job builds the frontend, installs
Chromium, and runs the browser suite, keeping failure artifacts for 7 days.
