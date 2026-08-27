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
# Source-shape gate for issue #4. fakeConn's events channel is closed by the
# daemon's shutdown watcher, so any bare send into it races that close. Exactly
# one raw send is legal: the guarded one inside emit. This is gated by GREP and
# not by -race because -race structurally cannot see it — measured, a raw send
# restored at a test-body call site survives 5 of 5 race runs, since only
# ChatJoin has a second goroutine live at the same instant. Two clauses on
# purpose: the count catches a new send, the identity catches this gate being
# quietly defeated by respelling the line it allows.
raws=$(grep -o '\.events <-' internal/buddylist/chatd_test.go | wc -l | tr -d ' ')
[ "$raws" = 1 ] || { echo "check.sh: $raws raw sends into fakeConn.events; exactly 1 is allowed (inside emit). Route events through emit/push — issue #4." >&2; exit 1; }
grep -q 'case f\.events <- ev:' internal/buddylist/chatd_test.go || { echo "check.sh: emit's guarded send is gone or respelled — re-point this gate in the same commit." >&2; exit 1; }

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
