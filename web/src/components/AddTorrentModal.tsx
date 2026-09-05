import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import {
  closeAdd,
  addOpen,
  queuedTorrentFileErrors,
  queuedTorrentFiles,
  setQueuedTorrentFiles,
  showToast,
} from "../store/ui";
import { DirectoryBrowser, rememberDir } from "./DirectoryBrowser";

import { StorageForecast, useStorageForecast } from "./StorageForecast";

type Mode = "links" | "files";
type Label = { name: string; color: string };
type Settings = {
  directories?: { default?: string; per_label?: Record<string, string> };
  labels?: Label[];
};
type AddResult = { source: string; ok: boolean; error?: string };

function linkError(value: string) {
  if (!value) return "";
  if (value.startsWith("magnet:?xt=urn:btih:")) return "";
  try {
    const url = new URL(value);
    if (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.pathname.toLowerCase().endsWith(".torrent")
    )
      return "";
  } catch {
    /* rendered below */
  }
  return "Expected magnet:?xt=urn:btih:… or an http(s) .torrent URL";
}

export function AddTorrentModal() {
  const [mode, setMode] = createSignal<Mode>("links");
  const [links, setLinks] = createSignal("");
  const [destination, setDestination] = createSignal("/mnt/data/downloads");
  const [label, setLabel] = createSignal("");
  const [labels, setLabels] = createSignal<Label[]>([]);
  const [perLabel, setPerLabel] = createSignal<Record<string, string>>({});
  const [start, setStart] = createSignal(true);
  const [skipHashCheck, setSkipHashCheck] = createSignal(false);
  const [sequential, setSequential] = createSignal(false);
  const [fileErrors, setFileErrors] = createSignal<string[]>([]);
  const [results, setResults] = createSignal<AddResult[]>([]);
  const [browsing, setBrowsing] = createSignal(false);
  const [submitting, setSubmitting] = createSignal(false);

  const lines = createMemo(() =>
    links()
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean),
  );
  const badLines = createMemo(() =>
    lines()
      .map((line) => ({ line, error: linkError(line) }))
      .filter((entry) => entry.error),
  );
  const validLines = createMemo(() => lines().filter((line) => !linkError(line)));
  const queued = queuedTorrentFiles;
  const validInput = createMemo(() =>
    mode() === "links" ? validLines().length > 0 : queued().length > 0,
  );

  createEffect(() => {
    if (!addOpen()) return;
    if (queued().length || queuedTorrentFileErrors().length) setMode("files");
    void loadSettings();
  });
  onMount(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && addOpen()) close();
    };
    document.addEventListener("keydown", keydown);
    onCleanup(() => document.removeEventListener("keydown", keydown));
  });

  async function loadSettings() {
    try {
      const response = await fetch("/api/v1/settings", {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) return;
      const settings = (await response.json()) as Settings;
      const defaultDestination = settings.directories?.default || "/mnt/data/downloads";
      setDestination(defaultDestination);
      setLabels(settings.labels ?? []);
      setPerLabel(settings.directories?.per_label ?? {});
    } catch {
      /* defaults keep the intake form usable while disconnected */
    }
  }
  function close() {
    if (submitting()) return;
    setLinks("");
    setFileErrors([]);
    setResults([]);
    setLabel("");
    setStart(true);
    setSkipHashCheck(false);
    setSequential(false);
    closeAdd();
  }
  function addFiles(items: File[]) {
    const accepted: File[] = [];
    const rejected: string[] = [];
    for (const file of items) {
      if (file.name.toLowerCase().endsWith(".torrent")) accepted.push(file);
      else rejected.push(`${file.name}: not a .torrent file`);
    }
    if (accepted.length) {
      setQueuedTorrentFiles((current) => [...current, ...accepted]);
      setMode("files");
    }
    if (rejected.length) setFileErrors((current) => [...current, ...rejected]);
  }
  function chooseLabel(value: string) {
    setLabel(value);
    if (value && perLabel()[value]) setDestination(perLabel()[value]);
  }
  function intakeBody() {
    const body = new FormData();
    if (mode() === "links") body.append("magnets", validLines().join("\n"));
    else queued().forEach((file) => body.append("files", file, file.name));
    body.append("destination", destination().trim());
    body.append("label", label().trim());
    body.append("start", String(start()));
    body.append("skip_hash_check", String(skipHashCheck()));
    body.append("sequential", String(sequential()));
    return body;
  }
  const forecast = useStorageForecast({
    key: () =>
      JSON.stringify([
        addOpen(),
        mode(),
        links(),
        queued().map((f) => [f.name, f.size, f.lastModified]),
        destination(),
        label(),
        start(),
        skipHashCheck(),
        sequential(),
      ]),
    body: () => {
      const body = intakeBody();
      body.set("kind", "add");
      return body;
    },
  });
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!validInput() || submitting()) return;
    const body = intakeBody();
    rememberDir(destination().trim());
    setSubmitting(true);
    setResults([]);
    try {
      if (!(await forecast.ready())) return;
      const response = await fetch("/api/v1/torrents/add", { method: "POST", body });
      const payload = (await response.json().catch(() => ({}))) as {
        message?: string;
        results?: AddResult[];
      };
      if (!response.ok) throw new Error(payload.message || "Could not add torrent");
      const outcome = payload.results ?? [];
      setResults(outcome);
      const success = outcome.filter((item) => item.ok).length;
      const failed = outcome.length - success;
      if (success && !failed) {
        showToast(`Added ${success} torrent${success === 1 ? "" : "s"}.`);
        setSubmitting(false);
        close();
      } else if (success)
        showToast(`Added ${success}; ${failed} item${failed === 1 ? "" : "s"} need attention.`);
    } catch (error) {
      setResults([
        {
          source: "Request",
          ok: false,
          error: error instanceof Error ? error.message : "Could not add torrent",
        },
      ]);
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Show when={addOpen()}>
      <div
        class="modal-backdrop"
        role="presentation"
        onMouseDown={(event) => {
          if (event.currentTarget === event.target) close();
        }}
      >
        <form class="add-modal" aria-label="Add torrent" onSubmit={submit}>
          <header class="modal-title">
            <h2>Add torrent</h2>
            <button type="button" aria-label="Close add torrent" onClick={close}>
              ✕
            </button>
          </header>
          <fieldset class="add-body storage-form-fields" disabled={submitting()}>
            <div
              class="add-segments"
              role="tablist"
              aria-label="Add source"
              onKeyDown={(event) => {
                if (
                  event.key !== "ArrowRight" &&
                  event.key !== "ArrowLeft" &&
                  event.key !== "Home" &&
                  event.key !== "End"
                )
                  return;
                event.preventDefault();
                event.stopPropagation();
                const next = event.key === "ArrowLeft" || event.key === "Home" ? "links" : "files";
                setMode(next as Mode);
                (
                  event.currentTarget.querySelectorAll('[role="tab"]')[
                    next === "links" ? 0 : 1
                  ] as HTMLElement
                )?.focus();
              }}
            >
              <button
                classList={{ active: mode() === "links" }}
                type="button"
                role="tab"
                aria-selected={mode() === "links"}
                tabindex={mode() === "links" ? 0 : -1}
                onClick={() => setMode("links")}
              >
                Magnet / URL
              </button>
              <button
                classList={{ active: mode() === "files" }}
                type="button"
                role="tab"
                aria-selected={mode() === "files"}
                tabindex={mode() === "files" ? 0 : -1}
                onClick={() => setMode("files")}
              >
                .torrent file
              </button>
            </div>
            <Show
              when={mode() === "links"}
              fallback={
                <FileSource
                  queued={queued()}
                  errors={[...queuedTorrentFileErrors(), ...fileErrors()]}
                  onChoose={addFiles}
                  onRemove={(index) =>
                    setQueuedTorrentFiles((current) => current.filter((_, item) => item !== index))
                  }
                />
              }
            >
              <div class="link-source" role="tabpanel" aria-label="Magnet or URL source">
                <label for="torrent-links">Magnet links — one per line</label>
                <textarea
                  id="torrent-links"
                  value={links()}
                  onInput={(event) => setLinks(event.currentTarget.value)}
                  placeholder={"magnet:?xt=urn:btih:…\nhttps://example.com/release.torrent"}
                  spellcheck={false}
                />
                <For each={badLines()}>
                  {(item) => (
                    <p class="input-error">
                      <b>{item.line}</b> — {item.error}
                    </p>
                  )}
                </For>
              </div>
            </Show>
            <div class="add-options">
              <label>
                <span>Destination</span>
                <input
                  value={destination()}
                  onInput={(event) => setDestination(event.currentTarget.value)}
                />
              </label>
              <button
                type="button"
                class="settings-add-row"
                onClick={() => setBrowsing(!browsing())}
              >
                {browsing() ? "Hide browser" : "Browse…"}
              </button>
              <Show when={browsing()}>
                <DirectoryBrowser value={destination()} onPick={setDestination} />
              </Show>
              <label>
                <span>Label</span>
                <input
                  list="torrent-labels"
                  value={label()}
                  onInput={(event) => chooseLabel(event.currentTarget.value)}
                  placeholder="Optional label"
                />
                <datalist id="torrent-labels">
                  <For each={labels()}>{(item) => <option value={item.name} />}</For>
                </datalist>
              </label>
            </div>
            <div class="add-checkboxes">
              <label>
                <input
                  type="checkbox"
                  checked={start()}
                  onChange={(event) => setStart(event.currentTarget.checked)}
                />
                Start immediately
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={skipHashCheck()}
                  onChange={(event) => setSkipHashCheck(event.currentTarget.checked)}
                />
                Skip hash check
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={sequential()}
                  onChange={(event) => setSequential(event.currentTarget.checked)}
                />
                Sequential download
              </label>
            </div>
            <StorageForecast model={forecast} />
            <Show when={results().length}>
              <div class="add-results">
                <For each={results()}>
                  {(result) => (
                    <div classList={{ success: result.ok, failure: !result.ok }}>
                      <span>{result.source}</span>
                      <b>{result.ok ? "Added" : result.error}</b>
                    </div>
                  )}
                </For>
              </div>
            </Show>
          </fieldset>
          <footer class="modal-footer">
            <button class="modal-cancel" type="button" onClick={close}>
              Cancel
            </button>
            <button class="modal-submit" type="submit" disabled={!validInput() || submitting()}>
              {submitting() ? "Adding…" : "Add"}
            </button>
          </footer>
        </form>
      </div>
    </Show>
  );
}

