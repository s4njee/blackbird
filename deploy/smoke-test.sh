#!/bin/sh
set -eu

# Lifecycle smoke test for the Compose appliance. It uses disposable bind
# mounts and a project name, so it cannot touch a developer's normal stack.
# Set COMPOSE_FILES to a space-separated list to exercise another host
# override, for example: COMPOSE_FILES='-f docker-compose.yml -f deploy/docker-compose.macos.yml'.
# Set SMOKE_ROOT to a parent path when exercising host paths (including spaces).
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="blackbird-smoke-$$"
STATE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/blackbird-smoke.XXXXXX")
if [ -n "${SMOKE_ROOT:-}" ]; then
	# Keep caller-selected parent paths (including spaces) while isolating each
	# invocation so an earlier run cannot make bootstrap appear idempotent.
	SMOKE_ROOT="$SMOKE_ROOT/$PROJECT"
else
	SMOKE_ROOT="$STATE_DIR"
fi
mkdir -p "$SMOKE_ROOT/config" "$SMOKE_ROOT/downloads" "$SMOKE_ROOT/watch" "$SMOKE_ROOT/session"

# Bind mounts make the smoke test independent of pre-existing named volumes.
# A high default port also avoids colliding with a developer's local daemon.
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
COMPOSE_FILES=${COMPOSE_FILES:--f docker-compose.yml -f deploy/docker-compose.linux.yml}
COMPOSE="docker compose -p ${PROJECT} ${COMPOSE_FILES}"

cleanup() {
	$COMPOSE down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cd "$ROOT"
if command -v go >/dev/null 2>&1; then
	# Generate a disposable, Compose-aware config before mounting the bind path.
	bootstrap_output=$(go run ./cmd/blackbird --bootstrap "$BLACKBIRD_CONFIG_DIR")
	SMOKE_PASSWORD=$(printf '%s\n' "$bootstrap_output" | sed -n 's/^Password .*: //p')
	if [ -z "$SMOKE_PASSWORD" ]; then
		echo "bootstrap did not return a disposable password" >&2
		exit 1
	fi
else
	echo "Go toolchain is required to generate the smoke-test config." >&2
	exit 1
fi

echo "Validating Compose model..."
$COMPOSE config -q

echo "Building and starting disposable appliance..."
$COMPOSE up --build -d

echo "Waiting for service health..."
for _ in $(seq 1 90); do
	rtorrent_id=$($COMPOSE ps -q rtorrent)
	blackbird_id=$($COMPOSE ps -q blackbird)
	if [ -n "$rtorrent_id" ] && [ -n "$blackbird_id" ]; then
		rtorrent_health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}unknown{{end}}' "$rtorrent_id" 2>/dev/null || true)
		blackbird_status=$(docker inspect -f '{{.State.Status}}' "$blackbird_id" 2>/dev/null || true)
		rtorrent_version=$(docker exec "$rtorrent_id" rtorrent -h 2>/dev/null | head -1 || true)
		if [ "$rtorrent_health" = healthy ] && [ "$blackbird_status" = running ] \
			&& printf '%s' "$rtorrent_version" | grep -q '0.15.7' \
			&& docker exec "$rtorrent_id" test -d /data/session \
			&& docker exec "$rtorrent_id" sh -c 'find /data/session -type f -print -quit | grep -q .' \
			&& docker exec "$blackbird_id" wget -qO- http://127.0.0.1:8222/healthz | grep -q '"ok":true'; then
			echo "rTorrent healthcheck and Blackbird liveness probe passed."
			echo "Exercising daemon recovery and container recreation..."
			docker kill "$rtorrent_id" >/dev/null
			disconnected=false
			for _ in $(seq 1 30); do
				if curl -fsS --max-time 2 -u "admin:$SMOKE_PASSWORD" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/health" \
					| grep -q '"connection":"disconnected"'; then
					disconnected=true
					break
				fi
				sleep 1
			done
			if [ "$disconnected" != true ]; then
				echo "Blackbird did not report disconnected state after rTorrent was killed." >&2
				$COMPOSE logs --no-color >&2 || true
				exit 1
			fi
			echo "Disconnected-state transition observed."
			# Compose restart policies are supplemented by an explicit up path,
			# which is also what a host reboot/update invokes.
			$COMPOSE up -d rtorrent >/dev/null
			recovered=false
			for _ in $(seq 1 30); do
				new_rtorrent_id=$($COMPOSE ps -q rtorrent)
				if [ -n "$new_rtorrent_id" ] && [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}unknown{{end}}' "$new_rtorrent_id" 2>/dev/null || true)" = healthy ]; then
					recovered=true
					break
				fi
				sleep 2
			done
			if [ "$recovered" != true ]; then
				echo "rTorrent did not recover after being killed." >&2
				$COMPOSE logs --no-color >&2 || true
				exit 1
			fi
			if ! curl -fsS --max-time 3 -u "admin:$SMOKE_PASSWORD" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null; then
				echo "Blackbird did not restore the authenticated session after rTorrent recovery." >&2
				exit 1
			fi
			$COMPOSE up -d --force-recreate --no-build >/dev/null
			sleep 2
			new_blackbird_id=$($COMPOSE ps -q blackbird)
			if ! docker exec "$new_blackbird_id" wget -qO- http://127.0.0.1:8222/healthz | grep -q '"ok":true'; then
				echo "Blackbird liveness failed after container recreation." >&2
				exit 1
			fi
			# Exercise the second service's restart path as well. Compose restart is
			# deterministic across Docker Engine and Docker Desktop; rTorrent above
			# is the crash/disconnect test.
			$COMPOSE restart blackbird >/dev/null
			blackbird_recovered=false
			for _ in $(seq 1 60); do
				current_blackbird_id=$($COMPOSE ps -q blackbird)
				if [ -n "$current_blackbird_id" ] \
					&& [ "$(docker inspect -f '{{.State.Status}}' "$current_blackbird_id" 2>/dev/null || true)" = running ] \
					&& docker exec "$current_blackbird_id" wget -qO- http://127.0.0.1:8222/healthz 2>/dev/null | grep -q '"ok":true'; then
					blackbird_recovered=true
					break
				fi
				sleep 1
			done
			if [ "$blackbird_recovered" != true ]; then
				echo "Blackbird did not recover after being killed." >&2
				$COMPOSE logs --no-color >&2 || true
				exit 1
			fi
			echo "Daemon recovery and container recreation passed."
			$COMPOSE ps
			exit 0
		fi
	fi
	sleep 2
done

echo "Compose services did not become healthy." >&2
$COMPOSE ps >&2 || true
$COMPOSE logs --no-color >&2 || true
exit 1
