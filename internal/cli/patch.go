package cli

// Codex's canonical apply_patch hook carries the entire patch in
// tool_input.command, even when an Edit/Write matcher selected the hook.
// Treating that as a pathless unknown tool made it pause-only. This parser
// extracts every write before adjudication: a move writes its source AND its
// destination, and one conflict refuses the whole tool call.
//
// This is a path adapter, not a patch applier. It validates the local patch
// structure without opening target files or trying to match hunk contents.
// Unsupported envelopes (including remote Environment ID routing) refuse;
// guessing the local cwd for a remote environment would check the wrong ledger.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// shellPatchRoute recognizes Codex's patch engine invoked from a SHELL command
// rather than through the apply_patch tool.
//
// Codex intercepts `apply_patch <<'EOF' ... EOF` (also `applypatch`, and `cd
// <dir> && apply_patch ...`) in exec_command and applies the patch IN PROCESS
// with the same engine as the tool (rust-v0.160.1 exec_command.rs:378,
// apply-patch/src/invocation.rs). Its PreToolUse payload says tool_name "Bash",
// so the gate saw a pause-only shell command and checked no claim at all
// (review F-01, 2026-10-06: a peer-held delete allowed in all three spellings
// while the same patch through the tool was denied). Codex also puts an
// apply_patch alias on the shell's PATH, so the word in any command position
// reaches the engine, not just the forms it intercepts.
//
// The gate does NOT parse such a patch and adjudicate its paths: exec_command's
// workdir is not in the hook payload, so a relative path cannot be placed, and a
// wrong placement is a wrong verdict. It refuses instead, wherever a ledger
// governs, and says to use the tool.
//
// Parse only command structure, never execute or expand it. A regular
// expression confused quoted search patterns with commands and missed a real
// `bash -lc 'apply_patch ...'` route (verification V-04, native Codex 0.160.0).
//
// THE DEFAULT IS INVERTED (audit A-01, 2026-10-07). The recognizer used to
// list the wrappers that execute an operand (env, sudo, xargs, ...) and let
// every other program through. Real bash and zsh then ran apply_patch past the
// gate through each spelling the list lacked: `nice`, `caffeinate -i`,
// `bash -c --`, `bash +x -c`, `eval`, `trap`, a shell fed a here-document or a
// pipe, a brace expansion, zsh's `noglob`/`repeat 1`/`=apply_patch`, `coproc`,
// `find -exec`. Now an UNKNOWN program's every operand is a possible command
// position. Only an established command position (the first word, or the one a
// known wrapper's option grammar lands on) may name a DATA program, whose
// operands are data; a data word among an unknown program's operands proves
// nothing (`script -q echo apply_patch` runs apply_patch, Codex design review).
//
// UNCERTAIN TEXT REFUSES WHERE A LEDGER GOVERNS. Script text produced at run
// time (`printf 'apply_%s' patch | bash`, `bash -c "$(...)"`, `eval "$x"`,
// `bash <(...)`) and a script the bash-mode parser rejects (zsh's anonymous
// functions; mvdan.cc/sh has no zsh mode) cannot be ruled out by any static
// reading, and enumerating their spellings is the failure above again. Measured
// on this machine's transcripts (2026-10-07, counts only): 31 of 109,729 unique
// agent shell commands did not parse and 50 fed a shell or eval produced text,
// across every repo, governed or not. Refusing those where a ledger governs is
// the cheap side of that trade.
//
// CODEX ONLY (D-087, narrowing D-073). The "cannot rule out" arm, run-time
// text and a script the parser rejects, guards Codex's in-process engine, and a
// Claude Code Bash call has no such engine and no apply_patch tool to be sent
// to. On one fleet of 144 Claude sessions it fired about 10 times in about 9
// sessions (`eval`, `sh -c "$c"`, zsh's `${=p}`, a pipe into `ssh 'sh -s'`),
// each a rewrite, and writing the same probe to a script file passed: it
// protected nothing there. So under any harness but Codex (classifyHook's
// verdict) the walker records no uncertainty and keeps reading, and only a
// LITERAL apply_patch/applypatch at a command position refuses, as it does
// everywhere. Exhausting a work bound still refuses everywhere: that is the
// gate having stopped reading before it could rule out the literal, not a
// verdict about the text. A Claude call's unparseable command is therefore
// not searched for the literal; that is the same cooperative residue as a
// script file, and the operator's decision (2026-10-20).
//
// What remains cooperative, and is documented as such (USAGE, D-073): dynamic
// command NAMES ($x, ${U:-apply_patch}); script FILES (bash file.sh, source
// file, bash < file); aliases and functions defined outside the command;
// programs that run text in another language (awk system(), sed e, python -c,
// make recipes, git aliases and config); command names read from stdin (xargs
// with no command); CDPATH, OLDPWD and the pushd stack; `~user`; and a workdir
// passed outside the command.
func shellPatchCommand(h hookInput) bool { return shellPatchRoute(h).refuses() }

// shellRoute is the recognizer's verdict on one shell command.
type shellRoute struct {
	found     bool   // apply_patch/applypatch reaches a command position
	uncertain string // why the command cannot be ruled out; "" when it can
}

func (r shellRoute) refuses() bool { return r.found || r.uncertain != "" }

func shellPatchRoute(h hookInput) shellRoute {
	if h.ToolName == "apply_patch" || h.ToolInput.Command == "" {
		return shellRoute{}
	}
	w := &shellWalker{literalOnly: h.Harness != "codex"}
	w.analyze(h.ToolInput.Command, 0, true, nil)
	return w.route
}

// Work bounds, shared by every script one hook analyses. A per-script bound
// alone let speculative recursion and brace expansion multiply the work of one
// command (Codex design review, 2026-10-07). Exhausting any of them is an
// uncertain route: it refuses where a ledger governs and never establishes
// jurisdiction where none does.
const (
	shellMaxDepth       = 8
	shellMaxScript      = 128 << 10 // one script's bytes
	shellMaxParsedBytes = 512 << 10 // every script parsed for one hook
	shellMaxParses      = 64
	shellMaxWordFields  = 256 // brace fields from one word, counted BEFORE expanding
	shellMaxFields      = 1024
	// Lexical recovery reads text the parser did not: bounded separately,
	// above the 16 MiB hook cap so one oversize command is still read once.
	shellMaxLexedBytes = 32 << 20
)

var (
	patchCommandNames = map[string]bool{"apply_patch": true, "applypatch": true}
	// Every shell whose -c operand or stdin is a script. csh, tcsh and fish
	// run `-c` too (audit A-01); listing a shell the box lacks costs nothing.
	scriptShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true,
		"oksh": true, "yash": true, "posh": true, "csh": true, "tcsh": true, "fish": true}
	// Programs whose operands are data when they sit at an ESTABLISHED command
	// position. Each one's ordinary use executes no operand. Cut after review:
	// man (-P runs a pager), sort (--compress-program runs one), and every
	// program that runs another language's text (awk, sed, python): those stay
	// unknown programs, so an operand spelled exactly apply_patch still refuses.
	// git is not here: it is read by gitRoute, because bisect run, submodule
	// foreach and rebase --exec run their operands.
	dataCommands = map[string]bool{"echo": true, "printf": true, "print": true, "grep": true, "egrep": true,
		"fgrep": true, "rg": true, "ag": true, "ack": true, "gh": true, "which": true, "type": true, "whence": true,
		"where": true, "hash": true, "cat": true, "head": true, "tail": true, "wc": true, "ls": true, "stat": true,
		"file": true, "tee": true, "make": true, "diff": true, "cmp": true, "uniq": true, "cut": true, "tr": true,
		"jq": true, "printenv": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
		"mkdir": true, "touch": true, "rm": true, "cp": true, "mv": true, "ln": true, "chmod": true, "du": true,
		"shasum": true, "test": true, "[": true, "true": true, "false": true, ":": true, "pwd": true, "buddy": true}
	// The one option of a data program that names a program it runs:
	// `rg --pre=apply_patch` (Codex fifth code pass).
	dataExecOption = map[string]string{"rg": "--pre"}
	// Programs that run their command operand, with the options that take a
	// value, so the scan lands on the established command position. A program
	// missing from this table is an unknown program: every operand is scanned,
	// which over-refuses and never under-refuses.
	wrapperOptions = map[string]map[string]bool{
		"env":        set("-u", "--unset", "-C", "--chdir", "-P", "-S", "--split-string"),
		"sudo":       set("-u", "--user", "-g", "--group", "-h", "--host", "-p", "--prompt", "-C", "--close-from", "-D", "--chdir", "-R", "--chroot", "-T", "--command-timeout", "-U", "--other-user", "-r", "--role", "-t", "--type"),
		"doas":       set("-u", "-C"),
		"exec":       set("-a"),
		"command":    set(),
		"builtin":    set(),
		"nohup":      set(),
		"time":       set("-f", "--format", "-o", "--output"),
		"nice":       set("-n", "--adjustment"),
		"xargs":      set("-I", "-J", "-L", "-n", "-P", "-s", "-E", "-R", "-S", "-d", "-a", "--arg-file", "--delimiter", "--max-args", "--max-procs", "--max-lines", "--max-chars", "--replace", "--eof"),
		"stdbuf":     set("-i", "-o", "-e", "--input", "--output", "--error"),
		"caffeinate": set("-t", "-w"),
		"timeout":    set("-s", "--signal", "-k", "--kill-after"),
		"noglob":     set(),
		"nocorrect":  set(),
		"setsid":     set(),
		"unbuffer":   set(),
		"repeat":     set(),
		"arch":       set("-arch", "-e", "-d"),
		// util-linux script's log-file and format options take a value too
		// (-O/-I/-B files, -o size, -E when, -m format); without them a no-command
		// `script -O a -I b <<'SH'` desyncs the lead operand and the stdin arm
		// below never fires (D-106, 2026-10-12 re-review follow-up). NOT RUN here
		// (macOS script is BSD and lacks them); listing a value a box lacks only
		// over-reads an option that never appears.
		"script": set("-t", "-T", "--log-timing", "-c", "--command", "-O", "--log-out", "-I", "--log-in",
			"-B", "--log-io", "-o", "--output-limit", "-E", "--echo", "-m", "--logging-format"),
	}
	// Operands a wrapper takes before its command: timeout's duration,
	// zsh repeat's count, script's transcript file.
	wrapperLeadOperands = map[string]int{"timeout": 1, "repeat": 1, "script": 1}
)

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

