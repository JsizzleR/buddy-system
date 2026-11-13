#!/bin/sh
# check-codex-review.sh — the done-check for scripts/codex-review.sh's verdict: what
# the out-file says and what the script exits with, on every exit path (Codex report
# A2, A6, A8).
#
# WHY A GATE. Every review receipt rests on this one instrument, and until this check
# nothing tested it: a downgraded header exited 0 with a stderr WARNING and an
# unmarked out-file, a "slots busy" refusal left the previous pass's verdict in place
# at the same path (a lane rerunning read it as this one's), `CODEX_BUDGET=abc` or `0`
# meant no watchdog, and a missing out-file directory lost the whole run after it ran.
# Each case below is one of those exits, driven by a STUB `codex` on PATH (a real pass
# costs minutes and money, and its header cannot be made to lie on demand). The stub
# prints codex exec's own layout (version line, a `--------`-fenced header, `user`, the
# echoed prompt, `codex`, the reply, `tokens used`), writes the reply where `-o` says,
# and records each launch, so a refusal can be shown to have run nothing.
#
# Positive controls: the `ok` case (rc 0, header ok, the reply alone in the out-file)
# proves the stub is reachable and the script can pass, so every refusal below is the
# script's verdict and not a broken harness; each stale-file case plants a sentinel
# and the `ok` case shows the same sentinel overwritten by a pass that ran.
#
# Hermetic: a temp dir, a private PATH head, no network, no real codex. The busy case
# needs a process the slot detector counts: it starts the stub as `codex exec busy`
# (its argv carries the pattern) with CODEX_SLOTS=1, so a real codex run elsewhere on
# the machine only adds to a count that is already at the cap; every other case sets
# CODEX_SLOTS high enough that a peer's runs cannot make it NOT RUN.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# lib.sh unsets git's local environment (D-057): with a GIT_DIR inherited from a
# hook, the pass record below would land in the real repository's common dir.
. "$ROOT/scripts/lib.sh"
SCRIPT=$ROOT/scripts/codex-review.sh
T=$(mktemp -d "${TMPDIR:-/tmp}/check-codex-review.XXXXXX")
BUSY=
trap '[ -z "$BUSY" ] || kill "$BUSY" 2>/dev/null || true; rm -rf "$T"' EXIT INT TERM
mkdir -p "$T/bin" "$T/out"
# Every case runs from a temp git repository, so the pass record (A3) lands in ITS
# common dir, never this checkout's.
mkrepo "$T/repo"
REC=$T/repo/.git/codex-passes.tsv

cat > "$T/bin/codex" <<'STUB'
#!/bin/sh
# Stub codex: STUB_MODE picks the run; every launch is logged to $STUB_LOG.
[ "${1:-}" = exec ] || exit 64
[ "${2:-}" = busy ] && { sleep 60; exit 0; }
echo launched >> "$STUB_LOG"
echo $$ > "$STUB_LOG.pid"
# Like real codex, the header echoes what argv asked for (-m, -c model_reasoning_effort),
# and a default when it asked for nothing, so dropping a pin is a mismatch here too.
reply= prompt= prev= model=stub-default-model effort=stub-default-effort
for a; do
  [ "$prev" = -o ] && reply=$a
  [ "$prev" = -m ] && model=$a
  case $prev$a in -cmodel_reasoning_effort=*) effort=${a#model_reasoning_effort=} ;; esac
  prev=$a prompt=$a
done
printf '%s' "$prompt" > "$STUB_LOG.prompt"
rc=0 sleep=0 write=1 nomodel=0
case ${STUB_MODE:-ok} in
  ok) ;;
  nomodel) nomodel=1 ;;
  downgrade) model=gpt-5-mini ;;
  prefix) model=gpt-6-astra-mini ;;
  effort) effort=high ;;
  slow) sleep=10 ;;
  noreply) write=0 ;;
  fail) rc=1 write=0 ;;
esac
echo "OpenAI Codex v0.166.0"
echo "--------"
echo "workdir: $PWD"
[ "$nomodel" -eq 1 ] || echo "model: $model"
echo "provider: openai"
echo "sandbox: read-only"
echo "reasoning effort: $effort"
echo "session id: ${STUB_SESSION:-00000000-0000-0000-0000-000000000001}"
echo "--------"
echo "user"
printf '%s\n' "$prompt"
[ "$sleep" -eq 0 ] || sleep "$sleep"
echo "exec"
echo "/bin/zsh -lc 'cat CLAUDE.md' succeeded"
echo "TRANSCRIPT-ONLY-LINE"
echo "codex"
echo "REPLY: one finding. model: gpt-6-astra"
echo "tokens used"
echo "12,345"
[ "$write" -eq 0 ] || printf 'REPLY: one finding. model: gpt-6-astra\n' > "$reply"
echo "REPLY: one finding. model: gpt-6-astra"
exit $rc
STUB
chmod +x "$T/bin/codex"

