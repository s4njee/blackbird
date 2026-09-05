// RSS store: the /api/rss view (feeds, items, filters) plus refresh and
// item actions (PAR-3.3). The sidebar unread badge and the RSS view share it.

import { createSignal } from "solid-js";
import { showToast } from "./ui";

export interface RssFeed {
  name: string;
  url: string;
  label: string;
  destination: string;
  pollIntervalNs: number;
  lastFetch?: string;
  lastOk?: string;
  lastError?: string;
  retryInSecs: number;
  items: number;
  unread: number;
}

export interface RssItem {
  feed: string;
  id: string;
  title: string;
  link: string;
  enclosureUrl: string;
  enclosureType?: string;
  length: number;
  categories: string[];
  published?: string;
  read: boolean;
  loaded: boolean;
  loadedHash?: string;
  loadedBy?: string;
}

export interface RssEval {
  at: string;
  feed: string;
  itemId: string;
  title: string;
  outcome: string;
  reason: string;
}

export interface RssFilter {
  name: string;
  feed?: string;
  evaluated: number;
  matched: number;
  loaded: number;
  history: RssEval[];
}

export interface RssView {
  feeds: RssFeed[];
  items: RssItem[];
  filters: RssFilter[];
}

const [view, setView] = createSignal<RssView>({ feeds: [], items: [], filters: [] });
const [loading, setLoading] = createSignal(false);
const [failed, setFailed] = createSignal(false);

export { view, loading, failed };

/** Total unread, unloaded items across feeds, for the sidebar badge. */
export function unreadCount(): number {
  return view().feeds.reduce((sum, feed) => sum + (feed.unread || 0), 0);
}

export async function refreshRss(quiet = false): Promise<void> {
  setLoading(true);
  setFailed(false);
  try {
    const response = await fetch("/api/v1/rss", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("Could not load RSS feeds");
    setView((await response.json()) as RssView);
  } catch (error) {
    setFailed(true);
    if (!quiet) showToast(error instanceof Error ? error.message : "Could not load RSS feeds");
  } finally {
    setLoading(false);
  }
}

export async function addRssItem(feed: string, id: string): Promise<void> {
  try {
    const response = await fetch("/api/v1/rss/add", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feed, id }),
    });
    const body = (await response.json().catch(() => ({}))) as {
      hash?: string;
      error?: { message?: string };
    };
    if (!response.ok) throw new Error(body.error?.message || "Could not add torrent");
    showToast("Torrent added to session.");
    await refreshRss();
  } catch (error) {
    showToast(error instanceof Error ? error.message : "Could not add torrent");
  }
}

export async function markRssRead(feed?: string): Promise<void> {
  try {
    const response = await fetch("/api/v1/rss/read", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(feed ? { feed, all: true } : { all: true }),
    });
    if (!response.ok) throw new Error("Could not mark items read");
    await refreshRss();
  } catch (error) {
    showToast(error instanceof Error ? error.message : "Could not mark items read");
  }
}