type shellWalker struct {
	ctx    *shellPatchContext // nil: recognition only, so stop at the first refusal
	homes  []string           // jurisdiction: candidate values of HOME for `~`
	parses int
	parsed int
	fields int
	// literalOnly: not a Codex call, so text that merely cannot be ruled out
	// is not a refusal (D-087); only a literal command word or an exhausted
	// bound is.
	literalOnly bool
	// speculative > 0 while reading an operand that merely looks like shell
	// text. There only an established command position counts: scanning an
	// unknown program's operands would refuse prose such as
	// `curl -d "please run apply_patch now"`.
	speculative int
	lexed       int
	funcs       map[string]bool // functions the script being walked defines
	route       shellRoute
}

// uncertainty records the first reason a command cannot be ruled out, for a
// Codex call only (D-087). Under any other harness it records nothing, so
// done() stays false and the walk goes on looking for a literal command word:
// `eval "$x"; apply_patch < p` still refuses there.
func (w *shellWalker) uncertainty(why string) {
	if !w.literalOnly {
		w.cannotRuleOut(why)
	}
}

func (w *shellWalker) cannotRuleOut(why string) {
	if w.route.uncertain == "" {
		w.route.uncertain = why
	}
}

// exhausted is an uncertainty that also says jurisdiction discovery stopped
// early, so the ungoverned arm can name it. It refuses under every harness:
// the walk stopped before it could rule out a literal command word.
func (w *shellWalker) exhausted(why string) {
	w.cannotRuleOut(why)
	if w.ctx != nil && w.ctx.err == nil {
		w.ctx.err = errors.New(why)
	}
}

func (w *shellWalker) done() bool { return w.ctx == nil && w.route.refuses() }

// analyze reads one script. A CERTAIN script is known to be shell text (the
// command itself, a -c operand, eval's text, a here-document fed to a shell):
// failing to parse it is uncertainty. A SPECULATIVE one is an unknown
// program's operand that might be shell text (su -c, watch, flock -c): failing
// to parse it means it is not shell text we can read, which stays cooperative.
//
// in is the stdin the script's top-level statements inherit: a nested script
// reads the stdin of the command that runs it, so `bash -c 'bash -s' <<'SH'`
// feeds the inner shell the here-document (Codex sixth code pass).
func (w *shellWalker) analyze(script string, depth int, certain bool, in fdTable) {
	if w.done() {
		return
	}
	if depth >= shellMaxDepth || len(script) > shellMaxScript {
		w.exhausted("shell patch routing exceeds its script budget")
		w.unparsed(script)
		return
	}
	w.parses++
	w.parsed += len(script)
	if w.parses > shellMaxParses || w.parsed > shellMaxParsedBytes {
		w.exhausted("shell patch routing exceeds its analysis budget")
		w.unparsed(script)
		return
	}
	if w.ctx != nil {
		w.ctx.scripts = append(w.ctx.scripts, script)
	}
	if !certain {
		w.speculative++
		defer func() { w.speculative-- }()
	}
	tree, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		if certain {
			w.uncertainty("it does not parse as bash (" + err.Error() + ")")
			w.lexicalDirectories(script)
		}
		return
	}
	w.collectHomes(tree)
	// Functions this script defines. A call to one reads its stdin the way
	// the shells in its body do: `run() { bash -s; }; run <<'SH'` (Codex
	// fourth code pass). Functions do not cross into a nested shell.
	outer := w.funcs
	w.funcs = make(map[string]bool)
	defer func() { w.funcs = outer }()
	syntax.Walk(tree, func(node syntax.Node) bool {
		if fn, ok := node.(*syntax.FuncDecl); ok && fn.Name != nil {
			w.funcs[store.Fold(fn.Name.Value)] = true
		}
		return true
	})
	stmtOf := make(map[*syntax.CallExpr]*syntax.Stmt)
	// fds maps a statement to its open input descriptors. It is filled top
	// down: syntax.Walk visits a statement before the statements inside it,
	// so a parent's descriptors are known when its children are reached.
	fds := make(map[*syntax.Stmt]fdTable)
	if len(in) > 0 {
		for _, st := range tree.Stmts {
			fds[st] = in
		}
	}
	syntax.Walk(tree, func(node syntax.Node) bool {
		if w.done() {
			return false
		}
		switch x := node.(type) {
		case *syntax.Stmt:
			if call, ok := x.Cmd.(*syntax.CallExpr); ok {
				stmtOf[call] = x
			}
			// A compound statement's stdin (its own redirection, a pipe, or
			// what it inherited) reaches every statement inside it:
			// `{ bash; } <<'SH'`, `... | { true && bash; }` (Codex code review).
			own, overflow := effectiveFDs(x, fds[x])
			if overflow {
				// Uncertain now; an empty table keeps nested statements from
				// copying and growing it (Codex fourth code pass: 6,000 nested
				// blocks allocated 1.2 GB in discovery).
				w.exhausted("shell patch routing exceeds its descriptor budget")
				own = fdTable{}
			}
			fds[x] = own
			if b, ok := x.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) {
				// The parser nests pipelines on the LEFT ((a | b) | c), so Y
				// is always the one statement reading this pipe.
				fds[b.Y] = own.with("0", stdinSource{piped: true})
			}
			for _, inner := range childStmts(x.Cmd) {
				if _, set := fds[inner]; !set {
					fds[inner] = own
				}
			}
		case *syntax.CoprocClause:
			// The parser reads `coproc apply_patch < p` as a coprocess NAMED
			// apply_patch with an empty command. bash and zsh run it.
			if x.Name != nil {
				words := []*syntax.Word{x.Name}
				if call, ok := x.Stmt.Cmd.(*syntax.CallExpr); ok {
					words = append(words, call.Args...)
				}
				own, _ := effectiveFDs(x.Stmt, fds[x.Stmt]) // the statement's own visit reports overflow
				w.command(w.words(words), callSite{fds: own}, depth)
			}
		case *syntax.CallExpr:
			if len(x.Args) > 0 {
				if first := decodeWord(x.Args[0]); first.known && dataCommands[commandName(first)] &&
					dataExecOption[commandName(first)] == "" && !w.funcs[commandName(first)] {
					// A data program's operands decide nothing, so they are not
					// brace-expanded: `touch x{1..500}` must not spend the field
					// budget and refuse. (A first word WITH braces is not a
					// literal data name, so `{echo,apply_patch}` still expands.)
					return true
				}
			}
			w.command(w.words(x.Args), callSite{fds: fds[stmtOf[x]]}, depth)
		}
		return true
	})
}

// childStmts are the statements a compound command runs with its own stdin.
// A function body is not one of them: it runs where it is called.
func childStmts(cmd syntax.Command) []*syntax.Stmt {
	switch c := cmd.(type) {
	case *syntax.BinaryCmd:
		return []*syntax.Stmt{c.X, c.Y}
	case *syntax.TimeClause:
		if c.Stmt != nil {
			return []*syntax.Stmt{c.Stmt}
		}
	case *syntax.Block:
		return c.Stmts
	case *syntax.Subshell:
		return c.Stmts
	case *syntax.WhileClause:
		return append(append([]*syntax.Stmt(nil), c.Cond...), c.Do...)
	case *syntax.ForClause:
		return c.Do
	case *syntax.IfClause:
		var all []*syntax.Stmt
		for ; c != nil; c = c.Else {
			all = append(all, c.Cond...)
			all = append(all, c.Then...)
		}
		return all
	case *syntax.CaseClause:
		var all []*syntax.Stmt
		for _, item := range c.Items {
			all = append(all, item.Stmts...)
		}
		return all
	}
	return nil
}

// callSite carries the descriptors a command inherits; fds["0"] is its stdin.
// The whole table, not just stdin, reaches a nested script: `bash -c 'bash
// <&3' 3<<'SH'` (Codex seventh code pass).
type callSite struct {
	fds fdTable
}

// stdinSource is what fd 0 reads: an input redirection (after resolving
// `0<&N` against the same statement), a pipe, or a descriptor this command
// does not show, which stays cooperative like a script file.
type stdinSource struct {
	redirect *syntax.Redirect
	piped    bool
}

// fdTable is a statement's open input descriptors, as far as the command
// shows them. Every descriptor is inherited, not just 0: `{ bash 0<&3; }
// 3<<'SH'` reads the block's fd 3 (Codex follow-up review).
type fdTable map[string]stdinSource

