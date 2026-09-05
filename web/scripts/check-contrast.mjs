#!/usr/bin/env node
// Contrast gate (THM-9.2): every built-in theme passes WCAG AA (4.5:1) for
// body text on the app background, muted text on the app background, and
// accent text on the tinted chip. Palettes are parsed from CSS (single
// source of truth); derivations mirror styles/tokens.css + lib/themes.ts.
// Usage: node scripts/check-contrast.mjs [--json]
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const MIN = 4.5;

function parseVars(css, selector) {
  // Capture the first block for `selector` (:root or html[data-theme="x"]).
  const re = new RegExp(
    `${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([\\s\\S]*?)\\}`,
    "",
  );
  const m = re.exec(css);
  const vars = {};
  if (!m) return vars;
  for (const line of m[1].split("\n")) {
    const mm = /--([\w-]+)\s*:\s*(#[0-9a-fA-F]{6}|[^;]+?)\s*(;|$)/.exec(line);
    if (mm) vars[mm[1]] = mm[2].trim();
  }
  return vars;
}

const hex = (v) => (/^#[0-9a-fA-F]{6}$/.test(v ?? "") ? v.toLowerCase() : null);
const rgb = (h) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));

function mix(a, b, wb) {
  // color-mix(in srgb): raw channel interpolation, wb = fraction of b.
  const A = rgb(a),
    B = rgb(b);
  return (
    "#" +
    A.map((v, i) =>
      Math.round(v * (1 - wb) + B[i] * wb)
        .toString(16)
        .padStart(2, "0"),
    ).join("")
  );
}

function blendOver(fg, alpha, bg) {
  const F = rgb(fg),
    B = rgb(bg);
  return (
    "#" +
    F.map((v, i) =>
      Math.round(v * alpha + B[i] * (1 - alpha))
        .toString(16)
        .padStart(2, "0"),
    ).join("")
  );
}

function lum(h) {
  const f = (v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const [r, g, b] = rgb(h);
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function ratio(a, b) {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const paletteCss = readFileSync(join(root, "src/styles/palette.css"), "utf8");
const base = parseVars(paletteCss, ":root");
const basePal = Object.fromEntries(
  Object.entries(base).filter(([k, v]) => k.startsWith("pal-") && hex(v)),
);
const baseKeys = Object.keys(basePal).sort();

const themes = { dark: basePal };
for (const file of readdirSync(join(root, "src/styles/themes")).filter((f) => f.endsWith(".css"))) {
  const css = readFileSync(join(root, "src/styles/themes", file), "utf8");
  const m = /html\[data-theme="([a-z]+)"\]/.exec(css);
  if (!m) {
    console.error(`no data-theme block in ${file}`);
    process.exitCode = 1;
    continue;
  }
  themes[m[1]] = { ...basePal };
  for (const [k, v] of Object.entries(parseVars(css, `html[data-theme="${m[1]}"]`))) {
    if (k.startsWith("pal-") && hex(v)) themes[m[1]][k] = v.toLowerCase();
  }
}

let failed = 0;
const rows = [];
for (const [id, pal] of Object.entries(themes)) {
  const missing = baseKeys.filter((k) => !(k in pal));
  if (missing.length) {
    console.error(`${id}: missing palette keys: ${missing.join(", ")}`);
    failed++;
    continue;
  }
  const p = (k) => pal[k];
  const ink = p("pal-accent-ink");
  const chipBg = blendOver(p("pal-accent"), 0.3, p("pal-bg-app"));
  const chipText = mix(p("pal-accent"), ink, 0.45);
  const checks = [
    ["body", ratio(p("pal-text-body"), p("pal-bg-app"))],
    ["muted", ratio(p("pal-text-muted"), p("pal-bg-app"))],
    ["chip", ratio(chipText, chipBg)],
  ];
  for (const [name, r] of checks) {
    const ok = r >= MIN;
    if (!ok) failed++;
    rows.push({ theme: id, check: name, ratio: r.toFixed(2), ok });
  }
}

if (process.argv.includes("--json")) {
  console.log(JSON.stringify(rows, null, 2));
} else {
  console.log("theme    check   ratio  AA");
  for (const r of rows)
    console.log(
      `${r.theme.padEnd(8)} ${r.check.padEnd(6)} ${r.ratio.padStart(5)}  ${r.ok ? "pass" : "FAIL"}`,
    );
}
if (failed) {
  console.error(`${failed} contrast/completeness failure(s) below WCAG AA ${MIN}:1`);
  process.exitCode = 1;
} else {
  console.log(`all themes pass WCAG AA ${MIN}:1`);
}
