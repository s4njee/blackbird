#!/bin/sh
set -eu

CONFIG_PATH=${BLACKBIRD_CONFIG_PATH:-/config/config.yml}
CONFIG_DIR=$(dirname "$CONFIG_PATH")
PUID=${PUID:-1000}
PGID=${PGID:-1000}

# Start as root only long enough to repair ownership, then re-exec this same
# script as PUID:PGID. Docker creates a missing bind-mount source as an
# empty, root-owned directory and reports no error, so a fresh host would
# otherwise get a container that can neither read its config nor write to
# /downloads. Bind mounts get only their mount-point directory fixed, never
# their contents; /config holds nothing but Blackbird's own files, so it is
# fixed recursively — which also migrates volumes created by older images
# that ran as a fixed internal user.
if [ "$(id -u)" = 0 ]; then
	case "$PUID$PGID" in
	  *[!0-9]*)
		echo "PUID and PGID must be numeric (got PUID=$PUID PGID=$PGID)" >&2
		exit 64
		;;
	esac
	# Best effort: a mount that cannot be repaired (read-only, or a
	# filesystem that rejects chown) must not stop the container. If the
	# process later needs to write there it will fail loudly on its own.
	for dir in /downloads /watch /data/session; do
		if [ -d "$dir" ] && [ "$(stat -c %u "$dir")" = 0 ]; then
			chown "$PUID:$PGID" "$dir" 2>/dev/null || echo "note: could not chown $dir (read-only?)" >&2
		fi
	done
	if [ -d "$CONFIG_DIR" ] && [ "$(stat -c %u "$CONFIG_DIR")" != "$PUID" ]; then
		chown -R "$PUID:$PGID" "$CONFIG_DIR"
	fi
	exec su-exec "$PUID:$PGID" "$0" "$@"
fi

# Metadata/help commands do not need a mounted configuration volume, and an
# explicit bootstrap (rotation) must reach the binary without the first-run
# logic below running first.
case "${1:-}" in
	--version|--help|-h)
		exec /usr/local/bin/blackbird "$@"
		;;
	--bootstrap|-bootstrap)
		exec /usr/local/bin/blackbird "$@"
		;;
esac

# A pre-seeded hash is only usable if it survived Compose intact. Compose
# interpolates `$NAME` inside env files, and a bcrypt hash is full of `$`
# followed by letters, so an unescaped hash arrives truncated and would seed
# a config the server then rejects on every start. Never brick first boot
# over it: explain, drop it, and generate fresh credentials instead.
seed_hash=${BLACKBIRD_PASSWORD_HASH:-}
if [ -n "$seed_hash" ]; then
	case "$seed_hash" in
		'$2a$'??'$'*|'$2b$'??'$'*|'$2y$'??'$'*) hash_len=$(printf '%s' "$seed_hash" | wc -c | tr -d ' ') ;;
		*) hash_len=0 ;;
	esac
	if [ "$hash_len" -ne 60 ]; then
		cat >&2 <<EOF
blackbird: ignoring BLACKBIRD_PASSWORD_HASH: not a complete bcrypt hash (got ${hash_len} chars, want 60).
  This usually means deploy/.env holds an unescaped hash and Compose interpolated
  part of it away. Either write every \$ in that file as \$\$, or delete deploy/.env
  and let the container generate credentials (the password is printed below).
EOF
		seed_hash=""
	fi
fi

# A freshly-created config volume is empty. There are two ways to fill it.
if [ ! -s "$CONFIG_PATH" ]; then
	if [ -n "$seed_hash" ]; then
		# Someone ran deploy/bootstrap.sh on the host and Compose passed the
		# resulting hash through deploy/.env. Seed a minimal config from it.
		tmp_path="${CONFIG_PATH}.tmp.$$"
		trap 'rm -f "$tmp_path"' EXIT INT TERM
		{
			printf '%s\n' 'server:' '  listen: ":8222"' '  base_url: "/"'
			printf '%s\n' 'log:' '  level: info'
			printf '%s\n' 'auth:' '  username: admin'
			printf '  password_hash: %s\n' "$seed_hash"
			printf '%s\n' 'rtorrent:' '  scgi: "tcp://rtorrent:5000"' '  timeout: 10s'
			printf '%s\n' 'poll:' '  interval: 2s' '  detail_interval: 1s' '  volume_interval: 30s'
			printf '%s\n' 'directories:' '  default: "/downloads"' '  watch:' '    - path: "/watch"' '  session: "/data/session"'
			printf '%s\n' 'volumes:' '  - /downloads'
			printf '%s\n' 'ui:' '  accent: "#35418f"' '  sort:' '    column: added' '    dir: desc'
		} > "$tmp_path"
		chmod 0600 "$tmp_path"
		mv "$tmp_path" "$CONFIG_PATH"
		trap - EXIT INT TERM
		echo "blackbird: initialized $CONFIG_PATH from bootstrap environment" >&2
	else
		# Nothing was pre-seeded, so generate credentials here. The binary in
		# this image is built for the image's own architecture, so this works
		# no matter what the host is — copying the repo from an arm64 laptop
		# to an amd64 server needs no host binary and no Go toolchain.
		#
		# --bootstrap prints the generated password. It is the only time the
		# plaintext exists: only the bcrypt hash is stored, so it cannot be
		# shown again on later starts.
		echo "blackbird: no configuration found; generating admin credentials" >&2
		/usr/local/bin/blackbird --bootstrap "$CONFIG_DIR"
	fi
fi

# Reprint the login details on every start so they are visible in
# `docker compose up` / `docker compose logs blackbird` without digging
# through the config volume. The password is deliberately absent: it is
# stored only as a bcrypt hash and cannot be recovered.
if [ -s "$CONFIG_PATH" ]; then
	username=$(sed -n 's/^[[:space:]]*username:[[:space:]]*//p' "$CONFIG_PATH" | head -n 1 | tr -d '"')
	port=${BLACKBIRD_HTTP_PORT:-8223}
	cat >&2 <<EOF

  ┌─ Blackbird ─────────────────────────────────────────────────────────
  │  URL:      http://localhost:${port}/
  │  Username: ${username:-admin}
  │  Password: set at first run and stored hashed; it cannot be reprinted.
  │            Lost it? Rotate with:
  │              docker compose run --rm blackbird --bootstrap /config --rotate-password
  └─────────────────────────────────────────────────────────────────────

EOF
fi

exec /usr/local/bin/blackbird "$@"
