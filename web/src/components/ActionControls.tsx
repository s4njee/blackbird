import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { connected, patchTorrents, restoreTorrents, torrentList } from "../store/session";
import { moveSelection, openMove, selectAllVisible, selectedHashes, showToast, shownTorrentCount, visibleHashes } from "../store/ui";
import type { Torrent } from "../lib/types";
import type { ContextTarget } from "./TorrentTable";

type Action = "start" | "force_start" | "pause" | "stop" | "recheck" | "reannounce" | "remove" | "remove_with_data" | "set_label" | "move_data" | "priority" | "superseed" | "sequential" | "save_session" | "set_custom";
type ActionOptions = { label?: string; destination?: string; priority?: number; enabled?: boolean; customField?: "custom2" | "custom3" | "custom4" | "custom5"; customValue?: string };

async function sendAction(action: Action, hashes: string[], options: ActionOptions = {}) {
  const response = await fetch("/api/torrents/action", {
    method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ action, hashes, ...options }),
  });
  const data = await response.json().catch(() => ({})) as { message?: string; results?: Array<{ hash: string; ok: boolean; error?: string }> };
  if (!response.ok) throw new Error(data.message || "Action failed");
  const failures = (data.results ?? []).filter((result) => !result.ok);
  if (failures.length) throw new Error(failures.map((result) => result.error || result.hash).join("; "));
}

function actionLabel(action: Action) {
  return ({ start: "Start", force_start: "Force start", pause: "Pause", stop: "Stop", recheck: "Force recheck", reannounce: "Force reannounce", remove: "Remove", remove_with_data: "Remove + data", set_label: "Set label", move_data: "Move data", priority: "Set priority", superseed: "Superseeding", sequential: "Sequential download", save_session: "Save session", set_custom: "Set custom field" } as Record<Action, string>)[action];
}

function optimisticPatch(action: Action, options: ActionOptions): Partial<Torrent> | null {
  if (action === "start" || action === "force_start") return { state: "downloading", downRate: 0, upRate: 0 };
  if (action === "pause" || action === "stop") return { state: "stopped", downRate: 0, upRate: 0 };
  if (action === "priority" && options.priority !== undefined) return { priority: options.priority };
  if (action === "superseed" && options.enabled !== undefined) return { superseeding: options.enabled };
  if (action === "sequential" && options.enabled !== undefined) return { sequential: options.enabled };
  if (action === "set_custom" && options.customField) {
    const patch = { [options.customField]: options.customValue ?? "" } as Partial<Torrent>;
    if (options.customField === "custom2") patch.ratioGroup = options.customValue ?? "";
    return patch;
  }
  return null;
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || (target instanceof HTMLElement && target.isContentEditable);
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
      showToast(`${actionLabel(action)} queued for ${hashes.length} torrent${hashes.length === 1 ? "" : "s"}`);
    } catch (error) {
      if (patch) restoreTorrents(previous);
      showToast(error instanceof Error ? error.message : "Action failed");
    }
  };
  const setLabel = () => {
    const label = window.prompt("Label for selected torrents:");
    if (label !== null) void perform("set_label", { label: label.trim() });
  };
  const move = () => openMove(selection());
  const remove = (withData = false) => {
    const count = selection().length;
    const words = withData ? `Remove ${count} torrent${count === 1 ? "" : "s"} and their data? This cannot be undone.` : `Remove ${count} torrent${count === 1 ? "" : "s"}?`;
    if (window.confirm(words)) void perform(withData ? "remove_with_data" : "remove");
  };
  onMount(() => {
    const keydown = (event: KeyboardEvent) => {
      const editable = isEditableTarget(event.target);
      if (event.key === "Escape") {
        if (document.activeElement instanceof HTMLElement && document.activeElement !== document.body) document.activeElement.blur();
        return;
      }
      if (editable || event.repeat) return;
      if (event.key === "/" && !event.metaKey && !event.ctrlKey && !event.altKey) {
        event.preventDefault();
        document.querySelector<HTMLInputElement>(".filter-input")?.focus();
        return;
      }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "a") {
        event.preventDefault();
        selectAllVisible(visibleHashes());
        return;
      }
      if (event.key === "ArrowUp" || event.key === "ArrowDown") {
        event.preventDefault();
        moveSelection(event.key === "ArrowUp" ? -1 : 1, visibleHashes(), event.shiftKey);
        return;
      }
      if (event.key === " " || event.key === "Space" || event.key === "Spacebar") {
        const selection = torrentList().filter((torrent) => selectedHashes().includes(torrent.hash));
        if (!selection.length) return;
        event.preventDefault();
        const pause = selection.every((torrent) => torrent.state === "downloading" || torrent.state === "seeding");
        void perform(pause ? "pause" : "start");
        return;
      }
      // macOS labels its primary forward-delete key "Delete" but reports it
      // as Backspace; accept both names while editable targets are suppressed.
      if (event.key === "Delete" || event.key === "Del" || event.key === "Backspace") {
        if (!selectedHashes().length) return;
        event.preventDefault();
        remove(event.shiftKey);
      }
    };
    document.addEventListener("keydown", keydown);
    onCleanup(() => document.removeEventListener("keydown", keydown));
  });
  return <ToolbarView enabled={enabled()} onAction={perform} onSetLabel={setLabel} onMove={move} onRemove={remove} onOpenMenu={props.onOpenMenu} />;
}

