# Blackbird — Product Backlog v2: ruTorrent parity, performance, polish, theming

`backlog.md` covers shipping and packaging (security, Compose appliance, CI, release engineering). This backlog covers the **product** work that turns the Epic 1–9 console from `plan.md` into something a long-time ruTorrent user can switch to without losing capability, and something a new user finds fast and finished.

It is written against the code as it exists today, not against `plan.md`'s intent. Where the two disagree, the baseline below records the reality, and the stories are scoped to close the gap. Story IDs are prefixed by theme (`PAR` parity, `PERF` performance, `POL` polish, `THM` theming) so they can be scheduled independently of `backlog.md`'s `SHIP`/`SEC`/`PKG`/`QA`/`OPS`/`DOC`/`REL` stories.

## Priority and completion policy

- **P0 — required for v1.0:** without it, a ruTorrent user cannot reasonably migrate, or the console is visibly unfinished.
- **P1 — should ship in v1.x:** high-value parity or quality work with a documented workaround if deferred.
- **P2 — later:** ruTorrent plugin-tier features with a small audience, or refinements beyond parity.
- A story is complete only when implementation, automated tests (Go unit, frontend component, or browser e2e as appropriate), and user-facing documentation are done. Frontend stories may not claim completion until the harness in POL-3.1 exists.
- Anything that touches the REST/WebSocket contract must also update the versioning rules from `backlog.md` SHIP-1.4.

---

## Baseline — what exists today

Facts established by reading the code on 2026-09-02. Every story below cites this section rather than re-deriving it.

**Implemented and working:** SCGI/XML-RPC transport with an 8-call concurrency cap; `d.multicall2` list poll (25 fields) every 2s; whole-object deltas over a versioned WebSocket (`v:1`); all five detail tabs; add via magnet/URL/file with destination, label, start, skip-hash, sequential options; start/pause/stop/recheck/remove/remove-with-data/label/move/priority/file-priority/tracker-add/tracker-enable/reannounce actions with per-hash batch results; 21 tuning keys applied on connect and editable from Settings; Stats page with a 60-minute throughput graph, volumes, and space-by-label; HTTP Basic auth with failed-login rate limiting; Compose appliance with a real-rTorrent smoke test; 89 Go tests.

**Gaps that shape this backlog:**

| Area | Finding |
|---|---|
| Table | No virtualization: every filtered row is a real `<tr>`. PAR-1.1/1.2 now provide the 27-column catalogue and browser-persisted hide/reorder/resize; virtualization remains deferred. |
| Data flow | `store/session.ts` holds torrents in a plain signal and shallow-copies the whole map twice per tick; `<For>` recreates the full row for every changed torrent. `pruneSelection` returns a new array every tick, invalidating every row's selected state. `torrentList` sorts all keys on every delta, then the table sorts again. |
| Poller | `refreshDetailsLocked` performs network I/O while holding the poller write lock. `Snapshot()` deep-copies on every call. `FetchDetail` is four sequential round trips despite its comment. The per-client WebSocket detail loop re-sends unchanged detail every 500ms. SCGI response reading is unbounded. No benchmarks anywhere. |
| Settings | `ui.date_format`, `ui.rate_format`, `ui.sort`, and `ui.poll_interval` still need wiring. PAR-1.2 migrates the inert `ui.visible_columns` key into `ui.columns`; browser-local layout takes precedence over the operator default. |
| Theming | Dark palette only; 76 tokens in a single `:root` block. No `prefers-color-scheme`, no theme attribute. Changing `--accent` leaves `--accent-tint`, `--accent-tint-strong`, `--accent-text`, and `--accent-foreground` fixed to the default navy. Label colors are a hard-coded five-name list duplicated in `Sidebar.tsx`, `StatsView.tsx`, and `app.css`; user-created labels render neutral. |
| Automation | No watch directory (the `directories.watch` keys are documented as "future"), no RSS, no scheduler, no per-torrent throttle channels (`d.throttle_name` unused), no ratio groups (`group.seeding.*` unused), no unpack. |
| Detail panel | Pieces tab synthesizes a contiguous fill from `chunksDone`; it never reads the bitfield. Tracker "Remove" sends `tracker_enable {enabled:false}`. `[DHT]`/`[PEX]` rows are static. Peers list is read-only. Panel height is fixed at 288px. |
| UX | Eleven `window.confirm`/`window.prompt` call sites for destructive actions, label entry, move destination, and tracker URL. Single-slot toast with no severity or queue. No focus trap on the modal or context menu, no `aria-sort`, no `role="dialog"`, no keyboard-reachable sort headers. No URL routing. |
| Tests | Zero frontend tests, no test runner, no Playwright. Go tests run only against `fakertorrent`, whose fallback returns an empty string for any unknown method. CI runs `go test` but not `go vet`, `-race`, or a linter. |
| Code health | Several TSX lines exceed 3,000 characters (`SettingsPanel.tsx:142`, `:147`). Dead code: `Placeholders.tsx`, `poller.samplesSince`, `rtorrent.GlobalSettingsKeys`, `ConfigStore.SaveTuning`, unused `Get/SetGlobal*` helpers. |

---

## ruTorrent parity matrix

ruTorrent's core client plus the plugins that ship enabled by default or are near-universally installed. "Blackbird today" is the baseline above; the last column is the story that closes the gap. Features deliberately out of scope for v1.x are listed at the end of the document.