printf 'Review this. Do not explore.\n' > "$T/prompt"
# A prompt that quotes a whole pinned header: the old unanchored grep took it as the
# run's header.
printf 'Quoted header:\nmodel: gpt-6-astra\nreasoning effort: xhigh\n' > "$T/quote"

# The caller's own CODEX_* knobs must not reach the cases: CODEX_EFFORT=high would make
# the positive control fail on correct code, CODEX_WAIT would change the busy timing.
CLEAN="-u CODEX_MODEL -u CODEX_EFFORT -u CODEX_TIER -u CODEX_BUDGET -u CODEX_SLOTS -u CODEX_WAIT -u CODEX_CHARTER -u CODEX_NO_CHARTER -u CODEX_DIFF"
fails=0
fail() { echo "check-codex-review: FAIL $*" >&2; fails=$((fails + 1)); }

# run <name> <want-rc> <out-file> [VAR=value ...] -- runs the script on $T/prompt
# (or $PROMPT, set on its own line: an assignment prefixed to a function call
# persists after it in sh), recording its rc and the stub's launch count.
run() {
  _n=$1 _want=$2 _out=$3; shift 3
  : > "$T/log"
  set +e
  ( cd "${RUNDIR:-$T/repo}" && env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 "$@" \
    sh "$SCRIPT" "${PROMPT:-$T/prompt}" "$_out" > "$T/stdout.$_n" 2> "$T/stderr.$_n" )
  _rc=$?
  set -e
  launched=$(grep -c . "$T/log" || true)
  [ "$_rc" -eq "$_want" ] || fail "$_n: exit $_rc, want $_want (stderr: $(head -3 "$T/stderr.$_n" | tr '\n' ' '))"
}
first() { head -1 "$1"; }
rows() { if [ -f "$REC" ]; then grep -c . "$REC"; else echo 0; fi; }
# lastrow <field-number>... — those fields of the record's last row, space-joined.
lastrow() { tail -1 "$REC" | awk -F '\t' -v want="$*" 'BEGIN { n = split(want, w, " ") } { for (i = 1; i <= n; i++) printf "%s%s", $w[i], (i < n ? " " : "\n") }'; }
has() { grep -qF -- "$2" "$1"; }
stale() { echo "SENTINEL-7f3a old verdict from an earlier pass" > "$1"; echo "SENTINEL-7f3a old transcript" > "$1.transcript"; }
# charter_of <parts-word> — the charter those parts make, unfolded, in the script's order
# (adapter, install, testinfra, process; D-128, D-147, D-158).
charter_of() {
  cat "$ROOT/docs/review-charter.md"
  for _pt in adapter install testinfra process; do
    case +$1+ in *+$_pt+*) printf '\n\n'; cat "$ROOT/docs/review-charter-$_pt.md" ;; esac
  done
}
# prefix_is <name> <parts-word> — the prompt begins with EXACTLY that charter, folded, then
# the reviewer line: presence and heading order alone would pass a part sent twice or cut
# short (Codex code pass, D-147).
prefix_is() {
  { charter_of "$2"; printf '\n\nYou are a constrained review pass, and the charter above'; } |
    python3 -I -B "$ROOT/scripts/ascii-fold.py" > "$T/want.prompt"
  head -c "$(wc -c < "$T/want.prompt")" "$T/log.prompt" | cmp -s - "$T/want.prompt" ||
    fail "$1: the prompt does not begin with exactly the charter $2, its parts in order, then the reviewer line"
}

# --- ok: the positive control, with the real charter prepended ---
O=$T/out/ok.txt; stale "$O"
run ok 0 "$O" CODEX_NO_CHARTER=0
[ "$launched" -eq 1 ] || fail "ok: stub launched $launched times, want 1"
[ "$(first "$O")" = "REPLY: one finding. model: gpt-6-astra" ] || fail "ok: first line is '$(first "$O")', want the reply"
has "$O" SENTINEL-7f3a && fail "ok: the stale verdict survived a pass that ran"
has "$O.transcript" SENTINEL-7f3a && fail "ok: the stale transcript survived a pass that ran"
has "$O" TRANSCRIPT-ONLY-LINE && fail "ok: the transcript leaked into the out-file"
has "$O" "# Review charter" && fail "ok: the echoed charter is in the out-file"
has "$O.transcript" TRANSCRIPT-ONLY-LINE || fail "ok: the transcript is not beside the out-file"
has "$O.transcript" "# Review charter" || fail "ok: the charter did not reach codex (control for the line above)"
# A1: the fixed reviewer line rides after the charter and before the review prompt.
awk '/^# Review charter/ { c = NR } /^You are a constrained review pass, and the charter above/ { l = NR } /^Review this. Do not explore./ { p = NR } END { exit !(c && l && p && c < l && l < p) }' "$T/log.prompt" || fail "ok: the reviewer line is not between the charter and the prompt"
st=$(tail -1 "$O")
case $st in "codex-review: status rc=0 exit=0 "*) ;; *) fail "ok: last line is '$st', want the status line" ;; esac
for k in "header=ok" "model=gpt-6-astra" "effort=xhigh" "tokens=12345" "B(core+install+testinfra)" "prompt=" "elapsed="; do
  case $st in *"$k"*) ;; *) fail "ok: status line lacks '$k': $st" ;; esac
