#!/bin/sh
# check-fence.sh — invariant 9, as a gate: every peer-controlled value that is
# printed reaches its reader through internal/fence, on ONE line.
#
# WHY A GATE. The hello digest was fenced, the inbox drain was fenced, the
# deny reason was fenced — and `buddy ls`, `buddy sessions`, the identity
# notices, the claim echo and two MCP headers were not (review, 2026-09-08).
# Each of those was written after the rule existed, by someone who knew it.
# A rule that holds only where its author happened to remember it is a
# convention, not an invariant; this turns it back into one.
#
# WHAT IT CHECKS. Every fmt.Fprint*/Sprintf/Errorf statement in the non-test
# Go of the packages that render (cli, buddylist, cmd) is joined into one line
# (an argument list routinely spans several) and searched for a field that
# carries peer text: .Label .Slug .Desc .Scopes .Worktree .Body .From .Note.
# A statement that names one of those must call fence.Line or Fence at least
# as many times as it names them. Counting is coarse on purpose: the precise
# alternative is taint tracking, and a grep that fires slightly too often is
# corrected in thirty seconds, while one that fires slightly too rarely is the
# defect this gate exists for.
#
# TWO CLAUSES (see check.sh for the convention). The scan is one; the other is
# a planted violation the scan MUST flag, so that a respelling of the pattern
# that silently matches nothing cannot pass as "no violations".
#
# Escape hatch: a line ending in `// fence: not peer text` is skipped, and the
# reason has to be on that line, where the next reader sees it.
set -eu
CDPATH= cd -- "$(dirname "$0")/.."

# scan reads Go source on stdin and prints one line per offending statement.
scan() {
  # The field pattern lives INSIDE the program: awk -v processes backslash
  # escapes, and macOS awk has no \b, so the boundary is spelled out.
  awk '
    function flush(   nf, nfence, tmp) {
      if (stmt == "") return
      if (stmt !~ /fence: not peer text/) {
        tmp = stmt; nf = gsub(/\.(Label|Slug|Desc|Scopes|Worktree|Body|From|Note)([^A-Za-z0-9_]|$)/, "&", tmp)
        tmp = stmt; nfence = gsub(/fence\.Line\(|[^a-zA-Z]Fence\(/, "&", tmp)
        if (nf > nfence) printf "%s:%d: %d peer field(s), %d fence call(s): %s\n", FILENAME, start, nf, nfence, stmt
      }
      stmt = ""
    }
    /^[[:space:]]*\/\// { next }
    {
      line = $0; sub(/\/\/.*fence: not peer text.*$/, " // fence: not peer text", line)
      if (stmt == "" && line ~ /fmt\.(Fprintf|Fprint|Fprintln|Sprintf|Sprint|Errorf)\(/) { start = NR; depth = 0 }
      if (stmt != "" || line ~ /fmt\.(Fprintf|Fprint|Fprintln|Sprintf|Sprint|Errorf)\(/) {
        stmt = stmt " " line
        tmp = line; depth += gsub(/\(/, "(", tmp); tmp = line; depth -= gsub(/\)/, ")", tmp)
        if (depth <= 0) flush()
      }
    }
    END { flush() }
  ' "$@"
}

# Clause 2 first: the positive control. If the scanner cannot see THIS, its
# verdict on the real tree means nothing.
control=$(mktemp -t fence-control.XXXXXX.go)
trap 'rm -f "$control"' EXIT
cat > "$control" <<'GO'
package x
func f() {
	fmt.Fprintf(w, "%s %s\n",
		fence.Line(c.Slug, 128), c.Owner.Label)
}
GO
if [ -z "$(scan "$control")" ]; then
  echo "check-fence: the scanner did not flag its own planted violation — the gate is broken, not the tree" >&2
  exit 1
fi

files=$(find internal/cli internal/buddylist cmd -name '*.go' ! -name '*_test.go')
# shellcheck disable=SC2086
hits=$(scan $files)
if [ -n "$hits" ]; then
  echo "check-fence: peer-controlled text printed without internal/fence (invariant 9):" >&2
  echo "$hits" >&2
  echo "  wrap each value in fence.Line(v, cap) — caps: slug 128, label/room 64, desc/scopes/path 512, body 4096" >&2
  exit 1
fi
echo "check-fence: GREEN (positive control flagged; $(echo "$files" | wc -l | tr -d ' ') files clean)"