| ruTorrent capability | Blackbird today | Story |
|---|---|---|
| ~30 selectable table columns (done bytes, remaining, uploaded, down/up totals, peers/seeds detail, priority, created, finished, path, tracker status, throttle, …) | 27-column catalogue | PAR-1.1 |
| Show/hide, reorder, resize columns; persisted | Header picker with browser-local layout and YAML operator default | PAR-1.2 |
| Category views: All, Downloading, Completed, Active, Inactive, Error + labels + trackers | Status/label/tracker filters (no Active/Inactive/Completed distinct from Seeding) | PAR-1.3 |
| Search across name/hash/path/tracker; per-column filter | Name substring only | PAR-1.4 |
| Multiple label fields (custom1–custom5) and "ratio group" column | Label only | PAR-1.1, PAR-4.2 |
| Force start, force recheck, force reannounce, pause/unpause | No force start | PAR-2.1 |
| Torrent priority Off/Low/Normal/High | Only ↑ (3) / ↓ (0) | PAR-2.1 |
| Superseed toggle, sequential toggle post-add | Sequential only at add time | PAR-2.1 |
| Set download directory with directory browser (`_getdir`) | `window.prompt` free text | PAR-2.2, PAR-5.1 |
| Edit trackers: add, remove, enable/disable, reorder groups | Add, enable/disable; "Remove" disables | PAR-2.3 |
| Peers tab: country/geoip, ban/kick/snub/unsnub | Read-only list | PAR-2.4 |
| General tab (full info), Speed tab (per-torrent graph), Logger tab | Facts column only | PAR-2.5 |
| Chunks/Pieces tab with real bitfield | Synthesized | PAR-2.6 |
| Save `.torrent` file, open magnet, rename torrent | Copy magnet only | PAR-2.7 |
| Watch directory auto-load with per-directory label/destination (autotools autowatch) | Config keys exist, nothing runs | PAR-3.1 |
| Auto-label and auto-move on completion (autotools) | None | PAR-3.2 |
| RSS feeds with filters/autodl | None | PAR-3.3 |
| Unpack (rar/zip) on completion | None | PAR-3.4 |
| Retrackers (auto-add tracker list to new torrents) | None | PAR-3.5 |
| Named throttle channels assignable per torrent | Global limits only | PAR-4.1 |
| Ratio groups with stop/erase-on-ratio rules | None | PAR-4.2 |
| Scheduler: hourly/daily bandwidth grid | None | PAR-4.3 |
| Quick global speed-limit popup on the status bar | Settings > Bandwidth only | PAR-4.4 |
| Seeding time column and limits | None | PAR-4.2 |
| Traffic history (per day/month totals) | 60-minute rate history only | PAR-5.2 |
| Disk space per volume, CPU load | Volumes only | PAR-5.2 |
| Torrent history (added/finished/removed log) | None | PAR-5.3 |
| Create torrent | None | PAR-5.4 |
| Check port | Status-bar "Port N open" from `network.port` only | PAR-5.5 |
| IP filter | None | PAR-5.6 |
| Mediainfo, screenshots, spectrogram, stream, fileshare | None | P2 / out of scope |
| Themes (theme plugin, ~10 community themes) | Accent color only | Epic 9 |
| Mobile plugin | Unsupported below 900px by policy | Out of scope |
| Multi-user | Single user by design | Out of scope |

---

## Epic 1 — Torrent list parity

The table is where ruTorrent users spend 90% of their time. Match its information density and configurability without abandoning the handoff's fixed row height and formatting rules.

### PAR-1.1 — Extend the column catalogue (P0)

**As a** ruTorrent user, **I want** the same breadth of columns **so that** I can see remaining bytes, totals, and timestamps without opening details.

Acceptance criteria:

- The list poll fetches and the API exposes at least: `d.left_bytes`, `d.up.total`, `d.down.total`, `d.timestamp.finished`, `d.throttle_name`, `d.custom2`–`d.custom5`, `d.tied_to_file`, `d.skip.total`, `d.peers_accounted`, `d.chunks_hashed`, `d.is_multi_file`, `d.directory`, `d.connection_current`, `d.creation_date`, and per-torrent seeding time derived from `d.timestamp.finished`.
- The column catalogue offers at minimum: Name, Size, Done, Remaining, Status, Seeds/Peers, Seeds (detail), Peers (detail), Down, Up, Downloaded, Uploaded, ETA, Ratio, Label, Priority, Throttle, Ratio group, Added, Finished, Created, Seeding time, Tracker, Tracker status, Path, Hash, Message.
- Every new column follows the handoff's formatting rules (tabular numerals, dimmed `—`, accent/cyan rates, `12:04 today` dates); new formatters have unit tests in `lib/format.ts`.
- Adding fields to `d.multicall2` does not measurably regress poll time on the 5,000-torrent fixture (PERF-6.6); unused fields are pruned server-side when no client has the column visible (stretch, may defer).
- `torrentChanged` in the poller compares every shipped field, including `AddedAt` and `IsPrivate`, so a column never shows stale data.

Results:

- Added the extended rTorrent fields to the list poll and normalized API model, including remaining bytes, transfer totals, timestamps, throttle/custom fields, path, connection, peer/chunk accounting, and privacy state.
- Added the complete 27-column catalogue to the torrent table with sorting and rendering for bytes, rates, ratios, dates, priorities, tracker status, and client-derived seeding time.
- Updated poller delta detection to compare the complete torrent value, covering every shipped field and preventing stale column values.
- Verified with `go test ./...`, frontend TypeScript typechecking, and a production frontend build; the rebuilt Docker service is healthy.

Remaining:

- Add dedicated frontend unit-test coverage for `formatDate` and `formatSeedingTime` once a web test runner is introduced.
- Run the 5,000-torrent polling benchmark from PERF-6.6; server-side pruning of fields when columns are hidden remains deferred to PAR-1.2’s configurable column layout.
- Column show/hide, reorder, resize, and persisted layouts remain scoped to PAR-1.2.

### PAR-1.2 — Column show/hide, reorder, and resize (P0)

**As a** user, **I want** to configure which columns appear, in what order, and how wide **so that** the table fits my workflow and monitor.

Acceptance criteria:

- Right-clicking the header opens a column picker; columns can be toggled on and off, dragged to reorder, and resized by dragging the header edge, with a double-click to auto-fit.
- Name remains the only fluid column; minimum widths prevent unreadable cells; the handoff widths are the defaults.
- Layout persists per browser (localStorage) and can be saved to YAML `ui.columns` as the operator default from Settings > Interface; the existing inert `ui.visible_columns` key is migrated or removed with a documented deprecation.
- The responsive column-drop rules from `docs/responsive-ui.md` still apply on top of the user's layout.
- "Reset to defaults" restores the handoff layout.
- Component tests cover toggle, reorder, persist, and reset; a browser test covers drag-resize.

Results:

