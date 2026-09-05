import { createEffect } from "solid-js";
import { aggregates, connection, globalStats } from "../store/session";
import { themeVersion } from "../store/ui";
import { connectionDotColors, themeColor } from "../lib/theme";
import { buildTitle, faviconHref } from "../lib/documentMeta";

/** Keeps the tab title (aggregate rates + active downloads), the favicon
 * (connection state, themed), and the theme-color meta (app background
 * token, THM-9.1) current. Mount once in App. */
export function DocumentMeta() {
  createEffect(() => {
    const stats = globalStats();
    const state = connection();
    // Tracked so accent/theme applications re-resolve token colors.
    themeVersion();
    document.title = buildTitle({
      connection: state,
      downRate: stats?.downRate ?? 0,
      upRate: stats?.upRate ?? 0,
      active: aggregates().status["downloading"] ?? 0,
    });
    const href = faviconHref(state, connectionDotColors());
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    if (link.href !== href) link.href = href;
    const color = themeColor();
    let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    if (!meta) {
      meta = document.createElement("meta");
      meta.name = "theme-color";
      document.head.appendChild(meta);
    }
    if (meta.content !== color) meta.content = color;
  });
  return null;
}