func (t fdTable) with(fd string, src stdinSource) fdTable {
	out := make(fdTable, len(t)+1)
	for k, v := range t {
		out[k] = v
	}
	out[fd] = src
	return out
}

// shellMaxFDs bounds the descriptors one statement's table tracks. Past it
// the route is uncertain: dropping one could hide `3<<'SH' ... 0<&3`.
const shellMaxFDs = 64

// effectiveFDs applies a statement's own redirections, in order, over what it
// inherited. `bash 3<<'SH' 0<&3` reads the here-document (Codex code review);
// `0<&3-` MOVES fd 3 (Codex third code pass); a duplicate of a
// descriptor the command never showed is unseen. The table is copied once
// per statement, never once per redirection: 8,000 redirections used to cost
// about 32 million entry copies (Codex third code pass). It reports overflow.
func effectiveFDs(stmt *syntax.Stmt, inherited fdTable) (fdTable, bool) {
	if len(stmt.Redirs) == 0 {
		return inherited, false
	}
	own := make(fdTable, len(inherited)+1)
	for k, v := range inherited {
		own[k] = v
	}
	for _, r := range stmt.Redirs {
		fd := "0"
		if r.N != nil {
			fd = r.N.Value
		}
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc, syntax.RdrIn, syntax.RdrInOut:
			own[fd] = stdinSource{redirect: r}
		case syntax.DplIn:
			// N<&M- moves M; the model leaves M open too, which can only
			// over-refuse (closing it was unobservable within one statement).
			own[fd] = own[strings.TrimSuffix(r.Word.Lit(), "-")] // absent (inherited from outside, or closed): unseen
		default:
			continue
		}
		if len(own) > shellMaxFDs {
			return own, true
		}
	}
	return own, false
}

// shellWord is one field of a command, decoded only when it is a fixed literal.
type shellWord struct {
	text      string
	known     bool // a fixed literal: no expansion of any kind
	tilde     bool // begins with an unquoted ~
	equals    bool // begins with an unquoted = (zsh equals expansion)
	procSubst bool // the whole word is a process substitution
	// overBudget: brace expansion was not attempted. Uncertain only if the
	// scan reaches it; after a data program it decides nothing
	// (`env touch x{1..500}`, Codex code review).
	overBudget bool
}

func decodeWord(word *syntax.Word) shellWord {
	var sw shellWord
	if len(word.Parts) == 1 {
		if _, ok := word.Parts[0].(*syntax.ProcSubst); ok {
			sw.procSubst = true
			return sw
		}
	}
	if len(word.Parts) > 0 {
		if lit, ok := word.Parts[0].(*syntax.Lit); ok {
			sw.tilde = strings.HasPrefix(lit.Value, "~")
			sw.equals = strings.HasPrefix(lit.Value, "=")
		}
	}
	sw.text, sw.known = shellLiteral(word.Parts, false)
	return sw
}

// words decodes a command's arguments, brace-expanding each unquoted word the
// way bash does: `{apply_patch,} < p` runs apply_patch (audit A-01). Fields are
// COUNTED before anything is expanded, so `{1..99999999}` costs a count, not
// an allocation.
func (w *shellWalker) words(args []*syntax.Word) []shellWord {
	out := make([]shellWord, 0, len(args))
	for _, arg := range args {
		split := &syntax.Word{Parts: append([]syntax.WordPart(nil), arg.Parts...)}
		if !syntax.SplitBraces(split) {
			out = append(out, decodeWord(arg))
			continue
		}
		n := braceFields(split.Parts)
		w.fields += n
		if n > shellMaxWordFields || w.fields > shellMaxFields {
			out = append(out, shellWord{overBudget: true})
			continue
		}
		for _, field := range expand.Braces(split) {
			if field.Lit() == "" && len(field.Parts) <= 1 {
				continue // bash drops an empty unquoted field: {apply_patch,}
			}
			out = append(out, decodeWord(field))
		}
	}
	return out
}

// braceFields counts the fields expand.Braces would produce, saturating
// rather than overflowing.
func braceFields(parts []syntax.WordPart) int {
	const ceiling = 1 << 20
	total := 1
	for _, part := range parts {
		br, ok := part.(*syntax.BraceExp)
		if !ok {
			continue
		}
		n := 0
		if br.Sequence {
			from, to := br.Elems[0].Lit(), br.Elems[1].Lit()
			a, errA := strconv.ParseInt(from, 10, 64)
			b, errB := strconv.ParseInt(to, 10, 64)
			if errA != nil || errB != nil {
				if from == "" || to == "" {
					return ceiling
				}
				a, b = int64(from[0]), int64(to[0])
			}
			// Saturate before subtracting: x{0..9223372036854775807} used to
			// wrap to one field (Codex code review).
			const span = 1 << 40
			if a > span || a < -span || b > span || b < -span {
				return ceiling
			}
			n = int(a - b)
			if n < 0 {
				n = -n
			}
			n++ // a step only shortens the sequence; the count stays an upper bound
		} else {
			for _, elem := range br.Elems {
				n += braceFields(elem.Parts)
			}
		}
		if total > ceiling/max(n, 1) {
			return ceiling
		}
		total *= max(n, 1)
	}
	return total
}

// commandName folds a command word for comparison (invariant 13), stripping
// zsh's equals expansion. A trailing slash names a directory or a regex
// (`awk '/apply_patch/'`), never a program.
func commandName(word shellWord) string {
	text := word.text
	if word.equals {
		text = strings.TrimPrefix(text, "=")
	}
	if text == "" || strings.HasSuffix(text, "/") {
		return ""
	}
	return store.Fold(filepath.Base(text))
}

// isURL: scheme://, with a scheme of letters, digits, + - . after a letter.
func isURL(s string) bool {
	scheme, _, ok := strings.Cut(s, "://")
	if !ok || scheme == "" || !unicode.IsLetter(rune(scheme[0])) {
		return false
	}
	for _, r := range scheme {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("+-.", r))) {
			return false
		}
	}
	return true
}

// looksLikeScript: an operand worth parsing speculatively as shell text.
func looksLikeScript(s string) bool { return strings.ContainsAny(s, " \t\n;&|<>()`$") }

// command scans one simple command from its established command position.
func (w *shellWalker) command(words []shellWord, site callSite, depth int) {
	for i := 0; i < len(words); i++ {
		if w.done() {
			return
		}
		word := words[i]
		if word.overBudget {
			w.exhausted("shell patch routing exceeds its brace expansion budget")
			return
		}
		if !word.known {
			// A dynamic command name stays cooperative. Keep looking past it:
			// it may be a wrapper whose operand is the command.
			continue
		}
		name := commandName(word)
		rest := words[i+1:]
		switch {
		case patchCommandNames[name]:
			w.route.found = true
			return
		case w.funcs[name]:
			// A function defined in this script shadows every other name,
			// data programs included: `cat() { bash -s; }; cat <<'SH'` (Codex
			// fifth code pass). Its body was walked where it was defined;
			// this call adds its stdin and its operands ("$@" runs them).
			w.stdinScript(site, depth)
			if w.speculative == 0 {
				w.operands(rest, site, depth)
			}
			return
		case scriptShells[name]:
			w.shell(name, rest, site, depth)
			return
		case name == "eval":
			w.scriptOf(rest, depth, "eval runs text built at run time", site.fds)
			return
		case name == "trap":
			for len(rest) > 0 && rest[0].known && strings.HasPrefix(rest[0].text, "-") && rest[0].text != "-" {
				rest = rest[1:]
			}
			if len(rest) > 0 && rest[0].overBudget {
				// `trap {rm,-rf,/;,apply_patch} EXIT` — an over-budget brace
				// expands in place into the ACTION (and its SIGNAL), so the
				// `len(rest) > 1` test below never sees the two operands it
				// needs (D-106 follow-up, FULL-review cross-check).
				w.exhausted("shell patch routing exceeds its brace expansion budget")
				return
			}
			if len(rest) > 1 { // trap ACTION SIGNAL...; a lone operand only lists or resets
				w.scriptOf(rest[:1], depth, "trap runs text built at run time", site.fds)
			}
			return
		case name == "source" || name == ".":
			w.scriptFile(rest, site, depth)
			return
		case name == "cd" || name == "pushd" || name == "chdir":
			w.changeDirectory(name, rest)
			return
		case name == "find":
			w.findActions(rest, site, depth)
			return
		case name == "git":
			w.gitRoute(rest, site, depth)
			return
		case name == "command" && lookupOnly(rest):
			return // command -v / -V reports where a name resolves; it runs nothing
		case dataCommands[name]:
			if opt := dataExecOption[name]; opt != "" {
				w.dataExec(opt, rest, site, depth)
			}
			return
		}
		if skip, ok := w.wrapper(name, rest, site, depth); ok {
			i += skip
			continue
		}
		if w.speculative == 0 {
			w.operands(rest, site, depth)
		}
		return
	}
}

// dataExec reads a data program's one program-valued option as a command
// position: the preprocessor rg runs on each file.
func (w *shellWalker) dataExec(opt string, rest []shellWord, site callSite, depth int) {
	for j, word := range rest {
		if word.overBudget {
			// The unexpanded word may hold the option: rg {--pre=x,-e{1..500}}
			// (Codex sixth code pass).
			w.exhausted("shell patch routing exceeds its brace expansion budget")
			return
		}
		if !word.known {
			continue
		}
		if value, ok := strings.CutPrefix(word.text, opt+"="); ok {
			w.command([]shellWord{{text: value, known: true}}, site, depth)
		} else if word.text == opt && j+1 < len(rest) {
			w.command(rest[j+1:j+2], site, depth)
		}
	}
}