- Added a shared 27-column catalogue with handoff widths and minimum widths; Name remains the sole fluid column.
- Added the header column picker with visibility toggles, drag-to-reorder support, header-edge pointer resizing, double-click auto-fit, Escape/outside-click dismissal, and a reset-to-defaults action.
- Persisted browser layout state in `localStorage` and added Settings > Interface controls to save the current browser layout or edit the operator default in YAML as `ui.columns`.
- Added backwards-compatible migration from the inert `ui.visible_columns` key and kept the documented narrow-width Tracker/Added/Ratio drop rules active.
- Verified with Go tests, frontend typechecking/build, and a healthy rebuilt Docker service.

Remaining:

- Add component and browser automation coverage once the frontend test runner/Playwright harness is introduced.
- Server-side pruning of hidden list fields remains deferred until the configurable layout is used to drive poll selection.
- Sidebar width, detail-panel height/tab, and broader layout reset remain part of POL-8.6 rather than this story.

### PAR-1.3 — ruTorrent category views (P0)

**As a** ruTorrent user, **I want** the familiar status views **so that** "Active", "Inactive", and "Completed" mean what I expect.

Acceptance criteria:

- Sidebar status group offers: All, Downloading, Seeding, Completed, Active, Inactive, Stopped, Queued, Checking, Error; definitions match ruTorrent (Active = any non-zero rate; Completed = `d.complete` regardless of state; Inactive = zero rates and open).
- Counts are computed server-side in `computeAggregates` and pushed with the delta so the sidebar never disagrees with the table.
- Filter definitions are documented in the user guide.
- Poller tests cover each category's membership rules against fixture torrents.

### PAR-1.4 — Search and advanced filtering (P1)

**As a** user with thousands of torrents, **I want** to search by more than name **so that** I can find a torrent by hash, path, tracker, or message.

Acceptance criteria:

- The filter field matches name, hash prefix, base path, tracker host, and error message; a field prefix syntax (`label:`, `tracker:`, `path:`, `status:`, `ratio>`, `size<`) narrows the match, documented in a `?` popover.
- Matching runs against a precomputed lowercase index updated incrementally per delta, not by re-lowercasing every row per keystroke.
- Saved filters (name + query + sidebar state) can be pinned to the sidebar and persist per browser; YAML `ui.saved_filters` provides operator defaults.
- Selection and focus survive filter changes when the focused torrent is still visible.
- Tests cover the query grammar and index invalidation.

### PAR-1.5 — Sort parity (P1)

**As a** user, **I want** sorting on every column with stable secondary ordering **so that** the table is predictable while it ticks.

Acceptance criteria:

- Every column in the catalogue is sortable, with numeric, string, date, and enum comparators chosen per column.
- Shift-click adds a secondary sort key; the header shows both carets with an ordinal.
- Sort preference has exactly one source of truth: per-browser localStorage seeded from YAML `ui.sort`; the current split between the two stores is removed.
- Sorting is incremental (PERF-7.2); a sort-key change on a 5,000-row fixture completes within one frame.

---

## Epic 2 — Torrent actions and detail parity

### PAR-2.1 — Complete the transport and priority action set (P0)

**As a** user, **I want** every per-torrent action ruTorrent offers **so that** no workflow forces me back to the old UI.

Acceptance criteria:

- New actions over the existing batch endpoint: `force_start` (`d.open` + `d.start` regardless of queue), `set_priority` with values 0–3 exposed as Off/Low/Normal/High in the toolbar and context menu, `superseed` toggle (`d.connection_seed.set`), `sequential` toggle (`d.sequential.set`) on live torrents, `save_session` (`d.save_full_session`), and `set_custom` for `custom2`–`custom5`.
- Context menu gains "Priority ▸" and "Advanced ▸" submenus matching ruTorrent grouping; keyboard hints are added only where a binding exists.
- Each action is recorded by `fakertorrent` and verified against real rTorrent in the QA-5.2 integration target from `backlog.md`.
- Optimistic UI covers the new state fields with rollback on failure.

### PAR-2.2 — Move data and set directory (P0)

**As a** user, **I want** a safe, guided move flow **so that** I never type a path by hand.

Acceptance criteria:

- The move dialog replaces `window.prompt` with a modal offering configured download roots, recent destinations, per-label destinations, and a directory browser (PAR-5.1) constrained to allowed roots.
- Two modes: "Set directory only" (`d.directory.set` for already-relocated data) and "Move files" (Go-side move); cross-device moves copy then verify then delete, with a progress indicator and cancellation.
- Running torrents are stopped, moved, and restarted automatically; the dialog explains this and shows per-torrent results.
- Path safety rules from `backlog.md` SEC-2.3 apply; symlink escapes and cross-root moves are refused with a clear message.
- Tests cover same-device rename, cross-device copy, partial failure, and cancellation.

### PAR-2.3 — Full tracker editing (P0)

**As a** user, **I want** to add, remove, disable, and regroup trackers **so that** dead trackers can actually be cleaned up.

Acceptance criteria:

- "Remove" removes the tracker (rebuild the tracker list via `d.tracker.insert`/`d.tracker.remove` or the announce-list rewrite approach that the supported rTorrent versions provide; the chosen mechanism is documented); "Disable" remains separate.
- Trackers can be added to a chosen group; groups are shown in the list with their tier.
- `[DHT]` and `[PEX]` rows reflect real state from `d.is_private`, `dht.mode`, and `protocol.pex`, and are disabled visually for private torrents.
- Multi-select "Edit trackers" applies an add or remove across the selection with per-torrent results.
- Tracker status text maps rTorrent's `t.latest_event`, `t.failed_counter`, `t.success_counter`, and `t.latest_new_peers` into Working / Failed (with reason) / Updating / Disabled / Not contacted.
- Component and integration tests cover add, remove, disable, and private-torrent rendering.

### PAR-2.4 — Peers tab parity (P1)

**As a** user, **I want** peer country, flags, and moderation controls **so that** I can diagnose swarm behaviour.

Acceptance criteria:

- Peers show country flag and name from an embedded, license-compatible GeoIP database updated by a documented build step; the database is optional and the column degrades to `—` when absent.
- Per-peer actions: Ban (`p.banned.set`), Snub/Unsnub (`p.snubbed.set`), Disconnect (`p.disconnect`), and Copy IP; multi-select supported.
- Columns: IP, Port, Country, Client, Flags (decoded tooltip), Have, Down, Up, Downloaded, Uploaded, Peer ID (hidden by default); sortable, with column visibility persisted.
- Peer list updates in place keyed by `ip:port` with no scroll jump under a 200-peer fixture.

