#!/bin/bash
# Update IS24 cookie in .env file
# Usage:
#   IS24_COOKIE='name=value; ...' ./scripts/update_cookie.sh
#   ./scripts/update_cookie.sh /opt/immobot/.env.cookie

set -euo pipefail

TARGET_FILE="${1:-/opt/immobot/.env.cookie}"
COOKIE="${IS24_COOKIE:-}"

if [ -z "$COOKIE" ]; then
  read -r -s -p "IS24 cookie: " COOKIE
  echo
fi

if [ -z "$COOKIE" ]; then
  echo "No cookie provided" >&2
  exit 1
fi

mkdir -p "$(dirname "$TARGET_FILE")"
escaped=${COOKIE//\'/\'\\\'\'}
umask 077
printf "IS24_COOKIE='%s'\n" "$escaped" > "$TARGET_FILE"
chmod 600 "$TARGET_FILE"

echo "Cookie saved to $TARGET_FILE"
