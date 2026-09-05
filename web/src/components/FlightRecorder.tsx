import { For, Show, createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { observedAt, type Recording } from "../lib/flight";

const localInput = (at?: string) => {
  if (!at) return "";
  const d = new Date(at);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};

const when = (at?: string | null) => (at ? new Date(at).toLocaleString() : "Unknown");

export function FlightRecorder(props: { hash?: string; from?: string; to?: string }) {
  const [data, setData] = createSignal<Recording>();
  const [hash, setHash] = createSignal(props.hash ?? "");
  const [from, setFrom] = createSignal(localInput(props.from));
  const [to, setTo] = createSignal(localInput(props.to));
  const [applied, setApplied] = createSignal("");
  const [index, setIndex] = createSignal(0);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [preview, setPreview] = createSignal("");
  let request: AbortController | undefined;
  const events = () => data()?.events ?? [];
  const selected = () => events()[index()];
  const sample = createMemo(() =>
    observedAt(
      events(),
      index(),
      new URLSearchParams(applied()).get("hash") || selected()?.hash || "",
    ),
  );
  const causeIndex = () => events().findIndex((e) => e.id === selected()?.causeId);
  const revision = () =>
    events().find((e) => e.phase === "configuration" && e.revision === selected()?.revision);

  function params() {
    const p = new URLSearchParams({ limit: "500" });
    if (hash().trim()) p.set("hash", hash().trim());
    if (from()) p.set("from", new Date(from()).toISOString());
    if (to()) p.set("to", new Date(to()).toISOString());
    return p.toString();
  }

  async function load(query: string, exporting = false) {
    request?.abort();
    const active = new AbortController();
    request = active;
    setBusy(true);
    setError("");
    setPreview("");
    if (!exporting) {
      setData(undefined);
      setApplied(query);
    }
    try {
      const response = await fetch(
        `/api/v1/history/flight?${query}${exporting ? "&export=1" : ""}`,
        { signal: active.signal, headers: { Accept: "application/json" } },
      );
      const body = await response.json();
      if (!response.ok) throw new Error(body.error?.message || "Could not load recording");
      if (active.signal.aborted) return;
      if (exporting) setPreview(JSON.stringify(body, null, 2));
      else {
        setData(body as Recording);
        setIndex(Math.max(0, (body.events?.length ?? 0) - 1));
      }
    } catch (cause) {
      if (!active.signal.aborted)
        setError(cause instanceof Error ? cause.message : "Recording unavailable");
    } finally {
      if (!active.signal.aborted) setBusy(false);
    }
  }

  createEffect(() => {
    const value = props.hash ?? "";
    const start = localInput(props.from),
      end = localInput(props.to);
    untrack(() => {
      setHash(value);
      setFrom(start);
      setTo(end);
      void load(params());
    });
  });
  onCleanup(() => request?.abort());

  function download() {
    const url = URL.createObjectURL(new Blob([preview()], { type: "application/json" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "blackbird-incident-v1.json";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function previewIncident() {
    const query = new URLSearchParams(applied());
    // Freeze the bounds to the displayed recording; download later uses the
    // exact JSON shown in the preview, never a second live fetch.
    const dates = events().map((e) => Date.parse(e.at));
    if (dates.length) {
      query.set("from", new Date(Math.min(...dates)).toISOString());
      // JS dates truncate the recorder's nanoseconds. Include the whole final
      // millisecond so the selected last event remains in the export window.
      query.set("to", new Date(Math.max(...dates) + 1).toISOString());
    }
    void load(query.toString(), true);
  }

  return (
    <section class="flight-recorder" aria-label="Session flight recorder" aria-busy={busy()}>
      <h2>Session flight recorder</h2>
      <p>
        Inspect what was requested, what returned, and what was observed. Recording order alone does
        not establish a cause.
      </p>
      <form
        class="flight-controls"
        onSubmit={(event) => {
          event.preventDefault();
          void load(params());
        }}
      >
        <label>
          Torrent hash
          <input
            value={hash()}
            onInput={(e) => setHash(e.currentTarget.value)}
            placeholder="All torrents + session events"
          />
        </label>
        <label>
          From (local time)
          <input
            type="datetime-local"
            value={from()}
            onInput={(e) => setFrom(e.currentTarget.value)}
          />
        </label>
        <label>
          To (local time)
          <input type="datetime-local" value={to()} onInput={(e) => setTo(e.currentTarget.value)} />
        </label>
        <button class="copy-button" type="submit" disabled={busy()}>
          Load incident
        </button>
      </form>
      <Show when={error()}>
        <p role="alert">
          {error()}{" "}
          <button class="copy-button" type="button" onClick={() => void load(applied())}>
            Retry
          </button>
        </p>
      </Show>
      <Show when={busy()}>
        <p role="status">Loading recording…</p>
      </Show>
      <Show when={data()}>
        {(view) => (
          <>
            <p class="flight-status" role="status">
              Last saved: {when(view().status.lastPersistedAt)} · {view().status.pending} pending ·{" "}
              {view().status.dropped} dropped · {view().status.pruned} expired/evicted.
              {view().status.error ? ` ${view().status.error}` : ""}
            </p>
            <Show
              when={events().length}
              fallback={<p>No retained events in this window. Try a wider time range.</p>}
            >
              <label class="flight-scrubber">
                Recording position {index() + 1} of {events().length}
                <input
                  type="range"
                  min="0"
                  max={Math.max(0, events().length - 1)}
                  value={index()}
                  aria-label="Recording position"
                  onInput={(e) => setIndex(Number(e.currentTarget.value))}
                />
              </label>
              <div class="flight-timeline">
                <div class="flight-events" aria-label="Recorded events">
                  <For each={events()}>
                    {(event, i) => (
                      <button
                        type="button"
                        aria-pressed={index() === i()}
                        onClick={() => setIndex(i())}
                      >
                        <time>{when(event.at)}</time>
                        <strong>
                          {event.phase} · {event.action || "event"}
                        </strong>
                        <span>{event.name || event.hash || "Session"}</span>
                      </button>
                    )}
                  </For>
                </div>
                <Show when={selected()}>
                  {(event) => (
                    <article class="flight-evidence" aria-label="Selected evidence">
                      <h3>
                        {event().phase} · {event().action}
                      </h3>
                      <p>
                        {when(event().at)} · {event().actor || "Unknown actor"} ·{" "}
                        {event().result || "No result recorded"}
                      </p>
                      <p>{event().message}</p>
                      <p class="flight-id">
                        Event {event().id} · revision {event().revision || "unavailable"}
                      </p>
                      <Show when={event().causeId}>
                        <Show
                          when={causeIndex() >= 0}
                          fallback={<p>Linked intent is outside this retained window.</p>}
                        >
                          <button
                            class="copy-button"
                            type="button"
                            onClick={() => setIndex(causeIndex())}
                          >
                            Inspect linked intent
                          </button>
                        </Show>
                      </Show>
                      <Show when={event().revision && !revision()}>
                        <p>
                          Configuration for this revision is outside this window or was not
                          captured.
                        </p>
                      </Show>
                      <Show when={revision()}>
                        <details>
                          <summary>Recorded configuration revision</summary>
                          <pre>{JSON.stringify(revision()?.after, null, 2)}</pre>
                        </details>
                      </Show>
                      <table>
                        <caption>Evidence values (requests are not applied to replay)</caption>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Before</th>
                            <th>After / requested</th>
                          </tr>
                        </thead>
                        <tbody>
                          <For
                            each={[
                              ...new Set([
                                ...Object.keys(event().before ?? {}),
                                ...Object.keys(event().after ?? {}),
                              ]),
                            ]}
                          >
                            {(key) => (
                              <tr>
                                <th>{key}</th>
                                <td>{event().before?.[key] ?? "Unknown"}</td>
                                <td>{event().after?.[key] ?? "Unknown"}</td>
                              </tr>
                            )}
                          </For>
                        </tbody>
                      </table>
                      <h4>Last observed torrent state at this position</h4>
                      <Show
                        when={sample()}
                        fallback={
                          <p>Unknown: no retained sample after the latest gap for this torrent.</p>
                        }
                      >
                        {(state) => (
                          <p>
                            {state().after?.state} · sampled {when(state().at)}. Later requests do
                            not change this observation.
                          </p>
                        )}
                      </Show>
                    </article>
                  )}
                </Show>
              </div>
              <button class="copy-button" type="button" disabled={busy()} onClick={previewIncident}>
                Preview incident export
              </button>
            </Show>
            <details class="flight-coverage">
              <summary>Coverage and uncertainty</summary>
              <ul>
                <For each={view().coverage}>{(line) => <li>{line}</li>}</For>
              </ul>
            </details>
          </>
        )}
      </Show>
      <Show when={preview()}>
        <section class="flight-preview" aria-label="Incident export preview">
          <h3>Incident export preview</h3>
          <p>
            Review this local bundle. Free-form text, names, URLs and paths are omitted. Download
            saves exactly the JSON below.
          </p>
          <pre>{preview()}</pre>
          <button class="copy-button" type="button" onClick={download}>
            Download reviewed bundle
          </button>
          <button class="copy-button" type="button" onClick={() => setPreview("")}>
            Close preview
          </button>
        </section>
      </Show>
    </section>
  );
}
