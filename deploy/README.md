# Docker Compose appliance

The default stack builds both images locally and starts them on an internal
Compose network. Torrent data is bind-mounted from
`~/Downloads/torrents`, so it remains directly accessible on the host:

```sh
docker compose up
```

That is the entire first-run procedure; the top-level [README](../README.md)
walks through it. Nothing needs creating or configuring beforehand:

- **No host binary or Go toolchain.** On first start the `blackbird` container
  finds an empty config volume and generates its own admin credentials with
  the binary already inside the image, so a repository copied from an arm64
  laptop to an amd64 server bootstraps correctly. The password is printed
  once, on that start — only its bcrypt hash is stored, so it cannot be shown
  again. Rotate it with
  `docker compose run --rm blackbird --bootstrap /config --rotate-password`,
  which replaces the credential and leaves the rest of the configuration
  untouched. Every start reprints the URL and username, never the password.
- **No `mkdir`, no `PUID` on the command line.** Docker creates a missing
  bind-mount source as an empty, root-owned directory and reports no error.
  Both containers therefore start as root, repair ownership of any mount
  point Docker created — only the directory itself, never its contents — and
  drop to `PUID:PGID` (default `1000:1000`) before rTorrent or Blackbird runs.
  `/config` is the one path fixed recursively: it holds nothing but
  Blackbird's own files, and this is what lets a config volume created by an
  older image (which ran as a fixed internal user) keep working after an
  upgrade with no manual `chown`.
- **A pre-seeded hash cannot brick first boot.** `deploy/bootstrap.sh` still
  exists for operators who want to generate the hash on the host and pass it
  in through `deploy/.env`; it is optional. Compose interpolates `$NAME` in
  env files, so that file must write each `$` in the hash as `$$` (the script
  does). If a hash arrives malformed anyway, the entrypoint says so, ignores
  it, and generates fresh credentials rather than seeding a config the server
  would reject on every start.

Set `PUID`/`PGID`, ports, and other `${...}` substitutions in a `.env` file at
the **repository root**; `deploy/.env` only carries environment into the
container and is read after substitution has already happened.

Blackbird's HTTP port (`8223` by default) and rTorrent's BitTorrent listener
(`47111` by default, over both TCP and UDP) are published. Set
`RTORRENT_P2P_PORT` in a `.env` file at the repository root (**not**
`deploy/.env` — Compose substitutes `${RTORRENT_P2P_PORT}` before it ever
reads that file, so setting it there changes nothing) to use a different
port; update your host firewall and router port-forwarding to match. rTorrent's SCGI port (`5000`)
remains private to the Compose network; Blackbird connects to it as
`tcp://rtorrent:5000`. Config and watch data live in named volumes, so
`docker compose down` does not remove them. Downloads are written to
`~/Downloads/torrents`, and rTorrent's session state to
`~/Downloads/torrents/.session` — a bind mount rather than a named volume, so
the session sits beside the data it describes and survives
`docker compose down --volumes`. Existing config is never overwritten on
start. To intentionally remove the named volumes, use the separately
documented destructive command:

```sh
docker compose down --volumes
```

### Migrating an existing session off the named volume

Deployments created before the session became a bind mount keep their state in
the `rtorrent_session` named volume. Starting the new Compose file mounts the
(empty) `~/Downloads/torrents/.session` directory instead, and rTorrent comes
up with **no torrents loaded** — alarming, but nothing is lost: the old volume
is still there. Copy it across once, with the stack stopped:

```sh
docker compose down
mkdir -p "$HOME/Downloads/torrents/.session"
docker run --rm -v blackbird_rtorrent_session:/from -v "$HOME/Downloads/torrents/.session:/to" \
  alpine sh -c 'cp -a /from/. /to/'
docker compose up -d
```

Confirm the torrent count matches what you had before removing the old volume.

### Shared session directory

Blackbird mounts rTorrent's session volume read-only at `/data/session` so it
can read each torrent's session `.torrent` file and backfill `comment` /
`created by` metadata on the General detail tab for torrents that predate
Blackbird (PAR-2.5). The Compose-bootstrapped config points
`directories.session` at that path; point it at the session directory
wherever you run rTorrent natively so Blackbird can find the files. Metadata
for torrents added through Blackbird is always captured at add time, so this
mount only matters for pre-existing sessions.

