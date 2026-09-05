import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { closeMove, moveHashes, moveOpen, showToast } from "../store/ui";
import { torrentList } from "../store/session";
import { DirectoryBrowser, rememberDir } from "./DirectoryBrowser";

import { StorageForecast, useStorageForecast } from "./StorageForecast";

type Mode = "move_files" | "set_directory";
type Job = {
  id: string;
  status: "running" | "completed" | "cancelled";
  results: Array<{ hash: string; status: string; error?: string }>;
};

export function MoveDataModal() {
  const [mode, setMode] = createSignal<Mode>("move_files");
  const [destination, setDestination] = createSignal("");
  const [job, setJob] = createSignal<Job | null>(null);
  const [perLabelDestinations, setPerLabelDestinations] = createSignal<Record<string, string>>({});
  const selected = createMemo(() =>
    torrentList().filter((torrent) => moveHashes().includes(torrent.hash)),
  );
  const [submitting, setSubmitting] = createSignal(false);
  const forecast = useStorageForecast({
    key: () => JSON.stringify([moveOpen(), moveHashes(), destination(), mode()]),
    body: () => {
      const body = new FormData();
      body.set("kind", "move");
      body.set("destination", destination());
      body.set("mode", mode());
      moveHashes().forEach((hash) => body.append("hashes", hash));
      return body;
    },
  });
  let timer: number | undefined;
  createEffect(() => {
    if (moveOpen()) {
      setJob(null);
      setDestination("");
      setMode("move_files");
      void fetch("/api/v1/settings")
        .then((response) =>
          response.ok
            ? (response.json() as Promise<{ directories?: { per_label?: Record<string, string> } }>)
            : null,
        )
        .then((settings) => setPerLabelDestinations(settings?.directories?.per_label ?? {}))
        .catch(() => {});
    }
  });
  onMount(() => {
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape" && moveOpen() && !job() && !submitting()) closeMove();
    };
    document.addEventListener("keydown", key);
    onCleanup(() => {
      document.removeEventListener("keydown", key);
      if (timer) window.clearInterval(timer);
    });
  });
  const poll = (id: string) => {
    if (timer) window.clearInterval(timer);
    timer = window.setInterval(
      () =>
        void fetch(`/api/v1/torrents/move/${encodeURIComponent(id)}`)
          .then((response) => (response.ok ? (response.json() as Promise<Job>) : Promise.reject()))
          .then((next) => {
            setJob(next);
            if (next.status !== "running" && timer) window.clearInterval(timer);
          })
          .catch(() => {}),
      400,
    );
  };
  const start = async () => {
    if (!destination() || job() || submitting()) return;
    setSubmitting(true);
    try {
      if (!(await forecast.ready())) return;
      const response = await fetch("/api/v1/torrents/move", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ hashes: moveHashes(), destination: destination(), mode: mode() }),
      });
      const data = (await response.json().catch(() => ({}))) as Job & {
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(data.error?.message || "Could not start move");
      rememberDir(destination());
      setJob(data);
      poll(data.id);
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Could not start move");
    } finally {
      setSubmitting(false);
    }
  };
  const cancel = async () => {
    const active = job();
    if (active)
      await fetch(`/api/v1/torrents/move/${encodeURIComponent(active.id)}/cancel`, {
        method: "POST",
      });
  };
  return (
    <Show when={moveOpen()}>
      <div class="modal-backdrop" role="presentation">
        <section class="move-modal" role="dialog" aria-modal="true" aria-label="Move torrent data">
          <header class="modal-title">
            <h2>Move torrent data</h2>
            <button
              type="button"
              disabled={submitting() || Boolean(job()?.status === "running")}
              onClick={closeMove}
            >
              ✕
            </button>
          </header>
          <fieldset class="add-body storage-form-fields" disabled={submitting()}>
            <p class="move-notice">
              <Show when={selected().some((torrent) => torrent.state !== "stopped")}>
                Running torrents will be stopped, updated, and restarted automatically.{" "}
              </Show>
              {mode() === "move_files"
                ? "Files are moved with copy-and-verify fallback across filesystems."
                : "Use this only after data has already been relocated."}
            </p>
            <div class="add-segments">
              <button
                classList={{ active: mode() === "move_files" }}
                type="button"
                disabled={Boolean(job())}
                onClick={() => setMode("move_files")}
              >
                Move files
              </button>
              <button
                classList={{ active: mode() === "set_directory" }}
                type="button"
                disabled={Boolean(job())}
                onClick={() => setMode("set_directory")}
              >
                Set directory only
              </button>
            </div>
            <div class="move-shortcuts">
              <span>Configured roots and label destinations</span>
              <For each={Object.values(perLabelDestinations())}>
                {(path) => (
                  <button
                    type="button"
                    disabled={Boolean(job())}
                    onClick={() => setDestination(path)}
                  >
                    {path}
                  </button>
                )}
              </For>
            </div>
            <DirectoryBrowser value={destination()} onPick={setDestination} pickDefault={false} />
            <Show when={destination()}>
              <p class="move-destination tnum">Destination: {destination()}</p>
            </Show>
            <Show when={!job()}>
              <StorageForecast model={forecast} />
            </Show>
            <Show when={job()}>
              {(active) => (
                <div class="add-results">
                  <For each={active().results}>
                    {(result) => (
                      <div
                        classList={{
                          success: result.status === "completed",
                          failure: result.status === "failed",
                        }}
                      >
                        <span>{result.hash}</span>
                        <b>{result.error || result.status}</b>
                      </div>
                    )}
                  </For>
                </div>
              )}
            </Show>
          </fieldset>
          <footer class="modal-footer">
            <Show
              when={job()?.status === "running"}
              fallback={
                <>
                  <button
                    class="modal-cancel"
                    type="button"
                    disabled={submitting()}
                    onClick={closeMove}
                  >
                    {job() ? "Close" : "Cancel"}
                  </button>
                  <button
                    class="modal-submit"
                    type="button"
                    disabled={!destination() || submitting() || Boolean(job())}
                    onClick={() => void start()}
                  >
                    Continue
                  </button>
                </>
              }
            >
              <button class="modal-cancel" type="button" onClick={() => void cancel()}>
                Cancel move
              </button>
            </Show>
          </footer>
        </section>
      </div>
    </Show>
  );
}
