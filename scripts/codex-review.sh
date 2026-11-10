#!/bin/sh
# codex-review.sh — the ONE way to invoke Codex for a review pass. Use this, never a
# hand-rolled `codex exec`.
#
# WHY THIS IS A SCRIPT AND NOT A WRITTEN-DOWN RECIPE. A correct invocation has six
# independent requirements. Getting five right and one wrong costs hours, and the one
# that gets missed is always the one that lives in prose somebody has to remember to
# read. Writing it down a third time does not help; making it code means remembering
# one thing instead of six.
#
# Usage:
#   scripts/codex-review.sh <prompt-file> [out-file]
#   scripts/codex-review.sh - <<'EOF' ... EOF        # prompt on stdin
#
# THE OUT-FILE IS THE VERDICT, and it can say only what this pass did (Codex report
# A2, A6, A8). Before anything can refuse, it is overwritten with a `NO VERDICT` line,
# so a file left by an earlier pass at the same path can never be read as this one's:
# a lane that reran after "slots busy" used to read the previous verdict. It ends as
# exactly one of:
#   - the reply alone (codex's `-o` last message), then one `codex-review: status`
#     line: rc, elapsed, model, effort, header ok|MISMATCH, charter and prompt bytes,
#     codex's token count. The full transcript (header, echoed prompt, every command)
#     is beside it as <out-file>.transcript: it was 99% of an out-file (median 435 KB
#     against a 2.8 KB reply), and lanes read it back into their context;
#   - `NO VERDICT` and why (a refusal before the run, the watchdog, no reply), then the
#     status line when codex ran;
#   - `NOT RUN` when every slot was busy.
# Exit codes: codex's own, or 1 a refusal before the run (usage, a knob, the charter,
# the prompt, an out-file that cannot be written), 75 all slots busy (EX_TEMPFAIL: come
# back later, nothing ran), 124 the watchdog fired, 3 codex exited 0 but the result is
# not a pass at the pin (a header that does not name the pinned model and effort, or no
# reply). A downgrade used to exit 0 with a stderr WARNING and an unmarked out-file.
# Without an out-file, stdout is the whole transcript then the status line; with one,
# stdout is what the out-file holds.
#
# What it enforces, each with the reason it is not optional:
#
#  1. FOREGROUND ONLY. `codex exec` WEDGES when it is backgrounded by an agent harness:
#     elapsed time climbs, CPU stays near zero, and nothing is ever emitted. A live run
#     and a wedged one look IDENTICAL from the output file, because codex renders stdout
#     only at the end — which is why a wedge keeps getting mistaken for "still thinking".
#     If you need a longer budget, cut the prompt's SCOPE (see 6). Never background it.
#  2. `< /dev/null`. Without it codex can hang reading stdin.
#  3. MODEL, EFFORT AND TIER PINNED, and the run header VERIFIED rather than eyeballed.
#     A silent downgrade turns a cross-model review into a same-family one — that is not
#     a weaker gate, it is no gate at all, and it fails silently.
#  4. ASCII-FOLDED prompt. macOS pgrep/pkill abort with "illegal byte sequence" on a
#     UTF-8 pattern, so a prompt containing an em dash cannot be killed by `pkill -f` —
#     you believe you cleaned up and you did not.
#  5. NO CONCURRENT RUNS above a cap. Stacking codex processes is how one wedged run
#     becomes four, and how "codex is too slow" gets recorded as an environment fact.
#  6. A WATCHDOG, and a DIAGNOSIS when it fires — including whether the run was wedged
#     (CPU ~0) or genuinely working, so the next person does not re-derive it.
#  7. A STANDING CHARTER: docs/review-charter.md carries this repo's settled decisions
#     and environment facts, prepended to every prompt so the reviewer neither
#     re-litigates rulings nor explores to re-derive facts. Exploration is the dominant
#     cause of watchdog timeouts. CODEX_NO_CHARTER=1 opts out for a byte-tight pass, but
#     then you must restate the relevant GIVENs in the prompt yourself.
#     CODEX_CHARTER=adapter also appends docs/review-charter-adapter.md, the full text of
#     the GIVENs only adapter code rests on (internal/harness, cmd/buddy-adapter, the
#     qualify/check .py scripts, docs/admissions, USAGE's adapter sections); every other
#     pass gets each of them as a one-line stub naming its rule, D-numbers and fences
#     (D-128). Unset or `core` is the stub-only charter. Any other value REFUSES, set but
#     empty included (an unset shell variable spliced in for `adapter`), and so
#     does `adapter` beside CODEX_NO_CHARTER=1: a caller who asked for more must never
#     silently get less. The run header says which parts went in
#     (`charter=<bytes>B(core)` / `(core+adapter)`), so a receipt can prove it.
#     Rejected: guessing the parts from the prompt text (the script sees a prompt file,
#     not the diff), and a separate file with no stubs (a reviewer not handed it would not
#     know the fences exist).
set -eu

