#!/bin/sh
# buddy-system repo check.
#
# Usage: sh scripts/check.sh [all|hermetic|live]      (default: all)
#
#   hermetic  gofmt, source-shape gates, vet, tests, -race, and the per-feature
#             done-checks. Needs nothing but the toolchain and git. This is the
#             tier a fresh clone can run, and the tier CI runs.
#   live      the oscar legs, which drive the real pinned server binary.
#   all       both. The DEFAULT, and it exits 2 when the live binary is absent.
#
# WHY `all` IS THE DEFAULT even though it can exit 2 on a fresh clone: the live
# leg is REGISTERED here so it runs somewhere real. A suite that quietly skips
# the leg it cannot run reports the same green as one that ran it, and then
# nobody ever notices the leg stopped running. Exit 2 says "not run", which is a
# different fact from both pass and fail. Ask for `hermetic` when you mean it —
# opting out explicitly is fine; opting out silently is what is refused.
#
# CALLER TRAP, measured: `sh scripts/check.sh | tail` reports rc=0 over a FAILING
# run, because sh has no pipefail. Read the summary line, or drop the pipe.
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

TIER=${1:-all}
case "$TIER" in
  all|hermetic|live) ;;
  *) echo "check.sh: unknown tier '$TIER' (want all, hermetic or live)" >&2; exit 2 ;;
esac
run_hermetic=1; run_live=1
[ "$TIER" = live ] && run_hermetic=0
[ "$TIER" = hermetic ] && run_live=0

if [ "$run_hermetic" = 1 ]; then
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

  # Every script that git executes must at least parse. A hook with a syntax
  # error fails at commit time, which is the worst moment to discover it.
  for s in .githooks/* scripts/*.sh; do
    [ -f "$s" ] || continue
    sh -n "$s" || { echo "check.sh: $s has a syntax error" >&2; exit 1; }
  done

  go vet ./...
  go test ./... -count=1
  go test -race ./... -count=1

  # Per-feature done-checks. A feature whose done-check is not invoked here is
  # a feature nothing gates.
  sh scripts/check-commit-gate.sh
  sh scripts/check-pre-push.sh
fi

if [ "$run_live" = 1 ]; then
  go vet -tags oscarlive ./internal/buddylist
  if [ -x .cache/oscar-server ]; then
    go test -tags oscarlive ./internal/buddylist -count=1
    go test -race -tags oscarlive ./internal/buddylist -count=1
  else
    echo "check.sh: LIVE LEG NOT RUN — no .cache/oscar-server; run scripts/get-oscar.sh first" >&2
    exit 2
  fi
fi

case "$TIER" in
  hermetic) echo "check.sh: HERMETIC GREEN (vet + tests + race + done-checks; live leg NOT run)" ;;
  live)     echo "check.sh: LIVE GREEN" ;;
  all)      echo "check.sh: ALL GREEN (hermetic + race + live)" ;;
esac
