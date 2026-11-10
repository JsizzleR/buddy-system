# codex-gate-row.sh — sourced, never run: the ONE Codex row that install.sh's
# report and env-check.sh both print (Codex report B1).
#
# WHY. A plain Codex session on this machine is gated only if the operator
# hand-wired buddy's gate into Codex's hooks.json (USAGE "Opt-in wiring"). An
# absent hook is a silent allow: nothing in buddy can see a gate that was never
# called, so a Codex patch to a peer-held file went through and no report said
# so. Measured on the developing machine 2026-11-10: ~/.codex existed, no
# hooks.json, a config.toml trust entry for that missing file, and five
# non-review Codex rollouts in the repo in 30 days.
#
# WHAT IT READS. `${CODEX_HOME:-$HOME/.codex}`: no directory, no row (Codex is
# not on this machine, and a row would be noise). Its hooks.json is judged by
# `buddy diagnose --codex-hooks`, the one reader of that file (marker, matcher,
# absolute path, the fail-closed wrapper, async; D-107's continuation), so the
# two reports and diagnose cannot drift apart. diagnose runs with cwd `/`, so
# it judges no repository and reads no ledger. A gate counts as wired only when
# diagnose lists it clean: `{"event":"PreToolUse","verb":"gate","marker":"codex"}`
# and no finding (Go's encoder writes that object byte-for-byte, a finding adds
# a field). config.toml is grepped for a hooks table other than `hooks.state`
# (trust state): Codex can declare hooks there too, and this reads only
# hooks.json, so a file-less machine with such a table CANNOT ASSESS rather
# than NOT WIRED.
#
# WHAT IT NEVER DOES. Write, edit, trust or wire anything (D-135's shape:
# report only; `install.sh --wire-hooks` is Claude Code's settings only). It
# says nothing about trust or what Codex loaded: an edited line is skipped
# until it is trusted again in Codex's /hooks, and only a real Codex patch
# against a peer-held scope shows enforcement.
#
# codex_gate_row <buddy binary> — sets cgr_state (wired | NOT WIRED |
# CANNOT ASSESS) and cgr_detail; returns 1, setting neither, when there is no
# Codex directory.
codex_gate_row() {
	cgr_bin=$1
	cgr_dir=${CODEX_HOME:-$HOME/.codex}
	cgr_state="" cgr_detail=""
	[ -d "$cgr_dir" ] || return 1
	# diagnose runs from / and wants an absolute file: a relative bin dir
	# (install.sh's QA-11) or CODEX_HOME would otherwise read CANNOT ASSESS.
	case $cgr_bin in ''|/*) ;; *) cgr_bin=$PWD/$cgr_bin ;; esac
	case $cgr_dir in /*) ;; *) cgr_dir=$PWD/$cgr_dir ;; esac
	cgr_file=$cgr_dir/hooks.json
	cgr_toml=$cgr_dir/config.toml
	# Paths are SHOWN on one line (invariant 9's register): a newline in
	# CODEX_HOME would otherwise print a second, forged row (Codex code pass).
	# The raw paths are used only for the filesystem.
	cgr_sfile=$(cgr_line "$cgr_file") cgr_stoml=$(cgr_line "$cgr_toml")
	# A directory that cannot be searched hides its files: absence is not
	# knowable, so it is no NOT WIRED (Codex code pass).
	if [ ! -x "$cgr_dir" ]; then
		cgr_state="CANNOT ASSESS" cgr_detail="cannot search $(cgr_line "$cgr_dir")"
		return 0
	fi
	cgr_more=""
	# config.toml is SCANNED, not parsed: any table header naming hooks, other
	# than the trust state's `[hooks.state...]`, counts, whatever its key
	# quoting (`["hooks".PreToolUse]`, Codex code pass). A dotted key or an
	# inline table outside a header is not seen; that stays open.
	if [ -e "$cgr_toml" ] && [ ! -r "$cgr_toml" ]; then
		cgr_more="cannot read $cgr_stoml, which can declare hooks too"
	elif [ -f "$cgr_toml" ] && grep -E '^[[:space:]]*\[.*hooks' "$cgr_toml" 2>/dev/null | grep -Eqv '^[[:space:]]*\[[[:space:]]*hooks[[:space:]]*\.[[:space:]]*state([].[:space:]]|$)'; then
		cgr_more="$cgr_stoml declares hooks too, and this reads only hooks.json"
	fi
	if [ ! -e "$cgr_file" ] && [ ! -L "$cgr_file" ]; then
		if [ -n "$cgr_more" ]; then
			cgr_state="CANNOT ASSESS" cgr_detail="no $cgr_sfile; $cgr_more"
		else
			cgr_state="NOT WIRED" cgr_detail="no $cgr_sfile: a Codex session here patches peer-held files unrefused (USAGE \"Opt-in wiring\")"
		fi
		return 0
	fi
	if [ ! -r "$cgr_file" ]; then
		cgr_state="CANNOT ASSESS" cgr_detail="cannot read $cgr_sfile"
		return 0
	fi
	if [ -z "$cgr_bin" ] || [ ! -x "$cgr_bin" ]; then
		cgr_state="CANNOT ASSESS" cgr_detail="no buddy to read $cgr_sfile with (buddy diagnose --codex-hooks)"
		return 0
	fi
	# Status kept apart from the output: a refusal (a symlink, over 1 MiB, not
	# JSON) is not a verdict about the gate.
	if ! cgr_out=$(cd / && "$cgr_bin" diagnose --codex-hooks "$cgr_file" 2>/dev/null); then
		cgr_state="CANNOT ASSESS" cgr_detail="buddy diagnose --codex-hooks $cgr_sfile refused it (a symlink, over 1 MiB or not a hooks layer)"
		return 0
	fi
	case $cgr_out in
	*'{"event":"PreToolUse","verb":"gate","marker":"codex"}'*)
		cgr_state="wired" cgr_detail="gate wired fail-closed in $cgr_sfile (the file only: trust is Codex's /hooks', enforcement a real patch's)" ;;
	*'"event":"PreToolUse","verb":"gate"'*)
		cgr_state="NOT WIRED" cgr_detail="the gate in $cgr_sfile draws findings (marker, matcher, path, fail-closed wrapper or async): buddy diagnose --codex-hooks $cgr_sfile lists them" ;;
	*)
		cgr_state="NOT WIRED" cgr_detail="$cgr_sfile runs no buddy gate on PreToolUse (USAGE \"Opt-in wiring\")" ;;
	esac
	return 0
}

# cgr_line <s> — s on one line: every byte that is not printable ASCII, a
# newline included, shown as `?`.
cgr_line() {
	printf '%s' "$1" | LC_ALL=C awk 'BEGIN { ORS = "" } { if (NR > 1) print "?"; gsub(/[^ -~]/, "?"); print }'
}