// operands scans an UNKNOWN program's operands. Every one is a possible
// command position, and none of them may stop the scan.
func (w *shellWalker) operands(rest []shellWord, site callSite, depth int) {
	for j, word := range rest {
		if w.done() {
			return
		}
		if word.overBudget {
			w.exhausted("shell patch routing exceeds its brace expansion budget")
			return
		}
		if !word.known {
			continue
		}
		name := commandName(word)
		if isURL(word.text) {
			// An operand that is a URL is a resource, not a program: `curl -I
			// https://host/apply_patch` (Codex code review). At an ESTABLISHED
			// command position the same spelling is a relative path and still
			// counts: `route://apply_patch` (Codex follow-up review).
			name = ""
		}
		if opt, value, ok := strings.Cut(word.text, "="); ok && strings.HasPrefix(opt, "-") {
			// --compress-program=apply_patch names the program it runs.
			name = commandName(shellWord{text: value, known: true})
		}
		if patchCommandNames[name] {
			w.route.found = true
			return
		}
		if scriptShells[name] {
			// Maybe a shell, maybe an operand spelled like one: `flock sh
			// apply_patch` runs apply_patch with lock file sh. Read it as a
			// shell and keep scanning.
			w.shell(name, rest[j+1:], site, depth)
		}
		if looksLikeScript(word.text) {
			w.analyze(word.text, depth+1, false, site.fds)
		}
	}
}

func lookupOnly(rest []shellWord) bool {
	for _, word := range rest {
		if !word.known || !strings.HasPrefix(word.text, "-") || word.text == "-" || strings.HasPrefix(word.text, "--") {
			return false
		}
		if strings.ContainsAny(word.text[1:], "vV") {
			return true
		}
	}
	return false
}

// wrapper reads a known wrapper's options and reports how many words they
// span, so the caller resumes at the wrapper's command position.
func (w *shellWalker) wrapper(name string, rest []shellWord, site callSite, depth int) (int, bool) {
	values, ok := wrapperOptions[name]
	if !ok {
		return 0, false
	}
	lead := wrapperLeadOperands[name]
	shellMode := false // sudo -s/-i, doas -s: with no command, a shell reads stdin
	playback := false  // script -p replays a recording: it spawns NO shell
	scriptCmd := false // script -c CMD: a command WAS supplied, so stdin is not a shell
	j := 0
	for j < len(rest) && !w.done() {
		word := rest[j]
		if !word.known {
			break
		}
		t := word.text
		if (name == "env" || name == "sudo" || name == "doas") && !strings.HasPrefix(t, "-") && strings.Contains(t, "=") {
			j++ // VAR=value; a HOME assignment was collected for jurisdiction
			continue
		}
		if t == "--" {
			j++
			// After `--` everything is an operand. Consume the wrapper's lead
			// operands (script's transcript file) so a no-command `script -q --
			// /dev/null <<'SH'` still reaches the stdin arm below; a further
			// operand is the command and breaks the scan for command() to read
			// (D-106, 2026-10-12 re-review follow-up).
			for lead > 0 && j < len(rest) {
				if rest[j].overBudget {
					// An over-budget brace as a lead operand expands in place
					// into the wrapper's argv, options and command included:
					// `timeout -- {1,apply_patch,x{1..300}}` becomes
					// `timeout 1 apply_patch ...` (D-106 follow-up, FULL-review
					// codex finding 8). Refuse rather than swallow it.
					w.exhausted("shell patch routing exceeds its brace expansion budget")
					return len(rest), true
				}
				lead--
				j++
			}
			break
		}
		if !strings.HasPrefix(t, "-") || t == "-" {
			if lead > 0 {
				lead--
				j++
				continue
			}
			break
		}
		// Only a long option carries an inline value after '='. A short
		// cluster's value is the rest of the word, '=' included:
		// env -S'X=1 apply_patch' (Codex sixth code pass).
		if (name == "sudo" || name == "doas") && (t == "--shell" || t == "--login" ||
			!strings.HasPrefix(t, "--") && strings.ContainsAny(t[1:], "si")) {
			shellMode = true
		}
		if name == "script" && (t == "-p" || t == "--replay") {
			// BSD `script -p` PLAYS BACK a recording and spawns no shell, so it
			// never reads the inherited stdin as a script; firing the stdin arm
			// for it is a false denial (`script -p f <<'SH'…` runs nothing). Only
			// the bare `-p`/`--replay` token: scanning the cluster for 'p'/'T'
			// read the 'p' inside an attached filename (`-O/tmp/x.log`) and the
			// util-linux `-T` timing option (which DOES run a shell) as playback,
			// both false allows (D-106 follow-up, FULL-review codex findings 1,2).
			// A clustered `-qp` is not matched and over-refuses, the safe side.
			playback = true
		}
		opt, value, inline := t, "", false
		if strings.HasPrefix(t, "--") {
			opt, value, inline = strings.Cut(t, "=")
		} else if len(t) > 2 && !values[t] {
			// A cluster of short options: the first that takes a value takes
			// the rest of the word (-C/dir, -S'text') or the next word, so
			// -vC DIR is -v -C DIR (Codex fourth code pass).
			for k := 1; k < len(t); k++ {
				if o := "-" + t[k:k+1]; values[o] {
					opt = o
					if k+1 < len(t) {
						value, inline = t[k+1:], true
					}
					break
				}
			}
		}
		next := func() (shellWord, bool) {
			if inline {
				// A shell expands ~ only at the start of a word, so
				// -C~/x and --chdir=~/x name a directory called ~.
				return shellWord{text: value, known: true}, true
			}
			if j+1 < len(rest) {
				return rest[j+1], true
			}
			return shellWord{}, false
		}
		switch {
		case name == "env" && (opt == "-S" || opt == "--split-string"),
			name == "script" && (opt == "-c" || opt == "--command"):
			// The value IS the command line (env -S splits it, GNU script
			// runs it through a shell).
			if name == "script" {
				// A command WAS supplied, so script does not read stdin as a
				// shell even if the value is dynamic and the scan then continues
				// to the end of argv (`script -c "$cmd" /dev/null <<'SH'` runs
				// $cmd, not the heredoc: D-106 follow-up, FULL-review codex
				// finding 5, a false denial the stdin arm would otherwise make).
				scriptCmd = true
			}
			v, ok := next()
			if !ok {
				return len(rest), true
			}
			if v.overBudget {
				// A brace word too large to expand sits in the -S value: a shell
				// expands it in place into env's own argv, options and command
				// included, before the gate ever sees the pieces (finding E,
				// 2026-10-12 re-review). Refuse rather than skip it unchecked.
				w.exhausted("shell patch routing exceeds its brace expansion budget")
				return len(rest), true
			}
			if !v.known {
				w.uncertainty(name + " " + opt + " runs text built at run time")
				// env still PROCESSES the options after a dynamic -S value:
				// `env -S "$x" -C /governed apply_patch` MAY change directory
				// (an empty or option-only $x leaves -C in force), so -C
				// must still be discovered and the literal after it still found
				// (finding A, 2026-10-12 re-review). The uncertainty already
				// refuses a Codex call where a ledger governs; under Claude the
				// value is no refusal, so the literal after it is read anyway
				// (D-087). script's -c value is its whole command line, with no
				// further directory-changing argv, so under Codex it returns.
				if name == "env" || w.literalOnly {
					j += 2
					continue
				}
				return len(rest), true
			}
			if name == "env" && strings.ContainsAny(v.text, `\$#`) {
				// env -S has its own escapes and ${VAR} expansion, and `\c`
				// ends the string: `env -S 'apply_patch\c'` runs apply_patch
				// (Codex fourth code pass). They are not modelled.
				// A # comment ends the split text but not argv, so joining
				// the rest after it would hide a command (Codex eighth pass).
				w.uncertainty("env -S text with escapes, expansions or comments is not modelled")
				// A directory change INSIDE the -S text is still discovered:
				// `env -S '-C /governed #' apply_patch` splits to `-C /governed`
				// and a `#` comment, and env changes directory before running
				// the trailing argv (finding B, 2026-10-12 re-review). The
				// lexical scan honours quotes but NOT env -S's own escapes, so it
				// is best-effort BOTH ways: it may add a directory a real env
				// would skip (a `#` that begins the split string), and it may
				// miss one env's `\_` separator or `\c` truncation would change
				// (FULL-review cross-check: so this is NOT "only over-refuses").
				// The uncertainty recorded above still refuses a Codex call where
				// a ledger governs. Recognition ignores the dirs (ctx == nil).
				w.lexicalDirectories(v.text)
				// Keep reading env's argv: an option after the -S text still
				// changes directory (Codex ninth code pass).
				j++
				if !inline {
					j++
				}
				continue
			}
			var text strings.Builder
			if name == "env" {
				// The split words are env's own argv, options included:
				// `env -S '-C /governed apply_patch'` changes directory first
				// (Codex seventh code pass). Read them as `env <split words>`.
				text.WriteString("env ")
			}
			text.WriteString(v.text)
			if name == "env" {
				// env -S's split words are followed by the rest of argv:
				// `env -S env apply_patch` runs `env apply_patch`.
				tail := j + 2
				if inline {
					tail = j + 1
				}
				// One builder, stopped just past the script budget, so a long
				// tail costs linear work (Codex follow-up review: repeated +=
				// copied about 4 GB for a 120 KB command).
				for _, word := range rest[min(tail, len(rest)):] {
					if text.Len() > shellMaxScript {
						break
					}
					// An over-budget brace in the tail is masked as "$_": whether it
					// matters depends on what the split value runs, which with quotes,
					// leading env options and wrapper prefixes (`env -S 'env echo'`,
					// `env -S '-i echo'`) cannot be read statically without false
					// denials of common data forms (re-verify r2 finding 5). The
					// crafted `env -S env {…apply_patch…}` / `env -S '-u' {…}` /
					// assignment forms are recorded OPEN in D-106 (brace-budget
					// bypasses, free under Claude's allow arm).
					if word.known {
						text.WriteString(" " + shellQuote(word.text))
					} else {
						text.WriteString(` "$_"`)
					}
				}
			}
			w.analyze(text.String(), depth+1, true, site.fds)
			return len(rest), true
		case name == "env" && (opt == "-C" || opt == "--chdir"), name == "sudo" && (opt == "-D" || opt == "--chdir"):
			if v, ok := next(); ok {
				w.directory(v)
			}
		}
		j++
		if values[opt] && !inline {
			if j < len(rest) && rest[j].overBudget {
				// A brace word too large to expand sits where this option's
				// value goes, and a shell expands it in place into more options
				// and a command: `env -u {X,-S,apply_patch,x{1..300}}` becomes
				// `env -u X -S apply_patch ...` (finding E, 2026-10-12
				// re-review). Refuse rather than skip it unchecked.
				w.exhausted("shell patch routing exceeds its brace expansion budget")
				return len(rest), true
			}
			j++
		}
	}
	if j >= len(rest) && (shellMode || (name == "script" && !playback && !scriptCmd)) {
		// A wrapper that reaches the end of its argv with no command runs a
		// shell that reads the inherited stdin: `sudo -s <<'SH'` (Codex ninth
		// code pass), and `script -q /dev/null <<'SH' ... apply_patch ... SH`,
		// where BSD and util-linux script with a transcript file (or none) and
		// NO command spawn $SHELL on the heredoc (finding C, 2026-10-12
		// re-review). A command operand or -c breaks the scan earlier, so
		// reaching the end here means there is none; playback (-p) spawns no
		// shell, so it is excluded.
		w.stdinScript(site, depth)
	}
	return j, true
}

