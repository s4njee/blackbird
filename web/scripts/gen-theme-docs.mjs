#!/usr/bin/env node
// Theme token reference generator (THM-9.4): parses the semantic layer
// (src/styles/tokens.css) into docs/theme-tokens.md so the reference
// cannot drift from the code. CI fails when the output is stale:
//   npm run gen:theme-docs && git diff --exit-code docs/theme-tokens.md
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const css = readFileSync(join(root, "src/styles/tokens.css"), "utf8");
const palette = readFileSync(join(root, "src/styles/palette.css"), "utf8");

// Role comments live on the palette values; merge them onto the semantic
// aliases so authors document a token once, where its value lives.
const palRoles = {};
for (const line of palette.split("\n")) {
  const m = /--(pal-[\w-]+)\s*:[^;]+;\s*\/\*(.+?)\*\//.exec(line);
  if (m) palRoles[`--${m[1].slice(4)}`] = m[2].trim();
}

const block = css.match(/:root\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
// Join wrapped declarations (e.g. multi-line color-mix) before parsing:
// a continuation appends only onto an unterminated declaration line.
const logical = block.split("\n").reduce((acc, line) => {
  const last = acc[acc.length - 1];
  const openDecl = last !== undefined && /^\s*--[\w-]+\s*:/.test(last) && !/;/.test(last);
  if (openDecl && line.trim() !== "" && line.trim() !== "}") {
    acc[acc.length - 1] = `${last} ${line.trim()}`;
  } else {
    acc.push(line);
  }
  return acc;
}, []);
const rows = [];
let section = "";
for (const raw of logical) {
  const line = raw.trim();
  const sectionMatch = /^\/\* ---- (.+) ---- \*\/$/.exec(line);
  if (sectionMatch) {
    section = sectionMatch[1];
    continue;
  }
  const decl = /^(--[\w-]+)\s*:\s*(.+?);(?:\s*\/\*(.+?)\*\/)?$/.exec(line);
  if (decl) {
    const palRef = /^var\((--pal-[\w-]+)\)$/.exec(decl[2].trim());
    const role = (decl[3] ?? "").trim() || (palRef ? (palRoles[palRef[1].slice(6)] ?? "") : "");
    rows.push({ section, token: decl[1], value: decl[2].trim(), role });
  }
}

const out = [
  "# Theme token reference (generated — do not hand-edit)",
  "",
  "Regenerate with `npm run gen:theme-docs` (web/). Source of truth:",
  "`src/styles/tokens.css` (`:root` semantic layer). Raw values live in",
  "`src/styles/palette.css`; theme files in `src/styles/themes/` or the",
  "config `themes/` directory override `--pal-*` values only.",
  "",
  "| Token | Default | Role |",
  "|---|---|---|",
];
let lastSection = "";
for (const r of rows) {
  if (r.section !== lastSection) {
    lastSection = r.section;
    out.push(`| **${r.section}** | | |`);
  }
  out.push(`| \`${r.token}\` | \`${r.value}\` | ${r.role} |`);
}
out.push("");
writeFileSync(join(root, "..", "docs/theme-tokens.md"), out.join("\n"));
console.log(`wrote docs/theme-tokens.md (${rows.length} tokens)`);
