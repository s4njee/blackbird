#!/bin/sh
set -eu

# Smoke test the default named-volume topology. The Linux smoke test uses bind
# mounts; this companion verifies the installer path and persistence semantics
# users get from `docker compose up --build -d`.
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="blackbird-named-smoke-$$"
STATE_DIR=$(mktemp -d "${TMPDIR:-/tmp}/blackbird-named-smoke.XXXXXX")
export BLACKBIRD_HTTP_PORT="${BLACKBIRD_HTTP_PORT:-18223}"
COMPOSE="docker compose -p ${PROJECT} -f docker-compose.yml"

cleanup() {
	$COMPOSE down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cd "$ROOT"
mkdir -p "$STATE_DIR"
bootstrap_output=$(go run ./cmd/blackbird --bootstrap "$STATE_DIR")
password=$(printf '%s\n' "$bootstrap_output" | sed -n 's/^Password (save it now; it will not be shown again): //p')
if [ -z "$password" ]; then
	echo "bootstrap did not return a password" >&2
	exit 1
fi
export BLACKBIRD_ENV_FILE="$STATE_DIR/.env"

echo "Building and starting the named-volume appliance..."
$COMPOSE up --build -d

echo "Waiting for named-volume appliance..."
ready=false
for _ in $(seq 1 60); do
	rtorrent_id=$($COMPOSE ps -q rtorrent)
	blackbird_id=$($COMPOSE ps -q blackbird)
	if [ -n "$rtorrent_id" ] && [ -n "$blackbird_id" ]; then
		rtorrent_health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}unknown{{end}}' "$rtorrent_id" 2>/dev/null || true)
		blackbird_status=$(docker inspect -f '{{.State.Status}}' "$blackbird_id" 2>/dev/null || true)
		if [ "$rtorrent_health" = healthy ] && [ "$blackbird_status" = running ] \
			&& curl -fsS --max-time 3 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null; then
			ready=true
			break
		fi
	fi
	sleep 2
done

if [ "$ready" != true ]; then
	echo "named-volume services did not become ready" >&2
	$COMPOSE ps >&2 || true
	$COMPOSE logs --no-color >&2 || true
	exit 1
fi

# Exercise the authenticated torrent API with a deterministic, zero-byte
# fixture so the smoke test does not depend on an external tracker or payload.
fixture="$STATE_DIR/smoke.torrent"
base64 -d < deploy/fixtures/smoke.torrent.b64 > "$fixture"
add_response=$(curl -fsS --max-time 5 -u "admin:${password}" \
	-F "files=@${fixture}" \
	-F label=smoke \
	-F start=false \
	"http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/torrents/add")
printf '%s' "$add_response" | grep -q '"ok":true'

