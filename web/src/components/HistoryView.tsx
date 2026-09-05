// History view (PAR-5.3): the global torrent event log — adds, completions,
// moves, rule actions, removals, scheduler applications, and daemon messages
// — with kind/actor/search filters and newest-first pagination. Reached from
// the sidebar Views group. The per-torrent Logger tab shows the same store's
// subset for one hash.

import { For, Show, createMemo, createSignal, onMount } from "solid-js";
import { FlightRecorder } from "./FlightRecorder";

export interface HistoryEvent {
  seq: number;
  at: string; // RFC3339
  hash: string;
  name?: string;
  kind: "action" | "message" | "add" | "move" | "complete";
  actor?: string;
  action?: string;
  result?: string;
  message?: string;
}

const KINDS = ["action", "add", "move", "message", "complete"] as const;
const KIND_LABEL: Record<string, string> = {
  action: "Action",
  add: "Add",
  move: "Move",
  message: "Message",
  complete: "Completed",
};
const PAGE_SIZE = 50;

function formatEventTime(at: string): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function subject(event: HistoryEvent): string {
  if (event.name) return event.name;
  if (event.hash) return event.hash.length > 12 ? `${event.hash.slice(0, 12)}…` : event.hash;
  return "—";
}

function EventRow(props: { event: HistoryEvent }) {
  const event = () => props.event;
  return (
    <div class="history-row" classList={{ [`kind-${event().kind}`]: true }}>
      <span class="history-time" title={event().at}>
        {formatEventTime(event().at)}
      </span>
      <span class="history-kind">{KIND_LABEL[event().kind] ?? event().kind}</span>
      <span class="history-subject" title={event().hash || event().name || ""}>
        {subject(event())}
      </span>
      <span class="history-actor">{event().actor || "—"}</span>
      <span class="history-summary">
        {event().action || "—"}
        {event().result && event().result !== "info" ? ` · ${event().result}` : ""}
      </span>
      <Show when={event().message}>
        <small class="history-message" title={event().message}>
          {event().message}
        </small>
      </Show>
    </div>
  );
}

export function HistoryView() {
  const [flight, setFlight] = createSignal(false);
  const [events, setEvents] = createSignal<HistoryEvent[]>([]);
  const [hasMore, setHasMore] = createSignal(false);
  const [cursor, setCursor] = createSignal(0);
  const [loading, setLoading] = createSignal(true);
  const [loadingMore, setLoadingMore] = createSignal(false);
  const [failed, setFailed] = createSignal(false);
  // Draft filter inputs; applied on Apply/Enter so typing never refetches.
  const [kind, setKind] = createSignal("");
  const [actor, setActor] = createSignal("");
  const [query, setQuery] = createSignal("");
  const [applied, setApplied] = createSignal({ kind: "", actor: "", q: "" });
  const activeFilterCount = createMemo(
    () => (applied().kind ? 1 : 0) + (applied().actor ? 1 : 0) + (applied().q ? 1 : 0),
  );

  function queryString(beforeSeq: number) {
    const params = new URLSearchParams({ limit: String(PAGE_SIZE) });
    if (beforeSeq) params.set("before_seq", String(beforeSeq));
    const f = applied();
    if (f.kind) params.set("kind", f.kind);
    if (f.actor) params.set("actor", f.actor);
    if (f.q) params.set("q", f.q);
    return params.toString();
  }

  async function loadPage(beforeSeq: number, append: boolean) {
    if (append) setLoadingMore(true);
    else {
      setLoading(true);
      setFailed(false);
    }
    try {
      const response = await fetch(`/api/v1/history?${queryString(beforeSeq)}`, {
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error();
      const page = (await response.json()) as {
        events: HistoryEvent[];
        nextBeforeSeq: number;
        hasMore: boolean;
      };
      setEvents((current) => (append ? [...current, ...(page.events ?? [])] : (page.events ?? [])));
      setCursor(page.nextBeforeSeq ?? 0);
      setHasMore(Boolean(page.hasMore));
    } catch {
      if (!append) {
        setEvents([]);
        setFailed(true);
      }
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }

  function applyKind(value: string) {
    setKind(value);
    // Kind applies immediately but preserves the other applied filters;
    // actor/search drafts stay untouched until Apply.
    setApplied((current) => ({ ...current, kind: value }));
    void loadPage(0, false);
  }

  function apply() {
    setApplied({ kind: kind(), actor: actor().trim(), q: query().trim() });
  }

  function refresh() {
    void loadPage(0, false);
  }

  // Page one loads on mount; every filter interaction above reloads it
  // explicitly so typing in actor/search never refetches mid-keystroke.
  onMount(() => {
    void loadPage(0, false);
  });

  return (
    <div class="history-view">
      <header class="history-header">
        <h1>History</h1>
        <span class="history-header-actions">
          <button type="button" onClick={() => setFlight(!flight())}>
            {flight() ? "Activity history" : "Flight recorder"}
          </button>
          <Show when={loading()}>
            <span class="settings-intro">Loading…</span>
          </Show>
          <button type="button" onClick={refresh}>
            Refresh
          </button>
        </span>
      </header>
      <Show when={flight()}>
        <FlightRecorder />
      </Show>
      <Show when={!flight()}>
        <form
          class="history-filters"
          onSubmit={(event) => {
            event.preventDefault();
            apply();
            void loadPage(0, false);
          }}
        >
          <label>
            Kind
            <select value={kind()} onChange={(event) => applyKind(event.currentTarget.value)}>
              <option value="">All kinds</option>
              <For each={KINDS}>{(k) => <option value={k}>{KIND_LABEL[k]}</option>}</For>
            </select>
          </label>
          <label>
            Actor{" "}
            <input
              value={actor()}
              placeholder="user, watch, rss…"
              onInput={(event) => setActor(event.currentTarget.value)}
            />
          </label>
          <label class="history-search">
            Search{" "}
            <input
              value={query()}
              placeholder="name, hash, message…"
              onInput={(event) => setQuery(event.currentTarget.value)}
            />
          </label>
          <button type="submit">Apply</button>
          <Show when={activeFilterCount() > 0}>
            <button
              type="button"
              onClick={() => {
                setKind("");
                setActor("");
                setQuery("");
                setApplied({ kind: "", actor: "", q: "" });
                void loadPage(0, false);
              }}
            >
              Clear
            </button>
          </Show>
        </form>

        <Show when={failed()}>
          <p class="settings-intro">
            Could not load history.{" "}
            <button type="button" onClick={refresh}>
              Retry
            </button>
          </p>
        </Show>
        <Show when={!failed() && !loading() && events().length === 0}>
          <p class="settings-intro">
            No events yet. Actions, adds, completions, moves, rule outcomes, and scheduler
            applications appear here as they happen.
          </p>
        </Show>
        <Show when={loading() && events().length === 0}>
          <div class="list-skeleton" aria-hidden="true">
            <span />
            <span />
            <span />
            <span />
          </div>
        </Show>
        <div class="history-list">
          <For each={events()}>{(event) => <EventRow event={event} />}</For>
        </div>
        <Show when={hasMore()}>
          <div class="history-more">
            <button
              type="button"
              disabled={loadingMore()}
              onClick={() => void loadPage(cursor(), true)}
            >
              {loadingMore() ? "Loading…" : "Load more"}
            </button>
          </div>
        </Show>
      </Show>
    </div>
  );
}
