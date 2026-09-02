#!/bin/sh
set -eu

# rTorrent leaves a session lock when it is terminated with SIGKILL. A fresh
# container is the sole owner of this session directory, so remove that stale
# marker before starting and allow Compose to recover automatically.
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

# Set the daemon's TCP/uTP listener after loading the appliance rc file so it
# cannot fall back to a random, unpublished port.
exec /usr/local/bin/rtorrent "$@" \
  -o "network.listen.port.random.set=0" \
  -o "network.listen.port.set=$port"