MODEL=${CODEX_MODEL:-gpt-6-astra}
# EFFORT: run xhigh FIRST, always. This is about what a FAILURE COSTS, not about
# capability — both efforts find real defects. A timed-out `max` that was your only pass
# leaves you with no coverage at all; the same timeout after an xhigh pass costs time and
# nothing else. Reach for CODEX_EFFORT=max as a SECOND pass when a missed defect would be
# unrecoverable. The script does not try max and fall back automatically: two passes do
# not fit under one foreground tool cap.
EFFORT=${CODEX_EFFORT:-xhigh}
# TIER is passed EXPLICITLY rather than inherited from ~/.codex/config.toml, because an
# inherited setting is invisible here — the run header echoes model and effort but NOT
# the tier, so a config edit elsewhere would silently change what the gate runs on with
# nothing to notice it by.
#
# DEFAULT, not fast, on this project (operator's call, 2026-09-07): the fast tier buys
# latency, and latency is the one thing a review here does not need — the run is
# foreground, budgeted, and happens once per change. What it must not do is give up
# depth on a codebase whose whole point is the failure nobody noticed. CODEX_TIER=fast
# to trade back.
TIER=${CODEX_TIER:-default}
BUDGET=${CODEX_BUDGET:-540} # under the 600s foreground tool cap; scope the prompt to fit

# die: a refusal before the run. Once the out-file is armed (below) it says so there
# too, so the file a lane reads names the refusal instead of keeping an old verdict.
OUT=
die() {
  echo "codex-review: $*" >&2
  if [ -n "$OUT" ]; then
    { echo "codex-review: NO VERDICT -- refused before the run, nothing was reviewed:"; echo "  $*"; } > "$OUT" 2>/dev/null || true
  fi
  exit 1
}