done
[ "$(tail -1 "$O.transcript")" = "$st" ] || fail "ok: the transcript does not end with the status line"
cmp -s "$O" "$T/stdout.ok" || fail "ok: stdout is not the out-file's content"
# A3: one row, counts only. Fields: 1 time 2 rc 3 exit 4 elapsed 5 model 6 effort
# 7 header 8 charter 9 charter_bytes 10 prompt_bytes 11 tokens 12 commands
# 13 charter_reads 14 session. The stub ran one command, `cat CLAUDE.md`.
[ "$(rows)" = 1 ] || fail "ok: the record has $(rows) rows, want 1"
# No knob: the code-area parts ride (D-158).
cb=$(charter_of core+install+testinfra | wc -c | tr -d ' ')
[ "$(lastrow 2 3 5 6 7 8 9 11 12 13 14)" = "0 0 gpt-6-astra xhigh ok core+install+testinfra $cb 12345 1 1 00000000-0000-0000-0000-000000000001" ] ||
  fail "ok: record row is '$(lastrow 2 3 5 6 7 8 9 11 12 13 14)'"
[ "$(awk -F '\t' '{ print NF }' "$REC")" = 14 ] || fail "ok: the record row does not have 14 fields"
tail -1 "$REC" | awk -F '\t' '$1 ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$/ && $4 ~ /^[0-9]+$/ && $10 ~ /^[0-9]+$/ { ok = 1 } END { exit !ok }' ||
  fail "ok: the record row's time, elapsed or prompt bytes are malformed: $(tail -1 "$REC")"
grep -q -e REPLY -e "Review this" -e "Review charter" "$REC" && fail "ok: review or prompt text reached the pass record"
# A7: the caller's half of the foreground rule is printed beside the budget line, and
# the tier is said to be requested, not pinned ("stop doing" 4).
grep -q '^codex-review: model=.* tier=default(requested) budget=' "$T/stderr.ok" || fail "ok: the budget line does not say the tier is requested"
awk '/^codex-review: model=.* budget=/ { b = NR } /^codex-review: the caller runs this in the FOREGROUND with a 600000 ms tool timeout/ { r = NR } END { exit !(b && r == b + 1) }' "$T/stderr.ok" ||
  fail "ok: the foreground requirement is not on the line after the budget line"

# --- no out-file: stdout is the transcript, then the status line ---
: > "$T/log"
( cd "$T/repo" && env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 sh "$SCRIPT" "$T/prompt" > "$T/stdout.none" 2>/dev/null ) || fail "none: exit $?"
has "$T/stdout.none" TRANSCRIPT-ONLY-LINE || fail "none: stdout lacks the transcript"
case $(tail -1 "$T/stdout.none") in "codex-review: status rc=0 "*) ;; *) fail "none: stdout does not end with the status line" ;; esac

# --- header mismatches exit 3 and say so first ---
for m in downgrade prefix effort; do
  O=$T/out/$m.txt; stale "$O"
  run "$m" 3 "$O" STUB_MODE=$m
  case $(first "$O") in "codex-review: HEADER MISMATCH"*) ;; *) fail "$m: first line '$(first "$O")', want HEADER MISMATCH" ;; esac
  has "$O" "header=MISMATCH" || fail "$m: status line lacks header=MISMATCH"
  has "$O" "status rc=0 exit=3 " || fail "$m: status line does not show codex rc 0 and exit 3"
  has "$O" SENTINEL-7f3a && fail "$m: stale verdict survived"
