package cli

// The gate refuses gh stack's rewriting subcommands to a session that does not
// hold .buddy/slot/gh-stack (D-108).
//
// THE FAILURE. The operator adopted gh-stack (`gh extension install
// github/gh-stack --pin v0.3.0`) to restack a dependent lane. A restack
// rewrites lanes' branches IN THEIR OWN WORKTREES (`gh stack rebase` runs
// `git -C <lane's worktree> rebase --onto …` for every stack branch), a plain
// `gh stack rebase` fetches origin and can fast-forward trunk, and one
// conflict holds the stack catalog's lock (`<git-common-dir>/gh-stack/`) for
// EVERY stack until `--abort`/`--continue`. The gate never saw any of it: `gh`
// sat in dataCommands, so `analyze` returned before `command()` and every
// `gh stack …` was a pathless Bash call, pause-only (the 2026-10-27 trial:
// nothing refused it, nothing consulted a claim). The skill's recipe says only
// the session holding `.buddy/slot/gh-stack` runs it; this file is the gate's
// half of that rule. The slot is one per checkout because gh-stack's catalog
// and lock are one per git common dir, the directory buddy.db lives in, so
// every linked worktree shares both and no mapping is needed.
//
// WHAT IT IS NOT. A `git rebase` or cherry-pick never runs pre-commit, so a
// commit the commit gate refused can be rewritten with rc=0 by plain git today;
// that is git's, not gh-stack's, and this arm does not close it. The arm
// answers WHO may run a stack mutation; WHICH mutation (`--no-trunk`, `--remote`,
// `unstack --local`, the network denied under sandbox-exec) is the recipe's
// discipline. Flag modelling was cut: pflag's `--remote --no-trunk` makes
// `--no-trunk` a remote NAME, every such distinction is a fresh false-allow or
// false-deny surface, and the ten A-01 re-verify rounds each introduced a
// defect (memory: narrow folds). So the slot is the only test, for every
// gated subcommand, under every harness: a dynamic word at the command, stack
// or subcommand position stays OPEN everywhere (no Codex-only uncertainty; gh
// stack is a child process under either harness, with no in-process engine
// for D-087's arm to guard), and an over-budget brace word at a position the
// route reads exhausts everywhere, like `git -C`'s value.
//
// IDENTITY. The payload names session_id and no incarnation (invariant 12);
// the arm admits the caller when an OPEN claim owned by that session_id covers
// the slot (invariant 14's containment on invariant 13's fold, so a claim on
// `.buddy/slot` or `.buddy` admits too: D-035's own note). That is the same
// predicate denyIfHeld's OwnerOf applies to every path, sound for the same
// reason: Hello over an ended session orphans the old incarnation's open
// claims in the same transaction, so an open claim always carries its owner's
// current incarnation. What session_id alone cannot tell apart is a DELAYED
// hook from a dead incarnation of the same session (the design pass): the
// claim is current, the hook is not. That limitation is every gate check's,
// inherited here, not introduced. A subagent's hook carries the parent's
// session_id (D-099), so a holder's subagents are admitted with it.
//
// JURISDICTION. Every governed ledger the command can reach is asked: the
// hook cwd's and every literal directory change the walker discovers
// (shellPatchDirs). ALL must admit; the first refusal wins; an unreadable one
// denies (invariant 3); none governed is feature-off. A slot held in checkout
// A does not admit `cd B && gh stack rebase` (the design pass's P1), and the
// association is by command, not by statement: `gh stack rebase; cd B` needs
// B's slot too (over-approximation, recorded).
//
// Measured on the extension binary (v0.3.0, a non-repository directory, the
// network denied, 2026-10-27): `rebase --help --help=false`, `--help=false
// rebase` and `--help --help=false rebase` all RUN rebase (pflag lets a later
// flag cancel help), while `rebase --help`, `rebase -h x`, `help rebase`,
// `-- rebase`, `-- init a b`, `--help init a b`, `-v init a b` and an unknown
// subcommand print help or the version and run nothing. A value flag BEFORE
// the subcommand runs it: `--base main init a b`, `-m wip add x` and
// `--base -- init a b` all RAN (cobra's stripFlags hands the next word to a
// `--x`/`-x` flag with no `=` that is not one of the root's boolean flags,
// then finds the subcommand after it; the panel's bypass), while `--base
// rebase` and `--base main --remote origin init a b` fail on the unknown flag
// and run nothing. So help passes only as the sole word after the
// subcommand; leading dash words are read past to reach the subcommand, and
// the word after a value-shaped flag is tested as the subcommand and, when it
// is not gated, skipped as the value (ghSubcommand); `rebase -h x`, `--
// rebase`, `--base rebase` and `rebase --no-trunk --help` are refused:
// documented over-refusals of forms nobody types (the operator's Q4). On gh
// itself, `gh --help=false stack rebase` RAN rebase (cobra passes `stack`
// past a `--x=v` word and the extension command takes the rest unparsed),
// `gh --help stack` and `gh --version stack` reach the extension but run
// help or fail on the subcommand's flags, and gh refuses `gh --x stack`,
// `gh -R x stack`, `gh -xy stack` and `gh -- stack` itself; every leading
// dash word before gh's command is skipped and the next word is read as the
// command, so `--x`, `-xy` and `--` are over-refused and `-R x stack rebase`
// reads `x` and passes: right while gh (not pinned) has no value-taking root
// flag; a future one is recorded OPEN rather than read (the cross-check). `gh
// extension exec gh-stack`, `gh extension --help=false exec stack`, `gh
// extension exec --help=false stack` and `gh extension exec -- stack` are
// "not found": only `exec` then `stack` is read at those positions. An EMPTY
// word is dropped by cobra at a command position: `gh '' stack --version`
// dispatched, `gh-stack '' rebase` RAN rebase and `gh-stack '' init a b` ran
// init (the Codex code pass asked; measured), so an empty literal word is
// skipped wherever the route reads a command, as a flag taking nothing.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// ghStackGated are the subcommands that rewrite commits, move refs, write the
// shared catalog or reach GitHub (v0.3.0's help, each): the operator's
// decisions gate `checkout` (it fetches and imports) and `alias` (it installs
// `~/.local/bin/gs`, a spelling the gate cannot see; `gs` itself stays OPEN),
// and keep `unstack --local` and `rebase --abort/--continue` with the holder.
// `delete` is `unstack`'s alias. Not gated: view, bottom/top/up/down/trunk/
// switch (they move the CALLER's HEAD, as `git checkout <branch>` does),
// feedback, help, completion.
var ghStackGated = set("rebase", "sync", "push", "submit", "merge", "link", "unstack", "delete",
	"init", "add", "modify", "checkout", "alias")

