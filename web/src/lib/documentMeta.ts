// Document title and favicon builders (POL-8.3). Pure: the DocumentMeta
// component applies them, unit tests pin the strings.
import { formatRate } from "./format";

export type ConnectionState = "connecting" | "connected" | "disconnected";

/** Tab title: live aggregate rates plus the active-download count while
 * connected, the bare product name otherwise. */
export function buildTitle(args: {
  connection: ConnectionState;
  downRate: number;
  upRate: number;
  active: number;
}): string {
  if (args.connection !== "connected") return "Blackbird Console";
  return `↓ ${formatRate(args.downRate)} ↑ ${formatRate(args.upRate)} · ${args.active} active — Blackbird Console`;
}

const FAVICON_COLORS: Record<ConnectionState, string> = {
  connected: "#3fb950",
  connecting: "#7c828a",
  disconnected: "#e0705a",
};

/** Data-URL dot favicon reflecting connection state (handoff status hues).
 * Pass themed colors (lib/theme connectionDotColors) so the dot follows the
 * palette; the literals above cover non-DOM environments. */
export function faviconHref(
  state: ConnectionState,
  colors: Record<ConnectionState, string> = FAVICON_COLORS,
): string {
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><circle cx='8' cy='8' r='6' fill='${colors[state]}'/></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}