done
# The anchor: a prompt and a reply that both quote the pinned header do not rescue a
# downgraded run (the old grep over the whole output passed it).
O=$T/out/quote.txt
PROMPT=$T/quote; run quote 3 "$O" STUB_MODE=downgrade; PROMPT=
has "$O.transcript" "reasoning effort: xhigh" || fail "quote: the quoted header never reached the transcript (the case tests nothing)"
case $(first "$O") in "codex-review: HEADER MISMATCH"*) ;; *) fail "quote: a quoted header passed the check" ;; esac
# A header with no model line at all, and the pinned one quoted on its own line in the
# body: a line-anchored match over the whole output takes the body's line.
O=$T/out/nomodel.txt
PROMPT=$T/quote; run nomodel 3 "$O" STUB_MODE=nomodel; PROMPT=
case $(first "$O") in "codex-review: HEADER MISMATCH"*) ;; *) fail "nomodel: a body line stood in for the header's missing model" ;; esac

# --- the fold: the prompt codex receives is ASCII, fence.Field's characters spelled ---
printf 'fence.Field renders a space as \342\220\243 and an empty result as \342\210\205 \342\200\224 ok\n' > "$T/utf8"
O=$T/out/fold.txt
PROMPT=$T/utf8; run fold 0 "$O"; PROMPT=
# Read the prompt the stub was handed, not the transcript: the transcript's header
# carries the working directory, which may itself be non-ASCII.
has "$T/log.prompt" "renders a space as <SP> and an empty result as <EMPTY> -- ok" || fail "fold: the prompt did not reach codex folded (<SP>, <EMPTY>, --)"
LC_ALL=C grep -q "$(printf '[\200-\377]')" "$T/log.prompt" && fail "fold: a non-ASCII byte reached codex"

# --- busy: its own exit code, NOT RUN, nothing launched ---
O=$T/out/busy.txt; stale "$O"
PATH="$T/bin:$PATH" STUB_LOG=/dev/null codex exec busy &
BUSY=$!
i=0; until ps -eo command | grep -q "[c]odex exec busy"; do i=$((i + 1)); [ $i -lt 50 ] || break; sleep 0.1; done
run busy 75 "$O" CODEX_SLOTS=1
kill "$BUSY" 2>/dev/null || true; wait "$BUSY" 2>/dev/null || true; BUSY=
case $(first "$O") in "codex-review: NOT RUN"*) ;; *) fail "busy: first line '$(first "$O")', want NOT RUN" ;; esac
has "$O" SENTINEL-7f3a && fail "busy: the stale verdict survived a busy refusal"
has "$O.transcript" SENTINEL-7f3a && fail "busy: the stale transcript survived a busy refusal"
[ "$launched" -eq 0 ] || fail "busy: stub launched $launched times, want 0"
[ "$(lastrow 2 3 7 14)" = "- 75 - -" ] || fail "busy: the record's last row is '$(lastrow 2 3 7 14)', want a NOT RUN row"

# --- refusals before the run: exit 1, NO VERDICT, nothing launched ---
nrows=$(rows)
for kv in CODEX_BUDGET=abc CODEX_BUDGET=0 CODEX_BUDGET=9m CODEX_BUDGET=4294967296 CODEX_BUDGET=0600 CODEX_SLOTS=x CODEX_SLOTS=0 CODEX_SLOTS=9223372036854775808 CODEX_WAIT=1m CODEX_WAIT=3601 CODEX_CHARTER=bogus CODEX_CHARTER= CODEX_CHARTER=adapter+process CODEX_CHARTER=process CODEX_CHARTER=all; do
  n=refuse-$(echo "$kv" | tr '=' '-')
  O=$T/out/$n.txt; stale "$O"
  run "$n" 1 "$O" "$kv"
  case $(first "$O") in "codex-review: NO VERDICT -- refused"*) ;; *) fail "$n: first line '$(first "$O")', want NO VERDICT -- refused" ;; esac
  has "$O" SENTINEL-7f3a && fail "$n: stale verdict survived"
  has "$O" "refused: " || fail "$n: out-file does not name the refusal"
  [ "$launched" -eq 0 ] || fail "$n: stub launched $launched times, want 0"
done
[ "$(rows)" = "$nrows" ] || fail "refusals: a refused request wrote a pass record row"
# Positive control for the knob refusals: valid values run.
run knobs-ok 0 "$T/out/knobs.txt" CODEX_BUDGET=86400 CODEX_SLOTS=1000 CODEX_WAIT=0
[ "$launched" -eq 1 ] || fail "knobs-ok: stub launched $launched times, want 1"
# No charter, no reviewer line: a pass without the charter may need to open it.
has "$T/log.prompt" "You are a constrained review pass" && fail "knobs-ok: the reviewer line rode a CODEX_NO_CHARTER=1 pass"

