# Docker Compose appliance

The default stack builds both images locally and starts them on an internal
Compose network. Torrent data is bind-mounted from
`~/Downloads/torrents`, so it remains directly accessible on the host:

```sh
mkdir -p "$HOME/Downloads/torrents"
./deploy/bootstrap.sh
PUID="$(id -u)" PGID="$(id -g)" docker compose up --build -d
docker compose ps
```

Blackbird's HTTP port (`8223` by default) and rTorrent's BitTorrent listener
(`47111` by default, over both TCP and UDP) are published. Set
`RTORRENT_P2P_PORT` in `deploy/.env` to use a different port; update your host
firewall and router port-forwarding to match. rTorrent's SCGI port (`5000`)
remains private to the Compose network; Blackbird connects to it as
`tcp://rtorrent:5000`. The default deployment uses named volumes for config,
watch data, and rTorrent session state, so `docker compose down` does not
remove that state. Downloads are written to `~/Downloads/torrents`. On first
start, the Blackbird entrypoint seeds the empty named config volume from the
bcrypt hash in `deploy/.env`; existing config is never overwritten. To
intentionally remove the named volumes, use the separately documented
destructive command:

```sh
docker compose down --volumes
```

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

## Moving torrent data

Use **Move data** in the toolbar or torrent context menu to choose a configured
root, per-label destination, recent destination, or folder in the constrained
directory browser. **Move files** stops running torrents, moves their data,
and restarts them; cross-filesystem moves copy, verify, then remove the source.
**Set directory only** updates rTorrent after files were moved separately.
The dialog shows a per-torrent progress/result list and can cancel pending or
copying work. Paths outside configured download roots and symlink escapes are
refused.

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

## Image versions

The rTorrent image pins rTorrent and libtorrent `0.16.18` source archives and
verifies their SHA-256 checksums before compiling. Override the versions and
corresponding checksum build arguments only as part of a reviewed upgrade.

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
