import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import { formatBytes, formatRatio, formatRate, formatUptime } from "../lib/format";
import type { Torrent, TorrentDetail, TorrentFile, Tracker } from "../lib/types";
import { details, fetchDetail, globalStats, patchDetailFile, sendFocus, sendUnfocus, torrentList } from "../store/session";
import { focusedHash, showToast } from "../store/ui";

type Tab = "files" | "peers" | "trackers" | "transfer" | "pieces";
const TABS: Array<{ id: Tab; label: string }> = [
  { id: "files", label: "Files" }, { id: "peers", label: "Peers" }, { id: "trackers", label: "Trackers" }, { id: "transfer", label: "Transfer" }, { id: "pieces", label: "Pieces" },
];

async function detailAction(action: string, hash: string, extra: Record<string, unknown> = {}) {
  const response = await fetch("/api/torrents/action", { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ action, hashes: [hash], ...extra }) });
  const body = await response.json().catch(() => ({})) as { message?: string; results?: Array<{ ok: boolean; error?: string }> };
  if (!response.ok) throw new Error(body.message || "Action failed");
  const failure = body.results?.find((result) => !result.ok);
  if (failure) throw new Error(failure.error || "Action failed");
}

function filePercent(file: TorrentFile) { return file.sizeChunks ? Math.min(100, (file.completedChunks / file.sizeChunks) * 100) : 0; }
function filePriorityLabel(priority: number) { return priority === 2 ? "High" : priority === 0 ? "Skip" : "Normal"; }

type TreeNode = { name: string; path: string; depth: number; file?: TorrentFile; children: TreeNode[]; size: number; completed: number };
function fileTree(files: TorrentFile[]) {
  const root: TreeNode = { name: "", path: "", depth: -1, children: [], size: 0, completed: 0 };
  const byPath = new Map<string, TreeNode>([["", root]]);
  for (const file of files) {
    const parts = file.path.split("/").filter(Boolean);
    let parent = root; let key = "";
    parts.forEach((part, index) => {
      key += `/${part}`;
      let node = byPath.get(key);
      if (!node) { node = { name: part, path: key, depth: index, children: [], size: 0, completed: 0 }; byPath.set(key, node); parent.children.push(node); }
      if (index === parts.length - 1) node.file = file;
      parent = node;
    });
  }
  const sum = (node: TreeNode): TreeNode => {
    for (const child of node.children) sum(child);
    if (node.file) { node.size = node.file.sizeBytes; node.completed = node.file.sizeChunks ? (node.file.completedChunks / node.file.sizeChunks) * node.file.sizeBytes : 0; }
    else { node.size = node.children.reduce((total, child) => total + child.size, 0); node.completed = node.children.reduce((total, child) => total + child.completed, 0); }
    return node;
  };
  sum(root); return root;
}

export function DetailPanel() {
  const [tab, setTab] = createSignal<Tab>("files");
  const hash = focusedHash;
  const torrent = createMemo(() => torrentList().find((item) => item.hash === hash()));
  const detail = createMemo(() => hash() ? details()[hash()] : undefined);
  createEffect(() => {
    const value = hash();
    if (!value) return;
    sendFocus(value); void fetchDetail(value);
    onCleanup(() => sendUnfocus());
  });
  return <section class="detail-panel" aria-label="Torrent detail">
    <header class="detail-tabs"><div class="detail-tab-list"><For each={TABS}>{(item) => <button class="detail-tab" classList={{ active: tab() === item.id }} type="button" onClick={() => setTab(item.id)}>{item.label}</button>}</For></div><span class="detail-title" title={torrent()?.name}>{torrent()?.name || "Select a torrent to inspect its details"}</span></header>
    <Show when={torrent()} fallback={<div class="detail-empty">Select a torrent to inspect files, peers, trackers, transfer totals, and pieces.</div>}>
      {(current) => (
        <div class="detail-body">
          <div class="detail-content">
            <Show when={detail()} fallback={<DetailSkeleton />}>
              {(data) => (
                <>
                  <Show when={tab() === "files"}><FilesTab hash={current().hash} detail={data()} /></Show>
                  <Show when={tab() === "peers"}><PeersTab detail={data()} /></Show>
                  <Show when={tab() === "trackers"}><TrackersTab hash={current().hash} detail={data()} /></Show>
                  <Show when={tab() === "transfer"}><TransferTab torrent={current()} detail={data()} /></Show>
                  <Show when={tab() === "pieces"}><PiecesTab detail={data()} /></Show>
                </>
              )}
            </Show>
          </div>
          <Facts torrent={current()} detail={detail()} />
        </div>
      )}
    </Show>
  </section>;
}

function DetailSkeleton() { return <div class="detail-skeleton"><span /><span /><span /><span /></div>; }