# --- the charter's parts (D-128, D-147): each value sends exactly its files, in order,
# and says so in the status line and the record. In the loop above, `process` and `all`
# refused beside run()'s CODEX_NO_CHARTER=1; here they run, the positive control. ---
for v in core:core+install+testinfra adapter:core+adapter+install+testinfra process:core+install+testinfra+process all:core+adapter+install+testinfra+process; do
  val=${v%%:*} parts=${v#*:}
  O=$T/out/parts-$val.txt; stale "$O"
  run "parts-$val" 0 "$O" CODEX_NO_CHARTER=0 CODEX_CHARTER=$val
  has "$O" "B($parts) " || fail "parts-$val: status line lacks B($parts): $(tail -1 "$O")"
  [ "$(lastrow 8)" = "$parts" ] || fail "parts-$val: record says charter '$(lastrow 8)', want $parts"
  a=0; p=0
  has "$T/log.prompt" "# Review charter: adapter GIVENs (full text)" && a=1
  has "$T/log.prompt" "# Review charter: process GIVENs (full text)" && p=1
  case $parts in *adapter*) [ $a = 1 ] || fail "parts-$val: the adapter file did not reach codex" ;; *) [ $a = 0 ] || fail "parts-$val: the adapter file reached codex unasked" ;; esac
  case $parts in *process) [ $p = 1 ] || fail "parts-$val: the process file did not reach codex" ;; *) [ $p = 0 ] || fail "parts-$val: the process file reached codex unasked" ;; esac
  prefix_is "parts-$val" "$parts"
done

