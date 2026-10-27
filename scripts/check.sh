#!/bin/sh
# buddy-system repo check.
#
# Usage: sh scripts/check.sh [all|hermetic|live]      (default: all)
#
#   hermetic  gofmt, source-shape gates, vet, tests, -race, the per-feature
#             done-checks, and the qualification scripts' hermetic self-tests
#             and syntax sweep. Needs nothing but the toolchain, git and
#             python3 (node optional, reported when absent). This is the tier
#             a fresh clone can run, and the tier CI runs.
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
# The driver is not a session in any fixture ledger. Standalone done-checks
# also scrub these in lib.sh; cover the Go and pre-push legs here as well.
unset CODEX_THREAD_ID CODEX_SESSION_ID
# GIT'S LOCAL ENVIRONMENT IS NOT THIS SUITE'S (#49). Run from a hook, git hands
# us GIT_DIR, and from a LINKED worktree it is absolute: measured 2026-09-27, a
# pre-push from one sent every fixture's `git -C <tmp> …` into the real
# repository, which came out bare with repositoryformatversion=99. Git's own
# list plus a fixed copy, so a git that dies on the repository it was pointed
# at still leaves nothing set. The test binaries scrub again in TestMain
# (testguard.ScrubGitEnv), for a `go test` started some other way.
for v in $(git rev-parse --local-env-vars 2>/dev/null || true) \
  GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT \
  GIT_OBJECT_DIRECTORY GIT_DIR GIT_WORK_TREE GIT_IMPLICIT_WORK_TREE GIT_GRAFT_FILE \
  GIT_INDEX_FILE GIT_NO_REPLACE_OBJECTS GIT_REPLACE_REF_BASE GIT_PREFIX GIT_SHALLOW_FILE \
  GIT_COMMON_DIR; do
  unset "$v"
