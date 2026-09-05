// @vitest-environment happy-dom
// UI store behavior through the public API (POL-8.1): layout, sort,
// selection, filters, and detail prefs. Each test resets what it touches —
// module state and happy-dom localStorage persist across tests in this file.
import { describe, expect, it, vi } from "vitest";
import {
  applySavedFilter,
  applyAccent,
  changeSort,
  clearFilters,
  columnLayout,
  focusedHash,
  hydrateAppearance,
  hydrateSortFromConfig,
  jumpSelection,
  loadLastRoute,
  moveSelectionBy,
  navigate,
  pruneSelection,
  removeSavedFilter,
  reorderColumns,
  resetColumns,
  resetLayout,
  resetPeerColumns,
  restoreSelection,
  saveCurrentFilter,
  savedFilters,
  selectAllVisible,
  selectedHashes,
  selectedSet,
  selectOnly,
  selectRange,
  setColumnWidth,
  setDetailCollapsed,
  setDetailHeight,
  setDetailTab,
  detailPrefs,
  setSettingsSection,
  setSidebarWidth,
  settingsSection,
  sidebarWidth,
  sort,
  setFilter,
  filters,
  setQuery,
  query,
  route,
  toggleColumn,
  togglePeerColumn,
  toggleSelection,
  visibleColumnKeys,
  visiblePeerColumnKeys,
} from "../src/store/ui.js";
import { DEFAULT_COLUMN_KEYS } from "../src/lib/columns.js";
import { formatPrefs, setFormatPrefs } from "../src/lib/format.js";