# --- the code-area parts (D-158): CODEX_DIFF drops a part only when git names the diff's
# paths and none is in its area; everything else sends the part or refuses. Each case is
# a commit (or a working-tree change) in its own repository, so the paths are exactly
# known. The positive controls: one path in each area keeps that part (and the case with
# neither drops both, proving a dropped part is reachable at all). ---
D=$T/drepo; mkrepo "$D"
mkdir -p "$D/scripts/hooks" "$D/internal/store" "$D/internal/testguard" "$D/internal/cli" "$D/sub" "$D/docs"
for f in scripts/install.sh internal/store/x.go internal/testguard/w.go internal/cli/cli_test.go sub/n.txt README.md CLAUDE.md docs/USAGE.md; do echo v1 > "$D/$f"; done
printf '1. **A core GIVEN.**\n\n53. **An install stub.** (full text: part `install`)\n\n55. **A testinfra stub.** (full text: part `testinfra`)\n' > "$D/docs/review-charter.md"
git -C "$D" add -A && git -C "$D" commit -qm base
dcommit() { for f; do echo "v-$f-$(date +%s%N)" >> "$D/$f"; done; git -C "$D" add -A; git -C "$D" commit -qm "$*"; }
# diffcase <name> <want-parts> <CODEX_DIFF> [VAR=value ...]
diffcase() {
  _dn=$1 _dp=$2 _dd=$3; shift 3
  O=$T/out/diff-$_dn.txt; stale "$O"
  RUNDIR=${DRUN:-$D}; run "diff-$_dn" 0 "$O" CODEX_NO_CHARTER=0 "CODEX_DIFF=$_dd" "$@"; RUNDIR=
  has "$O" "B($_dp) " || fail "diff-$_dn: status line lacks B($_dp): $(tail -1 "$O")"
  prefix_is "diff-$_dn" "$_dp"
  has "$T/stderr.diff-$_dn" "codex-review: CODEX_DIFF=$_dd:" || fail "diff-$_dn: no stderr line naming the pick"
}
dcommit internal/store/x.go
diffcase none core HEAD~1..HEAD
diffcase none-adapter core+adapter HEAD~1..HEAD CODEX_CHARTER=adapter
dcommit scripts/install.sh
diffcase install core+install HEAD~1..HEAD
dcommit internal/testguard/w.go
diffcase testinfra core+testinfra HEAD~1..HEAD
diffcase both-range core+install+testinfra HEAD~3..HEAD CODEX_CHARTER=core
# The prose that restates these GIVENs is not in an area (D-158: it rests on the stubs).
dcommit README.md CLAUDE.md docs/USAGE.md
diffcase prose core HEAD~1..HEAD
# ...unless a changed line names the part's subject (Codex code pass's counterexamples).
echo 'Installation fails when the optional hook is missing.' >> "$D/README.md"; git -C "$D" commit -qam prose-install
diffcase prose-install core+install HEAD~1..HEAD
echo "The watchdog's hard deadline is the test timeout." >> "$D/CLAUDE.md"; git -C "$D" commit -qam prose-testinfra
diffcase prose-testinfra core+testinfra HEAD~1..HEAD
# A touched prose file with no changed line (a mode change) cannot say which part it
# touched, so both ride (mutation: dropping that arm sent neither).
chmod +x "$D/README.md"; git -C "$D" commit -qam prose-mode
diffcase prose-mode core+install+testinfra HEAD~1..HEAD
# A changed charter line carrying a part's mark is that stub edited: the part rides. A
# charter change elsewhere does not bring one (the control).
printf '\n2. **A new core GIVEN.**\n' >> "$D/docs/review-charter.md"; git -C "$D" commit -qam charter-core
diffcase charter-core core HEAD~1..HEAD
sed 's/An install stub/An install stub, edited/' "$D/docs/review-charter.md" > "$D/rc.tmp" && mv "$D/rc.tmp" "$D/docs/review-charter.md"; git -C "$D" commit -qam charter-install
diffcase charter-install core+install HEAD~1..HEAD
grep -v 'testinfra stub' "$D/docs/review-charter.md" > "$D/rc.tmp" && mv "$D/rc.tmp" "$D/docs/review-charter.md"; git -C "$D" commit -qam charter-testinfra-gone
diffcase charter-removed core+testinfra HEAD~1..HEAD
# The stub-line read survives a subdirectory cwd, forced color and a `-diff` attribute,
# each of which hid the edited stub (Codex code pass).
stubedit() { sed "s/An install stub[^.]*/An install stub, edited $1/" "$D/docs/review-charter.md" > "$D/rc.tmp" && mv "$D/rc.tmp" "$D/docs/review-charter.md"; git -C "$D" commit -qam "stub $1"; }
stubedit sub; DRUN=$D/sub; diffcase charter-subdir core+install HEAD~1..HEAD; DRUN=
git -C "$D" config color.diff always; git -C "$D" config color.ui always
stubedit color; diffcase charter-color core+install HEAD~1..HEAD
git -C "$D" config --unset color.diff; git -C "$D" config --unset color.ui
printf 'docs/review-charter.md -diff\n' > "$D/.gitattributes"; git -C "$D" add .gitattributes; git -C "$D" commit -qm attr
stubedit binary; diffcase charter-binary core+install HEAD~1..HEAD
git -C "$D" rm -q .gitattributes; git -C "$D" commit -qm unattr
# A submodule at an area's root: git names the gitlink by its bare path (Codex code pass).
# A gitlink written straight into the index, so no clone and no network.
E=$T/erepo; mkrepo "$E"; echo a > "$E/f"; git -C "$E" add f; git -C "$E" commit -qm one
git -C "$E" update-index --add --cacheinfo "160000,$(git -C "$E" rev-parse HEAD),internal/testguard"; git -C "$E" commit -qm link
# (`commit -a` would drop the gitlink: it has no directory in the working tree.)
echo b > "$E/f"; git -C "$E" add f; git -C "$E" commit -qm two
git -C "$E" update-index --cacheinfo "160000,$(git -C "$E" rev-parse HEAD),internal/testguard"; git -C "$E" commit -qm bump
DRUN=$E; diffcase gitlink core+testinfra HEAD~1..HEAD; DRUN=
# Every area path, one commit each: the list is the spec, so dropping a path from the
# script's area is a red here (Codex code pass: deleting update.go went unseen).
for ap in install:scripts/install.sh install:scripts/uninstall.sh install:scripts/install-lock.sh install:scripts/wire-hooks.py \
  install:scripts/gate-hook.py install:scripts/install-skill.sh install:scripts/skill-dest.sh install:scripts/setup-clone.sh install:scripts/codex-gate-row.sh \
  install:scripts/check-install.sh install:scripts/check-uninstall.sh install:scripts/check-gate-line.sh \
  install:scripts/check-setup-clone.sh install:scripts/hooks/agent-model-gate.py install:internal/cli/update.go \
  install:internal/cli/update_test.go install:internal/cli/uninstall.go install:internal/cli/uninstall_test.go \
  install:internal/cli/browser_test.go install:docs/review-charter-install.md \
  testinfra:internal/testguard/testguard.go testinfra:internal/testguard/sub/x.go testinfra:internal/cli/cli_test.go \
  testinfra:docs/review-charter-testinfra.md; do
  mkdir -p "$D/$(dirname "${ap#*:}")"; dcommit "${ap#*:}"
  diffcase "area-$(printf '%s' "${ap#*:}" | tr -c 'A-Za-z0-9.' '_')" "core+${ap%%:*}" HEAD~1..HEAD
done
# A rename's OLD end is a path in the area: --no-renames names both ends.
git -C "$D" mv scripts/install.sh sub/renamed.sh && git -C "$D" commit -qm rename
diffcase rename core+install HEAD~1..HEAD
# A single rev is that rev against the working tree: an uncommitted change counts, and so
# does an untracked new file (a new file in an area must not hide).
echo dirty >> "$D/internal/cli/cli_test.go"
diffcase worktree core+testinfra HEAD
git -C "$D" checkout -q -- internal/cli/cli_test.go
echo new > "$D/scripts/hooks/new.py"
echo dirty >> "$D/sub/n.txt"
diffcase untracked core+install HEAD
rm "$D/scripts/hooks/new.py"
diffcase tracked-only core HEAD
git -C "$D" checkout -q -- sub/n.txt
# Paths are the top's, whatever the cwd and the repository's diff.relative: run from sub/
# with diff.relative=true, a change outside sub/ is still seen.
git -C "$D" config diff.relative true
dcommit internal/testguard/w.go
DRUN=$D/sub; diffcase relative core+testinfra HEAD~1..HEAD; DRUN=
git -C "$D" config --unset diff.relative
# Refusals: before any run, nothing launched, the out-file names the refusal, and a
# leading-dash value writes no file (git would take --output=<file> as an option).
for kv in CODEX_DIFF= CODEX_DIFF=--output=$T/injected CODEX_DIFF=no-such-rev CODEX_DIFF=HEAD..HEAD; do
  n=refuse-diff-$(printf '%s' "${kv#CODEX_DIFF=}" | tr -c 'A-Za-z0-9.' '_')
  O=$T/out/$n.txt; stale "$O"
  RUNDIR=$D; run "$n" 1 "$O" CODEX_NO_CHARTER=0 "$kv"; RUNDIR=
  has "$O" "refused: " || fail "$n: out-file does not name the refusal"
  has "$O" "refused: CODEX_DIFF with CODEX_NO_CHARTER" && fail "$n: refused for the wrong reason"
  [ "$launched" -eq 0 ] || fail "$n: stub launched $launched times, want 0"
done
[ ! -e "$T/injected" ] || fail "refuse-diff: a leading-dash CODEX_DIFF reached git as an option and wrote a file"
O=$T/out/refuse-diff-nocharter.txt; stale "$O"
RUNDIR=$D; run refuse-diff-nocharter 1 "$O" CODEX_DIFF=HEAD~1..HEAD; RUNDIR=
has "$O" "refused: CODEX_DIFF with CODEX_NO_CHARTER=1" || fail "refuse-diff-nocharter: out-file does not name the refusal"
mkdir -p "$T/nogit2"
O=$T/out/refuse-diff-nogit.txt; stale "$O"
RUNDIR=$T/nogit2; run refuse-diff-nogit 1 "$O" CODEX_NO_CHARTER=0 CODEX_DIFF=HEAD; RUNDIR=
has "$O" "refused: CODEX_DIFF outside a git checkout" || fail "refuse-diff-nogit: out-file does not name the refusal"

# A missing prompt, after the out-file is armed.
O=$T/out/noprompt.txt; stale "$O"
PROMPT=$T/absent; run noprompt 1 "$O"; PROMPT=
case $(first "$O") in "codex-review: NO VERDICT -- refused"*) ;; *) fail "noprompt: first line '$(first "$O")'" ;; esac

