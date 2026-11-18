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
# WHAT IT CHECKS. Every print statement in the non-test Go of the packages that
# render (cli, buddylist, cmd) is joined into one line (an argument list
# routinely spans several) and searched for a field that carries peer text:
# .Label .Slug .Desc .Scopes .Worktree .Body .From .Note .Terminal (the last is
# read from an ENVIRONMENT and printed into other sessions' context, which is
# the same trigger), and .Room .Path .Paths .Nick .Members .Outcome .Reason,
# which invariant 9 names too. A print statement is any fmt print verb (Print,
# Printf, Println, Fprint*, Sprint*, Errorf, Append*), log's Print*/Fatal*/
# Panic*, the builtin print/println, any .WriteString (io.WriteString too), and
# writeWakeCopy (cli's printf wrapper). Outside a print statement, a line that
# CONCATENATES a peer field with `+` or `+=` is checked the same way: the
# string it builds is printed somewhere, and `"[" + c.Label + "] "` is how the
# unfenced chat slot of #109 was spelled.
# A statement (or line) that names peer fields must call fence.Line,
# fence.Field, fence.Slot (Line, quoted when it could close a delimited slot;
# D-206), Fence or scopeList at least as many times as it names them. scopeList
# is the one renderer of a claim's scope list (D-066): it fences each scope with
# fence.Field. Counting it by name is an allowance, so a clause below checks
# that its body still calls fence.Field: a scopeList that stopped fencing would
# otherwise pass every statement that uses it. Fence (buddylist's alias) is
# held to the same: it must be exactly the one-line wrapper of fence.Line.
# (Field is Line plus a guarantee that the value stays ONE whitespace-delimited
# field — see issue #6 — so it counts as fencing, and a column that needs it is
# not a second rule but the same one applied where the columns are.) Counting
# is coarse on purpose: the precise alternative is taint tracking, and a grep
# that fires slightly too often is corrected in thirty seconds, while one that
# fires slightly too rarely is the defect this gate exists for.
#
# WIDENED BY #129 (D-221). It used to start a statement only at six fmt verbs
# (Fprintf Fprint Fprintln Sprintf Sprint Errorf). An audit planted
# `fmt.Printf("%s\n", c.Slug)`, `fmt.Println(c.Desc)`, `log.Printf("%s",
# c.Slug)` and `return "[" + c.Label + "] " + c.Desc` in internal/cli: all four
# GREEN, the Fprintf control red. Widening the verbs found nothing new on the
# tree; what it found was the scanner's own blindness, which the panel and the
# Codex design pass reproduced in a private copy (each is a planted control
# below, so it cannot come back unnoticed):
#   - it matched on RAW text, so a `)` inside a string closed a statement early
#     (`fmt.Printf(")%s",` then `c.Label)` on the next line: never scanned) and
#     a `(` kept one open to borrow the next statement's fence call; a fence
#     name in a string or a comment counted as a fence call (`/* fence.Line( */`,
#     `// TODO: fence.Line(`); and the exemption marker in a string exempted.
#     Now every line is TOKENIZED first: string, raw-string (multi-line) and
#     rune literals are blanked and comments removed, and only then are
#     statements found, depth counted, fields and fence calls counted;
#   - the exemption covered a whole multi-line statement from any one of its
#     lines. It now drops only the fields on its own line, and needs a reason;
#   - line numbers were NR, cumulative across files (store.go:37639 for line
#     1449): now FNR, with the file the statement started in.
# Kept open, recorded in D-221 rather than widened: a fence call on the WRONG
# value pays for a raw one (the count is coarse); a concatenation split across
# lines; Write/io.Copy/errors.New/strings.Join; %v of a whole struct; templates;
# a hoisted local (`s := c.Slug`, D-066's blind spot); log.Default().Printf; a
# method value of WriteString; packages outside the three (internal/store's
# renderers, internal/harness).
#
# TWO CLAUSES (see check.sh for the convention). The scan is one: a new
# occurrence. The other is that the scan cannot be quietly defeated by
# respelling the line it would flag, and every guard here has a planted
# violation it MUST flag first, so that a respelling of the pattern that
# silently matches nothing cannot pass as "no violations":
#   R1 a renamed or dot import of fmt or log (`import p "fmt"` makes `p.Printf`
#      invisible), anywhere in the module's non-test Go;
#   R2 a print verb used as a VALUE (`pr := fmt.Printf`), or a selector broken
#      at its dot (`fmt.` ending a line, which gofmt leaves), module-wide;
#   R3 a new printf-style function (`format string, args ...any`) in a scanned
#      package that is not on the list below: a wrapper is a starter nobody
#      added;
#   R4 the counted helpers are honest: Fence is exactly `return fence.Line(s,
#      max)`, scopeList's body (comments stripped) calls fence.Field, errResult
#      fences what it formats, and `.Fence(`/`.scopeList(` (a method any type
#      could define) do not count.
#
# Escape hatch: `// fence: not peer text — <reason>` as the line's trailing
# comment drops that line's fields, and the reason has to be on that line,
# where the next reader sees it.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"