// ghStackSlot is the resource slot the recipe claims (D-035, D-108).
const ghStackSlot = store.SlotPrefix + "/gh-stack"

// ghBudgetWhy names the exhaustion so the deny says where the gate stopped.
// It rides shellPatchReason's frame unchanged: the gate did stop reading
// before it could rule out a literal.
const ghBudgetWhy = "shell patch routing exceeds its brace expansion budget at a gh command position"

// ghRoute reads gh's own grammar from the word after `gh`. gh has no root
// options but --help/--version (measured: `gh -R x stack view` is refused by
// gh itself), so the first word is the command, always. Only `stack` and the
// extension-exec spelling lead anywhere; every other command's operands are
// data and are never scanned, so `gh pr comment -b "run gh stack rebase"` and
// `gh api repos/{owner}/{repo}/pulls` pass as they did when gh was a data
// program. An over-budget word at a position the route reads exhausts (a
// shell expands `gh {stack,x{1..300}} rebase` in place); anywhere else in
// gh's argv it is ignored, by gitRoute's rule (most of gh's words are data).
//
// CERTAIN TEXT ONLY. A quoted operand of an unknown program is parsed
// speculatively for a patch literal (`script -q echo apply_patch`), and that
// reading denied the fleet's own commands here: `sed -i ” 's/gh stack rebase
// /…/'` parses as the words `s/gh stack rebase /gh …`, whose first word's
// basename is gh; `awk '/gh stack rebase /…'` the same; a prompt `claude -p
// "take the slot; gh stack rebase"` or one with a backticked `gh stack
// rebase` (the panel's false-denial pass). So the gh route reads only text a
// shell certainly runs: the command, a -c operand, eval's and trap's text, a
// here-document fed to a shell, a function body. `watch 'gh stack rebase'`,
// `flock -c '…'`, `su -c '…'` and `ssh host '…'` are therefore OPEN (recorded
// in D-108); the bare operand `gh` of an unknown program is still read.
func (w *shellWalker) ghRoute(rest []shellWord) {
	if w.speculative > 0 {
		return
	}
	// gh's command is the first non-dash word: `gh --help=false stack rebase`
	// RAN rebase (measured; cobra reads `stack` past a `--x=v` word). gh's
	// own root flags are not read: it refuses `gh --x stack` and `gh -- stack`
	// itself, so skipping every leading dash word costs only the over-refusal
	// of forms gh rejects, and holds if gh (not pinned) grows a BOOLEAN root
	// flag; a value-taking one would hand its value to the command position
	// (`gh -X v stack rebase` reads `v`): recorded OPEN, not read.
	i := 0
	for ; i < len(rest); i++ {
		if !w.ghWord(rest[i]) {
			return
		}
		if rest[i].text != "" && !strings.HasPrefix(rest[i].text, "-") {
			break // an empty word is dropped by cobra (measured): read past it
		}
	}
	if i == len(rest) {
		return
	}
	switch rest[i].text {
	case "stack":
		w.ghStackArgs(rest[i+1:])
	case "extension", "ext", "extensions":
		// `gh extension exec stack <sub>` runs the extension (measured, all
		// three spellings); `exec gh-stack`, `--help=false exec stack`, `exec
		// --help=false stack` and `exec -- stack` are "not found". An empty
		// word is skipped before each (as gh's command position drops it;
		// `exec '' stack` is "not found" by the same rule: a harmless
		// over-refusal).
		args := rest[i+1:]
		for _, want := range []string{"exec", "stack"} {
			for len(args) > 0 && args[0].known && args[0].text == "" {
				args = args[1:]
			}
			if len(args) == 0 || !w.ghWord(args[0]) || args[0].text != want {
				return
			}
			args = args[1:]
		}
		w.ghStackArgs(args)
	}
}