hash=""
for _ in $(seq 1 20); do
		session_json=$(curl -fsS --max-time 5 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session")
	if printf '%s' "$session_json" | grep -q 'smoke.bin'; then
		hash=$(printf '%s' "$session_json" | sed -n 's/.*"hash":"\([a-fA-F0-9]*\)".*"name":"smoke.bin".*/\1/p')
		if [ -n "$hash" ]; then
			break
		fi
	fi
	sleep 1
done
if [ -z "$hash" ]; then
	echo "deterministic torrent was not visible in the session snapshot" >&2
	exit 1
fi

action() {
	name=$1
	body=$2
	response=$(curl -fsS --max-time 5 -u "admin:${password}" \
		-H 'Content-Type: application/json' \
		-d "$body" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/torrents/action")
	printf '%s' "$response" | grep -q '"ok":true' || {
		echo "$name action failed: $response" >&2
		exit 1
	}
}
action label "{\"action\":\"set_label\",\"hashes\":[\"$hash\"],\"label\":\"smoke\"}"
action priority "{\"action\":\"priority\",\"hashes\":[\"$hash\"],\"priority\":2}"
action file-priority "{\"action\":\"file_priority\",\"hashes\":[\"$hash\"],\"fileIndex\":0,\"priority\":2}"
action start "{\"action\":\"start\",\"hashes\":[\"$hash\"]}"
action stop "{\"action\":\"stop\",\"hashes\":[\"$hash\"]}"
action remove "{\"action\":\"remove\",\"hashes\":[\"$hash\"]}"

# Write sentinels through each persistent mount, then recreate both services.
docker exec "$blackbird_id" sh -c 'printf config-persisted > /config/.smoke-config && printf download-persisted > /downloads/.smoke-download'
docker exec "$rtorrent_id" sh -c 'printf session-persisted > /data/session/.smoke-session'
$COMPOSE up -d --force-recreate --no-build >/dev/null
sleep 3
new_blackbird_id=$($COMPOSE ps -q blackbird)
new_rtorrent_id=$($COMPOSE ps -q rtorrent)
[ "$(docker exec "$new_blackbird_id" cat /config/.smoke-config)" = config-persisted ]
[ "$(docker exec "$new_blackbird_id" cat /downloads/.smoke-download)" = download-persisted ]
[ "$(docker exec "$new_rtorrent_id" cat /data/session/.smoke-session)" = session-persisted ]
curl -fsS --max-time 3 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null

# Exercise an image upgrade and rollback with the same named volumes. The
# fixture tags intentionally point at the just-built image so this remains
# deterministic and does not pull from a registry.
docker tag blackbird/blackbird:dev blackbird/blackbird:fixture-v1
docker tag blackbird/blackbird:dev blackbird/blackbird:fixture-v2
BLACKBIRD_VERSION=fixture-v2 docker compose -p "$PROJECT" -f docker-compose.yml up -d --no-build >/dev/null
sleep 3
upgraded_id=$($COMPOSE ps -q blackbird)
curl -fsS --max-time 3 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null
[ "$(docker exec "$upgraded_id" cat /config/.smoke-config)" = config-persisted ]
BLACKBIRD_VERSION=fixture-v1 docker compose -p "$PROJECT" -f docker-compose.yml up -d --no-build >/dev/null
sleep 3
rolled_back_id=$($COMPOSE ps -q blackbird)
curl -fsS --max-time 3 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null
[ "$(docker exec "$rolled_back_id" cat /config/.smoke-config)" = config-persisted ]

# A normal `down` must preserve the named volumes; only `down --volumes` in
# cleanup is destructive. Bring the same project back and verify the data.
$COMPOSE down --remove-orphans >/dev/null
$COMPOSE up -d --no-build >/dev/null
down_up_ready=false
for _ in $(seq 1 45); do
	down_rtorrent_id=$($COMPOSE ps -q rtorrent)
	down_blackbird_id=$($COMPOSE ps -q blackbird)
	if [ -n "$down_rtorrent_id" ] && [ -n "$down_blackbird_id" ] \
		&& [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}unknown{{end}}' "$down_rtorrent_id" 2>/dev/null || true)" = healthy ] \
		&& curl -fsS --max-time 3 -u "admin:${password}" "http://127.0.0.1:${BLACKBIRD_HTTP_PORT}/api/session" >/dev/null; then
		down_up_ready=true
		break
	fi
	sleep 2
done
if [ "$down_up_ready" != true ]; then
	echo "appliance did not recover after a data-preserving compose down/up" >&2
	$COMPOSE ps >&2 || true
	$COMPOSE logs --no-color >&2 || true
	exit 1
fi
[ "$(docker exec "$down_blackbird_id" cat /config/.smoke-config)" = config-persisted ]
[ "$(docker exec "$down_blackbird_id" cat /downloads/.smoke-download)" = download-persisted ]
[ "$(docker exec "$down_rtorrent_id" cat /data/session/.smoke-session)" = session-persisted ]

echo "Named-volume authentication, persistence, upgrade, and rollback passed."
$COMPOSE ps
