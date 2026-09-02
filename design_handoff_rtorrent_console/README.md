# Handoff: rTorrent Web Console

## Overview

A browser front end for [rTorrent](https://github.com/rakshasa/rtorrent), in the spirit of ruTorrent but built as a modern dense "pro tool" interface: a single-window, desktop-first console for managing a large torrent session (hundreds of torrents) over rTorrent's XML-RPC / SCGI interface.

The design covers five surfaces:

1. **Main window** — status/label/tracker sidebar, 12-column torrent table with multi-select, action toolbar, right-click context menu, bottom detail panel, status bar.
2. **Detail panel — Peers & Trackers** tabs.
3. **Add torrent** dialog (magnet/URL and .torrent file).
4. **Settings** — mapped to real rTorrent config keys.
5. **Disk & global stats** — throughput graph, stat cards, volume and per-label space usage.

## About the Design Files

The file in this bundle (`rTorrent Console.dc.html`) is a **design reference created in HTML** — a static prototype showing intended look, layout, and information hierarchy. It is **not production code to copy directly**.

The task is to **recreate these designs in the target codebase's existing environment** (React, Vue, Svelte, etc.) using its established patterns, component library, routing, and state management. If no environment exists yet, choose an appropriate stack — a Vite + React + TypeScript SPA talking to rTorrent's XML-RPC endpoint (via an SCGI/HTTP proxy such as nginx `scgi_pass` or a small Node/Go backend) is a reasonable default — and implement the designs there.

Notes on the prototype's structure, so you can read it:

- It uses a lightweight template runtime (`support.js`). Markup lives in the `<x-dc>` block; all mock data (torrents, files, peers, trackers, settings, stats) lives in the `renderVals()` method of the `Component` class near the bottom of the file. **That data block is the fastest way to see every field the UI expects.**
- All styling is inline. Do not port the inline styles verbatim — read the values out of them and express them in the target codebase's styling system.
- The five surfaces are laid out side by side on one canvas as labeled frames (`1a`–`1e`) purely for review. In the real app they are: one main route, a tabbed panel, a modal, a settings route, and a stats route.

## Fidelity

**High-fidelity.** Colors, typography, spacing, row heights, and column widths are final and should be reproduced closely. Interaction *behavior* is documented below but is not implemented in the prototype (it is static — no live data ticking, no click handlers).

---

## Design Tokens

### Colors

| Token | Hex | Use |
|---|---|---|
| `bg/canvas` | `#0b0c0d` | Page backdrop outside the app window |
| `bg/app` | `#101214` | App window base, odd table rows |
| `bg/row-alt` | `#121417` | Alternating table rows, action toolbar |
| `bg/panel` | `#0e1012` | Sidebar, detail panel, stat sub-panels, inputs |
| `bg/chrome` | `#15181b` | Top bar, table header, tab strip, status bar, cards |
| `bg/elevated` | `#1a1e22` | Context menu, popovers |
| `bg/control` | `#1b1f23` | Toolbar buttons |
| `bg/control-active` | `#1f2429` | Selected segmented-control segment |
| `bg/track` | `#1e2226` | Progress-bar troughs, neutral chips |
| `bg/track-dim` | `#191c20` | Row bottom border, skipped-file chips |
| `border/strong` | `#33383e` | Modal + popover borders, dashed dropzone |
| `border/default` | `#26292e` | Panel and section dividers |
| `border/control` | `#2c3035` | Buttons, inputs |
| `border/subtle` | `#1c1f23` | Table column dividers |
| `border/row` | `#191c20` | Main table row separators |
| `border/row-dim` | `#17191c` | Detail/peer table row separators |
| `text/primary` | `#e8eaed` | Headings, emphasized values, torrent names in focus |
| `text/body` | `#d6d9dd` | Default body text, table cells |
| `text/secondary` | `#b6bbc1` | Sidebar items, secondary labels |
| `text/muted` | `#9aa0a7` | Numeric cells, column headers |
| `text/dim` | `#8b9199` | Inactive tabs, complete-status text |
| `text/faint` | `#7c828a` | Meta text, stopped status |
| `text/fainter` | `#6b7178` | Hints, timestamps, tracker column |
| `text/ghost` | `#5d6369` | Section captions, placeholder text |
| `state/disabled` | `#4b5158` | Em-dash cells, stopped bar fill |
| **`accent`** | **`#f59e0b`** | Primary action, active filter, download rate, active status, focus |
| `accent/tint` | `rgba(245,158,11,0.09–0.12)` | Selected row bg, active sidebar item, label chips, graph fill |
| `accent/text` | `#f8c777` | Accent text on tinted chips, link hover |
| `rate/up` | `#22d3ee` | Upload rate, peer "have" bars, PEX/DHT rows |
| `rate/up-text` | `#8ae4ef` | Upload-ish text on tinted chips |
| `progress/active` | `#2f9dff` | Progress bars **below 100%** |
| `progress/complete` | `#3fb950` | Progress bars **at 100%** |
| `status/error` | `#e0705a` | Tracker error status, destructive menu items, near-full volume |
| `status/warn` | `#c9a86a` | Timed-out tracker, `archive` label |
| `label/apps` | `#a78bfa` | `apps` label |

Notable rule: **the accent is deliberately not used for progress.** Progress bars are blue while incomplete and green at 100%, independent of the accent. Download rates use the accent; upload rates use cyan.

The prototype exposes `accent` as a single tweakable prop (`#f59e0b` default, alternates `#e5484d`, `#2f9dff`, `#3fb950`). If theming is in scope, mirror that: one accent variable, everything else fixed.

### Typography

- **Family:** `"IBM Plex Sans", system-ui, sans-serif` (Google Fonts, weights 400/500/600). One family only — no monospace.
- **All numeric cells, counts, sizes, rates, ratios and timestamps use `font-variant-numeric: tabular-nums`.** This is essential for a dense table; do not omit it.
- Scale in use:

| Role | Size / weight | Notes |
|---|---|---|
| Stat card value | 22px / 600, line-height 1.1 | |
| Frame headings (review only) | 26px / 600, 15px / 500 | Not part of the app |
| Section title (Settings) | 14px / 600 | |
| App name, modal title | 13px / 600, letter-spacing 0.04em | |
| Table cells, sidebar items | 12.5px / 400 | |
| Buttons, inputs, tabs, detail cells | 12px / 400 | |
| Detail facts, tracker rows | 11.5px / 400 | |
| Column headers | 11px / 500, uppercase, letter-spacing 0.05em | |
| Status bar, hints, meta | 11px / 400 | |
| Chips (label, priority) | 10.5px / 400 | |
| Section captions (sidebar, cards) | 10–10.5px / 400, uppercase, letter-spacing 0.10–0.14em | |
| Progress-bar inline % | 9.5px / 400, letter-spacing 0.02em | |

### Spacing, radius, shadow

- Spacing steps used: 4, 6, 8, 10, 12, 14, 16, 18, 20px. Panel padding 12–20px; cell padding `0 8px` (main table) / `0 10px` (peer table).
- Radius: `2px` chips, progress bars, dots-as-squares; `3px` buttons, inputs, menu items; `4px` cards, dropzone, popovers; `6px` app window and large panels; `50%` status dots.
- Shadows: app window `0 24px 60px rgba(0,0,0,0.5)`; modal `0 24px 60px rgba(0,0,0,0.6)`; context menu `0 18px 40px rgba(0,0,0,0.6)`.
- Fixed heights (dense — keep these): top bar 44, action toolbar 36, table header 28, **table row 30**, detail tab strip 32, detail header row 24, detail file row 26, peer header 26, peer row 27, status bar 26, sidebar item 26, buttons 24–28, inputs 26–28, context-menu item 26.

---

## Screens / Views

### 1a — Main window

**Purpose:** the operator's home. Scan session state, filter, multi-select, act.

**Layout:** app window `1600px` wide (fluid in production; treat 1600 as the design width), `border-radius 6px`, `1px solid #26292e`, vertical flex:

```
┌ top bar (44px) ─────────────────────────────────────────────┐
├ action toolbar (36px) ──────────────────────────────────────┤
├ sidebar (196px) │ torrent table (flex, scrolls)             │
│                 ├ detail panel (288px)                      │
│                 ├ status bar (26px)                         │
└─────────────────────────────────────────────────────────────┘
```

The sidebar spans the full height of the content area; the table / detail / status bar stack sits to its right.

#### Top bar (44px, `#15181b`, bottom border `#26292e`, padding `0 12px`, gap 16)

Left to right:
- Logo mark: 16×16, `2px solid` accent, radius 2. Wordmark "rTorrent" 13px/600, letter-spacing 0.04em, `#e8eaed`.
- Vertical divider 1×22 `#26292e`.
- Global rates, 12px `#9aa0a7`: `▼ 41.2 MB/s` (glyph accent, unit `#6b7178`), `▲ 12.8 MB/s` (glyph `#22d3ee`).
- Mini sparkline: 170×26 box, `1px solid #26292e`, radius 3, bg `#0e1012`, 2px inner padding. SVG `viewBox="0 0 340 44"`, `preserveAspectRatio="none"`, two `polyline`s (`stroke-width 2`): download in accent, upload in `#22d3ee`.
- Spacer.
- Filter field: 240×26, `1px solid #26292e`, radius 3, bg `#0e1012`, padding `0 10px`, gap 8. `⌕` glyph + placeholder "Filter torrents…" both `#5d6369`, 12px.
- Primary button "**+ Add torrent**": 26px tall, padding `0 12px`, radius 3, bg accent, text `#0b0c0d` 12px/600.
- Icon button `⚙`: 28×26, `1px solid #26292e`, radius 3, `#9aa0a7`.

#### Action toolbar (36px, `#121417`, bottom border `#26292e`, padding `0 10px`, gap 4)

- Three transport buttons with accent glyphs: `▶ Start`, `❙❙ Pause`, `■ Stop`. Each: 24px tall, padding `0 9px`, radius 3, bg `#1b1f23`, `1px solid #2c3035`, label 12px `#d6d9dd`, glyph 10px accent, gap 6.
- 1×18 divider `#26292e` with 6px side margins.
- Secondary buttons, same chrome without glyphs: `Force recheck`, `Set label`, `Move data`, `Priority ↑`, `Priority ↓`, `Remove`.
- Right-aligned selection readout, 11px `#6b7178`, tabular: `3 selected · 14 of 412 shown`.

Buttons are disabled (reduce text to `#5d6369`, border `#26292e`) when the selection is empty.

#### Sidebar (196px, `#0e1012`, right border `#26292e`, `10px 0` padding)

Three filter groups, each preceded by a caption (10px, uppercase, letter-spacing 0.14em, `#5d6369`, padding `0 12px 6px`). Items: 26px tall, padding `0 12px`, 12.5px `#b6bbc1`, label flex-1, right-aligned count 11px `#6b7178` tabular. The active item gets bg `accent/tint` and a `2px` left border in the accent (inactive items carry a transparent 2px left border so text doesn't shift).

- **Status:** All 412 · **Downloading 11 (active)** · Seeding 289 · Stopped 96 · Checking 2 · Errored 14
- **Labels** (7×7 radius-2 color square before the name): iso 184 (accent) · archive 62 `#c9a86a` · kernel 38 `#22d3ee` · apps 71 `#a78bfa` · media 44 `#e0705a` · unlabeled 13 `#4b5158`
- **Trackers** (name truncates with ellipsis): academictorrents.com 88 · torrent.ubuntu.com 41 · bittorrent.debian.org 36 · linuxtracker.org 29 · kiwix.org 12

Dividers between groups: 1px `#26292e`, margin `10px 12px`.

Sidebar footer, pinned to the bottom above a 1px top border, padding `10px 12px 0`: a row of `/mnt/data` / `3.1 / 8.0 TB` (11px `#7c828a`, tabular, space-between) over a 4px radius-2 trough `#1f2328` with a 39% accent fill.

#### Torrent table

`table-layout: fixed`, `border-collapse: collapse`, 12.5px, tabular numerals. Column widths (px; the Name column is the only fluid one):

| # | Column | Width | Align |
|---|---|---|---|
| — | checkbox | 26 | center |
| 1 | Name | flex | left |
| 2 | Size | 74 | right |
| 3 | Done (progress) | 112 | left |
| 4 | Status | 96 | left |
| 5 | Seeds/Peers | 66 | right |
| 6 | Down | 78 | right |
| 7 | Up | 78 | right |
| 8 | ETA | 64 | right |
| 9 | Ratio | 54 | right |
| 10 | Label | 88 | left |
| 11 | Added | 86 | right |
| 12 | Tracker | 120 | left |

- **Header row:** bg `#15181b`, 28px, 11px/500 uppercase letter-spacing 0.05em, Name `#c3c8ce` and the rest `#8b9199`, bottom border `#26292e`, right border `#1c1f23` per cell, `white-space: nowrap`. Clicking a header sorts (see Interactions); columns are resizable and reorderable if cheap to support.
- **Rows:** 30px. Backgrounds alternate `#101214` / `#121417`; a **selected** row is `rgba(245,158,11,0.09)`. Bottom border `#191c20`.
- **Checkbox cell:** 11×11 box, radius 2, `1px solid #3a3f45` unchecked / accent border + accent fill when checked.
- **Name:** `#e0e3e6`, single line, ellipsis.
- **Progress cell:** 12px tall trough, radius 2, bg `#1e2226`, fill left-aligned to the percentage; the percentage label is centered *over* the whole bar (absolutely positioned, 9.5px `#e8eaed`). Fill color: `#4b5158` if stopped **and** incomplete, `#3fb950` at 100%, else `#2f9dff`. Label reads `100%` when complete, otherwise one decimal (`61.4%`).
- **Status:** `#e0705a` on error, `#7c828a` when stopped/queued, `#8b9199` when complete/seeding, accent while active. Values seen: `Downloading`, `Seeding`, `Stopped`, `Queued`, `Checking 42%`, `Tracker error`.
- **Seeds/Peers:** `38 / 112` while downloading, `— / 44` while seeding, `#9aa0a7`.
- **Down / Up:** accent / `#22d3ee` when non-zero; `—` in `#4b5158` when idle.
- **ETA:** `4m 12s`, `2d 04h`, `∞`, or `—`; `#9aa0a7`.
- **Ratio:** two decimals; `#9aa0a7` at ≥ 2.00, `#7c828a` below.
- **Label chip:** 16px tall, padding `0 6px`, radius 2, 10.5px. Tinted per label family (e.g. iso = accent tint + `#f8c777` text; kernel = cyan tint + `#8ae4ef`; archive = `rgba(217,175,106,0.12)` + `#c9a86a`); default neutral `#1e2226` + `#9aa0a7`.
- **Added:** `12:04 today` for today, otherwise `Aug 30`; `#6b7178`.
- **Tracker:** hostname only, ellipsis, `#6b7178`.

The prototype ships 14 sample rows (Linux ISOs, kernel tarballs, public archives) covering every status, label, and progress state — use them as your fixture set.

#### Context menu

Shown in the prototype as an open popover over the table. 196px wide, bg `#1a1e22`, `1px solid #33383e`, radius 4, 4px padding, shadow `0 18px 40px rgba(0,0,0,0.6)`. Items 26px, padding `0 8px`, radius 3, 12px `#d6d9dd`; hovered/active item bg `accent/tint` with `#e8eaed` text; right-aligned shortcut hint 11px `#5d6369`. Separators: 1px `#2a2e33`, margin `4px 6px`.

Order: Start · Pause · Stop — Force recheck · Set label ▸ · Move data… · Copy magnet link — Remove (`Del`) · **Remove + data** (`⇧Del`, text `#e0705a`).

#### Detail panel (288px)

- Tab strip 32px, bg `#15181b`, bottom border `#26292e`. Tabs: **Files** (active), Peers, Trackers, Transfer, Pieces. Tab: padding `0 14px`, 12px; active = `#e8eaed` + bg `#0e1012` + `2px` accent bottom border; inactive = `#8b9199` + transparent bottom border; 1px `#1c1f23` right border. Right side shows the focused torrent's name, 11.5px `#7c828a`, max-width 420px, ellipsis.
- Body splits: file table (flex) | facts column (300px, left border `#26292e`, padding `10px 12px`, 7px gap).
- **File table** columns: File (flex) · Size 80 · Progress 110 · Done 70 · Priority 120. Header 24px, 10.5px uppercase `#5d6369`, bottom border `#26292e`. Rows 26px, separator `#17191c`. Tree depth via left padding **0 / 14 / 28px** with a leading glyph (`▾` expanded dir, `▸` collapsed dir, `·` file); skipped entries dim to `#7c828a`. Progress bar 10px tall, same blue/green rule. Priority chip 16px, padding `0 7px`, radius 2, 10.5px: High = accent tint + `#f8c777`, Normal = `#1e2226` + `#9aa0a7`, Skip = `#191c20` + `#7c828a`.
- **Facts column** — key/value rows, 11.5px, key `#6b7178`, value `#b6bbc1`/`#d6d9dd` (rates in accent/cyan), value truncates: Hash · Downloaded · Uploaded · Ratio · Pieces · Peers · Down rate · Up rate · Path · Added · Private.

#### Status bar (26px, `#15181b`, top border `#26292e`, 11px `#7c828a`, tabular, gap 18)

Left: 6px accent dot + `rtorrent 0.15.4 / libtorrent 0.14.4` · `412 torrents · 289 seeding · 11 downloading` · `DHT 1,204 nodes` · `Port 51413 open`. Right: `Session ratio 2.41` · `Uptime 41d 06h`. The dot is the XML-RPC connection indicator: accent = connected, `#e0705a` = disconnected.

### 1b — Detail panel: Peers & Trackers

Same panel shell; shown as two standalone cards in the prototype.

**Peers.** Header 30px, `#15181b`, 11px uppercase letter-spacing 0.10em `#9aa0a7`: `Peers — 34 connected`. Columns: IP 130 · Port 66 (right) · Client (flex) · Have 110 · Down 78 (right) · Up 78 (right) · Flags 70 (right). Rows 27px, separator `#17191c`, 12px tabular. "Have" is a 9px trough with a `#22d3ee` fill. Down/Up follow the accent/cyan + `#4b5158`-for-`—` rule. Flags are rTorrent's peer flag letters (`EIX`, `EU`, `EH`…) in `#6b7178`.

**Trackers.** Rows are flex, padding `9px 12px`, separator `#17191c`: 6px status dot (accent working / `#c9a86a` timed out / `#22d3ee` for DHT & PEX pseudo-rows) · URL (flex, 12px `#d6d9dd`, ellipsis) · status 120px right-aligned 11px (`#f8c777` working, `#c9a86a` timed out, `#8ae4ef` enabled) · seeds/leechers 96px · next announce 86px, both 11px `#6b7178` tabular. Pseudo-rows `[DHT]` and `[Peer exchange]` sit in the same list. Footer strip (padding `10px 12px`, gap 6) with `Add tracker`, `Force reannounce`, and a destructive `Remove` (text `#d97757`).

### 1c — Add torrent (modal)

660px wide, bg `#15181b`, `1px solid #33383e`, radius 6, shadow `0 24px 60px rgba(0,0,0,0.6)`.

- Title bar 38px, padding `0 14px`, bottom border `#26292e`: "Add torrent" 13px/600 `#e8eaed`; `✕` 14px `#6b7178`.
- Body padding `16px 14px`, 14px gap:
  - Segmented control, `1px solid #2c3035`, radius 3, `width: fit-content`: **Magnet / URL** (active, bg `#1f2429`, `#e8eaed`) | **.torrent file** (`#8b9199`, 1px left border). Both padding `5px 14px`, 12px.
  - Field group: caption "MAGNET LINKS — ONE PER LINE" (11px uppercase 0.08em `#6b7178`) over a 78px textarea, `1px solid #2c3035`, radius 3, bg `#0e1012`, padding `8px 10px`, 12px `#9aa0a7`, line-height 1.6; second line is placeholder `#5d6369`.
  - Dropzone: `1px dashed #33383e`, radius 4, padding 18, bg `#101214`, centered — "Drop .torrent files here" 12.5px `#b6bbc1` over "or click to browse" 11px `#5d6369`.
  - Two-column grid (12px gap): **Destination** (text input showing `/mnt/data/downloads`) and **Label** (select showing `iso` with a `▼` in `#5d6369`). Inputs 28px, `1px solid #2c3035`, radius 3, bg `#0e1012`, 12px `#d6d9dd`.
  - Checkbox row (20px gap): **Start immediately** (checked — 12×12 accent box), Skip hash check, Sequential download (unchecked — `1px solid #3a3f45`). Labels 12px `#b6bbc1`, gap 7.
- Footer: padding `12px 14px`, top border `#26292e`, bg `#121417`, right-aligned — `Cancel` (28px, `1px solid #2c3035`, radius 3, 12px `#b6bbc1`) and `Add` (28px, bg accent, `#0b0c0d` 12px/600, padding `0 16px`).

### 1d — Settings

1000×552 panel, radius 6, `1px solid #26292e`, bg `#101214`, split into a 180px nav and a content pane.

- **Nav** (`#0e1012`, right border `#26292e`, `10px 0`): items 28px, padding `0 14px`, 12.5px `#b6bbc1`; active ("Connection") = accent tint + 2px accent left border + `#e8eaed`. Sections: General · **Connection** · Bandwidth · Queue · Directories · Labels · Interface · Advanced (.rtorrent.rc).
- **Content** (padding `18px 20px`, 18px gap): title "Connection & bandwidth" 14px/600 `#e8eaed`, then rows on a `230px 1fr` grid with 16px gap. Left cell: label 12.5px `#d6d9dd` over the underlying rTorrent key in 11px `#6b7178`. Right cell: value control 26px, `1px solid #2c3035`, radius 3, bg `#0e1012`, 12px `#e8eaed`, tabular, `min-width` 100px (numbers) or 300px (enums), followed by an 11px `#6b7178` unit/hint.

| Label | Key shown | Value | Unit/hint |
|---|---|---|---|
| Listening port | `network.port_range` | 51413 | TCP + uTP |
| Global download limit | `throttle.global_down.max_rate` | 0 | KB/s · 0 = unlimited |
| Global upload limit | `throttle.global_up.max_rate` | 20480 | KB/s |
| Max peers per torrent | `throttle.max_peers.normal` | 200 | |
| Max uploads per torrent | `throttle.max_uploads` | 12 | |
| Encryption | `protocol.encryption.set` | Require outgoing, allow incoming | |
| DHT | `dht.mode.set` | Auto — off for private torrents | |
| Peer exchange | `protocol.pex.set` | Enabled | |

Footer pinned to the bottom above a 1px `#26292e` border: `Revert` (outline) and `Save` (accent).

### 1e — Disk & global stats

1240px panel, padding 18, 16px gap.

- **Stat cards:** 5-up grid, 12px gap. Card: `1px solid #26292e`, radius 4, bg `#15181b`, padding 12, 5px gap — caption 10.5px uppercase 0.10em `#6b7178`, value 22px/600 tabular (Download in accent, Upload in `#22d3ee`, rest `#e8eaed`), sub-line 11px `#6b7178`. Cards: Download `41.2 MB/s` / 11 active · Upload `12.8 MB/s` / 289 seeding · Session ratio `2.41` / 18.4 TB up / 7.6 TB down · Torrents `412` / 96 stopped · 14 errored · Disk free `4.9 TB` / of 8.0 TB across 3 volumes.
- **Throughput graph:** panel `1px solid #26292e`, radius 4, bg `#0e1012`, padding 12. Header row: "THROUGHPUT — LAST 60 MIN" 11px uppercase 0.10em `#9aa0a7`, legend `▼ download` (accent) and `▲ upload` (`#22d3ee`) 11px, right-aligned `peak 78.4 MB/s` 11px `#6b7178`. Chart: SVG `viewBox="0 0 1180 220"`, `preserveAspectRatio="none"`, 200px tall — three 1px `#1e2226` gridlines at y = 55/110/165, a download area `polygon` filled `rgba(245,158,11,0.12)`, then download and upload `polyline`s at `stroke-width 2`.
- **Bottom row:** two equal panels, 16px gap.
  - **Volumes:** per volume a space-between row (path 12px `#d6d9dd`, detail 12px `#7c828a`, tabular) over an 8px radius-2 trough `#1e2226` with a fill colored by pressure — `/mnt/data` 39% accent, `/mnt/archive` 93% `#e0705a`, `/mnt/scratch` 24% `#22d3ee`.
  - **Space by label:** rows of `7px color square · 96px name · flex 6px bar · 76px right-aligned size` (12px; size `#7c828a` tabular). Bars are scaled relative to the largest label: archive 9.84 TB (100%) · iso 4.10 TB (42%) · media 2.71 TB (28%) · apps 1.08 TB (11%) · kernel 402 GB (4%) · unlabeled 188 GB (2%).

---

## Interactions & Behavior

Not implemented in the prototype. Intended behavior:

**Selection**
- Click a row: select only it, and load it into the detail panel.
- Ctrl/Cmd-click: toggle a row. Shift-click: range-select from the anchor. Ctrl/Cmd-A: select all visible.
- The toolbar readout shows `N selected · M of T shown`; toolbar actions apply to the whole selection.
- With a multi-selection, the detail panel keeps showing the most recently clicked row.

**Context menu**
- Right-click opens at the cursor, clamped to the viewport. Right-clicking a row outside the current selection replaces the selection with that row; inside it, the selection is kept.
- "Set label ▸" opens a submenu of existing labels plus "New label…".
- "Remove" and "Remove + data" both require confirmation; the data variant names the path and torrent count in the dialog.
- Dismiss on Escape, outside click, or scroll.

**Sorting / filtering**
- Header click sorts asc, second click desc, with a caret in the accent next to the label. Default sort: Added desc. Ties break on name.
- Sidebar filters and the search field intersect (status AND label AND tracker AND text match on name).
- Text filter is case-insensitive substring on the torrent name, debounced ~150ms.

**Live data**
- Poll rTorrent (`d.multicall2`) on a ~2s interval for the visible set; pause polling when the tab is hidden.
- Rates, progress, ETA, peer counts, and the sparkline/graph update in place. Progress-bar width transitions `width 300ms linear`; rate text swaps without animation (no number tweening — it reads as jitter at this density).
- Row insert/remove: 120ms fade, no layout animation.

**Transport actions**
- Start/Pause/Stop map to `d.start` / `d.pause` / `d.stop`. Buttons apply optimistically: set status text immediately, revert on RPC error and surface a toast.
- Force recheck asks for confirmation when the selection includes anything currently downloading.
- Move data opens a path picker and requires the torrent to be stopped; do the move via `d.directory.set` + `d.start` after the transfer completes.

**Add torrent**
- Accepts one magnet/URL per line; validate the `magnet:?xt=urn:btih:` prefix or a `.torrent` URL, and mark bad lines inline in `#e0705a` under the field.
- The dropzone accepts multi-file drops and shows the queued filenames once files are chosen. Highlight on dragover: border and text move to the accent.
- `Add` is disabled until at least one valid input exists.

**Settings**
- Fields are dirty-tracked; `Save` and `Revert` are disabled until something changes. Numeric fields validate range (port 1–65535; rates ≥ 0) and show the error inline.
- Saving writes each changed key via its `*.set` XML-RPC method, sequentially, and reports partial failure per key.

**Keyboard**
- `/` focus filter · `Space` start/pause selection · `Del` remove · `⇧Del` remove + data · `↑/↓` move selection · `Esc` close popovers/modals.

**Empty / loading / error states**
- First load: render the table chrome with 8 shimmer rows (`#15181b` → `#191c20`).
- No torrents match: centered 12.5px `#7c828a` — "No torrents match this filter" with a "Clear filters" text button in the accent.
- RPC unreachable: status-bar dot goes `#e0705a`, a persistent bar above the toolbar reads "Lost connection to rTorrent — retrying…", and destructive actions disable.

**Responsive**
- Desktop-first; the design assumes ≥1280px. Below ~1100px collapse the sidebar to an icon rail or a dropdown filter; below ~900px drop the Tracker, Added, and Ratio columns in that order. No mobile layout is designed.

## State Management

Server-derived state (poll or subscribe; cache and dedupe):
- `torrents[]` — hash, name, sizeBytes, completedBytes, percent, state (`downloading|seeding|stopped|queued|checking|error`), checkingPercent, seeds, peers, downRate, upRate, etaSeconds, ratio, label, addedAt, trackerHost, isPrivate, basePath.
- `torrentDetail[hash]` — files[] (path, sizeBytes, completedChunks, percent, priority `0|1|2`), peers[] (ip, port, client, completedPercent, downRate, upRate, flags), trackers[] (url, group, status, seeds, leechers, nextAnnounceAt), transfer facts (downloadedBytes, uploadedBytes, chunkSize, chunkCount, chunksDone).
- `global` — downRate, upRate, sessionRatio, counts by state, dhtNodes, portStatus, uptime, version strings.
- `history` — rolling 60min rate samples for the graphs.
- `volumes[]`, `labelUsage[]`, `settings{}`.

Client state: `selection: Set<hash>`, `focusedHash`, `filters {status, label, tracker, query}`, `sort {column, dir}`, `columnWidths`, `detailTab`, `contextMenu {open, x, y}`, `modals {addTorrent, confirmRemove, movePath}`, `settingsDraft`, `connection {status, lastError}`, `accent`.

Notes: keep the poll response normalized by hash so row identity is stable across sorts; derive ETA and percent client-side when the daemon omits them; never block the table render on detail fetches (detail loads lazily per focused hash).

## Assets

None. No images, no icon font — every glyph in the design is a Unicode character: `▼ ▲ ▶ ❙❙ ■ ⌕ ⚙ ✕ ▸ ▾ · ↑ ↓ ❙ ∞`. Replace them with the target codebase's icon set (transport, gear, search, close, chevrons) if one exists; keep the arrows for rate direction only if they read clearly at 10–12px.

The only external dependency is the **IBM Plex Sans** webfont (Google Fonts, weights 400/500/600). Self-host it if the app must work offline.

The two SVG charts are drawn from data (`polyline`/`polygon` point strings generated in `renderVals()`); reimplement with whatever charting approach the codebase uses, keeping the flat 2px-stroke, gridline-only styling — no axis labels, no tooltips beyond a hover readout.

## Files

- `rTorrent Console.dc.html` — the full design: all five frames. Markup in the `<x-dc>` block; **all sample data in `renderVals()`** near the end of the file.
- `support.js` — the prototype's template runtime. Reference only; nothing in it should be ported.

Open the HTML file directly in a browser to view the design.
