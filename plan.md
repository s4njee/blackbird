# Blackbird — Implementation Plan

A web front end for rTorrent in the spirit of ruTorrent, built as a dense desktop-first "pro tool" console per the design handoff in `design_handoff_rtorrent_console/`.

## Stack & architecture decisions (settled)

| Decision | Choice |
|---|---|
| Backend | Go — single binary, serves the API, WebSocket, and the embedded frontend |
| rTorrent link | Direct SCGI from Go to rtorrent (unix socket or TCP), speaking XML-RPC over SCGI. No nginx/Apache proxy. |
| Frontend | SolidJS 1.x + Vite + TypeScript, embedded into the Go binary via `go:embed` |
| Live data | Backend polls rtorrent (~2s), diffs, and pushes deltas to clients over WebSocket. Actions go over REST. |
| Configuration | YAML file(s) are the source of truth for all app + tuning settings. No ruTorrent import — but the Settings UI must cover a similar breadth of features as ruTorrent, particularly network settings for tweaking. rTorrent runtime settings declared in YAML are applied to the daemon on connect via `*.set` methods. |
| Auth | Single user, basic auth (YAML-defined credentials), covering API + WebSocket |
| Design fidelity | High — colors, typography, spacing, row heights, and column widths from the handoff README are final |

Repository layout (target):

```
/cmd/ustorrent          — main package
/internal/scgi          — SCGI transport + XML-RPC codec
/internal/rtorrent      — typed client (d.*, f.*, p.*, t.*, throttle.*, …)
/internal/poller        — poll loop, normalization, diffing, history ring
/internal/config        — YAML schema, validation, load/save
/internal/api           — REST handlers, WebSocket hub, auth middleware
/web                    — SolidJS app (Vite)
/web/dist               — build output, go:embed'ed
```

---

## Epic 1 — Project foundation

Scaffold both halves, wire the build, and establish the design system so every later story lands on rails.

### 1.1 Backend scaffold
**As a** developer, **I want** a Go module with a runnable HTTP server skeleton **so that** later epics have a home.

Acceptance criteria:
- `go build ./cmd/ustorrent` produces a single binary; `ustorrent --config config.yml` starts an HTTP server on the configured address.
- `--version` prints version info; structured logging (level configurable) goes to stderr.
- Graceful shutdown on SIGINT/SIGTERM (drains WebSocket clients, closes the SCGI connection).

### 1.2 Frontend scaffold + embedding
**As a** developer, **I want** a SolidJS + Vite + TypeScript app served by the Go binary **so that** deployment is one artifact.

Acceptance criteria:
- `web/` builds with Vite; output is embedded via `go:embed` and served at `/` with correct MIME types and cache headers (hashed assets immutable, `index.html` no-cache).
- Dev mode: Vite dev server proxies `/api` and `/ws` to the Go backend for live reload.
- A `make build` (or equivalent) target builds frontend then backend in one step.

### 1.3 Design system & tokens
**As a** frontend developer, **I want** the handoff's design tokens codified once **so that** all components share exact colors, type, and spacing.

Acceptance criteria:
- All color tokens from the handoff README (`bg/*`, `border/*`, `text/*`, `accent`, `rate/up`, `progress/*`, `status/*`) exist as CSS custom properties; no hard-coded hex in components.
- `accent` is a single variable defaulting to deep blue-indigo `#35418f`; changing it re-tints selection, chips, sidebar active states, and graphs while progress colors remain fixed.
- IBM Plex Sans 400/500/600 is **self-hosted** (bundled woff2, no Google Fonts request); numeric cells apply `font-variant-numeric: tabular-nums` via a shared class.
- Fixed heights from the handoff (top bar 44, toolbar 36, table header 28, row 30, status bar 26, etc.) are defined as tokens.
- Progress bars follow the handoff rule: blue `#2f9dff` below 100%, green `#3fb950` at 100%, `#4b5158` when stopped+incomplete — never the accent.

---