[ $# -ge 1 ] && [ $# -le 2 ] || die "usage: codex-review.sh <prompt-file|-> [out-file]
  refused: wrong number of arguments"
SRC=$1

# Arm the out-file FIRST: every later exit, a refusal included, leaves this file saying
# what this pass did. Its directory is checked here, before any run: a missing one used
# to fail the final `cp` under set -e, after the run, and the trap then deleted the
# run's only copy. The transcript beside it is overwritten the same way.
if [ $# -eq 2 ]; then
  [ -n "$2" ] || die "the out-file argument is empty
  refused: empty out-file"
  _od=$(dirname -- "$2")
  [ -d "$_od" ] || die "the out-file's directory '$_od' does not exist; nothing was run
  refused: missing out-file directory"
  _nv="codex-review: NO VERDICT -- this pass has not finished (started $(date -u +%Y-%m-%dT%H:%M:%SZ)); a file still saying this was left by a run that never completed"
  { echo "$_nv" > "$2" && echo "$_nv" > "$2.transcript"; } 2>/dev/null || die "cannot write '$2' or '$2.transcript'; nothing was run
  refused: out-file not writable"
  OUT=$2
fi

# The numeric knobs, refused unless they are canonical whole numbers in range.
# `CODEX_BUDGET=abc` or `0` used to give `alarm 0`, i.e. NO watchdog; `9m` gave 9 s; a
# non-numeric CODEX_SLOTS turned the cap off (every `-ge` test failed). The ceilings
# close the same holes from above: 4294967296 passes a digits test and wraps to 0 in
# Darwin's 32-bit alarm(), and a SLOTS past the shell's integer range fails its
# comparison inside `if`, which lets the run through. A leading zero is refused rather
# than read as octal or decimal. CODEX_WAIT is held to the same rule: a typo there
# silently meant "do not wait".
knob() { # <name> <value> <min> <max>
  case $2 in ''|*[!0-9]*|0?*) die "$1='$2' is not a whole number from $3 to $4
  refused: $1 not an integer in range" ;; esac
  [ ${#2} -le ${#4} ] && [ "$2" -ge "$3" ] && [ "$2" -le "$4" ] || die "$1='$2' is not a whole number from $3 to $4
  refused: $1 not an integer in range"
}
knob CODEX_BUDGET "$BUDGET" 1 86400
SLOTS=${CODEX_SLOTS:-4}
knob CODEX_SLOTS "$SLOTS" 1 1000
WAIT=${CODEX_WAIT:-0}
knob CODEX_WAIT "$WAIT" 0 3600

# (7) Which charter parts, decided BEFORE anything runs or waits for a slot, so a typo
# costs nothing and is never a pass on the wrong charter (D-128).
case ${CODEX_CHARTER-core} in
  core) PARTS=core ;;
  adapter) PARTS=core+adapter ;;
  *) die "CODEX_CHARTER='${CODEX_CHARTER}' is not core or adapter
  refused: unknown CODEX_CHARTER value" ;;
esac
if [ "${CODEX_NO_CHARTER:-0}" = "1" ] && [ "$PARTS" != core ]; then
  die "CODEX_CHARTER=adapter asks for the adapter GIVENs and CODEX_NO_CHARTER=1 for no charter
  refused: CODEX_CHARTER=adapter with CODEX_NO_CHARTER=1"
fi

# (5) Refuse to stack runs. Kill by PID, never `pkill -f` (see 4).
#
# live_runs() is THE detector, and it is a function so that nothing has to hand-roll one.
# `pgrep -f 'codex exec'` DIES on macOS ("illegal byte sequence") the moment a live run's
# argv carries non-ASCII, which a review prompt routinely does — and it dies by printing
# nothing and exiting non-zero, i.e. it reports BUSY as FREE. A waiter built on it exits
# at once and the next run is refused for "already running", which reads like the slot
# never freed.
#
# COUNT THE CODEX PROCESS, NOT ITS WATCHDOG. The pattern `codex exec` matches TWICE per
# run: the perl watchdog below is exec'd with the codex command as its argv, so its own
# command line carries the pattern. A bare match count reports 2 for one run and the cap
# means half what it says. Anchoring on the command NAME fixes it; the pid list stays
# whole-run so the kill-by-PID advice still points at something killable.
#
# The comm anchor FAILS CLOSED: if the pattern matches but no row's comm is `codex`, the
# anchor is wrong on this box, so report every match rather than reporting FREE.
# Reporting busy costs a wait; reporting free stacks runs, which is the failure this
# detector exists to prevent.
live_runs() {
	_lr_all=$(ps -eo pid,comm,command 2>/dev/null | grep "[c]odex exec")
	[ -n "$_lr_all" ] || return 0
	_lr_real=$(printf '%s\n' "$_lr_all" | awk '$2=="codex"{print $1}')
	[ -n "$_lr_real" ] || { printf '%s\n' "$_lr_all" | awk '{print $1}'; return 0; }
	printf '%s\n' "$_lr_real"
}
live_count() { live_runs | grep -c . ; }

