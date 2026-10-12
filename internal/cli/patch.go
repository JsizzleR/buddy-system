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
	"path/filepath"
	"strconv"
	"strings"
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
	w := &shellWalker{}
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
		"script":     set("-t", "-T", "-c", "--command"),
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
	// speculative > 0 while reading an operand that merely looks like shell
	// text. There only an established command position counts: scanning an
	// unknown program's operands would refuse prose such as
	// `curl -d "please run apply_patch now"`.
	speculative int
	lexed       int
	funcs       map[string]bool // functions the script being walked defines
	route       shellRoute
}

// uncertainty records the first reason a command cannot be ruled out.
func (w *shellWalker) uncertainty(why string) {
	if w.route.uncertain == "" {
		w.route.uncertain = why
	}
}

// exhausted is an uncertainty that also says jurisdiction discovery stopped
// early, so the ungoverned arm can name it.
func (w *shellWalker) exhausted(why string) {
	w.uncertainty(why)
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
			v, ok := next()
			if !ok {
				return len(rest), true
			}
			if !v.known {
				w.uncertainty(name + " " + opt + " runs text built at run time")
				return len(rest), true
			}
			if name == "env" && strings.ContainsAny(v.text, `\$#`) {
				// env -S has its own escapes and ${VAR} expansion, and `\c`
				// ends the string: `env -S 'apply_patch\c'` runs apply_patch
				// (Codex fourth code pass). They are not modelled.
				// A # comment ends the split text but not argv, so joining
				// the rest after it would hide a command (Codex eighth pass).
				w.uncertainty("env -S text with escapes, expansions or comments is not modelled")
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
			j++
		}
	}
	if shellMode && j >= len(rest) {
		// `sudo -s <<'SH'`: no command, so sudo's shell runs stdin (Codex
		// ninth code pass).
		w.stdinScript(site, depth)
	}
	return j, true
}

// shell reads a shell's options: with -c (fish: --command) its first operand
// is a certain script; with -s or no operand it reads its script from stdin;
// otherwise its first operand is a script FILE, which stays cooperative.
// Options are read as clusters of either sign, so `bash -c --`, `bash +x -c`
// and `bash +o posix -c` all find their script (audit A-01).
func (w *shellWalker) shell(name string, args []shellWord, site callSite, depth int) {
	command, stdin, noexec := false, false, false
	var operands []shellWord
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
				case opt == "--command":
					command = true
					if inline {
						operands = append(operands, shellWord{text: value, known: true})
					}
				case opt == "--init-command" && name == "fish":
					// fish runs its init commands too (Codex seventh code pass).
					if inline {
						w.scriptOf([]shellWord{{text: value, known: true}}, depth, "fish runs init text built at run time", site.fds)
					} else if j+1 < len(args) {
						w.scriptOf(args[j+1:j+2], depth, "fish runs init text built at run time", site.fds)
						j++
					}
				case !inline && (opt == "--rcfile" || opt == "--init-file"):
					j++
				}
				continue
			case name == "fish" && len(t) > 1 && t[0] == '-' && strings.IndexAny(t[1:], "cC") >= 0 && t[1+strings.IndexAny(t[1:], "cC")] == 'C':
				// fish -C TEXT: an init command, run like -c's script. Only
				// when C comes first: in -capply_patch < CHANGE.patch the C
				// is script text (Codex ninth code pass).
				if c := strings.IndexRune(t, 'C'); c+1 < len(t) {
					w.scriptOf([]shellWord{{text: t[c+1:], known: true}}, depth, "fish runs init text built at run time", site.fds)
				} else if j+1 < len(args) {
					w.scriptOf(args[j+1:j+2], depth, "fish runs init text built at run time", site.fds)
					j++
				}
				continue
			case len(t) > 1 && (t[0] == '-' || t[0] == '+'):
				if strings.ContainsRune(t[1:], 'c') {
					command = true
					if c := strings.IndexRune(t, 'c'); name == "fish" && t[0] == '-' && c+1 < len(t) {
						// fish takes -c's script attached: -capply_patch
						// (Codex eighth code pass). Other shells read the
						// rest of the cluster as more options.
						operands = append(operands, shellWord{text: t[c+1:], known: true})
						continue
					}
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
		if !word.known {
			continue
		}
		t := word.text
		switch {
		case t == "-C" && j+1 < len(rest):
			// git -C DIR runs the subcommand, and whatever it executes,
			// there (Codex fifth code pass).
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
		dirs = append(dirs, existingDir(filepath.Join(canon(base), "x")))
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
