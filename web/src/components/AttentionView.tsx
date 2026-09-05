import { For, Show, createMemo, createEffect, createSignal, onCleanup, onMount } from "solid-js";
import { FlightRecorder } from "./FlightRecorder";
import { WhyTab } from "./WhyTab";
import { navigate } from "../store/ui";
import { setAttentionCount } from "../store/attention";

export interface Incident {
  id: string;
  kind: string;
  title: string;
  evidence: string;
  nextStep: string;
  hashes: string[];
  affected: number;
  firstSeen: string;
  lastSeen: string;
  episodeStarted: string;
  episode: number;
  active: boolean;
  status: "open" | "acknowledged" | "snoozed" | "resolved";
  snoozedUntil?: string;
  acknowledgedAt?: string;
  resolvedAt?: string;
}
export interface Inbox {
  state: {
    incidents: Incident[];
    lastVisit: string | null;
    observedAt: string | null;
    savedAt: string | null;
    error?: string;
    omitted: number;
    pruned: number;
    coverage: string[];
  };
  since: string;
  generatedAt: string;
  completedCount: number;
  summaryCoverage: string;
  completed: { id: string; hash: string; action: string; at: string }[];
}
const when = (s?: string | null) => (s ? new Date(s).toLocaleString() : "Not yet recorded");