function FileSource(props: {
  queued: File[];
  errors: string[];
  onChoose: (files: File[]) => void;
  onRemove: (index: number) => void;
}) {
  const [dragging, setDragging] = createSignal(false);
  return (
    <div class="file-source" role="tabpanel" aria-label="Torrent file source">
      <input
        class="file-picker"
        type="file"
        accept=".torrent,application/x-bittorrent"
        multiple
        onChange={(event) => {
          props.onChoose(Array.from(event.currentTarget.files ?? []));
          event.currentTarget.value = "";
        }}
      />
      <button
        class="torrent-dropzone"
        classList={{ dragging: dragging() }}
        type="button"
        onClick={() => document.querySelector<HTMLInputElement>(".file-picker")?.click()}
        onDragOver={(event) => {
          event.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(event) => {
          event.preventDefault();
          setDragging(false);
          props.onChoose(Array.from(event.dataTransfer?.files ?? []));
        }}
      >
        <b>Drop .torrent files here</b>
        <span>or click to browse</span>
      </button>
      <For each={props.queued}>
        {(file, index) => (
          <div class="queued-file">
            <span>{file.name}</span>
            <button
              type="button"
              aria-label={`Remove ${file.name}`}
              onClick={() => props.onRemove(index())}
            >
              ✕
            </button>
          </div>
        )}
      </For>
      <For each={props.errors}>{(error) => <p class="input-error">{error}</p>}</For>
    </div>
  );
}