// fishValueLetters are fish's short options that take a value, per its
// documented options: -c and -C (commands), -d (debug categories), -o (debug
// output file), -f (features), -p (profile file), and fish 3's -D (debug stack
// frames). fishValueLong are the long spellings shell() does not otherwise
// read. Re-verify r4 finding 1 (D-106).
const fishValueLetters = "cCdofpD"

var fishValueLong = map[string]bool{"--debug": true, "--debug-output": true, "--features": true,
	"--profile": true, "--profile-startup": true, "--debug-stack-frames": true}

// shell reads a shell's options: with -c (fish: --command) its first operand
// is a certain script; with -s or no operand it reads its script from stdin;
// otherwise its first operand is a script FILE, which stays cooperative.
// Options are read as clusters of either sign, so `bash -c --`, `bash +x -c`
// and `bash +o posix -c` all find their script (audit A-01).
func (w *shellWalker) shell(name string, args []shellWord, site callSite, depth int) {
	command, stdin, noexec := false, false, false
	var operands []shellWord
	var fishCmds []shellWord // fish runs EACH -c/-C command (it repeats); analysed at the end unless -n
	fishSawCommand := false  // fish got a -c/--command: it is in command mode, never reading stdin
	fishUsage := false       // a fish option ended the line without its value: fish exits, reading no script
	options := true
	for j := 0; j < len(args); j++ {
		a := args[j]
		if a.overBudget {
			// `bash {-c,apply_patch,x{1..500}}`: the unexpanded word may hold
			// the -c and the script (Codex follow-up review).
			w.exhausted("shell patch routing exceeds its brace expansion budget")
			return
		}
		if options && a.known {
			t := a.text
			switch {
			case t == "--" || t == "-":
				options = false
				continue
			case strings.HasPrefix(t, "--"):
				opt, value, inline := strings.Cut(t, "=")
				switch {
				case opt == "--command" && name == "fish":
					// fish's long --command is one of its repeated commands, so it
					// joins fishCmds and is analysed at the end like a short -c (not
					// left in operands, which the fish early return does not read):
					// `fish --command='apply_patch' -c true` ran apply_patch
					// (re-verify r1, finding 1). Seeing it puts fish in command
					// mode: a missing value is a fish error, not a stdin shell
					// (re-verify r2 finding 7).
					fishSawCommand = true
					if inline {
						fishCmds = append(fishCmds, shellWord{text: value, known: true})
					} else if j+1 < len(args) {
						fishCmds = append(fishCmds, args[j+1])
						j++
					}
				case opt == "--command":
					command = true
					if inline {
						operands = append(operands, shellWord{text: value, known: true})
					}
				case opt == "--init-command" && name == "fish":
					// fish runs its init commands too, but --no-execute suppresses
					// them like any command and may come LATER on the line, so they
					// are deferred to fishCmds (analysed at the end only if not
					// under -n): `fish --command true --no-execute --init-command
					// apply_patch` runs nothing (re-verify r2 finding 6).
					if inline {
						fishCmds = append(fishCmds, shellWord{text: value, known: true})
					} else if j+1 < len(args) {
						fishCmds = append(fishCmds, args[j+1])
						j++
					} else {
						// No value: a fish usage error, so fish exits before
						// reading stdin (`fish -C true --init-command <<<…`,
						// re-verify r4 finding 2).
						fishUsage = true
					}
				case (opt == "--no-execute" || opt == "--version" || opt == "--print-debug-categories") && name == "fish":
					// fish --no-execute (the long -n) checks syntax and runs
					// nothing, so its -c commands are not analysed (G1,
					// verdict3.md; D-106 operator fold 2026-10-27). --version
					// and --print-debug-categories print and exit while fish
					// reads its options, before any command or script runs
					// (documented; re-verify r4 finding 3).
					noexec = true
				case !inline && name == "fish" && fishValueLong[opt]:
					// The value is the next word, never the script file:
					// `fish --profile /tmp/p -c 'apply_patch …'` runs the -c
					// (re-verify r4 finding 1, the long spelling).
					if j+1 >= len(args) {
						fishUsage = true
					} else if args[j+1].overBudget {
						// A brace too large to expand in the value position
						// expands in place into options and a command (finding E).
						w.exhausted("shell patch routing exceeds its brace expansion budget")
						return
					}
					j++
				case !inline && (opt == "--rcfile" || opt == "--init-file"):
					if j+1 < len(args) && args[j+1].overBudget {
						// The file argument is a brace word too large to expand;
						// a shell expands it in place into more options and a
						// command: `bash --rcfile {f,-c,apply_patch,x{1..300}}`
						// becomes `bash --rcfile f -c apply_patch ...` (finding E,
						// 2026-10-12 re-review).
						w.exhausted("shell patch routing exceeds its brace expansion budget")
						return
					}
					j++
				}
				continue
			case name == "fish" && len(t) > 1 && t[0] == '-':
				// fish reads a cluster as getopt does, letter by letter, and the
				// FIRST letter that takes a value takes the rest of the cluster,
				// or else the next word, so no letter after it is an option.
				// Finding the first c or C anywhere read the C inside
				// `-p/tmp/initCommands.prof`, and its preceding n as -n, so
				// `fish -p/tmp/initCommands.prof -c 'apply_patch …'` was allowed;
				// a detached value was read as the script file (`fish -p /tmp/p
				// -c …`) or as bash's `-o noexec` (`fish -o noexec -c …`)
				// (re-verify r4 finding 1). In -capply_patch < CHANGE.patch the C
				// is script text (Codex ninth code pass).
				// Letters BEFORE the value letter apply: -n checks syntax only
				// (G2, `-nC`, re-verify r3 finding 2), -v prints the version and
				// exits (re-verify r4 finding 3).
				flags, letter, rest := t[1:], byte(0), ""
				if k := strings.IndexAny(t[1:], fishValueLetters); k >= 0 {
					flags, letter, rest = t[1:1+k], t[1+k], t[2+k:]
				}
				if strings.ContainsAny(flags, "nv") {
					noexec = true
				}
				if strings.ContainsRune(flags, 's') {
					stdin = true
				}
				if letter == 0 {
					continue
				}
				value, ok := shellWord{text: rest, known: true}, rest != ""
				if !ok && j+1 < len(args) {
					value, ok = args[j+1], true
					j++
				}
				switch {
				case !ok:
					// A value option ending the line is a fish usage error: fish
					// exits before reading stdin (`fish -C true -C <<<…`,
					// re-verify r4 finding 2; r2 finding 7 for -c).
					fishUsage = true
				case letter == 'c' || letter == 'C':
					// fish runs EACH -c and -C command (it repeats): `fish -c true
					// -c "$x"` runs both, so the dynamic "$x" is read, not treated
					// as a positional (F1, verdict3.md). They are analysed at the
					// end, so a LATER -n suppresses them (re-verify r2 finding 6).
					fishCmds = append(fishCmds, value)
				case value.overBudget:
					// A brace too large to expand in the value position expands
					// in place into options and a command (finding E).
					w.exhausted("shell patch routing exceeds its brace expansion budget")
					return
				}
				if letter == 'c' {
					fishSawCommand = true
				}
				continue
			case len(t) > 1 && (t[0] == '-' || t[0] == '+'):
				if strings.ContainsRune(t[1:], 'c') {
					// fish's '-' clusters are read in their own case above (its
					// -c repeats, and its options take values); other shells read
					// the rest of the cluster as more options.
					command = true
				}
				if t[0] == '-' && strings.ContainsRune(t[1:], 's') {
					stdin = true
				}
				if strings.ContainsRune(t[1:], 'n') {
					// -n reads and checks syntax and nothing runs; +n turns
					// execution back on (Codex follow-up review).
					noexec = t[0] == '-'
				}
				if strings.ContainsAny(t[1:], "oO") {
					// The option's name is the next word. `+o noexec` turns
					// execution back on like +n (Codex third code pass).
					if j+1 < len(args) && args[j+1].overBudget {
						// A brace word too large to expand sits where the option
						// name goes; a shell expands it in place into more
						// options and a command: `bash -o {posix,-c,apply_patch,
						// x{1..300}}` becomes `bash -o posix -c apply_patch ...`
						// (finding E, 2026-10-12 re-review).
						w.exhausted("shell patch routing exceeds its brace expansion budget")
						return
					}
					if j+1 < len(args) && args[j+1].known && args[j+1].text == "noexec" {
						noexec = t[0] == '-'
					}
					j++
				}
				continue
			}
		}
		options = false
		operands = append(operands, a)
	}
	if name == "fish" {
		// fish's -c and init commands are analysed (unless -n suppresses
		// execution). A -c/--command puts fish in COMMAND mode, so it does not
		// then read stdin — even a missing -c value is a fish error, not a
		// stdin shell (finding 7). But init commands ALONE (`-C`/--init-command,
		// fishSawCommand == false) do NOT suppress stdin: fish still reads its
		// main script from stdin or a file, so the switch below must run
		// (re-verify r3 finding 1: `fish -C true <<'SH'…` reads the heredoc).
		// An option missing its value is a usage error: fish exits and reads
		// no script (re-verify r4 finding 2); the commands before it are still
		// analysed, which can only over-refuse.
		if !noexec {
			for _, c := range fishCmds {
				w.scriptOf([]shellWord{c}, depth, "fish runs command text built at run time", site.fds)
			}
		}
		if fishSawCommand || fishUsage {
			return
		}
	}
	switch {
	case noexec:
	case command:
		if len(operands) == 0 {
			return
		}
		w.scriptOf(operands[:1], depth, "a shell runs script text built at run time", site.fds)
		for _, o := range operands[1:] {
			if o.known && looksLikeScript(o.text) {
				w.analyze(o.text, depth+1, false, site.fds) // $0 and positional parameters, probably
			}
		}
	case stdin || len(operands) == 0:
		w.stdinScript(site, depth)
	default:
		w.scriptFile(operands, site, depth)
	}
}