# The awk library every check below shares. tok() is the tokenizer: it sets
# CODE (the line with string/rune literal contents blanked and comments
# removed) and LCMT (the line's trailing // comment), carrying raw-string and
# block-comment state across lines in INRAW and INBLK. Fields and fence names
# are patterns INSIDE the program: awk -v processes backslash escapes, and
# macOS awk has no \b, so the boundaries are spelled out.
AWKLIB='
function tok(line,   n, i, c, d, out) {
  out = ""; LCMT = ""; n = length(line)
  for (i = 1; i <= n; i++) {
    c = substr(line, i, 1); d = substr(line, i + 1, 1)
    if (INBLK) { if (c == "*" && d == "/") { INBLK = 0; i++; out = out " " } ; continue }
    if (INRAW) { if (c == "`") { INRAW = 0; out = out "`" } ; continue }
    if (c == "/" && d == "/") { LCMT = substr(line, i + 2); break }
    if (c == "/" && d == "*") { INBLK = 1; i++; continue }
    if (c == "`") { INRAW = 1; out = out "`"; continue }
    if (c == "\"" || c == "\047") {
      out = out c
      for (i++; i <= n; i++) {
        d = substr(line, i, 1)
        if (d == "\\") { i++; continue }
        if (d == c) break
      }
      out = out c
      continue
    }
    out = out c
  }
  CODE = out
}
function nfields(s) { return gsub(/\.(Label|Slug|Desc|Scopes|Worktree|Body|From|Note|Terminal|Room|Path|Paths|Nick|Members|Outcome|Reason)([^A-Za-z0-9_]|$)/, "&", s) }
function nfences(s) { return gsub(/fence\.Line\(|fence\.Field\(|fence\.Slot\(|(^|[^A-Za-z0-9_.])Fence\(|buddylist\.Fence\(|(^|[^A-Za-z0-9_.])scopeList\(/, "&", s) }
function exempt(c,   r) {
  if (c !~ /^[ \t]*fence: not peer text/) return 0
  r = c; sub(/^[ \t]*fence: not peer text[ \t]*/, "", r); gsub(/^[-—:,; \t]+/, "", r)
  return r != ""
}
function starts(s) {
  return s ~ /(^|[^A-Za-z0-9_.])(fmt[ \t]*\.[ \t]*(Print|Printf|Println|Fprint|Fprintf|Fprintln|Sprint|Sprintf|Sprintln|Errorf|Append|Appendf|Appendln)|log[ \t]*\.[ \t]*(Print|Printf|Println|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln)|print|println|writeWakeCopy)[ \t]*\(|\.WriteString\(/
}
# concat: a field after a `+` (through a call: `+ strings.TrimSpace(c.Desc)`),
# or before one, directly (`c.Label + "] "`, as gofmt spells `c.Slug+" is held"`) or
# later on the line. The before-arm once required a character between the
# field and ` + `, so the direct spellings passed GREEN (Fable cross-check).
# `c.Path++` and `c.Path += 1` are not concatenation (Codex re-verify).
function concat(s) {
  return s ~ /\+=?[ \t]*[A-Za-z_][A-Za-z0-9_.(]*\.(Label|Slug|Desc|Scopes|Worktree|Body|From|Note|Terminal|Room|Path|Paths|Nick|Members|Outcome|Reason)([^A-Za-z0-9_]|$)/ ||
         s ~ /\.(Label|Slug|Desc|Scopes|Worktree|Body|From|Note|Terminal|Room|Path|Paths|Nick|Members|Outcome|Reason)([ \t]*\+([^+=]|$)|[^A-Za-z0-9_].*[ \t]\+[ \t])/
}
'

# scan reads Go source and prints one line per offending statement or line.
scan() {
  awk "$AWKLIB"'
    function flush() {
      if (stmt != "" && nf > nfc) printf "%s:%d: %d peer field(s), %d fence call(s): %s\n", sfile, start, nf, nfc, stmt
      stmt = ""; nf = 0; nfc = 0
    }
    FNR == 1 { flush(); INRAW = 0; INBLK = 0 }
    {
      tok($0); f = nfields(CODE); if (exempt(LCMT)) f = 0
      if (stmt == "" && starts(CODE)) { start = FNR; sfile = FILENAME; depth = 0 }
      if (stmt != "") {
        stmt = stmt " " $0; nf += f; nfc += nfences(CODE)
      } else if (starts(CODE)) {
        stmt = $0; nf = f; nfc = nfences(CODE)
      } else {
        if (f > 0 && concat(CODE) && f > nfences(CODE)) printf "%s:%d: %d peer field(s) concatenated, %d fence call(s): %s\n", FILENAME, FNR, f, nfences(CODE), $0
        next
      }
      tmp = CODE; depth += gsub(/\(/, "(", tmp); tmp = CODE; depth -= gsub(/\)/, ")", tmp)
      if (depth <= 0) flush()
    }
    END { flush() }
  ' "$@"
}

# respell prints one line per R1/R2/R3 violation in the files given.
respell() {
  awk "$AWKLIB"'
    FNR == 1 { INRAW = 0; INBLK = 0; INIMP = 0 }
    {
      raw = $0; tok($0)
      # R1 reads only an import line or a line inside an `import (` block:
      # read anywhere, `return "log"` was a renamed import (Fable cross-check).
      # A block opens at `import (` with or without its first spec on the line,
      # and closes at a `)` ending a line, `);` too (Codex re-verify).
      isimp = INIMP || CODE ~ /^import[ \t]/
      if (CODE ~ /^import[ \t]*\(/ && CODE !~ /\)[ \t;]*$/) INIMP = 1
      else if (INIMP && CODE ~ /\)[ \t;]*$/) INIMP = 0
      # The path is read on the raw line WITHOUT its trailing comment:
      # `import p "os" // replaces "fmt"` is not an import of fmt.
      rawc = raw; sub(/[ \t]*\/\/.*$/, "", rawc)
      # R1: an import spec with a name, read on CODE (so the text of a raw string is
      # not an import, and a comment between name and path is gone), whose path,
      # blanked in CODE, the raw line spells. `_` alone is a blank import and
      # cannot be called; `_fmt` is a name like any other (Codex code pass).
      spec = CODE; sub(/^[ \t]*(import[ \t]+)?\(?[ \t]*/, "", spec)
      if (isimp && spec ~ /^([A-Za-z_][A-Za-z0-9_]*|\.)[ \t]+["`]["`][ \t]*\)?[ \t]*$/ && spec !~ /^_[ \t]/ &&
          (rawc ~ /["`](fmt|log)["`]/))
        printf "%s:%d: R1 renamed or dot import of fmt/log: %s\n", FILENAME, FNR, raw
      if (CODE ~ /(^|[^A-Za-z0-9_.])(fmt|log)[ \t]*\.[ \t]*(Print|Printf|Println|Fprint|Fprintf|Fprintln|Sprint|Sprintf|Sprintln|Errorf|Append|Appendf|Appendln|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln)([^A-Za-z0-9_( \t]|[ \t]*$)/ ||
          CODE ~ /(^|[^A-Za-z0-9_.])(fmt|log)[ \t]*\.[ \t]*$/)
        printf "%s:%d: R2 print verb used as a value or split at its dot: %s\n", FILENAME, FNR, raw
    }
  ' "$@"
}

# r3 prints each printf-style function (`format string, args ...any`) that is
# not on the list: errResult fences what it formats (checked below), and
# writeWakeCopy is a starter.
r3() {
  awk "$AWKLIB"'
    FNR == 1 { INRAW = 0; INBLK = 0; sig = "" }
    { tok($0) }
    # A signature may span lines (one parameter per line): join it from `func`
    # to the line holding its `{` (or the end of a bodiless declaration).
    # One action: awk has no `else` between two pattern-action rules (written
    # that way, the whole program failed to parse and r3 flagged nothing).
    { if (sig == "" && CODE ~ /^func[ \t]/) { sig = CODE; at = FNR } else if (sig != "") sig = sig " " CODE }
    # The end is a `{` that is not an empty interface{} or struct{} (the one in
    # `args ...interface{},` ended the join early), or a bodiless line ending
    # in a result type, qualified too: `func external() io.Writer` swallowed
    # the next function into its signature (Codex re-verify).
    { e = CODE; gsub(/(interface|struct)[ \t]*\{[ \t]*\}/, "", e) }
    sig != "" && (e ~ /\{/ || e ~ /\)[ \t]*[A-Za-z_*.\[\]]*[ \t]*$/ && e !~ /,[ \t]*$/ && e !~ /\([ \t]*$/) {
      flat = sig; gsub(/[ \t]+/, " ", flat); gsub(/\( /, "(", flat); gsub(/, \)/, ")", flat); gsub(/,\)/, ")", flat)
      # Matched in the FIRST parameter list only: a func-typed result
      # `func factory() func(format string, args ...any)` is not a wrapper.
      if (flat ~ /^func (\([^)]*\) ?)?[A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])? ?\(([^()]*, ?)?[A-Za-z_][A-Za-z0-9_]* string, ?[A-Za-z_][A-Za-z0-9_]* \.\.\.(any|interface\{\})\)/) {
        name = flat; sub(/^func (\([^)]*\) ?)?/, "", name); sub(/[\[(].*/, "", name)
        if (name != "errResult" && name != "writeWakeCopy")
          printf "%s:%d: R3 printf-style %s is not a starter (add it to check-fence.sh, or fence inside and list it): %s\n", FILENAME, at, name, flat
      }
      sig = ""
    }
  ' "$@"
}

# honest FUNC PATTERN FILE...: every definition of FUNC in FILE..., comments
# stripped by the tokenizer (a `// was fence.Field(` must not pass), contains
# PATTERN. Prints each one that does not; prints "none" if FUNC is not defined.
honest() {
  fn=$1; pat=$2; shift 2
  awk "$AWKLIB"'
    FNR == 1 { INRAW = 0; INBLK = 0 }
    { tok($0) }
    !in_f && CODE ~ "^func[ \t]+(\\([^)]*\\)[ \t]*)?" fn "[[(]" { in_f = 1; body = ""; at = FILENAME ":" FNR; seen++ }
    in_f { body = body " " CODE; if (CODE ~ /^}/ || (CODE ~ /}[ \t]*$/ && at == FILENAME ":" FNR)) { if (body !~ pat) print at ": " fn " does not fence"; in_f = 0 } }
    END { if (!seen) print "none" }
  ' fn="$fn" pat="$pat" "$@"
}

control=$(mktemp -t fence-control.XXXXXX)
trap 'rm -f "$control"' EXIT

# must_flag NAME CHECK: the planted Go on stdin must make CHECK print something.
must_flag() {
  name=$1; shift
  cat > "$control"
  if [ -z "$("$@" "$control" 2>/dev/null || true)" ]; then
    echo "check-fence: the scanner did not flag its planted violation ($name) — the gate is broken, not the tree" >&2
    exit 1
  fi
}

# Clause 2 first: the positive controls. If the scanner cannot see THESE, its
# verdict on the real tree means nothing. One per verb family, one per shape
# the old scanner passed (#129's plants and the panel's), so a respelling of
# any one pattern that silently matches nothing goes red here.
must_flag fprintf scan <<'GO'
package x
func f() {
	fmt.Fprintf(w, "%s %s\n",
		fence.Line(c.Slug, 128), c.Owner.Label)
}
GO
must_flag printf scan <<'GO'
func f() { fmt.Printf("%s\n", c.Slug) }
GO
must_flag println scan <<'GO'
func f() { fmt.Println(c.Desc) }
GO
must_flag log scan <<'GO'
func f() { log.Printf("%s", c.Label) }
GO
must_flag builtin scan <<'GO'
func f() { println(c.Slug) }
GO
must_flag writestring scan <<'GO'
func f() { b.WriteString(si.Terminal) }
GO
must_flag io-writestring scan <<'GO'
func f() { io.WriteString(w, c.Note) }
GO
must_flag wakecopy scan <<'GO'
func f() { writeWakeCopy(env, "%s\n", c.Room) }
GO
must_flag concat scan <<'GO'
func f(c Claim) string { return "[" + c.Label + "] " + c.Desc }
GO
must_flag concat-assign scan <<'GO'
func f() { out += c.Slug }
GO
must_flag paren-in-string-closes-early scan <<'GO'
func f() {
	fmt.Printf(")%s",
		c.Label)
}
GO
must_flag paren-in-string-keeps-open scan <<'GO'
func f() {
	fmt.Fprintf(w, "(%s\n", c.Slug)
	fmt.Fprintln(w, fence.Line(note, 512))
}
GO
must_flag block-comment-fence scan <<'GO'
func f() { fmt.Printf("%s", /* fence.Line() */ c.Label) }
GO
must_flag line-comment-fence scan <<'GO'
func f() { fmt.Fprintf(w, "%s\n", c.Slug) // TODO: fence.Line(
}
GO
must_flag string-fence scan <<'GO'
func f() { fmt.Printf("fence.Line( %s \" fence.Line(", c.Slug) }
GO
must_flag raw-string-fence scan <<'GO'
func f() { fmt.Printf(`fence.Line(
%s`, c.Slug) }
GO
must_flag rune-quote scan <<'GO'
func f() { fmt.Printf("%c%s", '"', c.Slug) }
GO
must_flag exemption-in-string scan <<'GO'
func f() { fmt.Printf("%s // fence: not peer text — x", c.Slug) }
GO
must_flag exemption-leaks scan <<'GO'
func f() {
	fmt.Fprintf(w, "%s %s\n",
		version, // fence: not peer text — version is ours
		c.Slug)
}
GO
must_flag exemption-without-reason scan <<'GO'
func f() { fmt.Printf("%s\n", c.Slug) // fence: not peer text
}
GO
must_flag method-fence scan <<'GO'
func f() { fmt.Printf("%s\n", x.Fence(c.Slug, 128)) }
GO
must_flag method-scopelist scan <<'GO'
func f() { fmt.Printf("%s\n", t.scopeList(c.Scopes)) }
GO
must_flag r1-renamed respell <<'GO'
import p "fmt"
GO
must_flag r1-dot respell <<'GO'
import (
	. "log"
)
GO
must_flag r1-backtick respell <<'GO'
import p `fmt`
GO
must_flag r2-value respell <<'GO'
func f() { pr := fmt.Printf; pr("%s", c.Slug) }
GO
must_flag r2-split respell <<'GO'
func f() {
	fmt.
		Printf("%s\n", c.Slug)
}
GO
must_flag r1-underscore-name respell <<'GO'
import _fmt "fmt"
GO
must_flag r1-comment-between respell <<'GO'
import f /* why */ "fmt"
GO
must_flag split-by-comment scan <<'GO'
func f() { fmt. /* why */ Println(c.Slug) }
GO
must_flag r3-multiline r3 <<'GO'
func say(
	format string,
	args ...any,
) {
	fmt.Printf(format, args...)
}
GO
must_flag r3-multiline-interface r3 <<'GO'
func say(
	format string,
	args ...interface{},
) {
	fmt.Printf(format, args...)
}
GO
must_flag r3-after-bodiless r3 <<'GO'
func writeWakeCopy() io.Writer
func say(format string, args ...any) {}
GO
must_flag r1-opener-with-spec respell <<'GO'
import ("os"
	f "fmt"
)
GO
must_flag r3-wrapper r3 <<'GO'
func say(w io.Writer, format string, args ...any) { fmt.Fprintf(w, format, args...) }
GO
must_flag r3-method r3 <<'GO'
func (l *lg) note(format string, args ...interface{}) { fmt.Fprintf(l.w, format, args...) }
GO
must_flag concat-field-first scan <<'GO'
func f(c Claim) string { return c.Label + "] " }
GO
must_flag concat-field-first-tight scan <<'GO'
func f() { warn(env, c.Slug+" is held") }
GO
must_flag concat-through-call scan <<'GO'
func f(c Claim) string { return "claim " + strings.TrimSpace(c.Desc) }
GO
# One plant per alternative: the lists are alternations, and deleting one
# member (`Sprintf|`, `|Fatalf`, a field) left every plant above flagging
# and the gate GREEN (Fable cross-check: 20 such mutants survived).
for v in Print Printf Println Fprint Fprintf Fprintln Sprint Sprintf Sprintln Errorf Append Appendf Appendln; do
  printf 'func f() { fmt.%s(w, c.Slug) }\n' "$v" | must_flag "fmt.$v" scan
  printf 'func f() { pr := fmt.%s; pr(c.Slug) }\n' "$v" | must_flag "r2-fmt.$v" respell
done
for v in Print Printf Println Fatal Fatalf Fatalln Panic Panicf Panicln; do
  printf 'func f() { log.%s(c.Slug) }\n' "$v" | must_flag "log.$v" scan
  printf 'func f() { pr := log.%s; pr(c.Slug) }\n' "$v" | must_flag "r2-log.$v" respell
done
for fld in $(echo "Label|Slug|Desc|Scopes|Worktree|Body|From|Note|Terminal|Room|Path|Paths|Nick|Members|Outcome|Reason" | tr '|' ' '); do
  printf 'func f() { fmt.Println(c.%s) }\n' "$fld" | must_flag "field-$fld" scan
  printf 'func f() string { return "x" + c.%s }\n' "$fld" | must_flag "concat-after-$fld" scan
  printf 'func f() string { return c.%s + "x" }\n' "$fld" | must_flag "concat-before-$fld" scan
done

# The report names the file and line the statement STARTED at, counted per
# file: NR put the second file's line 2 at its cumulative line.
control2=$(mktemp -t fence-control2.XXXXXX)
trap 'rm -f "$control" "$control2"' EXIT
printf 'package x\nfunc a() {}\nfunc b() {}\n' > "$control"
printf 'package x\nfunc f() { fmt.Printf("%%s", c.Slug) }\n' > "$control2"
case $(scan "$control" "$control2") in
  "$control2:2: "*) ;;
  *) echo "check-fence: the scanner misreported where a statement is (want $control2:2) — the gate is broken, not the tree" >&2; exit 1 ;;
esac

# Negative controls: what the gate must NOT flag, so a respelling that flags
# everything (and trains everyone to the exemption) is caught too.
for ok in \
  'func f() { fmt.Printf("%s\n", fence.Line(c.Slug, 128)) }' \
  'func f() { fmt.Printf("%s\n", c.Slug) // fence: not peer text — a test fixture'"'"'s literal' \
  'func f() { fmt.Printf /* why */ ("%s\n", fence.Field(c.Label, 64)) }' \
  'func f() { s := "[" + fence.Slot(c.Label, 64) + "] " }' \
  'func f() { fmt.Printf("%s", buddylist.Fence(c.Body, 4096)) }' \
  'import _ "fmt"' \
  'func f() string {
	return "log"' \
  'import (
	_ "fmt"
);
func f() string {
	return "log"' \
  'import p "os" // replaces "fmt"' \
  'func bump(c *T) { c.Path++; c.Path += 1 }' \
  'func factory() func(
	format string,
	args ...any,
) {
	return nil' \
  'const example = `
import p "fmt"
`'
do
  printf '%s\n}\n' "$ok" > "$control"
  if [ -n "$(scan "$control")$(respell "$control")$(r3 "$control")" ]; then
    echo "check-fence: the scanner flagged a fenced or exempted line — the gate is broken, not the tree: $ok" >&2
    exit 1
  fi
done

# R4: the counted helpers are honest. Each check fires on its plant first.
printf 'package x\n// was fence.Field(s, 512)\nfunc scopeList(scopes []string) string {\n\t// fence.Field(\n\treturn strings.Join(scopes, ", ")\n}\n' > "$control"
case $(honest scopeList 'fence\\.Field\\(' "$control") in
  "") echo "check-fence: the scopeList check passed a planted scopeList that does not fence — the gate is broken, not the tree" >&2; exit 1 ;;
esac
# fence_defs FILE...: every definition of Fence (a generic one too: `func
# Fence[S ~string](` is a definition the call count would credit, Codex code
# pass) that is not the one-line wrapper, byte for byte.
fence_defs() { grep -nE '^func Fence[[(]' /dev/null "$@" | grep -vE ':func Fence\(s string, max int\) string \{ return fence\.Line\(s, max\) \}$' || true; }
for plant in 'func Fence(s string, max int) string { _ = fence.Line("", max); return s }' \
  'func Fence[S ~string](s S, max int) string { return string(s) }'; do
  printf 'package x\n%s\n' "$plant" > "$control"
  if [ -z "$(fence_defs "$control")" ]; then
    echo "check-fence: the Fence check passed a planted Fence that returns its input — the gate is broken, not the tree: $plant" >&2
    exit 1
  fi
done
printf 'package x\nfunc errResult(format string, args ...any) toolResult {\n\treturn toolResult{Text: fmt.Sprintf(format, args...)}\n}\n' > "$control"
if [ -z "$(honest errResult 'Fence\\(fmt\\.Sprintf\\(format, args\\.\\.\\.\\)' "$control")" ]; then
  echo "check-fence: the errResult check passed a planted errResult that does not fence — the gate is broken, not the tree" >&2
  exit 1
fi

files=$(find internal/cli internal/buddylist cmd -name '*.go' ! -name '*_test.go')
module=$(find internal cmd -name '*.go' ! -name '*_test.go')

out=$(honest scopeList 'fence\\.Field\\(' $files)
if [ -n "$out" ]; then
  [ "$out" = none ] && out="no scopeList is defined, and it is counted as a fence call"
  echo "check-fence: scopeList is counted as a fence call, and it no longer calls fence.Field (D-066): $out" >&2
  exit 1
fi
out=$(honest errResult 'Fence\\(fmt\\.Sprintf\\(format, args\\.\\.\\.\\)' $files)
if [ -n "$out" ]; then
  echo "check-fence: errResult is exempt from R3 because it fences what it formats, and it no longer does: $out" >&2
  exit 1
fi
# Every Fence definition in a scanned package is the one-line wrapper, byte for byte.
# shellcheck disable=SC2086
bad=$(fence_defs $files)
# shellcheck disable=SC2086
if [ -n "$bad" ] || ! grep -qE '^func Fence\(s string, max int\) string \{ return fence\.Line\(s, max\) \}$' $files; then
  echo "check-fence: Fence is counted as a fence call, so it must be exactly \`return fence.Line(s, max)\`: ${bad:-no such definition}" >&2
  exit 1
fi

# shellcheck disable=SC2086
hits=$(scan $files)
# shellcheck disable=SC2086
resp=$(respell $module; r3 $files)
if [ -n "$hits$resp" ]; then
  echo "check-fence: peer-controlled text printed without internal/fence (invariant 9):" >&2
  [ -n "$hits" ] && echo "$hits" >&2
  [ -n "$resp" ] && echo "$resp" >&2
  echo "  wrap each value in fence.Line(v, cap) — caps: slug 128, label/room 64, desc/scopes/path 512, body 4096" >&2
  exit 1
fi
echo "check-fence: GREEN (positive controls flagged; $(echo "$files" | wc -l | tr -d ' ') files clean, $(echo "$module" | wc -l | tr -d ' ') checked for respelling)"