### PAR-2.5 — General, Speed, and Logger detail tabs (P1)

**As a** user, **I want** the remaining ruTorrent detail tabs **so that** per-torrent history is visible.

Acceptance criteria:

- General: full-width key/value layout with every list field, tied file, comment, created-by, creation date, session path, and message; values copyable.
- Speed: per-torrent down/up graph over the last 60 minutes from a per-focused-torrent history ring on the server (retained for 60 minutes after unfocus, capped in memory).
- Logger: the torrent's `d.message` history and Blackbird-side action log (actions taken, result, actor) retained for a configurable window.
- Tab choice persists per browser; the detail panel is resizable by dragging its top edge and collapsible, with height persisted.

### PAR-2.6 — Real piece map (P1)

**As a** user, **I want** the Pieces tab to show the actual bitfield **so that** I can see where a download is stuck.

Acceptance criteria:

- The server fetches `d.bitfield` and, for the focused torrent only, exposes it as a base64 bitfield on the detail message, diffed so unchanged bitfields are not re-sent.
- The client renders a canvas piece map that buckets to the panel width, shows done/downloading/missing per the handoff colors, highlights the pieces of the hovered file, and shows piece index and file on hover.
- Rendering a 100,000-piece torrent stays under 4ms per update on the reference machine.

### PAR-2.7 — Torrent file utilities (P1)

**As a** user, **I want** to save the `.torrent`, open the magnet, and rename a torrent **so that** small chores don't need the shell.

Acceptance criteria:

- "Save .torrent" downloads the session file via an authenticated endpoint that never exposes the session directory path.
- "Rename" changes `d.name` where the daemon allows and otherwise is hidden; renaming files inside a torrent is P2.
- "Open directory" copies the base path and, where a configured URL template exists (`directories.open_url_template`), opens it.
- "Copy hash", "Copy name", "Copy path", and "Copy magnet" share one clipboard helper with a toast on success and a fallback for non-secure contexts.

---

## Epic 3 — Intake and automation

The features ruTorrent users install first: autowatch, autotools, RSS, unpack.

### PAR-3.1 — Watch directories (P0)

**As an** operator, **I want** Blackbird to load `.torrent` files dropped into watched directories **so that** tools like download managers and browsers can feed the session.

Acceptance criteria:

- `directories.watch` becomes a list of `{path, label, destination, start, delete_after_load, poll_interval}` entries; the existing scalar keys are migrated with a deprecation warning.
- A Go watcher (fsnotify with a polling fallback for network filesystems) loads files with `load.*` plus the same trailing commands used by the Add API, then renames the file to `.loaded` or deletes it per config.
- Malformed files move to a `failed/` subdirectory with a logged reason; duplicates (already-loaded hash) are handled without error spam.
- Watch activity appears in the torrent history (PAR-5.3) and as a toast when the UI is open.
- Settings > Directories manages the list; the Compose appliance wires the existing `watch` volume to a default entry.
- Tests cover load, duplicate, malformed, label/destination application, and the polling fallback.

### PAR-3.2 — Completion rules: auto-label and auto-move (P1)

**As an** operator, **I want** rules that run when a torrent finishes **so that** data lands in the right place without manual moves.

Acceptance criteria:

- YAML `automation.on_complete` defines an ordered rule list with match conditions (label, tracker host, name regex, size range, private flag) and actions (set label, move to destination, add tracker, run webhook).
- The poller detects the `d.complete` 0→1 transition and runs matching rules once, recording the result in the history log; rules never run twice for the same hash across restarts (persisted marker in Blackbird state, not in the torrent's `custom` slots unless configured).
- Moves use the PAR-2.2 engine; failures are surfaced as toasts and history entries.
- Settings > Automation edits rules with a "test against existing torrents" dry-run that lists what would match.

### PAR-3.3 — RSS feeds and filters (P1)

**As a** user, **I want** RSS subscriptions with match rules **so that** Blackbird replaces ruTorrent's RSS and autodl workflow.

Acceptance criteria:

- Feeds are configured with URL, poll interval, optional cookies/headers (stored as secrets, redacted in logs and the API), and a per-feed default label/destination.
- The server fetches and parses RSS/Atom, deduplicates items by GUID and enclosure hash, and stores the last N items per feed in Blackbird state.
- Filters match on title regex, category, size range, and feed; matches load automatically with the filter's label/destination/start options; a per-filter "match history" shows what was and was not loaded and why.
- An RSS view in the sidebar lists feeds and items with manual "Add" per item and a "mark all read" action.
- Fetch failures back off and show an error badge; feeds never block the torrent poller.
- Tests use recorded feed fixtures; an integration test proves a filter match ends as a torrent in the session.

### PAR-3.4 — Unpack on completion (P1)

**As a** user, **I want** archives extracted automatically **so that** downloads are usable without a shell.

Acceptance criteria:

- Extraction supports zip and rar (including multi-part) via a bundled extractor in the container image and a documented host dependency for native installs; missing extractor disables the feature with a clear Settings message.
- Rules choose destination (in-place or configured extract root), whether to delete archives, and a label filter; extraction runs in a bounded worker pool at low priority.
- Progress and results appear in the history log; a failed extraction never leaves a partial directory without a `.failed` marker.
- Path safety: extraction refuses entries that escape the destination (zip-slip) and is tested for it.

### PAR-3.5 — Retrackers (P2)

**As a** user, **I want** a list of trackers added to every new public torrent **so that** swarms are healthier.

Acceptance criteria:

- YAML `automation.retrackers` lists tracker URLs; on add and on watch load, public torrents receive them in a new group.
- Private torrents are never modified; the rule is visible in Settings > Automation.

---

## Epic 4 — Bandwidth and seeding policy

### PAR-4.1 — Named throttle channels (P0)

**As a** user on a shared line, **I want** named throttle groups assignable per torrent **so that** bulk seeds don't starve interactive downloads.

Acceptance criteria:

- YAML `tuning.throttles` defines named channels with up/down KB/s; on connect they are created with `throttle.up`/`throttle.down` and re-applied on change.
- Context menu and toolbar offer "Throttle ▸" listing channels plus "None"; assignment uses `d.throttle_name.set`, requiring a stop/start cycle only if the daemon version needs it (documented).
- The Throttle column (PAR-1.1) and a sidebar group show assignment; the Settings > Bandwidth section edits channels with validation and shows live usage per channel from `throttle.up.rate`/`throttle.down.rate`.
- Tests cover creation, reassignment, and removal of a channel still in use (refused with a count).

### PAR-4.2 — Ratio groups and seeding limits (P0)

**As a** user, **I want** stop-on-ratio and seeding-time rules per group **so that** seeding policy is enforced without cron scripts.

Acceptance criteria:

- YAML `seeding.groups` defines groups with min ratio, max ratio, min upload bytes, max seeding time, and an action (stop, stop and set label, erase, erase with data); torrents are assigned via context menu and a Ratio group column, stored in a configurable `custom` slot.
- Enforcement is implemented in Blackbird's poller (evaluated per cycle against the list data) rather than `group.seeding.*` schedules, so rules are visible, testable, and versioned; the design note explains why and records the trade-off.
- Rules act at most once per torrent per condition and log to the history; "erase with data" honours SEC-2.3 safety.
- Settings > Seeding edits groups with a dry-run preview listing torrents that would be acted on now.
- Tests cover every condition and action, including a torrent that changes group after triggering.

### PAR-4.3 — Bandwidth scheduler (P1)

**As a** user, **I want** a weekly grid of bandwidth limits **so that** the connection is free during work hours.

Acceptance criteria:

- YAML `schedule.bandwidth` defines a 7×24 grid of profile names, each profile a set of global and channel limits; the active profile is applied by Blackbird on the minute boundary and after reconnect.
- Settings > Scheduler renders the grid with paint-to-fill editing and profile colors; the status bar shows the active profile with a manual override that expires.
- Time zone is explicit in YAML and defaults to the server's; DST transitions are tested.

### PAR-4.4 — Quick speed-limit popover (P1)

**As a** user, **I want** to change the global limits from the status bar **so that** I can throttle instantly.

Acceptance criteria:

- Clicking the global rates in the status bar opens a popover with presets (unlimited, 25%, 50%, 75%, custom) for down and up, applied immediately via `throttle.global_*.max_rate.set`.
- The popover shows the current limit next to the live rate and reflects scheduler overrides.
- Changes persist to YAML only when the user chooses "Save as default".

---

## Epic 5 — Operator tools and observability

### PAR-5.1 — Directory browser (P0)

**As a** user, **I want** to browse server-side directories **so that** destinations are chosen, not typed.

Acceptance criteria:

- An authenticated endpoint lists directories under configured download roots only, never files outside them, with symlink resolution and canonical-path checks; requests outside roots return the SEC-2.3 error code.
- The browser component shows free space per root, supports creating a new directory, remembers recent picks, and is reused by Add, Move, Settings > Directories, and automation rules.
- Tests cover traversal attempts, unreadable directories, and roots on separate filesystems.

### PAR-5.2 — Traffic and resource statistics (P1)

**As an** operator, **I want** daily and monthly transfer totals and host load **so that** I can watch quotas.

Acceptance criteria:

- Blackbird persists per-day down/up totals derived from `throttle.global_*.total` deltas, robust to daemon restarts (totals reset detection).
- Stats page gains Traffic (day/week/month bars with a selectable range, export to CSV) and Host cards (load average, memory, and per-volume free space from the existing statfs sampler).
- Retention is configurable; storage is a compact append-only file in the state directory.

### PAR-5.3 — Torrent history log (P1)

**As an** operator, **I want** a log of adds, completions, moves, rule actions, and removals **so that** I can answer "what happened to X".

Acceptance criteria:

- Events include timestamp, hash, name, event type, actor (user, watch, RSS, rule, scheduler), and details; retained for a configurable count/age.
- A History view lists and filters events, and a per-torrent Logger tab (PAR-2.5) shows the subset.
- Events are exposed by an authenticated endpoint with pagination and are the same source used by toasts.

### PAR-5.4 — Create torrent (P2)

**As a** user, **I want** to build a `.torrent` from server-side data **so that** seeding my own content doesn't need a desktop client.

Acceptance criteria:

- Modal picks a path via the directory browser, trackers, piece size (auto or fixed), private flag, comment, and source; creation runs in the background with progress and hashes in a bounded worker.
- The result can be downloaded and optionally added to the session started and tied to the source path.

### PAR-5.5 — Port check (P1)

**As an** operator, **I want** an external reachability test **so that** "Port open" means something.

Acceptance criteria:

- A user-initiated check asks a configurable external service (or Blackbird's own probe when a public listener is configured) whether the listening port is reachable; the result, timestamp, and method are shown next to the port field and in the status bar.
- No external request is ever made automatically; the service URL is documented and can be disabled.

### PAR-5.6 — IP filter (P2)

**As an** operator, **I want** to load a blocklist **so that** known-bad ranges are not contacted.

Acceptance criteria:

- YAML `network.ipfilter` points to a local P2P/DAT-format file or URL with a refresh interval; ranges are applied with `ipv4_filter.load` on connect and refresh.
- Settings shows the rule count, last load time, and errors.

---

## Epic 6 — Backend performance

Target: a 5,000-torrent session with 20 active downloads on a 2-core VM keeps the list poll under 150ms, WebSocket delta payloads under 20 KB per tick at steady state, and Blackbird under 150 MB resident.

### PERF-6.1 — Take network I/O out of the poller lock (P0)

**As an** operator, **I want** API and WebSocket reads to never block on rTorrent **so that** the UI stays responsive during slow detail fetches.

Acceptance criteria:

- `refreshDetailsLocked` and every other path that performs SCGI calls under `p.mu` is restructured to fetch outside the lock and swap results in under a short critical section.
- `Snapshot()` returns an immutable snapshot pointer produced once per cycle (copy-on-publish), not a deep copy per caller.
- A regression test asserts that `Snapshot()` returns within 1ms while a detail fetch is artificially stalled.

### PERF-6.2 — Field-level deltas (P0)

**As a** user with a large session, **I want** only changed fields on the wire **so that** ticks are cheap for the browser.

Acceptance criteria:

- The WebSocket delta carries `{hash, fields: {...}}` patches for changed torrents instead of whole objects; the message schema version is bumped and the client handles both during a documented transition.
- Global stats are included only when changed; aggregates are sent as patches.
- Per-message compression (`permessage-deflate`) is enabled with a measured before/after on the 5,000-torrent fixture recorded in the PERF-6.6 report.
- Slow clients are coalesced (latest-wins per hash) rather than silently dropped; a metric counts coalesced ticks.

### PERF-6.3 — Batch and bound rTorrent round trips (P0)

**As an** operator, **I want** each poll cycle to be as few SCGI calls as possible **so that** rTorrent spends its time on torrents.

Acceptance criteria:

- The list poll and global stats are fetched in one `system.multicall`; `FetchDetail` is genuinely one multicall, matching its comment.
- SCGI responses are read with a configurable size limit (default 64 MB) and a per-call timeout; exceeding either produces a typed error and a reconnect, never unbounded memory.
- Detail refresh interval and per-client detail sends are driven by change detection (hash of the detail payload), not a fixed 500ms re-send.
- Adaptive list polling: the interval stretches toward `poll.max_interval` when no client is connected or all are hidden, and snaps back on the first active client.

### PERF-6.4 — Allocation and aggregate hygiene (P1)

**As a** maintainer, **I want** the poll cycle to be allocation-stable **so that** GC pauses don't show as jitter.

Acceptance criteria:

- `indexByHash`, `computeAggregates`, and `computeDelta` reuse buffers across cycles; per-cycle allocations on the 5,000-torrent fixture are measured and recorded, with a CI benchmark guarding a 20% regression.
- History ring pruning and the per-torrent speed rings (PAR-2.5) are ring buffers, not slice re-slices.
- Dead code listed in the baseline is removed.

### PERF-6.5 — HTTP delivery (P1)

**As a** user on a slow link, **I want** the app shell to load fast **so that** first paint is under a second on a LAN.

Acceptance criteria:

- Embedded assets are served pre-compressed (brotli and gzip) with `Content-Encoding` negotiation and immutable cache headers; `index.html` remains no-cache.
- The font is preloaded; the initial bundle is under 120 KB compressed and the size is checked in CI.
- HTTP/2 is supported behind TLS termination and documented; `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are set (shared with `backlog.md` SEC-2.2).

### PERF-6.6 — Performance fixtures, benchmarks, and budgets (P0)

**As a** maintainer, **I want** a repeatable large-session benchmark **so that** every performance story has a number.

Acceptance criteria:

- `fakertorrent` can generate deterministic sessions of 500, 5,000, and 20,000 torrents with configurable activity; a `make bench` target runs Go benchmarks for the poll cycle, delta computation, and WebSocket encoding.
- A documented performance report records poll latency, delta payload size, memory, and goroutine count per fixture on a reference machine, and CI fails on a 20% regression against checked-in baselines.
- The unknown-method fallback in `fakertorrent` returns a fault instead of an empty string, so typo'd methods fail tests.

---

## Epic 7 — Frontend performance

Target: 5,000 rows visible in the filter with 200 changing per tick renders each tick in under 8ms of main-thread time on a 2020 laptop, scrolls at 60 fps, and sorts or filters within one frame.

### PERF-7.1 — Keyed store with reconciled updates (P0)

**As a** user, **I want** live updates to touch only the cells that changed **so that** the table never flickers or drops frames.

Acceptance criteria:

- `store/session.ts` moves torrents to `createStore` keyed by hash; deltas are applied with field-level `produce`/`reconcile` so unchanged torrents keep object identity and changed torrents update in place.
- Rows render from stable per-hash references; changing `upRate` updates a single text node (verified with a DOM mutation count test).
- All signal writes for one WebSocket message happen inside one `batch()`.
- `pruneSelection` and `selectedSet` are no-ops when nothing changed (identity preserved), verified by a test asserting no row re-evaluation on an unrelated tick.

### PERF-7.2 — Incremental sort and filter (P0)

**As a** user, **I want** the visible row order maintained incrementally **so that** a tick doesn't re-sort the world.

Acceptance criteria:

- The sorted, filtered view is maintained as an ordered index updated per delta (insert/remove/reposition only for torrents whose sort key or filter membership changed); full re-sort happens only on sort or filter changes.
- `torrentList`'s per-tick key sort is removed; the four-array allocation chain (`rows`, `hashes`, the effect, `visibleHashes`) collapses to one derived index.
- `DetailPanel` looks up the focused torrent by key, not by linear scan.
- Benchmarks in the component test suite record sort and filter timings on the 5,000-row fixture.

### PERF-7.3 — Virtualized table (P0)

**As a** user with thousands of torrents, **I want** smooth scrolling **so that** the table behaves like a native list.

Acceptance criteria:

- Rows are windowed with overscan using the fixed 30px row height; sticky header, alternating row backgrounds, selection, keyboard navigation (including page up/down, home/end), and drag-and-drop continue to work.
- Scroll position is preserved across ticks and filter changes; the focused row scrolls into view on keyboard navigation.
- Row insert/remove fade (120ms) is preserved for rows inside the window and skipped outside it.
- 20,000 rows scroll at 60 fps in the browser benchmark; DOM node count stays under 1,500 regardless of session size.

### PERF-7.4 — Detail, sparkline, and stats rendering (P1)

**As a** user, **I want** secondary panels to stay cheap **so that** the main table keeps its budget.

Acceptance criteria:

- Sparkline and throughput graph build point strings incrementally (append one sample, drop one) and memoize hover computations; hover does not rebuild polylines.
- Peers and files lists are virtualized above 200 rows; the piece map draws to canvas (PAR-2.6).
- All 1-second intervals are consolidated into one shared ticker signal, paused while the tab is hidden.
- Detail subscriptions are cancelled on unfocus and the tab-hidden signal also suspends stats polling.

### PERF-7.5 — Bundle and startup (P1)

**As a** user, **I want** a fast first load **so that** the console feels instant on a LAN.

Acceptance criteria:

- Settings, Stats, RSS, and History routes are code-split; the console route ships first.
- Bundle size is reported in CI with a budget; no dependency is added without a recorded size delta.
- First snapshot renders skeleton-to-data within 300ms of WebSocket connect on the 5,000-torrent fixture.

---

## Epic 8 — Polish and shippable quality

### POL-8.1 — Frontend test harness (P0)

**As a** maintainer, **I want** component and browser tests **so that** UI stories can be verified and regressions caught.

Acceptance criteria:

- Vitest with Solid testing utilities and a DOM environment; a `test` script in `web/package.json`; CI runs typecheck, lint, unit tests, and build on every pull request.
- Playwright runs against the Go server with `fakertorrent` for deterministic end-to-end flows, and against the Compose appliance for the release smoke (shared with `backlog.md` QA-5.3).
- ESLint and Prettier are configured; the current 3,000-character lines are reformatted; a lint rule caps line length and bans `window.confirm`/`window.prompt`/`window.alert`.
- Coverage thresholds are set for `lib/`, `store/`, and formatting utilities.

### POL-8.2 — Replace native dialogs with an in-app dialog system (P0)

**As a** user, **I want** consistent confirmations and inputs **so that** destructive actions look intentional and can't be blocked by browser popup settings.

Acceptance criteria:

- One `Dialog` primitive (modal, focus-trapped, `role="dialog"`, `aria-modal`, Escape and backdrop close where safe) backs confirm, prompt, and form dialogs; every one of the eleven native call sites is migrated.
- Destructive confirmations name the torrent count and, for data removal, the paths; the destructive button is styled per the handoff and requires no double-confirm.
- "Don't ask again for this session" is available on non-data-destructive confirms.
- Tests cover focus trap, restore focus on close, and keyboard operation.

### POL-8.3 — Notification system (P0)

**As a** user, **I want** notifications that queue, carry severity, and can be acted on **so that** feedback is never lost.

Acceptance criteria:

- Toasts queue (max visible 3, with an overflow count), have success/info/warning/error variants, optional action buttons (Undo for label changes, Retry for failed actions, View for completed torrents), and configurable duration; errors persist until dismissed.
- Duplicate messages within a window coalesce into a count (shared with `backlog.md` SHIP-1.3).
- Optional browser Notifications for completion, error, and RSS matches, gated by a Settings toggle and permission flow.
- A notification centre lists the last 50 with timestamps.
- The tab title shows aggregate rates and a count of active downloads; the favicon reflects connection state.

### POL-8.4 — Wire every Settings field and remove inert ones (P0)

**As a** user, **I want** every setting to do something **so that** the UI never lies.

Acceptance criteria:

- `ui.date_format` and `ui.rate_format` drive all formatters (with tests for binary vs decimal and local vs ISO); `ui.sort` seeds the sort store; `ui.visible_columns` migrates into PAR-1.2's layout; `ui.poll_interval` is either removed or becomes the client-side polling hint it claims to be, with a documented migration.
- `ui.accent` (and the theme selection from Epic 9) is applied at boot from the session snapshot, not only when Settings is mounted; unsaved Settings drafts preview live and revert on Revert or navigation away.
- Settings sections that are prose-only (General) gain their content or are removed; the Bandwidth and Queue sections reflect PAR-4.x.
- A "restart required" or "reconnect required" badge appears on fields that need it (shared with `backlog.md` DOC-7.2).

### POL-8.5 — Accessibility and keyboard completeness (P0)

**As a** keyboard or screen-reader user, **I want** the console operable without a mouse **so that** it meets a basic WCAG 2.1 AA bar.

Acceptance criteria:

- Table: rows are focusable with roving tabindex, `aria-selected`, and `aria-rowindex`; headers are buttons with `aria-sort`; the selection model is announced.
- Tabs (detail panel, add modal) use `role="tab"`/`tabpanel` with arrow-key navigation; context menu uses `role="menu"` with arrow, Home/End, and type-ahead.
- Visible focus rings on every interactive element using an accent-derived outline token; no `outline: none` without a replacement.
- Lost-connection banner is a live region; toasts use `aria-live` appropriate to severity.
- Additional shortcuts: `A` add torrent, `R` recheck, `1`–`5` detail tabs, `?` shortcut help overlay, `F` toggle detail panel, `Ctrl/Cmd+K` command palette (P1); all listed in a help dialog generated from the binding table so hints never drift.
- Automated axe checks run in the Playwright suite with zero serious violations.

### POL-8.6 — Routing, layout persistence, and session continuity (P1)

**As a** user, **I want** the browser back button, deep links, and remembered layout **so that** the console behaves like an app.

Acceptance criteria:

- Routes `/`, `/settings/:section`, `/stats`, `/rss`, `/history`, and `?filter=`/`?focus=<hash>` query state are reflected in the URL and restorable on reload; back/forward work.
- Sidebar width, detail panel height and tab, column layout, sort, and last route persist per browser; a "Reset layout" action clears them.
- Reconnect after a server restart restores selection and focus where the hashes still exist.

### POL-8.7 — Empty, loading, and error states across every view (P1)

**As a** new user, **I want** every screen to explain itself when empty **so that** first run isn't a blank table.

Acceptance criteria:

- Empty session shows a guided empty state (add torrent, set up a watch directory, configure RSS) with links to docs.
- Every list (peers, trackers, files, RSS, history, volumes) has an empty state and a loading skeleton; every fetch failure has an inline retry.
- Version and update check: Settings > About shows Blackbird, rTorrent, and libtorrent versions, build metadata, and an optional opt-in release check.
- Screenshots for the README (`backlog.md` DOC-7.1) are captured from the Playwright suite so they never go stale.

### POL-8.8 — Code health (P1)

**As a** maintainer, **I want** the codebase reviewable **so that** the community can contribute.

Acceptance criteria:

- Dead code in the baseline is removed; `GlobalSettingsKeys` and `tuning.GetterMethods` are unified into one table.
- Components over 150 lines are split by responsibility (settings sections become one file each); CSS is split per component or uses CSS modules, keeping tokens in one place.
- `go vet`, `staticcheck`, `-race`, and the frontend lint run in CI (shared with `backlog.md` QA-5.1).
- The REST API gains a `/api/v1` prefix and a `/api/v1/version` route; the WebSocket schema bump from PERF-6.2 ships alongside.

---

## Epic 9 — Theming

The handoff fixed everything but the accent. Supporting real themes means promoting the current 76-token dark palette to one theme among several, without giving up the handoff's density rules or the progress-color rule.

### THM-9.1 — Two-layer token architecture (P0)

**As a** frontend developer, **I want** semantic tokens separated from palette values **so that** a theme is a value set, not a stylesheet.

Acceptance criteria:

- Tokens split into a **palette layer** (raw colors per theme) and a **semantic layer** (`--bg-*`, `--text-*`, `--border-*`, `--accent-*`, `--rate-*`, `--progress-*`, `--status-*`) consumed by components; components reference only semantic tokens (enforced by a stylelint rule banning hex and `rgb()` outside `themes/`).
- Derived accent tokens (`--accent-tint`, `--accent-tint-strong`, `--accent-text`, `--accent-foreground`, focus ring) are computed from `--accent` with `color-mix()` and relative color syntax, with a JS fallback that computes them on theme apply for browsers lacking support; the accent picker therefore re-tints selection, chips, sidebar, and graphs correctly.
- Non-color tokens (typography, heights, spacing, radii, shadows, motion) stay theme-independent by default but are overridable by a theme for shadow and radius only.
- Charts, sparkline, canvas piece map, and the `<meta name="theme-color">` read computed token values, not literals, and re-read on theme change.
- The rule "progress is never the accent" is preserved per theme by keeping `--progress-*` tokens separate and documented.

### THM-9.2 — Theme mechanism and built-in themes (P0)

**As a** user, **I want** to choose a theme, including light and system-follow **so that** the console fits my environment.

Acceptance criteria:

- Themes are applied via `data-theme` on `<html>`; a `system` mode follows `prefers-color-scheme` and switches live; the choice persists per browser and can be set as the operator default in YAML `ui.theme`.
- Built-in themes ship with full palette definitions and pass WCAG AA contrast for body text, muted text on row backgrounds, and accent text on tinted chips (checked by a script in CI):
  - **Blackbird Dark** (the handoff palette, default)
  - **Blackbird Light**
  - **Midnight** (true-black OLED variant)
  - **High Contrast** (dark, larger borders, stronger text)
  - **Classic** (a ruTorrent-inspired light theme with blue selection and the familiar status colors, for migrating users)
- Theme choice is applied before first paint (inline script reading localStorage plus a server-injected default) so there is no flash of the wrong theme.
- Label colors, status colors, and the five accent presets are defined per theme so a label readable on dark is also readable on light; user-defined label colors get an automatic contrast-safe text color.
- Every Playwright screenshot test runs in Dark and Light; visual regression baselines exist for both.

### THM-9.3 — Theme picker and live preview (P0)

**As a** user, **I want** to preview and tweak themes in Settings **so that** customization doesn't require editing files.

Acceptance criteria:

- Settings > Interface shows theme cards with miniature previews, the system-follow toggle, accent presets and a custom accent picker, and a density option (Dense per handoff, Comfortable adds 4px to rows and controls) applied via the height tokens.
- Changes preview live across the whole app and revert on Revert; Save persists per browser and, for operators, offers "Set as server default".
- The current accent-application bug (only applied while Settings is mounted) is fixed as part of this story.

### THM-9.4 — Custom themes and operator CSS (P1)

**As an** operator, **I want** to define my own theme **so that** the console can match my other dashboards.

Acceptance criteria:

- A theme file format (YAML or JSON, versioned, documented with every token and its role) can be placed in the config directory under `themes/`; valid files appear in the picker, invalid ones log a line-numbered error and are skipped.
- Themes can extend a built-in theme and override a subset of tokens; the contrast checker runs on load and reports warnings in Settings.
- An optional `custom.css` in the config directory is injected after the theme with a documented stability warning; it is served with the same auth as the app.
- Export from the picker writes the current theme (including accent and density) as a theme file; import validates and installs it.
- Theme authoring is documented with a token reference generated from the semantic token table so it cannot drift.

### THM-9.5 — Theme-aware everything (P1)

**As a** user, **I want** every surface to respect the theme **so that** nothing looks bolted on.

Acceptance criteria:

- Scrollbars (`scrollbar-color`), form controls (`color-scheme`), selection highlight, native date/color inputs, the drop-zone highlight, skeleton shimmer, and the modal backdrop all use tokens.
- Print styles exist for History and Stats (P2 may defer).
- The favicon and `theme-color` follow the theme; the Compose smoke test verifies the light theme loads without a console error.

---

## Suggested delivery order

1. **Foundations:** POL-8.1 (test harness), PERF-6.6 (fixtures and benchmarks), THM-9.1 (token layers). Everything else is measured and tested on top of these.
2. **Performance core:** PERF-6.1, 6.2, 6.3, then PERF-7.1, 7.2, 7.3. Do these before adding columns and features, so parity work lands on a fast data path.
3. **Table and action parity:** PAR-1.1, 1.2, 1.3, PAR-2.1, 2.2, 2.3, PAR-5.1.
4. **Shippable polish:** POL-8.2, 8.3, 8.4, 8.5, THM-9.2, 9.3.
5. **Automation and policy:** PAR-3.1, PAR-4.1, PAR-4.2, then PAR-3.2, 3.3, 3.4, PAR-4.3, 4.4.
6. **Depth:** PAR-2.4 through 2.7, PAR-5.2 through 5.6, PERF-6.4, 6.5, 7.4, 7.5, POL-8.6 through 8.8, THM-9.4, 9.5.

Interleave with `backlog.md`: its Epics 1–2 (contract freeze, security) should land before PERF-6.2's schema bump and PAR-5.1's filesystem endpoint; its Epic 5 CI matrix is the home for the benchmarks and browser suites defined here.

## v1.0 product gate

In addition to the `backlog.md` ship gate, Blackbird v1.0 is feature-complete when every P0 story in this document is done, meaning: a ruTorrent user can configure columns, use every category view, watch directories, throttle channels and ratio groups; a 5,000-torrent session meets the Epic 6 and 7 budgets in the recorded benchmark; no native browser dialog remains; the console is keyboard-operable with zero serious axe violations; and Dark, Light, and system-follow themes ship with contrast checks passing in CI.

## Explicitly out of scope for v1.x

Recorded so they are not mistaken for gaps: multi-user accounts and per-user permissions; a mobile layout below 900px; ruTorrent plugin equivalents for mediainfo, screenshots, spectrogram, media streaming, file sharing links, XMPP, Cloudflare bypass, and rutracker checks; a plugin system of any kind; importing ruTorrent configuration or plugin data; and Windows as a server platform (the statfs sampler is Unix-only and stays so).