export function AttentionView() {
  const [data, setData] = createSignal<Inbox>();
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [filter, setFilter] = createSignal("unresolved");
  const [evidence, setEvidence] = createSignal<{
    mode: "why" | "flight";
    hash: string;
    from?: string;
    to?: string;
  }>();
  let controller: AbortController | undefined;
  let since = "";
  let visited = false;
  let disposed = false;
  let evidencePanel: HTMLElement | undefined;
  createEffect(() => {
    if (evidence()) queueMicrotask(() => evidencePanel?.scrollIntoView?.({ block: "start" }));
  });
  const incidents = createMemo(() =>
    (data()?.state.incidents ?? [])
      .filter(
        (i) =>
          filter() === "all" ||
          (filter() === "unresolved" ? i.status !== "resolved" : i.status === filter()),
      )
      .sort((a, b) => {
        const rank = { open: 0, acknowledged: 1, snoozed: 2, resolved: 3 };
        return (
          rank[a.status] - rank[b.status] ||
          Date.parse(b.episodeStarted) - Date.parse(a.episodeStarted)
        );
      }),
  );
  async function post(body: object) {
    const response = await fetch("/api/v1/attention", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: controller?.signal,
    });
    const result = await response.json();
    if (!response.ok)
      throw new Error(
        result.error?.message || "Attention change was not confirmed. Refresh before retrying.",
      );
  }
  async function load() {
    controller?.abort();
    const active = new AbortController();
    controller = active;
    setBusy(true);
    setError("");
    try {
      const response = await fetch(
        `/api/v1/attention${since ? `?since=${encodeURIComponent(since)}` : ""}`,
        { signal: active.signal },
      );
      const body = await response.json();
      if (!response.ok) throw new Error(body.error?.message || "Could not load attention inbox");
      if (active.signal.aborted) return;
      const inbox = body as Inbox;
      since ||= inbox.since;
      setData(inbox);
      setAttentionCount(inbox.state.incidents.filter((i) => i.status === "open").length);
      if (!visited) {
        await post({ action: "visit", visitedAt: inbox.generatedAt });
        visited = true;
      }
    } catch (e) {
      if (!active.signal.aborted) setError(e instanceof Error ? e.message : "Inbox unavailable");
    } finally {
      if (!active.signal.aborted) setBusy(false);
    }
  }
  async function update(inbox: Incident, action: string, seconds?: number) {
    setBusy(true);
    setError("");
    try {
      await post({ id: inbox.id, episode: inbox.episode, action, seconds });
      if (!disposed) await load();
    } catch (e) {
      if (!disposed) {
        setError(e instanceof Error ? e.message : "Update not confirmed; refresh before retrying.");
        setBusy(false);
      }
    }
  }
  function recording(hash: string, at: string, end: string) {
    setEvidence({
      mode: "flight",
      hash,
      from: new Date(Date.parse(at) - 60000).toISOString(),
      to: new Date(Date.parse(end) + 60000).toISOString(),
    });
  }
  onMount(() => void load());
  onCleanup(() => {
    disposed = true;
    controller?.abort();
  });
  return (
    <main class="attention-view" aria-label="Attention inbox" aria-busy={busy()}>
      <header class="attention-header">
        <div>
          <h1>Attention inbox</h1>
          <p>Shared symptoms, decisions, and observed recovery.</p>
        </div>
        <div class="attention-actions">
          <button type="button" disabled={busy()} onClick={() => void load()}>
            Refresh
          </button>
          <button type="button" onClick={() => navigate("console")}>
            Back to console
          </button>
        </div>
      </header>
      <Show when={error()}>
        <p role="alert">{error()}</p>
      </Show>
      <Show when={busy()}>
        <p role="status">Loading or saving inbox…</p>
      </Show>
      <Show when={data()}>
        {(inbox) => (
          <>
            <section class="attention-summary" aria-label="Since your last visit">
              <h2>Since your last visit</h2>
              <p>
                Since {when(inbox().since)} ·{" "}
                {inbox().state.incidents.filter((i) => i.active).length} unresolved incidents ·{" "}
                {inbox().completedCount} important completed actions in retained history.
              </p>
              <details>
                <summary>Completed actions ({inbox().completed.length} shown)</summary>
                <p>{inbox().summaryCoverage}</p>
                <ul>
                  <For each={inbox().completed}>
                    {(item) => (
                      <li>
                        <time>{when(item.at)}</time> · {item.action || "Download completed"} ·{" "}
                        {item.hash || "Session"}{" "}
                        <button
                          type="button"
                          onClick={() => recording(item.hash, item.at, item.at)}
                        >
                          Recorded evidence
                        </button>
                      </li>
                    )}
                  </For>
                </ul>
              </details>
            </section>
            <p class="attention-status">
              Last observation: {when(inbox().state.observedAt)} · Last saved:{" "}
              {when(inbox().state.savedAt)} · {inbox().state.omitted} groups omitted at capacity ·{" "}
              {inbox().state.pruned} expired/evicted.
            </p>
            <Show when={inbox().state.error}>
              <p role="alert">{inbox().state.error}</p>
            </Show>
            <label class="attention-filter">
              Show{" "}
              <select value={filter()} onChange={(e) => setFilter(e.currentTarget.value)}>
                <option value="unresolved">Unresolved</option>
                <option value="open">Needs attention</option>
                <option value="acknowledged">Acknowledged</option>
                <option value="snoozed">Snoozed</option>
                <option value="resolved">Resolved</option>
                <option value="all">All retained</option>
              </select>
            </label>
            <Show when={incidents().length} fallback={<p>No incidents in this view.</p>}>
              <div class="attention-list">
                <For each={incidents()}>
                  {(item) => (
                    <article class="attention-incident" aria-label={item.title}>
                      <header>
                        <h2>{item.title}</h2>
                        <span class={`attention-state attention-${item.status}`}>
                          {item.status}
                        </span>
                      </header>
                      <p>{item.evidence}</p>
                      <p>
                        <strong>Next step:</strong> {item.nextStep}
                      </p>
                      <p class="attention-times">
                        First observed {when(item.firstSeen)} · Last observed {when(item.lastSeen)}{" "}
                        · Episode {item.episode} · {item.affected} affected torrents
                      </p>
                      <Show when={item.snoozedUntil}>
                        <p>Snoozed until {when(item.snoozedUntil)}.</p>
                      </Show>
                      <Show when={item.resolvedAt}>
                        <p>Recovery observed {when(item.resolvedAt)}.</p>
                      </Show>
                      <div class="attention-actions">
                        <Show when={item.active}>
                          <button
                            type="button"
                            disabled={busy() || item.status === "acknowledged"}
                            onClick={() => void update(item, "acknowledge")}
                          >
                            Acknowledge
                          </button>
                          <button
                            type="button"
                            disabled={busy()}
                            onClick={() => void update(item, "snooze", 3600)}
                          >
                            Snooze 1 hour
                          </button>
                          <button
                            type="button"
                            disabled={busy()}
                            onClick={() => void update(item, "snooze", 86400)}
                          >
                            Snooze 1 day
                          </button>
                          <Show when={item.status === "snoozed"}>
                            <button
                              type="button"
                              disabled={busy()}
                              onClick={() => void update(item, "resume")}
                            >
                              End snooze
                            </button>
                          </Show>
                        </Show>
                        <button
                          type="button"
                          onClick={() =>
                            recording(
                              "",
                              item.episodeStarted,
                              item.resolvedAt || inbox().generatedAt,
                            )
                          }
                        >
                          Recorded evidence
                        </button>
                      </div>
                      <Show when={item.hashes.length}>
                        <details>
                          <summary>Affected torrents ({item.hashes.length} listed)</summary>
                          <ul class="attention-hashes">
                            <For each={item.hashes}>
                              {(hash) => (
                                <li>
                                  <code>{hash}</code>
                                  <button
                                    type="button"
                                    onClick={() => setEvidence({ mode: "why", hash })}
                                  >
                                    Why?
                                  </button>
                                  <button
                                    type="button"
                                    onClick={() =>
                                      recording(
                                        hash,
                                        item.episodeStarted,
                                        item.resolvedAt || inbox().generatedAt,
                                      )
                                    }
                                  >
                                    Recording
                                  </button>
                                </li>
                              )}
                            </For>
                          </ul>
                        </details>
                      </Show>
                    </article>
                  )}
                </For>
              </div>
            </Show>
            <details class="attention-coverage">
              <summary>Coverage and recurrence rules</summary>
              <For each={inbox().state.coverage}>{(line) => <p>{line}</p>}</For>
              <p>
                Acknowledgement lasts for the current episode. A fresh failure after confirmed
                recovery opens a new episode. Snooze expiry returns an unacknowledged incident to
                attention. These controls do not change torrents.
              </p>
            </details>
          </>
        )}
      </Show>
      <Show when={evidence()}>
        {(selected) => (
          <section ref={evidencePanel} class="attention-evidence" aria-label="Incident evidence">
            <header>
              <h2>Incident evidence</h2>
              <button type="button" onClick={() => setEvidence(undefined)}>
                Close evidence
              </button>
            </header>
            <Show
              when={selected().mode === "why"}
              fallback={
                <FlightRecorder hash={selected().hash} from={selected().from} to={selected().to} />
              }
            >
              <WhyTab hash={selected().hash} />
            </Show>
          </section>
        )}
      </Show>
    </main>
  );
}