## Watch directories

Files dropped into a configured watch directory (PAR-3.1) load into the session
automatically with the entry's label, destination, and start options. Configure
the list under Settings > Directories or directly in YAML:

```yaml
directories:
  watch:
    - path: /watch
      label: iso
      destination: /downloads/iso
      start: true            # default; false loads paused
      delete_after_load: false
      poll_interval: 5s      # optional; for shares that cannot deliver events
```

A loaded file is renamed `<name>.loaded` or deleted per `delete_after_load`.
A malformed or unreadable file moves into the directory's `failed/`
subdirectory and the reason is logged, so it is never retried or lost. A
torrent whose infohash was already loaded in this session is skipped without
an error. Open consoles toast every outcome (loaded, duplicate, rejected).
The Compose appliance mounts the `watch` volume and bootstraps a default
`/watch` entry; the deprecated scalar `directories.watch` and
`directories.watch_label` keys still load but are migrated and cleared on the
next save.

## Completion rules

Rules in `automation.on_complete` (PAR-3.2) run when a torrent finishes
downloading (`d.complete` flips 0→1). Rules are evaluated in order and the
**first rule whose conditions all match** handles the torrent; each torrent is
handled at most once, even across restarts (a marker file next to the config,
`config-state.json`, records which hashes were processed — it is not stored in
the torrent's custom slots).

```yaml
automation:
  on_complete:
    - name: tv-shows          # required, unique
      # match conditions (empty = ignored), all must pass:
      label: tv               # d.custom1, exact match
      tracker: tracker.example  # substring of the tracker host
      name_regex: "^Show\\.S\\d\\d"  # Go regex on the name
      min_size: 1000000000    # bytes; 0 = unbounded
      max_size: 0
      private: false          # d.is_private; omit for any
      # actions (at least one), run in this order:
      set_label: tv-done
      add_tracker: "udp://tracker.example:1337/announce"  # public only
      move_to: /downloads/tv  # via the move engine; inside download roots
      webhook: "https://hooks.example/blackbird"  # JSON completion POST
```

Actions run as the `automation` actor and appear on the torrent's Logger tab;
a failed action toasts on open consoles. Edit rules under Settings >
Automation — the editor includes a dry run that tests the draft rules against
the live session. Note that a torrent that completed while Blackbird was down
produces no transition, so rules do not retroactively apply to it.

## RSS feeds and filters

RSS/Atom feeds (PAR-3.3) auto-load matching items without leaving the
console. Feeds poll on their own schedule and never block the torrent list;
the sidebar Views group shows the unread count, and the RSS view lists feeds
(with error badges and retry countdowns), items (with manual **Add** and
**Mark all read**), and filters with their match history.

```yaml
automation:
  rss:
    feeds:
      - name: tv
        url: "https://tracker.example/rss?passkey=…"  # http(s) only
        poll_interval: 15m    # default when omitted
        label: tv             # d.custom1.set on auto-loads
        destination: /downloads/tv
        cookies: "uid=7"      # secret: masked in the API, never logged
        headers:              # secret: same handling as cookies
          Authorization: "Bearer …"
    filters:                  # first match wins per item
      - name: weekly-shows
        feed: tv              # empty = all feeds
        title_regex: "^Show\\.S\\d\\d"
        category: TV          # substring of an item category
        min_size: 1000000000  # enclosure bytes; unknown sizes never match a bound
        max_size: 0
        label: tv-done        # overrides the feed default
        start: true           # default
```

Items deduplicate by GUID and enclosure URL, so reposts and re-polls never
reload; torrents already in the session are skipped with the reason recorded
in the filter's match history. A failing feed backs off (1 minute doubling to
1 hour) and shows its last error in the RSS view. Cookies/headers display as
`***` in Settings; resubmitting the mask keeps the stored secret, empty
clears it. Feed URLs may carry passkeys — those are stripped from any error
text before logging or serving.

