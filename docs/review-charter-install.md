# Review charter: install GIVENs (full text)

The GIVENs only install code rests on, appended by `scripts/codex-review.sh` unless `CODEX_DIFF` names a
diff with no path in the install area. The main charter's one-line
stub of each binds as written; this is the full text (D-158).

53. **`scripts/install.sh` is the one install and upgrade path (D-050).** It rebuilds every buddy
    binary the machine runs, found by where it is run from (`~/bin`, the launchd daemon's plist
    program, the checkout's `bin/`), runs each once, installs the skill, `kickstart -k`s the
    daemon, runs setup-clone, and REPORTS hook wiring. Every sub-step's status is the install's.
    It edits settings only under the operator's opt-in `--wire-hooks` (D-135; `buddy update
    --wire-hooks` forwards it to an install.sh carrying the line `# buddy-install-accepts:
    --wire-hooks`): before step 1 a preflight refuses a file it would refuse to write; at step 6
    `scripts/wire-hooks.py` APPENDS, as new entries, exactly the tokens the report's grep named
    missing in a snapshot the file must still match, plus the #71 browser beats
    `gate-hook.py`'s `browser_gone` names, each spelled by `gate-hook.py`'s table (README's and
    USAGE's blocks held to it); a text splice verified to decode to the old document plus those
    entries, a 0600 backup, a same-directory temp renamed over it; refused, nothing written: a
    symlink, a hard link, another user's or a read-only file, over 1 MiB, not UTF-8 JSON, a
    duplicate key, a non-object `hooks`, a non-list event or a non-hook entry, `disableAllHooks`,
    and (before step 1) a `$BUDDY_BIN_DIR` that is not `~/bin`, which the lines run. No harness environment is refused (the operator's choice:
    the write set is fixed, and `env -u` defeats a check); settings are never relayed. Do not
    propose it rewrite, reorder, "fix" or remove an entry, touch another key or file, or wire the
    Agent-model hook; nor `cp` for the binaries. It also REPORTS, and never installs or fails on, the Agent-model hook
    (D-120): whether `$BUDDY_BIN_DIR/agent-model-gate.py` is byte-identical to
    `scripts/hooks/agent-model-gate.py` (naming the `cp` when not), and whether settings wire
    a PreToolUse command naming it under a matcher selecting `Agent`, decoded with
    `gate-hook.py`'s matcher reading; a wired path with no file is said loudly (python3 exits 2,
    which blocks every Agent call). Do not propose that it copy the script: the hook is the
    operator's opt-in (D-117). It stamps each buddy with its chat posture and keeps it while
    `BUDDY_CHAT` is unset (D-124: `off`/`on` switch, anything else refused, no stamp is on); do
    not propose reading the posture from settings, which is `hello`'s knob.

111. **`buddy update` runs install.sh in the checkout install.sh stamped into buddy, after a
    local report (D-110).** The operator chose install only: it never fetches or pulls, and
    `buddy` stays network-free. The checkout is an `-ldflags -X` stamp, not a record file
    (a file drifts from the bytes it describes); a missing, relative, gone or foreign-module
    stamp, or a non-regular `scripts/install.sh`, refuses naming install.sh, never a guess.
    The installed commit is the vcs stamp of `$BUDDY_BIN_DIR/buddy`, the file the hooks run,
    not the running process's. origin/main is "as of the last fetch", dated by FETCH_HEAD only
    when it records origin's main, else by the ref's reflog entry. The report's git reads run
    no filter, fsmonitor, lazy fetch or submodule. Dirty or off-main WARNS and installs; one
    case refuses: an installed commit in the checkout that
    HEAD does not contain (a downgrade is D-037's machine-wide deny); shallow or unknown
    history says it cannot place it. install.sh runs with no stdin, its status the verb's.
    Not a hook verb, opens no ledger, never delegable (D-083). Do not propose a pull, a
    `--check` alias, or refusing a dirty or off-main tree.

119. **Removal is `scripts/uninstall.sh [--chat] [--dry-run]`, which `buddy uninstall` runs in
    the stamped checkout as update runs install.sh (D-125).** Never a knob of install. Order:
    the daemon through launchd only (`print` by label, 113 is absent, any other status stops
    the run before anything goes; `bootout`, confirmed), its plist MOVED to ~/.buddylist, the
    buddylist copies, the skills identical to the checkout's, buddy LAST by one unlink, so a
    stop leaves the claims half working and a re-run finishes. Ours is a build-info `path`
    line in the bytes, never a run. A full run that leaves an executable at
    `$BUDDY_BIN_DIR/buddy` exits INCOMPLETE. `--chat` first runs the installed buddy's
    `BUDDY_CHAT=off buddy update` and removes chat only once D-124's stamp reads off. By
    default it also removes (D-173) the buddy hook lines whose `$HOME/bin` binary it removed
    (`wire-hooks.py unwire`: gate-hook.py's one table, a hook buddy's only when its command is
    that table's exactly, a verified splice, backup, rename; LAST, after buddy's unlink; a
    refusal named, never a stop; an entry shared with a foreign hook left whole), the
    `buddylist` MCP registrations whose command it removed (`claude mcp remove`, in each
    project's physical path, read back, each bounded by `BUDDY_MCP_TIMEOUT` (D-176: past
    it the process group is stopped and the registration named left, never a stop);
    `~/.claude.json` never written), and `com.buddy-system.oscar` and `com.buddy-system.ergo`
    when the program's bytes are get-oscar.sh's or get-ergo.sh's pinned build (D-176: build
    path and `vcs.revision`; a Label and a program under ~/.buddylist prove nothing; bootout
    confirmed, plist moved), and the model router's own files with its skill (D-179: by
    name and bytes, on a dir fd; a refusal leaves it whole, named, never a stop). Other
    settings, an unprovable server agent, Codex
    hooks, ledgers, the journal, core.hooksPath, `.bak`, an edited skill and the Agent-model
    copy and line are reported, never touched; processes are never signalled. Install and uninstall hold ONE
    lock for their whole run (D-126): `$BUDDY_BIN_DIR/.buddy-install.lock`, a symlink naming
    the holder's pid and start time, a dead or reused pid taken over; not HOME-keyed, not a
    mkdir'd directory. Do not propose a `--purge`, editing settings beyond those lines or
    `~/.claude.json` directly, killing a daemon or a `buddylist` process, or removing buddy
    before chat.