## Epic 2 — rTorrent integration layer

The Go client that everything else sits on.

### 2.1 SCGI transport + XML-RPC codec
**As the** backend, **I want** to speak XML-RPC over SCGI directly to rtorrent **so that** no web-server proxy is required.

Acceptance criteria:
- Supports `scgi` over unix socket and TCP, selected by YAML (`rtorrent.scgi: unix:///path/socket` or `tcp://host:port`).
- Correct SCGI framing (netstring header with `CONTENT_LENGTH`, `SCGI 1`) and XML-RPC marshalling for strings, ints (i8), booleans, arrays, and structs.
- XML-RPC faults surface as typed Go errors with the fault string; transport errors are distinguishable from faults.
- Concurrent-safe: requests are serialized or pooled over connections; a configurable per-call timeout applies.
- Unit tests cover codec round-trips and fault decoding against recorded rtorrent responses.

### 2.2 Typed rtorrent client
**As the** backend, **I want** typed wrappers for the commands the UI needs **so that** handlers never build raw XML-RPC calls.

Acceptance criteria:
- Torrent list via `d.multicall2` returning: hash, name, size, completed bytes, state (downloading/seeding/stopped/queued/checking/error), checking %, seeds, peers, down/up rate, ratio, label (`d.custom1`), added time, tracker host, private flag, base path — matching the handoff's `torrents[]` state shape.
- Detail calls: `f.multicall` (path, size, completed chunks, priority), `p.multicall` (ip, port, client, have %, rates, flag letters), `t.multicall` (url, status, seeds/leechers, next announce), plus transfer facts (chunk size/count, downloaded/uploaded).
- Actions: `d.start`, `d.pause`, `d.stop`, `d.check_hash`, `d.erase`, remove-with-data (erase + delete files under base path with safety checks), `d.custom1.set`, `d.directory.set`, `f.priority.set`, `d.priority.set`, `load.normal`/`load.start`/`load.raw_start`, tracker add/enable, `d.tracker_announce`.
- Global getters/setters for every key exposed in the Settings epic (throttles, port, encryption, DHT, PEX, peer limits, etc.).
- Version detection (`system.client_version`, `system.library_version`) for the status bar.

### 2.3 Poller & session cache
**As the** backend, **I want** a normalized in-memory session model refreshed on an interval **so that** clients get consistent, diffable state.

Acceptance criteria:
- Polls the full torrent list every `poll_interval` (default 2s, YAML-tunable); detail data (files/peers/trackers) is fetched lazily only for the hash(es) clients have focused, on its own interval.
- State is normalized by hash; ETA and percent are derived server-side when the daemon omits them.
- Each cycle produces a delta (added / removed / changed hashes, changed global stats) for the WebSocket hub.
- A rolling 60-minute ring buffer of global down/up rate samples backs the sparkline and throughput graph.
- On rtorrent unreachability: cache marked stale, `connection` status flips to disconnected, reconnect with capped exponential backoff; recovery is automatic and pushed to clients.
- Sidebar aggregates (counts by status, by label, by tracker host) and disk volume stats (from configured mount paths) are computed per cycle.

---

## Epic 3 — YAML configuration

Configuration lives in YAML; the Settings UI reads and writes it.

### 3.1 Config schema & loading
**As an** operator, **I want** one documented YAML file driving the app **so that** deployments are reproducible and diffable.

Acceptance criteria:
- Schema covers: server (listen addr, base URL), auth (username, bcrypt password hash), rtorrent endpoint + timeouts, poll intervals, download directories (default destination + per-label paths), labels (names + colors), watched disk volumes, UI defaults (accent, visible columns, sort), and the full `tuning:` block (Epic 3.2).
- Missing file: the app starts with defaults and writes a commented example config; invalid file: startup fails with a line-numbered error naming the bad key.
- All keys have sane defaults; a minimal config is just the rtorrent endpoint and auth credentials.
- `--check-config` validates and exits.

