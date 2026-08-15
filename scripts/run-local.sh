#!/bin/sh
# Bring up the local chat stack for a trial run:
#   open-oscar-server (loopback, DISABLE_AUTH) + fleetchat chatd.
# Ctrl-C stops both. Durable launchd setup comes after the naming decision.
set -eu
cd "$(dirname "$0")/.."
[ -x .cache/oscar-server ] || sh scripts/get-oscar.sh
mkdir -p "$HOME/.buddylist"
OSCAR_LISTENERS='LOCAL://127.0.0.1:5190' \
OSCAR_ADVERTISED_LISTENERS_PLAIN='LOCAL://127.0.0.1:5190' \
TOC_LISTENERS='127.0.0.1:9898' \
API_LISTENER='127.0.0.1:8080' \
DB_PATH="$HOME/.buddylist/oscar.sqlite" \
DISABLE_AUTH=true LOG_LEVEL=info \
.cache/oscar-server &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT INT TERM
sleep 1
go run ./cmd/buddylist serve --rooms lobby,ops