## Unpack on completion

Archives inside a finished torrent (.zip, .rar including multi-part sets)
extract automatically (PAR-3.4) with an external `7z`-compatible binary: the
container image bundles `p7zip`, while native installs need `p7zip-full`
(Debian/Ubuntu), `p7zip` (Alpine), or `sevenzip` (macOS Homebrew, which
provides `7zz`) on PATH — the service probes `7z`, `7zz`, then `7za`. Without
an extractor the feature stays disabled with a clear message under Settings >
Automation and in `GET /api/unpack`; rules are kept but do nothing.

```yaml
automation:
  unpack:
    workers: 2      # bounded low-priority pool (nice +10); YAML-managed
    timeout: 30m    # per-torrent cap; YAML-managed
    rules:          # first label match wins
      - name: tv
        label: tv   # post-completion label; empty matches all
        destination: ""           # empty = in place; else an existing extract root
        delete_archives: true     # removes extracted archives incl. part siblings
```

Extraction runs after completion-rule actions (so moves are already applied)
with exactly-once semantics from the shared completion marker. Listings are
validated before extraction, so entries escaping the destination (zip-slip)
are refused; the extract root must exist inside the download roots.
Extraction runs niced at low priority; progress (25% milestones) and results
land on the torrent's Logger tab, and a failed extraction keeps its partial
output with a `.failed` marker beside it.

## Torrent categories

The Status sidebar offers the standard rTorrent categories. **Completed** is
any torrent for which rTorrent reports `d.complete`, regardless of whether it
is downloading, seeding, or stopped. **Active** means its current download or
upload rate is non-zero. **Inactive** means it is open with both rates at zero.
All other filters use Blackbird's normalized rTorrent state: Downloading,
Seeding, Stopped, Queued, Checking, and Error.

## Search and saved filters

The top-bar filter searches torrent name, hash prefix, download path, tracker
host, and message. Terms are combined with AND. Prefix a value with
`label:`, `tracker:`, `path:`, or `status:` to search one field; use numeric
comparisons such as `ratio>1.5` and `size<4GB`. The `?` beside the filter
shows this syntax in the console. Save the current filter there to pin it in
the sidebar; those pins stay in the browser. Operators can seed defaults in
YAML with `ui.saved_filters`; local saved filters take precedence.

## Sorting

Click any visible table heading to sort it. Shift-click a second heading to
add it as the secondary key; the header shows both directions and the second
key's ordinal. The browser keeps this preference locally. On a browser with
no saved preference, `ui.sort` provides the initial key; `ui.sort.keys` may
provide a primary and secondary operator default.

## Torrent actions

The action toolbar offers Start, Force start, pause/stop, force recheck, and
all four rTorrent priorities: Off, Low, Normal, and High. Right-click one or
more torrents for the same controls plus force reannounce. Its **Advanced**
submenu toggles live sequential downloading and superseeding, saves the
torrent session, and sets the `custom2` through `custom5` fields. Actions are
batched and report an error for each affected torrent; live priority,
sequential, superseeding, and custom-field changes update immediately and are
restored if rTorrent rejects the action.

## Throttle channels

Named throttle groups (PAR-4.1) cap bulk seeds without starving interactive
downloads. Channels are declared in YAML and created on connect with
`throttle.up`/`throttle.down` (re-applied on change via Settings or SIGHUP):

```yaml
tuning:
  throttles:
    - name: slow
      up_kb: 100     # KB/s; 0 = unlimited
      down_kb: 500
```

Assign torrents from the toolbar Throttle control or the Throttle context
submenu (None clears back to the global limits). Assignment stops a running
torrent, sets `d.throttle_name`, and restarts it — rTorrent faults otherwise
("Cannot set throttle on active download"). The sidebar Throttles group shows
assignment counts, the Throttle column shows each torrent's channel, and
Settings > Bandwidth edits channels with live per-channel throughput from
`throttle.up.rate`/`throttle.down.rate`. Removing a channel still referenced
by torrents is refused with the referencing count until they are unassigned.

## Seeding policy

