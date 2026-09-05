#!/bin/sh
# Backs the Playwright suite (POL-8.1) with a real blackbird binary talking
# to fakertorrent: deterministic synthetic session, no auth, temp state.
# Requires web/dist to be built first (the binary embeds it).
# Env overrides: E2E_PORT (default 8223), E2E_SESSION_SIZE (default 50),
# E2E_TMP (work dir, default mktemp).
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TMP="${E2E_TMP:-$(mktemp -d)}"
SOCK="$TMP/rtorrent-fake.sock"
PORT="${E2E_PORT:-18223}"

go -C "$ROOT" build -o "$TMP/fakertorrent" ./cmd/fakertorrent
go -C "$ROOT" build -o "$TMP/blackbird" ./cmd/blackbird

# Row 49 is the first deterministic tracker-error fixture (EX-05).
FAKE_SESSION_SIZE="${E2E_SESSION_SIZE:-50}" FAKE_SEED=7 "$TMP/fakertorrent" "$SOCK" &
FAKE_PID=$!
i=0
while [ ! -S "$SOCK" ]; do
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "e2e: fakertorrent socket never appeared" >&2
    kill $FAKE_PID 2>/dev/null || true
    exit 1
  fi
  sleep 0.1
done

mkdir -p "$TMP/data/downloads" "$TMP/data/watch" "$TMP/data/session"
cat >"$TMP/e2e.yml" <<EOF
server:
  listen: "127.0.0.1:$PORT"
rtorrent:
  scgi: "unix://$SOCK"
directories:
  default: "$TMP/data/downloads"
  session: "$TMP/data/session"
volumes:
  - $TMP/data
EOF

cleanup() {
  kill $FAKE_PID 2>/dev/null || true
}
trap cleanup EXIT INT TERM
"$TMP/blackbird" --config "$TMP/e2e.yml"