describe("ui store", () => {
  it("toggles, reorders, resizes, and resets columns", () => {
    resetColumns();
    expect(visibleColumnKeys()).toEqual(DEFAULT_COLUMN_KEYS);
    toggleColumn("sizeBytes");
    expect(visibleColumnKeys()).not.toContain("sizeBytes");
    toggleColumn("sizeBytes");
    expect(visibleColumnKeys()).toContain("sizeBytes");
    toggleColumn("name");
    expect(visibleColumnKeys()).toContain("name");
    const before = columnLayout().order.indexOf("label");
    reorderColumns("label", "name");
    expect(columnLayout().order.indexOf("label")).toBeLessThan(before);
    setColumnWidth("label", 5);
    expect(columnLayout().widths["label"]).toBeGreaterThanOrEqual(88);
    resetColumns();
    expect(visibleColumnKeys()).toEqual(DEFAULT_COLUMN_KEYS);
  });

  it("changes primary and secondary sort keys", () => {
    changeSort("name");
    expect(sort()[0]).toEqual({ column: "name", direction: "asc" });
    changeSort("name");
    expect(sort()[0]).toEqual({ column: "name", direction: "desc" });
    changeSort("downRate", true);
    expect(sort().length).toBe(2);
    expect(sort()[1].column).toBe("downRate");
    changeSort("addedAt");
  });

  it("moves selection by offset and jumps to the ends", () => {
    const hashes = ["a", "b", "c", "d"];
    selectOnly("b");
    moveSelectionBy(1, hashes, false);
    expect(focusedHash()).toBe("c");
    moveSelectionBy(-2, hashes, false);
    expect(focusedHash()).toBe("a");
    moveSelectionBy(-5, hashes, false);
    expect(focusedHash()).toBe("a");
    jumpSelection("last", hashes, false);
    expect(focusedHash()).toBe("d");
    jumpSelection("first", hashes, false);
    expect(focusedHash()).toBe("a");
    moveSelectionBy(1, hashes, true);
    expect([...selectedHashes()].sort()).toEqual(["a", "b"]);
    selectAllVisible([]);
  });

  it("selects ranges, toggles, and prunes against the live session", () => {
    selectOnly("a");
    selectRange("c", ["a", "b", "c", "d"]);
    expect([...selectedSet()].sort()).toEqual(["a", "b", "c"]);
    toggleSelection("b");
    expect(selectedSet().has("b")).toBe(false);
    selectAllVisible(["a", "b"]);
    expect(selectedHashes()).toEqual(["a", "b"]);
    pruneSelection(new Set(["a", "b"]));
    expect(selectedSet().size).toBe(2);
    pruneSelection(new Set(["a"]));
    expect([...selectedSet()]).toEqual(["a"]);
    selectAllVisible([]);
  });

  it("sets, clears, saves, applies, and removes filters", () => {
    setFilter("status", "downloading");
    expect(filters().status).toBe("downloading");
    setFilter("status", "downloading");
    expect(filters().status).toBe("");
    setQuery("ubuntu");
    expect(query()).toBe("ubuntu");
    setFilter("label", "iso");
    saveCurrentFilter("e2e-tmp-filter");
    const saved = savedFilters().find((f) => f.name === "e2e-tmp-filter");
    expect(saved?.query).toBe("ubuntu");
    expect(saved?.label).toBe("iso");
    clearFilters();
    expect(filters().label).toBe("");
    applySavedFilter(saved!);
    expect(filters().label).toBe("iso");
    expect(query()).toBe("ubuntu");
    removeSavedFilter(saved!.id);
    expect(savedFilters().some((f) => f.name === "e2e-tmp-filter")).toBe(false);
    clearFilters();
  });

  it("persists detail panel prefs", () => {
    setDetailTab("peers");
    setDetailHeight(400);
    setDetailCollapsed(true);
    expect(detailPrefs()).toEqual({ tab: "peers", height: 400, collapsed: true });
    setDetailHeight(5);
    expect(detailPrefs().height).toBeGreaterThanOrEqual(120);
    setDetailTab("files");
    setDetailHeight(288);
    setDetailCollapsed(false);
  });

  it("toggles peer columns and restores defaults", () => {
    resetPeerColumns();
    const full = visiblePeerColumnKeys();
    togglePeerColumn("client");
    expect(visiblePeerColumnKeys()).not.toContain("client");
    togglePeerColumn("client");
    expect(visiblePeerColumnKeys()).toEqual(full);
    resetPeerColumns();
  });

  it("seeds the sort store from the operator YAML (ui.sort)", () => {
    hydrateSortFromConfig({ column: "name", direction: "asc" });
    expect(sort()[0]).toEqual({ column: "name", direction: "asc" });
    hydrateSortFromConfig({ column: "bogus", direction: "sideways" });
    expect(sort()[0]).toEqual({ column: "name", direction: "asc" });
    hydrateSortFromConfig({ column: "addedAt", direction: "desc" });
  });

  it("hydrates appearance prefs and applies the accent at boot (ui.accent)", () => {
    hydrateAppearance({ accent: "#ff0000", date_format: "iso", rate_format: "decimal" });
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("#ff0000");
    expect(formatPrefs()).toEqual({ rateFormat: "decimal", dateFormat: "iso" });
    // Invalid values keep the previous prefs and leave the accent alone.
    hydrateAppearance({ accent: "amber", date_format: "martian", rate_format: "roman" });
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("#ff0000");
    expect(formatPrefs()).toEqual({ rateFormat: "decimal", dateFormat: "iso" });
    applyAccent("#35418f");
    setFormatPrefs({ rateFormat: "binary", dateFormat: "local" });
  });

  it("ignores invalid accents without touching the document", () => {
    applyAccent("#35418f");
    applyAccent("not-a-color");
    applyAccent("#12345");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("#35418f");
  });

  it("navigates routes and settings sections with URL sync", async () => {
    const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
    navigate("stats");
    expect(route()).toBe("stats");
    await tick();
    expect(window.location.hash).toBe("#/stats");
    navigate("settings", "Bandwidth");
    expect(route()).toBe("settings");
    expect(settingsSection()).toBe("Bandwidth");
    await tick();
    expect(window.location.hash).toBe("#/settings/Bandwidth");
    navigate("settings", "Bogus");
    expect(settingsSection()).toBe("Connection");
    navigate("console");
    setSettingsSection("Connection");
    await tick();
  });

  it("restores surviving selection against the live session", () => {
    restoreSelection(["a", "b", "c"], "b", new Set(["a", "c"]));
    expect([...selectedHashes()].sort()).toEqual(["a", "c"]);
    expect(focusedHash()).toBe("a");
    restoreSelection(["gone"], "gone", new Set());
    expect(selectedHashes()).toEqual([]);
    expect(focusedHash()).toBe("");
    selectAllVisible([]);
  });

  it("clamps sidebar width and resets the whole layout", () => {
    setSidebarWidth(500);
    expect(sidebarWidth()).toBe(340);
    setSidebarWidth(10);
    expect(sidebarWidth()).toBe(140);
    toggleColumn("sizeBytes");
    changeSort("name");
    setDetailTab("peers");
    resetLayout();
    expect(sidebarWidth()).toBe(196);
    expect(visibleColumnKeys()).toEqual(DEFAULT_COLUMN_KEYS);
    expect(sort()).toEqual([{ column: "addedAt", direction: "desc" }]);
    expect(detailPrefs()).toEqual({ tab: "files", height: 288, collapsed: false });
    expect(route()).toBe("console");
  });

  it("persists the last route per browser", async () => {
    const backing = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (key: string) => (backing.has(key) ? backing.get(key)! : null),
      setItem: (key: string, value: string) => void backing.set(key, value),
      removeItem: (key: string) => void backing.delete(key),
      clear: () => backing.clear(),
    });
    try {
      navigate("rss");
      expect(loadLastRoute()).toBe("rss");
      navigate("console");
      expect(loadLastRoute()).toBe("console");
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