# --- a missing out-file directory refuses BEFORE the run ---
run nodir 1 "$T/out/missing/dir/v.txt"
[ "$launched" -eq 0 ] || fail "nodir: stub launched $launched times, want 0 (the run would be lost)"
[ ! -e "$T/out/missing" ] || fail "nodir: the directory was created"
has "$T/stderr.nodir" "refused: missing out-file directory" || fail "nodir: stderr does not name the refusal"

# --- the watchdog: 124, NO VERDICT, the partial transcript kept ---
O=$T/out/slow.txt; stale "$O"
run slow 124 "$O" STUB_MODE=slow CODEX_BUDGET=1
case $(first "$O") in "codex-review: NO VERDICT -- the 1s watchdog fired"*) ;; *) fail "slow: first line '$(first "$O")'" ;; esac
has "$O.transcript" "OpenAI Codex" || fail "slow: the partial transcript was not kept"
has "$O" "codex-review: status rc=124" || fail "slow: no status line"

# --- the arming itself: a run killed mid-pass leaves NO VERDICT, not the old file ---
# Every other case overwrites the out-file at its end, so only a run that never reaches
# its end shows the line written before it ran.
O=$T/out/killed.txt; stale "$O"; : > "$T/log"
( cd "$T/repo" && exec env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" STUB_MODE=slow CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 CODEX_BUDGET=60 \
  sh "$SCRIPT" "$T/prompt" "$O" > /dev/null 2>&1 ) &
K=$!
i=0; until grep -q launched "$T/log" 2>/dev/null; do i=$((i + 1)); [ $i -lt 100 ] || break; sleep 0.1; done
kill -9 "$K" 2>/dev/null || true; wait "$K" 2>/dev/null || true
[ -s "$T/log.pid" ] && kill "$(cat "$T/log.pid")" 2>/dev/null || true
case $(first "$O") in "codex-review: NO VERDICT -- this pass has not finished"*) ;; *) fail "killed: first line '$(first "$O")', want the armed NO VERDICT" ;; esac
case $(first "$O.transcript") in "codex-review: NO VERDICT -- this pass has not finished"*) ;; *) fail "killed: transcript not armed" ;; esac
grep -q launched "$T/log" || fail "killed: the stub never launched (the case tests nothing)"

