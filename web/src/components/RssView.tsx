// RSS view (PAR-3.3): feeds with status/error badges, items with manual
// Add, and filters with match history. Reached from the sidebar Views group.

import { For, Show, createMemo, createSignal, onMount } from "solid-js";
import { formatBytes, formatDate } from "../lib/format";
import {
  addRssItem,
  failed as rssFailed,
  loading,
  markRssRead,
  refreshRss,
  view,
  type RssItem,
} from "../store/rss";
import { navigate } from "../store/ui";

function feedByName(name: string) {
  return view().feeds.find((feed) => feed.name === name);
}

function itemSize(item: RssItem): string {
  return item.length >= 0 ? formatBytes(item.length) : "size unknown";
}

function FeedCard(props: { name: string }) {
  const feed = createMemo(() => feedByName(props.name));
  return (
    <Show when={feed()}>
      {(f) => (
        <div class="rss-feed" classList={{ error: Boolean(f().lastError) }}>
          <div class="rss-feed-head">
            <b>{f().name}</b>
            <Show when={f().unread > 0}>
              <span class="sidebar-count tnum">{f().unread} new</span>
            </Show>
            <Show when={f().lastError}>
              <span class="rss-badge-error" title={f().lastError}>
                error{f().retryInSecs > 0 ? ` · retry in ${f().retryInSecs}s` : ""}
              </span>
            </Show>
            <span class="rss-feed-actions">
              <Show
                when={!f().lastError}
                fallback={
                  <button type="button" onClick={() => void refreshRss()}>
                    Retry
                  </button>
                }
              >
                <button type="button" onClick={() => void markRssRead(f().name)}>
                  Mark all read
                </button>
              </Show>
            </span>
          </div>
          <div class="rss-feed-meta">
            <span title={f().url}>{f().url}</span>
            <Show when={f().label}>
              <span>label: {f().label}</span>
            </Show>
            <Show when={f().destination}>
              <span>to: {f().destination}</span>
            </Show>
            <span>{f().items} items</span>
            <Show when={f().lastOk}>
              <span>fetched {formatDate(f().lastOk!)}</span>
            </Show>
          </div>
          <Show when={f().lastError}>
            <p class="rss-error">{f().lastError}</p>
          </Show>
        </div>
      )}
    </Show>
  );
}

function ItemRow(props: { item: RssItem }) {
  const item = () => props.item;
  const canAdd = () => !item().loaded && Boolean(item().enclosureUrl || item().link);
  return (
    <div class="rss-item" classList={{ read: item().read, loaded: item().loaded }}>
      <div class="rss-item-main">
        <b title={item().title}>{item().title}</b>
        <small>
          {item().feed} · {itemSize(item())}
          <Show when={item().published}> · {formatDate(item().published!)}</Show>
          <Show when={item().categories.length}> · {item().categories.join(", ")}</Show>
        </small>
      </div>
      <div class="rss-item-side">
        <Show when={item().loaded}>
          <span class="rss-badge-loaded" title={item().loadedHash || ""}>
            loaded{item().loadedBy ? ` · ${item().loadedBy}` : ""}
          </span>
        </Show>
        <button
          type="button"
          disabled={!canAdd()}
          title={
            canAdd()
              ? "Download and add to the session"
              : item().loaded
                ? "Already loaded"
                : "No downloadable enclosure"
          }
          onClick={() => void addRssItem(item().feed, item().id)}
        >
          Add
        </button>
      </div>
    </div>
  );
}

export function RssView() {
  const [feedFilter, setFeedFilter] = createSignal("");
  onMount(() => void refreshRss());

  const feeds = createMemo(() => view().feeds);
  const items = createMemo(() => {
    const all = view().items;
    const only = feedFilter();
    const list = only ? all.filter((item) => item.feed === only) : all;
    return [...list].sort((a, b) => (b.published || "").localeCompare(a.published || ""));
  });
  const filters = createMemo(() => view().filters);

  return (
    <div class="rss-view">
      <header class="rss-header">
        <h1>RSS feeds</h1>
        <span class="rss-header-actions">
          <Show when={loading()}>
            <span class="settings-intro">Loading…</span>
          </Show>
          <button type="button" onClick={() => void refreshRss()}>
            Refresh
          </button>
          <button type="button" onClick={() => void markRssRead()}>
            Mark all read
          </button>
        </span>
      </header>

      <Show when={rssFailed()}>
        <p class="settings-intro">
          Could not load RSS feeds.{" "}
          <button type="button" onClick={() => void refreshRss()}>
            Retry
          </button>
        </p>
      </Show>
      <Show when={feeds().length === 0 && !rssFailed()}>
        <p class="settings-intro">
          No feeds configured.{" "}
          <button type="button" onClick={() => navigate("settings", "Automation")}>
            Add feeds under Settings &gt; Automation
          </button>
        </p>
      </Show>
      <div class="rss-feeds">
        <For each={feeds()}>{(feed) => <FeedCard name={feed.name} />}</For>
      </div>

      <Show when={feeds().length > 0}>
        <h2>Items</h2>
        <div class="rss-toolbar">
          <label>
            Feed
            <select
              value={feedFilter()}
              onChange={(event) => setFeedFilter(event.currentTarget.value)}
            >
              <option value="">All feeds</option>
              <For each={feeds()}>{(feed) => <option value={feed.name}>{feed.name}</option>}</For>
            </select>
          </label>
        </div>
        <Show when={items().length === 0}>
          <Show
            when={!loading()}
            fallback={
              <div class="list-skeleton" aria-hidden="true">
                <span />
                <span />
                <span />
              </div>
            }
          >
            <p class="settings-intro">No items yet. Feeds populate on their poll interval.</p>
          </Show>
        </Show>
        <div class="rss-items">
          <For each={items()}>{(item) => <ItemRow item={item} />}</For>
        </div>

        <h2>Filters</h2>
        <Show when={filters().length === 0}>
          <p class="settings-intro">
            No filters configured.{" "}
            <button type="button" onClick={() => navigate("settings", "Automation")}>
              Filters auto-load matching items under Settings &gt; Automation
            </button>
          </p>
        </Show>
        <div class="rss-filters">
          <For each={filters()}>
            {(filter) => (
              <div class="rss-filter">
                <div class="rss-feed-head">
                  <b>{filter.name}</b>
                  <Show when={filter.feed}>
                    <span>feed: {filter.feed}</span>
                  </Show>
                  <span>
                    {filter.evaluated} evaluated · {filter.matched} matched · {filter.loaded} loaded
                  </span>
                </div>
                <Show when={filter.history.length === 0}>
                  <p class="settings-intro">No matches yet.</p>
                </Show>
                <Show when={filter.history.length > 0}>
                  <div class="rss-history">
                    <For each={filter.history}>
                      {(entry) => (
                        <div>
                          <span class={`rss-outcome rss-outcome-${entry.outcome}`}>
                            {entry.outcome}
                          </span>
                          <b title={entry.title}>{entry.title}</b>
                          <small title={entry.reason}>{entry.reason}</small>
                        </div>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