// scriptOf analyses fixed text a command will run as shell. Text with any
// expansion is produced at run time: uncertain.
func (w *shellWalker) scriptOf(words []shellWord, depth int, dynamic string, in fdTable) {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		if word.overBudget {
			w.exhausted("shell patch routing exceeds its brace expansion budget")
			return
		}
		if !word.known {
			w.uncertainty(dynamic)
			return
		}
		parts = append(parts, word.text)
	}
	if len(parts) > 0 {
		w.analyze(strings.Join(parts, " "), depth+1, true, in)
	}
}

// scriptFile: the first operand names the script. A file's contents are
// cooperative; a process substitution is text produced at run time; stdin's
// device reads the statement's own stdin.
func (w *shellWalker) scriptFile(operands []shellWord, site callSite, depth int) {
	if len(operands) == 0 {
		return
	}
	switch first := operands[0]; {
	case first.overBudget:
		// An over-budget brace as the script name expands in place; its first
		// field is the file a shell sources, and `source {/dev/stdin,x{1..300}}`
		// reads the inherited stdin (D-106 follow-up, FULL-review cross-check).
		w.exhausted("shell patch routing exceeds its brace expansion budget")
	case first.procSubst:
		w.uncertainty("a shell runs script text a process substitution produces")
	case first.known && (first.text == "/dev/stdin" || first.text == "/dev/fd/0"):
		w.stdinScript(site, depth)
	}
}

// stdinScript reads what a stdin-reading shell will run.
func (w *shellWalker) stdinScript(site callSite, depth int) {
	src := site.fds["0"]
	if src.redirect == nil {
		if src.piped {
			w.uncertainty("a shell reads script text a pipeline produces")
		}
		return
	}
	if depth+1 >= shellMaxDepth {
		// Refuse before rebuilding an inherited body that analyze would only
		// refuse: at the depth limit 8,000 shells each rebuilt one 80 KB body
		// uncounted (Codex eighth code pass).
		w.exhausted("shell patch routing exceeds its script budget")
		return
	}
	// The script's commands keep every other inherited descriptor; stdin is
	// the text being read as script (Codex eighth code pass).
	rest := site.fds.with("0", stdinSource{})
	switch r := src.redirect; r.Op {
	case syntax.Hdoc, syntax.DashHdoc:
		// A quoted delimiter keeps the body literal; an unquoted one with an
		// expansion makes it produced text.
		size := 0
		for _, part := range r.Hdoc.Parts {
			lit, ok := part.(*syntax.Lit)
			if !ok {
				w.uncertainty("a shell reads a here-document with expansions")
				return
			}
			size += len(lit.Value)
		}
		if w.parsed+size > shellMaxParsedBytes {
			// Checked before the body is rebuilt: one inherited
			// here-document read by 8,000 shells used to be rebuilt and kept
			// 8,000 times (Codex seventh code pass).
			w.exhausted("shell patch routing exceeds its analysis budget")
			return
		}
		var b strings.Builder
		b.Grow(size)
		for _, part := range r.Hdoc.Parts {
			b.WriteString(part.(*syntax.Lit).Value)
		}
		// Commands in a stdin-fed script read the rest of that same text,
		// which this analysis already reads as script.
		w.analyze(b.String(), depth+1, true, rest)
	case syntax.WordHdoc:
		if w.parsed+int(r.Word.End().Offset()-r.Word.Pos().Offset()) > shellMaxParsedBytes {
			w.exhausted("shell patch routing exceeds its analysis budget")
			return
		}
		w.scriptOf([]shellWord{decodeWord(r.Word)}, depth, "a shell reads a here-string built at run time", rest)
	default:
		if decodeWord(r.Word).procSubst {
			w.uncertainty("a shell reads script text a process substitution produces")
		}
		// A file: cooperative, like a script file.
	}
}

// findActions scans each -exec/-execdir/-ok/-okdir action on its own, from its
// established command position. One action's data program must not hide a
// later action (Codex design review).
func (w *shellWalker) findActions(rest []shellWord, site callSite, depth int) {
	// An over-budget brace in find's argv is NOT refused: most of find's words
	// are data (primaries and their values, `find . -name foo{1..300}`,
	// `-exec grep -l foo path{1..300} \;`), and a guard here false-denied them
	// (re-verify r1 finding 9 at the top level, r2 finding 4 inside an action).
	// The crafted action-position brace `find . {-exec,apply_patch,…} \;` is
	// recorded OPEN in D-106; a 256+-field brace is non-idiomatic and, under
	// Claude's allow arm, that bypass costs nothing while the false denial cost
	// every find with a large data brace a tool call (orc-7's direction).
	for j := 0; j < len(rest); j++ {
		switch rest[j].text {
		case "-exec", "-execdir", "-ok", "-okdir":
		default:
			continue
		}
		k := j + 1
		for k < len(rest) && !(rest[k].known && (rest[k].text == ";" || rest[k].text == "+")) {
			k++
		}
		w.command(rest[j+1:k], site, depth)
		j = k
	}
}

// gitRoute: git's ordinary operands are data, except the three subcommands
// that run one (Codex design review). Aliases and -c config stay cooperative.
func (w *shellWalker) gitRoute(rest []shellWord, site callSite, depth int) {
	seen := make(map[string]bool)
	for j := 0; j < len(rest); j++ {
		word := rest[j]
		// An over-budget brace elsewhere in git's argv is NOT refused here: most
		// of git's words are data (refs, pathspecs, `git diff -- path{1..300}`),
		// and a blanket guard false-denied them (re-verify r1 finding 5). The
		// over-budget subcommand-position brace `git -C . {bisect,run,
		// apply_patch,…}` (codex 10) is recorded OPEN in D-106 as crafted
		// (it also needs an active bisect). Only `-C`'s directory value, below,
		// is a non-data position and still exhausts.
		if !word.known {
			continue
		}
		t := word.text
		switch {
		case t == "-C" && j+1 < len(rest):
			// git -C DIR runs the subcommand, and whatever it executes,
			// there (Codex fifth code pass).
			if rest[j+1].overBudget {
				// The directory word is a brace too large to expand, which a
				// shell expands in place into git's own argv: `git -C
				// {.,bisect,run,apply_patch,x{1..300}}` becomes `git -C .
				// bisect run apply_patch ...` (finding E follow-up, 2026-10-12
				// re-review; an over-budget value consumer outside wrapper/shell).
				w.exhausted("shell patch routing exceeds its brace expansion budget")
				return
			}
			w.directory(rest[j+1])
			j++
			continue
		case t == "run" && seen["bisect"]:
			w.command(rest[j+1:], site, depth)
			return
		case t == "foreach" && seen["submodule"]:
			k := j + 1
			for k < len(rest) && rest[k].known && strings.HasPrefix(rest[k].text, "--") {
				k++
			}
			w.scriptOf(rest[k:], depth, "git submodule foreach runs text built at run time", site.fds)
			return
		case seen["rebase"] && (t == "-x" || t == "--exec") && j+1 < len(rest):
			w.scriptOf(rest[j+1:j+2], depth, "git rebase --exec runs text built at run time", site.fds)
			j++
		case seen["rebase"] && strings.HasPrefix(t, "-x") && len(t) > 2:
			w.analyze(t[2:], depth+1, true, site.fds)
		case seen["rebase"] && strings.HasPrefix(t, "--exec="):
			w.analyze(strings.TrimPrefix(t, "--exec="), depth+1, true, site.fds)
		}
		seen[t] = true
	}
}

