#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STATE_DIR="$ROOT/deploy"
ENV_FILE="$ROOT/deploy/.env"

mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

# The session bind mount must exist before Compose starts, or Docker creates
# it owned by root and the non-root container cannot write to it.
mkdir -p "${HOME}/Downloads/torrents/.session"

# A checked-out or rsync'd bin/blackbird may have been built for a different
# OS/architecture than this host, which is the usual way this script fails:
# it runs fine on the laptop that built it and dies on the server. Prove the
# binary executes here before trusting it.
if [ -x "$ROOT/bin/blackbird" ] && "$ROOT/bin/blackbird" --version >/dev/null 2>&1; then
  BOOTSTRAP_CMD="$ROOT/bin/blackbird"
elif command -v go >/dev/null 2>&1; then
  BOOTSTRAP_CMD="go run ./cmd/blackbird"
else
  echo "no usable blackbird binary or Go toolchain on this host." >&2
  echo "this script is optional: 'docker compose up -d' generates credentials" >&2
  echo "inside the container and prints the password. See deploy/README.md." >&2
  exit 1
fi

if [ "${1:-}" = "--rotate" ]; then
  shift
  set -- --rotate-password
fi
if [ "$#" -gt 0 ]; then
  echo "usage: $0 [--rotate]" >&2
  exit 2
fi

# Bootstrap is idempotent: an existing config or env file is left untouched.
(cd "$ROOT" && $BOOTSTRAP_CMD --bootstrap "$STATE_DIR" "$@")

if [ ! -f "$ENV_FILE" ]; then
  echo "warning: deploy/.env was not created; Compose will use the hash in the config volume" >&2
fi

cat <<EOF

Next step:
  PUID="\$(id -u)" PGID="\$(id -g)" docker compose up --build -d

This host-side bootstrap is optional: with no deploy/.env the container
generates its own credentials on first start and prints the password.
EOF
