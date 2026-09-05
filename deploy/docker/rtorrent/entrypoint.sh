#!/bin/sh
set -eu

# This entrypoint starts as root so it can repair ownership of the mount
# points, then drops to PUID:PGID before rTorrent ever runs. Docker creates a
# missing bind-mount source as an empty, root-owned directory without any
# error — so without this step, "docker compose up" on a fresh host silently
# produces a daemon that cannot write its own session. Only the mount-point
# directory itself is touched, and only when it is root-owned (i.e. Docker
# made it); user data is never chowned, and never recursively.
PUID=${PUID:-1000}
PGID=${PGID:-1000}
case "$PUID$PGID" in
  *[!0-9]*)
    echo "PUID and PGID must be numeric (got PUID=$PUID PGID=$PGID)" >&2
    exit 64
    ;;
esac

if [ "$(id -u)" = 0 ]; then
	# Best effort: a mount that cannot be repaired (read-only, or a
	# filesystem that rejects chown) must not stop the container. If the
	# process later needs to write there it will fail loudly on its own.
	for dir in /data/session /data/watch /downloads; do
		if [ -d "$dir" ] && [ "$(stat -c %u "$dir")" = 0 ]; then
			chown "$PUID:$PGID" "$dir" 2>/dev/null || echo "note: could not chown $dir (read-only?)" >&2
		fi
	done
	# rTorrent leaves a session lock when it is terminated with SIGKILL. A
	# fresh container is the sole owner of this session directory, so remove
	# that stale marker before starting and allow Compose to recover
	# automatically.
	rm -f /data/session/rtorrent.lock
	exec setpriv --reuid="$PUID" --regid="$PGID" --clear-groups "$0" "$@"
fi

# From here on we are PUID:PGID. (An operator who still forces `user:` in
# Compose lands here directly and skips the repair above.)
rm -f /data/session/rtorrent.lock

port="${RTORRENT_P2P_PORT:-47111}"
case "$port" in
  ''|*[!0-9]*)
    echo "RTORRENT_P2P_PORT must be an integer from 1 to 65535" >&2
    exit 64
    ;;
esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
  echo "RTORRENT_P2P_PORT must be an integer from 1 to 65535" >&2
  exit 64
fi

# Set the daemon's TCP/uTP listener with the long-supported native option so
# this wrapper also works with the older rTorrent version used by the image.
exec /usr/local/bin/rtorrent "$@" -p "$port-$port"