Ratio groups (PAR-4.2) stop, label, or erase seeding torrents once a group's
conditions are met:

```yaml
seeding:
  custom_slot: custom2   # custom field holding the group (custom1 is the label)
  groups:
    - name: archive
      min_ratio: 2.0
      max_seeding_time: 168h
      action: stop_and_set_label
      label: done
```

Assign groups from the Ratio group context submenu (stored in the configured
slot, shown in the Ratio group column). Each torrent triggers a group at most
once, even across restarts (persisted marker); moving it to another group
re-arms it there. Editing a group's action never re-fires already-triggered
pairs. `erase_with_data` honors the same download-root boundary as moves.
Design note (why the poller, not `group.seeding.*` schedules): enforcement in
Blackbird's poller puts every trigger and outcome in the per-torrent history
log and server logs instead of a silent daemon schedule; evaluation is pure
and unit-testable without a daemon; rules are versioned YAML applied uniformly
on connect, SIGHUP, and Settings save. Trade-offs: granularity is one poll
interval (ratios can overshoot between polls), enforcement only runs while
Blackbird is connected, and only complete, open torrents are evaluated
(stopped torrents need no action; a seeding torrent with a tracker warning
still counts as seeding).

## Speed limits

Clicking the global rates in the status bar (PAR-4.4) opens a popover with
presets (unlimited, 25%, 50%, 75%, custom) for down and up. Each row shows
the current daemon limit next to the live rate; presets apply immediately,
custom values apply on Enter or Apply, and percentages base on the saved
default (falling back to the current limit). While a scheduler override is
active the popover says so and edits update the override; while a scheduler
profile is active a hint warns the next profile change will re-apply. "Save
as default" persists the shown values to `tuning.global_*_rate_kb` — runtime
changes never touch YAML on their own. Limits apply via the exact-KiB/s
`.set_kb` daemon calls.

## Bandwidth scheduler

Named limit profiles painted onto a 7×24 grid apply on the minute boundary
and after reconnect (PAR-4.3), in an explicit time zone:

```yaml
schedule:
  timezone: "America/Chicago"   # empty = server local
  bandwidth:
    profiles:
      - name: day
        color: "#f59e0b"
        down_kb: 1000           # KB/s globals; 0 = unlimited
        up_kb: 500
        throttles:              # channel caps; created on the daemon if missing
          - name: slow
            up_kb: 100
            down_kb: 200
      - name: night
        down_kb: 0
        up_kb: 0
    grid:
      mon: ["night", ..., "day", ...]   # 24 hourly profiles per day
```

Empty cells leave the daemon limits alone; only profile changes are applied.
Settings > Scheduler renders the grid with paint-to-fill editing, profile
colors, and the manual override (temporary global limits that pause the
schedule until they expire). The status bar shows the active profile, the
override countdown, and the next change; clicking it opens the Scheduler
settings.

## Traffic history and host load

The Stats page gains a **Traffic history** panel and **Host** cards (PAR-5.2):

- Every poll cycle feeds the daemon's `throttle.global_down.total` /
  `throttle.global_up.total` counters into per-day and per-hour UTC buckets.
  A backward counter step (daemon restart) counts the current totals as new
  traffic instead of going negative, and a restart of Blackbird itself
  bridges the shutdown gap from the last persisted totals. Buckets are UTC
  so daylight-saving transitions never split a day.
- Week / Month / 90-day presets and custom from/to dates render daily bars;
  the Hours view drills into one day's 24 hourly buckets. Totals for the
  visible range sit next to an **Export CSV** download (same numbers,
  `day,down_bytes,up_bytes` or hourly rows).
- Host cards show load average (1/5/15 min), memory used/total, and the
  Blackbird process itself (RSS where the OS exposes it, Go heap otherwise).
  Per-volume free space keeps coming from the existing Volumes panel.
  Unavailable groups render as `—`: telemetry never breaks the console.
- Retention is `stats.traffic_days` (default 90, `0` disables persistence
  while still counting the live session in memory), editable live from
  Settings > History and via SIGHUP reload. Storage is the compact
  append-only `<config>-traffic.jsonl` file next to the config, rewritten
  whenever pruning or 10,000 lines accumulate.

