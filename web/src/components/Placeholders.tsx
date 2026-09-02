// Structural placeholders for the surfaces that land in later Epic 5 stories.
// They reserve the design's fixed heights/widths so the shell (5.1) already
// reads correctly, and each carries a tag identifying its future story.

export function ToolbarPlaceholder() {
  return (
    <div class="toolbar">
      <span class="placeholder-tag">Actions — Epic 5.6</span>
    </div>
  );
}

export function SidebarPlaceholder() {
  return (
    <aside class="sidebar">
      <span class="placeholder-tag">Sidebar — Epic 5.5</span>
    </aside>
  );
}

export function TablePlaceholder() {
  return (
    <div class="table-area">
      <span class="placeholder-tag">Torrent table — Epic 5.2 · filter &amp; sort — Epic 5.4</span>
    </div>
  );
}