### 3.2 rTorrent tuning via YAML (`tuning:` block)
**As an** operator, **I want** rtorrent runtime settings declared in YAML and applied on connect **so that** my tuning survives daemon restarts without editing `.rtorrent.rc`.

Acceptance criteria:
- The `tuning:` block supports at minimum the network/bandwidth/queue keys listed in Epic 8 (port range, encryption, DHT mode, PEX, throttles, peer/upload limits, etc.), each mapping to a documented rtorrent key.
- On (re)connect, each declared key is applied via its `*.set` method; per-key failures are logged and reported, not fatal.
- Keys absent from YAML are left untouched on the daemon.
- Edits saved from the Settings UI update both the live daemon and the YAML file atomically (write temp + rename), preserving unrelated keys; document that comments in the managed block are not preserved.
- File-watch (or SIGHUP) reload: changed tuning values are re-applied without restart.

---

## Epic 4 — API, WebSocket & auth

### 4.1 REST API
**As the** frontend, **I want** JSON endpoints for all actions and snapshots **so that** every UI mutation has a home.

Acceptance criteria:
- Endpoints: session snapshot; torrent actions (start/pause/stop/recheck/remove/remove-with-data/set-label/move-data/priority) accepting a hash list; add-torrent (magnet list + multipart `.torrent` upload with options: destination, label, start, skip-hash-check, sequential); detail fetch per hash; settings get/save; stats/history.
- Batch actions are per-hash atomic: the response reports per-hash success/failure so partial failures surface in the UI.
- Errors return structured JSON (`code`, `message`); rtorrent faults pass their message through.
- Remove-with-data refuses paths outside configured download directories (defense against a corrupted base path).

### 4.2 WebSocket delta stream
**As the** frontend, **I want** a push stream of deltas **so that** the table ticks live without request overhead.

Acceptance criteria:
- On connect: full snapshot (torrents, globals, sidebar aggregates, history), then deltas each poll cycle; message schema is versioned.
- Client can subscribe/unsubscribe to a focused hash to receive detail (files/peers/trackers) updates for it.
- Heartbeat ping/pong; the client's `hidden` signal (tab not visible) pauses detail subscriptions and downgrades to a slow keepalive, resuming with a fresh snapshot on visibility.
- Connection status transitions (rtorrent lost/recovered) are pushed as events.

### 4.3 Basic auth (single user)
**As an** operator, **I want** credential-gated access **so that** the console isn't open to the network.

Acceptance criteria:
- Credentials come from YAML (username + bcrypt hash); every API route, the WebSocket upgrade, and the embedded app itself require auth.
- Browser flow works without JS gymnastics: HTTP Basic challenge (or a minimal login form issuing an HttpOnly session cookie — pick one, document it).
- Failed attempts are rate-limited and logged with source IP; credentials never appear in logs.
- Docs state the TLS expectation (terminate TLS in front, or configure the built-in listener with cert/key from YAML).

---

## Epic 5 — Main window (design frame 1a)

The operator's home: scan, filter, select, act. All measurements per the handoff README.

### 5.1 App shell: top bar, status bar
**As a** user, **I want** the frame chrome with live global stats **so that** session health is visible at a glance.

Acceptance criteria:
- Top bar (44px): logo + wordmark, global down (accent) / up (cyan) rates, 170×26 sparkline fed by the history buffer, filter field (240px, placeholder "Filter torrents…"), accent "+ Add torrent" button, ⚙ button routing to Settings.
- Status bar (26px): connection dot (accent connected / `#e0705a` disconnected), `rtorrent x.y.z / libtorrent x.y.z`, torrent counts, DHT node count, port status, session ratio, uptime — all live.
- On lost connection: dot goes red, a persistent bar above the toolbar reads "Lost connection to rTorrent — retrying…", destructive actions disable; everything reverts on reconnect.

### 5.2 Torrent table
**As a** user, **I want** the 12-column dense table **so that** I can scan hundreds of torrents.