## Moving torrent data

Use **Move data** in the toolbar or torrent context menu to choose a configured
root, per-label destination, recent destination, or folder in the constrained
directory browser. **Move files** stops running torrents, moves their data,
and restarts them; cross-filesystem moves copy, verify, then remove the source.
**Set directory only** updates rTorrent after files were moved separately.
The dialog shows a per-torrent progress/result list and can cancel pending or
copying work. Paths outside configured download roots and symlink escapes are
refused.

## Directory browser

Every destination picker (PAR-5.1) — Add, Move, Settings > Directories, and
the completion, RSS, unpack, and watch rule editors — shares one
server-constrained browser: roots with free space, navigation that never
leaves the configured download roots, new-folder creation, and shared recent
picks. Symlinks escaping the roots are refused with the same
`path_outside_download_dirs` error the move engine returns, and unreadable
directories report cleanly instead.

## Torrent history

The sidebar **History** view (PAR-5.3) lists every recorded event newest
first: torrent adds (console, watch directories, RSS), completions,
per-torrent actions (start/stop/label/priority/trackers/peers/throttle),
move-data job outcomes, completion-rule and seeding-policy actions, unpack
results, scheduler profile applications and override changes, and daemon
message transitions. Each event carries its timestamp, torrent hash and name,
kind, actor (the authenticated user, `local` with auth disabled, `watch`,
`rss`, `automation`, `seeding`, `unpack`, `scheduler`, or `daemon`), action,
outcome, and details.

Kind, actor, and free-text (name/hash/action/message) filters narrow the
list; **Load more** pages older events via a stable sequence cursor, so new
arrivals never shift or duplicate a page. The same store backs the
per-torrent Logger tab (the History entry for one hash) and
`GET /api/history` (`limit` default 50, max 200; `before_seq` cursor;
`kind`, `actor`, `hash`, `q` filters), which the console polls with the
operator's authentication. Retention is the `history:` block:
per-torrent entries and age window plus `global_entries` bounding the
History ring (default 5000), all applied live from Settings > History and
via SIGHUP reload.

## Settings apply behavior

Every field the Settings UI edits applies live on Save: tuning setters and
throttle channels go straight to the daemon (per-key outcomes report
below the form), directories, automation, seeding, schedule, RSS, watch,
labels, history, and traffic settings re-read or re-apply without a
restart, and interface preferences (`ui.sort`, `ui.columns`,
`ui.date_format`, `ui.rate_format`, `ui.accent`) take effect in every open
console immediately. Accent and format drafts also preview live while
editing; Revert or navigating away without saving restores the committed
values.

Restart is required only for process-level keys the UI does not edit:
`server.listen`, `server.base_url`, `rtorrent.scgi`, `rtorrent.timeout`,
`rtorrent.max_response_bytes`, `auth.*`, `poll.interval`, and `log.level`.
The frontend classification lives in `web/src/lib/applyBehavior.ts`, shared
with the config reference.

Display preferences: `ui.date_format` is `local` ("12:04 today" / "Sep 2")
or `iso` ("2026-09-03 12:04" UTC); `ui.rate_format` is `binary`
(1024-based) or `decimal` (1000-based) across byte and rate cells.

`ui.poll_interval` is deprecated and ignored (POL-8.4): it never drove
anything — the torrent list poll is server-driven (`poll.interval`). Old
configs still load with a startup warning; remove the key.

## Port check

The status bar used to claim `Port N open` whenever the daemon was
connected. PAR-5.5 replaces the claim with a verified verdict: a
user-initiated check asks a configurable external probe whether the
rTorrent listening port is reachable from the outside. The checked port is
the live daemon port when reported, else the start of the configured
`port_range`.

Configure the probe under Settings > Connection or YAML (empty URL
disables the check):

```yaml
#portcheck:
#  url: "https://probe.example/check?port={port}"
#  timeout: 10s
```

