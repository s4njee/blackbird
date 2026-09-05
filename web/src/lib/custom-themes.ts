// Custom file themes (THM-9.4): resolution, warnings, CSS generation, and
// export. File shapes come from GET /api/themes (see internal/themefile);
// base palettes come from lib/theme-palettes.ts (drift-tested against CSS).
import { BUILTIN_PALETTES, PALETTE_KEYS } from "./theme-palettes.js";
import { contrastRatio } from "./themes.js";

export interface CustomThemeFile {
  name: string;
  description?: string;
  extends?: string;
  dark?: boolean;
  accents?: string[];
  palette?: Record<string, string>;
  preview?: Record<string, string>;
  accent?: string;
  density?: string;
}

export interface ResolvedCustomTheme {
  id: string;
  name: string;
  description: string;
  dark: boolean;
  accents: string[];
  palette: Record<string, string>;
  fileAccent: string;
  fileDensity: string;
}

/** CSS-safe id for a custom theme name (mirrors server filename rules). */
export function customThemeId(name: string): string {
  const id = name
    .toLowerCase()
    .replace(/[^a-z0-9-_]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return id || "custom";
}

const HEX = /^#[0-9a-f]{6}$/i;
const ACCENT_FALLBACK = ["#35418f", "#f59e0b", "#e5484d", "#2f9dff", "#3fb950"];

/** Base palette for an extends id (built-ins only); null when unknown. */
export function basePalette(extendsId: string): Record<string, string> | null {
  return BUILTIN_PALETTES[extendsId] ?? null;
}

/** Effective palette: extends base merged with the file's overrides. */
export function resolveCustomPalette(file: CustomThemeFile): Record<string, string> {
  const base = (file.extends && basePalette(file.extends)) || {};
  return { ...base, ...(file.palette ?? {}) };
}

/** Effective accents: file list when valid, else the extends theme's
 * presets from the built-in catalogue, else the dark fallback. */
export function resolveCustomAccents(
  file: CustomThemeFile,
  builtinAccents: (id: string) => string[],
): string[] {
  if (
    Array.isArray(file.accents) &&
    file.accents.length === 5 &&
    file.accents.every((a) => HEX.test(a ?? ""))
  ) {
    return [...file.accents];
  }
  if (file.extends) {
    try {
      const presets = builtinAccents(file.extends);
      if (presets.length === 5) return [...presets];
    } catch {
      /* fall through */
    }
  }
  return [...ACCENT_FALLBACK];
}

export function resolveCustomTheme(
  file: CustomThemeFile,
  builtinAccents: (id: string) => string[],
): ResolvedCustomTheme {
  return {
    id: customThemeId(file.name),
    name: file.name,
    description: file.description ?? "",
    dark: file.dark ?? true,
    accents: resolveCustomAccents(file, builtinAccents),
    palette: resolveCustomPalette(file),
    fileAccent: typeof file.accent === "string" ? file.accent : "",
    fileDensity: file.density === "dense" || file.density === "comfortable" ? file.density : "",
  };
}

function alphaBlend(fg: string, alpha: number, bg: string): string {
  const c = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
  const F = c(fg);
  const B = c(bg);
  return (
    "#" +
    F.map((v, i) =>
      Math.round(v * alpha + B[i] * (1 - alpha))
        .toString(16)
        .padStart(2, "0"),
    ).join("")
  );
}

function mixToward(hex: string, target: string, fraction: number): string {
  return (
    "#" +
    [1, 3, 5]
      .map((i) => {
        const a = parseInt(hex.slice(i, i + 2), 16);
        const b = parseInt(target.slice(i, i + 2), 16);
        return Math.round(a * (1 - fraction) + b * fraction)
          .toString(16)
          .padStart(2, "0");
      })
      .join("")
  );
}

/** Human-readable warnings for a custom file (Settings display). Empty
 * means clean. Completeness/contrast mirror scripts/check-contrast.mjs. */
export function customThemeWarnings(file: CustomThemeFile): string[] {
  const warnings: string[] = [];
  const palette = file.palette ?? {};
  if (!file.extends) {
    const missing = PALETTE_KEYS.filter((k) => !(k in palette));
    if (missing.length)
      warnings.push(
        `missing ${missing.length} palette keys without extends: ${missing.slice(0, 5).join(", ")}${missing.length > 5 ? "…" : ""}`,
      );
  }
  const unknown = Object.keys(palette).filter((k) => !PALETTE_KEYS.includes(k));
  for (const k of unknown.slice(0, 5)) warnings.push(`unknown palette key "${k}" (ignored)`);
  if (unknown.length > 5) warnings.push(`${unknown.length - 5} more unknown keys (ignored)`);
  // Contrast on the effective palette (needs a complete color base to
  // judge; shadow shorthands are not colors and are exempt).
  const effective = resolveCustomPalette(file);
  const get = (k: string) => effective[k];
  const colorKeys = PALETTE_KEYS.filter((k) => !k.startsWith("shadow-"));
  if (colorKeys.every((k) => HEX.test(get(k) ?? ""))) {
    const ink = get("accent-ink");
    const chipBg = alphaBlend(get("accent"), 0.3, get("bg-app"));
    const chipText = mixToward(get("accent"), ink, 0.45);
    const checks: Array<[string, number]> = [
      ["body", contrastRatio(get("text-body"), get("bg-app"))],
      ["muted", contrastRatio(get("text-muted"), get("bg-app"))],
      ["chip", contrastRatio(chipText, chipBg)],
    ];
    for (const [label, ratio] of checks) {
      if (!(ratio >= 4.5))
        warnings.push(`${label} contrast ${ratio.toFixed(2)}:1 below WCAG AA 4.5:1`);
    }
  }
  return warnings;
}

/** CSS block applying an effective palette (`html[data-theme]` selector). */
export function customThemeCss(id: string, palette: Record<string, string>): string {
  const lines = Object.keys(palette)
    .sort()
    .map((k) => `  --pal-${k}: ${palette[k]};`);
  return `html[data-theme="custom-${id}"] {\n${lines.join("\n")}\n}`;
}

/** Minimal YAML serializer for theme export (flat known shape only). */
function yamlScalar(value: string): string {
  return JSON.stringify(value);
}

export function exportThemeYaml(args: {
  name: string;
  description: string;
  dark: boolean;
  accents: string[];
  palette: Record<string, string>;
  accent: string;
  density: string;
}): string {
  const lines = [
    "# Exported from the Blackbird picker (THM-9.4).",
    "# Install: save under <config-dir>/themes/<name>.yml and reload.",
    "version: 1",
    `name: ${yamlScalar(args.name)}`,
  ];
  if (args.description) lines.push(`description: ${yamlScalar(args.description)}`);
  lines.push(`dark: ${args.dark ? "true" : "false"}`);
  lines.push("accents:");
  for (const a of args.accents) lines.push(`  - ${yamlScalar(a)}`);
  lines.push("palette:");
  for (const k of Object.keys(args.palette).sort())
    lines.push(`  ${k}: ${yamlScalar(args.palette[k])}`);
  lines.push(`accent: ${yamlScalar(args.accent)}`);
  lines.push(`density: ${yamlScalar(args.density)}`);
  lines.push("");
  return lines.join("\n");
}
