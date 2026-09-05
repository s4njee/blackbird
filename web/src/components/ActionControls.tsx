import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { copyText, magnetFor } from "../lib/clipboard";
import {
  connected,
  patchTorrents,
  restoreTorrents,
  torrentList,
  visibleHashes,
  visibleRows,
} from "../store/session";
import { channels, refreshThrottles } from "../store/throttles";
import { confirmDialog, dialogRequest, promptDialog } from "../store/dialog";
import { policy as seedingPolicy, refreshSeeding } from "../store/seeding";
import {
  daemonInfo,
  DETAIL_TABS,
  detailPrefs,
  jumpSelection,
  menuOpen,
  moveSelection,
  moveSelectionBy,
  openAdd,
  openMove,
  route,
  selectAllVisible,
  selectedHashes,
  setDetailCollapsed,
  setDetailTab,
  setHelpOpen,
  showToast,
} from "../store/ui";
import { matchShortcut, type ShortcutDef } from "../lib/shortcuts";
import type { Torrent } from "../lib/types";
import type { ContextTarget } from "./TorrentTable";

type Action =
  | "start"
  | "force_start"
  | "pause"
  | "stop"
  | "recheck"
  | "reannounce"
  | "remove"
  | "remove_with_data"
  | "set_label"
  | "move_data"
  | "priority"
  | "superseed"
  | "sequential"
  | "save_session"
  | "set_custom"
  | "rename"
  | "set_throttle";
type ActionOptions = {
  label?: string;
  destination?: string;
  priority?: number;
  enabled?: boolean;
  name?: string;
  customField?: "custom2" | "custom3" | "custom4" | "custom5";
  customValue?: string;
  throttle?: string;
};

async function sendAction(action: Action, hashes: string[], options: ActionOptions = {}) {
  const response = await fetch("/api/v1/torrents/action", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ action, hashes, ...options }),
  });
  const data = (await response.json().catch(() => ({}))) as {
    message?: string;
    results?: Array<{ hash: string; ok: boolean; error?: string }>;
  };
  if (!response.ok) throw new Error(data.message || "Action failed");
  const failures = (data.results ?? []).filter((result) => !result.ok);
  if (failures.length)
    throw new Error(failures.map((result) => result.error || result.hash).join("; "));
}

function actionLabel(action: Action) {
  return (
    {
      start: "Start",
      force_start: "Force start",
      pause: "Pause",
      stop: "Stop",
      recheck: "Force recheck",
      reannounce: "Force reannounce",
      remove: "Remove",
      remove_with_data: "Remove + data",
      set_label: "Set label",
      move_data: "Move data",
      priority: "Set priority",
      superseed: "Superseeding",
      sequential: "Sequential download",
      save_session: "Save session",
      set_custom: "Set custom field",
      rename: "Rename",
      set_throttle: "Set throttle",
    } as Record<Action, string>
  )[action];
}

function optimisticPatch(action: Action, options: ActionOptions): Partial<Torrent> | null {
  if (action === "start" || action === "force_start")
    return { state: "downloading", downRate: 0, upRate: 0 };
  if (action === "pause" || action === "stop") return { state: "stopped", downRate: 0, upRate: 0 };
  if (action === "priority" && options.priority !== undefined)
    return { priority: options.priority };
  if (action === "superseed" && options.enabled !== undefined)
    return { superseeding: options.enabled };
  if (action === "sequential" && options.enabled !== undefined)
    return { sequential: options.enabled };
  if (action === "rename" && options.name !== undefined) return { name: options.name };
  if (action === "set_throttle" && options.throttle !== undefined)
    return { throttle: options.throttle };
  if (action === "set_custom" && options.customField) {
    const patch = { [options.customField]: options.customValue ?? "" } as Partial<Torrent>;
    if (options.customField === "custom2") patch.ratioGroup = options.customValue ?? "";
    return patch;
  }
  return null;
}

function isEditableTarget(target: EventTarget | null) {
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement ||
    (target instanceof HTMLElement && target.isContentEditable)
  );
}

/** "3 torrents" / "1 torrent" style counts for confirmations. */
function plural(count: number, word: string): string {
  return `${count} ${word}${count === 1 ? "" : "s"}`;
}