# --- an out-file writable in a directory that is not: the result is still published ---
mkdir "$T/ro"; O=$T/ro/v.txt; stale "$O"; chmod a-w "$T/ro"
run rodir 0 "$O"
[ "$(first "$O")" = "REPLY: one finding. model: gpt-6-astra" ] || fail "rodir: first line '$(first "$O")', want the reply"
chmod u+w "$T/ro"

# --- the record holds no value that could break a row: a model name with a tab and a
# session id with spaces become `other`, and the row keeps 14 fields ---
run hostile 0 "$T/out/hostile.txt" "CODEX_MODEL=gpt	CANARYzq7x" "CODEX_EFFORT=hi	CANARYzq7x" "STUB_SESSION=a b CANARYzq7x"
[ "$(lastrow 5 6 14)" = "other other other" ] || fail "hostile: model/effort/session fields are '$(lastrow 5 6 14)', want 'other other other'"
# `-n` passes the character test and must still be printed (echo would print nothing).
run dashn 0 "$T/out/dashn.txt" CODEX_MODEL=-n STUB_SESSION=-n
[ "$(lastrow 5 14)" = "-n -n" ] || fail "dashn: model/session fields are '$(lastrow 5 14)', want '-n -n'"
[ "$(tail -1 "$REC" | awk -F '\t' '{ print NF }')" = 14 ] || fail "hostile: the row does not have 14 fields"
grep -q CANARYzq7x "$REC" && fail "hostile: a planted value reached the pass record"
# --- not in a git checkout: no row, a stderr note, the verdict unchanged ---
mkdir "$T/nogit"; n0=$(rows)
RUNDIR=$T/nogit; run nogit 0 "$T/out/nogit.txt"; RUNDIR=
[ "$(rows)" = "$n0" ] || fail "nogit: a row was written from outside a checkout"
has "$T/stderr.nogit" "no pass record (not in a git checkout)" || fail "nogit: no stderr note"
[ "$(first "$T/out/nogit.txt")" = "REPLY: one finding. model: gpt-6-astra" ] || fail "nogit: the verdict was not published"
# --- a record that cannot be written: a stderr note, the verdict and exit unchanged ---
mv "$REC" "$T/rec.saved"; mkdir "$REC"
run recfail 0 "$T/out/recfail.txt"
has "$T/stderr.recfail" "the pass record could not be written" || fail "recfail: no stderr note"
[ "$(first "$T/out/recfail.txt")" = "REPLY: one finding. model: gpt-6-astra" ] || fail "recfail: the verdict was not published"
rmdir "$REC"; mv "$T/rec.saved" "$REC"
# --- a killed run's tokens are not read: a prompt carrying a footer of its own ---
printf 'tokens used\n999999\n' > "$T/fakefoot"
PROMPT=$T/fakefoot; run fakefoot 124 "$T/out/fakefoot.txt" STUB_MODE=slow CODEX_BUDGET=1; PROMPT=
[ "$(lastrow 2 11)" = "124 -" ] || fail "fakefoot: a killed run recorded tokens '$(lastrow 11)' from its prompt"

# --- codex ran but gave no reply: exit 3; codex failed: its own code ---
O=$T/out/noreply.txt; stale "$O"
run noreply 3 "$O" STUB_MODE=noreply
case $(first "$O") in "codex-review: NO VERDICT -- codex exited 0 but wrote no reply"*) ;; *) fail "noreply: first line '$(first "$O")'" ;; esac
O=$T/out/fail.txt; stale "$O"
run fail 1 "$O" STUB_MODE=fail
case $(first "$O") in "codex-review: NO VERDICT -- codex exited 1"*) ;; *) fail "fail: first line '$(first "$O")'" ;; esac
[ "$launched" -eq 1 ] || fail "fail: stub launched $launched times, want 1 (a refusal would also exit 1)"

if [ "$fails" -ne 0 ]; then
  echo "check-codex-review: RED ($fails failure(s))" >&2
  exit 1
fi
echo "check-codex-review: GREEN (ok + reviewer line + pass record, no-out, 3 header mismatches + quoted header + no model line, fold, busy 75, 15 knob refusals + control, 4 charter parts, 21 CODEX_DIFF picks + 23 area paths + 6 refusals, killed mid-run, read-only dir, missing prompt, missing dir, watchdog 124, no reply 3, codex failure, hostile record values, -n, no git, record write failure, prompt footer)"
