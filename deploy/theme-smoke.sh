#!/bin/sh
# Theme smoke test for the Compose appliance (THM-9.5): boots a disposable
# stack like smoke-test.sh, then runs the Playwright appliance theme spec
# (dark + light console/stats loads without console/page/network errors).
# Reuses the same disposable bind mounts, project isolation, and env knobs
# (COMPOSE_FILES, SMOKE_ROOT, BLACKBIRD_HTTP_PORT).
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="blackbird-theme-smoke-$$"
STATE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/blackbird-theme-smoke.XXXXXX")
if [ -n "${SMOKE_ROOT:-}" ]; then
	SMOKE_ROOT="$SMOKE_ROOT/$PROJECT"
else
	SMOKE_ROOT="$STATE_DIR"
fi
mkdir -p "$SMOKE_ROOT/config" "$SMOKE_ROOT/downloads" "$SMOKE_ROOT/watch" "$SMOKE_ROOT/session"

export BLACKBIRD_CONFIG_DIR="${BLACKBIRD_CONFIG_DIR:-$SMOKE_ROOT/config}"
export BLACKBIRD_DOWNLOAD_DIR="${BLACKBIRD_DOWNLOAD_DIR:-$SMOKE_ROOT/downloads}"
export BLACKBIRD_WATCH_DIR="${BLACKBIRD_WATCH_DIR:-$SMOKE_ROOT/watch}"
export RTORRENT_SESSION_DIR="${RTORRENT_SESSION_DIR:-$SMOKE_ROOT/session}"
export BLACKBIRD_MAC_CONFIG_DIR="${BLACKBIRD_MAC_CONFIG_DIR:-$SMOKE_ROOT/config}"
export BLACKBIRD_MAC_DOWNLOAD_DIR="${BLACKBIRD_MAC_DOWNLOAD_DIR:-$SMOKE_ROOT/downloads}"
export BLACKBIRD_MAC_WATCH_DIR="${BLACKBIRD_MAC_WATCH_DIR:-$SMOKE_ROOT/watch}"
export BLACKBIRD_MAC_SESSION_DIR="${BLACKBIRD_MAC_SESSION_DIR:-$SMOKE_ROOT/session}"
mkdir -p "$BLACKBIRD_CONFIG_DIR" "$BLACKBIRD_DOWNLOAD_DIR" "$BLACKBIRD_WATCH_DIR" "$RTORRENT_SESSION_DIR"
mkdir -p "$BLACKBIRD_MAC_CONFIG_DIR" "$BLACKBIRD_MAC_DOWNLOAD_DIR" "$BLACKBIRD_MAC_WATCH_DIR" "$BLACKBIRD_MAC_SESSION_DIR"
export PUID="$(id -u)"
export PGID="$(id -g)"
export BLACKBIRD_HTTP_PORT="${BLACKBIRD_HTTP_PORT:-18222}"
# rTorrent's P2P port is fixed in Compose; derive a per-run value so this
# disposable stack never collides with a developer's normal stack.
export RTORRENT_P2P_PORT="${RTORRENT_P2P_PORT:-$((47200 + $$ % 500))}"
COMPOSE_FILES=${COMPOSE_FILES:--f docker-compose.yml -f deploy/docker-compose.linux.yml}
COMPOSE="docker compose -p ${PROJECT} ${COMPOSE_FILES}"

cleanup() {
	$COMPOSE down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cd "$ROOT"
if ! command -v go >/dev/null 2>&1; then
	echo "Go toolchain is required to generate the smoke-test config." >&2
	exit 1
fi
bootstrap_output=$(go run ./cmd/blackbird --bootstrap "$BLACKBIRD_CONFIG_DIR")
SMOKE_PASSWORD=$(printf '%s\n' "$bootstrap_output" | sed -n 's/^Password .*: //p')
if [ -z "$SMOKE_PASSWORD" ]; then
	echo "bootstrap did not return a disposable password" >&2
	exit 1
fi

echo "Building and starting disposable appliance..."
$COMPOSE up --build -d

echo "Waiting for Blackbird health..."
healthy=false
for _ in $(seq 1 60); do
	if curl -fsS --max-time 2 -u "admin:$SMOKE_PASSWORD" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/health" >/dev/null 2>&1; then
		healthy=true
		break
	fi
	sleep 2
done
if [ "$healthy" != true ]; then
	echo "Blackbird never became healthy." >&2
	$COMPOSE logs --no-color blackbird >&2 || true
	exit 1
fi

echo "Running appliance theme spec (dark + light)..."
cd "$ROOT/web"
if [ ! -d node_modules ]; then
	npm ci --ignore-scripts
fi
npx playwright install --with-deps chromium
E2E_BASE_URL="http://127.0.0.1:${BLACKBIRD_HTTP_PORT}" \
	E2E_USER="admin" \
	E2E_PASSWORD="$SMOKE_PASSWORD" \
	npx playwright test e2e/appliance-theme.spec.ts