/** Restores pre-change labels per distinct previous value (Undo action). */
function undoSetLabel(previous: Torrent[]) {
  const groups = new Map<string, string[]>();
  for (const torrent of previous) {
    const list = groups.get(torrent.label) ?? [];
    list.push(torrent.hash);
    groups.set(torrent.label, list);
  }
  void (async () => {
    for (const [label, hashes] of groups) {
      try {
        await sendAction("set_label", hashes, { label });
      } catch (error) {
        showToast(error instanceof Error ? error.message : "Action failed", { kind: "error" });
        return;
      }
    }
    const count = previous.length;
    showToast(`Label change undone for ${count} torrent${count === 1 ? "" : "s"}.`, {
      kind: "success",
    });
  })();
}

function pluralTorrents(count: number): string {
  return `${count} torrent${count === 1 ? "" : "s"}`;
}

/** Distinct base paths for a hash selection (data-removal confirmations). */
function selectionPaths(hashes: string[]): string[] {
  return [
    ...new Set(
      torrentList()
        .filter((torrent) => hashes.includes(torrent.hash))
        .map((torrent) => torrent.directory || torrent.basePath)
        .filter(Boolean),
    ),
  ];
}

/** One destructive confirmation for both remove flows (POL-8.2): names the
 * torrent count and, for data removal, the affected paths. Single confirm,
 * no double-confirm. Plain removal offers "don't ask again for this
 * session"; data removal always asks. */
async function confirmRemoveTorrent(hashes: string[], withData: boolean): Promise<boolean> {
  const count = hashes.length;
  if (!withData) {
    return confirmDialog({
      title: "Remove torrents",
      body: `Remove ${count} torrent${count === 1 ? "" : "s"} from the session? Data on disk is kept.`,
      confirmLabel: "Remove",
      skipKey: "remove-torrents",
    });
  }
  return confirmDialog({
    title: "Remove torrents and data",
    body: `Remove ${plural(count, "torrent")} and delete ${count === 1 ? "its" : "their"} data? This cannot be undone.`,
    details: selectionPaths(hashes),
    confirmLabel: "Remove + data",
    danger: true,
  });
}

