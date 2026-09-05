// Global keyboard bindings (POL-8.5): ONE table drives both matching and
// the `?` help overlay, so hints can never drift from behavior. Modifiers:
// `mod: "none"` requires no Ctrl/Meta/Alt (Shift still allowed unless
// stated); `mod: "ctrl"` accepts Ctrl or Cmd. Single-character keys match
// case-insensitively. Runs return whether they acted, so preventDefault only
// fires when something happened.
export type ShortcutId =
  | "blur"
  | "help"
  | "focus-filter"
  | "add"
  | "recheck"
  | "detail-tab"
  | "toggle-detail"
  | "select-all"
  | "move-up"
  | "move-down"
  | "page-up"
  | "page-down"
  | "home"
  | "end"
  | "toggle-start"
  | "remove";

export type ShortcutDef = {
  id: ShortcutId;
  /** Display form for the help overlay (e.g. "Ctrl+A", "1–9"). */
  keys: string;
  label: string;
  key: string | string[];
  mod?: "none" | "ctrl";
  shift?: "any" | true | false;
};

export const SHORTCUTS: ShortcutDef[] = [
  { id: "blur", keys: "Esc", label: "Close dialog / blur focused control", key: "Escape" },
  { id: "help", keys: "?", label: "Keyboard shortcut help", key: "?" },
  { id: "focus-filter", keys: "/", label: "Focus torrent filter", key: "/", mod: "none" },
  { id: "add", keys: "A", label: "Add torrent", key: "a", mod: "none" },
  { id: "recheck", keys: "R", label: "Force recheck selected", key: "r", mod: "none" },
  {
    id: "detail-tab",
    keys: "1–9",
    label: "Detail tabs (Files … Why?)",
    key: ["1", "2", "3", "4", "5", "6", "7", "8", "9"],
    mod: "none",
  },
  { id: "toggle-detail", keys: "F", label: "Toggle detail panel", key: "f", mod: "none" },
  { id: "select-all", keys: "Ctrl+A", label: "Select all visible", key: "a", mod: "ctrl" },
  { id: "move-up", keys: "↑", label: "Move focus up", key: "ArrowUp" },
  { id: "move-down", keys: "↓", label: "Move focus down", key: "ArrowDown" },
  { id: "page-up", keys: "PgUp", label: "Move focus one page up", key: "PageUp" },
  { id: "page-down", keys: "PgDn", label: "Move focus one page down", key: "PageDown" },
  { id: "home", keys: "Home", label: "Focus first row", key: "Home" },
  { id: "end", keys: "End", label: "Focus last row", key: "End" },
  {
    id: "toggle-start",
    keys: "Space",
    label: "Start / pause selected",
    key: [" ", "Space", "Spacebar"],
  },
  {
    id: "remove",
    keys: "Del",
    label: "Remove selected (Shift: with data)",
    key: ["Delete", "Del", "Backspace"],
  },
];

function keyMatches(def: ShortcutDef, key: string): boolean {
  const candidates = Array.isArray(def.key) ? def.key : [def.key];
  return candidates.some((candidate) =>
    candidate.length === 1 && key.length === 1
      ? candidate.toLowerCase() === key.toLowerCase()
      : candidate === key,
  );
}

/** Finds the binding for a key event, or null. Order matters: Escape first,
 * then exact-modifier letters before motion keys, mirroring the old chain. */
export function matchShortcut(event: {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}): ShortcutDef | null {
  for (const def of SHORTCUTS) {
    if (!keyMatches(def, event.key)) continue;
    if (def.mod === "ctrl") {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) continue;
    } else if (def.mod === "none") {
      if (event.ctrlKey || event.metaKey || event.altKey) continue;
    }
    if (def.shift === true && !event.shiftKey) continue;
    if (def.shift === false && event.shiftKey) continue;
    return def;
  }
  return null;
}