// changeDirectory records where cd, pushd and zsh's chdir may land. Only
// jurisdiction discovery reads it; recognition ignores it.
func (w *shellWalker) changeDirectory(name string, rest []shellWord) {
	if w.ctx == nil {
		return
	}
	var operands []shellWord
	for _, word := range rest {
		if !word.known {
			return // a dynamic directory is unseen
		}
		if len(operands) == 0 && strings.HasPrefix(word.text, "-") && word.text != "-" && !isStackIndex(word.text) {
			if name == "pushd" && word.text == "-n" {
				return // pushd -n changes only the stack
			}
			continue
		}
		operands = append(operands, word)
	}
	switch {
	case len(operands) == 0:
		w.directory(shellWord{text: "~", known: true, tilde: true}) // cd alone goes home
	case operands[0].text == "-" || isStackIndex(operands[0].text):
		// OLDPWD and the directory stack are unseen.
	case len(operands) == 2:
		// zsh: `cd old new` substitutes new for old in the current
		// directory; bash refuses two operands. Keep both readings.
		w.directory(operands[0])
		w.ctx.substitute(operands[0].text, operands[1].text)
	default:
		w.directory(operands[0])
	}
}

func isStackIndex(s string) bool {
	if len(s) < 2 || (s[0] != '+' && s[0] != '-') {
		return false
	}
	_, err := strconv.Atoi(s[1:])
	return err == nil
}

// directory adds one literal directory, expanding an unquoted leading ~ or
// ~/ against every HOME the command could run with (audit A-01: the gate used
// to place `~` under the cwd; bash and zsh land in the home directory). ~user,
// ~+ and ~- stay unseen.
func (w *shellWalker) directory(word shellWord) {
	if w.ctx == nil || !word.known {
		return
	}
	if !word.tilde {
		w.ctx.addDirectory(word.text)
		return
	}
	rest, ok := strings.CutPrefix(word.text, "~")
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return
	}
	for _, home := range w.homes {
		w.ctx.addDirectory(home + rest)
	}
}

// collectHomes adds every literal HOME the command assigns (HOME=x cmd, export
// HOME=x, env HOME=x zsh -c 'cd ~'), keeping the hook's own: which assignment
// is in force where is not modelled, so every candidate is checked.
func (w *shellWalker) collectHomes(tree *syntax.File) {
	if w.ctx == nil {
		return
	}
	add := func(value string) {
		for _, home := range w.homes {
			if home == value {
				return
			}
		}
		// A relative HOME is relative to wherever the shell is when ~ is
		// read; addDirectory resolves it against every candidate base.
		if value != "" && len(w.homes) < maxPatchPaths {
			w.homes = append(w.homes, value)
		}
	}
	assign := func(a *syntax.Assign) {
		if a != nil && a.Name != nil && a.Name.Value == "HOME" && a.Value != nil {
			if value, ok := shellLiteral(a.Value.Parts, false); ok {
				add(value)
			}
		}
	}
	syntax.Walk(tree, func(node syntax.Node) bool {
		switch x := node.(type) {
		case *syntax.CallExpr:
			for _, a := range x.Assigns {
				assign(a)
			}
			for _, arg := range x.Args {
				if text, ok := shellLiteral(arg.Parts, false); ok {
					if value, ok := strings.CutPrefix(text, "HOME="); ok {
						add(value)
					}
				}
			}
		case *syntax.DeclClause:
			for _, a := range x.Args {
				assign(a)
			}
		}
		return true
	})
}

// unparsed keeps what jurisdiction can still learn from a script the walker
// will not parse (over a budget): its literal directory changes and its
// embedded patch paths. An oversize script used to return before either was
// recorded, so `cd /governed; apply_patch ...` plus 128 KiB of comment was
// refused where governed and ALLOWED from outside (Codex code review).
func (w *shellWalker) unparsed(script string) {
	if w.ctx == nil || w.lexed+len(script) > shellMaxLexedBytes {
		return // the retained and lexed text share one bound
	}
	w.ctx.scripts = append(w.ctx.scripts, script)
	w.lexicalDirectories(script)
}

// lexicalDirectories recovers literal directory changes from a script that
// does not parse, so jurisdiction survives the failure: `cd /governed;
// () { apply_patch < p }` runs in zsh (Codex design review). Tokens honour
// quotes and backslashes, so `cd '/governed repo'` is one directory (Codex
// code review); anything subtler is unseen. Total lexical work is bounded.
func (w *shellWalker) lexicalDirectories(script string) {
	if w.ctx == nil || w.lexed+len(script) > shellMaxLexedBytes {
		return
	}
	w.lexed += len(script)
	tokens := lexTokens(script)
	for k := 0; k+1 < len(tokens); k++ {
		switch t := tokens[k].text; {
		case t == "cd" || t == "pushd" || t == "chdir" || t == "-C" || t == "--chdir" || t == "-D":
			w.directory(tokens[k+1])
		case strings.HasPrefix(t, "--chdir="):
			w.directory(shellWord{text: strings.TrimPrefix(t, "--chdir="), known: true})
		case len(t) > 1 && t[0] == '-' && t[1] != '-' && strings.ContainsAny(t, "CD"):
			// A short-option cluster: -C/dir, -vC DIR, sudo -D/dir (Codex
			// fifth code pass: -vC used to be missed).
			if c := strings.IndexAny(t, "CD"); c+1 < len(t) {
				w.directory(shellWord{text: t[c+1:], known: true})
			} else {
				w.directory(tokens[k+1])
			}
		}
	}
}

// lexTokens splits shell text on blanks and operators, joining quoted runs and
// backslash escapes into their word. A token is tilde-expandable only when it
// begins with an unquoted ~.
func lexTokens(s string) []shellWord {
	var out []shellWord
	var b strings.Builder
	open, tilde := false, false
	flush := func() {
		if open {
			out = append(out, shellWord{text: b.String(), known: true, tilde: tilde})
		}
		b.Reset()
		open, tilde = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				end = len(s) - i - 1
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 1
			open = true
		case c == '"':
			// Inside double quotes a backslash escapes only $ ` " \ and a
			// newline, as shellLiteral decodes them (Codex follow-up review).
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
					if s[i] == '\n' {
						continue
					}
				}
				b.WriteByte(s[i])
			}
			open = true
		case c == '\\' && i+1 < len(s):
			i++
			if s[i] == '\n' {
				continue // line continuation: `cd \<newline>/dir` (Codex third code pass)
			}
			b.WriteByte(s[i])
			open = true
		case c == ' ' || c == '\t' || c == '\n' || strings.IndexByte(";&|(){}<>", c) >= 0:
			flush()
		default:
			if !open && c == '~' {
				tilde = true
			}
			b.WriteByte(c)
			open = true
		}
	}
	flush()
	return out
}

// shellLiteral decodes only fixed quoted words. Expansions are never evaluated:
// syntax.Walk visits any command substitutions separately, even in double quotes.
func shellLiteral(parts []syntax.WordPart, double bool) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			for i := 0; i < len(p.Value); i++ {
				if p.Value[i] == '\\' && i+1 < len(p.Value) {
					next := p.Value[i+1]
					if !double || strings.ContainsRune("$`\"\\\n", rune(next)) {
						i++
						if next != '\n' {
							b.WriteByte(next)
						}
						continue
					}
				}
				b.WriteByte(p.Value[i])
			}
		case *syntax.SglQuoted:
			// Only this fixed quoted node enters the literal decoder: no
			// substitutions, environment values, globbing or execution callbacks.
			value, err := expand.Literal(nil, &syntax.Word{Parts: []syntax.WordPart{p}})
			if err != nil {
				return "", false
			}
			b.WriteString(value)
		case *syntax.DblQuoted:
			value, ok := shellLiteral(p.Parts, true)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		default:
			return "", false
		}
	}
	return b.String(), true
}