function ToolbarView(props: {
  enabled: boolean; onAction: (action: Action, options?: ActionOptions) => void; onSetLabel: () => void; onMove: () => void; onRemove: (withData?: boolean) => void; onOpenMenu: (target: ContextTarget) => void;
}) {
  const openMenu = (event: MouseEvent) => props.onOpenMenu({ x: event.clientX, y: event.clientY, hashes: selectedHashes() });
  return <div class="toolbar">
    <button class="toolbar-button transport" disabled={!props.enabled} onClick={() => props.onAction("start")}><i>▶</i>Start</button>
    <button class="toolbar-button transport" disabled={!props.enabled} onClick={() => props.onAction("force_start")}>Force start</button>
    <button class="toolbar-button transport" disabled={!props.enabled} onClick={() => props.onAction("pause")}><i>❙❙</i>Pause</button>
    <button class="toolbar-button transport" disabled={!props.enabled} onClick={() => props.onAction("stop")}><i>■</i>Stop</button><span class="toolbar-divider" />
    <button class="toolbar-button" disabled={!props.enabled} onClick={() => props.onAction("recheck")}>Force recheck</button>
    <button class="toolbar-button" disabled={!props.enabled} onClick={props.onSetLabel}>Set label</button>
    <button class="toolbar-button" disabled={!props.enabled} onClick={props.onMove}>Move data</button>
    <label class="toolbar-priority">Priority <select disabled={!props.enabled} value="" onChange={(event) => { const value = event.currentTarget.value; if (value !== "") props.onAction("priority", { priority: Number(value) }); event.currentTarget.value = ""; }}><option value="">Set…</option><option value="0">Off</option><option value="1">Low</option><option value="2">Normal</option><option value="3">High</option></select></label>
    <button class="toolbar-button destructive" disabled={!props.enabled} onClick={() => props.onRemove(false)}>Remove</button>
    <span class="toolbar-spacer" /><button class="toolbar-more" disabled={!props.enabled} onClick={openMenu} title="More actions">•••</button>
    <span class="selection-readout tnum">{selectedHashes().length} selected · {shownTorrentCount()} of {torrentList().length} shown</span>
  </div>;
}

