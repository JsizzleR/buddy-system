#!/bin/sh
# buddy-system repo check: hermetic suite always; the live oscar leg when the pinned
# server binary is present (scripts/get-oscar.sh fetches+builds it — network
# needed once). The live leg is REGISTERED here so it runs somewhere real;
# absence of the binary is reported loudly, never silently skipped.
set -eu
cd "$(dirname "$0")/.."
fmt=$(gofmt -l cmd internal)
[ -z "$fmt" ] || { echo "gofmt needed: $fmt"; exit 1; }
go vet ./...
go test ./... -count=1
go vet -tags oscarlive ./internal/buddylist
if [ -x .cache/oscar-server ]; then
  go test -tags oscarlive ./internal/buddylist -count=1
else
  echo "check.sh: LIVE LEG NOT RUN — no .cache/oscar-server; run scripts/get-oscar.sh first" >&2
  exit 2
fi
echo "check.sh: ALL GREEN (hermetic + live)"