export function ActionToolbar(props: { onOpenMenu: (target: ContextTarget) => void }) {
  const selection = selectedHashes;
  const enabled = createMemo(() => connected() && selection().length > 0);
  const perform = async (action: Action, options?: ActionOptions) => {
    const hashes = selection();
    if (!hashes.length || !connected()) return;
    const previous = torrentList().filter((torrent) => hashes.includes(torrent.hash));
    const patch = optimisticPatch(action, options ?? {});
    if (patch) patchTorrents(hashes, patch);
    try {
      await sendAction(action, hashes, options);
      if (action === "set_label") {
        showToast(`Label set for ${pluralTorrents(hashes.length)}.`, {
          kind: "success",
          action: { label: "Undo", run: () => undoSetLabel(previous) },
        });
      } else {
        showToast(`${actionLabel(action)} queued for ${pluralTorrents(hashes.length)}.`, {
          kind: "success",
        });
      }
    } catch (error) {
      if (patch) restoreTorrents(previous);
      showToast(error instanceof Error ? error.message : "Action failed", {
        kind: "error",
        action: { label: "Retry", run: () => void perform(action, options) },
      });
    }
  };
  const setLabel = async () => {
    const label = await promptDialog({
      title: "Set label",
      label: "Label for selected torrents",
      confirmLabel: "Set label",
    });
    if (typeof label === "string") void perform("set_label", { label: label.trim() });
  };
  const move = () => openMove(selection());
  const remove = async (withData = false) => {
    const hashes = selection();
    if (!hashes.length) return;
    if (await confirmRemoveTorrent(hashes, withData))
      void perform(withData ? "remove_with_data" : "remove");
  };
  // Table-driven dispatch (POL-8.5): matchShortcut finds the binding, each
  // arm returns whether it acted so preventDefault fires only then.
  const runShortcut = (def: ShortcutDef, event: KeyboardEvent): boolean => {
    switch (def.id) {
      case "focus-filter":
        document.querySelector<HTMLInputElement>(".filter-input")?.focus();
        return true;
      case "add":
        openAdd();
        return true;
      case "recheck":
        if (!selection().length) return false;
        void perform("recheck");
        return true;
      case "detail-tab": {
        if (route() !== "console") return false;
        const index = Number(event.key) - 1;
        if (index < 0 || index >= DETAIL_TABS.length) return false;
        setDetailCollapsed(false);
        setDetailTab(DETAIL_TABS[index]);
        return true;
      }
      case "toggle-detail":
        if (route() !== "console") return false;
        setDetailCollapsed(!detailPrefs().collapsed);
        return true;
      case "help":
        setHelpOpen(true);
        return true;
      case "select-all":
        selectAllVisible(visibleHashes());
        return true;
      case "move-up":
        moveSelection(-1, visibleHashes(), event.shiftKey);
        return true;
      case "move-down":
        moveSelection(1, visibleHashes(), event.shiftKey);
        return true;
      case "page-up":
        moveSelectionBy(-20, visibleHashes(), event.shiftKey);
        return true;
      case "page-down":
        moveSelectionBy(20, visibleHashes(), event.shiftKey);
        return true;
      case "home":
        jumpSelection("first", visibleHashes(), event.shiftKey);
        return true;
      case "end":
        jumpSelection("last", visibleHashes(), event.shiftKey);
        return true;
      case "toggle-start": {
        const targeted = torrentList().filter((torrent) => selectedHashes().includes(torrent.hash));
        if (!targeted.length) return false;
        const pause = targeted.every(
          (torrent) => torrent.state === "downloading" || torrent.state === "seeding",
        );
        void perform(pause ? "pause" : "start");
        return true;
      }
      case "remove":
        if (!selectedHashes().length) return false;
        void remove(event.shiftKey);
        return true;
      case "blur":
        return false;
    }
  };
  onMount(() => {
    void refreshThrottles();
    void refreshSeeding();
    const keydown = (event: KeyboardEvent) => {
      // A modal dialog or the context menu owns the keyboard while open
      // (POL-8.2/8.5): global shortcuts stay out so stacked UI can never
      // double-fire.
      if (dialogRequest() || menuOpen()) return;
      // Solid delegates every handler at document level, so stopPropagation
      // in a nested widget cannot suppress this listener: tab lists, menus,
      // and dialogs own their keys by ancestry instead.
      if (
        event.target instanceof HTMLElement &&
        event.target.closest('[role="tablist"], [role="dialog"], [role="menu"]')
      )
        return;
      const editable = isEditableTarget(event.target);
      if (event.key === "Escape") {
        if (
          document.activeElement instanceof HTMLElement &&
          document.activeElement !== document.body
        )
          document.activeElement.blur();
        return;
      }
      if (editable || event.repeat) return;
      const def = matchShortcut(event);
      if (def && runShortcut(def, event)) event.preventDefault();
    };
    document.addEventListener("keydown", keydown);
    onCleanup(() => document.removeEventListener("keydown", keydown));
  });
  return (
    <ToolbarView
      enabled={enabled()}
      onAction={perform}
      onSetLabel={setLabel}
      onMove={move}
      onRemove={remove}
      onOpenMenu={props.onOpenMenu}
    />
  );
}

