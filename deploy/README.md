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