func shellPatchReason(h hookInput, r shellRoute) string {
	if h.Harness != "codex" {
		// Not Codex (D-087): no in-process engine and no apply_patch tool to
		// point at, so the words are about the literal and the budget only.
		if !r.found {
			return fmt.Sprintf("buddy gate: the gate stopped reading this %s command (%s) before it could rule out a literal apply_patch; split the command or put it in a script file; refusing.", h.ToolName, fence.Line(r.uncertain, 256))
		}
		return fmt.Sprintf("buddy gate: this %s command runs apply_patch (or applypatch) as a command. Under Codex that word is an in-process patch engine whose paths the gate cannot place, so buddy refuses it at a command position under every harness (D-073, D-087); refusing.", h.ToolName)
	}
	if !r.found && r.uncertain != "" {
		return fmt.Sprintf("buddy gate: this %s command cannot rule out apply_patch through the shell: %s. Codex can apply a patch in-process from its shell, and the hook payload omits the command's working directory, so the gate cannot place such a patch against the claims. Write the command so its script is literal text the gate can read, or use the apply_patch tool, which the gate checks path by path; refusing.", h.ToolName, fence.Line(r.uncertain, 256))
	}
	return fmt.Sprintf("buddy gate: this %s command invokes, or cannot safely rule out, apply_patch through the shell. Codex can apply it in-process, but the hook payload omits the command's working directory, so the gate cannot place the patched paths against the claims. Use the apply_patch tool, which the gate checks path by path; refusing.", h.ToolName)
}

// shellPatchContext records possible literal working directories from the
// same syntax walk used for recognition. Branches and subshells can leave the
// prior directory intact, so retain both instead of guessing which executes.
// This is jurisdiction discovery only: any governed candidate refuses the
// shell route. It never grants a patch's individual paths.
type shellPatchContext struct {
	bases       []string
	scripts     []string
	err         error
	resolutions int
}

// maxDirectoryResolutions bounds the gateTargets calls one hook's directory
// changes may cost, so relative directories cannot fan out against every
// retained base without limit.
const maxDirectoryResolutions = 512

func (c *shellPatchContext) addDirectory(raw string) {
	if len(c.bases) >= maxPatchPaths {
		// Saturated: later directories are unseen, and the route says so. It
		// used to keep resolving every directory against every base: 1,000
		// literal cds took 9.3 s in one gate call (Codex code review).
		c.err = fmt.Errorf("shell patch routing exceeds %d candidate directories", maxPatchPaths)
		return
	}
	bases := append([]string(nil), c.bases...)
	if filepath.IsAbs(raw) {
		bases = bases[:1] // an absolute directory lands in one place from every base
	}
	for _, base := range bases {
		if c.resolutions++; c.resolutions > maxDirectoryResolutions {
			c.err = fmt.Errorf("shell patch routing exceeds %d directory resolutions", maxDirectoryResolutions)
			return
		}
		// cd can use logical or physical parent traversal; retain both placements.
		targets, err := gateTargets(base, raw+string(filepath.Separator)+".buddy-cwd-probe")
		if err != nil {
			c.err = err
		}
		for _, target := range targets {
			c.addBase(filepath.Dir(target))
		}
	}
}

// substitute is zsh's two-operand cd: the first occurrence of old in the
// current directory's name becomes new.
func (c *shellPatchContext) substitute(old, new string) {
	if old == "" {
		return
	}
	for _, base := range append([]string(nil), c.bases...) {
		if strings.Contains(base, old) {
			c.addDirectory(strings.Replace(base, old, new, 1))
		}
	}
}

func (c *shellPatchContext) addBase(candidate string) {
	for _, old := range c.bases {
		if old == candidate {
			return
		}
	}
	if len(c.bases) >= maxPatchPaths {
		c.err = fmt.Errorf("shell patch routing exceeds %d candidate directories", maxPatchPaths)
		return
	}
	c.bases = append(c.bases, candidate)
}

// shellPatchDirs checks literal cd/pushd/env/sudo directories inside every
// script the recognizer reads, as well as literal embedded patch paths. A
// workdir outside the hook command remains invisible; dynamic values are not
// expanded. home is the hook's HOME, the first candidate for `~`.
func shellPatchDirs(h hookInput, home string) ([]string, error) {
	context := &shellPatchContext{bases: []string{h.Cwd}}
	w := &shellWalker{ctx: context}
	if filepath.IsAbs(home) {
		w.homes = []string{home}
	}
	w.analyze(h.ToolInput.Command, 0, true, nil)
	var dirs []string
	for _, base := range context.bases {
		dir := existingDir(filepath.Join(canon(base), "x"))
		// A base that does not resolve (a vanished hook cwd, a `cd` under it,
		// or a `cd ..` that lands on a dangling link's own entry) is placed as
		// an empty directory there would be (D-102). canon cannot follow a
		// DANGLING link and existingDir then climbs past it lexically, into a
		// repository the directory would not be in (Codex code and re-verify
		// passes). Embedded patch targets below keep the lexical walk: at worst
		// a refusal of a shell-routed patch the tool would take.
		if _, err := os.Stat(base); errors.Is(err, syscall.ENOENT) {
			if anc, ok := resolvedAncestor(base); ok {
				dir = anc
			}
		}
		dirs = append(dirs, dir)
	}
	for _, script := range context.scripts {
		i := strings.Index(script, "*** Begin Patch")
		j := strings.LastIndex(script, "*** End Patch")
		if i < 0 || j < i {
			continue
		}
		paths, err := patchPaths(script[i : j+len("*** End Patch")])
		if err != nil {
			continue
		}
		for _, base := range context.bases {
			for _, p := range paths {
				targets, err := gateTargets(base, p)
				if err != nil {
					context.err = err
				}
				for _, cp := range targets {
					dirs = append(dirs, existingDir(cp))
				}
			}
		}
	}
	return dirs, context.err
}

// Bound adjudication work separately from the hook's byte cap. A short delete
// line can otherwise request hundreds of thousands of ledger/git lookups in
// one 16 MiB hook. Count distinct spellings; duplicate operations still have
// their structure validated, but cannot multiply the same target lookup.
const maxPatchPaths = 128

func gatePaths(h hookInput) ([]string, error) {
	if h.ToolName == "apply_patch" {
		return patchPaths(h.ToolInput.Command)
	}
	if path := h.path(); path != "" {
		return []string{path}, nil
	}
	if pathTools[h.ToolName] {
		return nil, fmt.Errorf("could not find the target path in %s's hook input (schema drift?)", h.ToolName)
	}
	return nil, nil // Shell side effects and unknown pathless tools remain pause-only.
}

// patchPaths follows the documented Add/Delete/Update grammar and its local
// whitespace rules. In an update, a leading space means CONTEXT, including a
// line spelling "*** Delete File:"; trimming that prefix invents a target.
// Header suffix whitespace is stripped by Codex, so preserve that behavior
// instead of checking a different filename. No partial path list survives an
// error: a malformed later hunk must not authorize the earlier ones.
func patchPaths(patch string) ([]string, error) {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("apply_patch requires a complete Begin Patch / End Patch envelope")
	}
	var paths []string
	seen := make(map[string]bool)
	mode := ""
	moved, chunk, content, eof := false, false, false, false
	bad := func(line int, why string) ([]string, error) {
		return nil, fmt.Errorf("apply_patch line %d: %s", line+1, why)
	}
	addPath := func(path string) error {
		// Filesystem paths cannot contain NUL. URI routing is not covered by
		// the local worktree contract, so never silently treat a URI as one.
		if path == "" || strings.ContainsAny(path, "\x00\r\n") || strings.Contains(path, "://") {
			return fmt.Errorf("missing or unsupported local target path")
		}
		if seen[path] {
			return nil
		}
		if len(paths) == maxPatchPaths {
			return fmt.Errorf("patch exceeds %d distinct target paths; split it into smaller patches", maxPatchPaths)
		}
		seen[path] = true
		paths = append(paths, path)
		return nil
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		header := strings.TrimSpace(line)
		if mode == "update" {
			header = strings.TrimRightFunc(line, unicode.IsSpace)
		}
		// The terminal envelope permits surrounding whitespace. Interior
		// context lines spelling End Patch remain body content.
		if i == len(lines)-1 {
			header = strings.TrimSpace(line)
		}
		nextMode, path := "", ""
		for _, op := range []struct{ prefix, mode string }{
			{"*** Add File: ", "add"}, {"*** Delete File: ", "delete"}, {"*** Update File: ", "update"},
		} {
			if p, ok := strings.CutPrefix(header, op.prefix); ok {
				nextMode, path = op.mode, p
				break
			}
		}
		if nextMode != "" || header == "*** End Patch" {
			if mode == "update" && (!chunk || !content) {
				return bad(i, "update hunk has no content")
			}
			if header == "*** End Patch" {
				if i != len(lines)-1 || len(paths) == 0 {
					return bad(i, "empty patch or content after End Patch")
				}
				return paths, nil
			}
			if err := addPath(path); err != nil {
				return bad(i, err.Error())
			}
			mode = nextMode
			moved, chunk, content, eof = false, false, false, false
			continue
		}
		switch mode {
		case "add":
			if strings.HasPrefix(line, "+") {
				continue
			}
		case "update":
			if p, ok := strings.CutPrefix(header, "*** Move to: "); ok && !moved && !chunk {
				if err := addPath(p); err != nil {
					return bad(i, err.Error())
				}
				moved = true
				continue
			}
			if header == "@@" || strings.HasPrefix(header, "@@ ") {
				if chunk && !content {
					return bad(i, "empty update chunk")
				}
				chunk, content, eof = true, false, false
				continue
			}
			if header == "*** End of File" && content && !eof {
				eof = true
				continue
			}
			if eof && header == "" {
				continue
			}
			if !eof && (line == "" || strings.ContainsAny(line[:1], " +-")) {
				chunk, content = true, true
				continue
			}
		}
		return bad(i, "unexpected patch line")
	}
	return nil, fmt.Errorf("apply_patch has no complete patch")
}
