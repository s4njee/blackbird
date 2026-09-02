#!/bin/sh
set -eu

CONFIG_PATH=${BLACKBIRD_CONFIG_PATH:-/config/config.yml}

# Metadata/help commands do not need a mounted configuration volume.
case "${1:-}" in
	--version|--help|-h)
		exec /usr/local/bin/blackbird "$@"
		;;
esac

# A freshly-created named volume is empty. Bootstrap writes the bcrypt hash to
# deploy/.env, so seed a minimal Compose config on first start. Existing
# operator config is never modified.
if [ ! -s "$CONFIG_PATH" ]; then
	if [ -z "${BLACKBIRD_PASSWORD_HASH:-}" ]; then
		echo "blackbird: $CONFIG_PATH is missing; run deploy/bootstrap.sh first" >&2
		exit 1
	fi
	tmp_path="${CONFIG_PATH}.tmp.$$"
	trap 'rm -f "$tmp_path"' EXIT INT TERM
	{
		printf '%s\n' 'server:' '  listen: ":8222"' '  base_url: "/"'
		printf '%s\n' 'log:' '  level: info'
		printf '%s\n' 'auth:' '  username: admin'
		printf '  password_hash: %s\n' "$BLACKBIRD_PASSWORD_HASH"
		printf '%s\n' 'rtorrent:' '  scgi: "tcp://rtorrent:5000"' '  timeout: 10s'
		printf '%s\n' 'poll:' '  interval: 2s' '  detail_interval: 1s' '  volume_interval: 30s'
		printf '%s\n' 'directories:' '  default: "/downloads"' '  watch: "/watch"' '  session: "/session"'
		printf '%s\n' 'volumes:' '  - /downloads'
		printf '%s\n' 'ui:' '  accent: "#35418f"' '  sort:' '    column: added' '    dir: desc'
	} > "$tmp_path"
	chmod 0600 "$tmp_path"
	mv "$tmp_path" "$CONFIG_PATH"
	trap - EXIT INT TERM
	echo "blackbird: initialized $CONFIG_PATH from bootstrap environment" >&2
fi

exec /usr/local/bin/blackbird "$@"
