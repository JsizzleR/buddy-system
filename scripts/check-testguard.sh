#!/bin/sh
# check-testguard.sh — done-check: every package's TestMain arms the test
# watchdog FIRST (`testguard.Arm()`), and no package whose tests can start a
# child process goes without a TestMain to arm (#130, amending D-053).
#
# Usage: sh scripts/check-testguard.sh
#
# WHY A GATE. D-053 armed only internal/cli, "the only package with a TestMain
# and the one #43 caught". By #130 five more packages had a TestMain
# (internal/harness, internal/store, internal/buddylist, internal/testguard,
# cmd/buddy-adapter), and the harness and adapter tests start real child
# processes, the shape #43 and #53 (D-061) left orphaned and spinning. Measured
# before this change, in a private copy, with a planted test that sleeps: kill
# its `go test` and an internal/harness, internal/store or cmd/buddy-adapter
# test binary was still alive 15 s later, reparented to launchd, with 10
# minutes of -test.timeout to go; internal/cli's was gone in about a second.
# And nothing held cli's one line: deleting it left every test and gate green.
# grep-shaped because no test can see the class: an unarmed package passes
# every test it has, and only the next orphan shows the difference.
#
# WHAT IT CHECKS.
#   1. In every `_test.go` in the tree, each `func TestMain(`
#      has `testguard.Arm()` as its FIRST statement, alone on its line at the
#      body's indentation. First, because in a watchdog process Arm never
#      returns (anything before it runs twice, once in a process that runs no
#      test), and because a wedge or an orphan that happens before it is not
#      bounded. Comment and blank lines before it are allowed.
#   2. Every package that has tests and imports os/exec, syscall or
#      golang.org/x/sys/unix, or names os.StartProcess, in ANY of its .go files
#      has a TestMain in a file the DEFAULT build compiles: the tests drive the
#      package's own code in-process, so a child its code starts is the test
#      binary's child too (D-057's keying, on the import rather than a call
#      spelling).
#   A file may be EXEMPT, by path, with its reason, in one of two modes:
#      late  Arm need not be first, but must still appear as an unconditional
#            statement at the body's indentation;
#      owed  Arm is missing and is owed; the reason says by whom.
#   An exemption is STALE, and red, when its file has no TestMain or already
#   arms first: an exemption outlives its reason only by being noticed.
#
# TWO CLAUSES (the convention check.sh and check-fence.sh use): the scan, and a
# planted control it must agree with exactly, one arm per way to be wrong,
# including the respellings that would quietly defeat clause 1 (Arm late, under
# a condition, deferred, in a helper, only in a comment, bare, missing from a
# second TestMain under a build tag, a comment between `func` and the name, CRLF),
# clause 2's (a reference to os.StartProcess, an aliased syscall, the only
# TestMain under a build tag, a test file at the module root, and a TestMain in
# the external `_test` package, which does cover it) and both exemption modes
# with stale ones. A scan that matched nothing would otherwise report the same
# GREEN as a clean tree, so the real scan must also have read at least six
# TestMains.
#
# DECLARED, NOT CLOSED (D-005: this gate is for the accidental case). A line
# opening `func TestMain(` at column 0 inside a raw string or a /* */ comment
# reads as a declaration, and can stand in for a missing one; an aliased "os"
# import calling StartProcess is not seen by clause 2. Telling those apart needs
# a Go parser (Codex code pass, answered).
#
# Considered and cut: keying clause 1 on ScrubGitEnv (#130's proposal, "a
# TestMain that calls ScrubGitEnv also calls Arm"). internal/store's TestMain
# runs no git and calls no ScrubGitEnv, and a store test can wedge as well as a
# git one; every TestMain arms, which is the simpler rule to hold. Arming a
# package with no TestMain by adding one only to arm it is clause 2's job and
# only where a child can be started: ircwire, tocwire and fence start none.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"
export LC_ALL=C

# The exemptions: `<path> <late|owed> <reason>`, one per line.
EXEMPT='internal/testguard/testguard_test.go late the package under test: a helper'"'"'s hold mode and the exit hook that records why it exited must be in place before Arm starts the poll and the watchdog'