function ToolbarView(props: {
  enabled: boolean;
  onAction: (action: Action, options?: ActionOptions) => void;
  onSetLabel: () => void;
  onMove: () => void;
  onRemove: (withData?: boolean) => void;
  onOpenMenu: (target: ContextTarget) => void;
}) {
  const openMenu = (event: MouseEvent) =>
    props.onOpenMenu({ x: event.clientX, y: event.clientY, hashes: selectedHashes() });
  return (
    <div class="toolbar">
      <button
        class="toolbar-button transport"
        disabled={!props.enabled}
        onClick={() => props.onAction("start")}
      >
        <i>▶</i>Start
      </button>
      <button
        class="toolbar-button transport"
        disabled={!props.enabled}
        onClick={() => props.onAction("force_start")}
      >
        Force start
      </button>
      <button
        class="toolbar-button transport"
        disabled={!props.enabled}
        onClick={() => props.onAction("pause")}
      >
        <i>❙❙</i>Pause
      </button>
      <button
        class="toolbar-button transport"
        disabled={!props.enabled}
        onClick={() => props.onAction("stop")}
      >
        <i>■</i>Stop
      </button>
      <span class="toolbar-divider" />
      <button
        class="toolbar-button"
        disabled={!props.enabled}
        onClick={() => props.onAction("recheck")}
      >
        Force recheck
      </button>
      <button class="toolbar-button" disabled={!props.enabled} onClick={props.onSetLabel}>
        Set label
      </button>
      <button class="toolbar-button" disabled={!props.enabled} onClick={props.onMove}>
        Move data
      </button>
      <label class="toolbar-priority">
        Priority{" "}
        <select
          disabled={!props.enabled}
          value=""
          onChange={(event) => {
            const value = event.currentTarget.value;
            if (value !== "") props.onAction("priority", { priority: Number(value) });
            event.currentTarget.value = "";
          }}
        >
          <option value="">Set…</option>
          <option value="0">Off</option>
          <option value="1">Low</option>
          <option value="2">Normal</option>
          <option value="3">High</option>
        </select>
      </label>
      <Show when={channels().length}>
        <label class="toolbar-priority">
          Throttle{" "}
          <select
            disabled={!props.enabled}
            value=""
            onChange={(event) => {
              const value = event.currentTarget.value;
              if (value !== "")
                props.onAction("set_throttle", { throttle: value === "none" ? "" : value });
              event.currentTarget.value = "";
            }}
          >
            <option value="">Set…</option>
            <option value="none">None</option>
            <For each={channels()}>
              {(channel) => <option value={channel.name}>{channel.name}</option>}
            </For>
          </select>
        </label>
      </Show>
      <button
        class="toolbar-button destructive"
        disabled={!props.enabled}
        onClick={() => props.onRemove(false)}
      >
        Remove
      </button>
      <span class="toolbar-spacer" />
      <button
        class="toolbar-more"
        disabled={!props.enabled}
        onClick={openMenu}
        title="More actions"
      >
        •••
      </button>
      <span class="selection-readout tnum">
        {selectedHashes().length} selected · {visibleRows().length} of {torrentList().length} shown
      </span>
    </div>
  );
}