Acceptance criteria:
- Fixed layout, exact column set/widths/alignments from the handoff (Name fluid; checkbox 26 … Tracker 120); 30px rows, alternating `#101214`/`#121417`, 28px header.
- Progress cell: 12px trough, centered overlaid percentage (one decimal below 100%, `100%` at complete), blue/green/stopped-grey fill rule.
- Cell formatting per handoff: status colors by state, seeds/peers `38 / 112` vs `— / 44`, down/up in accent/cyan with `—` dimmed, ETA forms (`4m 12s`, `2d 04h`, `∞`, `—`), ratio 2-decimals with ≥2.00 brightening, tinted label chips, `12:04 today` vs `Aug 30` added dates, hostname-only tracker.
- Renders 500+ rows at 60fps while ticking (virtualized scrolling or measured-adequate direct rendering — decision recorded); row identity keyed by hash so sorts don't remount rows.
- Live updates in place: progress width transitions `300ms linear`; rate text swaps without tweening; row insert/remove uses a 120ms fade.
- First load shows 8 shimmer skeleton rows; empty filter result shows "No torrents match this filter" + accent "Clear filters" action.

### 5.3 Selection model
**As a** user, **I want** desktop-grade multi-select **so that** bulk actions are fast.

Acceptance criteria:
- Click selects one and focuses it into the detail panel; Ctrl/Cmd-click toggles; Shift-click range-selects from anchor; Ctrl/Cmd-A selects all visible; checkbox column mirrors selection.
- Selected rows get the accent-tint background; toolbar readout shows `N selected · M of T shown` (tabular numerals).
- With multi-selection the detail panel keeps the most recently clicked row.
- Selection survives live reorders/updates (keyed by hash) and is pruned when torrents disappear.

### 5.4 Sorting & filtering
**As a** user, **I want** header sorting and intersecting filters **so that** I can isolate any slice of the session.

Acceptance criteria:
- Header click sorts asc → desc with an accent caret; default sort Added desc; ties break on name; sort persists (localStorage).
- Sidebar status/label/tracker filters and the text filter intersect (AND); text match is case-insensitive substring on name, debounced ~150ms; `/` focuses the filter field.
- Counts in the toolbar readout and sidebar stay correct as filters combine.

### 5.5 Sidebar
**As a** user, **I want** status/label/tracker filter groups with counts **so that** the session's shape is always visible.

Acceptance criteria:
- Three captioned groups (Status, Labels, Trackers) with live counts; labels show their 7×7 color squares (colors from YAML config); trackers list is derived from session hosts, truncating with ellipsis.
- Active item: accent tint background + 2px accent left border; inactive items reserve the 2px so text doesn't shift; clicking All / the active item clears that group's filter.
- Footer pinned to bottom: default volume path, `used / total`, and a 4px accent-fill usage bar, live from volume stats.

### 5.6 Action toolbar & context menu
**As a** user, **I want** transport and management actions on the selection **so that** the console is operable without leaving the table.

Acceptance criteria:
- Toolbar: ▶ Start, ❙❙ Pause, ■ Stop (accent glyphs), divider, Force recheck, Set label, Move data, Priority ↑/↓, Remove; all disabled (dimmed per spec) with empty selection; each applies to the whole selection via the batch API.
- Transport actions apply optimistically (status text flips immediately) and revert with a toast on RPC error.
- Right-click context menu at cursor, clamped to viewport, exact item order from the handoff incl. shortcut hints; right-click outside the selection replaces it, inside keeps it; "Set label ▸" opens a submenu of existing labels + "New label…"; dismiss on Escape/outside-click/scroll.
- Remove and Remove + data confirm first; the data variant names the path and torrent count; Copy magnet link builds the magnet URI from hash + name + trackers.
- Force recheck confirms when the selection includes anything downloading; Move data requires stopped torrents, opens a path picker (configured directories + free text), and performs `d.directory.set` + restart per the handoff.

---

## Epic 6 — Detail panel (frames 1a + 1b)

