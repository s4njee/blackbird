// Built-in themes (THM-9.2): ids, metadata, per-theme accent presets, and
// the data-theme application helpers. Palette values live in CSS
// (styles/palette.css + styles/themes/*.css); this module never duplicates
// them, except preset swatches for the picker.
import { toHex } from "./theme.js";

export type ThemeId = "dark" | "light" | "midnight" | "contrast" | "classic";
export type ThemeChoice = ThemeId | "system";

/** Row density (THM-9.3): dense is the handoff; comfortable adds 4px to
 * rows and controls via height-token overrides. */
export type Density = "dense" | "comfortable";

export const THEME_IDS: ThemeId[] = ["dark", "light", "midnight", "contrast", "classic"];
export const THEME_CHOICES: ThemeChoice[] = [...THEME_IDS, "system"];

export function parseThemeChoice(value: unknown): ThemeChoice | null {
  return typeof value === "string" && (THEME_CHOICES as string[]).includes(value)
    ? (value as ThemeChoice)
    : null;
}

export interface ThemeDef {
  id: ThemeId;
  label: string;
  description: string;
  /** True when the theme is dark-surfaced (system mapping + docs). */
  dark: boolean;
  /** Five accent presets offered for this theme (first = theme default). */
  accents: [string, string, string, string, string];
  /** Miniature preview swatches (must match the theme palette in CSS —
   * pinned by test/themes.test.ts, which parses the palette files). */
  preview: { bg: string; panel: string; text: string; accent: string; progress: string };
}

export const THEMES: ThemeDef[] = [
  {
    id: "dark",
    label: "Blackbird Dark",
    description: "The handoff palette. Default.",
    dark: true,
    accents: ["#35418f", "#f59e0b", "#e5484d", "#2f9dff", "#3fb950"],
    preview: {
      bg: "#101214",
      panel: "#0e1012",
      text: "#d6d9dd",
      accent: "#35418f",
      progress: "#2f9dff",
    },
  },
  {
    id: "light",
    label: "Blackbird Light",
    description: "Light surfaces, dark text.",
    dark: false,
    accents: ["#1d4ed8", "#b45309", "#c2410c", "#0e7490", "#15803d"],
    preview: {
      bg: "#f4f5f6",
      panel: "#ffffff",
      text: "#1a1d21",
      accent: "#1d4ed8",
      progress: "#1f8fff",
    },
  },
  {
    id: "midnight",
    label: "Midnight",
    description: "True-black OLED variant.",
    dark: true,
    accents: ["#4c5fd5", "#fbbf24", "#f87171", "#38bdf8", "#4ade80"],
    preview: {
      bg: "#000000",
      panel: "#0a0a0b",
      text: "#e6e9ec",
      accent: "#4c5fd5",
      progress: "#2f9dff",
    },
  },
  {
    id: "contrast",
    label: "High Contrast",
    description: "Dark, stronger text and borders.",
    dark: true,
    accents: ["#7aa2ff", "#ffd60a", "#ff6b61", "#4cc9f0", "#52e28c"],
    preview: {
      bg: "#000000",
      panel: "#000000",
      text: "#ffffff",
      accent: "#7aa2ff",
      progress: "#4da3ff",
    },
  },
  {
    id: "classic",
    label: "Classic",
    description: "ruTorrent-inspired light theme, blue selection.",
    dark: false,
    accents: ["#0b5cad", "#b26a00", "#c0392b", "#0e7c86", "#1e8e3e"],
    preview: {
      bg: "#e8edf2",
      panel: "#ffffff",
      text: "#1a1d21",
      accent: "#0b5cad",
      progress: "#2f9dff",
    },
  },
];

export function themeDef(id: ThemeId): ThemeDef {
  return THEMES.find((t) => t.id === id) ?? THEMES[0];
}

/** Resolve a stored choice to a concrete theme id. */
export function resolveThemeId(choice: ThemeChoice, prefersDark: boolean): ThemeId {
  if (choice !== "system") return choice;
  return prefersDark ? "dark" : "light";
}