The probe protocol is deliberately small so operators can self-host it:
`GET` the URL with `{port}` substituted; answer `200` plus
`{"reachable": true|false}` (`"open"` accepted as an alias). Anything else
is a probe failure, not a verdict. A minimal reference probe (Python
standard library only; run it on a host outside your NAT):

```python
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs
import socket

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        port = int(parse_qs(urlparse(self.path).query).get("port", ["0"])[0])
        reachable = False
        if 1 <= port <= 65535:
            s = socket.socket()
            s.settimeout(5)
            try:
                # The check request arrives from the network being tested,
                # so the client address is the host under test.
                s.connect((self.client_address[0], port))
                reachable = True
            except OSError:
                pass
            finally:
                s.close()
        body = json.dumps({"reachable": reachable}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
```

Press **Check now** in Settings > Connection (the check uses the saved
probe — save first after editing). The result, timestamp, and method
(`probe <host>`) appear next to the port field and in the status bar
(`Port N open` only after a reachable verdict, `closed` after a negative
one, bare `Port N` while unverified; clicking it opens Settings). Every
check is a history event. Blackbird never probes automatically — no
background, poll, or reconnect path calls the probe.

## IP filter

A PeerGuardian P2P / eMule DAT blocklist (PAR-5.6) loads into rTorrent's
`ipv4_filter` table on connect and on a refresh cadence, so known-bad
ranges are never contacted. Configure the source under Settings >
Connection or YAML (set exactly one of `path` or `url`; both unset
disables the feature):

```yaml
#network:
#  ipfilter:
#    path: "/data/filters/ipfilter.dat"
#    # url: "https://example.com/ipfilter.dat"  # plain or .gz
#    # refresh_interval: 24h   # re-fetch + reload cadence (URL default 24h)
```

Accepted line formats are single IPv4 addresses, CIDRs, `start - end`
ranges (zero-padded octets included), eMule DAT entries
(`start - end , level , name`), and PeerGuardian P2P entries
(`name:start-end`); blanks, `#` comments, and unparseable lines are
skipped for the count but still passed to the daemon inside the loaded
file. URL lists are fetched by Blackbird (plain or gzipped) to a
`<config>-ipfilter.dat` cache and then loaded from there.

Two path notes: `ipv4_filter.load` is read by the daemon, so a local file
must be visible to rTorrent as well as to Blackbird (which counts the
rules) — in Compose, keep it under a shared volume such as `/data`. A
URL cache only needs Blackbird-side disk.

Settings > Connection shows the rule count, the last load time, and
errors; **Reload now** re-fetches (URL sources) and re-loads immediately
using the saved configuration — save first after editing. Reload outcomes
are history events. The wire call is
`ipv4_filter.load <path>, unwanted`, verified against the upstream
rTorrent manual (the generic `ip_tables.*` commands remain unused).

## Create torrent

The top bar's **Create .torrent** dialog (PAR-5.4) builds a metafile from
server-side data without a desktop client:

- **Source** is picked with the shared directory browser (directories) or
  typed by hand (single files). A directory packages recursively in
  lexicographic order; an empty source is refused. Symlink escapes are
  refused: the source resolves to its target first and must land inside the
  roots, and symlinks met while walking a directory abort the job. Sources
  outside the roots fail with the same `path_outside_download_dirs` refusal
  as moves and the browser.
- **Trackers** go one per line (`http(s)` or `udp` with a host): the first
  is the announce URL and each further tracker lands in its own
  announce-list tier. Optional **name override**, **comment**, **source tag**
  (no spaces, for private-tracker tagging), **private** flag
  (`info.private`, disabling DHT/PEX), and **piece size** (automatic —
  smallest 64 KiB–16 MiB step with at most ~2000 pieces — or a fixed power
  of two from 16 KiB to 16 MiB).
- Hashing runs on a bounded pool (2 concurrent creations) with progress
  (bytes, pieces, current file) and cancellation; up to 20 finished jobs are
  retained for download and post-hoc session adds.
- The finished `.torrent` downloads as an attachment (the server path never
  leaks) and can load into the session **started**, tied to the source path:
  the download directory becomes the source's parent so the data is found
  where it sits, with an optional label. Both the creation and the add are
  history events.