288px bottom panel: Files, Peers, Trackers, Transfer, Pieces.

### 6.1 Panel shell & facts column
**As a** user, **I want** the tabbed panel bound to the focused torrent **so that** deep state is one glance away.

Acceptance criteria:
- 32px tab strip (Files active by default) with the focused torrent's name right-aligned, truncating at 420px; active tab gets accent underline per spec.
- Facts column (300px): Hash, Downloaded, Uploaded, Ratio, Pieces, Peers, Down/Up rate (accent/cyan), Path, Added, Private — live-updating, values truncate.
- Detail data loads lazily per focused hash (WebSocket subscription); switching focus never blocks the table; a brief skeleton shows while loading.

### 6.2 Files tab
**As a** user, **I want** a file tree with per-file priority **so that** I can shape what downloads.

Acceptance criteria:
- Tree built from file paths; depth via 0/14/28px indents with ▾/▸/· glyphs; directories collapsible, aggregating size and progress; skipped entries dimmed.
- Columns File/Size/Progress/Done/Priority at spec widths; 26px rows; 10px progress bars with the blue/green rule.
- Priority chips (High/Normal/Skip) cycle on click and persist via `f.priority.set` (directory click applies to all children); the chip updates optimistically and reverts on error.

### 6.3 Peers tab
**As a** user, **I want** the live peer list **so that** I can see who I'm connected to.

Acceptance criteria:
- Header `Peers — N connected`; columns IP/Port/Client/Have/Down/Up/Flags at spec widths; 27px rows; "Have" as a 9px cyan-fill trough; rates follow accent/cyan/dimmed-`—` rules; rTorrent flag letters in the Flags column.
- List updates on the detail interval without scroll jumping (keyed by ip:port).

### 6.4 Trackers tab
**As a** user, **I want** tracker status and controls **so that** I can debug announces.

Acceptance criteria:
- Rows: status dot (accent working / `#c9a86a` timed out / cyan for `[DHT]` and `[Peer exchange]` pseudo-rows), URL, status text, seeds/leechers, next-announce countdown ticking client-side.
- Footer actions: Add tracker (prompt for URL, validated), Force reannounce (`d.tracker_announce`), Remove (destructive styling, confirms).

### 6.5 Transfer & Pieces tabs
**As a** user, **I want** transfer totals and a piece map **so that** the remaining tabs aren't stubs.

Acceptance criteria:
- Transfer: session + total transferred, chunk size/count/done, and per-torrent throttle assignment if available.
- Pieces: a compact canvas/SVG grid of chunk completion (done green, in-progress blue, missing trough color) that handles 10k+ pieces by bucketing; updates live.

---

## Epic 7 — Add torrent (frame 1c)

### 7.1 Add-torrent modal
**As a** user, **I want** to add by magnet/URL or file with options **so that** intake matches the design.

Acceptance criteria:
- 660px modal per spec: segmented Magnet/URL vs .torrent file; textarea accepts one magnet/URL per line; validation flags bad lines inline in `#e0705a` (accepts `magnet:?xt=urn:btih:` and http(s) `.torrent` URLs); Add stays disabled until ≥1 valid input.
- Dropzone accepts multi-file drop and click-to-browse; queued filenames listed; dragover highlights border/text in accent; non-`.torrent` files rejected with a message.
- Options row: Destination (defaults from YAML, per-label override applies when a label is picked), Label select (configured labels + free entry), Start immediately (default on), Skip hash check, Sequential download.
- Submit calls the add API (`load.*` variants per options); per-item results reported — successes close into a toast, failures stay listed in the modal; new torrents appear in the table within one poll cycle with the label applied.
- Opens from the top-bar button and via drag-anywhere onto the table (drop opens the modal pre-populated); Esc/✕/Cancel close it.

---

## Epic 8 — Settings (frame 1d) — ruTorrent-class coverage

No ruTorrent import; instead, match its breadth of daemon tuning. YAML is the store (Epic 3.2); this epic is the UI over it. Every field shows its underlying rtorrent key in the 11px hint line, per the design.