function Facts(props: { torrent: Torrent; detail?: TorrentDetail }) {
  const transfer = () => props.detail?.transfer;
  const facts = createMemo(() => [
    ["Hash", props.torrent.hash], ["Downloaded", formatBytes(transfer()?.downloadedBytes ?? props.torrent.completedBytes)], ["Uploaded", formatBytes(transfer()?.uploadedBytes ?? 0)], ["Ratio", formatRatio(props.torrent.ratio)],
    ["Pieces", transfer() ? `${transfer()!.chunksDone.toLocaleString()} / ${transfer()!.chunkCount.toLocaleString()}` : "—"], ["Peers", `${props.torrent.seeds || "—"} / ${props.torrent.peers || "—"}`], ["Down rate", formatRate(props.torrent.downRate), "down"], ["Up rate", formatRate(props.torrent.upRate), "up"],
    ["Path", transfer()?.directory || props.torrent.basePath], ["Added", new Date(props.torrent.addedAt).toLocaleString()], ["Private", props.torrent.isPrivate ? "Yes" : "No"],
  ]);
  return <aside class="detail-facts"><For each={facts()}>{(fact) => <div class="fact"><span>{fact[0]}</span><b classList={{ "rate-down": fact[2] === "down", "rate-up": fact[2] === "up" }} title={String(fact[1])}>{fact[1]}</b></div>}</For></aside>;
}

function FilesTab(props: { hash: string; detail: TorrentDetail }) {
  const [collapsed, setCollapsed] = createSignal(new Set<string>());
  const root = createMemo(() => fileTree(props.detail.files));
  const flattened = createMemo(() => {
    const rows: TreeNode[] = [];
    const visit = (node: TreeNode) => { for (const child of node.children) { rows.push(child); if (child.children.length && !collapsed().has(child.path)) visit(child); } };
    visit(root()); return rows;
  });
  const setPriority = async (files: TorrentFile[], current?: number) => {
    const next = current === 2 ? 1 : current === 1 ? 0 : 2;
    for (const file of files) patchDetailFile(props.hash, file.index, next);
    try { await Promise.all(files.map((file) => detailAction("file_priority", props.hash, { fileIndex: file.index, priority: next }))); showToast(`Priority set to ${filePriorityLabel(next)}.`); }
    catch (error) { void fetchDetail(props.hash); showToast(error instanceof Error ? error.message : "Could not set priority"); }
  };
  const descendants = (node: TreeNode): TorrentFile[] => node.file ? [node.file] : node.children.flatMap(descendants);
  return <div class="detail-table files-table"><div class="detail-header file-grid"><span>File</span><span>Size</span><span>Progress</span><span>Done</span><span>Priority</span></div><div class="detail-rows"><Show when={props.detail.files.length} fallback={<div class="detail-empty-rows">This torrent does not expose a file list yet.</div>}><For each={flattened()}>{(node) => {
    const percent = node.file ? filePercent(node.file) : node.size ? (node.completed / node.size) * 100 : 0; const isDir = node.children.length > 0;
    return <div class="file-grid detail-row" classList={{ skipped: node.file?.priority === 0 }}><button class="file-name" type="button" style={{ "--depth": String(node.depth) }} onClick={() => isDir ? setCollapsed((old) => { const next = new Set(old); next.has(node.path) ? next.delete(node.path) : next.add(node.path); return next; }) : undefined}><i>{isDir ? (collapsed().has(node.path) ? "▸" : "▾") : "·"}</i>{node.name}</button><span class="numeric">{formatBytes(node.size)}</span><Progress percent={percent} /><span class="numeric">{formatBytes(node.completed)}</span><button class={`priority-chip p${node.file?.priority ?? 1}`} type="button" onClick={() => void setPriority(descendants(node), node.file?.priority)}>{node.file ? filePriorityLabel(node.file.priority) : "Set all"}</button></div>;
  }}</For></Show></div></div>;
}

function Progress(props: { percent: number }) { return <div class="small-progress"><span classList={{ complete: props.percent >= 100 }} style={{ width: `${Math.min(100, props.percent)}%` }} /><b>{props.percent >= 100 ? "100%" : `${props.percent.toFixed(1)}%`}</b></div>; }

function PeersTab(props: { detail: TorrentDetail }) { return <div class="detail-table peers-table"><div class="detail-header peer-grid"><span>Peers — {props.detail.peers.length} connected</span><span>Port</span><span>Client</span><span>Have</span><span>Down</span><span>Up</span><span>Flags</span></div><div class="detail-rows"><Show when={props.detail.peers.length} fallback={<div class="detail-empty-rows">No peers are connected right now.</div>}><For each={props.detail.peers}>{(peer) => <div class="peer-grid detail-row"><span title={peer.address}>{peer.address}</span><span class="numeric">{peer.port}</span><span title={peer.client}>{peer.client || "—"}</span><div class="peer-have"><span style={{ width: `${Math.min(100, peer.completedPercent)}%` }} /></div><span class="numeric rate-down">{peer.downRate ? formatRate(peer.downRate) : "—"}</span><span class="numeric rate-up">{peer.upRate ? formatRate(peer.upRate) : "—"}</span><span class="tnum">{peer.flags || "—"}</span></div>}</For></Show></div></div>; }

