import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { torrentList } from "../store/session";
import { navigate, selectedHashes, setFocusedHash, setQuery } from "../store/ui";

type Sample = {
  at: string;
  observedAt?: string;
  seeds: number | null;
  complete: boolean | null;
  status: string;
};
type Tracker = {
  source: string;
  host: string;
  cachedAt: string;
  seeds: number | null;
  enabled: boolean;
};
type Watch = {
  hash: string;
  name: string;
  revision: number;
  pinned: boolean;
  reason: string;
  reviewDate: string;
  reviewDue: boolean;
  lastActivity?: string;
  since: string;
  band: string;
  evidence: string;
  coverage: number;
  latest?: Sample;
  samples?: Sample[];
  trackers: Tracker[];
  trackerHistory?: Tracker[];
  trackersOmitted: number;
};
type Response = { watches: Watch[]; error: string; coverage: string; generatedAt?: string };
const when = (s?: string) => (s ? new Date(s).toLocaleString() : "Not observed");
const bands: Record<string, string> = {
  few_seeds: "Few seeds observed repeatedly",
  recent_low: "Recent low observation",
  mixed: "Mixed observations",
  more_seeds: "More connected seeds observed",
  unknown: "Insufficient current evidence",
};
export function PreservationView() {
  const [data, setData] = createSignal<Response>();
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [filter, setFilter] = createSignal("all");
  const [search, setSearch] = createSignal("");
  const [hash, setHash] = createSignal(selectedHashes()[0] || "");
  const [detail, setDetail] = createSignal<Watch>();
  const controller = new AbortController();
  const watchedHashes = createMemo(() => new Set(data()?.watches.map((w) => w.hash)));
  const candidates = createMemo(() => {
    const matches = [];
    const term = search().toLowerCase();
    for (const t of torrentList()) {
      if (
        !watchedHashes().has(t.hash.toUpperCase()) &&
        `${t.name} ${t.hash}`.toLowerCase().includes(term)
      )
        matches.push(t);
      if (matches.length === 50) break;
    }
    return matches;
  });
  const watches = createMemo(() =>
    (data()?.watches || []).filter(
      (w) =>
        filter() === "all" ||
        (filter() === "pinned"
          ? w.pinned
          : filter() === "due"
            ? w.reviewDue
            : w.band === "few_seeds"),
    ),
  );
  async function request(path: string, body?: object) {
    const response = await fetch(`/api/v1/preservation${path}`, {
      ...(body
        ? {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          }
        : {}),
      signal: controller.signal,
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message || "Preservation request failed");
    return result;
  }
  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      if (!controller.signal.aborted)
        setError(
          e instanceof Error ? e.message : "Request not confirmed; refresh before retrying.",
        );
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  }
  const load = async () => {
    setData(await request(""));
  };
  const change = (body: object) =>
    run(async () => {
      await request("", body);
      setHash("");
      setDetail(undefined);
      await load();
    });
  onMount(() => void run(load));
  onCleanup(() => controller.abort());
  return (
    <main
      class="preservation-view attention-view"
      aria-label="Preservation watchlist"
      aria-busy={busy()}
    >
      <header class="attention-header">
        <div>
          <h1>Preservation watchlist</h1>
          <p>Choose what deserves storage and upload capacity.</p>
        </div>
        <div class="attention-actions">
          <button disabled={busy()} onClick={() => void run(load)}>
            Refresh
          </button>
          <button onClick={() => navigate("console")}>Back to console</button>
        </div>
      </header>
      <p>
        Few connected seeds are an observation of this client, not proof of swarm-wide rarity. Pins
        block Blackbird removal, seeding cleanup, and source-archive deletion. Stop policies still
        apply.
      </p>
      <Show when={error() || data()?.error}>
        <p role="alert">{error() || data()?.error}</p>
      </Show>
      <fieldset disabled={busy()} class="preservation-add">
        <legend>Watch a torrent</legend>
        <label>
          Find in session
          <input type="search" value={search()} onInput={(e) => setSearch(e.currentTarget.value)} />
        </label>
        <label>
          Torrent
          <select value={hash()} onChange={(e) => setHash(e.currentTarget.value)}>
            <option value="">Choose a torrent</option>
            <For each={candidates()}>{(t) => <option value={t.hash}>{t.name}</option>}</For>
          </select>
        </label>
        <button disabled={!hash()} onClick={() => void change({ action: "watch", hash: hash() })}>
          Watch torrent
        </button>
        <small>
          Up to 50 matches shown. Watching starts new observations; it does not fetch trackers.
        </small>
      </fieldset>
      <label class="attention-filter">
        Show{" "}
        <select
          aria-label="Watchlist filter"
          value={filter()}
          onChange={(e) => setFilter(e.currentTarget.value)}
        >
          <option value="all">All watched</option>
          <option value="low">Few seeds repeatedly</option>
          <option value="pinned">Pinned</option>
          <option value="due">Review due</option>
        </select>
      </label>
      <p>
        Evaluated at {when(data()?.generatedAt)}. {data()?.coverage}
      </p>
      <Show when={watches().length} fallback={<p>No watched torrents in this view.</p>}>
        <div class="attention-list">
          <For each={watches()}>
            {(w) => (
              <article class="attention-incident" aria-label={w.name || w.hash}>
                <header>
                  <h2>{w.name || w.hash}</h2>
                  <strong>{w.pinned ? "Pinned" : "Watching"}</strong>
                </header>
                <p>
                  <strong>{bands[w.band]}</strong> · {Math.round(w.coverage * 100)}% observation
                  coverage
                </p>
                <p>{w.evidence}</p>
                <p>
                  Last sample: {when(w.latest?.at)} · Connected seeds:{" "}
                  {w.latest?.seeds ?? "Unknown"} · Local completion:{" "}
                  {w.latest?.complete == null
                    ? "Unknown"
                    : w.latest.complete
                      ? "Complete"
                      : "Incomplete"}
                </p>
                <p>
                  {w.latest?.status || "Waiting for the next five-minute sample"} · Last observed
                  transfer activity: {when(w.lastActivity)}
                </p>
                <Show when={w.reviewDue}>
                  <p>
                    <strong>Pin review due. The pin remains active until you remove it.</strong>
                  </p>
                </Show>
                <PinEditor watch={w} busy={busy()} save={(body) => void change(body)} />
                <div class="attention-actions">
                  <button
                    disabled={busy() || w.pinned}
                    onClick={() =>
                      void change({ action: "unwatch", hash: w.hash, revision: w.revision })
                    }
                  >
                    Stop watching
                  </button>
                  <button
                    onClick={() => {
                      setQuery(w.hash);
                      setFocusedHash(
                        torrentList().find((t) => t.hash.toUpperCase() === w.hash)?.hash || w.hash,
                      );
                      navigate("console");
                    }}
                  >
                    Open torrent
                  </button>
                  <button
                    disabled={busy()}
                    onClick={() =>
                      void run(async () => {
                        const response = await request(`?hash=${encodeURIComponent(w.hash)}`);
                        setDetail(response.watches[0]);
                      })
                    }
                  >
                    Observation history
                  </button>
                </div>
                <details>
                  <summary>Tracker evidence ({w.trackers.length} cached sources)</summary>
                  <p>
                    Report timestamps are unavailable. Cached scrape counts can be old and never
                    contribute to the risk band. Opening torrent details may populate this cache
                    through normal polling.
                  </p>
                  <Show when={!w.trackers.length}>
                    <p>No tracker report observed.</p>
                  </Show>
                  <For each={w.trackers}>
                    {(t) => (
                      <p>
                        {t.host} · Source {t.source} · {t.enabled ? "Enabled" : "Disabled"} · Seeds:{" "}
                        {t.seeds ?? "Unknown"} · Cache read {when(t.cachedAt)} · Report age unknown
                      </p>
                    )}
                  </For>
                  <Show when={w.trackersOmitted}>
                    <p>{w.trackersOmitted} additional sources omitted.</p>
                  </Show>
                </details>
                <Show when={detail()?.hash === w.hash}>
                  <section aria-label="Recorded observations">
                    <button onClick={() => setDetail(undefined)}>Close history</button>
                    <p>Retained tracker cache reads (report age unknown):</p>
                    <For each={detail()?.trackerHistory}>
                      {(t) => (
                        <p>
                          {t.host} · {t.source} · {when(t.cachedAt)} · Seeds: {t.seeds ?? "Unknown"}
                        </p>
                      )}
                    </For>
                    <div class="preservation-history">
                      <table>
                        <thead>
                          <tr>
                            <th>Sample time</th>
                            <th>Source observation</th>
                            <th>Connected seeds</th>
                            <th>Local completion</th>
                            <th>Coverage</th>
                          </tr>
                        </thead>
                        <tbody>
                          <For each={[...(detail()?.samples || [])].reverse()}>
                            {(p) => (
                              <tr>
                                <td>{when(p.at)}</td>
                                <td>{when(p.observedAt)}</td>
                                <td>{p.seeds ?? "Unknown"}</td>
                                <td>
                                  {p.complete == null
                                    ? "Unknown"
                                    : p.complete
                                      ? "Complete"
                                      : "Incomplete"}
                                </td>
                                <td>{p.status}</td>
                              </tr>
                            )}
                          </For>
                        </tbody>
                      </table>
                    </div>
                  </section>
                </Show>
              </article>
            )}
          </For>
        </div>
      </Show>
      <p>
        At least 12 active samples spanning 55 minutes, 75% coverage, and 80% low observations are
        required for “few seeds observed repeatedly.” A recent sample must also be low.
        Private-torrent discovery settings are unchanged.
      </p>
    </main>
  );
}
function PinEditor(props: { watch: Watch; busy: boolean; save: (body: object) => void }) {
  const [reason, setReason] = createSignal(props.watch.reason);
  const [date, setDate] = createSignal(props.watch.reviewDate);
  const [pinned, setPinned] = createSignal(props.watch.pinned);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        props.save({
          action: "update",
          hash: props.watch.hash,
          revision: props.watch.revision,
          pinned: pinned(),
          reason: reason(),
          reviewDate: date(),
        });
      }}
    >
      <fieldset disabled={props.busy} class="preservation-pin">
        <legend>Preservation pin</legend>
        <label>
          <input
            type="checkbox"
            checked={pinned()}
            onChange={(e) => setPinned(e.currentTarget.checked)}
          />{" "}
          Protect from cleanup
        </label>
        <label>
          Reason
          <input
            maxLength={500}
            value={reason()}
            onInput={(e) => setReason(e.currentTarget.value)}
          />
        </label>
        <label>
          Review date (UTC)
          <input type="date" value={date()} onInput={(e) => setDate(e.currentTarget.value)} />
        </label>
        <button type="submit">Save pin</button>
      </fieldset>
    </form>
  );
}
