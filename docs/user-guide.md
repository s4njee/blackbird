# Blackbird user guide (intake, automation, versions)

This guide covers first-run intake: getting torrents into Blackbird and
confirming the console is healthy. It is the target of the empty-session
links on the console route (POL-8.7).

## Add a torrent

- Console route → **+ Add torrent**: paste a magnet/URL or drop `.torrent`
  files. Pick destination, label, start, skip-hash, and sequential options.
- Drag `.torrent` files onto the table for the same flow.

## Watch directories

- Settings → **Directories** → watch entries (`path`, `label`,
  `destination`, `start`, `delete_after_load`, `poll_interval`).
- Drop a `.torrent` file into a watched path: it loads with the entry's
  label/destination, then renames to `.loaded` (or deletes per config).
- Malformed files move to `failed/` with the reason logged; duplicates are
  skipped without error spam. Watch events toast in open consoles.

## RSS feeds

- Settings → **Automation** → feeds (URL, poll interval, cookies/headers as
  secrets, default label/destination) and filters (title regex, category,
  size range, label/destination overrides).
- The **RSS** view lists feed status, items (manual Add per item,
  mark-all-read), and per-filter match history. Failing feeds back off and
  show an error badge with a retry countdown.

## Completion rules and unpack

- Settings → **Automation** → `on_complete` rules (match on label, tracker,
  name regex, size, private flag; actions: set label, move, add tracker,
  webhook) run once per torrent on the `d.complete` 0→1 transition.
- Unpack rules extract zip/rar on completion (in-place or extract root,
  optional archive deletion); failures leave a `.failed` marker.

## Versions and updates
- Settings → **About** shows the Blackbird build (version/commit/date), the
  live rTorrent/libtorrent versions, and session state.
- The release check is **off by default** and runs only in your browser when
  enabled: turn on **Enable release check**, paste a releases endpoint
  (e.g. `https://api.github.com/repos/OWNER/REPO/releases/latest`), and
  press **Check now**.

## Themes
- Settings → **Interface** → Theme: pick Blackbird Dark, Light, Midnight,
  High Contrast, Classic, or System (follows the OS, live). The choice is
  per browser; **Operator default theme** (`ui.theme`) covers new browsers.
- **Accent color**: empty means the active theme's default accent; any
  `#rrggbb` overrides it. The per-theme preset swatches adopt a curated
  accent in one click. Configs saved by older releases may carry an explicit
  `"#35418f"` (the old default) — clear the field to adopt per-theme accents.
- Custom label colors (Settings → **Labels**) render with automatically
  contrast-safe text on every theme.

## Explain a torrent

Select a torrent and open the **Why?** detail tab to inspect observed state,
recorded actions, skipped files, bandwidth controls, and seeding conditions.
Each finding includes evidence and a time; suggested next steps open the
existing controls. Unknown causes and stale observations stay explicit.
See [Torrent explanations](torrent-explanations.md) for coverage and limits.

## Replay an incident

Open **History → Flight recorder** or **Logger → Flight recorder** for a
selected torrent. Load a time window, scrub recorded events, and follow linked
intents to distinguish requests from observations. The recorder persists
across restarts and marks missing coverage. Use **Preview incident export**
before downloading a redacted local bundle. See [Flight recorder](flight-recorder.md).

## Attention inbox

Open **Attention** in the sidebar, or **Attention inbox** in the notification
centre, to review grouped tracker symptoms, torrent errors, unavailable
configured volumes, and connection gaps. Acknowledge an incident or snooze it
for an hour/day; these decisions survive restart. Recovery followed by a new
failure starts a new episode. **Why?** and **Recording** open current explanations
and historical evidence for an affected torrent.

The return summary includes unresolved incidents and important completed
outcomes since the previous inbox visit. Notices arrive once per new episode or
expired snooze rather than on every repeated sample. See
[Attention inbox](attention-inbox.md) for recurrence rules, storage bounds,
summary coverage, and API details.

## Forecast storage before adding or moving

The Add and Move dialogs include **Storage forecast**. Choose the destination,
refresh the forecast, and inspect each filesystem's additional growth, projected
peak, and reserve. Magnet sizes and archive expansion can stay unknown or use
explicit GiB assumptions. Existing allocation, selected-file boundary pieces,
and copy-before-delete moves are treated separately.

Submission refreshes the evidence again. New or changed plans pause for review;
submit again to continue with the displayed advisory. No disk space is reserved.
See [Storage forecast](storage-forecast.md) for accounting assumptions and limits.


## Preservation watchlist

Open **Preservation** from the sidebar to watch selected torrents. The list samples cached observations every five minutes and ranks sustained low connected-seed counts with explicit coverage. Tracker evidence is separate; its report age is unknown. These observations do not establish swarm-wide rarity.

Save a **Preservation pin** with an optional reason and UTC review date to block Blackbird removal, automatic seeding cleanup and source-archive deletion. Pins survive restarts and overdue review dates. Unpin explicitly before removal or stopping a watch. Seeding stop and bandwidth policies continue to apply. Use the pinned/review-due filters to revisit your choices.

See [Preservation watchlist](preservation-watchlist.md) for scoring, data retention and protection limits.