export function ContextMenu(props: { target: ContextTarget | null; onClose: () => void }) {
  const [labelsOpen, setLabelsOpen] = createSignal(false);
  const [priorityOpen, setPriorityOpen] = createSignal(false);
  const [throttleOpen, setThrottleOpen] = createSignal(false);
  const [ratioGroupOpen, setRatioGroupOpen] = createSignal(false);
  const [advancedOpen, setAdvancedOpen] = createSignal(false);
  const [copyOpen, setCopyOpen] = createSignal(false);
  const labels = createMemo(() =>
    [
      ...new Set(
        torrentList()
          .map((torrent) => torrent.label)
          .filter(Boolean),
      ),
    ].sort(),
  );
  const target = () => props.target;
  let menuRef: HTMLDivElement | undefined;
  let typeBuffer = "";
  let typeTimer: number | undefined;

  // Initial focus lands on the first item so arrows work immediately.
  createEffect(() => {
    if (target()) {
      const id = window.setTimeout(() => {
        menuRef?.querySelector<HTMLButtonElement>(".menu-item")?.focus();
      }, 0);
      onCleanup(() => window.clearTimeout(id));
    }
  });

  /** Arrow/Home/End/type-ahead navigation across rendered items (POL-8.5).
   * Handled keys stop here so global shortcuts never double-fire. */
  const menuKey = (event: KeyboardEvent) => {
    if (event.key === "Tab") return;
    // Space/Enter activate the focused item natively; keep them from
    // reaching global shortcuts (Space would toggle the session).
    if (event.key === " " || event.key === "Enter") {
      event.stopPropagation();
      return;
    }
    event.stopPropagation();
    const items = Array.from(menuRef?.querySelectorAll<HTMLButtonElement>(".menu-item") ?? []);
    if (!items.length) return;
    const index = items.findIndex((el) => el === document.activeElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const next =
        event.key === "ArrowDown"
          ? (index + 1) % items.length
          : (index - 1 + items.length) % items.length;
      items[next].focus();
    } else if (event.key === "Home") {
      event.preventDefault();
      items[0].focus();
    } else if (event.key === "End") {
      event.preventDefault();
      items[items.length - 1].focus();
    } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      if (typeTimer) window.clearTimeout(typeTimer);
      typeTimer = window.setTimeout(() => {
        typeBuffer = "";
      }, 500);
      typeBuffer += event.key.toLowerCase();
      const hit = items.find((el) => el.textContent?.trim().toLowerCase().startsWith(typeBuffer));
      if (hit) {
        event.preventDefault();
        hit.focus();
      }
    }
  };
  const act = async (action: Action, options?: ActionOptions) => {
    const context = target();
    if (!context || !connected()) return;
    const previous = torrentList().filter((torrent) => context.hashes.includes(torrent.hash));
    const patch = optimisticPatch(action, options ?? {});
    if (patch) patchTorrents(context.hashes, patch);
    props.onClose();
    try {
      await sendAction(action, context.hashes, options);
      if (action === "set_label") {
        showToast(`Label set for ${pluralTorrents(context.hashes.length)}.`, {
          kind: "success",
          action: { label: "Undo", run: () => undoSetLabel(previous) },
        });
      } else {
        showToast(`${actionLabel(action)} queued for ${pluralTorrents(context.hashes.length)}.`, {
          kind: "success",
        });
      }
    } catch (error) {
      if (patch) restoreTorrents(previous);
      showToast(error instanceof Error ? error.message : "Action failed", {
        kind: "error",
        action: { label: "Retry", run: () => void act(action, options) },
      });
    }
  };
  const setLabel = async (value?: string) => {
    if (!value) {
      const next = await promptDialog({
        title: "Set label",
        label: "New label",
        confirmLabel: "Set label",
      });
      if (typeof next !== "string") return;
      value = next;
    }
    if (value) void act("set_label", { label: value });
  };
  const move = () => {
    const context = target();
    if (!context) return;
    openMove(context.hashes);
    props.onClose();
  };
  const remove = async (withData: boolean) => {
    const context = target();
    if (!context) return;
    if (await confirmRemoveTorrent(context.hashes, withData))
      void act(withData ? "remove_with_data" : "remove");
  };
  const copyValue = async (label: string, value: string) => {
    if (!value) {
      showToast("Nothing to copy.");
      return;
    }
    const ok = await copyText(value);
    showToast(ok ? `${label} copied.` : "Unable to copy — your browser blocked clipboard access.");
  };
  const copyMagnet = async () => {
    const context = target();
    if (!context?.torrent) return;
    const t = context.torrent;
    await copyValue("Magnet link", magnetFor(t.hash, t.name));
    props.onClose();
  };
  const copyField = async (label: string, field: "hash" | "name" | "path") => {
    const context = target();
    if (!context?.torrent) return;
    const t = context.torrent;
    const value = field === "hash" ? t.hash : field === "name" ? t.name : t.directory || t.basePath;
    await copyValue(label, value);
    props.onClose();
  };
  const rename = async () => {
    const context = target();
    if (!context?.torrent) return;
    const name = await promptDialog({
      title: "Rename torrent",
      label: "Name",
      initial: context.torrent.name,
      confirmLabel: "Rename",
    });
    if (typeof name !== "string" || !name.trim()) return;
    void act("rename", { name: name.trim() });
  };
  const saveTorrent = async () => {
    const context = target();
    if (!context) return;
    const hash = context.hashes[0];
    if (!hash) return;
    try {
      const response = await fetch(`/api/v1/torrent-file/${encodeURIComponent(hash)}`, {
        headers: { Accept: "application/x-bittorrent" },
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { error?: { message?: string } };
        throw new Error(body.error?.message || "Could not download the .torrent file");
      }
      const disposition = response.headers.get("Content-Disposition") ?? "";
      const match = /filename="([^"]+)"/.exec(disposition);
      const filename = match?.[1] || `${hash}.torrent`;
      const blob = await response.blob();
      const objectUrl = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = objectUrl;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(objectUrl);
      showToast(".torrent saved.");
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not download the .torrent file");
    }
    props.onClose();
  };
  const openDirectory = async () => {
    const context = target();
    if (!context?.torrent) return;
    const t = context.torrent;
    const basePath = t.directory || t.basePath;
    const template = daemonInfo().openUrlTemplate;
    if (template) {
      const escaped = encodeURIComponent(basePath);
      const url = template.includes("{path}")
        ? template.replaceAll("{path}", escaped)
        : template + escaped;
      window.open(url, "_blank", "noopener");
    } else {
      await copyValue("Path", basePath);
    }
    props.onClose();
  };
  const toggle = (field: "sequential" | "superseeding", action: "sequential" | "superseed") => {
    const context = target();
    if (!context) return;
    const selected = torrentList().filter((torrent) => context.hashes.includes(torrent.hash));
    void act(action, { enabled: !selected.every((torrent) => torrent[field]) });
  };
  const setCustom = async (customField: "custom2" | "custom3" | "custom4" | "custom5") => {
    const customValue = await promptDialog({
      title: `Set ${customField}`,
      label: "Value",
      confirmLabel: "Set field",
    });
    if (typeof customValue === "string") void act("set_custom", { customField, customValue });
  };
  const setRatioGroup = (group: string) => {
    const slot = seedingPolicy().customSlot;
    const customField = (
      slot === "custom3" || slot === "custom4" || slot === "custom5" ? slot : "custom2"
    ) as "custom2" | "custom3" | "custom4" | "custom5";
    void act("set_custom", { customField, customValue: group });
  };
  onMount(() => {
    const close = (event: Event) => {
      if (!(event.target as HTMLElement).closest(".context-menu")) props.onClose();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") props.onClose();
    };
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", key);
    window.addEventListener("scroll", props.onClose, true);
    onCleanup(() => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", key);
      window.removeEventListener("scroll", props.onClose, true);
    });
  });
  return (
    <Show when={target()}>
      {(context) => {
        const left = Math.min(context().x, window.innerWidth - 212);
        const top = Math.min(context().y, window.innerHeight - 300);
        const single = () => Boolean(context().torrent);
        return (
          <div
            ref={menuRef}
            class="context-menu"
            role="menu"
            aria-label="Torrent actions"
            style={{ left: `${Math.max(6, left)}px`, top: `${Math.max(6, top)}px` }}
            onKeyDown={(event) => menuKey(event)}
          >
            <MenuItem label="Start" onClick={() => void act("start")} />
            <MenuItem label="Pause" onClick={() => void act("pause")} />
            <MenuItem label="Stop" onClick={() => void act("stop")} />
            <MenuDivider />
            <MenuItem label="Force start" onClick={() => void act("force_start")} />
            <MenuItem label="Force recheck" onClick={() => void act("recheck")} />
            <MenuItem label="Force reannounce" onClick={() => void act("reannounce")} />
            <div class="menu-submenu-wrap">
              <MenuItem
                label="Priority"
                hint="▸"
                hasPopup
                expanded={priorityOpen()}
                onClick={() => setPriorityOpen(!priorityOpen())}
              />
              <Show when={priorityOpen()}>
                <div class="menu-submenu" role="group">
                  <MenuItem label="Off" onClick={() => void act("priority", { priority: 0 })} />
                  <MenuItem label="Low" onClick={() => void act("priority", { priority: 1 })} />
                  <MenuItem label="Normal" onClick={() => void act("priority", { priority: 2 })} />
                  <MenuItem label="High" onClick={() => void act("priority", { priority: 3 })} />
                </div>
              </Show>
            </div>
            <Show when={channels().length}>
              <div class="menu-submenu-wrap">
                <MenuItem
                  label="Throttle"
                  hint="▸"
                  hasPopup
                  expanded={throttleOpen()}
                  onClick={() => setThrottleOpen(!throttleOpen())}
                />
                <Show when={throttleOpen()}>
                  <div class="menu-submenu" role="group">
                    <MenuItem
                      label="None"
                      onClick={() => void act("set_throttle", { throttle: "" })}
                    />
                    <For each={channels()}>
                      {(channel) => (
                        <MenuItem
                          label={channel.name}
                          onClick={() => void act("set_throttle", { throttle: channel.name })}
                        />
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            </Show>
            <Show when={seedingPolicy().groups.length}>
              <div class="menu-submenu-wrap">
                <MenuItem
                  label="Ratio group"
                  hint="▸"
                  hasPopup
                  expanded={ratioGroupOpen()}
                  onClick={() => setRatioGroupOpen(!ratioGroupOpen())}
                />
                <Show when={ratioGroupOpen()}>
                  <div class="menu-submenu" role="group">
                    <MenuItem label="None" onClick={() => setRatioGroup("")} />
                    <For each={seedingPolicy().groups}>
                      {(group) => (
                        <MenuItem label={group.name} onClick={() => setRatioGroup(group.name)} />
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            </Show>
            <div class="menu-submenu-wrap">
              <MenuItem
                label="Advanced"
                hint="▸"
                hasPopup
                expanded={advancedOpen()}
                onClick={() => setAdvancedOpen(!advancedOpen())}
              />
              <Show when={advancedOpen()}>
                <div class="menu-submenu menu-advanced" role="group">
                  <MenuItem
                    label="Toggle sequential"
                    onClick={() => toggle("sequential", "sequential")}
                  />
                  <MenuItem
                    label="Toggle superseeding"
                    onClick={() => toggle("superseeding", "superseed")}
                  />
                  <MenuItem label="Save session" onClick={() => void act("save_session")} />
                  <MenuDivider />
                  <MenuItem label="Set custom2…" onClick={() => setCustom("custom2")} />
                  <MenuItem label="Set custom3…" onClick={() => setCustom("custom3")} />
                  <MenuItem label="Set custom4…" onClick={() => setCustom("custom4")} />
                  <MenuItem label="Set custom5…" onClick={() => setCustom("custom5")} />
                </div>
              </Show>
            </div>
            <div class="menu-label-wrap">
              <MenuItem
                label="Set label"
                hint="▸"
                hasPopup
                expanded={labelsOpen()}
                onClick={() => setLabelsOpen(!labelsOpen())}
              />
              <Show when={labelsOpen()}>
                <div class="label-submenu" role="group" aria-label="Labels">
                  <For each={labels()}>
                    {(label) => <MenuItem label={label} onClick={() => setLabel(label)} />}
                  </For>
                  <MenuItem label="New label…" onClick={() => setLabel()} />
                </div>
              </Show>
            </div>
            <Show when={single()}>
              <MenuItem label="Move data…" onClick={move} />
              <MenuItem label="Save .torrent" onClick={() => void saveTorrent()} />
              <MenuItem label="Open directory" onClick={() => void openDirectory()} />
              <Show when={daemonInfo().renameSupported}>
                <MenuItem label="Rename…" onClick={rename} />
              </Show>
              <div class="menu-submenu-wrap">
                <MenuItem
                  label="Copy"
                  hint="▸"
                  hasPopup
                  expanded={copyOpen()}
                  onClick={() => setCopyOpen(!copyOpen())}
                />
                <Show when={copyOpen()}>
                  <div class="menu-submenu menu-advanced" role="group">
                    <MenuItem label="Copy hash" onClick={() => void copyField("Hash", "hash")} />
                    <MenuItem label="Copy name" onClick={() => void copyField("Name", "name")} />
                    <MenuItem label="Copy path" onClick={() => void copyField("Path", "path")} />
                    <MenuItem label="Copy magnet link" onClick={copyMagnet} />
                  </div>
                </Show>
              </div>
            </Show>
            <Show when={!single()}>
              <MenuItem label="Move data…" onClick={move} />
              <MenuDivider />
            </Show>
            <MenuDivider />
            <MenuItem label="Remove" hint="Del" onClick={() => remove(false)} />
            <MenuItem label="Remove + data" hint="⇧Del" destructive onClick={() => remove(true)} />
          </div>
        );
      }}
    </Show>
  );
}

function MenuItem(props: {
  label: string;
  hint?: string;
  destructive?: boolean;
  hasPopup?: boolean;
  expanded?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      class="menu-item"
      classList={{ destructive: props.destructive }}
      type="button"
      role="menuitem"
      aria-haspopup={props.hasPopup ? "true" : undefined}
      aria-expanded={props.hasPopup ? props.expanded : undefined}
      onClick={props.onClick}
    >
      <span>{props.label}</span>
      <Show when={props.hint}>
        <small>{props.hint}</small>
      </Show>
    </button>
  );
}
function MenuDivider() {
  return <div class="menu-divider" role="separator" />;
}