## Host storage

The default download bind mount works on Docker Desktop for macOS and Docker
Engine on Linux. Ensure Docker Desktop is allowed to share your home folder.
The startup command passes your host UID/GID to rTorrent so it can write into
`~/Downloads/torrents` without leaving root- or container-owned files.

The optional Linux override bind-mounts every other state directory as well:

```sh
mkdir -p data/{config,downloads,watch,session}
PUID="$(id -u)" PGID="$(id -g)" \
  docker compose -f docker-compose.yml -f deploy/docker-compose.linux.yml up --build -d
```

Those additional bind mounts make files visible to host backup tools but
require ownership by the configured UID/GID; they are generally slower on
macOS because Docker Desktop synchronizes the shared host path. The macOS
override is intended for paths explicitly shared in Docker Desktop, including
paths containing spaces:

```sh
BLACKBIRD_MAC_DOWNLOAD_DIR="/Users/me/My Downloads" \
BLACKBIRD_MAC_WATCH_DIR="/Users/me/Blackbird Watch" \
BLACKBIRD_MAC_SESSION_DIR="/Users/me/Blackbird Session" \
BLACKBIRD_MAC_CONFIG_DIR="/Users/me/Blackbird Config" \
PUID="$(id -u)" PGID="$(id -g)" \
  docker compose -f docker-compose.yml -f deploy/docker-compose.macos.yml up --build -d
```

## Serving and TLS termination

Blackbird serves plain HTTP itself; terminate TLS at a reverse proxy for
anything beyond localhost (PERF-6.5, shared with SEC-2.2):

- **Compressed app shell.** Text assets (scripts, styles, `index.html`)
  ship pre-compressed brotli + gzip variants built into the binary and
  negotiate per request (`Vary: Accept-Encoding`); fonts and images stream
  raw. Hashed `/assets/*` files are immutable for a year, `index.html` is
  always `no-cache`, everything else caches for a day.
- **Timeouts.** Header reads 10s, body reads 15s, writes 60s, idle
  keep-alives 120s. Hijacked WebSocket connections are exempt.
- **HTTP/2.** Go negotiates H2 automatically whenever the proxy serves TLS
  (no Blackbird configuration needed); the proxy examples below both do.

Caddy (automatic certificates):

```caddy
torrents.example.com {
    reverse_proxy 127.0.0.1:8222
}
```

nginx (bring your own certificate):

```nginx
server {
    listen 443 ssl http2;
    server_name torrents.example.com;

    ssl_certificate /etc/ssl/torrents.example.com.crt;
    ssl_certificate_key /etc/ssl/torrents.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8222;
        proxy_http_version 1.1;
        # WebSocket console stream.
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        # gzip/brotli pass through untouched (Blackbird negotiates itself).
        proxy_set_header Accept-Encoding $http_accept_encoding;
    }
}
```

### Behind a proxy: failed-login accounting

Blackbird counts failed logins per client address. Behind a reverse proxy
every request arrives from the proxy, so tell Blackbird which hops may speak
for a client:

```yaml
server:
  trusted_proxies: ["127.0.0.1/32"]   # the proxy, not the clients
```

`X-Forwarded-For` is ignored unless the immediate peer matches this list, so
a client that is not a known proxy can never choose its own identity. With
the list empty (the default) the peer address is used as-is — correct, just
coarse when everyone shares one proxy.

Repeated failures are answered more and more slowly, up to a few seconds.
They are never refused outright: a correct password always succeeds, so a
shared source address cannot be locked out by someone else's bad guesses.

### Cross-origin requests

State-changing requests (and the WebSocket handshake) must come from the
page's own origin. HTTP Basic credentials are replayed automatically by the
browser, so this is what stops a page an authenticated operator visits from
driving the API. Nothing is needed for a normal deployment — the app is
served from the same origin it calls.

If a genuinely separate origin must drive the API (a dev server, say), list
it:

```yaml
server:
  trusted_origins: ["http://localhost:5173"]
```

### Raw XML-RPC

`POST /api/settings/execute`, the Advanced tab's escape hatch, is disabled
unless you opt in:

