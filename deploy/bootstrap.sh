#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STATE_DIR="$ROOT/deploy"
ENV_FILE="$ROOT/deploy/.env"

mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

if [ -x "$ROOT/bin/blackbird" ]; then
  BOOTSTRAP_CMD="$ROOT/bin/blackbird"
elif command -v go >/dev/null 2>&1; then
  BOOTSTRAP_CMD="go run ./cmd/blackbird"
else
  echo "blackbird binary or Go toolchain is required for first-run bootstrap" >&2
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
EOF
