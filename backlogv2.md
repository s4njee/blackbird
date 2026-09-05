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

Results:

- Added server-defined All, Completed, Active, and Inactive category counts alongside the normalized rTorrent states; counts now travel in every successful WebSocket delta.
- Added `d.complete` and `d.is_open` to the normalized torrent payload so table filters use the exact same category rules as the server.
- Updated the sidebar to show all ruTorrent category views in a stable order and consume server-authoritative counts.
- Documented category definitions in the Compose user guide and covered the membership rules with poller tests.

Remaining:

- Add browser-level coverage for category selection and live count updates when the frontend test harness from POL-3.1 lands.

### PAR-1.4 — Search and advanced filtering (P1)

**As a** user with thousands of torrents, **I want** to search by more than name **so that** I can find a torrent by hash, path, tracker, or message.

Acceptance criteria:

- The filter field matches name, hash prefix, base path, tracker host, and error message; a field prefix syntax (`label:`, `tracker:`, `path:`, `status:`, `ratio>`, `size<`) narrows the match, documented in a `?` popover.
- Matching runs against a precomputed lowercase index updated incrementally per delta, not by re-lowercasing every row per keystroke.
- Saved filters (name + query + sidebar state) can be pinned to the sidebar and persist per browser; YAML `ui.saved_filters` provides operator defaults.
- Selection and focus survive filter changes when the focused torrent is still visible.
- Tests cover the query grammar and index invalidation.

Results:

- Added incremental lowercase search indexing for name, hash prefix, path, tracker host, message, and label; snapshot, add/change delta, and removal paths update the index without lowercasing fields at query time.
- Added AND-combined field filters (`label:`, `tracker:`, `path:`, `status:`) and numeric comparisons (`ratio`, `size`) with a syntax popover beside the search input.
- Added browser-persisted saved filters with sidebar pins and optional YAML defaults through `ui.saved_filters`.
- Added parser/index invalidation coverage with the frontend test command and configuration loading coverage for operator defaults.

Remaining:

- Add browser automation coverage for the popover, saved-filter interactions, and selection retention when the POL-3.1 frontend harness is available.

### PAR-1.5 — Sort parity (P1)

**As a** user, **I want** sorting on every column with stable secondary ordering **so that** the table is predictable while it ticks.

Acceptance criteria:

- Every column in the catalogue is sortable, with numeric, string, date, and enum comparators chosen per column.
- Shift-click adds a secondary sort key; the header shows both carets with an ordinal.
- Sort preference has exactly one source of truth: per-browser localStorage seeded from YAML `ui.sort`; the current split between the two stores is removed.
- Sorting is incremental (PERF-7.2); a sort-key change on a 5,000-row fixture completes within one frame.

Results:

- Replaced the single sort field with browser-persisted primary/secondary sort keys, seeded from backward-compatible `ui.sort` YAML values.
- Made every catalogue column sortable with explicit numeric, date, enum, and locale-aware text comparisons; hash is the stable final tie-breaker.
- Added Shift-click secondary sorting, ordinal header carets, `aria-sort` on the primary heading, and a header hint.
- Added an incremental sorter that binary-inserts changed rows and falls back to a full sort only after a substantial list change; unit coverage exercises changed-row reordering and multi-key sorting.
- Documented browser and YAML sort behavior in the Compose user guide.

Remaining:

- Record the PERF-7.2 5,000-row frame-time benchmark and add browser automation for Shift-click headers when the POL-3.1 harness is available.

---

## Epic 2 — Torrent actions and detail parity

### PAR-2.1 — Complete the transport and priority action set (P0)

**As a** user, **I want** every per-torrent action ruTorrent offers **so that** no workflow forces me back to the old UI.

Acceptance criteria:

- New actions over the existing batch endpoint: `force_start` (`d.open` + `d.start` regardless of queue), `set_priority` with values 0–3 exposed as Off/Low/Normal/High in the toolbar and context menu, `superseed` toggle (`d.connection_seed.set`), `sequential` toggle (`d.sequential.set`) on live torrents, `save_session` (`d.save_full_session`), and `set_custom` for `custom2`–`custom5`.
- Context menu gains "Priority ▸" and "Advanced ▸" submenus matching ruTorrent grouping; keyboard hints are added only where a binding exists.
- Each action is recorded by `fakertorrent` and verified against real rTorrent in the QA-5.2 integration target from `backlog.md`.
- Optimistic UI covers the new state fields with rollback on failure.

Results:

- Added typed batch actions for force start, all four torrent priorities, live superseeding and sequential-download toggles, session saving, and the `custom2`–`custom5` fields.
- The torrent payload now includes authoritative sequential and superseeding state, so those controls and priority/custom field changes update optimistically and restore the prior rows if rTorrent rejects the request.
- Reworked the toolbar to expose the full Off/Low/Normal/High priority set. The context menu now has ruTorrent-style Priority and Advanced submenus for force start, live toggles, session save, and custom fields.
- Added fake-rTorrent command recording plus API/client coverage proving the extended actions reach their intended XML-RPC methods; malformed toggle and custom-field requests are rejected before any daemon call.
- Documented the controls and per-torrent error/rollback behavior in the Compose user guide.

Remaining:

- Exercise these actions against the dedicated disposable real-rTorrent QA-5.2 integration target rather than a user torrent.
- Add browser automation for toolbar priority selection, nested context menus, and optimistic rollback when the POL-3.1 harness is available.

### PAR-2.2 — Move data and set directory (P0)

**As a** user, **I want** a safe, guided move flow **so that** I never type a path by hand.

Acceptance criteria:

- The move dialog replaces `window.prompt` with a modal offering configured download roots, recent destinations, per-label destinations, and a directory browser (PAR-5.1) constrained to allowed roots.
- Two modes: "Set directory only" (`d.directory.set` for already-relocated data) and "Move files" (Go-side move); cross-device moves copy then verify then delete, with a progress indicator and cancellation.
- Running torrents are stopped, moved, and restarted automatically; the dialog explains this and shows per-torrent results.
- Path safety rules from `backlog.md` SEC-2.3 apply; symlink escapes and cross-root moves are refused with a clear message.
- Tests cover same-device rename, cross-device copy, partial failure, and cancellation.

Results:

- Replaced free-text move prompts with a guided Move data dialog offering configured roots, per-label destinations, browser-persisted recents, and a server-constrained directory browser.
- Added separate Move files and Set directory only modes, an asynchronous per-torrent job/result view, and cancellation for queued or copying work.
- Running torrents are now stopped and restarted automatically; stopped torrents remain stopped. Cross-device moves copy then SHA-256 verify before deleting the source.
- Enforced the configured-root boundary on both browser and move APIs, excluding symlink entries and refusing symlink sources or resolved escapes.
- Added filesystem coverage for rename, cross-device fallback, partial failure, and cancellation, and documented the flow in the Compose guide.

Remaining:

- Add browser automation for destination browsing, live progress, cancellation, and running-torrent restart once the POL-3.1 harness is available.

### PAR-2.3 — Full tracker editing (P0)

**As a** user, **I want** to add, remove, disable, and regroup trackers **so that** dead trackers can actually be cleaned up.

Acceptance criteria:

- "Remove" removes the tracker (rebuild the tracker list via `d.tracker.insert`/`d.tracker.remove` or the announce-list rewrite approach that the supported rTorrent versions provide; the chosen mechanism is documented); "Disable" remains separate.
- Trackers can be added to a chosen group; groups are shown in the list with their tier.
- `[DHT]` and `[PEX]` rows reflect real state from `d.is_private`, `dht.mode`, and `protocol.pex`, and are disabled visually for private torrents.
- Multi-select "Edit trackers" applies an add or remove across the selection with per-torrent results.
- Tracker status text maps rTorrent's `t.latest_event`, `t.failed_counter`, `t.success_counter`, and `t.latest_new_peers` into Working / Failed (with reason) / Updating / Disabled / Not contacted.
- Component and integration tests cover add, remove, disable, and private-torrent rendering.

Results:

- Added a distinct `tracker_remove` batch action backed by rTorrent’s `d.tracker.remove`; Disable/Enable continues to use `t.is_enabled.set` and no longer masquerades as removal.
- Tracker additions now accept a non-negative group/tier and the detail list displays each tracker’s tier.
- Extended tracker detail data with latest event, failed/success counters, and new-peer count; the UI maps those fields to Working, Failed, Updating, Disabled, and Not contacted states.
- Added validation and client/API transport coverage for tracker removal and grouped additions, and rebuilt the healthy Compose appliance.

Remaining:

- Add DHT/PEX state to the detail payload and render private-torrent-disabled rows.
- Add the multi-select Edit trackers workflow, including per-torrent batch results.
- Add component/browser coverage for grouped rendering, disable/remove distinction, and private torrents once the POL-3.1 harness is available; run the disposable real-rTorrent integration target as part of QA-5.2.

### PAR-2.4 — Peers tab parity (P1)

**As a** user, **I want** peer country, flags, and moderation controls **so that** I can diagnose swarm behaviour.

Acceptance criteria:

- Peers show country flag and name from an embedded, license-compatible GeoIP database updated by a documented build step; the database is optional and the column degrades to `—` when absent.
- Per-peer actions: Ban (`p.banned.set`), Snub/Unsnub (`p.snubbed.set`), Disconnect (`p.disconnect`), and Copy IP; multi-select supported.
- Columns: IP, Port, Country, Client, Flags (decoded tooltip), Have, Down, Up, Downloaded, Uploaded, Peer ID (hidden by default); sortable, with column visibility persisted.
- Peer list updates in place keyed by `ip:port` with no scroll jump under a 200-peer fixture.

Results:

- Peers now resolve to a country code from an embedded DB-IP Lite database (`internal/geoip`, CC-BY-4.0, committed gzipped and refreshed by a documented generate step). Public IPv4 ranges map to a flag + country name; unknown, private, and unroutable addresses degrade the column to `—`.
- Added per-peer Ban (`p.banned.set`), Snub/Unsnub (`p.snubbed.set`), and Disconnect (`p.disconnect`) to the batch action endpoint, keyed by `hash:pPEERID` with a validated `peerId`; multi-select Snub/Unsnub/Disconnect/Ban and Copy IP are available from a right-click menu and a bulk bar.
- The Peers tab now shows IP, Port, Country, Client, Flags (with decoded-tooltip meanings), Have, Down, Up, Downloaded, Uploaded, and Peer ID (hidden by default). Columns are sortable and show/hide/reorder is persisted per browser alongside a peer sort key; IP cannot be hidden.
- Peer rows now carry per-connection download/upload totals and snub state from `p.down_total`/`p.up_total`/`p.is_snubbed`. Detail ticks reconcile peers by `ip:port` so unchanged rows keep their DOM identity and only genuinely changed peers re-render or re-sort — no full-list rebuild or scroll jump under live 1s detail updates.
- Added Go client/API coverage for the new fields and moderation calls plus pure-logic frontend tests for peer sorting, flag decoding, and country formatting, all running under `go test ./...` and `npm test`; smoke-tested against the fake daemon.

Remaining:

- Add browser automation for peer multi-select, context menus, and moderation round-trips once the POL-3.1 harness is available.
- Exercise Snub/Unsnub/Disconnect/Ban against the disposable real-rTorrent integration target (QA-5.2) to confirm `p.*` sub-target semantics across supported daemon versions.
- Consider surfacing the DB-IP attribution link (required by CC-BY-4.0 for web display) in the UI rather than only in `THIRD_PARTY_NOTICES.md`.

### PAR-2.5 — General, Speed, and Logger detail tabs (P1)

**As a** user, **I want** the remaining ruTorrent detail tabs **so that** per-torrent history is visible.

Acceptance criteria:

- General: full-width key/value layout with every list field, tied file, comment, created-by, creation date, session path, and message; values copyable.
- Speed: per-torrent down/up graph over the last 60 minutes from a per-focused-torrent history ring on the server (retained for 60 minutes after unfocus, capped in memory).
- Logger: the torrent's `d.message` history and Blackbird-side action log (actions taken, result, actor) retained for a configurable window.
- Tab choice persists per browser; the detail panel is resizable by dragging its top edge and collapsible, with height persisted.

Results:

- Added General, Speed, and Logger detail tabs. General renders a full-width key/value list (name, hash, comment, created-by, creation date, tied file, session path, daemon message, transfer totals, tracker, priority, private) with copy buttons on identifier values.
- .torrent metadata (comment, created-by, creation date, infohash) is captured at add time by a new dependency-free bencode parser (`internal/torrentfile`) and merged with live session rows; torrents added outside Blackbird degrade the comment/created-by fields to `—` and keep `d.creation_date` for the date.
- Speed shows a per-torrent down/up SVG graph over the last 60 minutes. The server keeps a proper per-hash ring buffer, samples only focused torrents each poll, retains each ring for 60 minutes after the last unfocus, and prunes it afterward (capped per hash and in memory).
- Logger shows the torrent's `d.message` transitions (detected by the poller when a row's message changes) plus a Blackbird-side action log: every batch action records actor (the authenticated user, or "local" when auth is disabled), verb, per-hash result, and error. Retention is bounded per torrent and by age through a new `history:` YAML block (`action_log_entries`, `action_log_retention`, `message_entries`) that round-trips through the settings API.
- The three tabs are served as `?view=general|logger|speed` on the existing `GET /api/torrents/{hash}` route (Go 1.22 ServeMux cannot host both a `{hash}` wildcard and literal sub-routes like `move/{id}` without ambiguity).
- Detail tab choice, panel height, and collapsed state persist per browser under `blackbird.detail-panel.v1`; the panel is resizable by dragging its top edge and collapsible via a header toggle.
- Added Go coverage for the bencode parser, history log bounds/age-prune, poller speed-ring sampling/retention/pruning, message-transition detection, and the general/logger/speed endpoints; the frontend typechecks and builds.
- Follow-up: the `history:` retention settings (action-log entries, retention window, message entries) are now surfaced under a new Settings → History section that edits them as human durations and round-trips through the settings API.
- Follow-up: the Compose stack now mounts rTorrent's session volume read-only into Blackbird at `/data/session` (default, macOS, and Linux variants), the bootstrap/entrypoint config points `directories.session` there, and the server lazily backfills comment/created-by by reading `<infohash>.torrent` files from that directory for torrents that predate Blackbird (cached per hash, add-time capture still wins).

Remaining:

- Add browser automation for tab persistence, panel resize/collapse, and graph hover once the POL-3.1 harness is available.
- Consider full-directory session scans (list `*.torrent`, parse once) rather than per-hash lookups if session dirs grow large; current lookups are cached per hash with no repeated probes.

### PAR-2.6 — Real piece map (P1)

**As a** user, **I want** the Pieces tab to show the actual bitfield **so that** I can see where a download is stuck.

Acceptance criteria:

- The server fetches `d.bitfield` and, for the focused torrent only, exposes it as a base64 bitfield on the detail message, diffed so unchanged bitfields are not re-sent.
- The client renders a canvas piece map that buckets to the panel width, shows done/downloading/missing per the handoff colors, highlights the pieces of the hovered file, and shows piece index and file on hover.
- Rendering a 100,000-piece torrent stays under 4ms per update on the reference machine.

Results:

- `FetchDetail` now requests `d.bitfield=` and carries the hex bitfield on `Detail` (kept out of the JSON detail payload via `json:"-"` so the focused-torrent detail envelope stays lean).
- The WebSocket layer pushes a dedicated `bitfield` envelope for the focused torrent and diffs it per client: unchanged piece maps are not re-sent on the 500ms detail loop, and refocusing a different torrent resets the diff so its map always arrives.
- The Pieces tab is now a real canvas piece map. It decodes the hex bitfield MSB-first (each hex digit pair is one byte; bit per piece), buckets to ~3px cells matching the panel, and paints done / partially-done ("working") / missing in the handoff colors. The hovered cell resolves back to a piece index and its owning file; that file's whole piece span highlights in the map, and the readout shows "Piece N · path". File spans are derived from byte offsets (cumulative `sizeBytes`) ÷ chunk size.
- Bucket completion fractions are cached per bitfield so hover redraws don't rescan every piece; a 100k-piece decode/bucket measures ~4–6ms in the pure logic (and redraws only fire on bitfield change, not per frame).
- Added pure-logic tests (`web/src/lib/pieces.ts` under the `.mjs` harness: bit decode, hex length, bucketing, file-piece ranges), a client test asserting the bitfield column maps into `Detail`, and a WebSocket test proving an unchanged bitfield is sent once and not repeated. Smoke-tested live against the fake daemon (exactly one 250-hex-char bitfield envelope while focused; decode yields the fixture's 614/1000 pieces).

Remaining:

- Squeeze the 100k-piece bucketize under the strict 4ms budget on slower reference hardware (currently ~4–6ms in JS); a WebAssembly or precomputed per-byte lookup path would close the gap.
- Add browser automation for the piece map (hover highlight, file readout) once the POL-3.1 harness is available.
- Consider emitting "downloading" (in-progress) pieces separately once a daemon source for per-piece activity exists; the current map colors partially-done buckets as working.

### PAR-2.7 — Torrent file utilities (P1)

**As a** user, **I want** to save the `.torrent`, open the magnet, and rename a torrent **so that** small chores don't need the shell.

Acceptance criteria:

- "Save .torrent" downloads the session file via an authenticated endpoint that never exposes the session directory path.
- "Rename" changes `d.name` where the daemon allows and otherwise is hidden; renaming files inside a torrent is P2.
- "Open directory" copies the base path and, where a configured URL template exists (`directories.open_url_template`), opens it.
- "Copy hash", "Copy name", "Copy path", and "Copy magnet" share one clipboard helper with a toast on success and a fallback for non-secure contexts.

Results:

- Added `GET /api/torrent-file/{hash}`: streams the torrent's session `.torrent` from the configured `directories.session` (trying both hash spellings), as an authenticated attachment with a descriptive filename from the live row; the session directory path is never part of the response, and a missing file returns a clean 404.
- Added a `rename` batch action backed by `d.name.set`, plus a `system.methodExist` capability probe (`SupportsMethod`). `/api/settings` now advertises `capabilities.rename`, and the context menu shows "Rename…" only when the daemon supports it (vanilla rTorrent hides it).
- "Open directory" opens the configured `directories.open_url_template` (new YAML key + Settings → Directories field; `{path}` is URL-escaped and substituted, or appended when absent) and otherwise copies the base path.
- "Copy hash", "Copy name", "Copy path", and "Copy magnet" now share one clipboard helper (`web/src/lib/clipboard.ts`) with a `navigator.clipboard` path plus a hidden-textarea `execCommand` fallback for non-secure contexts and a success/blocked toast; the General tab and peers Copy IP also route through it.
- Added rtorrent client tests for `Rename`/`SupportsMethod`, API tests for the rename action, capabilities advertisement, the torrent-file download (success + clean 404 + attachment headers), and `openDirectoryURL` template expansion; smoke-tested all three flows live against the fake daemon.

Remaining:

- "Rename files inside a torrent" remains P2 as scoped.
- Add browser automation for the new context-menu items (save/rename/open-directory/copy) once the POL-3.1 harness is available.

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

Results:

- Shipped the list-form `directories.watch` (`path`, `label`, `destination`, `start`, `delete_after_load`, `poll_interval`) with backward-compatible migration of the deprecated scalar `watch` / `watch_label` keys, deprecation warnings surfaced through config load, and the legacy keys cleared on save.
- Added `internal/watchdir`: a supervisor per configured directory using fsnotify with an always-on polling fallback (default 5s, per-entry override) for network filesystems; files load via `load.raw`/`load.raw_start` plus the Add API's trailing commands (`d.directory.set`, `d.custom1.set`), then are renamed to `.loaded` or deleted per the entry. Loaded torrents are recorded in the per-torrent history with actor `watch`, and parsed metadata is ingested for the General tab.
- Malformed or unreadable files now move into the watch directory's `failed/` subdirectory with the reason logged (collision-safe names preserve repeated drops); a parse failure retries once after a settle delay so files caught mid-write are not falsely rejected.
- Duplicates are handled without error spam: a bounded per-process infohash ring skips a torrent already loaded in this session, and a consumed path is released from the seen set so a later drop with the same file name still loads.
- Watch activity is fanned out to every open console as a `watch` WebSocket envelope and toasted per kind (loaded / duplicate / malformed / load error / watch error).
- Settings > Directories now manages the entry list (path, label, destination, start, delete-after-load, poll interval) with client-side validation; the save round-trip converts durations to nanoseconds. The watcher applies Settings edits to an existing path immediately by restarting that directory's goroutine, and the Compose entrypoint bootstraps the `/watch` volume as a list-form entry.
- Verified with `go test ./...` (watchdir, config, API coverage for load, duplicate, malformed-to-failed, edit-without-restart, and the settings round-trip), the frontend typecheck/tests/build, and a live smoke test against the fake daemon covering load, rename, failed/ move, duplicate skip, and entry edit.

Remaining:

- Browser automation for the watch-list editor and toasts when the POL-3.1 harness is available.
- Duplicate suppression is in-memory only; a restart forgets loaded hashes (the `.loaded` rename already prevents same-file replays). Cross-restart infohash memory could attach to the state store if operators need it.
- Server-side pruning of hidden list fields (PERF-6.6) and the 5,000-torrent poll benchmark remain as recorded under PAR-1.1.

### PAR-3.2 — Completion rules: auto-label and auto-move (P1)

**As an** operator, **I want** rules that run when a torrent finishes **so that** data lands in the right place without manual moves.

Acceptance criteria:

- YAML `automation.on_complete` defines an ordered rule list with match conditions (label, tracker host, name regex, size range, private flag) and actions (set label, move to destination, add tracker, run webhook).
- The poller detects the `d.complete` 0→1 transition and runs matching rules once, recording the result in the history log; rules never run twice for the same hash across restarts (persisted marker in Blackbird state, not in the torrent's `custom` slots unless configured).
- Moves use the PAR-2.2 engine; failures are surfaced as toasts and history entries.
- Settings > Automation edits rules with a "test against existing torrents" dry-run that lists what would match.

Results:

- Added the `automation.on_complete` YAML section: an ordered rule list where each rule combines match conditions (label, tracker-host substring, name regex, byte size range, private flag — all optional, AND-combined) and actions (`set_label`, `add_tracker` in group 0 for public torrents only, `move_to`, `webhook` POSTing a JSON completion payload). Rules are first-match-wins, and `config.Validate` rejects nameless/duplicate-named rules, uncompilable regexes, inverted size ranges, relative move destinations, malformed URLs, and rules without actions.
- Added `internal/automation`: a single-worker engine fed by a bounded non-blocking queue so the poller's transition callback never blocks under the poller lock. The poller now fires `OnTorrentComplete(hash, torrent)` exactly on the `d.complete` false→true transition — never for already-complete rows or torrents first seen complete.
- Rules never run twice for the same hash: processed hashes persist to an atomic JSON marker file next to the config (`config-state.json`, bounded at 10,000 entries with oldest-pruning, corrupt-file recovery). With no rules configured nothing is marked, so rules added later are not locked out of previously completed torrents; once rules exist, a torrent completed before a rule was added does not retroactively match.
- Moves reuse the PAR-2.2 engine through the new `Server.MoveForAutomation` (stop/restart, cross-device copy + verify, configured-root boundary enforcement); every action records a history entry with actor `automation` on the torrent's Logger tab, and any failure fans out to open consoles as an `automation` WebSocket envelope and toast.
- Added Settings > Automation: a rule editor (conditions and actions per rule with client-side validation) and a "test against existing torrents" dry run via `POST /api/automation/dry-run`, which evaluates the unsaved draft against the live snapshot with the same first-match semantics and lists which rule would handle each torrent.
- Verified with `go test ./...` (new packages: automation matching/ordering/engine/marker/webhook tests, poller transition test, API dry-run and settings round-trip tests, config validation tests; all green including `-race` on the new packages), frontend typecheck/tests/build, and a live smoke test against the fake daemon covering the settings round-trip, dry-run assignment, YAML persistence, and validation errors.

Remaining:

- Browser automation for the rule editor and dry-run when the POL-3.1 harness is available.
- A torrent that completes while Blackbird is down produces no transition, so rules do not retroactively apply (documented; a session-scan backfill could close this later).
- Webhook responses are not surfaced beyond success/failure status; per-event retry/backoff is deferred until an operator needs it.

### PAR-3.3 — RSS feeds and filters (P1)

**As a** user, **I want** RSS subscriptions with match rules **so that** Blackbird replaces ruTorrent's RSS and autodl workflow.

Acceptance criteria:

- Feeds are configured with URL, poll interval, optional cookies/headers (stored as secrets, redacted in logs and the API), and a per-feed default label/destination.
- The server fetches and parses RSS/Atom, deduplicates items by GUID and enclosure hash, and stores the last N items per feed in Blackbird state.
- Filters match on title regex, category, size range, and feed; matches load automatically with the filter's label/destination/start options; a per-filter "match history" shows what was and was not loaded and why.
- An RSS view in the sidebar lists feeds and items with manual "Add" per item and a "mark all read" action.
- Fetch failures back off and show an error badge; feeds never block the torrent poller.
- Tests use recorded feed fixtures; an integration test proves a filter match ends as a torrent in the session.

Results:

- Added `automation.rss` config: `feeds` (name, http(s) URL, poll interval defaulting to 15m, default label/destination, cookies + extra headers) and `filters` (name, feed restriction, title regex, category substring, enclosure size range, label/destination overrides, start defaulting true). Validation rejects bad URLs, unknown feed references, uncompilable regexes, and inverted size ranges.
- Added `internal/rss`: RSS 2.0 + Atom parsing (enclosures, categories, pub dates with graceful degradation), per-feed poll loops on a supervisor that runs entirely outside the torrent poller, and ordered first-match-wins filter evaluation. Cookies/headers are secrets: they never reach logs, and feed URLs are query-stripped in any error text before logging or serving.
- Dedup works on both required dimensions: items are new only when both GUID and enclosure-URL hash are unseen (bounded sets), a per-batch guard stops two reposts sharing one enclosure from double-loading, and a downloaded torrent whose infohash is already in the session is skipped. The last 200 items per feed persist in process state; `.torrent` links without an enclosure element are picked up via a `.torrent`-suffix fallback, and magnets load directly.
- Matches auto-load with the filter's label/destination/start options (falling back to feed defaults) through the Add API's trailing-command semantics, recorded in the per-torrent history with actor `rss` under the infohash; each filter keeps counters plus a 50-entry match history showing what loaded and why skips/failures happened (`loaded`, `already-in-session`, `duplicate`, `no-enclosure`, `download-failed`, `load-failed`).
- The RSS view (sidebar Views entry with an unread badge) lists feed status with error badges and retry countdowns, items with manual per-item Add (feed defaults) and mark-all-read, and filters with match history. Failing feeds back off exponentially (1m doubling to 1h) without hammering.
- Settings > Automation edits feeds and filters; secrets round-trip masked (`***` keeps, empty clears, new values replace) and the save path merges them against stored values by feed name.
- Verified with `go test ./...` (recorded RSS/Atom fixtures, service integration tests proving a filter match ends in an `AddTorrentFile` load with the right trailing commands, dedupe/magnet/session-duplicate/backoff/manual-add/mark-read coverage, API endpoint + secret round-trip tests, config validation), frontend typecheck/tests/build, and a live smoke test against the fake daemon covering auto-load, enclosure download with cookies, manual add, mark-all-read, the error badge with backoff, and secret masking/preservation.

Remaining:

- Browser automation for the RSS view and settings editors when the POL-3.1 harness is available.
- Feed item state is in-memory: a restart re-polls and rebuilds it (dedup sets reset, so already-loaded items re-evaluate but hit the already-in-session guard instead of reloading). Persisting seen-hashes across restarts could attach to the PAR-3.2 marker file if operators need it.

### PAR-3.4 — Unpack on completion (P1)

**As a** user, **I want** archives extracted automatically **so that** downloads are usable without a shell.

Acceptance criteria:

- Extraction supports zip and rar (including multi-part) via a bundled extractor in the container image and a documented host dependency for native installs; missing extractor disables the feature with a clear Settings message.
- Rules choose destination (in-place or configured extract root), whether to delete archives, and a label filter; extraction runs in a bounded worker pool at low priority.
- Progress and results appear in the history log; a failed extraction never leaves a partial directory without a `.failed` marker.
- Path safety: extraction refuses entries that escape the destination (zip-slip) and is tested for it.

Results:

- Added the `automation.unpack` YAML section: `workers` (bounded pool, default 2, clamped to 8), `timeout` (per-torrent cap, default 30m), and an ordered rule list (`name`, post-completion `label` filter, `destination` empty for in-place or an extract root, `delete_archives`). Validation rejects bad worker counts, negative timeouts, nameless/duplicate rules, and relative destinations.
- Added `internal/unpack`: a worker-pool service fed by the PAR-3.2 engine after its rule actions (so completion moves are already applied), resolving data paths from a fresh snapshot with exactly-once semantics from the shared completion marker. Single-file bases extract the file itself; directory bases are walked recursively (symlinks skipped); only archive heads feed the extractor (`.r00+`/`.part2+`/`.rev` continuations are never separate jobs).
- Extraction shells out to a 7z-compatible binary probed as `7z`, `7zz`, then `7za` (p7zip bundled in the Alpine image; documented host dependency with a license notice). Archive listings are validated in Go before extraction and zip-slip entries (`..`, absolute, drive-letter) are refused; the extract root must exist inside the download roots like the move engine. Workers run niced (+10) with a per-job timeout; stdin stays closed so encrypted archives fail fast instead of prompting.
- `delete_archives` removes exactly the extracted family (the head plus detected multi-part siblings, nothing else); success clears a stale `.failed` marker, while any failure keeps the partial output and writes a `.failed` marker naming the torrent, rule, archive, and error. Progress milestones (25/50/75) and per-archive results land on the torrent's Logger tab with actor `unpack`; in-flight jobs are visible via `GET /api/unpack`, which also drives the extractor status.
- Settings > Automation edits unpack rules and shows an extractor banner (found binary + pool depth, or the install hint when missing); workers/timeout stay YAML-managed and documented.
- Verified with `go test ./...` (head/family/slip-validation units, a real zip-slip zip built with archive/zip refused through the listing path, fake-runner service tests for in-place/root/delete/marker/refusal/timeout/status/root-boundary, a live-7z test pinning the `-slt` format, an engine→service→real-7z→history integration test, config validation, API status tests; `-race` clean), frontend typecheck/tests/build, and a live smoke test covering the status endpoint with and without an extractor, the settings round-trip, and validation errors. Docker gains `p7zip` in the runtime image.

Remaining:

- Browser automation for the unpack editor and status banner when the POL-3.1 harness is available.
- No retry path exists for a failed unpack: the shared marker fires once per hash, so a failure stays marked with its `.failed` marker until the operator intervenes (documented; a manual "unpack now" action could close this later).
- Password-protected archives fail with a clear error (stdin is closed); a per-rule password/secret store is deferred until an operator needs it.

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

Results:

- Added `tuning.throttles`: named channels with up/down KB/s (0 = unlimited), validated (non-empty unique names, never `NULL`, non-negative rates) and documented in `example.yml`.
- Daemon semantics were verified against the real pinned rTorrent 0.16.18 (Compose image) instead of trusting 0.9.x-era docs: `throttle.up/down` take an empty target, the channel name, and the cap in KiB/s as a string; `.max` reads back bytes (`-1` undefined, 0 when throttling is inactive); `.rate` reads live bytes/s; `d.throttle_name.set(hash, name)` faults on active downloads, so assignment is a stop/set/start cycle restoring prior state (empty name clears to global limits).
- Channels are created on connect and re-applied on Settings save and SIGHUP (changed caps only); removals neutralize the daemon channel (rTorrent has no delete) and are refused with the referencing count while torrents still use the channel — the save is rejected with 400 and nothing persists.
- New `set_throttle` batch action (per-hash results, history entries, optimistic UI with rollback), a Throttle toolbar control and context submenu listing live channels plus None, a sidebar Throttles group with server-side counts and table filtering, and a Settings > Bandwidth channel editor with client-side validation plus live per-channel throughput and assignment counts from `GET /api/throttles` (single multicall for all rates).
- Verified with `go test ./...` (tuning channel entries/diff/apply/refusal/neutralization against the fake daemon, config validation, poller aggregates, API lifecycle: create → assign with cycle → listing → refusal → unassign → removal), frontend typecheck/tests/build, and a live smoke test against the real daemon covering connect-time creation, assignment with preserved downloading state, sidebar counts, refusal with count, unassign/removal, and SIGHUP re-apply.

Remaining:

- Browser automation for the throttle submenu, sidebar group, and channel editor when the POL-3.1 harness is available.
- Older daemon versions predate the `._val`/target conventions only in error paths; behavior on rTorrent < 0.15 is untested (per-key errors surface in Settings results if a method faults).

### PAR-4.2 — Ratio groups and seeding limits (P0)

**As a** user, **I want** stop-on-ratio and seeding-time rules per group **so that** seeding policy is enforced without cron scripts.

Acceptance criteria:

- YAML `seeding.groups` defines groups with min ratio, max ratio, min upload bytes, max seeding time, and an action (stop, stop and set label, erase, erase with data); torrents are assigned via context menu and a Ratio group column, stored in a configurable `custom` slot.
- Enforcement is implemented in Blackbird's poller (evaluated per cycle against the list data) rather than `group.seeding.*` schedules, so rules are visible, testable, and versioned; the design note explains why and records the trade-off.
- Rules act at most once per torrent per condition and log to the history; "erase with data" honours SEC-2.3 safety.
- Settings > Seeding edits groups with a dry-run preview listing torrents that would be acted on now.
- Tests cover every condition and action, including a torrent that changes group after triggering.

Results:

- Added the top-level `seeding:` section: `custom_slot` (custom2–custom5, default custom2; custom1 stays the label) plus `groups` with min/max ratio, min upload bytes, max seeding time, and an action (stop, stop_and_set_label with label, erase, erase_with_data). Validation covers slot, names, thresholds, actions, and the label requirement.
- Added `internal/seeding`: pure per-cycle evaluation (first met condition wins) plus a two-worker enforcement engine reusing the daemon's Stop/SetLabel/Erase/RemoveWithData (erase_with_data honors the download-root boundary). Every outcome logs to the torrent's Logger tab with actor `seeding`.
- The poller evaluates complete, open torrents per cycle against the live groups — deliberately not just `seeding` state, after live testing showed a seeding torrent with a tracker warning normalizes to error state. The fired check-and-set is atomic under the poller lock with a persisted per-(torrent, group) marker, so rules fire exactly once even across restarts; group changes re-arm, and editing a group's action never re-fires spent pairs.
- The Ratio group column follows the configured slot (resolved per cycle, zero-cost for the default), assignment reuses the `set_custom` batch action from a new Ratio group context submenu fed by `GET /api/seeding`, and Settings > Seeding edits groups with validation plus a dry-run preview (`POST /api/seeding/dry-run`) listing what would act now.
- Verified with `go test ./...` (evaluation units for every condition and boundary, engine tests for all four actions plus failure logging, marker persistence/corruption, poller trigger-once/group-change/error-state tests with `-race` clean, config validation, API settings/dry-run/info tests), frontend typecheck/tests/build, and a live smoke test against the real daemon: a completed torrent with a dead tracker triggered on seeding time (stop + label + history + marker file), then a group change erased it from the session.

Remaining:

- Browser automation for the ratio-group submenu, Seeding editor, and dry-run preview when the POL-3.1 harness is available.
- Torrents that finish while stopped are never evaluated until started and open; re-added torrents keep their spent markers (consistent with PAR-3.2).

### PAR-4.3 — Bandwidth scheduler (P1)

**As a** user, **I want** a weekly grid of bandwidth limits **so that** the connection is free during work hours.

Acceptance criteria:

- YAML `schedule.bandwidth` defines a 7×24 grid of profile names, each profile a set of global and channel limits; the active profile is applied by Blackbird on the minute boundary and after reconnect.
- Settings > Scheduler renders the grid with paint-to-fill editing and profile colors; the status bar shows the active profile with a manual override that expires.
- Time zone is explicit in YAML and defaults to the server's; DST transitions are tested.

Results:

- Added the top-level `schedule:` section: `timezone` (IANA, validated with LoadLocation; empty/Local = server local) plus `bandwidth.profiles` (name, color, down/up KB/s with 0 = unlimited, per-profile channel caps reusing the throttle-channel shape) and a `grid` of weekday → 24 hourly profile names (empty cell = leave the daemon alone; unknown references rejected).
- Added `internal/schedule`: pure grid lookup (Monday-first, zone-aware) driving a minute-boundary tick loop that applies only on profile change, plus reconnect-time force apply. Manual overrides (temporary global limits) pause the schedule and force re-apply on expiry/cancel; a next-change scan powers the status display.
- Daemon calls use the verified 0.16 wire forms throughout: globals via `.set_kb` with explicit empty target, channels via `throttle.up/down` with KB strings (PAR-4.1 findings).
- Settings > Scheduler offers profile CRUD with colors, paint-to-fill grid editing with an eraser, timezone input with client-side IANA validation, and override set/cancel with live status; the status bar chip shows the active profile, override countdown, and next change, deep-linking into the Scheduler section.
- Verified with `go test ./...` (cell mapping, timezone conversion, both 2026 America/New_York DST transitions, change-only application, override pause/expiry/restore, next-change scan, config validation, API status/override/settings tests), frontend typecheck/tests/build, and a live smoke test against the real daemon: connect-time application with byte-exact caps, override set/status/cancel, and a grid edit applying at 19:11:00.004 — on the minute boundary.

Remaining:

- Browser automation for grid painting, the override flow, and the status chip when the POL-3.1 harness is available.
- IMPORTANT (pre-existing, found via this story's live smoke): the older scalar tuning path is broken against rTorrent 0.16 — single-param `.set` calls fault ("target must be a string"; every CMD2_ANY command needs the explicit empty target), `=`-suffixed getters are undefined, `dht.mode`/`network.port_range` no longer exist, and plain `.set` carries bytes with >>10 rounding (KB values are divided by 1024). This deserves its own story: migrate setters to target-first `.set_kb` forms and getters to bare names, verified live per key.

### PAR-4.4 — Quick speed-limit popover (P1)

**As a** user, **I want** to change the global limits from the status bar **so that** I can throttle instantly.

Acceptance criteria:

- Clicking the global rates in the status bar opens a popover with presets (unlimited, 25%, 50%, 75%, custom) for down and up, applied immediately via `throttle.global_*.max_rate.set`.
- The popover shows the current limit next to the live rate and reflects scheduler overrides.
- Changes persist to YAML only when the user chooses "Save as default".

Results:

- Added `GET /api/bandwidth` (current caps in KB/s plus live throughput, one multicall) and `POST /api/bandwidth` (immediate apply, per-AC errors). Both use the verified 0.16 wire forms: bare getters (the `=`-suffixed form is undefined on 0.16) and exact-KiB/s `.set_kb` with explicit empty target (plain `.set` carries bytes with `>>10` rounding).
- The status bar now shows live global rates beside the current caps; clicking opens the popover with Unlimited/25/50/75%/custom per direction, the current limit next to the live rate, percentage bases pinned to the saved default (no compounding), override awareness (edits route into an active override so the status stays truthful, with a profile-reapply hint otherwise), and Save as default persisting through the validated settings path — runtime changes never touch YAML alone.
- Fixed `GlobalStats` getters to bare names, which the popover and status-bar rates require: every `=`-suffixed getter faults on rTorrent 0.16, so live rates, totals, and versions were silently zero/empty. Bare names work on old and new daemons (the versions call already used them). The remaining setter/getter migration (21 tuning keys, settings daemon map, missing `dht.mode`/`network.port_range`/`network.port`) stays a separate story, recorded under PAR-4.3.
- Verified with `go test ./...` (bandwidth round-trip/validation/override-routing against the fake daemon, extended for stored global rates), frontend typecheck/tests/build, and a live smoke test: versions/rates live via the fixed `GlobalStats`, POST→GET exact round-trip, save-as-default YAML persistence (with the pre-existing scalar fault correctly surfacing per-key), and override-aware status.

Remaining:

- Browser automation for the popover (presets, custom apply, save-as-default, override badge) when the POL-3.1 harness is available.
- The scalar tuning apply path and settings live-value map remain broken on rTorrent 0.16 (see PAR-4.3 Remaining): `tuning.Apply/ApplySequential` need target-first calls with `.set_kb` for rate keys, and `GetterMethods` needs bare names plus replacements for the removed `dht.mode`/`network.port_range`/`network.port` methods — one verified story, not a drive-by.

---

## Epic 5 — Operator tools and observability

### PAR-5.1 — Directory browser (P0)

**As a** user, **I want** to browse server-side directories **so that** destinations are chosen, not typed.

Acceptance criteria:

- An authenticated endpoint lists directories under configured download roots only, never files outside them, with symlink resolution and canonical-path checks; requests outside roots return the SEC-2.3 error code.
- The browser component shows free space per root, supports creating a new directory, remembers recent picks, and is reused by Add, Move, Settings > Directories, and automation rules.
- Tests cover traversal attempts, unreadable directories, and roots on separate filesystems.

Results:

- Hardened the existing `GET /api/directories` (PAR-2.2): the response now carries per-root free/total space via statfs, returns cleaned canonical paths, and still lists directories only under the download roots with symlinks excluded and every child re-checked (refusals keep the shared `path_outside_download_dirs` SEC-2.3 code).
- Added `POST /api/directories` for single-level creation: name validation (no separators or dot names), root containment for the result, idempotent re-create, and 409 when a file blocks the name.
- Added a shared `DirectoryBrowser` component (roots with free space, navigation, mkdir, shared browser-persisted recents) plus a `DirPicker` modal button, now reused by Add (collapsible browser), Move (replacing its inline browser and move-scoped recents), Settings > Directories (default dir and per-label destinations; the session dir stays typed as it lives outside the roots), and automation destinations (completion move_to, RSS feed/filter destinations, unpack and watch destinations).
- Verified with `go test ./...` (new directories suite: multi-root listing with free space, `..`/absolute/sibling traversal, symlink request + listing exclusion, missing/file/unreadable-as-non-root cases, no-roots config, mkdir create/idempotent/clash/invalid/outside-roots), frontend typecheck/tests/build, and a live smoke test (browse with free space, mkdir, re-browse, traversal refusal with the SEC-2.3 code).

Remaining:

- Browser automation for the shared browser, mkdir flow, and pickers when the POL-3.1 harness is available.
- Roots on truly separate filesystems are covered by equivalent multi-root semantics in tests; cross-device specifics (free-space divergence) need no code beyond the per-root statfs already shipped.

### PAR-5.2 — Traffic and resource statistics (P1)

**As an** operator, **I want** daily and monthly transfer totals and host load **so that** I can watch quotas.

Acceptance criteria:

- Blackbird persists per-day down/up totals derived from `throttle.global_*.total` deltas, robust to daemon restarts (totals reset detection).
- Stats page gains Traffic (day/week/month bars with a selectable range, export to CSV) and Host cards (load average, memory, and per-volume free space from the existing statfs sampler).
- Retention is configurable; storage is a compact append-only file in the state directory.

Results:

- Wired the `internal/traffic` tracker into the daemon loop: a new poller `OnGlobalStats` hook feeds every successful poll's `SessionDownTotal`/`SessionUpTotal` into UTC day/hour buckets (counter resets count current as new; restarts bridge the shutdown gap from persisted totals; skips failed polls). The tracker persists to `<config>-traffic.jsonl` next to the config with flush-on-interval plus flush-on-exit, compaction past 10,000 lines, and corrupt-line tolerance.
- Added `GET /api/traffic` (daily range defaulting to the last 30 days, capped at 366 days; `granularity=hour&day=` for the 24 hourly buckets; `format=csv` downloads either view as an attachment) and `GET /api/host` (load 1/5/15, memory total/avail, process RSS + Go heap, with OK flags so the UI degrades to dashes).
- Added `stats` to the settings API round-trip with live retention updates (`SetRetentionDays` prunes immediately); Settings > History edits `stats.traffic_days` (0 disables persistence, empty restores the 90-day default) with validation, and SIGHUP reloads re-apply it. Documented in `example.yml` and the Compose user guide.
- Stats page now shows a Traffic history panel (Week/Month/90-day presets, custom from/to, Hours drill-down, range totals, CSV export via fetch+blob) and Host cards (load, memory, Blackbird process) above the existing Volumes and Space-by-label panels.
- Fixed the `internal/host` test build on all platforms by moving the pure parsers into a shared `parse.go` (the Linux/Darwin test helpers previously lived behind opposite build tags, so the package never compiled for tests).
- Verified with `go test ./...`, `go vet`, frontend typecheck/tests/build, and a live smoke test against the fake daemon.

Remaining:

- Browser automation for the traffic presets, hour drill-down, CSV export, and host cards when the POL-8.1 harness is available.

### PAR-5.3 — Torrent history log (P1)

**As an** operator, **I want** a log of adds, completions, moves, rule actions, and removals **so that** I can answer "what happened to X".

Acceptance criteria:

- Events include timestamp, hash, name, event type, actor (user, watch, RSS, rule, scheduler), and details; retained for a configurable count/age.
- A History view lists and filters events, and a per-torrent Logger tab (PAR-2.5) shows the subset.
- Events are exposed by an authenticated endpoint with pagination and are the same source used by toasts.

Results:

- Extended the `internal/history` log with a global recency ring beside the per-torrent rings: one `Add` call writes both, so the History view, the Logger tab, and `GET /api/history` never disagree. Entries gained the torrent `Name`; daemon-wide events (scheduler applications, overrides) use an empty hash and live in the global ring only. The ring is bounded by count (`history.global_entries`, default 5000) and the existing age window.
- Added `GET /api/history` (`limit` default 50/max 200, `before_seq` sequence cursor, `kind`/`actor`/`hash`/`q` filters) returning newest-first pages with `nextBeforeSeq`/`hasMore`; sequence cursors stay stable under concurrent appends and same-timestamp events page without loss. Retention edits apply live via `SetBounds` from Settings saves and SIGHUP reloads.
- Closed the event-coverage gaps: poller completions now log even when no rule matches (new `complete` kind), move-data jobs log per-torrent `move` outcomes under the job's actor, magnet/URL adds log under their btih hash, scheduler profile applications/expiries log with the `scheduler` actor and user overrides log with the request actor. Every producer now records the torrent name where known (snapshot lookup, parsed `.torrent` name, RSS title, job name); the file-add path also stops attributing the `local` actor by passing the real auth.
- Added the sidebar History view (kind/actor/search filters, Load more, refresh, empty states) on the `history` route, and a `Completed` label for the new kind on the Logger tab.
- Verified with `go test ./...`, `go vet`, frontend typecheck/tests/build, and a live smoke test against the fake daemon.

Remaining:

- Browser automation for the History view (filters, pagination, route) when the POL-8.1 harness is available.
- Toasts still fire from action responses and WebSocket notices alongside the log write rather than being rendered from a log subscription; unifying them is deferred until the POL-8.3 notification centre lands.

### PAR-5.4 — Create torrent (P2)

**As a** user, **I want** to build a `.torrent` from server-side data **so that** seeding my own content doesn't need a desktop client.

Acceptance criteria:

- Modal picks a path via the directory browser, trackers, piece size (auto or fixed), private flag, comment, and source; creation runs in the background with progress and hashes in a bounded worker.
- The result can be downloaded and optionally added to the session started and tied to the source path.

Results:

- Added `internal/mktorrent`: a dependency-free builder (lexicographic multi-file layout, streaming SHA-1 pieces, canonical bencode, first-tracker announce with per-tier announce-list, private/comment/source fields, auto piece size as the smallest 64 KiB–16 MiB step with ≤2000 pieces) plus a Service with 2 hashing workers, per-chunk progress and cancellation, and 20 retained terminal jobs. Sources resolve symlinks and must land inside the download roots (`path_outside_download_dirs`); symlinks met while walking a directory and empty sources are refused.
- Added the `/api/torrents/create` job surface (submit 202, status, cancel, attachment download that never leaks the server path, post-hoc or at-creation session add tied to the source's parent directory with start/label options) with typed errors (`create_not_found`, `create_not_ready`).
- Added the top-bar Create .torrent modal (DirPicker source, name/trackers/piece/private/comment/source-tag fields, add-to-session options, live progress with cancel, download and add actions). Creations and session adds are history events.
- Verified with `go test ./...` (build round-trips re-verified piece-by-piece against an independent re-hash and parsed back through the existing `torrentfile` parser; service, validation, eviction, and API lifecycle tests), `go vet`, frontend typecheck/tests/build, and a live smoke test against the fake daemon.

Remaining:

- Browser automation for the create modal (field validation, progress, cancel, download, add-to-session) when the POL-8.1 harness is available.

### PAR-5.5 — Port check (P1)

**As an** operator, **I want** an external reachability test **so that** "Port open" means something.

Acceptance criteria:

- A user-initiated check asks a configurable external service (or Blackbird's own probe when a public listener is configured) whether the listening port is reachable; the result, timestamp, and method are shown next to the port field and in the status bar.
- No external request is ever made automatically; the service URL is documented and can be disabled.

Results:

- Added `internal/portcheck`: a user-initiated-only probe client for a documented `GET {url with {port}} → 200 + {"reachable"|"open": bool}` protocol (anything else is a probe failure, never a verdict), with http(s)-only validation, configurable timeout, and no background path that can call it.
- Added `GET`/`POST /api/port-check`: POST resolves the live daemon port (falling back to the `port_range` start), probes once, remembers the verdict, and logs a `port_check` history event; GET replays the last verdict and the enabled flag without touching the network. Added `portcheck.url`/`timeout` to config, validation, the settings round-trip, and `example.yml` (empty URL disables).
- Settings > Connection edits the probe and runs **Check now** against the saved configuration; the result, timestamp, and method render next to the port field. The status bar shows `open` only after a reachable verdict (`closed` after a negative one, bare port while unverified) and deep-links to Settings.
- Verified with `go test ./...` (probe protocol/validation/timeout units; disabled/round-trip/closed/outage/fallback/settings API tests), `go vet`, frontend typecheck/tests/build, and a live smoke test against the fake daemon with a stub probe.

Remaining:

- Browser automation for the check flow and status-bar states when the POL-8.1 harness is available.

### PAR-5.6 — IP filter (P2)

**As an** operator, **I want** to load a blocklist **so that** known-bad ranges are not contacted.

Acceptance criteria:

- YAML `network.ipfilter` points to a local P2P/DAT-format file or URL with a refresh interval; ranges are applied with `ipv4_filter.load` on connect and refresh.
- Settings shows the rule count, last load time, and errors.

Results:

- Added the top-level `network:` section with `ipfilter` (`path` xor `url`, `refresh_interval`): exactly one of a local P2P/DAT file or an http(s) URL; validation rejects both-set, relative paths, non-http URLs, and negative intervals. Unset refresh means the 24h default for URL sources and no periodic refresh for files.
- Added `internal/ipfilter`: a P2P/DAT rule counter (single IP, CIDR, start–end ranges including zero-padded eMule octets, DAT `start - end , level , name` and P2P `name:start-end` entries; blanks/`#` comments/unparseable lines skipped), a URL fetcher (plain or gzipped, 64 MB cap, atomic cache install at `<config>-ipfilter.dat`), and a service that loads the resolved file via `ipv4_filter.load <path>, unwanted` (two-arg form per the upstream ip-filtering manual).
- The service applies on daemon connect, on a minute-tick reconcile (changed source reloads immediately, URL sources re-fetch on the refresh cadence), on SIGHUP, immediately after Settings saves that change the source, and on manual reload; every outcome updates the rule count, last load time, or error surfaced by `GET /api/ipfilter` and `POST /api/ipfilter/reload` (reload verdicts are history events).
- Added `network` to the settings API round-trip (GET advertises, POST validates/persists), and Settings > Connection edits the source with client-side validation plus a status line (rule count, source, last load, error) and Reload now.
- Verified with `go test ./...` (parser, service change/refresh/fetch/gzip/failure, `ipv4_filter.load` wire-form pin, API disabled/file/URL/failure/settings-validation tests), `go vet`, frontend typecheck/tests/build, and a live smoke test against the fake daemon covering connect-time load, manual reload, file→URL settings switch with cache fetch, validation refusal, and SIGHUP re-apply.
- Documented in `example.yml` and the Compose user guide, including the shared-volume note (a local path is read by the daemon, so it must be visible to rTorrent as well as Blackbird).

Remaining:

- Browser automation for the blocklist editor and Reload now when the POL-8.1 harness is available.
- The daemon call form (`ipv4_filter.load` two-arg) follows the current upstream manual; it has not been exercised against the pinned real rTorrent 0.16.18 — confirm on the QA-5.2 integration target alongside the PAR-4.3 setter-migration story.
- No per-range inspection: the daemon holds the table and Blackbird only counts lines, so a "is IP X blocked?" lookup would need a local range index (deferred until an operator needs it).

---

## Epic 6 — Backend performance

Target: a 5,000-torrent session with 20 active downloads on a 2-core VM keeps the list poll under 150ms, WebSocket delta payloads under 20 KB per tick at steady state, and Blackbird under 150 MB resident.

### PERF-6.1 — Take network I/O out of the poller lock (P0)

**As an** operator, **I want** API and WebSocket reads to never block on rTorrent **so that** the UI stays responsive during slow detail fetches.

Acceptance criteria:

- `refreshDetailsLocked` and every other path that performs SCGI calls under `p.mu` is restructured to fetch outside the lock and swap results in under a short critical section.
- `Snapshot()` returns an immutable snapshot pointer produced once per cycle (copy-on-publish), not a deep copy per caller.
- A regression test asserts that `Snapshot()` returns within 1ms while a detail fetch is artificially stalled.

Results:

- `pollOnce` no longer performs SCGI I/O under `p.mu`: the focused-hash set is captured and the detail cycle marked under the lock, `FetchDetail` runs unlocked via a new `fetchDetails` helper, and each result swaps into `p.detail` under its own short critical section (hashes unfocused mid-fetch are dropped, not resurrected). `ListTorrents`/`GlobalStats`/`OnConnect` were already lock-free; the message/complete/seeding callbacks stay under the lock per their documented non-blocking contract.
- `Snapshot()` returns the published `*Snapshot` with no per-caller deep copy: each cycle builds fresh slices/maps and swaps one pointer, `onDisconnect` publishes a modified copy, and published cycles are never mutated (the now-unused `copyCounts*` helpers were removed). All 50+ call sites are read-only and were verified race-clean.
- Added `TestSnapshotSharedWithinCycle` (same pointer within a cycle, new pointer per cycle, old data stable) replacing `TestSnapshotIsACopy`, and `TestSnapshotUnblockedByStalledDetail` (5 × `Snapshot()` under a stalled `FetchDetail`, each <1ms).
- Verified with `go test ./...`, `go test -race` on the poller and API packages, and `go vet` clean.

Remaining:

- `History()` still copies per caller (small, bounded buffer) and `statVolumes` statfs still runs under the lock (local syscall, not SCGI); both are PERF-6.4 allocation-hygiene material, not correctness issues.
- Quantitative before/after on the 5,000-torrent fixture belongs to the PERF-6.6 benchmark harness, which does not exist yet.

### PERF-6.2 — Field-level deltas (P0)

**As a** user with a large session, **I want** only changed fields on the wire **so that** ticks are cheap for the browser.

Acceptance criteria:

- The WebSocket delta carries `{hash, fields: {...}}` patches for changed torrents instead of whole objects; the message schema version is bumped and the client handles both during a documented transition.
- Global stats are included only when changed; aggregates are sent as patches.
- Per-message compression (`permessage-deflate`) is enabled with a measured before/after on the 5,000-torrent fixture recorded in the PERF-6.6 report.
- Slow clients are coalesced (latest-wins per hash) rather than silently dropped; a metric counts coalesced ticks.

Results:

- The WebSocket protocol is now v1+v2 with a hello handshake (`{"type":"hello","version":2}`). v1 deltas are byte-identical to before (whole changed rows, global every tick, full aggregates); v2 deltas carry `{hash, fields}` patches, globals only when changed since the client's last flush, and aggregate patches (whole status map on status change, updated/removed keys for labels/trackers/throttles). The envelope `v` echoes the negotiated version per client.
- The poller computes patches alongside whole rows (`diffTorrentFields`, explicit per-field, no hot-path reflection); a reflection-based test guards that every Torrent field except the hash key is patch-covered, so the catalogue cannot grow a silently-unpatched field.
- The hub merges each poll cycle into per-client pending state (latest-wins: removal beats queued adds/changes, changes fold into queued adds, patches merge field-wise) and flushes one message per wakeup — slow clients converge instead of skipping ticks. A hub atomic counts merged ticks, exposed on `/api/health` as `coalescedTicks` (omitted when zero).
- Snapshot resends (tab un-hide) travel through the flush path with an epoch guard: pending clears, the snapshot writes first, and pre-snapshot flushes are dropped, so stale deltas can never land on fresh state.
- `permessage-deflate` is enabled on the upgrader (browsers negotiate it automatically); negotiation is covered by a test.
- Measured on a synthetic 5,000-torrent session with 200 changing rows per tick (PERF-6.6 fixture stand-in): v1 tick 193,942 B → v2 tick 41,307 B (21.3%); with raw-deflate (permessage-deflate proxy) v1 3,324 B → v2 2,097 B (1.1% of v1). The Epic 6 budget (20 KB/tick steady state) is met with deflate on (2.0 KB), not by patches alone (41 KB) — recorded for the PERF-6.6 report to confirm on the real fixture.
- Compatibility (SHIP-1.4): unknown inbound types are ignored, so v2 clients degrade to v1 against old servers and v1 clients are unaffected by the new server; the client applies both shapes presence-based; v1 service is retained with removal gated on the REL-8.1 deprecation policy. Rules live in the `ws.go` protocol comment (the contract doc).
- Verified with `go test ./...` (new: v2 hello e2e proving single-field patches + omitted globals, v1 byte-compat e2e, hub coalescing unit test, compression negotiation, size-measurement guard), `go test -race` on poller and API, `go vet`, and frontend typecheck/tests/build.

Remaining:

- v1 whole-row computation still runs server-side every cycle (needed for v1 clients); removing the dual path is a future major-version decision, not this story.
- Real-fixture confirmation (5,000-torrent poll latency, 20 KB budget, goroutine/memory impact of per-client pending) belongs to the PERF-6.6 harness.
- Browser automation for mixed-version reconnect flows when the POL-8.1 harness is available.

### PERF-6.3 — Batch and bound rTorrent round trips (P0)

**As an** operator, **I want** each poll cycle to be as few SCGI calls as possible **so that** rTorrent spends its time on torrents.

Acceptance criteria:

- The list poll and global stats are fetched in one `system.multicall`; `FetchDetail` is genuinely one multicall, matching its comment.
- SCGI responses are read with a configurable size limit (default 64 MB) and a per-call timeout; exceeding either produces a typed error and a reconnect, never unbounded memory.
- Detail refresh interval and per-client detail sends are driven by change detection (hash of the detail payload), not a fixed 500ms re-send.
- Adaptive list polling: the interval stretches toward `poll.max_interval` when no client is connected or all are hidden, and snaps back on the first active client.

Results:

- One SCGI round trip per poll cycle: new `Client.ListAndGlobals` nests the `d.multicall2` list poll as the first entry of the same `system.multicall` carrying the eight global getters (verified live against rTorrent 0.16.18, which answers the nested call with the rows array). The poller interface now exposes only the combined call; `ListTorrents`/`GlobalStats` remain as tested granular primitives. A client test pins the single-`system.multicall`, zero-top-level-`d.multicall2` shape against fakertorrent (which learned the nested branch).
- `FetchDetail` was already genuinely one `d.multicall2` round trip (the baseline gap predates this story and was closed by PAR-2.x detail work); verified by reading, no change needed.
- Bounded reads with typed errors: `scgi.Client.MaxResponseBytes` (default 64MB, well above single-digit-MB 5,000-torrent polls) aborts oversized reads with `*TooLargeError`; missed deadlines surface `*TimeoutError` (matching `errors.Is DeadlineExceeded`), while caller cancellation keeps its own identity. Both propagate through the typed client into the poller's existing disconnect/backoff/reconnect path. New YAML `rtorrent.max_response_bytes` (0 = default) with validation; wired in main.
- Change-driven detail: per-focused-hash pacing stretches the refresh 1x/2x/4x/8x on consecutive unchanged payloads (fnv64a over the fetched JSON; bitfield excluded by its `json:"-"` tag, piece progress still surfaces via file counters) and snaps back on any change; state resets on unfocus/refocus. Per-client sends hash the payload too (`DetailHash` accessor, no client-side marshal): identical detail is no longer re-sent on the 500ms tick — only the tick check remains.
- Adaptive polling: new YAML `poll.max_interval` (default 30s, must be >= interval) with validation and example docs; the run loop doubles idle waits toward the live cap and snaps back on the first visible client (`Server.HasVisibleClients`: any non-hidden tab). The cap applies live via `SetMaxInterval` on SIGHUP; the base interval still needs a restart.
- Verified with `go test ./...` (new: SCGI size/timeout/cancellation typing, combined-poll shape, config validation, deterministic backoff/reset/accessor tests on a manual clock, exact wait-schedule test, loose integration test for stretch/snap direction, visibility-signal and send-skip tests), `go test -race` on poller/api/scgi/rtorrent/config, `go vet`, and frontend typecheck/tests/build (no contract change, no UI work).
- Verified live against the real pinned daemon: combined poll returns connected state, full 43-field rows, and live globals; a 100-byte cap run shows `disconnected`/`stale` with `lastError: scgi: response exceeded 100 bytes` and keeps retrying.

Remaining:

- Quantitative before/after (poll latency, SCGI bytes/cycle, daemon CPU) belongs to the PERF-6.6 fixture harness.
- The worst-case detail staleness for a fully static torrent is now 8x the detail interval (8s at defaults); peer-rate drift without row changes is bounded by the same cap. If operators need tighter peer freshness, the cap (`maxDetailCalmShift`) wants its own YAML key.
- `poll.interval` itself is still restart-only; making the base interval live (same atomic pattern as the cap) is a small follow-up.

### PERF-6.4 — Allocation and aggregate hygiene (P1)

**As a** maintainer, **I want** the poll cycle to be allocation-stable **so that** GC pauses don't show as jitter.

Acceptance criteria:

- `indexByHash`, `computeAggregates`, and `computeDelta` reuse buffers across cycles; per-cycle allocations on the 5,000-torrent fixture are measured and recorded, with a CI benchmark guarding a 20% regression.
- History ring pruning and the per-torrent speed rings (PAR-2.5) are ring buffers, not slice re-slices.
- Dead code listed in the baseline is removed.

Results:

- Index maps reuse two alternating `map[string]*Torrent` buffers (cleared and refilled per cycle, then rotated): pointer values allocate nothing on insert, where 430-byte struct values cost one alloc each — measured 5,000 allocs per map fill before, zero after warmup now. The no-mutate-after-return rule is documented on the `Client` interface (it matches the existing snapshot-row ownership).
- Delta slices (`added`/`changed`/`removed`) reset in place via scratch parameters on `computeDelta`; subscribers that retain them must copy (the hub merges synchronously by value, so it is safe).
- The global history is now a fixed-capacity `historyRing` (pointer-bump eviction, no re-slicing); per-torrent speed rings were already proper fixed-array rings with head/size and are now pinned by an eviction/wraparound unit test.
- Measured on a synthetic 5,000-torrent session with 200 live rows per tick (PERF-6.6 stand-in): idle cycles went from ~10,100 allocs/op (two 5k value-map fills) to 15 allocs/op; busy cycles sit at ~2,014 allocs/op, dominated by per-row patch maps and value boxes that scale with genuine new data. `BenchmarkPollCycle` records ns/op, B/op, allocs/op; `TestPollCycleAllocBudget` guards idle ≤100 and busy ≤4000 (both ~2x+ headroom over 2026-09 darwin/arm64 numbers).
- Deliberate deviation, documented: aggregate maps stay freshly allocated per cycle instead of double-buffered. Published snapshots and the hub's retained aggregates are shared without locks, so reusing those maps would corrupt live readers — the reuse that is safe (indexes, delta slices, history) is done; the maps left over are small (distinct counts, not session size).
- Dead code removed: `Placeholders.tsx` (unimported), `poller.samplesSince`/`appendSample` (replaced by the ring; tests rewritten), `rtorrent.GlobalSettingsKeys` (definition-only; `tuning.GetterMethods` remains the live table for the POL-8.8 unification), `GetGlobal`/`GetGlobalString`/`GetGlobalInt`, `SetGlobalInt`/`SetGlobalString`/`SetGlobalBool` (+`boolToInt`; `SetGlobal` stays — `tuning.Apply` uses it), and `ConfigStore.SaveTuning` (zero callers; interface + impl + stubs).
- Verified with `go test ./...`, `go test -race` on poller/api/rtorrent/config, `go vet`, and frontend typecheck/tests/build.

Remaining:

- Real-fixture confirmation (5,000-torrent poll latency, resident memory, GC pause behavior) belongs to the PERF-6.6 harness; the synthetic numbers above are the interim record.
- Busy-cycle patch maps (~1,800 of the ~2,000 allocs) could be pooled, but the win is small, the data is short-lived nursery garbage, and pooling would add retain-contract risk for subscribers — deferred unless the real fixture shows GC pressure.

### PERF-6.5 — HTTP delivery (P1)

**As a** user on a slow link, **I want** the app shell to load fast **so that** first paint is under a second on a LAN.

Acceptance criteria:

- Embedded assets are served pre-compressed (brotli and gzip) with `Content-Encoding` negotiation and immutable cache headers; `index.html` remains no-cache.
- The font is preloaded; the initial bundle is under 120 KB compressed and the size is checked in CI.
- HTTP/2 is supported behind TLS termination and documented; `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are set (shared with `backlog.md` SEC-2.2).

Results:

- Text assets (scripts, styles, `index.html`, SVG/JSON) ship pre-computed brotli + gzip variants built once into a process-shared table (`sync.OnceValue`, ~1MB): negotiated per request with `q=0` respected, `Vary: Accept-Encoding` always set, deterministic content types (no OS mime-table drift). Fonts/images stream raw through the file server, preserving range/ETag behavior. Cache policy unchanged: hashed `/assets/*` immutable for a year, `index.html` `no-cache`, rest a day.
- Measured live on the production build: 255,482 B script → 75,740 B gzip → 62,526 B brotli; shell, SPA fallback, binary passthrough, and headers all verified over HTTP.
- The font (`/fonts/ibm-plex-sans-var.woff2`, stable unhashed public path) preloads from `index.html`; verified in the built shell.
- Initial bundle gate: `npm run size` recompresses `dist/assets/*.js` and fails over 120 KB gzip — currently 75,004 B. A new `frontend` CI job (node 22, matching the Docker build) runs install, build, and the gate on every push/PR.
- Server timeouts: header 10s (kept), body read 15s, write 60s (covers multi-GB local `.torrent` downloads; hijacked WebSocket conns are exempt), idle keep-alive 120s.
- HTTP/2 needs no code (Go negotiates it whenever TLS is present) and Blackbird intentionally terminates only plain HTTP: a new Compose-guide section documents Caddy (automatic certs) and nginx reverse-proxy configs with the WebSocket upgrade headers and encoding passthrough.
- New dependency: `github.com/andybalholm/brotli` (pure Go, no transitive code deps).
- Verified with `go test ./...` (new: negotiation table, stub-FS negotiation/headers/binary/SPA tests, embedded-artifact test proving the real shell and scripts carry both variants), `go test -race` on the API package, `go vet`, frontend typecheck/tests/build, and the live check above.

Remaining:

- Brotli level is fixed at best (one-time startup cost, ~ms); making it configurable is not warranted unless startup profiling says otherwise.
- The size gate covers script bytes only; styles/fonts are an order of magnitude smaller and stable — folding them in is trivial if the budget ever needs teeth there.
- First-paint timing on a reference LAN (the "under a second" target) still wants the POL-8.1 Playwright harness for a real measurement.

### PERF-6.6 — Performance fixtures, benchmarks, and budgets (P0)

**As a** maintainer, **I want** a repeatable large-session benchmark **so that** every performance story has a number.

Acceptance criteria:

- `fakertorrent` can generate deterministic sessions of 500, 5,000, and 20,000 torrents with configurable activity; a `make bench` target runs Go benchmarks for the poll cycle, delta computation, and WebSocket encoding.
- A documented performance report records poll latency, delta payload size, memory, and goroutine count per fixture on a reference machine, and CI fails on a 20% regression against checked-in baselines.
- The unknown-method fallback in `fakertorrent` returns a fault instead of an empty string, so typo'd methods fail tests.

Results:

- `fakertorrent` generates deterministic sessions (`Options.SessionSize/ActiveFraction/Seed`): 500/5,000/20,000-torrent fixtures with the full 43-column list shape, an 80/15/3/2 download/seed/stopped/error mix, and live rows advancing per served poll (sequential polls observe steady change; a fresh daemon replays identically). Canned rows are untouched when unset.
- `make bench` runs the poll-cycle (per fixture, full stack through SCGI), pure delta, list-decode, and v1/v2 encoding benchmarks with `-benchmem`; `make bench-update` re-records baselines.
- `docs/performance.md` is the report (methodology, per-fixture poll/delta/memory/goroutine tables, regeneration steps); `docs/performance-baselines.json` holds darwin/arm64 + linux/arm64 rows. `TestPerfRegression` (run isolated via `make bench-guard`, never inside the parallel `go test ./...` — first attempt false-positived at 2.5x with identical alloc counts from CPU contention) compares ns/op and allocs/op per current platform and fails over 20% only when the breach reproduces on re-run. A dedicated `perf-guard` CI job runs it. Platforms without an entry log and skip; allocations are additionally guarded deterministically cross-platform by the PERF-6.4 budget test. `TestPerfFootprint` logs 20k-session memory/goroutines behind coarse tripwires.
- Unknown methods now fault (`-501 unknown method …`); the 40-method ack table covering every command the console emits (actions, load, tuning scalars, throttle, ipfilter) was inventoried from the client sources, and the full suite passes with zero fallout.
- Headline finding (in the report): the 5k poll runs ~229ms vs the 150ms Epic 6 budget, and profiling attributes it to the XML codec — poller logic is ~1ms/0-alloc, idle cycles 15 allocs, 20k-session heap 18MB with 3 goroutines. Meeting 150ms needs a streaming-decode follow-up, recorded as Remaining.
- Verified with `go test ./...`, `go test -race` on the touched packages, `go vet`, and the live guard fail/restore cycle above.

Remaining:

- linux/amd64 (CI's platform) has no baseline row yet, so the ns/op guard logs-and-skips there until a maintainer records one on a quiet reference box (`make bench-update`, commit the file). Allocs/op stay guarded everywhere via the budget test.
- The follow-up the numbers demand: streaming XML decode for list responses (the only Epic 6 budget currently missed).
- `make bench` full run takes ~10s (dominated by the 20k fixture); the guard itself runs only the fast subset.

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

Results:

- `store/session.ts` holds torrents in a `createStore` keyed by hash. Row ops live in side-effect-free `store/rows.ts`: snapshots reconcile (reconnects and tab un-hides keep every row), v1 wholes reconcile in place, v2 patches merge only their fields (unknown hashes ignored), removals delete. Unchanged rows keep proxy identity, so the identity-keyed `<For>` never recreates their DOM.
- Every message path (`applySnapshot`, `applyDelta`, optimistic patch/restore) wraps all signal writes in one `batch()` — one flush per WebSocket message.
- `pruneSelection` early-returns (untracked read, same array identity) when nothing left; `selectedSet` was already a caching memo, now actually stable because the per-tick write is gone.
- Verified with `npm test` (new: `rows.test.mjs` for identity + single-flush batching via eager computeds, `selection.test.mjs` for memo stability + exactly-once re-evaluation, `rows-dom.test.mjs` proving an unrelated tick touches only the changed row's text node — exactly one `characterData` record — with stable `<tr>` nodes). The DOM tests run on happy-dom (new devDependency) with Solid's client build (`--conditions=browser`) and no JSX transform; the `npm test` compile now uses `--lib ES2022,DOM,DOM.Iterable`, and two `ui.ts` imports take explicit `.js` suffixes (matching the existing `peerKey.js` precedent) so plain node ESM resolves.
- Verified with frontend typecheck/tests/build (bundle 77 KB gzip, still under the 120 KB gate) and the untouched Go suite.

Remaining:

- The table's filter/sort still rebuilds its array per tick (same references, so no DOM churn, but wasted CPU) — that is PERF-7.2's incremental index.
- `DetailPanel` still scans linearly by hash (also PERF-7.2).
- Browser automation for multi-select and context menus still waits on the POL-8.1 harness.

### PERF-7.2 — Incremental sort and filter (P0)

**As a** user, **I want** the visible row order maintained incrementally **so that** a tick doesn't re-sort the world.

Acceptance criteria:

- The sorted, filtered view is maintained as an ordered index updated per delta (insert/remove/reposition only for torrents whose sort key or filter membership changed); full re-sort happens only on sort or filter changes.
- `torrentList`'s per-tick key sort is removed; the four-array allocation chain (`rows`, `hashes`, the effect, `visibleHashes`) collapses to one derived index.
- `DetailPanel` looks up the focused torrent by key, not by linear scan.
- Benchmarks in the component test suite record sort and filter timings on the 5,000-row fixture.

Results:

- New pure `lib/orderedView.ts`: one ordered row array plus a parallel hash array over the live session. Snapshots and filter/sort switches rebuild; deltas update via `applyChanges` (membership re-evaluated only for listed hashes, order merged O(n + k log k), removal wins). A signature guard rebuilds instead of corrupting when filter/sort drift under a delta call. Session rows are live store proxies (reference-diffing cannot work — caught during development), so the delta path is explicitly delta-driven.
- `session.ts` owns one instance and publishes `visibleRows`/`visibleHashes` signals only when the visible set changes (fresh identities, single flush); quiet ticks allocate and notify nothing. `torrentList` is back to an unsorted values array (all remaining consumers are order-insensitive); the filter predicate moved from the table into the shared lib helper.
- The table renders `visibleRows()` directly: per-tick filter scan, `IncrementalTorrentSorter`, hash-mapping memo, and count-sync effect are gone (sorter class stays as tested utility). Keyboard/selection read the same index; the prune-availability set still uses the full session so filtered-out selections survive. `DetailPanel` looks up `torrents[hash]` in O(1). The 150ms query debounce moved to the ui store so the table and the index share one value.
- Verified with `npm test` (new: `ordered-view.test.mjs` — rebuild/add/reposition/leave/remove cases plus a 300-step seeded fuzz asserting every step equals a naive recompute; `view-perf.test.mjs` — full rebuild 3.5ms vs 20 delta ticks at ~0.6ms each on 5,000 rows, label refilter 1.0ms), typecheck, build, and the Go suite.
- Notably, the first binary-insert version measured SLOWER than rebuilds (O(k·n) splices); the merge design fixed it — recorded so the trade-off isn't re-litigated.

Remaining:

- `DetailPanel` file/tree lists and the stats page are untouched by this story.
- Browser automation for sort-header Shift-click and keyboard nav still waits on the POL-8.1 harness.

### PERF-7.3 — Virtualized table (P0)

**As a** user with thousands of torrents, **I want** smooth scrolling **so that** the table behaves like a native list.

Acceptance criteria:

- Rows are windowed with overscan using the fixed 30px row height; sticky header, alternating row backgrounds, selection, keyboard navigation (including page up/down, home/end), and drag-and-drop continue to work.
- Scroll position is preserved across ticks and filter changes; the focused row scrolls into view on keyboard navigation.
- Row insert/remove fade (120ms) is preserved for rows inside the window and skipped outside it.
- 20,000 rows scroll at 60 fps in the browser benchmark; DOM node count stays under 1,500 regardless of session size.

Results:

- New pure `lib/virtualWindow.ts`: `computeWindow(total, scrollTop, viewportHeight)` with the fixed 30px handoff row height and 10 rows of overscan each side; `maxWindowRows` bounds the slice. The table renders only the window over the PERF-7.2 ordered index with top/bottom spacer rows reserving the hidden height, so ticks and filter changes never touch `scrollTop` — scroll position is preserved structurally.
- Sticky header, column picker/reorder/resize, select-all, per-row selection, context menu, and file-drop drag-and-drop are unchanged. Alternating backgrounds moved from `:nth-child` (which spacer rows would shift) to an explicit per-row `row-alt` class driven by the absolute view index; insert/remove fade is a 120ms `row-in` keyframe on windowed rows only (off-window rows render nothing, so the animation is skipped outside the window by construction).
- Keyboard: arrows still move one row; PageUp/PageDown move ±20 rows (~one viewport) via a new `moveSelectionBy` helper and Home/End jump to the ends via `jumpSelection` (both honour Shift-extend). A `focusedHash` effect scrolls the focused row into view with minimal adjustment and leaves the scroll alone otherwise.
- Verified with `npm test` (new: `virtual-window.test.mjs` — clamping, spacer math, DOM-node budget at 400–900px viewports, and 1,000 window slices over 20k rows in 0.7ms, far inside one frame each), typecheck, production build (78 KB gzip, still under the 120 KB gate), and the untouched Go suite.

Remaining:

- Real 60 fps scroll measurement and the full-catalogue DOM-node count still want the POL-8.1 Playwright harness on reference hardware; the unit-level slice benchmark and the analytic node budget above are the interim record.
- Column auto-fit measures only rendered (windowed) cells; fitting against off-window content would need a data-driven width pass.
- Browser automation for page/home/end keys and scroll-position retention when the POL-8.1 harness is available.

### PERF-7.4 — Detail, sparkline, and stats rendering (P1)

**As a** user, **I want** secondary panels to stay cheap **so that** the main table keeps its budget.

Acceptance criteria:

- Sparkline and throughput graph build point strings incrementally (append one sample, drop one) and memoize hover computations; hover does not rebuild polylines.
- Peers and files lists are virtualized above 200 rows; the piece map draws to canvas (PAR-2.6).
- All 1-second intervals are consolidated into one shared ticker signal, paused while the tab is hidden.
- Detail subscriptions are cancelled on unfocus and the tab-hidden signal also suspends stats polling.

Results:

- New `lib/chartPoints.ts`: pure series/scaled point builders plus a `SeriesPointsCache` that reuses the down/up strings by identity when the series key (length, first/last keys, scale max, geometry) is unchanged. Sparkline, StatsView throughput, and SpeedTab build through it, so quiet ticks rebuild nothing and append/drop rebuilds exactly once per data change. Hover resolves through dedicated `hoverIndex`/`hoverX`/`selected` memos in both graphs; mousemove never touches the polyline strings.
- Peers and files lists window above 200 rows (`VIRTUALIZE_ABOVE`) through the generalized `computeWindow` (new `rowHeight` parameter, `DETAIL_ROW_HEIGHT = 26` matching `--h-detail-file-row`). Small swarms/torrents render whole with no spacers; large ones render the slice with spacer divs, rAF-throttled scroll tracking, and fixed 26px rows scoped to `.is-virtualized` containers. The files tab content column is now a flex layout so the rows container (not the whole tab) is the scroll viewport when virtualized. The piece map already draws to canvas (PAR-2.6); untouched.
- New `store/ticker.ts`: one 1s interval per console (`tickerNow`/`tickerTick`/`isTabHidden`), injectable timers/document with a lazily-created real singleton so imports stay node-safe. TrackersTab countdowns, the StatusBar clock, SpeedTab refresh (every 2nd tick), StatsView refresh (every 5th tick), Sidebar RSS badge (every 60th tick), and the StatusBar 60s refresh all derive from it; the five local intervals they replace are gone. The ticker pauses on `visibilitychange` and steps immediately on resume, so hidden tabs run zero timers and stats/speed polling suspends structurally.
- Detail subscriptions cancel on unfocus: `fetchDetail`/`fetchGeneral`/`fetchSpeed`/`fetchLogger` accept an `AbortSignal`, the focus effect aborts in-flight detail on hash change/deselect/unmount alongside the existing `sendUnfocus`, and General/Logger/Speed tabs abort on refocus or unmount (SpeedTab also aborts the previous refresh before starting the next).
- Verified with `npm test` (new: `chart-points.test.mjs` — builder geometry, cache identity, append/drop rebuild-once, hover builds nothing; `ticker.test.mjs` — single interval, hidden pause, resume-with-step, dispose; extended `virtual-window.test.mjs` — 26px geometry, threshold constant), typecheck, production build (79.6 KB gzip, still under the 120 KB gate), and `go test ./...` + `go vet` clean.

Remaining:

- Real frame-time confirmation for the graphs and virtualized detail lists still wants the POL-8.1 Playwright harness; the cache-rebuild counts and slice benchmarks above are the interim record.
- Transient modal polls (MoveDataModal 400ms, CreateTorrentModal 1s) keep their own intervals; they are short-lived and out of the steady-state budget.
- Browser automation for hover readouts, large peer/file lists, and hidden-tab fetch suspension when the POL-8.1 harness is available.

### PERF-7.5 — Bundle and startup (P1)

**As a** user, **I want** a fast first load **so that** the console feels instant on a LAN.

Acceptance criteria:

- Settings, Stats, RSS, and History routes are code-split; the console route ships first.
- Bundle size is reported in CI with a budget; no dependency is added without a recorded size delta.
- First snapshot renders skeleton-to-data within 300ms of WebSocket connect on the 5,000-torrent fixture.

Results:

- Settings, Stats, RSS, and History routes are `lazy()` chunks with a `Suspense` loading fallback; the entry chunk ships the console only (171.6 KB raw / 55.4 KB gzip, down from the 268 KB / 79.6 KB single bundle). Lazy chunks: Settings 21.0 KB, Stats 4.2 KB, RSS 2.2 KB, History 2.0 KB gzip; total 84.9 KB. Hashed chunk names ride the existing immutable `/assets/*` caching, and `embed all:dist` picks them up with no server change.
- `scripts/size.mjs` now gates entry (80 KB) and total (120 KB) separately and additionally fails when a runtime dependency from `package.json` is missing from the new `web/DEPENDENCIES.md` ledger, which records each dep's purpose, the chunk baselines, and the before/after procedure for deltas. The CI frontend job now runs typecheck and `npm test` before build + size.
- The table renders 20 shimmer skeleton rows while `connection() === "connecting"` and swaps to data on the first snapshot. `test/startup-perf.test.mjs` drives the real snapshot→store→ordered-view→window-slice path on a 5,000-torrent fixture: 19.3ms + 22.4ms + 0.1ms ≈ 42ms total against the 300ms budget. Numbers recorded in `docs/performance.md`.
- Verified with `npm test`, typecheck, production build, `npm run size`, `go build`, `go test ./...`, and `go vet` clean.

Remaining:

- Browser paint time on reference hardware (parse + first contentful paint over LAN) still wants the POL-8.1 Playwright harness; the data-path benchmark and chunk budgets above are the interim record.
- `SettingsPanel` is the largest lazy chunk (21 KB gzip, driven by its long-line sections); splitting it per-section is POL-8.8 code-health material, not needed for the budget.

---

## Epic 8 — Polish and shippable quality

### POL-8.1 — Frontend test harness (P0)

**As a** maintainer, **I want** component and browser tests **so that** UI stories can be verified and regressions caught.

Acceptance criteria:

- Vitest with Solid testing utilities and a DOM environment; a `test` script in `web/package.json`; CI runs typecheck, lint, unit tests, and build on every pull request.
- Playwright runs against the Go server with `fakertorrent` for deterministic end-to-end flows, and against the Compose appliance for the release smoke (shared with `backlog.md` QA-5.3).
- ESLint and Prettier are configured; the current 3,000-character lines are reformatted; a lint rule caps line length and bans `window.confirm`/`window.prompt`/`window.alert`.
- Coverage thresholds are set for `lib/`, `store/`, and formatting utilities.

Results:

- Vitest (`web/vitest.config.ts`, kept separate so test-only resolve conditions never leak into the production build): suites in `test/**/*.test.ts(x)` import straight from `src` with no compile step, happy-dom globals, and the Solid client build. All 14 legacy `.mjs` scripts were migrated assertion-for-assertion (46 tests) and removed along with the `tsc/.test-dist` runner; `npm test` is `vitest run`. New suites: `format.test.ts` (formatting utilities, now 100% covered), `store-ui.test.ts` (UI store public API), `stores-fetch.test.ts` (six fetch-backed stores with stubbed fetch), and `components/sparkline.test.tsx` proving `@solidjs/testing-library` with a mocked session store. Total 69 tests, green across 12 consecutive runs.
- Coverage gate (`npm run test:coverage`, v8 over `src/lib` + `src/store`): lines 55, functions 55, branches 40, statements 55 — measured baselines, documented as raise-only. `lib` sits at 81% lines; the fetch-store suite lifted `store` from 19% to 47%; session/WS lifecycle stays on the Playwright suite by design.
- Playwright (`web/playwright.config.ts`, `web/e2e/`): `serve.sh` boots a real `blackbird` binary against `fakertorrent` (deterministic 25-torrent session, seed 7, no auth, temp state, port 18223) and three specs cover table load, filter/clear, and selection with detail tabs. Every test fails on console errors, page errors, failed requests, and non-OK API responses; traces/screenshots keep on failure. `E2E_BASE_URL` retargets the same suite at the Compose appliance for the QA-5.3 smoke. `FAKE_SESSION_SIZE/_SEED` env support was added to `cmd/fakertorrent` for this.
- ESLint (flat config: TypeScript recommended, Prettier-compatible) plus Prettier (printWidth 100): `no-alert` bans native dialogs in new code with `max-len` capping lines at 120. Prettier reformatted the 3,000-character SettingsPanel lines and the other long JSX across the tree; the 7 lines it cannot split were hand-fixed by extracting helpers (`peerColumnWidth`, `effectiveWidth`/`cellClass`, cache-key joins). The 14 legacy native-dialog sites carry targeted `eslint-disable-next-line no-alert` markers pointing at POL-8.2, which migrates them; `no-explicit-any` stays off until the POL-8.8 typed migration.
- CI (`compose-smoke.yml`): the frontend job runs typecheck, unit tests with coverage, lint, format check, build, and the entry/total bundle budget; a new `e2e` job builds the frontend, installs Chromium, runs the browser suite, and keeps failure artifacts 7 days. `make test-web`, `make lint-web`, and `make e2e` reproduce it all locally. Harness documented in `web/TESTING.md`; runtime/dependency ledger updated in `web/DEPENDENCIES.md`.
- Verified with `npm test` (69 green, 12x stable), typecheck, lint, format check, production build, `npm run size`, `go build`, `go test ./...`, and `go vet` clean.

Remaining:

- Fixed a real gap the suite exposed: `fakertorrent` returned one hardcoded detail row, so `FetchDetail` 502'd on generated sessions (`torrent %q not found`); it now returns one row per bench hash with size-derived transfer facts and bitfield, covered by `TestGeneratedDetailSelectableByHash`. Canned sessions are byte-identical.
- Fixed a flake the migration exposed: the PERF-7.2 view-perf guard compared one rebuild sample against 20 ticks and spiked under parallel-worker contention (~1 in 10 runs); it now retries once and fails only on a reproduced breach, matching the Go `TestPerfRegression` policy.
- Login flow is not covered against fakertorrent (auth disabled there by design); cover it via a Compose-appliance run, where bootstrap credentials apply.
- Browser paint timing on reference hardware still belongs to the Playwright harness follow-up noted under PERF-7.x; screenshot baselines per theme belong to THM-9.2.
- No Playwright run against the live Compose appliance yet — the `E2E_BASE_URL` mechanism is in place; the scheduled appliance job is QA-5.3 scope.

### POL-8.2 — Replace native dialogs with an in-app dialog system (P0)

**As a** user, **I want** consistent confirmations and inputs **so that** destructive actions look intentional and can't be blocked by browser popup settings.

Acceptance criteria:

- One `Dialog` primitive (modal, focus-trapped, `role="dialog"`, `aria-modal`, Escape and backdrop close where safe) backs confirm, prompt, and form dialogs; every one of the eleven native call sites is migrated.
- Destructive confirmations name the torrent count and, for data removal, the paths; the destructive button is styled per the handoff and requires no double-confirm.
- "Don't ask again for this session" is available on non-data-destructive confirms.
- Tests cover focus trap, restore focus on close, and keyboard operation.

Results:

- New promise-based system: `store/dialog.ts` (confirm/prompt/form requests, settle, session-scoped skip memory) plus `components/Dialog.tsx` (`DialogHost`, mounted once in App): `role="dialog"` + `aria-modal`, initial focus on the safe choice (input for prompts, cancel for danger, confirm otherwise), Tab trap with wrap, Escape/backdrop to cancel, and focus restored to the opener on close. Global shortcuts suspend while a dialog is open so a second dialog can never stack.
- All 14 native call sites migrated (the baseline counted 11; three more had accreted): toolbar and menu remove flows share one confirmation naming the torrent count, with data-removal listing the affected base paths (capped at 10 + "…and N more") on a danger-styled button and a single confirm; plain removal offers "don't ask again for this session" while data removal always asks. Label/rename/custom-field prompts, tracker add (now one two-field form instead of two sequential prompts, same validation), tracker remove, peer ban (names count + addresses), label delete + reassign, raw-method execute, and settings-discard flows all run through the primitive.
- Verified with `npm test` (new `components/dialog.test.tsx`: 10 tests covering modal semantics, safe-choice focus, Tab trap both directions, Escape/backdrop cancel, focus restore, prompt Enter/empty-ban, form records, details cap, skip memory, and danger never skipping — 6/6 stable runs), a new Playwright flow (Delete → dialog names the count → focused confirm → Escape cancels with selection intact), typecheck, lint with zero `no-alert` exceptions left, format check, production build (entry 57.4 KB, still under budget), `go test`/`vet` clean.

Remaining:

- Browser automation for the remaining dialog flows (label, rename, ban, tracker add) rides the POL-8.1 harness whenever needed; the primitives are covered at unit level.
- The dialog copy is English-only; localization, if ever wanted, needs a string-table pass over these and the toast messages.

### POL-8.3 — Notification system (P0)

**As a** user, **I want** notifications that queue, carry severity, and can be acted on **so that** feedback is never lost.

Acceptance criteria:

- Toasts queue (max visible 3, with an overflow count), have success/info/warning/error variants, optional action buttons (Undo for label changes, Retry for failed actions, View for completed torrents), and configurable duration; errors persist until dismissed.
- Duplicate messages within a window coalesce into a count (shared with `backlog.md` SHIP-1.3).
- Optional browser Notifications for completion, error, and RSS matches, gated by a Settings toggle and permission flow.
- A notification centre lists the last 50 with timestamps.
- The tab title shows aggregate rates and a count of active downloads; the favicon reflects connection state.

Results:

- New `store/notifications.ts`: every toast is a queued record with a kind, optional action, and timestamp. At most 3 show with an overflow count opening the centre; same kind+message pairs within 5s coalesce into a ×N count (SHIP-1.3); non-errors expire (4.2s success/info, 6s warning, per-call and Settings-configurable default, 0 disables auto-dismiss) while errors persist until dismissed. `showToast` keeps its signature and delegates, so all ~80 call sites work unchanged.
- Actions wired where the flows exist: Undo on both label-change paths (restores per-value previous labels), Retry on both batch-action failure paths, View on completion toasts (focuses the torrent on the console route). Watch notices gained severity (loaded success, malformed/load failures error, unknown warning) and rule failures now surface as errors.
- Server fanout (Go): new version-independent `notice` envelope with `completed` (from the poller's `OnTorrentComplete`, alongside the history write) and `rss-loaded` (new `Service.OnLoaded` hook on filter auto-loads) kinds, wired in `main.go`. The client toasts completions with View, records RSS loads silently (their home is the RSS view), and raises browser notifications for both plus rule failures.
- Browser Notifications fire only when the Settings toggle is on, permission is granted, and the tab is hidden; the toggle runs the permission flow inline and reverts with guidance on denial. Prefs are browser-local.
- Notification centre: top-bar bell with unread badge, right-side sheet with the last 50 records (kind stripe, timestamp, count, action), Clear, Escape/backdrop close; opening marks read.
- Tab title shows live aggregate rates and the downloading count while connected (`↓/↑ · N active — Blackbird Console`) via pure builders; the favicon is a state-colored dot (green/gray/red).
- Verified with `npm test` (new suites: 11 store tests — queue, dedup window, durations, sticky errors, actions, 50-ring, unread, prefs, policy matrix, hidden-only raise, permission flow; 5 component tests — variants/live regions, assertive errors, action/count, overflow, centre list/clear/close; 3 title/favicon pins — 98 total green), 4 Playwright flows including the remove-dialog cancel and the title check, typecheck/lint/format/size clean (entry 60 KB), and `go build/test` (new hub delivery + RSS hook tests) with `vet` clean.

Remaining:

- Toasts still originate at action/notice call sites rather than from a history-log subscription; the centre now shares the notify source, closing the PAR-5.3 gap for new events (backfill of old history into the centre is deliberately out).
- RSS failures stay in the filter match history without toasts; only successful loads notify.
- The centre panel is not focus-trapped (unlike the POL-8.2 Dialog); trapping it belongs to the POL-8.5 keyboard/ARIA pass.
- No Playwright coverage for browser Notification permission (headless permission is stubbed at unit level) or for real completion fanout (needs a completing torrent, not a static fake session).

### POL-8.4 — Wire every Settings field and remove inert ones (P0)

**As a** user, **I want** every setting to do something **so that** the UI never lies.

Acceptance criteria:

- `ui.date_format` and `ui.rate_format` drive all formatters (with tests for binary vs decimal and local vs ISO); `ui.sort` seeds the sort store; `ui.visible_columns` migrates into PAR-1.2's layout; `ui.poll_interval` is either removed or becomes the client-side polling hint it claims to be, with a documented migration.
- `ui.accent` (and the theme selection from Epic 9) is applied at boot from the session snapshot, not only when Settings is mounted; unsaved Settings drafts preview live and revert on Revert or navigation away.
- Settings sections that are prose-only (General) gain their content or are removed; the Bandwidth and Queue sections reflect PAR-4.x.
- A "restart required" or "reconnect required" badge appears on fields that need it (shared with `backlog.md` DOC-7.2).

Results:

- `ui.date_format`/`ui.rate_format` drive every formatter through module prefs in `lib/format` (binary/decimal divisor, local "12:04 today"/ISO "2026-09-03 12:04" UTC), hydrated at boot from `/api/settings` alongside columns/sort/saved-filters, previewed live from the Interface selects, and restored on Revert/unmount. `ui.sort` seeding (PAR-1.5) and the `ui.visible_columns` migration (PAR-1.2) were verified and pinned with store tests; the legacy keys still hydrate old browsers and configs.
- `ui.poll_interval` removed as a setting with a documented migration: the editor row is gone (the value round-trips untouched so saves never wipe it), the server keeps it read-compatible but warns (`ui.poll_interval is deprecated and ignored…`) — and config warnings, previously collected but never surfaced, now log at startup and on SIGHUP reload. `example.yml` and the user guide record the removal.
- `ui.accent` applies at boot from `/api/settings` (App mount, every route), previews live from drafts, and reverts to the committed value on Revert or navigation away (unmount cleanup). Theme selection does not exist yet — Epic 9 is unstarted — so accent-only behavior is implemented and the theme half is recorded under THM-9.2/9.3.
- General is a live session overview now (connection + port, daemon versions, torrent count with session down/up totals); Queue's stale "YAML-managed stop-on-ratio" note now points at Seeding ratio groups; Bandwidth already reflected PAR-4.1 (verified, untouched). A 167-line dead duplicate Interface section (shadowed by `InterfaceSection`, unreachable) was deleted.
- Restart badges: audited every editable field against the save handler and service wiring — all apply live (tuning setters/channels, directories, blocklist, retention, live-config re-reads, client-side ui.*) — so no editable field takes a badge. The `SettingRow` badge, the `lib/applyBehavior` classification (all UI surfaces live; `server.listen/base_url`, `rtorrent.scgi/timeout/max_response_bytes`, `auth.*`, `poll.interval`, `log.level` restart), and the user-guide list ship as the DOC-7.2-shared source, covered by table + badge-render tests.
- Verified with `npm test` (111 green: new format-pref, apply-behavior, badge-render, sort/accent hydrate, and full-panel accent preview/revert + General content suites), 5 Playwright flows (new accent preview/revert in a real browser), typecheck/lint/format/size clean (entry 60.2 KB), and `go build/test` (new deprecation-warning test) with `vet` clean.

Remaining:

- The 21 scalar tuning setters still use the pre-0.16 single-param `.set` form (PAR-4.3 follow-up story): on rTorrent 0.16 they fault per key and surface in the save results — live-apply in form, failure-reporting in practice until that migration lands.
- Derived accent tokens (`--accent-tint-*`, `--accent-text`, `--accent-foreground`) still follow the default navy when the accent changes; full re-tinting is THM-9.1 scope.
- Theme selection, theme cards, and density options remain THM-9.2/9.3.
- A full Compose-appliance settings round-trip (save → daemon effect per section) is QA-5.3 e2e scope; the unit/browser pins above cover draft/preview/revert locally.

### POL-8.5 — Accessibility and keyboard completeness (P0)

**As a** keyboard or screen-reader user, **I want** the console operable without a mouse **so that** it meets a basic WCAG 2.1 AA bar.

Acceptance criteria:

- Table: rows are focusable with roving tabindex, `aria-selected`, and `aria-rowindex`; headers are buttons with `aria-sort`; the selection model is announced.
- Tabs (detail panel, add modal) use `role="tab"`/`tabpanel` with arrow-key navigation; context menu uses `role="menu"` with arrow, Home/End, and type-ahead.
- Visible focus rings on every interactive element using an accent-derived outline token; no `outline: none` without a replacement.
- Lost-connection banner is a live region; toasts use `aria-live` appropriate to severity.
- Additional shortcuts: `A` add torrent, `R` recheck, `1`–`5` detail tabs, `?` shortcut help overlay, `F` toggle detail panel, `Ctrl/Cmd+K` command palette (P1); all listed in a help dialog generated from the binding table so hints never drift.
- Automated axe checks run in the Playwright suite with zero serious violations.


Results:

- Table: rows carry roving tabindex (focused row, else first rendered), `aria-selected`, and absolute `aria-rowindex`; sort headers are native buttons with the existing `aria-sort` kept on the `th`; a polite live region announces count plus focused name. Detail tabs and add-modal segments are `tablist`/`tab`/`tabpanel` with arrow/Home/End support; the context menu is `role="menu"` with `menuitem` items, popup state on submenu toggles, initial focus, arrows/Home/End, and 500ms type-ahead. Global shortcuts stand down inside tablists/menus/dialogs (by signal and by DOM ancestry — Solid delegates everything at document level, so `stopPropagation` alone cannot suppress the global handler).
- Shortcuts are one table (`lib/shortcuts.ts`) driving both matching and the `?` help overlay: `A` add, `R` recheck, `1`–`8` detail tabs (all eight, not five — five would strand three tabs), `F` panel toggle, `/`, arrows/pages/Home/End, Space, Del, Ctrl+A, Esc. The `Ctrl/Cmd+K` palette is P1-marked in the AC and stays deferred.
- Focus: new `--focus-ring` token with a global `:focus-visible` ring, covering the ten `outline: none` input resets; banner is `role="alert"` (toasts were already severity-lifted in POL-8.3).
- The POL-8.3 notification centre graduates here too: focus-trapped with initial focus and opener restore (tested), closing the deferred trap item.
- Axe runs on console + settings with zero serious/critical violations. The first run found real issues, all fixed: unnamed icon/dot/label-less controls (sb-dot role, notice log role, select-all name, SettingRow wrapping labels so all ~60 settings inputs associate without id plumbing, two selects named), and contrast (captions/counts/hints/units/percents/empty-states lifted to passing tones; progress % labels moved beside the bar because overlaid text cannot pass on both fill and trough).
- Verified with `npm test` (124 green: new shortcuts-table, help, table-semantics, menu-keyboard suites), 12 Playwright flows (new keyboard suite: help, add, detail toggle, digit tabs, menu map, tablist arrows; axe on both routes), typecheck/lint/format/size clean, Go suite clean (untouched).

Remaining:

- Two load-bearing findings fixed along the way, recorded here: (1) tablist Home/End double-fired into global selection jumps (Solid delegation) and threw `Stale read from <Show>` — fixed by ancestry guards; (2) the PERF-7.2 view-perf ratio guard was invalid by construction (1.3–17ms rebuild variance vs single-digit-ms margins) and flaked ~25% of runs — replaced with an absolute 250ms budget on pre-built, garbage-free tick rounds with warmup + retry, logging the rebuild as context only. Precise regression measurement stays with the isolated Go PERF-6.6 bench.
- Command palette (`Ctrl/Cmd+K`) deferred as the AC marks P1.
- Moderate/minor axe findings (if any) and theme-contrast baselines belong to THM-9.2's checker; a full screen-reader pass (NVDA/VoiceOver) is still recommended before v1.0.
- Full Compose-appliance axe + keyboard run is QA-5.3 scope (`E2E_BASE_URL` mechanism in place).

### POL-8.6 — Routing, layout persistence, and session continuity (P1)

**As a** user, **I want** the browser back button, deep links, and remembered layout **so that** the console behaves like an app.

Acceptance criteria:

- Routes `/`, `/settings/:section`, `/stats`, `/rss`, `/history`, and `?filter=`/`?focus=<hash>` query state are reflected in the URL and restorable on reload; back/forward work.
- Sidebar width, detail panel height and tab, column layout, sort, and last route persist per browser; a "Reset layout" action clears them.
- Reconnect after a server restart restores selection and focus where the hashes still exist.

Results:

- Hash routing (`lib/router.ts` pure parse/build + store sync): `#/`, `#/settings/:section`, `#/stats`, `#/rss`, `#/history` with `?filter=`/`?focus=` on the console. Route/section switches push history (back/forward work); filter/focus motion replaces in place against the live query (an earlier debounced-query variant dropped filters on reload-before-flush — caught by e2e). Boot prefers URL, then the persisted last route; unknown routes/sections fall back instead of blanking.
- Settings sections moved from mount-only local state to the router-owned `settingsSection`, so deep links (`#/settings/Bandwidth`), nav, and back/forward share one source; status-bar chips navigate with their section. The obsolete `requestedSettingsSection` handoff is gone.
- Sidebar is resizable (drag + double-click reset, 140–340px) with per-browser persistence; detail height/tab, columns, sort, peer layout, and last route were already persisted. One `Reset layout` action (Settings > Interface) clears exactly the layout keys — saved filters, notification prefs, and dialog skips are user data and stay.
- Reconnect continuity: the snapshot path stashes selection/focus on the connected→disconnected edge and restores survivors on reconnect; a pending-focus handoff covers boot/deep-link URLs applied against an empty session (the table's prune-on-mount otherwise clears them unseen), superseded by any explicit selection.
- Verified with `npm test` (134 green: new router parse/build, navigate/sync, restore-selection, layout-reset, last-route suites), 16 Playwright flows (new routing suite: section deep-link + back/forward, reload filter/focus restore, sidebar persist, reset layout), typecheck/lint/format/size clean (entry 64.1 KB), and `go build/test/vet` clean (untouched).

Remaining:

- Reconnect restore is covered at helper level (`restoreSelection`) plus the stash/consume wiring review; a full kill-and-restart-the-daemon browser proof needs process control Playwright lacks — QA-5.3 scope if wanted.
- `E2E_BASE_URL` runs of the new routing suite against the Compose appliance are QA-5.3 scope.
- Playwright no longer reuses squatting dev servers (`reuseExistingServer: false`): reuse silently tested stale embeds across rebuilds during this story.

### POL-8.7 — Empty, loading, and error states across every view (P1)

**As a** new user, **I want** every screen to explain itself when empty **so that** first run isn't a blank table.

Acceptance criteria:

- Empty session shows a guided empty state (add torrent, set up a watch directory, configure RSS) with links to docs.
- Every list (peers, trackers, files, RSS, history, volumes) has an empty state and a loading skeleton; every fetch failure has an inline retry.
- Version and update check: Settings > About shows Blackbird, rTorrent, and libtorrent versions, build metadata, and an optional opt-in release check.
- Screenshots for the README (`backlog.md` DOC-7.1) are captured from the Playwright suite so they never go stale.

Results:

- Added `GET /api/version` (`internal/api/version.go`): stamped Blackbird build (version/commit/buildDate wired from `main.go` ldflags, dev/none/unknown defaults) plus live rTorrent/libtorrent versions, connection, and torrent count from the poller snapshot; empty daemon fields while disconnected.
- Added Settings > About (`store/version.ts` + `SettingsPanel` About section): build identity, daemon versions, session state, loading skeleton, failed + Retry, and an opt-in browser-local release check (off by default, no request until enabled with an endpoint; semver compare, current/update/failed states, configurable endpoint persisted per browser).
- Empty session now links into the in-app guides (watch directories, automation rules, About) and `docs/user-guide.md` (new: intake, watch, RSS, completion/unpack, versions).
- Every detail view distinguishes loading (skeleton), empty, and failed + Retry: General/Logger/Speed track per-view errors in the session store (`viewFailed`, cleared on success, pruned with the session); files/trackers/peers/piece-map empties gained inline Retry via `fetchDetail`.
- Stats traffic gained loading skeleton plus failed + Retry (previously conflated with empty); host telemetry failures surface with Retry instead of silent dashes; RSS gained a global failed + Retry, per-feed error Retry, and Settings > Automation navigation buttons instead of dead text.
- New `e2e/screenshots.spec.ts` captures console, console-detail, empty-filter, stats, settings-about, settings-general, rss, and history into `docs/screenshots/` for DOC-7.1; documented in `web/TESTING.md`.
- Verified with `go test ./...` (new `version_test.go`: wired/stamped/unwired), `go vet`, `npm test` (141 green: new `version.test.ts` store suite + `settings-about.test.tsx` component suite), typecheck/lint/format/size clean (entry 65 KB, total 96.8 KB), and the full Playwright suite (17 flows incl. screenshots, green).

Remaining:

- No Playwright coverage for real completion fanout toasts or browser Notification permission (as before, POL-8.3); About update-check against a real releases endpoint is unit-covered only (no network in e2e by policy).
- `docs/screenshots/` images are not yet embedded anywhere — DOC-7.1 owns the README that consumes them.
- RSS per-feed Retry currently re-runs the global refresh (no per-feed backend endpoint); item-add failures stay toast-only.

### POL-8.8 — Code health (P1)

**As a** maintainer, **I want** the codebase reviewable **so that** the community can contribute.

Acceptance criteria:

- Dead code in the baseline is removed; `GlobalSettingsKeys` and `tuning.GetterMethods` are unified into one table.
- Components over 150 lines are split by responsibility (settings sections become one file each); CSS is split per component or uses CSS modules, keeping tokens in one place.
- `go vet`, `staticcheck`, `-race`, and the frontend lint run in CI (shared with `backlog.md` QA-5.1).
- The REST API gains a `/api/v1` prefix and a `/api/v1/version` route; the WebSocket schema bump from PERF-6.2 ships alongside.

Results:

- Tuning is one table (`internal/tuning/methodTable`: key → setter + getter): `Entries()` resolves setters through it and `GET /api/settings` iterates `Keys()`/`GetterFor()` in stable order (previously a nondeterministic map iteration). `GetterMethods()` is deleted (zero callers). Wire forms are unchanged — the 0.16 `.set_kb`/bare-getter migration stays its own story per PAR-4.3. Pinned by `TestMethodTableIntegrity`/`TestMethodTableCoversEntries`.
- `SettingsPanel.tsx` (3,965 lines) is now `SettingsPanel.tsx` (shell: state/load/save/nav, ~1,000 lines) plus `components/settings/`: `types.ts` (all draft/prop types incl. `SectionProps`), `model.ts` (pure helpers), `fields.tsx` (tuning input/check rows), `SettingRow.tsx`, `SettingsSection.tsx` (dispatcher), and one file per section (General, Connection, Queue, Directories, Labels, Interface, Automation+Unpack, History, About, Scheduler, Seeding, Bandwidth, Advanced). `SettingRow` is re-exported from the old path so existing imports keep working.
- `styles/app.css` (4,719 lines) is 19 per-component files under `styles/app/` with the cascade order pinned by the `@import` list (verified byte-identical concatenation modulo the new header); tokens stay in `tokens.css`, responsive overrides stay last.
- REST is versioned: one route table serves every route under both `/api/*` (legacy, retained per SHIP-1.4) and `/api/v1/*`; the frontend, its tests, and the CSV/export hrefs all use `/api/v1`. `/api/v1/version` exists alongside `/api/version`, and the version payload now advertises `api: v1` plus the WebSocket `min: 1, current: 2` range (the PERF-6.2 v1+v2 negotiation itself is unchanged).
- CI has a `go-checks` job (`go vet`, `go test -race`, pinned staticcheck v0.8.1; `make staticcheck` reproduces locally). staticcheck is clean repo-wide: 17 pre-existing findings fixed along the way (dead test helpers/fields/`readLimited`/`revRe`/`hub.sub` removed, SA4006 discards, S1011 append rewrites, S1016 conversion, S1030) — all behavior-preserving, each verified against package tests.
- Verified with `go build/test/vet/staticcheck` clean, `go test -race` on poller+api, `npm test` (154 green: new `settings-sections.test.tsx` renders all 13 sections through the dispatcher), typecheck/lint/format/size clean (entry 65 KB, total 97.7 KB), and the full Playwright suite (17 green, against `/api/v1`).

Remaining:

- Other 150-line+ components (ActionControls, TorrentTable, DetailPanel, PeersTab, StatsView, modals) are untouched: each is still one cohesive file, and splitting them further is follow-up work with its own regression risk, not part of this story.
- The legacy `/api/*` spelling and WebSocket v1 service have no removal date; per SHIP-1.4/REL-8.1 they stay until a major version with a documented migration.
- A full `go test -race ./...` runs in CI (20 min timeout); locally only poller+api were run under race (107s+18s) since the full matrix is slow on a laptop.

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

Results:

- New `styles/palette.css`: every literal (32 colors + 3 shadow shorthands) as `--pal-*` raw values — the default (Blackbird Dark) theme value set. `styles/tokens.css` is now the pure semantic layer: color tokens alias `var(--pal-*)`, and the accent family derives via `color-mix()` (`--accent-tint*` mix toward transparent = hue-preserving alpha; `--accent-text`/`--focus-ring` mix toward white). `--accent-foreground` stays a near-white palette alias; `--progress-*` stay palette aliases with the never-the-accent rule documented in the header.
- Non-color tokens are untouched and theme-independent; the header records that themes may override `--shadow-*`/`--r-*` only (shadows alias `--pal-shadow-*` so they can).
- Five `rgba()` literals in component CSS converted to `color-mix()` (banner error tints derive from `--status-error`); `base.css` loads palette before tokens.
- stylelint gate (new devDependency + `.stylelintrc.json`): `color-no-hex` and an `rgb()`-family ban everywhere except `palette.css`/`themes/`; `npm run lint:css` runs in CI (`frontend` job), `make lint-web`, and `web/TESTING.md`; recorded in `web/DEPENDENCIES.md`.
- New `lib/theme.ts`: `color-mix(in srgb)` JS mirror (`mixWithWhite`/`withAlpha`, plain channel interpolation), `supportsColorMix()` detection, `readToken()`, and token readers with handoff fallbacks (`pieceMapColors`, `connectionDotColors`, `themeColor`). `applyAccent()` sets `--accent`, drops stale inline derivations when color-mix works, writes the JS mirror when it doesn't, and bumps a `themeVersion` signal.
- Canvas piece map reads `--progress-*`/`--bg-track`/`--label-iso` per draw and repaints on `themeVersion`; favicon dots resolve from tokens (pure `faviconHref` keeps its literal fallback signature); `DocumentMeta` maintains `<meta name="theme-color">` from `--bg-app` (static `#101214` fallback in `index.html`).
- SVG charts/sparkline already consumed `var(--accent)`/`var(--rate-up)` through stylesheets — verified, untouched.
- Verified with `npm test` (165 green: new `theme.test.ts` math/fallback/layer-contract suites + `theme-fallback.test.ts` no-color-mix path), typecheck/lint/format/size/stylelint clean (entry 65.9 KB, total 98.6 KB), full Playwright suite (17 green), and screenshot review (selection tint, sidebar active, progress blue/green, graphs intact).

Remaining:

- No `data-theme` mechanism yet and no additional themes — that is THM-9.2, which consumes this architecture.
- JS-side color defaults (`SettingsPanel`/`LabelsSection` label palette, `documentMeta` fallbacks) still duplicate handoff literals as data/fallback values; THM-9.2 should source label colors per theme with contrast-safe text.
- Derived `--accent-text`/`--focus-ring` shifted slightly from the hand-picked handoff values (white-mix cannot reproduce blue-channel-max colors); full contrast baselines belong to THM-9.2's checker.

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

Results:

- Five themes on the THM-9.1 palette layer: `dark` is `:root` (unchanged handoff); `light`/`midnight`/`contrast`/`classic` each define the complete 42-key `--pal-*` set in `styles/themes/*.css`, loaded via `base.css`. `data-theme` on `<html>` always holds the concrete id; `system` resolves through `prefers-color-scheme` and re-applies live on OS changes.
- Browser choice (`blackbird.theme.v1`) wins; otherwise the operator default (`ui.theme` YAML: dark|light|midnight|contrast|classic|system, default `dark`, validated with a named-values error, documented in `example.yml`). `lib/themes.ts` holds ids, metadata, per-theme accent presets, resolution/persistence, and contrast math; `ui.ts` owns the signals (`browserTheme`, `serverTheme`, `resolvedTheme`, `initTheme`, `setBrowserTheme`) and re-applies on changes.
- No-flash boot: an inline `<head>` script (localStorage → server meta → dark, system resolved, validated) sets `data-theme` plus the accent before first paint. The server injects both defaults into `index.html` per request (`ThemeDefault`/`AccentDefault` hooks on the asset server, validated, identity encoding; nil hook keeps the precompressed path). Covered by `assets_theme_test.go` and a no-flash e2e (early `data-theme` matches post-load).
- `ui.accent` default is now empty = theme default (was `#35418f`; dark output identical). Empty previews/restores the theme accent via `clearAccentOverride()`; the picker adds a Theme-default swatch, per-theme preset swatches, and keeps the custom picker. Old configs carrying explicit `#35418f` are unaffected (documented upgrade note in the user guide).
- Accent derivations needed a light-theme-safe endpoint: new `--pal-accent-ink` (white on dark, black on light) feeds `--accent-text`/`--focus-ring`; `deriveAccentTokens(accent, ink)` mirrors it (tints still mix toward transparent). Fallback browsers re-derive with the active theme's ink on every theme switch.
- Labels: `--label-*`/`--status-*` vary per theme; configured label colors render in table chips (tinted chip + `readableTextOn` text) and sidebar dots, composited against the live `--bg-app` surface and re-resolved on theme change.
- Contrast gate `scripts/check-contrast.mjs` (parses CSS as source of truth, asserts palette completeness, replicates derivations): all 5 themes × body/muted/chip pass WCAG AA (dark 13.3/7.1/5.6 … contrast 21.0/13.3/8.0). Runs in CI (`frontend` job) via `npm run check-contrast`.
- Screenshots run every shot in Dark and Light (`*-dark.png`/`*-light.png` in `docs/screenshots/`, old unsuffixed files removed); new `e2e/theme.spec.ts` covers persistence, live system-following, and the server default.
- Verified with `go build/vet/staticcheck/test` clean, `npm test` (179+ green incl. `themes.test.ts`, `settings-theme.test.tsx` picker suite), typecheck/lint/format/size/stylelint/contrast clean, and full Playwright green.

Remaining:

- Density options, theme cards with miniature previews, and custom accent picker refinements are THM-9.3.
- Custom themes / operator CSS files are THM-9.4; theme-aware print/scrollbars are THM-9.5.
- Saved pre-old-release configs keep explicit `accent: "#35418f"` (byte-identical on dark); clear the field to adopt per-theme accents.
- StatsView space-by-label bars keep the built-in name→token map with a neutral fallback for custom names (chips/dots carry the custom colors).

### THM-9.3 — Theme picker and live preview (P0)

**As a** user, **I want** to preview and tweak themes in Settings **so that** customization doesn't require editing files.

Acceptance criteria:

- Settings > Interface shows theme cards with miniature previews, the system-follow toggle, accent presets and a custom accent picker, and a density option (Dense per handoff, Comfortable adds 4px to rows and controls) applied via the height tokens.
- Changes preview live across the whole app and revert on Revert; Save persists per browser and, for operators, offers "Set as server default".
- The current accent-application bug (only applied while Settings is mounted) is fixed as part of this story.

Results:

- Theme cards carry miniature previews (`ThemeMini`: sidebar/rows/accent/progress mock from each theme's palette excerpt in `ThemeDef.preview`, pinned against the CSS files by `themes.test.ts` so excerpts cannot drift). System follow stays a radio card resolving live. Accent presets (per resolved theme) + custom picker + Theme-default swatch remain; the operator-default theme select remains.
- Browser theme/density choices preview through staged signals (`previewTheme`/`previewDensity`) without persisting: effective resolution prefers staged → browser → server. Save commits staged previews to localStorage (YAML POST skipped when the draft is otherwise clean, with a confirmation toast); Revert and Settings-unmount discard them (`discardAppearancePreviews`). Footer Save/Revert enable on previews via extended `dirty()`. "Set as server default" (disabled until a theme is previewed) copies the preview into `draft.ui.theme` and saves immediately.
- Density (`dense`/`comfortable`, `blackbird.density.v1`, `data-density` on `<html>`, pre-paint inline script): `html[data-density="comfortable"]` adds 4px to `--h-table-row/file-row/peer-row/sidebar-item/button/input/menu-item`. Virtualization follows through `tableRowHeight()`/`detailRowHeight()` fed by `resolvedDensity()` (table, files, peers).
- Accent-mount bug: confirmed already fixed — `hydrateAppearance` applies `ui.accent` at App boot without Settings mounted (pinned by the existing `store-ui` boot test), the inline script paints the server accent pre-paint, and empty drafts restore the theme default via `clearAccentOverride()` on preview/revert/unmount.
- Verified with `npm test` (184 green: picker preview/commit/revert/default/density suites, excerpt-drift + density-mapping tests), typecheck/lint/format/size/stylelint/contrast clean, `go build/vet/staticcheck/test` clean, and full Playwright green (21 incl. a preview→revert→save→persist→density flow; fixed two real issues found: stale `dist` in the loop and the suite's `gotoConsole` colliding with the persisted last route).

Remaining:

- Full theme authoring surface (custom theme files, import/export, token reference) is THM-9.4; theme-aware scrollbars/selection/print are THM-9.5.
- Density has no operator default (browser-only by design); virtualized lists keep their scroll position structurally on density switch but the absolute offset reinterprets under the new row height.
- The `title`-attribute descriptions were removed from theme radio cards after finding they shadow button content in accessible-name computation; descriptions remain visible nowhere — re-add as a describedby tooltip if wanted.

### THM-9.4 — Custom themes and operator CSS (P1)

**As an** operator, **I want** to define my own theme **so that** the console can match my other dashboards.

Acceptance criteria:

- A theme file format (YAML or JSON, versioned, documented with every token and its role) can be placed in the config directory under `themes/`; valid files appear in the picker, invalid ones log a line-numbered error and are skipped.
- Themes can extend a built-in theme and override a subset of tokens; the contrast checker runs on load and reports warnings in Settings.
- An optional `custom.css` in the config directory is injected after the theme with a documented stability warning; it is served with the same auth as the app.
- Export from the picker writes the current theme (including accent and density) as a theme file; import validates and installs it.
- Theme authoring is documented with a token reference generated from the semantic token table so it cannot drift.

Results:

- Format: versioned YAML (`version: 1`, `name`, `description`, `extends` ∈ built-ins, `dark`, 5 `accents`, `palette` without `pal-` prefixes, `accent`, `density`, `preview`), documented in `docs/custom-themes.md` with the generated `docs/theme-tokens.md` reference (87 tokens parsed from `tokens.css` + palette roles; `npm run gen:theme-docs`, freshness enforced in CI via `git diff --exit-code`).
- Server (`internal/themefile` + 4 routes, each with a `/api/v1` twin): `GET /api/themes` (valid files + `file:line: message` error strings), `POST /api/themes/import` (validates, installs `<sanitized>.yml`, overwrite = update, 400 with line errors, traversal-safe), `DELETE /api/themes/{name}`, `GET /api/custom-css` (text/css, 200-empty when absent, 413 over 256 KB). Validation walks `yaml.Node` so every error carries a line; rescans per request (SIGHUP-safe); auth-gated like all routes.
- Browser: boot fetches the list, custom cards (mini previews, warning badges) stage through the 9.3 preview/commit flow, effective palettes resolve subsets over built-in records (`lib/theme-palettes.ts`, drift-tested against CSS), per-file contrast/completeness warnings render in Settings, import/export/delete wired in the picker, `custom.css` injected last in `<head>` with status + stability warning. Export materializes the visible theme (full palette + accent + density) as installable YAML.
- Verified with `go build/vet/staticcheck/test` clean (19-case invalid table asserting `:LINE:`, route/lifecycle tests), `npm test` (196 green: drift, resolution, warnings, export, picker import/delete/css suites), all linters/contrast/size clean, and full Playwright green (21). Two real issues fixed: happy-dom/SettingRow label click-forwarding (action buttons `preventDefault`) and absent-`custom.css` signaling (200-empty, after finding Chromium reports fetched 204s as aborted).

Remaining:

- Cold boot with a stored custom theme paints the stored built-in/system choice first (custom CSS needs the API round-trip); only the `data-theme` attribute is pre-set.
- `extends` accepts built-ins only (no custom-to-custom chains); the legacy `/api/*` twins stay per SHIP-1.4.
- StatsView space-by-label bars still use the built-in name map (custom colors show in chips/dots).

### THM-9.5 — Theme-aware everything (P1)

**As a** user, **I want** every surface to respect the theme **so that** nothing looks bolted on.

Acceptance criteria:

- Scrollbars (`scrollbar-color`), form controls (`color-scheme`), selection highlight, native date/color inputs, the drop-zone highlight, skeleton shimmer, and the modal backdrop all use tokens.
- Print styles exist for History and Stats (P2 may defer).
- The favicon and `theme-color` follow the theme; the Compose smoke test verifies the light theme loads without a console error.

Results:

- `base.css` now owns the theme-aware primitives: `html` sets `color-scheme: dark` with a light/classic override (native date/color inputs and form controls follow; the one-off `color-scheme: dark` in traffic.css is removed), `*` sets token `scrollbar-color`, and `::selection` uses the accent tint. `applyResolvedTheme` additionally writes inline `colorScheme` from the resolved theme's darkness flag so custom file themes follow too (stylesheet holds the pre-JS fallback).
- Skeletons (token gradients + shimmer), modal backdrop (`color-mix` black), and drop-zone highlight (border/accent tokens) were already token-driven — verified, untouched.
- New `styles/app/print.css` (last import): History and Stats print as ink-friendly documents with app chrome hidden; verified via print-emulation screenshots.
- Favicon + `theme-color` already follow the theme since THM-9.1 (verified in place).
- New `deploy/theme-smoke.sh` (+ `make theme-smoke`): disposable Compose appliance (unique project, ports, and dirs; per-run `RTORRENT_P2P_PORT` so it never collides with a dev stack) running `e2e/appliance-theme.spec.ts` — dark and light console/stats loads with zero console/page/network errors, authenticated. Verified live against Docker Desktop (2/2 green). CI runs it as the `appliance-theme` job.

Remaining:

- The appliance spec covers console + stats only; full-suite runs against the appliance remain QA-5.3 scope via the existing `E2E_BASE_URL` mechanism.
- `theme-smoke.sh` duplicates the bootstrap/wait scaffolding of `smoke-test.sh`; unifying them is future cleanup, not behavior.

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