### 8.1 Settings shell
**As a** user, **I want** the nav + form layout from the design **so that** settings feel native to the console.

Acceptance criteria:
- 180px nav (General · Connection · Bandwidth · Queue · Directories · Labels · Interface · Advanced) with the accent active state; content on the `230px 1fr` grid with label + key hint left, control + unit hint right.
- Dirty tracking: Save/Revert disabled until a change exists; leaving with dirty state warns; Revert restores loaded values.
- Save writes changed keys sequentially via `*.set`, persists them to YAML, and reports per-key partial failure inline; numeric validation (port 1–65535, rates ≥ 0, counts ≥ 0) blocks save with inline errors.

### 8.2 Connection & network tuning (the priority section)
**As an** operator, **I want** ruTorrent-level network controls **so that** I can tweak connectivity without touching `.rtorrent.rc`.

Acceptance criteria — fields (each mapped to its rtorrent key, current value read from the daemon):
- Listening port range (`network.port_range`) and port randomization (`network.port_random`).
- Open port check indicator (reuses the status-bar port probe) next to the port field.
- Encryption policy (`protocol.encryption.set`): none / allow / require / require RC4 presented as the design's enum select.
- DHT mode (`dht.mode.set`: auto/on/off/disable) + DHT port (`dht.port`); UDP tracker support (`trackers.use_udp.set`); Peer exchange (`protocol.pex.set`).
- IP to report to trackers (`network.local_address`) and bind address (`network.bind_address`).
- HTTP settings for tracker/scrape requests: max open HTTP (`network.http.max_open`), CA bundle/path passthroughs if set.
- Peer connection limits: min/max peers (`throttle.min_peers.normal`/`max_peers.normal`), seeding variants (`*.seed`), max uploads (`throttle.max_uploads`), global connection cap (`network.max_open_sockets`), max open files (`network.max_open_files`).

### 8.3 Bandwidth & queue
**As an** operator, **I want** rate and scheduling controls **so that** the box behaves on a shared line.

Acceptance criteria:
- Global down/up limits (`throttle.global_down.max_rate`, `throttle.global_up.max_rate`, KB/s, 0 = unlimited) with the design's unit hints.
- Named alternative throttle group definition (e.g. "slow") assignable per-torrent from the context menu (stretch: mark clearly if deferred).
- Queue: max downloads/uploads active (`throttle.max_downloads.global`, `throttle.max_uploads.global`), and stop-on-ratio rules expressed as YAML-driven schedule commands with UI (target ratio, min upload) — documented limitations if rtorrent's `group.seeding.*` is used.

### 8.4 Directories, labels, interface, advanced
**As an** operator, **I want** the remaining sections **so that** coverage is complete.

Acceptance criteria:
- Directories: default download dir (`directory.default.set`), session dir (read-only display), optional watch directory with auto-load + per-watch label, per-label destination map (YAML) editable here.
- Labels: create/rename/delete labels, assign colors (drives sidebar squares and chips); deleting a label offers to clear or reassign affected torrents.
- Interface: accent color picker (the four design alternates + custom), default sort, visible columns, poll interval override, date/rate format toggles — persisted in YAML under `ui:`.
- Advanced: read-only view of every `tuning:` key applied and its live daemon value, plus a raw "execute XML-RPC method" box gated behind a confirm (operator escape hatch, logged).

---

## Epic 9 — Disk & global stats (frame 1e)

### 9.1 Stats page
**As a** user, **I want** the throughput graph and stat cards **so that** I can see session health over time.

Acceptance criteria:
- Five stat cards per spec (Download / Upload / Session ratio / Torrents / Disk free) with 22px tabular values, accent/cyan coloring, live sub-lines.
- Throughput graph: 60-minute rolling window from the server history ring; flat 2px polylines (download accent + tinted area fill, upload cyan), three gridlines, no axis labels; header shows the window peak; hover shows a readout of both rates at the cursor time; survives reconnects (history refetched).
- Sparkline in the top bar shares the same history source and styling.