# CODEX_SLOTS — how many reviews may be in flight AT ONCE across the whole box, not just
# this repo: several sessions run concurrently and each one's review is a mandatory,
# foreground, minutes-long step, so a single slot serialises the entire fleet behind
# whoever grabbed it first. The cap is sized to the fleet, not to a measured throughput
# ceiling — nothing here knows the provider's own limits, so if runs start FAILING rather
# than QUEUEING, that ceiling is what you have found and the remedy is a lower
# CODEX_SLOTS, never a longer watchdog.
#
# It is enforced by OBSERVATION, not by a lock: N sessions checking simultaneously can all
# see the same free slot and all start, so treat the number as intent and expect an
# occasional overshoot. CODEX_SLOTS=1 for serial behavior. (Read and validated above.)

# The detector's own positive control. `ps -eo pid,command` is the one thing every clause
# here stands on, so prove it can see a process whose argv we KNOW — this shell — before
# trusting a zero from it. Without this, a ps that silently emitted nothing would read as
# "slot free" and every stacking guard below would be inert.
if ! ps -eo pid,command 2>/dev/null | awk -v p=$$ '$1 == p {found=1} END {exit !found}'; then
  die "the process-table detector is BROKEN (ps -eo pid,command cannot see this shell, pid $$) — a zero from it would read as 'slot free'; fix that before trusting any run"
fi

# CODEX_WAIT=<seconds>: block until a slot frees instead of refusing. Parallel sessions
# share a fixed pool, so "come back later" is a normal outcome, not an error — but the
# waiting belongs HERE, on the verified detector, not in a per-session poll. Keep it small
# (<=60s) and re-invoke, rather than asking one blocking call to cover the wait AND the
# run against the 600s foreground cap.
if [ "$WAIT" -gt 0 ]; then
  waited=0
  while [ "$(live_count)" -ge "$SLOTS" ] && [ "$waited" -lt "$WAIT" ]; do
    echo "codex-review: all $SLOTS slots busy (pids: $(live_runs | tr '\n' ' ')); waited ${waited}s of ${WAIT}s" >&2
    sleep 15
    waited=$((waited + 15))
  done
fi

live=$(live_runs || true)
n=$(printf '%s' "$live" | grep -c . || true)
if [ "$n" -ge "$SLOTS" ]; then
  echo "codex-review: all $SLOTS review slots are BUSY ($n live; pids: $(echo "$live" | tr '\n' ' '))." >&2
  ps -o pid,etime,time,%cpu -p $(echo "$live" | tr '\n' ',' | sed 's/,$//') >&2 || true
  echo "  Wait for one instead of stacking: CODEX_WAIT=60 sh scripts/codex-review.sh <prompt>" >&2
  echo "  If a row's CPU time is ~0 while ELAPSED climbs, THAT ONE is WEDGED (almost certainly" >&2
  echo "  backgrounded). Kill that PID specifically — never all of them, and never pkill -f;" >&2
  echo "  a peer session's healthy run is very likely one of these rows." >&2
  echo "codex-review: refusing to start a competing run (exit 75: NOT RUN)" >&2
  # Its own exit code and its own word: a busy pool is "come back later", not a
  # refusal of the request, and the out-file must say nothing was reviewed.
  [ -z "$OUT" ] || echo "codex-review: NOT RUN -- all $SLOTS review slots were busy ($n live); nothing was reviewed. Rerun (CODEX_WAIT=60) to the same out-file." > "$OUT"
  exit 75
fi
[ "$n" -eq 0 ] || echo "codex-review: $n of $SLOTS slots in use; starting in the free one." >&2