# A TestMain declaration: `func`, then blanks or /* */ comments, then the name
# (Codex code pass: `func /* entrypoint */ TestMain(` is legal Go and gofmt
# keeps it). No receiver: a method named TestMain is not one.
TM='^func([[:space:]]|/\*[^*]*\*/)+TestMain\('
# A package that may start a child: it imports os/exec, syscall or x/sys/unix,
# or names os.StartProcess, in any .go file. Keyed on the import, not a call
# spelling, so `start := os.StartProcess` and `sc "syscall"` count too (Codex).
SPAWN='"os/exec"|"syscall"|"golang.org/x/sys/unix"|os\.StartProcess'

# tmfirst <file> — one line per TestMain in <file>: `<line>|<toplevel-arm>|<first statement>`:
# 1 when an unconditional Arm() statement stands anywhere at the body's
# indentation, and the first line of its body that is neither blank nor a //
# comment (text after the opening brace on the func line counts as one). The
# statement is last so a `|` in it (`if a || b {`) stays in it. A CR before
# the newline is dropped, so a CRLF file reads like any other.
tmfirst() {
	awk '
		{ sub(/\r$/, "") }
		function flush() { if (on) print ln "|" top "|" first; on = 0 }
		/^func([ \t]|\/\*[^*]*\*\/)+TestMain\(/ {
			flush(); on = 1; ln = NR; first = ""; top = 0
			rest = $0; sub(/^[^{]*\{/, "", rest)
			if (rest ~ /[^ \t]/) first = rest
			next
		}
		on && /^}/ { flush(); next }
		on {
			if (first == "" && $0 !~ /^[ \t]*$/ && $0 !~ /^[ \t]*\/\//) first = $0
			if ($0 ~ /^\t(testguard\.)?Arm\(\)$/) top = 1
		}
		END { flush() }
	' "$1"
}

# armscan <exemptions> <module-root> — `<dir>|<why>` for each violation and
# stale exemption (dir `.` is the root), and `count|<n>` last: the TestMains
# read. `!|<why>` when it could not look. A subshell: it changes directory.
#
# Clause 1 reads every _test.go as text, build constraint or not, so a file only
# another platform or tag compiles is held too. Clause 2 asks `go list` which
# test files the DEFAULT build compiles, and only a TestMain in one of those
# covers the package: a TestMain under `//go:build watchdog`, or in a
# `_linux_test.go` on darwin, leaves the plain `go test` binary unarmed (Codex
# code pass). -find: list the files, resolve no imports (no network, no cache).
armscan() (
	ex=$1
	cd -- "$2" || { echo "!|no directory $2"; exit 0; }
	export GOFLAGS= GOWORK=off GOPROXY=off
	mod=$(go list -m 2>&1) || { printf '!|go list -m failed in %s: %s\n' "$2" "$mod"; exit 0; }
	incl=$(go list -e -find -f '{{.ImportPath}}|{{join .TestGoFiles " "}} {{join .XTestGoFiles " "}}' ./... 2>&1) ||
		{ printf '!|go list failed in %s: %s\n' "$2" "$incl"; exit 0; }
	files=$(find . \( -name .git -o -name .cache -o -name testdata -o -name node_modules \) -prune -o -name '*_test.go' -print | sed 's#^\./##' | sort)
	dirs=$(printf '%s\n' "$files" | awk 'NF { if (index($0, "/")) { sub(/\/[^\/]*$/, ""); print } else print "." }' | sort -u)
	n=0
	for f in $(grep -lE "$TM" $files 2>/dev/null || true); do
		d=$(dirname "$f")
		mode=$(printf '%s\n' "$ex" | awk -v f="$f" '$1 == f { print $2; exit }')
		armedfirst=0
		while IFS='|' read -r ln top first; do
			[ -n "$ln" ] || continue
			n=$((n + 1))
			if printf '%s\n' "$first" | grep -qE '^	(testguard\.)?Arm\(\)$'; then
				# Either spelling makes an exemption stale; only the qualified one
				# passes without one (a bare Arm() elsewhere is some other Arm).
				armedfirst=1
				printf '%s\n' "$first" | grep -qE '^	testguard\.Arm\(\)$' || [ -n "$mode" ] ||
					printf '%s|%s:%s: TestMain opens with a bare Arm(), not testguard.Arm()\n' "$d" "$f" "$ln"
				continue
			fi
			case $mode in
			late) [ "$top" = 1 ] || printf "%s|%s:%s: exempt as late, but no unconditional Arm() at TestMain's indentation\n" "$d" "$f" "$ln" ;;
			owed) ;;
			*) printf "%s|%s:%s: TestMain's first statement is not testguard.Arm(): %s\n" "$d" "$f" "$ln" "$(printf '%s' "$first" | sed 's/^[[:space:]]*//')" ;;
			esac
		done <<EOF
$(tmfirst "$f")
EOF
		if [ -n "$mode" ] && [ "$armedfirst" = 1 ]; then
			printf '%s|%s: exemption (%s) is stale: this file arms first; drop it from EXEMPT\n' "$d" "$f" "$mode"
		fi
	done
	for d in $dirs; do
		grep -qsE "$SPAWN" "$d"/*.go || continue
		if [ "$d" = . ]; then ip=$mod; else ip=$mod/$d; fi
		covered=0
		for t in $(printf '%s\n' "$incl" | awk -F'|' -v ip="$ip" '$1 == ip { print $2 }'); do
			grep -qsE "$TM" "$d/$t" && covered=1
		done
		[ "$covered" = 1 ] || printf '%s|%s: may start child processes (%s) and no TestMain in a file the default build compiles arms it\n' "$d" "$d" "os/exec, syscall, x/sys/unix or os.StartProcess"
	done
	printf '%s\n' "$ex" | while read -r f mode _; do
		[ -n "$f" ] || continue
		grep -qsE "$TM" "$f" || printf '%s|%s: exemption (%s) is stale: no TestMain in it\n' "$(dirname "$f")" "$f" "$mode"
	done
	echo "count|$n"
)

# THE CONTROL. Each package of a throwaway module is one way to be right or
# wrong; the scan must name exactly the wrong ones. Nothing here is compiled;
# `go list -find` reads only package clauses, imports and build constraints.
ctl=$(mktemp -d -t testguard-control.XXXXXX)
trap 'rm -rf "$ctl"' EXIT
printf 'module ctl\n\ngo 1.26\n' > "$ctl/go.mod"
mk() { mkdir -p "$ctl/$1"; printf "$2" > "$ctl/$1/${3:-x_test.go}"; }
H='package p\nimport "testing"\n'
X='package p\nimport (\n\t"os/exec"\n\t"testing"\n)\nfunc TestX(t *testing.T) { exec.Command("true").Run() }\n'
ARMED='func TestMain(m *testing.M) {\n\ttestguard.Arm()\n\tos.Exit(m.Run())\n}\n'
mk a "${H}${ARMED}"
mk b "${H}func TestMain(m *testing.M) {\n\ttestguard.ScrubGitEnv()\n\tos.Exit(m.Run())\n}\n"
mk c "${H}func TestMain(m *testing.M) {\n\ttestguard.ScrubGitEnv()\n\ttestguard.Arm()\n\tos.Exit(m.Run())\n}\n"
mk d "${H}func TestMain(m *testing.M) {\n\tif os.Getenv(\"CI\") == \"\" {\n\t\ttestguard.Arm()\n\t}\n\tos.Exit(m.Run())\n}\n"
mk e "${H}func TestMain(m *testing.M) {\n\t// testguard.Arm()\n\tos.Exit(m.Run())\n}\n"
mk f "$X"
mk g "${H}func TestMain(m *testing.M) {\n\t// Why it arms first.\n\n\ttestguard.Arm()\n\tos.Exit(m.Run())\n}\n"
mk h "${H}func TestMain(m *testing.M) {\n\tdefer testguard.Arm()\n\tos.Exit(m.Run())\n}\n"
mk i "${H}func TestX(t *testing.T) {}\n"
mk j "${H}func arm() { testguard.Arm() }\nfunc TestMain(m *testing.M) {\n\tarm()\n\tos.Exit(m.Run())\n}\n"
mk k "${H}func TestMain(m *testing.M) {\n\tsetup()\n\tArm()\n\tos.Exit(m.Run())\n}\n"
mk l "${H}func TestMain(m *testing.M) {\n\tsetup()\n\tif x {\n\t\tArm()\n\t}\n\tos.Exit(m.Run())\n}\n"
mk m "${H}func TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n"
mk n "${H}${ARMED}"
mk p "${H}${ARMED}"
mk p "//go:build live\n\n${H}func TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n" y_test.go
mk q "${H}func TestMain(m *testing.M) {\n\ttestguard.Arm(); os.Exit(m.Run())\n}\n"
mk s "${H}func TestMain(m *testing.M) {\n\tok := a || b\n\t_ = ok\n\tArm()\n\tos.Exit(m.Run())\n}\n"
mk t "${H}func /* entrypoint */ TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n"
mk u "$X"
mk u "//go:build watchdog\n\n${H}${ARMED}" main_test.go
mk v "package p\nimport (\n\t\"os\"\n\t\"testing\"\n)\nfunc TestX(t *testing.T) { start := os.StartProcess; _ = start }\n"
mk w "package p\nimport (\n\tsc \"syscall\"\n\t\"testing\"\n)\nfunc TestX(t *testing.T) { _ = sc.ForkExec }\n"
mk x "${H}func TestMain(m *testing.M) {\n\tArm()\n\tos.Exit(m.Run())\n}\n"
mk y "package p\r\nimport \"testing\"\r\nfunc TestMain(m *testing.M) {\r\n\ttestguard.Arm()\r\n\tos.Exit(m.Run())\r\n}\r\n"
mk z "package p\r\nimport \"testing\"\r\nfunc TestMain(m *testing.M) {\r\n\ttestguard.Arm()\r\n\tos.Exit(m.Run())\r\n}\r\n"
mk aa "$X"
mk aa "${H}${ARMED}" main_test.go
mk ab "$X"
mk ab "package p_test\nimport \"testing\"\n${ARMED}" main_test.go
mk ac "${H}func TestMain(m *testing.M) {\n\tArm()\n\tos.Exit(m.Run())\n}\n"
printf "$X" > "$ctl/root_test.go"
mkdir -p "$ctl/r"
ctlex="k/x_test.go late control
l/x_test.go late control
m/x_test.go owed control
n/x_test.go owed control
r/x_test.go owed control
s/x_test.go late control
x/x_test.go late control
y/x_test.go owed control
gone/x_test.go owed control"
out=$(armscan "$ctlex" "$ctl")
got=$(printf '%s\n' "$out" | grep -v '^count|' | cut -d'|' -f1 | sort -u | tr '\n' ' ')
want=". ac b c d e f gone h j l n p q r t u v w x y "
[ "$got" = "$want" ] || {
	echo "check-testguard: FAIL — the scan reported '$got' of its control, want '$want': the gate is broken, not the tree" >&2
	printf '%s\n' "$out" >&2
	exit 1
}
ctln=$(printf '%s\n' "$out" | sed -n 's/^count|//p')
[ "$ctln" = 24 ] || { echo "check-testguard: FAIL — the scan read $ctln TestMains in its control, want 24" >&2; exit 1; }

# The real tree: every _test.go in the module, wherever it is (a new top-level
# directory, or the root itself, is not exempt by being new), but no copy:
# .cache holds downloaded modules and testdata holds fixtures nothing compiles.
out=$(armscan "$EXEMPT" .)
bad=$(printf '%s\n' "$out" | grep -v '^count|' | cut -d'|' -f2- || true)
n=$(printf '%s\n' "$out" | sed -n 's/^count|//p')
[ -z "$bad" ] || {
	echo "check-testguard: FAIL — a test binary these packages build is not bounded by the watchdog (#130, D-053):" >&2
	printf '  %s\n' "$bad" >&2
	echo "  make \`testguard.Arm()\` the first statement of TestMain (see internal/cli/cli_test.go), or add a TestMain that does" >&2
	exit 1
}
[ "$n" -ge 6 ] || { echo "check-testguard: FAIL — the scan read only $n TestMains in the tree, want at least 6: the scan is broken" >&2; exit 1; }
nex=$(printf '%s\n' "$EXEMPT" | grep -c .)
echo "check-testguard: GREEN (29-package control held, 24 TestMains; $n TestMains read, each arms first or is one of $nex named exemptions)"