### 9.2 Volumes & space by label
**As a** user, **I want** disk pressure and per-label usage **so that** I know what's eating space.

Acceptance criteria:
- Volumes: one row per YAML-configured mount — path, `used of total`, 8px bar colored by pressure (accent normal, `#e0705a` ≥90%, cyan for scratch-class), from real statfs data refreshed on a slow interval.
- Space by label: sums torrent sizes by label, bars scaled relative to the largest, label colors from config; unlabeled bucket included.

---

## Epic 10 — Keyboard, states & responsive polish

### 10.1 Keyboard shortcuts
**As a** power user, **I want** the documented shortcuts **so that** the console works hands-on-keyboard.

Acceptance criteria:
- `/` focuses filter · `Space` start/pause selection · `Del` remove (confirm) · `⇧Del` remove + data (confirm) · `↑/↓` move selection (Shift extends) · `Esc` closes popovers/modals/clears filter focus · Ctrl/Cmd-A select all visible.
- Shortcuts are suppressed while typing in inputs; context-menu hints match actual bindings.

### 10.2 Responsive behavior
**As a** user on a smaller window, **I want** graceful degradation **so that** the console stays usable ≥900px.

Acceptance criteria:
- Below ~1100px the sidebar collapses to an icon rail or dropdown filter; below ~900px the Tracker, Added, then Ratio columns drop in that order; no horizontal body scroll at any width ≥900px.
- No mobile layout is designed or required; document the floor.

### 10.3 Toasts & error surfacing
**As a** user, **I want** consistent failure feedback **so that** optimistic UI never lies silently.

Acceptance criteria:
- A single toast system reports action failures (with the rtorrent fault message), partial batch failures ("3 of 5 started"), and settings save issues; toasts are dismissible and non-blocking.
- Optimistic reverts always pair with a toast naming the affected torrent(s).

---

## Epic 11 — Packaging, docs & release

### 11.1 Distribution
**As an** operator, **I want** turnkey deployment **so that** install is one binary + one YAML.

Acceptance criteria:
- CI builds static binaries for linux/amd64 and linux/arm64 (darwin optional); frontend embedded; version stamped.
- Dockerfile (scratch/distroless) and a docker-compose example pairing with an rtorrent container over a shared SCGI socket; systemd unit example in docs.
- README documents: rtorrent prerequisites (`scgi_local`/`scgi_port` line), the full annotated YAML reference, auth + TLS guidance, and upgrade notes.

### 11.2 Test & verification baseline
**As a** developer, **I want** confidence in the rtorrent seam **so that** refactors don't break the daemon contract.

Acceptance criteria:
- Unit tests: SCGI/XML-RPC codec, config load/validate/save round-trip, delta computation, ETA/percent derivation, magnet validation.
- An integration test target that runs against a real rtorrent (docker) exercising: list, add (magnet + file), start/stop, label set, file priority, remove; CI-runnable.
- Frontend: component tests for table formatting rules (progress colors, ETA/date/rate formats) and the selection model; one e2e smoke (load → filter → select → start/stop → add) against the integration stack.

---

## Suggested sequencing

1. **Epics 1–2** (foundation + rtorrent client/poller) — everything depends on these.
2. **Epics 3–4** (config, API, WebSocket, auth) — the contract the frontend consumes.
3. **Epic 5** (main window) — the bulk of UI value; ship a usable console at the end of this.
4. **Epics 6–7** (detail panel, add torrent) — completes daily-driver workflows.
5. **Epic 8** (settings) — network tuning coverage; YAML plumbing already exists from Epic 3.
6. **Epics 9–11** (stats, polish, packaging) — finish and release.

Out of scope for v1 (recorded for later): multi-user mode, ruTorrent plugin equivalents (RSS/autodl, unpack), mobile layout, per-torrent scheduled throttles UI beyond the named-group basics.