// ghWord reports whether word is a literal the route may read: an over-budget
// brace exhausts, a dynamic word is the cooperative boundary (OPEN under every
// harness, D-108) and ends the route.
func (w *shellWalker) ghWord(word shellWord) bool {
	if word.overBudget {
		w.exhausted(ghBudgetWhy)
		return false
	}
	return word.known
}

// ghStackArgs reads the extension's argv (after `gh stack`, after `gh
// extension exec stack`, or after the binary `gh-stack` itself). Leading dash
// words are read past to reach the subcommand (ghSubcommand), because
// `--help=false rebase` and `--base main init a b` run their subcommand
// (measured); a lone `-h`/`--help`/`-v`/`--version` runs nothing and passes.
// A gated subcommand followed by exactly one word that is `-h`/`--help`
// passes (help); any other tail is the subcommand running, `--help
// --help=false` included (measured).
func (w *shellWalker) ghStackArgs(args []shellWord) {
	if w.speculative > 0 {
		return // see ghRoute: speculative text is not read for gh
	}
	i := w.ghSubcommand(args)
	if i < 0 {
		return // `gh stack`, only flags, help, or a dynamic word: nothing the route names
	}
	sub := args[i].text
	if !ghStackGated[sub] {
		return // view, up, help, an unknown or misspelt word: nothing is rewritten
	}
	tail := args[i+1:]
	if len(tail) == 1 && tail[0].known && (tail[0].text == "-h" || tail[0].text == "--help") {
		return
	}
	if w.route.ghStack == "" {
		// sub equals a ghStackGated key byte for byte (the map lookup above),
		// so the deny prints the table's constant, never the input word.
		w.route.ghStack = sub
	}
}

func ghHelpWord(t string) bool {
	return t == "-h" || t == "--help" || t == "-v" || t == "--version"
}

// ghSubcommand returns the index of the extension's subcommand in args, or -1
// when the route can name none (only flags, a dynamic word, exhaustion).
// Cobra's stripFlags hands the word after a `--x` or `-x` flag with no `=`
// that is not one of the root's boolean flags TO the flag and finds the
// subcommand after it: `gh stack --base main init a b` RAN init and `-m wip
// add x` RAN add (measured; the panel's bypass: the old loop read `main` as
// the subcommand and allowed). Which flags are boolean is v0.3.0's table
// (help, version), so neither reading is trusted alone: the word after a
// value-shaped flag is tested as the subcommand and, when it is not gated,
// skipped as the value. That refuses the union of both readings, never less
// than before this fold (`--base rebase` stays refused: an over-refusal, it
// fails on the unknown flag), and holds if a root flag is added. The value's
// text selects no command, so a dynamic value is skipped unread; an
// over-budget one exhausts (a shell expands it in place and shifts the
// subcommand).
func (w *shellWalker) ghSubcommand(args []shellWord) int {
	for i := 0; i < len(args); i++ {
		if !w.ghWord(args[i]) {
			return -1
		}
		t := args[i].text
		if t == "" {
			continue // dropped by cobra: `gh stack '' rebase` RAN rebase (measured)
		}
		if !strings.HasPrefix(t, "-") {
			return i
		}
		if !ghTakesValue(t) || i+1 == len(args) {
			continue
		}
		next := args[i+1]
		if next.overBudget {
			w.exhausted(ghBudgetWhy)
			return -1
		}
		if next.known && ghStackGated[next.text] {
			return i + 1
		}
		i++ // the flag's value, whatever it says
	}
	return -1
}