function TrackersTab(props: { hash: string; detail: TorrentDetail }) {
  const [now, setNow] = createSignal(Date.now());
  const timer = window.setInterval(() => setNow(Date.now()), 1000); onCleanup(() => window.clearInterval(timer));
  const next = (tracker: Tracker) => tracker.nextAnnounceAt ? formatUptime(Math.max(0, (Date.parse(tracker.nextAnnounceAt) - now()) / 1000)) : "—";
  const add = () => { const url = window.prompt("Tracker URL:"); if (!url) return; const group = Number(window.prompt("Tracker tier (0 is primary):", "0")); try { const parsed = new URL(url); if (!/^https?:$/.test(parsed.protocol) || !Number.isInteger(group) || group < 0) throw new Error(); } catch { showToast("Enter a valid http(s) tracker URL and tier."); return; } void detailAction("tracker_add", props.hash, { trackerUrl: url, trackerGroup: group }).then(() => { showToast("Tracker added."); void fetchDetail(props.hash); }).catch((error: Error) => showToast(error.message)); };
  const announce = () => void detailAction("reannounce", props.hash).then(() => showToast("Reannounce requested.")).catch((error: Error) => showToast(error.message));
  const remove = (tracker: Tracker) => { if (!window.confirm(`Remove tracker ${tracker.url}?`)) return; void detailAction("tracker_remove", props.hash, { trackerIndex: tracker.index }).then(() => { showToast("Tracker removed."); void fetchDetail(props.hash); }).catch((error: Error) => showToast(error.message)); };
  const toggle = (tracker: Tracker) => void detailAction("tracker_enable", props.hash, { trackerIndex: tracker.index, enabled: !tracker.isEnabled }).then(() => { showToast(tracker.isEnabled ? "Tracker disabled." : "Tracker enabled."); void fetchDetail(props.hash); }).catch((error: Error) => showToast(error.message));
  const status = (tracker: Tracker) => !tracker.isEnabled ? "Disabled" : tracker.failedCount ? `Failed${tracker.latestEvent ? `: ${tracker.latestEvent}` : ""}` : tracker.successCount ? "Working" : tracker.latestEvent ? "Updating" : "Not contacted";
  const trackerRows = () => props.detail.trackers;
  return <div class="trackers-tab"><div class="tracker-list"><For each={trackerRows()}>{(tracker) => <div class="tracker-row"><i classList={{ off: !tracker.isEnabled }} /><span class="tracker-url" title={tracker.url}>T{tracker.group} · {tracker.url}</span><span class="tracker-status" classList={{ off: !tracker.isEnabled }}>{status(tracker)}</span><span class="tnum">{tracker.seeds} / {tracker.leechers}</span><span class="tnum">{next(tracker)}</span><button class="tracker-remove" type="button" onClick={() => toggle(tracker)}>{tracker.isEnabled ? "Disable" : "Enable"}</button><button class="tracker-remove" type="button" onClick={() => remove(tracker)}>Remove</button></div>}</For></div><footer class="tracker-actions"><button type="button" onClick={add}>Add tracker</button><button type="button" onClick={announce}>Force reannounce</button></footer></div>;
}

function TransferTab(props: { torrent: Torrent; detail: TorrentDetail }) { const t = () => props.detail.transfer; const g = globalStats; return <div class="transfer-grid"><div><span>Downloaded</span><b>{formatBytes(t().downloadedBytes)}</b></div><div><span>Uploaded</span><b>{formatBytes(t().uploadedBytes)}</b></div><div><span>Chunk size</span><b>{formatBytes(t().chunkSize)}</b></div><div><span>Chunks</span><b class="tnum">{t().chunksDone.toLocaleString()} / {t().chunkCount.toLocaleString()}</b></div><div><span>Directory</span><b title={t().directory}>{t().directory || "—"}</b></div><div><span>Torrent priority</span><b>{props.torrent.priority === 3 ? "High" : props.torrent.priority === 0 ? "Off" : props.torrent.priority === 1 ? "Low" : "Normal"}</b></div><div><span>Session downloaded</span><b>{formatBytes(g()?.sessionDownTotal ?? 0)}</b></div><div><span>Session uploaded</span><b>{formatBytes(g()?.sessionUpTotal ?? 0)}</b></div></div>; }

function PiecesTab(props: { detail: TorrentDetail }) { const cells = createMemo(() => { const total = Math.max(0, props.detail.transfer.chunkCount); const done = Math.max(0, props.detail.transfer.chunksDone); const count = Math.min(400, Math.max(1, total)); return Array.from({ length: count }, (_, index) => { const from = (index / count) * total; const to = ((index + 1) / count) * total; return to <= done ? "done" : from < done ? "working" : "missing"; }); }); return <div class="pieces-tab"><svg class="piece-map" viewBox="0 0 40 10" preserveAspectRatio="none" role="img" aria-label={`${props.detail.transfer.chunksDone} of ${props.detail.transfer.chunkCount} pieces complete`}><For each={cells()}>{(state, index) => <rect class={state} x={index() % 40} y={Math.floor(index() / 40)} width=".82" height=".82" rx=".1" />}</For></svg><span class="tnum">{props.detail.transfer.chunksDone.toLocaleString()} / {props.detail.transfer.chunkCount.toLocaleString()} pieces complete</span></div>; }
