// Create-torrent modal (PAR-5.4): builds a .torrent from server-side data.
// The source is picked with the shared directory browser (files can be
// typed); trackers go one per line with the first as announce and each
// further tracker in its own tier. Hashing runs on the server's bounded
// worker pool with progress; the finished .torrent downloads as an
// attachment or loads into the session tied to the source path.

import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { formatBytes } from "../lib/format";
import { closeCreate, createOpen, showToast } from "../store/ui";
import { DirPicker } from "./DirectoryBrowser";

type Job = {
  id: string;
  status: "running" | "completed" | "failed" | "cancelled";
  source: string;
  name: string;
  trackers: string[];
  private: boolean;
  totalBytes: number;
  fileCount: number;
  pieceLength: number;
  pieceCount: number;
  bytesHashed: number;
  piecesDone: number;
  currentFile: string;
  infohash: string;
  torrentSize: number;
  error?: string;
  added: boolean;
  addedHash?: string;
  addError?: string;
  addToSession: boolean;
};

const PIECE_SIZES: Array<{ label: string; value: number }> = [
  { label: "Automatic", value: 0 },
  { label: "16 KiB", value: 16384 },
  { label: "32 KiB", value: 32768 },
  { label: "64 KiB", value: 65536 },
  { label: "128 KiB", value: 131072 },
  { label: "256 KiB", value: 262144 },
  { label: "512 KiB", value: 524288 },
  { label: "1 MiB", value: 1048576 },
  { label: "2 MiB", value: 2097152 },
  { label: "4 MiB", value: 4194304 },
  { label: "8 MiB", value: 8388608 },
  { label: "16 MiB", value: 16777216 },
];

