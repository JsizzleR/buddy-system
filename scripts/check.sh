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
# Safe spelling, not `cd "$(dirname "$0")/.."`: that resolves through the
# CALLER's $CDPATH and can run this whole suite against a DIFFERENT checkout.
# scripts/lib.sh has the measurement.
CDPATH= cd -- "$(dirname -- "$0")/.."

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
  # error fails at commit time, which is the worst moment to discover it. The
  # glob also covers scripts/lib.sh, which nothing executes and all four
  # done-checks SOURCE — a syntax error there takes every one of them down.
  for s in .githooks/* scripts/*.sh; do
    [ -f "$s" ] || continue
    sh -n "$s" || { echo "check.sh: $s has a syntax error" >&2; exit 1; }
  done

  # CDPATH GATE, source-shape. `cd "$(dirname "$0")/.."` resolves its operand
  # through the CALLER's exported $CDPATH before the filesystem, so with
  # CDPATH=$HOME/src set for interactive convenience a script can land in a
  # DIFFERENT checkout — and then every leg below builds, seeds and greps that
  # one while reporting GREEN for this one. Six of the nine scripts here carried
  # the unsafe spelling (counted, 2026-09-08); the safe one is `CDPATH= cd -- …`, and
  # scripts/lib.sh carries the reasoning. grep-shaped because no test can see
  # the class: a suite run against the wrong repo still passes.
  #
  # Two clauses, the same as the fakeConn gate above: the scan, and a planted
  # control the scan MUST agree with, so a respelled pattern that matches
  # nothing cannot report the same GREEN as a clean tree. The scan is a pattern
  # and TWO filters: whole-comment lines are dropped (this very comment quotes
  # the bad spelling, and so does lib.sh) and so is the safe spelling itself.
  cdscan() { grep -nHE 'cd +(--)? *"?\$\(dirname' "$@" | grep -v ':[[:space:]]*#' | grep -v 'CDPATH= cd -- '; }
  # THE CONTROL IS FOUR LINES, NOT ONE, because a one-line control arms the
  # pattern and NEITHER FILTER — and the filters are what decide the scan is
  # allowed to drop something. Measured, review 2026-09-08: widen the comment
  # filter to `grep -v '#'` (the sloppy edit somebody reaches for when this
  # comment's own quoting of the bad spelling trips the scan) and restore
  # scripts/run-local.sh:11 to the unsafe spelling it carried until this
  # commit — trailing comment and all, which is how that line is really
  # written. The one-line control still passed, the gate printed NOTHING and
  # exited 0, and the violation shipped. So the control pins WHICH lines come
  # back, not merely that something did:
  #   1  bare violation                  -> MUST be reported
  #   2  violation + trailing comment    -> MUST be reported (arms filter 1)
  #   3  a wholly commented-out mention  -> must NOT be (this file has one)
  #   4  the safe spelling               -> must NOT be (arms filter 2)
  # `dirname` arrives as a printf ARGUMENT so none of these lines spells the bad
  # idiom here and trips the scan on check.sh itself; the control file holds it
  # verbatim.
  cdctl=$(mktemp -t cdpath-control.XXXXXX)
  printf 'cd "$(%s "$0")/.."\ncd "$(%s "$0")/.."   # trailing comment\n  # cd "$(%s "$0")/.."\nCDPATH= cd -- "$(%s -- "$0")/.."\n' dirname dirname dirname dirname > "$cdctl"
  ctlhit=$(cdscan "$cdctl" | sed 's/^[^:]*:\([0-9][0-9]*\):.*$/\1/' | tr '\n' ' ' || true); rm -f "$cdctl"
  [ "$ctlhit" = "1 2 " ] || { echo "check.sh: the CDPATH scan reported line(s) '$ctlhit' of its four-line control, want '1 2 ' — the gate is broken, not the tree" >&2; exit 1; }
  unsafe=$(cdscan .githooks/* scripts/*.sh || true)
  [ -z "$unsafe" ] || { echo "check.sh: CDPATH-unsafe cd — use \`CDPATH= cd -- \"\$(dirname -- \"\$0\")/..\"\` (see scripts/lib.sh):" >&2; echo "$unsafe" >&2; exit 1; }

  go vet ./...
  go test ./... -count=1
  go test -race ./... -count=1

  # Per-feature done-checks. A feature whose done-check is not invoked here is
  # a feature nothing gates.
  sh scripts/check-commit-gate.sh
  sh scripts/check-pre-push.sh
  sh scripts/check-local-hooks.sh
  sh scripts/check-fence.sh
  sh scripts/check-charter.sh
  sh scripts/check-wait.sh
  sh scripts/check-skill.sh
  sh scripts/check-install.sh
  sh scripts/check-startup-report.sh
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