export function prefersDarkScheme(win?: Window): boolean {
  try {
    return win?.matchMedia?.("(prefers-color-scheme: dark)").matches ?? true;
  } catch {
    return true;
  }
}

/** Apply a theme id to <html> (built-in ids and `custom-<id>` alike).
 * Returns the id for chaining. */
export function applyThemeId(doc: Document | undefined, id: string): string {
  if (doc?.documentElement) {
    doc.documentElement.dataset.theme = id;
  }
  return id;
}

export const THEME_STORAGE_KEY = "blackbird.theme.v1";

/** Browser-saved choice, or null when following the operator default. */
export function loadThemeChoice(storage?: Storage | null): ThemeChoice | null {
  try {
    const raw = storage?.getItem(THEME_STORAGE_KEY);
    return raw === null || raw === undefined ? null : parseThemeChoice(raw);
  } catch {
    return null;
  }
}

export function saveThemeChoice(storage: Storage | null | undefined, choice: ThemeChoice | null) {
  try {
    if (!storage) return;
    if (choice === null) storage.removeItem(THEME_STORAGE_KEY);
    else storage.setItem(THEME_STORAGE_KEY, choice);
  } catch {
    /* private mode: choice lasts the session */
  }
}

/* ---- Contrast math (shared with scripts/check-contrast.mjs) ---- */

export type RGB = { r: number; g: number; b: number };

export function parseHexColor(hex: string): RGB | null {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) return null;
  const v = parseInt(m[1], 16);
  return { r: (v >> 16) & 255, g: (v >> 8) & 255, b: v & 255 };
}

/** Parses #rrggbb or rgb()/rgba() (computed values); null otherwise. Alpha
 * is dropped — callers composite explicitly when they need it. */
export function parseCssColor(value: string): RGB | null {
  const hex = parseHexColor(value.trim());
  if (hex) return hex;
  const m = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})/i.exec(value.trim());
  if (!m) return null;
  const [r, g, b] = [Number(m[1]), Number(m[2]), Number(m[3])];
  if ([r, g, b].some((v) => !Number.isInteger(v) || v < 0 || v > 255)) return null;
  return { r, g, b };
}

/** Relative luminance per WCAG 2.1 (0..1). */
export function luminance(c: RGB): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
}

/** WCAG contrast ratio (1..21) between two CSS colors; NaN when unparseable. */
export function contrastRatio(a: string, b: string): number {
  const ca = parseCssColor(a);
  const cb = parseCssColor(b);
  if (!ca || !cb) return NaN;
  const [hi, lo] = [luminance(ca), luminance(cb)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Opaque srgb mix of two CSS colors (w = fraction of `b`). */
export function mixCss(a: string, b: string, wb: number): string {
  const ca = parseCssColor(a);
  const cb = parseCssColor(b);
  if (!ca || !cb) return a;
  const w = Math.max(0, Math.min(1, wb));
  return toHex({
    r: ca.r * (1 - w) + cb.r * w,
    g: ca.g * (1 - w) + cb.g * w,
    b: ca.b * (1 - w) + cb.b * w,
  });
}

/** Readable text color for a filled chip/dot of `background`: white or
 * near-black, whichever contrasts better (user-defined label colors). */
export function readableTextOn(background: string): string {
  const onWhite = contrastRatio(background, "#ffffff");
  const onBlack = contrastRatio(background, "#0b0c0d");
  if (Number.isNaN(onWhite)) return "#ffffff";
  return onWhite >= onBlack ? "#ffffff" : "#0b0c0d";
}

/** Tinted chip style for a user-configured label color: 22% color over the
 * app surface (matching the built-in tinted chips) with contrast-safe text.
 * Returns undefined for built-in/unset names (class-driven rendering). */
export function labelChipStyle(
  name: string,
  lookup: (name: string) => string | undefined,
  surface: string,
): { background: string; color: string } | undefined {
  const hex = lookup(name);
  if (!hex) return undefined;
  const bg = mixCss(hex, surface, 0.78);
  return { background: bg, color: readableTextOn(bg) };
}