// ghTakesValue reports whether cobra's stripFlags would hand the next word to
// this leading flag: `--x` or a two-character `-x`, no `=`, not the root's
// help or version. `--` is read the same way (it ends cobra's flags, and
// `-- rebase` prints help: the over-refusal is documented), as is `-xy`'s
// opposite: a cluster takes nothing.
func ghTakesValue(t string) bool {
	return !strings.Contains(t, "=") && !ghHelpWord(t) && (strings.HasPrefix(t, "--") || len(t) == 2)
}

// gateGHStack adjudicates a gh stack route against EVERY ledger the command
// can reach: the home ledger (st, already open and pause-checked by cmdGate;
// nil when the hook cwd has none) and each governed repo a literal directory
// change discovers. It returns the deny reason, or "" when every governed
// ledger admits the caller or none governs (invariant 3: feature off).
func gateGHStack(h hookInput, env Env, st *store.Store, rc repoContext, sub string, repos map[string]gateRepo) string {
	dirs, routeErr := shellPatchDirs(h, env.getenv("HOME"))
	seen := make(map[string]bool)
	if st != nil {
		// The home ledger first, by its handle, never by a key match (a
		// vanished cwd keys its directory two ways, D-102; the panel).
		_, homeKey := repoKeyForDir(existingDir(filepath.Join(canon(h.Cwd), "x")))
		seen[homeKey] = true
		if routeErr != nil {
			return fmt.Sprintf("buddy gate cannot place this gh stack command's directories (%s); split the command; refusing %s",
				fence.Line(routeErr.Error(), 512), h.ToolName)
		}
		if rc.top == "" {
			return fmt.Sprintf("buddy gate could not resolve the worktree root for %s; refusing %s",
				fence.Line(h.Cwd, 512), h.ToolName)
		}
		if reason := ghStackSlotHeld(st, h, env, sub, ""); reason != "" {
			return reason
		}
	}
	for _, d := range dirs {
		dir, key := repoKeyForDir(d)
		if seen[key] {
			continue
		}
		seen[key] = true
		repo := openGateRepo(h, env, dir, key, repos)
		switch {
		case errors.Is(repo.err, errNoLedger):
			continue // not governed: outside our jurisdiction
		case repo.err != nil:
			return fmt.Sprintf("buddy: %s is in a repo whose ledger is unavailable (%s); refusing %s",
				fence.Line(dir, 512), fence.Line(repo.err.Error(), 512), h.ToolName)
		case repo.paused != "":
			return repo.paused // the target repo's brake applies (F-28)
		}
		ledger, top, where := repo.st, repo.rc.top, repo.rc.top
		if routeErr != nil {
			// Discovery stopped early (the candidate-directory cap). A
			// governed repo is in reach and the gate cannot say which others
			// are: refuse rather than admit on a partial list.
			return fmt.Sprintf("buddy gate cannot place this gh stack command's directories (%s); split the command; refusing %s",
				fence.Line(routeErr.Error(), 512), h.ToolName)
		}
		if top == "" {
			// A ledger with no worktree root (a bare repository): the slot
			// is a repo-relative scope and the claim can be read, but
			// gh-stack cannot run there either, and admitting against
			// nothing is the arm the paths gate refuses too.
			return fmt.Sprintf("buddy gate could not resolve the worktree root for %s; refusing %s",
				fence.Line(dir, 512), h.ToolName)
		}
		if reason := ghStackSlotHeld(ledger, h, env, sub, where); reason != "" {
			return reason
		}
	}
	return ""
}

