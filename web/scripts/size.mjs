// Bundle size gate (PERF-6.5, PERF-7.5): the console entry chunk ships first
// and lazy route chunks load on demand. Both are bounded: the entry must
// stay small enough for a fast first paint on a LAN, and the total of all
// chunks must stay under the overall budget. Run after `npm run build`;
// exits non-zero over budget so CI fails the pull request.
//
// A second gate enforces the dependency policy (PERF-7.5): every runtime
// dependency in package.json must be recorded in web/DEPENDENCIES.md with a
// measured size delta, so no dependency lands without a ledger entry.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const ENTRY_BUDGET_BYTES = 80 * 1024;
const TOTAL_BUDGET_BYTES = 120 * 1024;

const assetsDir = new URL("../dist/assets/", import.meta.url);
const root = new URL("../", import.meta.url);
const rows = [];
for (const entry of readdirSync(assetsDir)) {
  if (!entry.endsWith(".js")) continue;
  const full = join(assetsDir.pathname, entry);
  const raw = statSync(full).size;
  // Recompress here rather than trusting Vite's report, so the gate checks
  // the same bytes the Go server negotiates (gzip baseline; brotli is
  // smaller, so gzip is the conservative bound).
  const gzipped = gzipSync(readFileSync(full), { level: 9 }).length;
  const kind = entry.startsWith("index-") ? "entry" : "lazy";
  rows.push({ entry, raw, gzipped, kind });
}
rows.sort((a, b) => b.gzipped - a.gzipped);

const entry = rows.filter((r) => r.kind === "entry");
const entryBytes = entry.reduce((total, r) => total + r.gzipped, 0);
const totalBytes = rows.reduce((total, r) => total + r.gzipped, 0);
for (const r of rows) console.log(`${r.entry} [${r.kind}]: ${r.raw} B raw, ${r.gzipped} B gzip`);
console.log(`entry JS gzip: ${entryBytes} B (budget ${ENTRY_BUDGET_BYTES} B)`);
console.log(`total JS gzip: ${totalBytes} B (budget ${TOTAL_BUDGET_BYTES} B)`);

let failed = false;
if (entryBytes > ENTRY_BUDGET_BYTES) {
  console.error("entry chunk over budget");
  failed = true;
}
if (totalBytes > TOTAL_BUDGET_BYTES) {
  console.error("bundle over budget");
  failed = true;
}

// Dependency ledger gate: every runtime dependency must be recorded in
// DEPENDENCIES.md with a size delta. Dev dependencies never ship, so they
// are exempt (but still listed in the ledger for completeness).
const pkg = JSON.parse(readFileSync(join(root.pathname, "package.json"), "utf8"));
const ledger = readFileSync(join(root.pathname, "DEPENDENCIES.md"), "utf8");
for (const dep of Object.keys(pkg.dependencies ?? {})) {
  if (!ledger.includes(dep)) {
    console.error(`dependency "${dep}" is missing from DEPENDENCIES.md — record its size delta`);
    failed = true;
  }
}
if (failed) process.exit(1);
