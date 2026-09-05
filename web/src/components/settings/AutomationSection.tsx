// Automation section: completion rules, RSS, unpack (POL-8.8).
import { For, Show, createSignal, onMount } from "solid-js";
import type { Draft } from "./types";
import { SettingRow } from "./SettingRow";
import { DirPicker } from "../DirectoryBrowser";
import type { FeedDraft } from "./types";
import type { FilterDraft } from "./types";
import type { RuleDraft } from "./types";
import type { UnpackRuleDraft } from "./types";
import { formatBytes } from "../../lib/format";
import { showToast } from "../../store/ui";

/** Completion-rules and RSS editor (PAR-3.2, PAR-3.3): ordered on-complete
 * rules with a dry-run, plus RSS feeds and filters. */
export function AutomationSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  updateRule: (index: number, patch: Partial<RuleDraft>) => void;
  addRule: () => void;
  removeRule: (index: number) => void;
  updateUnpackRule: (index: number, patch: Partial<UnpackRuleDraft>) => void;
  addUnpackRule: () => void;
  removeUnpackRule: (index: number) => void;
  updateFeed: (index: number, patch: Partial<FeedDraft>) => void;
  addFeed: () => void;
  removeFeed: (index: number) => void;
  updateFilter: (index: number, patch: Partial<FilterDraft>) => void;
  addFilter: () => void;
  removeFilter: (index: number) => void;
}) {
  type DryRunMatch = {
    hash: string;
    name: string;
    label: string;
    trackerHost: string;
    sizeBytes: number;
    rule: string;
  };
  type DryRunResult = { matches: DryRunMatch[]; unmatched: number };
  const [result, setResult] = createSignal<DryRunResult | null>(null);
  const [testing, setTesting] = createSignal(false);

  const toApiRule = (rule: RuleDraft) => ({
    ...rule,
    private: rule.private === "true" ? true : rule.private === "false" ? false : null,
  });

  async function testRules() {
    setTesting(true);
    try {
      const response = await fetch("/api/v1/automation/dry-run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ rules: props.draft.automation.on_complete.map(toApiRule) }),
      });
      const body = (await response.json().catch(() => ({}))) as {
        matches?: DryRunMatch[];
        unmatched?: number;
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(body.error?.message || "Dry run failed");
      setResult({ matches: body.matches ?? [], unmatched: body.unmatched ?? 0 });
    } catch (error) {
      showToast(error instanceof Error ? error.message : "Dry run failed");
    } finally {
      setTesting(false);
    }
  }

  return (
    <section>
      <h1>Automation</h1>
      <p class="settings-intro">
        Completion rules run when a torrent finishes downloading. Rules are evaluated in order and
        the <b>first rule whose conditions all match</b> handles the torrent; each torrent is
        handled at most once, even across restarts. Actions run in order: set label, add tracker,
        move data, webhook.
      </p>
      <Show
        when={props.draft.automation.on_complete.length}
        fallback={<p class="settings-intro">No completion rules configured.</p>}
      >
        <div class="watch-entries">
          <For each={props.draft.automation.on_complete}>
            {(rule, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Rule name"
                    hint="automation.on_complete[].name"
                    error={props.errors[`rule-${index()}`]}
                  >
                    <input
                      value={rule.name}
                      placeholder="tv shows"
                      onInput={(event) =>
                        props.updateRule(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                </div>
                <h3 class="watch-entry-heading">Match conditions (empty = ignored)</h3>
                <div class="watch-entry-fields">
                  <SettingRow label="Label" hint="matches d.custom1 exactly">
                    <input
                      value={rule.label}
                      placeholder="Any label"
                      onInput={(event) =>
                        props.updateRule(index(), { label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Tracker host" hint="substring of the tracker host">
                    <input
                      value={rule.tracker}
                      placeholder="tracker.example"
                      onInput={(event) =>
                        props.updateRule(index(), { tracker: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Name regex" hint="Go regular expression on the torrent name">
                    <input
                      value={rule.name_regex}
                      placeholder="^Show\.S\d\d"
                      onInput={(event) =>
                        props.updateRule(index(), { name_regex: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Size range" hint="bytes; 0 = unbounded">
                    <div class="sort-control">
                      <input
                        type="number"
                        value={rule.min_size || ""}
                        placeholder="min"
                        onInput={(event) =>
                          props.updateRule(index(), {
                            min_size: Number(event.currentTarget.value) || 0,
                          })
                        }
                      />
                      <span>–</span>
                      <input
                        type="number"
                        value={rule.max_size || ""}
                        placeholder="max"
                        onInput={(event) =>
                          props.updateRule(index(), {
                            max_size: Number(event.currentTarget.value) || 0,
                          })
                        }
                      />
                    </div>
                  </SettingRow>
                  <SettingRow label="Private" hint="d.is_private">
                    <select
                      value={rule.private}
                      onChange={(event) =>
                        props.updateRule(index(), {
                          private: event.currentTarget.value as RuleDraft["private"],
                        })
                      }
                    >
                      <option value="any">Any</option>
                      <option value="true">Private only</option>
                      <option value="false">Public only</option>
                    </select>
                  </SettingRow>
                </div>
                <h3 class="watch-entry-heading">Actions (at least one)</h3>
                <div class="watch-entry-fields">
                  <SettingRow label="Set label" hint="d.custom1.set">
                    <input
                      value={rule.set_label}
                      placeholder="None"
                      onInput={(event) =>
                        props.updateRule(index(), { set_label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow
                    label="Move data to"
                    hint="move engine; must be inside the download roots"
                  >
                    <div class="sort-control">
                      <input
                        value={rule.move_to}
                        placeholder="/downloads/tv"
                        onInput={(event) =>
                          props.updateRule(index(), { move_to: event.currentTarget.value })
                        }
                      />
                      <DirPicker
                        value={rule.move_to}
                        onPick={(path) => props.updateRule(index(), { move_to: path })}
                      />
                    </div>
                  </SettingRow>
                  <SettingRow label="Add tracker" hint="announce URL, public torrents only">
                    <input
                      value={rule.add_tracker}
                      placeholder="udp://tracker.example:1337/announce"
                      onInput={(event) =>
                        props.updateRule(index(), { add_tracker: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Webhook URL" hint="POSTs a JSON completion payload">
                    <input
                      value={rule.webhook}
                      placeholder="https://hooks.example/blackbird"
                      onInput={(event) =>
                        props.updateRule(index(), { webhook: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                </div>
                <div class="watch-entry-flags">
                  <Show when={index() > 0}>
                    <small>Rules above this one win first.</small>
                  </Show>
                  <button type="button" onClick={() => props.removeRule(index())}>
                    Remove rule
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addRule()}>
        + Add completion rule
      </button>
      <h2>Test against existing torrents</h2>
      <p class="settings-intro">
        Runs the draft rules above against the current session without saving. Each torrent is
        listed under the rule that would handle it.
      </p>
      <button
        type="button"
        class="settings-add-row"
        disabled={testing() || !props.draft.automation.on_complete.length}
        onClick={() => void testRules()}
      >
        {testing() ? "Testing…" : "Run dry run"}
      </button>
      <Show when={result()}>
        <div class="automation-dryrun">
          <Show when={result()!.matches.length === 0}>
            <p class="settings-intro">No torrents match the draft rules.</p>
          </Show>
          <For each={result()!.matches}>
            {(match) => (
              <div>
                <span>{match.rule}</span>
                <b>{match.name}</b>
                <small>
                  {match.label || "no label"} · {formatBytes(match.sizeBytes)}
                </small>
              </div>
            )}
          </For>
          <Show when={result()!.unmatched > 0}>
            <p class="settings-intro">
              {result()!.unmatched} torrent{result()!.unmatched === 1 ? "" : "s"} match no rule.
            </p>
          </Show>
        </div>
      </Show>
      <h2>RSS feeds</h2>
      <p class="settings-intro">
        Feeds poll on their own schedule and never block the torrent list. Cookies and extra headers
        are secrets: stored values display as <code>***</code>, which keeps the stored secret; empty
        clears it. Feed URLs may carry a passkey query string and are never shown outside this page.
      </p>
      <Show
        when={props.draft.automation.rss.feeds.length}
        fallback={<p class="settings-intro">No feeds configured.</p>}
      >
        <div class="watch-entries">
          <For each={props.draft.automation.rss.feeds}>
            {(feed, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Feed name"
                    hint="automation.rss.feeds[].name · referenced by filters"
                    error={props.errors[`feed-${index()}`]}
                  >
                    <input
                      value={feed.name}
                      placeholder="tv"
                      onInput={(event) =>
                        props.updateFeed(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="URL" hint="automation.rss.feeds[].url · http(s)">
                    <input
                      value={feed.url}
                      placeholder="https://tracker.example/rss?passkey=…"
                      onInput={(event) =>
                        props.updateFeed(index(), { url: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Poll interval" hint="empty uses the 15m default">
                    <input
                      value={feed.poll_interval}
                      placeholder="15m"
                      onInput={(event) =>
                        props.updateFeed(index(), { poll_interval: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Default label" hint="d.custom1.set on auto-loads">
                    <input
                      value={feed.label}
                      placeholder="None"
                      onInput={(event) =>
                        props.updateFeed(index(), { label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Default destination" hint="d.directory.set on auto-loads">
                    <div class="sort-control">
                      <input
                        value={feed.destination}
                        placeholder="Use default directory"
                        onInput={(event) =>
                          props.updateFeed(index(), { destination: event.currentTarget.value })
                        }
                      />
                      <DirPicker
                        value={feed.destination}
                        onPick={(path) => props.updateFeed(index(), { destination: path })}
                      />
                    </div>
                  </SettingRow>
                  <SettingRow label="Cookies" hint="Cookie header; *** keeps, empty clears">
                    <input
                      value={feed.cookies}
                      placeholder="uid=7; pass=…"
                      onInput={(event) =>
                        props.updateFeed(index(), { cookies: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Extra headers" hint="one Name: value per line; *** keeps">
                    <textarea
                      class="headers-box"
                      value={feed.headers}
                      placeholder={"Authorization: Bearer …"}
                      onInput={(event) =>
                        props.updateFeed(index(), { headers: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                </div>
                <div class="watch-entry-flags">
                  <button type="button" onClick={() => props.removeFeed(index())}>
                    Remove feed
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addFeed()}>
        + Add feed
      </button>
      <h2>RSS filters</h2>
      <p class="settings-intro">
        Filters run in order per new item; the first filter whose conditions all match loads the
        item with its label, destination, and start options (falling back to the feed defaults).
        Match outcomes appear per filter in the RSS view.
      </p>
      <Show
        when={props.draft.automation.rss.filters.length}
        fallback={
          <p class="settings-intro">
            No filters configured. Without a filter, items are only stored, never loaded.
          </p>
        }
      >
        <div class="watch-entries">
          <For each={props.draft.automation.rss.filters}>
            {(filter, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Filter name"
                    hint="automation.rss.filters[].name"
                    error={props.errors[`rssfilter-${index()}`]}
                  >
                    <input
                      value={filter.name}
                      placeholder="weekly shows"
                      onInput={(event) =>
                        props.updateFilter(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Feed" hint="empty matches all feeds">
                    <select
                      value={filter.feed}
                      onChange={(event) =>
                        props.updateFilter(index(), { feed: event.currentTarget.value })
                      }
                    >
                      <option value="">All feeds</option>
                      <For each={props.draft.automation.rss.feeds}>
                        {(feed) => (
                          <option value={feed.name}>{feed.name || "(unnamed feed)"}</option>
                        )}
                      </For>
                    </select>
                  </SettingRow>
                  <SettingRow label="Title regex" hint="Go regular expression on the item title">
                    <input
                      value={filter.title_regex}
                      placeholder="^Show\.S\d\d"
                      onInput={(event) =>
                        props.updateFilter(index(), { title_regex: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Category" hint="substring of an item category">
                    <input
                      value={filter.category}
                      placeholder="TV"
                      onInput={(event) =>
                        props.updateFilter(index(), { category: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Size range" hint="enclosure bytes; 0 = unbounded">
                    <div class="sort-control">
                      <input
                        type="number"
                        value={filter.min_size || ""}
                        placeholder="min"
                        onInput={(event) =>
                          props.updateFilter(index(), {
                            min_size: Number(event.currentTarget.value) || 0,
                          })
                        }
                      />
                      <span>–</span>
                      <input
                        type="number"
                        value={filter.max_size || ""}
                        placeholder="max"
                        onInput={(event) =>
                          props.updateFilter(index(), {
                            max_size: Number(event.currentTarget.value) || 0,
                          })
                        }
                      />
                    </div>
                  </SettingRow>
                  <SettingRow label="Label" hint="overrides the feed default">
                    <input
                      value={filter.label}
                      placeholder="Feed default"
                      onInput={(event) =>
                        props.updateFilter(index(), { label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow label="Destination" hint="overrides the feed default">
                    <div class="sort-control">
                      <input
                        value={filter.destination}
                        placeholder="Feed default"
                        onInput={(event) =>
                          props.updateFilter(index(), { destination: event.currentTarget.value })
                        }
                      />
                      <DirPicker
                        value={filter.destination}
                        onPick={(path) => props.updateFilter(index(), { destination: path })}
                      />
                    </div>
                  </SettingRow>
                </div>
                <div class="watch-entry-flags">
                  <label>
                    <input
                      class="settings-check"
                      type="checkbox"
                      checked={filter.start}
                      onChange={(event) =>
                        props.updateFilter(index(), { start: event.currentTarget.checked })
                      }
                    />{" "}
                    Start loaded torrents
                  </label>
                  <button type="button" onClick={() => props.removeFilter(index())}>
                    Remove filter
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addFilter()}>
        + Add filter
      </button>
      <UnpackSection
        draft={props.draft}
        errors={props.errors}
        updateUnpackRule={props.updateUnpackRule}
        addUnpackRule={props.addUnpackRule}
        removeUnpackRule={props.removeUnpackRule}
      />
    </section>
  );
}

/** Unpack-on-completion editor (PAR-3.4) with the extractor status banner. */
export function UnpackSection(props: {
  draft: Draft;
  errors: Record<string, string>;
  updateUnpackRule: (index: number, patch: Partial<UnpackRuleDraft>) => void;
  addUnpackRule: () => void;
  removeUnpackRule: (index: number) => void;
}) {
  type UnpackStatus = {
    available: boolean;
    binary?: string;
    workers: number;
    queue: number;
    jobs: Array<{
      hash: string;
      name: string;
      rule: string;
      archive: string;
      destDir: string;
      percent: number;
      startedAt: string;
    }>;
  };
  const [status, setStatus] = createSignal<UnpackStatus | null>(null);
  onMount(() => {
    fetch("/api/v1/unpack", { headers: { Accept: "application/json" } })
      .then((response) => (response.ok ? response.json() : null))
      .then((body) => {
        if (body) setStatus(body as UnpackStatus);
      })
      .catch(() => {
        /* status stays unknown; the editor still works */
      });
  });
  return (
    <div>
      <h2>Unpack on completion</h2>
      <p class="settings-intro">
        Archives (.zip, .rar including multi-part) inside a finished torrent extract automatically.
        Rules match the torrent's label after completion actions; the first match wins. Empty
        destination extracts next to each archive, otherwise files land in{" "}
        <code>&lt;root&gt;/&lt;torrent name&gt;</code> (the root must exist inside the download
        roots). Worker count and timeout stay YAML-managed.
      </p>
      <Show when={status() && !status()!.available}>
        <p class="settings-warning">
          Extractor not found: install p7zip (container/host Linux) or sevenzip (macOS) so{" "}
          <code>7z</code> is on PATH. Unpacking is disabled until it is present; rules below are
          kept but do nothing.
        </p>
      </Show>
      <Show when={status()?.available}>
        <p class="settings-intro">
          Extractor: <code>{status()!.binary || "7z"}</code> · {status()!.workers} workers
          {status()!.queue > 0 ? ` · ${status()!.queue} queued` : ""}
          {status()!.jobs.length > 0 ? ` · ${status()!.jobs.length} running` : ""}.
        </p>
      </Show>
      <Show
        when={props.draft.automation.unpack.rules.length}
        fallback={<p class="settings-intro">No unpack rules configured.</p>}
      >
        <div class="watch-entries">
          <For each={props.draft.automation.unpack.rules}>
            {(rule, index) => (
              <div class="watch-entry">
                <div class="watch-entry-fields">
                  <SettingRow
                    label="Rule name"
                    hint="automation.unpack.rules[].name"
                    error={props.errors[`unpack-${index()}`]}
                  >
                    <input
                      value={rule.name}
                      placeholder="tv"
                      onInput={(event) =>
                        props.updateUnpackRule(index(), { name: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow
                    label="Label"
                    hint="torrent label after completion actions; empty matches all"
                  >
                    <input
                      value={rule.label}
                      placeholder="Any label"
                      onInput={(event) =>
                        props.updateUnpackRule(index(), { label: event.currentTarget.value })
                      }
                    />
                  </SettingRow>
                  <SettingRow
                    label="Destination"
                    hint="empty = in place; else an existing extract root"
                  >
                    <div class="sort-control">
                      <input
                        value={rule.destination}
                        placeholder="/downloads/extracted"
                        onInput={(event) =>
                          props.updateUnpackRule(index(), {
                            destination: event.currentTarget.value,
                          })
                        }
                      />
                      <DirPicker
                        value={rule.destination}
                        onPick={(path) => props.updateUnpackRule(index(), { destination: path })}
                      />
                    </div>
                  </SettingRow>
                </div>
                <div class="watch-entry-flags">
                  <label>
                    <input
                      class="settings-check"
                      type="checkbox"
                      checked={rule.delete_archives}
                      onChange={(event) =>
                        props.updateUnpackRule(index(), {
                          delete_archives: event.currentTarget.checked,
                        })
                      }
                    />{" "}
                    Delete archives after success
                  </label>
                  <button type="button" onClick={() => props.removeUnpackRule(index())}>
                    Remove rule
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>
      <button class="settings-add-row" type="button" onClick={() => props.addUnpackRule()}>
        + Add unpack rule
      </button>
    </div>
  );
}
