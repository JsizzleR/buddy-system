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
SCRIPT=$ROOT/scripts/codex-review.sh
T=$(mktemp -d "${TMPDIR:-/tmp}/check-codex-review.XXXXXX")
BUSY=
trap '[ -z "$BUSY" ] || kill "$BUSY" 2>/dev/null || true; rm -rf "$T"' EXIT INT TERM
mkdir -p "$T/bin" "$T/out"

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
echo "session id: 00000000-0000-0000-0000-000000000001"
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
CLEAN="-u CODEX_MODEL -u CODEX_EFFORT -u CODEX_TIER -u CODEX_BUDGET -u CODEX_SLOTS -u CODEX_WAIT -u CODEX_CHARTER -u CODEX_NO_CHARTER"
fails=0
fail() { echo "check-codex-review: FAIL $*" >&2; fails=$((fails + 1)); }

# run <name> <want-rc> <out-file> [VAR=value ...] -- runs the script on $T/prompt
# (or $PROMPT, set on its own line: an assignment prefixed to a function call
# persists after it in sh), recording its rc and the stub's launch count.
run() {
  _n=$1 _want=$2 _out=$3; shift 3
  : > "$T/log"
  set +e
  env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 "$@" \
    sh "$SCRIPT" "${PROMPT:-$T/prompt}" "$_out" > "$T/stdout.$_n" 2> "$T/stderr.$_n"
  _rc=$?
  set -e
  launched=$(grep -c . "$T/log" || true)
  [ "$_rc" -eq "$_want" ] || fail "$_n: exit $_rc, want $_want (stderr: $(head -3 "$T/stderr.$_n" | tr '\n' ' '))"
}
first() { head -1 "$1"; }
has() { grep -qF -- "$2" "$1"; }
stale() { echo "SENTINEL-7f3a old verdict from an earlier pass" > "$1"; echo "SENTINEL-7f3a old transcript" > "$1.transcript"; }

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
st=$(tail -1 "$O")
case $st in "codex-review: status rc=0 exit=0 "*) ;; *) fail "ok: last line is '$st', want the status line" ;; esac
for k in "header=ok" "model=gpt-6-astra" "effort=xhigh" "tokens=12345" "B(core)" "prompt=" "elapsed="; do
  case $st in *"$k"*) ;; *) fail "ok: status line lacks '$k': $st" ;; esac
done
[ "$(tail -1 "$O.transcript")" = "$st" ] || fail "ok: the transcript does not end with the status line"
cmp -s "$O" "$T/stdout.ok" || fail "ok: stdout is not the out-file's content"

# --- no out-file: stdout is the transcript, then the status line ---
: > "$T/log"
env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 sh "$SCRIPT" "$T/prompt" > "$T/stdout.none" 2>/dev/null || fail "none: exit $?"
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

# --- refusals before the run: exit 1, NO VERDICT, nothing launched ---
for kv in CODEX_BUDGET=abc CODEX_BUDGET=0 CODEX_BUDGET=9m CODEX_BUDGET=4294967296 CODEX_BUDGET=0600 CODEX_SLOTS=x CODEX_SLOTS=0 CODEX_SLOTS=9223372036854775808 CODEX_WAIT=1m CODEX_WAIT=3601 CODEX_CHARTER=bogus; do
  n=refuse-$(echo "$kv" | tr '=' '-')
  O=$T/out/$n.txt; stale "$O"
  run "$n" 1 "$O" "$kv"
  case $(first "$O") in "codex-review: NO VERDICT -- refused"*) ;; *) fail "$n: first line '$(first "$O")', want NO VERDICT -- refused" ;; esac
  has "$O" SENTINEL-7f3a && fail "$n: stale verdict survived"
  has "$O" "refused: " || fail "$n: out-file does not name the refusal"
  [ "$launched" -eq 0 ] || fail "$n: stub launched $launched times, want 0"
done
# Positive control for the knob refusals: valid values run.
run knobs-ok 0 "$T/out/knobs.txt" CODEX_BUDGET=86400 CODEX_SLOTS=1000 CODEX_WAIT=0
[ "$launched" -eq 1 ] || fail "knobs-ok: stub launched $launched times, want 1"

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
env $CLEAN PATH="$T/bin:$PATH" STUB_LOG="$T/log" STUB_MODE=slow CODEX_SLOTS=1000 CODEX_NO_CHARTER=1 CODEX_BUDGET=60 \
  sh "$SCRIPT" "$T/prompt" "$O" > /dev/null 2>&1 &
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
echo "check-codex-review: GREEN (ok, no-out, 3 header mismatches + quoted header + no model line, busy 75, 11 knob refusals + control, killed mid-run, read-only dir, missing prompt, missing dir, watchdog 124, no reply 3, codex failure)"