TMP=$(mktemp -d "${TMPDIR:-/tmp}/codex-review.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM

if [ "$SRC" = "-" ]; then cat > "$TMP/raw"; else [ -f "$SRC" ] || die "no such prompt file: $SRC"; cat "$SRC" > "$TMP/raw"; fi
[ -s "$TMP/raw" ] || die "the prompt is empty"

# (7) The standing charter, prepended so settled decisions arrive as GIVENs instead of
# being re-litigated, and environment facts arrive stated instead of being re-derived by
# exploration — which is the dominant timeout mode.
# Both paths on their own lines: check-prose-budget.sh reads them from here.
CHARTER="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/docs/review-charter.md"
CHARTER_ADAPTER="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/docs/review-charter-adapter.md"
if [ "${CODEX_NO_CHARTER:-0}" != "1" ]; then
  [ -f "$CHARTER" ] || die "docs/review-charter.md is missing (CODEX_NO_CHARTER=1 to skip)"
  cat "$CHARTER" > "$TMP/charter"
  if [ "$PARTS" = core+adapter ]; then
    [ -f "$CHARTER_ADAPTER" ] || die "docs/review-charter-adapter.md is missing; CODEX_CHARTER=adapter asked for it"
    { printf '\n\n'; cat "$CHARTER_ADAPTER"; } >> "$TMP/charter"
  fi
  { cat "$TMP/charter"; printf '\n\n---\n\n'; cat "$TMP/raw"; } > "$TMP/full"
  mv "$TMP/full" "$TMP/raw"
  CHARTER_NOTE="charter=$(wc -c < "$TMP/charter" | tr -d ' ')B($PARTS)"
else
  CHARTER_NOTE="charter=off"
fi

# (4) ASCII-fold, through scripts/ascii-fold.py: its table is the one check-charter.sh
# folds the charter with, so a GIVEN that would reach the reviewer with a `?` in place
# of a character (`fence.Field`'s `␣` did, Codex report A5) fails that check first.
# -I: the cwd is the repository under review, and a planted module there must not run.
FOLD="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/ascii-fold.py"
[ -f "$FOLD" ] || die "scripts/ascii-fold.py is missing; the prompt cannot be folded
  refused: no fold table"
LC_ALL=C tr -d '\000' < "$TMP/raw" | python3 -I -B "$FOLD" > "$TMP/prompt" || die "scripts/ascii-fold.py failed; nothing was run
  refused: the fold failed"

echo "codex-review: model=$MODEL effort=$EFFORT tier=$TIER budget=${BUDGET}s $CHARTER_NOTE prompt=$(wc -c < "$TMP/prompt") bytes" >&2

# (1)(2)(6) Foreground, stdin closed, watchdog.
#
# The fork+setpgid+TERM->KILL form, NOT `alarm; exec`. The simpler form is a measured
# NO-OP against a Go child, because Go ignores an uncaught SIGALRM — and its verdict was
# inferred from the child's exit code, so a binary that trapped the signal and exited 0
# within the grace period would read as a SUCCESSFUL run with silently truncated output,
# on the very instrument every review receipt rests on. Here the $timed_out LATCH is the
# verdict (exit 124, unconditional once fired), the parent escalates TERM->KILL on the
# process GROUP, waitpid retries EINTR, and a failed exec is a fail-closed 127.
set +e
T0=$(date +%s)
perl -MPOSIX -e '
  my $t = shift;
  my $pid = fork();
  die "fork: $!\n" unless defined $pid;
  if ($pid == 0) { POSIX::setpgid(0, 0); exec @ARGV; exit 127; }
  POSIX::setpgid($pid, $pid);
  my $timed_out = 0;
  my $deadline = time + $t;
  $SIG{ALRM} = sub {
    return if time() < $deadline;
    $timed_out = 1;
    kill "TERM", -$pid; select undef, undef, undef, 2.0; kill "KILL", -$pid;
  };
  alarm $t;
  my ($waited, $status);
  do { $waited = waitpid($pid, 0); $status = $? if $waited == $pid; } while $waited < 0 && $!{EINTR};
  alarm 0;
  exit 124 if $timed_out;
  exit 125 unless defined $status;
  exit(128 + ($status & 127)) if $status & 127;
  exit($status >> 8);
' "$BUDGET" \
  codex exec -s read-only -m "$MODEL" -c model_reasoning_effort="$EFFORT" \
  -c service_tier="$TIER" -o "$TMP/reply" \
  "$(cat "$TMP/prompt")" < /dev/null > "$TMP/out" 2>&1
rc=$?
T1=$(date +%s)
set -e

# (3) VERIFY the header rather than trusting it. ANCHORED to the run header: the block
# between codex's first two `--------` lines, which precedes the echoed prompt, and each
# line matched whole. The old check was an unanchored grep over the entire output, so a
# prompt or a reply that quoted `model: gpt-6-astra` satisfied it, and so did
# `model: gpt-6-astra-mini` for a prefix.
hdr() { awk -v k="$1" '/^--------$/ { if (++n == 2) exit; next } n == 1 && index($0, k ": ") == 1 { print substr($0, length(k) + 3); exit }' "$TMP/out"; }
got_model=$(hdr model)
got_effort=$(hdr "reasoning effort")
if [ "$got_model" = "$MODEL" ] && [ "$got_effort" = "$EFFORT" ]; then
  header=ok
else
  header=MISMATCH
  echo "codex-review: HEADER MISMATCH -- the run header says model=${got_model:-<none>} effort=${got_effort:-<none>}, not $MODEL / $EFFORT." >&2
  echo "  A silent downgrade makes this a same-model-family review, i.e. no gate at all." >&2
fi

# codex's own token count: the number on the line after its last `tokens used` line
# (a reply that itself says "tokens used" is followed by prose, not a number).
tokens=$(awk '/^tokens used$/ { if ((getline t) > 0 && t ~ /^[0-9,]+$/) v = t } END { gsub(/,/, "", v); print v }' "$TMP/out")
# Exit 3 only overrides a 0: a failed codex keeps its own code, which says more. The
# status line carries both: rc is codex's, exit is this script's.
xrc=$rc
if [ "$rc" -eq 0 ] && { [ "$header" != ok ] || [ ! -s "$TMP/reply" ]; }; then xrc=3; fi
STATUS="codex-review: status rc=$rc exit=$xrc elapsed=$((T1 - T0))s model=$MODEL effort=$EFFORT header=$header got=${got_model:-none}/${got_effort:-none} tier=$TIER(requested) $CHARTER_NOTE prompt=$(wc -c < "$TMP/prompt" | tr -d ' ')B tokens=${tokens:-none}"

fired=0
if [ "$rc" -eq 124 ] || [ "$rc" -eq 142 ] || [ "$rc" -eq 14 ]; then
  fired=1
  echo "codex-review: the ${BUDGET}s watchdog FIRED." >&2
  echo "  This is a SCOPE problem, not a reason to background it. Cut the prompt down --" >&2
  echo "  name the exact files/symbols to read, say 'do not explore', 'no web search'." >&2
  echo "  A tightly scoped pass returns in minutes; a broad one explores until it dies." >&2
fi

# The verdict file, built in $TMP and then written OVER the armed files in place (not
# renamed into the directory: a writable out-file in a directory the caller cannot
# write would fail a rename after the run, and the trap would delete the result). The
# transcript goes first, so a verdict never points at a missing one. Writing in place is
# not atomic: an interrupted write leaves a file without its last line, so a verdict
# without its `codex-review: status` line is not a finished verdict.
TR=${OUT:+$OUT.transcript}
{
  if [ "$fired" -eq 1 ]; then
    echo "codex-review: NO VERDICT -- the ${BUDGET}s watchdog fired (rc=$rc); the partial transcript is ${TR:-on stdout}."
  elif [ "$rc" -ne 0 ]; then
    echo "codex-review: NO VERDICT -- codex exited $rc; the transcript is ${TR:-on stdout}."
  elif [ ! -s "$TMP/reply" ]; then
    echo "codex-review: NO VERDICT -- codex exited 0 but wrote no reply; the transcript is ${TR:-on stdout}."
  else
    if [ "$header" != ok ]; then
      echo "codex-review: HEADER MISMATCH -- the run header says model=${got_model:-<none>} effort=${got_effort:-<none>}: this reply is NOT a $MODEL@$EFFORT pass, and no receipt may cite it as one."
    fi
    cat "$TMP/reply"
    echo
  fi
  echo "$STATUS"
} > "$TMP/verdict"

if [ -n "$OUT" ]; then
  { cat "$TMP/out"; echo "$STATUS"; } > "$TR"
  cat "$TMP/verdict" > "$OUT"
  cat "$OUT"
else
  cat "$TMP/out"
  echo "$STATUS"
fi
exit "$xrc"