// ghStackSlotHeld is the slot check against one ledger: "" when the caller
// owns an open claim covering the slot, else the one-line deny. One read on
// the allow path (ClaimsTouching); the holder's process table is read only
// to word a deny. where names a TARGET repo's root, "" for the home repo.
func ghStackSlotHeld(st *store.Store, h hookInput, env Env, sub, where string) string {
	touching, err := st.ClaimsTouching(ghStackSlot)
	if err != nil {
		return fmt.Sprintf("buddy ledger read failed (%v); refusing %s", err, h.ToolName)
	}
	var holder *store.ClaimInfo
	for i := range touching {
		if !touching[i].Covers {
			continue // a claim UNDER the slot reserves nothing of it
		}
		if h.SessionID != "" && touching[i].Claim.Owner.SessionID == h.SessionID {
			return ""
		}
		if holder == nil {
			holder = &touching[i].Claim
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "buddy gate: this %s command runs `gh stack %s`, which needs this checkout's gh-stack slot: the stack catalog and its lock live in the git common dir every worktree shares, and gh stack's rewriting subcommands move other lanes' branches. ", h.ToolName, sub)
	in := ""
	if where != "" {
		in = fmt.Sprintf(" — in the TARGET repo's buddy ledger (%s)", fence.Line(where, 512))
	}
	if holder == nil {
		fmt.Fprintf(&b, "Nobody holds %s%s. Take it first: buddy claim gh-stack --desc \"<what, and when you release>\" --scope %s", ghStackSlot, in, ghStackSlot)
	} else {
		// permissionDecisionReason is shown to the model on every deny, so it
		// is a context-injection sink: the holder's label, slug and desc are
		// the only peer text here, each fenced, all on this one line
		// (invariant 9).
		fmt.Fprintf(&b, "%s is held by session %s (slug %q: %s; %s)%s. When it is free: buddy claim gh-stack --desc \"<what, and when you release>\" --scope %s",
			ghStackSlot, fence.Line(holder.Owner.Label, 64), fence.Line(holder.Slug, 128), fence.Line(holder.Desc, 512),
			ghSlotHolder(env, st, *holder), in, ghStackSlot)
	}
	if where != "" {
		// A claim needs a live sessions row in THAT ledger (D-088: a second
		// ledger coordinates with nobody), so the hello comes first (the panel).
		fmt.Fprintf(&b, " (in %s, after buddy hello --session %s there)", fence.Line(where, 512), fence.Line(h.SessionID, 128))
	}
	b.WriteString("; one restack at a time, by the skill's \"Restacking a dependent lane\" recipe; a claim an overlapping one refuses is named by the refusal; refusing.")
	return b.String()
}

// ghSlotHolder words the holder's state the way the reader has to act on it
// (D-090's register). An ENDED holder is displaced by the caller's own
// `buddy claim` (orphaning runs first inside Claim's transaction, D-026), so
// no sweep is asked for. A GONE or SILENT holder's claim refuses like a fresh
// one (D-022) and only the operator frees it: `buddy release <slug>` alone
// releases the CALLER's claims ("not yours"), so the line carries the holder's
// `--session`. A live holder is messaged by a name that resolves to IT
// (msgTargetFor: a slug `all` would broadcast, D-090), and a name is printed
// as a command only when it pastes back as printed (the panel): the slug and
// the id are both shell-quoted, and a value the fence would alter (a newline,
// an over-long id) is named instead of printed as a command (the Codex code
// pass: the ledger does not restrict a session id's characters).
func ghSlotHolder(env Env, st *store.Store, c store.ClaimInfo) string {
	now := nowOf(env)
	seen := age(now, c.Owner.LastSeen)
	release := "buddy release <the slug> --session <the holder's id> (neither pastes back as printed)"
	if fence.Line(c.Slug, 128) == c.Slug && fence.Line(c.Owner.SessionID, 128) == c.Owner.SessionID {
		release = "buddy release " + shellQuote(c.Slug) + " --session " + shellQuote(c.Owner.SessionID)
	}
	switch readHolders(env, st).state(now, c.Owner) {
	case holderEnded:
		return "session ENDED — nobody will answer; your own buddy claim displaces it"
	case holderGone:
		return "live but its harness process is GONE, last seen " + seen + " — ask the operator: " + release
	case holderSilent:
		return "live but SILENT for " + seen + " — ask the operator: " + release
	default:
		ask := "buddy msg <the holder> (its name cannot be pasted back as printed)"
		if t := msgTargetFor(st, c.Slug, c.Owner.SessionID); fence.Line(t, 128) == t {
			ask = "buddy msg " + shellQuote(t)
		}
		return "live, last seen " + seen + " — ask the holder: " + ask
	}
}
