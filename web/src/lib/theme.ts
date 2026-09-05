// Theme token helpers (THM-9.1). The semantic layer (styles/tokens.css)
// derives accent tokens with color-mix(); this module mirrors that math in
// JS for browsers without color-mix() support, and reads computed token
// values for canvas/document surfaces that CSS cannot reach.
//
// color-mix() `in srgb` interpolates raw channel values, so the JS mirror
// is plain linear interpolation — no gamma handling.

export type RGB = { r: number; g: number; b: number };

const HEX6 = /^#[0-9a-f]{6}$/i;

/** Parses #rrggbb; returns null for anything else. */
export function parseHex(hex: string): RGB | null {
  if (!HEX6.test(hex)) return null;
  return {
    r: parseInt(hex.slice(1, 3), 16),
    g: parseInt(hex.slice(3, 5), 16),
    b: parseInt(hex.slice(5, 7), 16),
  };
}

export function toHex(c: RGB): string {
  const clamp = (v: number) => Math.max(0, Math.min(255, Math.round(v)));
  const pad = (v: number) => clamp(v).toString(16).padStart(2, "0");
  return `#${pad(c.r)}${pad(c.g)}${pad(c.b)}`;
}

/** color-mix(in srgb, hex N%, white): opaque lightening toward white. */
export function mixWithWhite(hex: string, whiteFraction: number): string {
  return mixWith(hex, "#ffffff", whiteFraction);
}

/** color-mix(in srgb, hex (1-w) + other w): opaque two-color mix. */
export function mixWith(hex: string, other: string, otherFraction: number): string {
  const base = parseHex(hex);
  const ink = parseHex(other);
  if (!base || !ink) return hex;
  const w = Math.max(0, Math.min(1, otherFraction));
  return toHex({
    r: base.r * (1 - w) + ink.r * w,
    g: base.g * (1 - w) + ink.g * w,
    b: base.b * (1 - w) + ink.b * w,
  });
}

/** color-mix(in srgb, hex N%, transparent): hue-preserving alpha scaling. */
export function withAlpha(hex: string, alphaFraction: number): string {
  const base = parseHex(hex);
  if (!base) return hex;
  const a = Math.max(0, Math.min(1, alphaFraction));
  return `rgba(${base.r}, ${base.g}, ${base.b}, ${Number(a.toFixed(3))})`;
}

/** Derivation spec mirroring styles/tokens.css (keep the two in sync):
 * tints mix toward transparent; text/ring mix toward the palette ink
 * endpoint (white on dark themes, black on light themes). */
export const ACCENT_DERIVATIONS = {
  tintAlpha: 0.22, // color-mix 22% accent + transparent
  tintStrongAlpha: 0.3, // color-mix 30% accent + transparent
  textInk: 0.45, // color-mix 55% accent + 45% ink
  ringInk: 0.55, // color-mix 45% accent + 55% ink
} as const;

export type DerivedTokens = {
  tint: string;
  tintStrong: string;
  text: string;
  ring: string;
};

/** JS mirror of the --accent-* / --focus-ring derivations for one accent.
 * Pass the theme's --pal-accent-ink as `ink` (default white = dark themes). */
export function deriveAccentTokens(accent: string, ink = "#ffffff"): DerivedTokens {
  return {
    tint: withAlpha(accent, ACCENT_DERIVATIONS.tintAlpha),
    tintStrong: withAlpha(accent, ACCENT_DERIVATIONS.tintStrongAlpha),
    text: mixWith(accent, ink, ACCENT_DERIVATIONS.textInk),
    ring: mixWith(accent, ink, ACCENT_DERIVATIONS.ringInk),
  };
}

/** True when the browser resolves color-mix() in authored styles. */
export function supportsColorMix(doc?: Document): boolean {
  const css = (doc ?? (typeof document !== "undefined" ? document : undefined))?.defaultView?.CSS;
  if (!css || typeof css.supports !== "function") return false;
  try {
    return css.supports("color", "color-mix(in srgb, red 50%, white)");
  } catch {
    return false;
  }
}

/** Reads a computed custom property value, trimmed; "" when unavailable. */
export function readToken(name: string, doc?: Document): string {
  const root = doc ?? (typeof document !== "undefined" ? document : undefined);
  const el = root?.documentElement;
  const view = root?.defaultView;
  if (!el || !view || typeof view.getComputedStyle !== "function") return "";
  try {
    return view.getComputedStyle(el).getPropertyValue(name).trim();
  } catch {
    return "";
  }
}

/** Canvas/document surfaces that CSS cannot reach, resolved from tokens
 * with the handoff literals as fallback (SSR/tests without a cascade). */
export const TOKEN_FALLBACKS = {
  progressComplete: "#3fb950",
  progressActive: "#2f9dff",
  track: "#2a2d33",
  labelIso: "#f59e0b",
  textFaint: "#7c828a",
  statusError: "#e0705a",
  bgApp: "#101214",
} as const;

function tokenOrFallback(name: string, fallback: string, doc?: Document): string {
  return readToken(name, doc) || fallback;
}

export type PieceMapColors = { done: string; working: string; missing: string; highlight: string };

/** Piece-map colors from semantic tokens (progress rule preserved: done
 * and working come from --progress-*, never --accent). */
export function pieceMapColors(doc?: Document): PieceMapColors {
  return {
    done: tokenOrFallback("--progress-complete", TOKEN_FALLBACKS.progressComplete, doc),
    working: tokenOrFallback("--progress-active", TOKEN_FALLBACKS.progressActive, doc),
    missing: tokenOrFallback("--bg-track", TOKEN_FALLBACKS.track, doc),
    highlight: tokenOrFallback("--label-iso", TOKEN_FALLBACKS.labelIso, doc),
  };
}

export type ConnectionDotColors = { connected: string; connecting: string; disconnected: string };

/** Favicon/status-dot colors from semantic tokens. */
export function connectionDotColors(doc?: Document): ConnectionDotColors {
  return {
    connected: tokenOrFallback("--progress-complete", TOKEN_FALLBACKS.progressComplete, doc),
    connecting: tokenOrFallback("--text-faint", TOKEN_FALLBACKS.textFaint, doc),
    disconnected: tokenOrFallback("--status-error", TOKEN_FALLBACKS.statusError, doc),
  };
}

/** theme-color meta value: the app background token. */
export function themeColor(doc?: Document): string {
  return tokenOrFallback("--bg-app", TOKEN_FALLBACKS.bgApp, doc);
}