function trackerError(value: string): string {
  if (!value) return "";
  const at = value.indexOf("://");
  if (at < 0) return "Expected an http(s) or udp URL";
  const scheme = value.slice(0, at).toLowerCase();
  if (scheme !== "http" && scheme !== "https" && scheme !== "udp")
    return "Expected an http(s) or udp URL";
  if (
    !value
      .slice(at + 3)
      .split(/[\/?#]/)[0]
      .trim()
  )
    return "Tracker URL has no host";
  return "";
}

export function CreateTorrentModal() {
  const [source, setSource] = createSignal("");
  const [name, setName] = createSignal("");
  const [trackers, setTrackers] = createSignal("");
  const [pieceLength, setPieceLength] = createSignal(0);
  const [isPrivate, setPrivate] = createSignal(false);
  const [comment, setComment] = createSignal("");
  const [sourceTag, setSourceTag] = createSignal("");
  const [addToSession, setAddToSession] = createSignal(false);
  const [start, setStart] = createSignal(true);
  const [label, setLabel] = createSignal("");
  const [job, setJob] = createSignal<Job | null>(null);
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const trackerLines = createMemo(() =>
    trackers()
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean),
  );
  const badTrackers = createMemo(() =>
    trackerLines()
      .map((line) => ({ line, error: trackerError(line) }))
      .filter((entry) => entry.error),
  );
  const valid = createMemo(() => source().trim() !== "" && badTrackers().length === 0 && !busy());
  const progress = createMemo(() => {
    const j = job();
    if (!j || !j.totalBytes) return 0;
    return Math.min(100, (j.bytesHashed / j.totalBytes) * 100);
  });

  let timer: number | undefined;
  const stopPolling = () => {
    if (timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  };
  onMount(() => onCleanup(stopPolling));

  async function poll(id: string) {
    try {
      const response = await fetch(`/api/v1/torrents/create/${encodeURIComponent(id)}`, {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error();
      const next = (await response.json()) as Job;
      setJob(next);
      if (next.status !== "running") {
        stopPolling();
        setBusy(false);
        if (next.status === "completed")
          showToast(`Created ${next.name} (${next.pieceCount} pieces).`);
      }
    } catch {
      /* next tick retries */
    }
  }

  function close() {
    // Like the add dialog's submit guard: the dialog stays put while the
    // server hashes, so progress and the finished download are never lost.
    if (job()?.status === "running") return;
    stopPolling();
    setJob(null);
    setError("");
    closeCreate();
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!valid()) return;
    setBusy(true);
    setError("");
    setJob(null);
    try {
      const response = await fetch("/api/v1/torrents/create", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({
          source: source().trim(),
          name: name().trim() || undefined,
          trackers: trackerLines(),
          piece_length: pieceLength(),
          private: isPrivate(),
          comment: comment(),
          source_tag: sourceTag().trim() || undefined,
          add_to_session: addToSession(),
          start: start(),
          label: label().trim() || undefined,
        }),
      });
      const body = (await response.json().catch(() => ({}))) as Job & { message?: string };
      if (!response.ok)
        throw new Error((body as { message?: string }).message || "Could not start creation");
      setJob(body);
      stopPolling();
      timer = window.setInterval(() => void poll(body.id), 1000);
      void poll(body.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start creation");
      setBusy(false);
    }
  }

  async function cancel() {
    const j = job();
    if (!j || j.status !== "running") return;
    try {
      const response = await fetch(`/api/v1/torrents/create/${encodeURIComponent(j.id)}/cancel`, {
        method: "POST",
      });
      if (response.ok) setJob((await response.json()) as Job);
    } catch {
      /* status polling reports the outcome */
    }
  }

  async function download() {
    const j = job();
    if (!j || j.status !== "completed") return;
    try {
      const response = await fetch(`/api/v1/torrents/create/${encodeURIComponent(j.id)}/download`);
      if (!response.ok) throw new Error();
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `${j.name || "torrent"}.torrent`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
    } catch {
      showToast("Could not download the .torrent file.");
    }
  }

  async function addNow() {
    const j = job();
    if (!j || j.status !== "completed" || j.added) return;
    setBusy(true);
    try {
      const response = await fetch(`/api/v1/torrents/create/${encodeURIComponent(j.id)}/add`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ start: start(), label: label().trim() || undefined }),
      });
      const body = (await response.json().catch(() => ({}))) as { message?: string };
      if (!response.ok) throw new Error(body.message || "Could not add to session");
      showToast("Added to the session.");
      void poll(j.id);
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Could not add to session.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Show when={createOpen()}>
      <div
        class="modal-backdrop"
        role="presentation"
        onMouseDown={(event) => {
          if (event.currentTarget === event.target) close();
        }}
      >
        <form class="add-modal create-modal" aria-label="Create torrent" onSubmit={submit}>
          <header class="modal-title">
            <h2>Create torrent</h2>
            <button type="button" aria-label="Close create torrent" onClick={close}>
              ✕
            </button>
          </header>
          <div class="add-body">
            <Show when={!job()}>
              <div class="add-options">
                <label>
                  <span>Source path</span>
                  <input
                    value={source()}
                    placeholder="/downloads/mypack or /downloads/movie.mkv"
                    onInput={(event) => setSource(event.currentTarget.value)}
                    spellcheck={false}
                  />
                </label>
                <DirPicker value={source()} onPick={setSource} label="Browse…" />
                <p class="settings-intro">
                  A directory packages recursively; a single file packages alone. The browser lists
                  directories — type a file path by hand. Sources stay inside the configured
                  download roots; symlinks are refused.
                </p>
                <label>
                  <span>Name override</span>
                  <input
                    value={name()}
                    placeholder="Defaults to the source file or directory name"
                    onInput={(event) => setName(event.currentTarget.value)}
                    spellcheck={false}
                  />
                </label>
                <label>
                  <span>Trackers — one per line</span>
                  <textarea
                    value={trackers()}
                    rows={3}
                    placeholder={
                      "https://tracker.example/announce\nudp://tracker.example:1337/announce"
                    }
                    onInput={(event) => setTrackers(event.currentTarget.value)}
                    spellcheck={false}
                  />
                </label>
                <For each={badTrackers()}>
                  {(item) => (
                    <p class="input-error">
                      <b>{item.line}</b> — {item.error}
                    </p>
                  )}
                </For>
                <p class="settings-intro">
                  The first tracker is the announce URL; each further tracker lands in its own
                  announce-list tier.
                </p>
                <label>
                  <span>Piece size</span>
                  <select
                    value={pieceLength()}
                    onChange={(event) => setPieceLength(Number(event.currentTarget.value))}
                  >
                    <For each={PIECE_SIZES}>
                      {(size) => <option value={size.value}>{size.label}</option>}
                    </For>
                  </select>
                </label>
                <label>
                  <span>Comment</span>
                  <input
                    value={comment()}
                    onInput={(event) => setComment(event.currentTarget.value)}
                    spellcheck={false}
                  />
                </label>
                <label>
                  <span>Source tag</span>
                  <input
                    value={sourceTag()}
                    placeholder="Private-tracker source string (no spaces)"
                    onInput={(event) => setSourceTag(event.currentTarget.value)}
                    spellcheck={false}
                  />
                </label>
              </div>
              <div class="add-checkboxes">
                <label>
                  <input
                    type="checkbox"
                    checked={isPrivate()}
                    onChange={(event) => setPrivate(event.currentTarget.checked)}
                  />
                  Private (disable DHT/PEX)
                </label>
                <label>
                  <input
                    type="checkbox"
                    checked={addToSession()}
                    onChange={(event) => setAddToSession(event.currentTarget.checked)}
                  />
                  Add to session when finished
                </label>
                <Show when={addToSession()}>
                  <label>
                    <input
                      type="checkbox"
                      checked={start()}
                      onChange={(event) => setStart(event.currentTarget.checked)}
                    />
                    Start immediately
                  </label>
                  <label>
                    <span>Label</span>
                    <input
                      value={label()}
                      placeholder="Optional label"
                      onInput={(event) => setLabel(event.currentTarget.value)}
                    />
                  </label>
                </Show>
              </div>
            </Show>
            <Show when={error()}>
              <p class="input-error">{error()}</p>
            </Show>
            <Show when={job()}>
              {(j) => (
                <>
                  <div class="create-status">
                    <div>
                      <span>{j().source}</span>
                      <b class="tnum">{j().status}</b>
                    </div>
                    <Show when={j().status === "running"}>
                      <div class="volume-stat-bar">
                        <i style={{ width: `${progress()}%` }} />
                      </div>
                      <small class="tnum">
                        {formatBytes(j().bytesHashed)} of {formatBytes(j().totalBytes)} ·{" "}
                        {j().piecesDone}/{j().pieceCount} pieces
                        {j().currentFile ? ` · ${j().currentFile}` : ""}
                      </small>
                    </Show>
                    <Show when={j().status === "completed"}>
                      <small class="tnum">
                        {j().fileCount} file{j().fileCount === 1 ? "" : "s"} ·{" "}
                        {formatBytes(j().totalBytes)} · {j().pieceCount} ×{" "}
                        {formatBytes(j().pieceLength)} ·{" "}
                        <span title={j().infohash}>{j().infohash.slice(0, 12)}…</span>
                      </small>
                    </Show>
                    <Show when={j().status === "failed"}>
                      <p class="input-error">{j().error}</p>
                    </Show>
                    <Show when={j().addError}>
                      <p class="input-error">Session add failed: {j().addError}</p>
                    </Show>
                    <Show when={j().added}>
                      <p class="settings-intro">
                        In the session{j().addedHash ? ` (${j().addedHash!.slice(0, 12)}…)` : ""}.
                      </p>
                    </Show>
                  </div>
                  <div class="create-actions">
                    <Show when={j().status === "running"}>
                      <button type="button" onClick={cancel}>
                        Cancel
                      </button>
                    </Show>
                    <Show when={j().status === "completed"}>
                      <button type="button" onClick={download}>
                        Download .torrent
                      </button>
                      <Show when={!j().added}>
                        <label>
                          <input
                            type="checkbox"
                            checked={start()}
                            onChange={(event) => setStart(event.currentTarget.checked)}
                          />
                          Start
                        </label>
                        <button type="button" disabled={busy()} onClick={addNow}>
                          Add to session
                        </button>
                      </Show>
                    </Show>
                    <Show when={j().status === "failed" || j().status === "cancelled"}>
                      <button
                        type="button"
                        onClick={() => {
                          setJob(null);
                          setError("");
                        }}
                      >
                        Back to form
                      </button>
                    </Show>
                  </div>
                </>
              )}
            </Show>
          </div>
          <footer class="modal-footer">
            <button class="modal-cancel" type="button" onClick={close}>
              {job()?.status === "running" ? "Hashing… (dialog stays open)" : "Close"}
            </button>
            <Show when={!job()}>
              <button class="modal-submit" type="submit" disabled={!valid()}>
                Create
              </button>
            </Show>
          </footer>
        </form>
      </div>
    </Show>
  );
}