export function ContextMenu(props: { target: ContextTarget | null; onClose: () => void }) {
  const [labelsOpen, setLabelsOpen] = createSignal(false);
  const [priorityOpen, setPriorityOpen] = createSignal(false);
  const [advancedOpen, setAdvancedOpen] = createSignal(false);
  const labels = createMemo(() => [...new Set(torrentList().map((torrent) => torrent.label).filter(Boolean))].sort());
  const target = () => props.target;
  const act = async (action: Action, options?: ActionOptions) => {
    const context = target();
    if (!context || !connected()) return;
    const previous = torrentList().filter((torrent) => context.hashes.includes(torrent.hash));
    const patch = optimisticPatch(action, options ?? {});
    if (patch) patchTorrents(context.hashes, patch);
    props.onClose();
    try { await sendAction(action, context.hashes, options); showToast(`${actionLabel(action)} queued for ${context.hashes.length} torrent${context.hashes.length === 1 ? "" : "s"}`); }
    catch (error) { if (patch) restoreTorrents(previous); showToast(error instanceof Error ? error.message : "Action failed"); }
  };
  const setLabel = (value?: string) => {
    if (!value) value = window.prompt("New label:") || undefined;
    if (value) void act("set_label", { label: value });
  };
  const move = () => { const context = target(); if (!context) return; openMove(context.hashes); props.onClose(); };
  const remove = (withData: boolean) => {
    const context = target(); if (!context) return;
    if (window.confirm(withData ? `Remove ${context.hashes.length} torrent(s) and their data? This cannot be undone.` : `Remove ${context.hashes.length} torrent(s)?`)) void act(withData ? "remove_with_data" : "remove");
  };
  const copyMagnet = async () => {
    const context = target(); if (!context?.torrent) return;
    const t = context.torrent;
    const magnet = `magnet:?xt=urn:btih:${t.hash}&dn=${encodeURIComponent(t.name)}`;
    try { await navigator.clipboard.writeText(magnet); showToast("Magnet link copied."); } catch { showToast("Unable to copy magnet link."); }
    props.onClose();
  };
  const toggle = (field: "sequential" | "superseeding", action: "sequential" | "superseed") => {
    const context = target();
    if (!context) return;
    const selected = torrentList().filter((torrent) => context.hashes.includes(torrent.hash));
    void act(action, { enabled: !selected.every((torrent) => torrent[field]) });
  };
  const setCustom = (customField: "custom2" | "custom3" | "custom4" | "custom5") => {
    const customValue = window.prompt(`Value for ${customField}:`);
    if (customValue !== null) void act("set_custom", { customField, customValue });
  };
  onMount(() => {
    const close = (event: Event) => { if (!(event.target as HTMLElement).closest(".context-menu")) props.onClose(); };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") props.onClose(); };
    document.addEventListener("pointerdown", close); document.addEventListener("keydown", key); window.addEventListener("scroll", props.onClose, true);
    onCleanup(() => { document.removeEventListener("pointerdown", close); document.removeEventListener("keydown", key); window.removeEventListener("scroll", props.onClose, true); });
  });
  return <Show when={target()}>{(context) => {
    const left = Math.min(context().x, window.innerWidth - 212); const top = Math.min(context().y, window.innerHeight - 300);
    return <div class="context-menu" style={{ left: `${Math.max(6, left)}px`, top: `${Math.max(6, top)}px` }}>
      <MenuItem label="Start" onClick={() => void act("start")} /><MenuItem label="Pause" onClick={() => void act("pause")} /><MenuItem label="Stop" onClick={() => void act("stop")} /><MenuDivider />
      <MenuItem label="Force start" onClick={() => void act("force_start")} /><MenuItem label="Force recheck" onClick={() => void act("recheck")} />
      <MenuItem label="Force reannounce" onClick={() => void act("reannounce")} />
      <div class="menu-submenu-wrap"><MenuItem label="Priority" hint="▸" onClick={() => setPriorityOpen(!priorityOpen())} />
        <Show when={priorityOpen()}><div class="menu-submenu"><MenuItem label="Off" onClick={() => void act("priority", { priority: 0 })} /><MenuItem label="Low" onClick={() => void act("priority", { priority: 1 })} /><MenuItem label="Normal" onClick={() => void act("priority", { priority: 2 })} /><MenuItem label="High" onClick={() => void act("priority", { priority: 3 })} /></div></Show></div>
      <div class="menu-submenu-wrap"><MenuItem label="Advanced" hint="▸" onClick={() => setAdvancedOpen(!advancedOpen())} />
        <Show when={advancedOpen()}><div class="menu-submenu menu-advanced"><MenuItem label="Toggle sequential" onClick={() => toggle("sequential", "sequential")} /><MenuItem label="Toggle superseeding" onClick={() => toggle("superseeding", "superseed")} /><MenuItem label="Save session" onClick={() => void act("save_session")} /><MenuDivider /><MenuItem label="Set custom2…" onClick={() => setCustom("custom2")} /><MenuItem label="Set custom3…" onClick={() => setCustom("custom3")} /><MenuItem label="Set custom4…" onClick={() => setCustom("custom4")} /><MenuItem label="Set custom5…" onClick={() => setCustom("custom5")} /></div></Show></div>
      <div class="menu-label-wrap"><MenuItem label="Set label" hint="▸" onClick={() => setLabelsOpen(!labelsOpen())} />
        <Show when={labelsOpen()}><div class="label-submenu"><For each={labels()}>{(label) => <MenuItem label={label} onClick={() => setLabel(label)} />}</For><MenuItem label="New label…" onClick={() => setLabel()} /></div></Show></div>
      <MenuItem label="Move data…" onClick={move} /><MenuItem label="Copy magnet link" onClick={copyMagnet} /><MenuDivider />
      <MenuItem label="Remove" hint="Del" onClick={() => remove(false)} /><MenuItem label="Remove + data" hint="⇧Del" destructive onClick={() => remove(true)} />
    </div>;
  }}</Show>;
}

function MenuItem(props: { label: string; hint?: string; destructive?: boolean; onClick: () => void }) {
  return <button class="menu-item" classList={{ destructive: props.destructive }} type="button" onClick={props.onClick}><span>{props.label}</span><Show when={props.hint}><small>{props.hint}</small></Show></button>;
}
function MenuDivider() { return <div class="menu-divider" />; }