```yaml
server:
  allow_execute: true
```

Even then the `execute*` family, `system.method.set`/`insert`, `import`,
and `schedule*` stay blocked: those run external programs or rewrite the
daemon's command table, which is a different power from editing a setting.

## Image versions
The rTorrent image pins rTorrent and libtorrent `0.15.7` source archives and
verifies their SHA-256 checksums before compiling. Override the versions and
corresponding checksum build arguments only as part of a reviewed upgrade.

**Do not move this pin to the 0.16 series without reading this.** 0.16
rewrote socket handling: libtorrent gained its own `curl_socket.cc` and a
`SocketManager` that keys every socket by raw fd number and treats any
inconsistency as fatal. Under sustained tracker announces it leaks socket
registrations — measured at 15 of 133 sockets over 88 seconds, because
libcurl does not always deliver the `close_socket()` callback that
`handle_poll_remove()` relies on to unregister. Peer connections recycle fd
numbers fast enough (845 opens across 326 distinct numbers in ~2 minutes)
that a leaked registration is eventually handed a live fd, and the daemon
aborts:

    SocketManager::open_event_or_throw(): tried to use an existing file descriptor
    CurlSocket::handle_poll_new(fd:N) existing CurlSocket easy_handle not null
    PollInternal::modify(): epoll_ctl(DEL) ... Bad file descriptor / No such file or directory

Leaked registrations are never reclaimed while the daemon runs, so this is
not a load threshold that can be tuned around — announces left enabled will
eventually abort the daemon. 0.15.7 is the last release before that rewrite
(published the same day as 0.16.0); it keeps curl in rTorrent itself with no
fd-keyed registry, and supports the same `--with-xmlrpc-tinyxml2` build and
every RPC method Blackbird uses.

## Tracker announces and file descriptors

Every in-flight tracker announce and every peer connection costs the daemon a
file descriptor. rTorrent does not degrade when it runs out — it raises
`torrent::resource_error` from its poll thread ("Listener port accept()
failed: Too many open files") and aborts, and Compose restarts it. A session
of 1,091 torrents with 2,648 trackers reproduces this in about 40 seconds
against the common `nofile` default of 2048.

Two things keep that from happening:

- The `rtorrent` service sets `ulimits.nofile` to 65536, so the container does
  not inherit a 1024/2048 host default.
- `rtorrent.rc` loads the session with announces off, and Blackbird ramps them
  back up a batch at a time after each connect. The defaults (`trackers.ramp_batch`
  25 every `trackers.ramp_interval` 2s) bring ~2,600 trackers up over roughly
  three and a half minutes; the ramp logs its start and completion. Set
  `trackers.enable_on_connect: false` to leave announces off.


Raising only the limit moves the cliff rather than removing it, so keep both.

## Verification matrix

The repository includes disposable lifecycle checks that exercise health,
authentication, SCGI recovery, torrent actions, and volume persistence:

```sh
./deploy/smoke-test.sh
./deploy/named-volume-smoke.sh
./deploy/verify-macos-paths.sh
```

CI runs the lifecycle checks on Linux and builds both images for `linux/amd64`
and `linux/arm64` with Buildx/QEMU. On an Apple Silicon Mac, run the same
smoke command with Docker Desktop and the macOS override after sharing the
bind-mount roots in Docker Desktop:

```sh
SMOKE_ROOT="/tmp/Blackbird Smoke Fixture" \
COMPOSE_FILES='-f docker-compose.yml -f deploy/docker-compose.macos.yml' \
  ./deploy/smoke-test.sh
```

The macOS path verifier also exercises paths containing spaces without
starting containers, which makes it safe to run in CI environments that do
not provide Docker Desktop.

Recorded local verification on this project host includes Docker Desktop on
Darwin/arm64 with the macOS override and space-containing bind paths, plus an
`linux/amd64` lifecycle run via `DOCKER_DEFAULT_PLATFORM=linux/amd64`; the
default lifecycle run covers the native `linux/arm64` engine. The CI matrix
repeats both image builds on every push and pull request.
