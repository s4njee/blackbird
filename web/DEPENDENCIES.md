# Frontend dependency ledger (PERF-7.5)

Every runtime dependency must be listed here with its purpose and measured
size delta. The `npm run size` gate fails if a `dependencies` entry from
`package.json` is missing from this file, so adding a dependency requires
recording its delta: run `npm run build` + `npm run size` before and after,
and paste both rows.

Dev dependencies never ship in the bundle but are listed for completeness;
they need no delta.

## Runtime

| Package | Purpose | Size delta (gzip) |
|---|---|---|
| `solid-js` | Reactive UI runtime (signals, store, `lazy`/code-splitting) | Baseline: entry chunk 55.4 KB / total 84.9 KB with code-split routes (2026-09, PERF-7.5). Removing it is not meaningful — the console is written against it. Any future runtime dep must record its before/after delta here. |

## Dev-only (never bundled)

| Package | Purpose |
|---|---|
| `happy-dom` | DOM environment for Vitest suites |
| `typescript` | Typechecking |
| `vite` | Dev server and production bundler |
| `vite-plugin-solid` | Solid JSX transform and `lazy` chunk splitting |
| `vitest` | Unit test runner |
| `@vitest/coverage-v8` | Coverage gate over `src/lib` and `src/store` |
| `@solidjs/testing-library` | Component rendering in tests |
| `@playwright/test` | Browser end-to-end suite |
| `eslint` | Lint runner |
| `typescript-eslint` | TypeScript lint rules |
| `eslint-config-prettier` | Disables stylistic rules owned by Prettier |
| `prettier` | Formatter |
| `stylelint` | CSS lint: bans hex/`rgb()` outside `palette.css`/`themes/` (THM-9.1) |

## Chunk baselines (2026-09, PERF-7.5; refreshed POL-8.3)

| Chunk | Contents | gzip |
|---|---|---|
| `index-*.js` (entry) | Console shell, table, detail, dialog + notice systems, routing, stores | 64.1 KB (budget 80 KB) |
| `SettingsPanel-*.js` | Settings route (lazy) | 21.3 KB |
| `StatsView-*.js` | Stats route (lazy) | 4.2 KB |
| `RssView-*.js` | RSS route (lazy) | 2.2 KB |
| `HistoryView-*.js` | History route (lazy) | 2.0 KB |
| Total | | ~94 KB (budget 120 KB) |
