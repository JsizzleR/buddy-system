#!/bin/sh
# buddy-system repo check: hermetic suite always; the live oscar leg when the pinned
# server binary is present (scripts/get-oscar.sh fetches+builds it — network
# needed once). The live leg is REGISTERED here so it runs somewhere real;
# absence of the binary is reported loudly, never silently skipped.
#
# The -race legs are UNSCOPED on purpose. This repo is a concurrency story
# almost everywhere — a reconnecting daemon, a socket server, per-session
# hooks over a shared SQLite ledger — so a race leg scoped to one package
# would leave the rest ungated, and a race no check can observe is a race
# nobody will find. It costs ~17s hermetic and ~3s live (measured), which is
# not enough saving to be worth an honesty problem. Issue #4: before this
# existed, a real data race sat green at HEAD indefinitely, visible only to
# someone who typed -race by hand.
set -eu
cd "$(dirname "$0")/.."
fmt=$(gofmt -l cmd internal)
[ -z "$fmt" ] || { echo "gofmt needed: $fmt"; exit 1; }
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
go vet -tags oscarlive ./internal/buddylist
if [ -x .cache/oscar-server ]; then
  go test -tags oscarlive ./internal/buddylist -count=1
  go test -race -tags oscarlive ./internal/buddylist -count=1
else
  echo "check.sh: LIVE LEG NOT RUN — no .cache/oscar-server; run scripts/get-oscar.sh first" >&2
  exit 2
fi
echo "check.sh: ALL GREEN (hermetic + race + live)"