done
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

  # GIT-ENV GATE, source-shape (#49). Every package that may run git — it
  # imports os/exec and names "git" anywhere, in a test or in the code its tests
  # drive in-process — must call `testguard.ScrubGitEnv()` as a statement
  # INSIDE its TestMain, where it runs before any test. Keyed on the import
  # and the literal, not on a call spelling: an exec.CommandContext with any
  # context argument, a variable holding "git", or an aliased import all still
  # name both (Codex, #49). The unset at the top covers this script's path;
  # the scrub covers `go test` started from inside any other hook.
  # grep-shaped because the failure is invisible to the suite: a fixture
  # writing into the wrong repository passes. Two clauses, as above: the scan,
  # and a planted five-package control it must agree with, one arm per way to
  # be wrong: a: runs git, no scrub; b: a scrub only in a comment; c: a scrub
  # in TestMain (the one that passes); d: a scrub in an unused helper, not in
  # TestMain; e: exec.CommandContext with a non-`ctx` context and no scrub.
  gitenvscan() {
    for d in "$@"; do
      grep -qs '"os/exec"' "$d"/*.go || continue
      grep -qs '"git"' "$d"/*.go || continue
      ls "$d"/*_test.go >/dev/null 2>&1 || continue
      awk '/^func TestMain\(/{on=1} on{print} on && /^}/{on=0}' "$d"/*_test.go |
        grep -qE '^[[:space:]]*(testguard\.)?ScrubGitEnv\(\)' || echo "$d"
    done
  }
  gectl=$(mktemp -d -t gitenv-control.XXXXXX)
  for p in a b c d e; do mkdir -p "$gectl/$p"; done
  printf 'package a\nimport "os/exec"\nfunc f() { exec.Command("git", "init") }\n' > "$gectl/a/a_test.go"
  printf 'package b\nimport "os/exec"\nfunc f() { exec.Command("git", "init") }\n// testguard.ScrubGitEnv() is only mentioned here\n' > "$gectl/b/b_test.go"
  printf 'package c\nimport "os/exec"\nfunc f() { exec.Command("git", "init") }\nfunc TestMain(m *testing.M) {\n\ttestguard.ScrubGitEnv()\n}\n' > "$gectl/c/c_test.go"
  printf 'package d\nimport "os/exec"\nfunc f() { exec.Command("git", "init") }\nfunc unused() {\n\ttestguard.ScrubGitEnv()\n}\nfunc TestMain(m *testing.M) {\n}\n' > "$gectl/d/d_test.go"
  printf 'package e\nimport "os/exec"\nfunc f() { exec.CommandContext(context.Background(), "git", "init") }\n' > "$gectl/e/e_test.go"
  gehit=$(gitenvscan "$gectl/a" "$gectl/b" "$gectl/c" "$gectl/d" "$gectl/e" | sed "s#^$gectl/##" | tr '\n' ' ')
  rm -rf "$gectl"
  [ "$gehit" = "a b d e " ] || { echo "check.sh: the git-env scan reported '$gehit' of its control, want 'a b d e ' — the gate is broken, not the tree" >&2; exit 1; }
  leaky=$(gitenvscan $(find cmd internal -type d) || true)
  [ -z "$leaky" ] || { echo "check.sh: these packages may run git with no testguard.ScrubGitEnv() in their TestMain (#49):" >&2; echo "$leaky" >&2; exit 1; }
  # ONE LIST IN FOUR PLACES (#49). The fixed copy of git's local environment is
  # what runs when `git rev-parse --local-env-vars` cannot, so every copy must
  # be the whole list: the shell copies once held six names against Go's
  # fifteen (Codex). Each copy is compared with testguard's, and that one must
  # still hold at least the fifteen it was measured with.
  golist=$(sed -n '/^var gitLocalEnv/,/^}/p' internal/testguard/gitenv.go | grep -o '"GIT_[A-Z_]*"' | tr -d '"' | sort -u | tr '\n' ' ')
  [ "$(echo $golist | wc -w | tr -d ' ')" -ge 15 ] || { echo "check.sh: testguard's gitLocalEnv read as '$golist' — fewer than 15 names, the extraction or the list is broken" >&2; exit 1; }
  for f in .githooks/pre-push scripts/check.sh scripts/lib.sh; do
    shlist=$(sed -n '/^[[:space:]]*for _\{0,1\}v in \$(git rev-parse --local-env-vars/,/; do$/p' "$f" | grep -o 'GIT_[A-Z_]*' | sort -u | tr '\n' ' ')
    [ "$shlist" = "$golist" ] || { echo "check.sh: $f's fallback list of git's local env differs from testguard's:" >&2; echo "  $f: $shlist" >&2; echo "  gitenv.go: $golist" >&2; exit 1; }
  done

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
  sh scripts/check-setup-clone.sh
  sh scripts/check-startup-report.sh
  sh scripts/check-wake-report.sh
  sh scripts/check-fleet-report.sh

  # QUALIFICATION SCRIPTS (review F-32). Before this, none of the .py/.mjs
  # files under scripts/ ran automatically and this file syntax-checked only
  # *.sh, so a qualification checker could stop parsing and every gate here
  # stayed green. The rule above ("a done-check not invoked here is a feature
  # nothing gates") is stated for check-*.sh; its point covers check-*.py.
  #
  # DEPENDENCY POLICY. python3 is REQUIRED by this tier: on macOS the git this
  # tier already needs is the Command Line Tools' git, and the same install
  # ships python3, so this adds no prerequisite there. node is NOT required: it
  # only syntax-checks the one .mjs helper, so its absence is reported loudly
  # (here and on the summary line) as NOT RUN, never as passed. Nothing below
  # launches a native claude/codex, needs the Agent SDK or a receipt under
  # /private/tmp: those self-tests (check-claude-*.py and friends against a
  # retained native receipt) stay manual until they carry synthetic evidence
  # (F-33). -I ignores PYTHON* and user site-packages, which would otherwise
  # let the caller's environment decide what a checker imports; -B keeps
  # __pycache__ out of the tree for a script that forgets dont_write_bytecode.
  command -v python3 >/dev/null 2>&1 || { echo "check.sh: python3 not found — the hermetic tier runs the qualification scripts' self-tests and syntax sweep" >&2; exit 1; }
  # The sweep COMPILES (in memory, no .pyc), not merely ast.parse: compile also
  # refuses what the parser accepts — `return` outside a function, a misplaced
  # `nonlocal`. Two clauses, as above: the sweep, and a planted three-file
  # control it must agree with: a parse error and a compile-only error MUST be
  # reported, a clean file must NOT be. A sweep respelled into ast.parse misses
  # the second; one that never opens its arguments misses both.
  pysweep() {
    python3 -I -B -c '
import sys
bad = []
for path in sys.argv[1:]:
    try:
        with open(path, "rb") as f:
            compile(f.read(), path, "exec", dont_inherit=True)
    except (SyntaxError, ValueError) as e:
        bad.append(path)
        print("check.sh: %s does not compile: %s" % (path, e), file=sys.stderr)
print(" ".join(p.rsplit("/", 1)[-1] for p in bad))
' "$@"
  }
  pyctl=$(mktemp -d -t pysweep-control.XXXXXX)
  printf 'def f(:\n    pass\n' > "$pyctl/a_parse.py"
  printf 'x = 1\nreturn x\n' > "$pyctl/b_compile.py"
  printf 'def f():\n    return 1\n' > "$pyctl/c_clean.py"
  pyhit=$(pysweep "$pyctl/a_parse.py" "$pyctl/b_compile.py" "$pyctl/c_clean.py" 2>/dev/null || true)
  rm -rf "$pyctl"
  [ "$pyhit" = "a_parse.py b_compile.py" ] || { echo "check.sh: the python sweep reported '$pyhit' of its control, want 'a_parse.py b_compile.py' — the gate is broken, not the tree" >&2; exit 1; }
  pybad=$(pysweep scripts/*.py)
  [ -z "$pybad" ] || { echo "check.sh: scripts that do not compile: $pybad" >&2; exit 1; }
  # NO `assert` STATEMENT IN scripts/*.py (F-39). `python -O` strips them, and
  # these were qualification GATES: measured, under -O qualify-codex trusted
  # hooks it never saw untrusted and qualify-adapters exited 0 over a failed
  # result. Use an explicit check (qualify-codex.require). AST-shaped, not
  # grep-shaped, because fixture programs embedded in string literals assert
  # on purpose (a child python runs them, without -O) and a test's
  # self.assertEqual is a call. The control: a bare assert and a nested one
  # MUST be reported; an assert inside a string and an assertEqual must NOT.
  pyasserts() {
    python3 -I -B -c '
import ast, sys
hits = []
for path in sys.argv[1:]:
    with open(path, "rb") as f:
        tree = ast.parse(f.read(), path)
    hits += ["%s:%d" % (path.rsplit("/", 1)[-1], n.lineno) for n in ast.walk(tree) if isinstance(n, ast.Assert)]
print(" ".join(sorted(hits)))
' "$@"
  }
  pyctl=$(mktemp -d -t pyassert-control.XXXXXX)
  printf 'x = 1\nassert x\n' > "$pyctl/a_bare.py"
  printf 'SRC = "assert False"\nclass T:\n    def t(self):\n        self.assertEqual(1, 1)\n' > "$pyctl/b_text.py"
  printf 'def f(y):\n    if y:\n        assert y, "m"\n' > "$pyctl/c_nested.py"
  pyhit=$(pyasserts "$pyctl/a_bare.py" "$pyctl/b_text.py" "$pyctl/c_nested.py" 2>/dev/null || true)
  rm -rf "$pyctl"
  [ "$pyhit" = "a_bare.py:2 c_nested.py:3" ] || { echo "check.sh: the assert scan reported '$pyhit' of its control, want 'a_bare.py:2 c_nested.py:3' — the gate is broken, not the tree" >&2; exit 1; }
  pyhit=$(pyasserts scripts/*.py)
  [ -z "$pyhit" ] || { echo "check.sh: assert statements in scripts/ vanish under python -O; use an explicit check: $pyhit" >&2; exit 1; }
  # The self-tests that need nothing but python3: a fake child behind every
  # native proxy (plus TERM/HUP cleanup and owned-identity kills), the
  # loopback provider against itself, and the checkers against SYNTHETIC
  # evidence they build in a temp dir: real git worktrees, SQLite snapshots,
  # wire logs and journals, no harness (check-mixed-fleet.Synthetic). Synthetic
  # evidence tests a checker; it qualifies no native harness. The four SUITE
  # checkers also run their splice tests here, so the run binding (F-13) is
  # exercised by the tree and not only by an ephemeral receipt (audit A-06).
  python3 -I -B scripts/test_native_proxy.py
  python3 -I -B scripts/mixed_fleet_provider.py --self-test
  python3 -I -B scripts/check-mixed-fleet.py --self-test
  python3 -I -B scripts/check-claude-resume.py --self-test
  python3 -I -B scripts/check-claude-interrupted.py --self-test
  python3 -I -B scripts/check-claude-interrupted-compaction.py --self-test
  python3 -I -B scripts/check-interrupted-compaction.py --self-test
  python3 -I -B scripts/check-claude-history.py --self-test
  python3 -I -B scripts/check-claude-compaction.py --self-test
  python3 -I -B scripts/check-compaction.py --self-test
  python3 -I -B scripts/check-claude-delivery.py --self-test
  python3 -I -B scripts/check-interrupted.py --self-test
  python3 -I -B scripts/check-recovery.py --self-test
  python3 -I -B scripts/check-uncertain.py --self-test
  # The start-tier checkers (consensus "Harness version tracking", Codex P1):
  # the four suites a new harness version's admission record rests on, plus
  # the jobs suite under the D-077 narrow contract. Each refuses, on synthetic
  # evidence, a denied edit that happened, a missing allowed control, a
  # receipt or file spliced from a second run, and passed:true over a failing
  # case; the Mod checker also an altered inventory. The Claude gate and Codex
  # checkers also re-derive claim renewal (F-26, D-078) from the run's ledger
  # images and journal, the Codex one in its --adapter mode: a renewal of the
  # denied or control claim, a renewal that did not move, one journalled after
  # idle, and a dropped move destination are each refused.
  python3 -I -B scripts/check-claude-gate.py --self-test
  python3 -I -B scripts/check-claude-mods.py --self-test
  python3 -I -B scripts/check-codex-gate.py --self-test
  python3 -I -B scripts/check-adapters.py --self-test
  python3 -I -B scripts/check-claude-jobs.py --self-test
  # Admission (D-081): which versions the committed records admit and what a
  # candidate run bound (admission_lib, read by every runner and checker
  # above), and the release runner's record-writing and refusal logic against
  # FAKE suites: a tier withheld for a failing checker, a run bound to other
  # bytes or not bound at all, no record without start or on a dirty tree, a
  # retained package never overwritten, a torn copy or moving source refused.
  # Neither launches a harness.
  python3 -I -B scripts/admission_lib.py --self-test >/dev/null
  python3 -I -B scripts/qualify-release.py --self-test >/dev/null
  # node --check, with a planted control so a check that accepts everything
  # cannot pass for one that checked.
  mjs_skipped=
  if command -v node >/dev/null 2>&1; then
    jsctl=$(mktemp -d -t mjs-control.XXXXXX)
    printf 'export const = ;\n' > "$jsctl/bad.mjs"
    if node --check "$jsctl/bad.mjs" >/dev/null 2>&1; then
      rm -rf "$jsctl"; echo "check.sh: node --check accepted its broken control — the gate is broken, not the tree" >&2; exit 1
    fi
    rm -rf "$jsctl"
    for s in scripts/*.mjs; do
      [ -f "$s" ] || continue
      node --check "$s" || { echo "check.sh: $s has a syntax error" >&2; exit 1; }
    done
  else
    mjs_skipped="; node --check of scripts/*.mjs NOT RUN (no node on PATH)"
    echo "check.sh: NODE CHECK NOT RUN — no node on PATH, so scripts/*.mjs were not syntax-checked" >&2
  fi
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
  hermetic) echo "check.sh: HERMETIC GREEN (vet + tests + race + done-checks + script self-tests; live leg NOT run${mjs_skipped:-})" ;;
  live)     echo "check.sh: LIVE GREEN" ;;
  all)      echo "check.sh: ALL GREEN (hermetic + race + script self-tests + live${mjs_skipped:-})" ;;
esac
