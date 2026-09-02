#!/bin/sh
set -eu

# Send a minimal XML-RPC request over SCGI and verify that rTorrent returns a
# successful method response. This avoids depending on a public management
# port or a separate diagnostic daemon.
body='<?xml version="1.0"?><methodCall><methodName>system.client_version</methodName><params></params></methodCall>'
length=$(printf '%s' "$body" | wc -c | tr -d ' ')
request=$(mktemp)
trap 'rm -f "$request"' EXIT
printf 'CONTENT_LENGTH\0%s\0SCGI\0%s\0' "$length" 1 > "$request"
header_length=$(wc -c < "$request" | tr -d ' ')

response=$( { printf '%s:' "$header_length"; cat "$request"; printf ',%s' "$body"; } | timeout 4s nc 127.0.0.1 5000 2>/dev/null || true)
printf '%s' "$response" | grep -q '<methodResponse>'
