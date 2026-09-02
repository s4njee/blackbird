import { createMemo } from "solid-js";
import { splitRate } from "../lib/format";
import { globalStats } from "../store/session";
import { openAdd, saveCurrentFilter, setQuery, query, route, setRoute, settingsDirty, showToast } from "../store/ui";
import { Sparkline } from "./Sparkline";

/** Top bar (44px): brand, live global rates, sparkline, filter, actions. */
export function TopBar() {
  const rates = createMemo(() => {
    const g = globalStats();
    return {
      down: g ? splitRate(g.downRate) : { value: "—", unit: "" },
      up: g ? splitRate(g.upRate) : { value: "—", unit: "" },
    };
  });

  return (
    <header class="topbar">
      <img class="logo-mark" src="/icon.jpg" alt="" aria-hidden="true" />
      <span class="wordmark">Blackbird</span>
      <span class="tb-divider" aria-hidden="true" />
      <div class="rates">
        <span class="rate">
          <span class="rate-glyph down" aria-hidden="true">▼</span>
          <span class="tnum rate-value">{rates().down.value}</span>
          <span class="rate-unit">{rates().down.unit}</span>
        </span>
        <span class="rate">
          <span class="rate-glyph up" aria-hidden="true">▲</span>
          <span class="tnum rate-value">{rates().up.value}</span>
          <span class="rate-unit">{rates().up.unit}</span>
        </span>
      </div>
      <Sparkline />
      <span class="tb-spacer" />
      <div class="filter">
        <span class="filter-glyph" aria-hidden="true">⌕</span>
        <input
          class="filter-input"
          type="text"
          placeholder="Filter torrents…"
          value={query()}
          onInput={(e) => setQuery(e.currentTarget.value)}
          spellcheck={false}
        />
        <details class="filter-help">
          <summary aria-label="Search help" title="Search syntax">?</summary>
          <div class="filter-help-popover">
            <b>Search and filters</b>
            <span>Plain text searches name, hash prefix, path, tracker, and message.</span>
            <span><code>label:</code> <code>tracker:</code> <code>path:</code> <code>status:</code></span>
            <span><code>ratio&gt;1.5</code> <code>size&lt;4GB</code></span>
            <button type="button" onClick={() => { saveCurrentFilter(); showToast("Filter saved to the sidebar."); }}>Save current filter</button>
          </div>
        </details>
      </div>
      <button class="btn-add" type="button" onClick={() => openAdd()}>
        + Add torrent
      </button>
      <button
        class="btn-icon"
        type="button"
        title={route() === "stats" ? "Back to console" : "Session statistics"}
        aria-label={route() === "stats" ? "Back to console" : "Session statistics"}
        onClick={() => setRoute(route() === "stats" ? "console" : "stats")}
      >
        ▥
      </button>
      <button
        class="btn-icon"
        type="button"
        title={route() === "settings" ? "Back to console" : "Settings"}
        aria-label={route() === "settings" ? "Back to console" : "Settings"}
        onClick={() => {
          if (route() === "settings" && settingsDirty() && !window.confirm("Discard unsaved settings changes?")) return;
          setRoute(route() === "settings" ? "console" : "settings");
        }}
      >
        ⚙
      </button>
    </header>
  );
}
