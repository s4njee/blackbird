// Hash routing (POL-8.6): routes plus `filter`/`focus` query state live in
// the location hash, so deep links survive reloads and back/forward work
// without any server cooperation (hash is never sent over the wire, which
// also keeps it correct behind `base_url` prefixes). Pure parse/build;
// store sync lives in store/ui.ts.
export type RouteName =
  "console" | "settings" | "stats" | "rss" | "history" | "attention" | "preservation";

export type ParsedRoute = {
  route: RouteName;
  /** Settings section slug, e.g. "Bandwidth". */
  section: string;
  /** Search filter text. */
  filter: string;
  /** Focused torrent hash. */
  focus: string;
};

const ROUTES: RouteName[] = [
  "console",
  "settings",
  "stats",
  "rss",
  "history",
  "attention",
  "preservation",
];

function isRouteName(value: string): value is RouteName {
  return (ROUTES as string[]).includes(value);
}

/** Parses a location hash (`#/settings/Bandwidth?filter=x&focus=h`). */
export function parseHash(hash: string): ParsedRoute {
  const fallback: ParsedRoute = { route: "console", section: "", filter: "", focus: "" };
  const stripped = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!stripped) return fallback;
  const [path, query] = stripped.split("?", 2);
  const segments = path.split("/").filter(Boolean);
  if (segments.length && !isRouteName(segments[0])) return fallback;
  const route: RouteName = (segments[0] as RouteName) || "console";
  const params = new URLSearchParams(query ?? "");
  return {
    route,
    section: route === "settings" ? (segments[1] ?? "") : "",
    filter: params.get("filter") ?? "",
    focus: params.get("focus") ?? "",
  };
}

/** Builds a location hash from route state. Query rides only the console. */
export function buildHash(state: {
  route: RouteName;
  section?: string;
  filter?: string;
  focus?: string;
}): string {
  const params = new URLSearchParams();
  if (state.filter) params.set("filter", state.filter);
  if (state.focus) params.set("focus", state.focus);
  const query = params.toString();
  let path = "#/";
  if (state.route === "settings") {
    path = state.section ? `#/settings/${encodeURIComponent(state.section)}` : "#/settings";
  } else if (state.route !== "console") {
    path = `#/${state.route}`;
  } else if (query) {
    path = "#/";
  }
  return query && state.route === "console" ? `${path}?${query}` : path;
}
