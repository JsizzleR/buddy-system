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

die() { echo "codex-review: $*" >&2; exit 1; }

[ $# -ge 1 ] || die "usage: codex-review.sh <prompt-file|-> [out-file]"
SRC=$1
OUT=${2:-}

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
# occasional overshoot. CODEX_SLOTS=1 for serial behavior.
SLOTS=${CODEX_SLOTS:-4}

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
WAIT=${CODEX_WAIT:-0}
if [ "$WAIT" -gt 0 ] 2>/dev/null; then
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
  die "refusing to start a competing run"
fi
[ "$n" -eq 0 ] || echo "codex-review: $n of $SLOTS slots in use; starting in the free one." >&2

TMP=$(mktemp -d "${TMPDIR:-/tmp}/codex-review.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM

if [ "$SRC" = "-" ]; then cat > "$TMP/raw"; else [ -f "$SRC" ] || die "no such prompt file: $SRC"; cat "$SRC" > "$TMP/raw"; fi
[ -s "$TMP/raw" ] || die "the prompt is empty"

# (7) The standing charter, prepended so settled decisions arrive as GIVENs instead of
# being re-litigated, and environment facts arrive stated instead of being re-derived by
# exploration — which is the dominant timeout mode.
CHARTER="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)/docs/review-charter.md"
if [ "${CODEX_NO_CHARTER:-0}" != "1" ]; then
  [ -f "$CHARTER" ] || die "docs/review-charter.md is missing (CODEX_NO_CHARTER=1 to skip)"
  { cat "$CHARTER"; printf '\n\n---\n\n'; cat "$TMP/raw"; } > "$TMP/full"
  mv "$TMP/full" "$TMP/raw"
  CHARTER_NOTE="charter=$(wc -c < "$CHARTER" | tr -d ' ')B"
else
  CHARTER_NOTE="charter=off"
fi

# (4) ASCII-fold.
LC_ALL=C tr -d '\000' < "$TMP/raw" | python3 -c '
import sys
s = sys.stdin.buffer.read().decode("utf-8", "replace")
for a, b in (("—","--"),("–","-"),("’","\x27"),("“","\""),("”","\""),
             ("→","->"),("≥",">="),("§","sec"),("…","..."),("×","x"),
             ("⏎","<CR>")):
    s = s.replace(a, b)
sys.stdout.write(s.encode("ascii", "replace").decode("ascii"))
' > "$TMP/prompt"

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
  -c service_tier="$TIER" \
  "$(cat "$TMP/prompt")" < /dev/null > "$TMP/out" 2>&1
rc=$?
set -e

[ -n "$OUT" ] && cp "$TMP/out" "$OUT"

# (3) VERIFY the header rather than trusting it.
if ! grep -q "model: $MODEL" "$TMP/out" || ! grep -q "reasoning effort: $EFFORT" "$TMP/out"; then
  echo "codex-review: WARNING — the run header does not echo $MODEL / $EFFORT." >&2
  grep -E "^(model|reasoning effort):" "$TMP/out" >&2 || echo "  (no header at all)" >&2
  echo "  A silent downgrade makes this a same-model-family review, i.e. no gate at all." >&2
fi

if [ "$rc" -eq 124 ] || [ "$rc" -eq 142 ] || [ "$rc" -eq 14 ]; then
  echo "codex-review: the ${BUDGET}s watchdog FIRED." >&2
  echo "  This is a SCOPE problem, not a reason to background it. Cut the prompt down —" >&2
  echo "  name the exact files/symbols to read, say 'do not explore', 'no web search'." >&2
  echo "  A tightly scoped pass returns in minutes; a broad one explores until it dies." >&2
fi
cat "$TMP/out"
exit "$rc"
