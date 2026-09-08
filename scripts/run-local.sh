#!/bin/sh
# Bring up the local TOC/AIM stack for a trial run:
#   open-oscar-server (loopback, DISABLE_AUTH) + the buddylist daemon.
# Ctrl-C stops both. There is no launchd plist for it: this is the nostalgia
# path — IRC is the daily driver (D-007) — and it is driven by hand for an
# evening.
#
# EVERYTHING BINDS LOOPBACK, and that is the only reason DISABLE_AUTH is
# acceptable here. Leaving 127.0.0.1 means turning real auth on first.
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."   # see scripts/lib.sh for the spelling
[ -x .cache/oscar-server ] || sh scripts/get-oscar.sh
mkdir -p "$HOME/.buddylist"
# The stock ports. docs/p0-facts.md records 15190/19898/18080 instead, because
# the P0 spike ran every listener at +10000 (5190→15190, 9898→19898,
# 8080→18080). Nothing reads either set from the other: the two files differ by
# choice, and neither is stale. Do not go looking for the bug.
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
# The stack above is the TOC/oscar server; serve defaults to IRC, so say so.
go run ./cmd/buddylist serve --backend toc --rooms lobby,ops
