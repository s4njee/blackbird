#!/bin/sh
set -eu

# Static verification for Docker Desktop's macOS override. Docker Desktop
# requires the parent paths to be shared; this check validates interpolation
# and quoting without requiring a macOS daemon on the CI host.
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
space_root="/tmp/Blackbird Smoke Fixture"
output=$(BLACKBIRD_MAC_CONFIG_DIR="$space_root/Blackbird Config" \
	BLACKBIRD_MAC_DOWNLOAD_DIR="$space_root/My Downloads" \
	BLACKBIRD_MAC_WATCH_DIR="$space_root/Blackbird Watch" \
	BLACKBIRD_MAC_SESSION_DIR="$space_root/Blackbird Session" \
	docker compose -f "$ROOT/docker-compose.yml" -f "$ROOT/deploy/docker-compose.macos.yml" config)

for path in \
	"$space_root/Blackbird Config" \
	"$space_root/My Downloads" \
	"$space_root/Blackbird Watch" \
	"$space_root/Blackbird Session"; do
	if ! printf '%s\n' "$output" | grep -Fq "$path"; then
		echo "macOS override dropped path with spaces: $path" >&2
		exit 1
	fi
done

echo "macOS paths-with-spaces Compose interpolation passed."
