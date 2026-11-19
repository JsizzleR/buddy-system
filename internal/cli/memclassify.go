package cli

// The memory slot's classifier (issue #144, D-238): what a shell command
// STARTS, and how much memory the slot books for it. memslot.go says why the
// slot exists and how a booking is judged; this file only reads the command.
//
// ONLY TEXT THAT RUNS IS READ. The operator's earlier local gate matched
// regular expressions over the whole command after blanking quoted strings and
// dropping heredoc bodies, and an independent review of it (#144 comment 1)
// found the class that blanking exists for: `git commit -m '...go test...'`,
// a `buddy msg` READY report naming its checks, and a heredoc writing a prompt
// were refused under low memory, which blocks exactly the reports a fleet
// needs. Here the command is parsed by mvdan.cc/sh, the parser the gate's
// shell-patch reader uses (patch.go, D-073), and only an established command
// position counts: a quoted word is a word, a heredoc fed to cat is data, an
// ssh operand runs elsewhere. What DOES run is read wherever it sits: a
// command substitution in an argument, an unquoted heredoc or a double-quoted
// string; a heredoc or here-string fed to a shell; a literal `sh -c` payload
// (re-parsed, depth-bounded).
//
// NOT patch.go's walker. Its default is inverted (an unknown program's
// operands are possible command positions), which is right for a refusal that
// must not under-read a patch and wrong here, where over-reading refuses a
// light call under low memory. This one follows the wrappers patch.go's table
// lists and nothing else; an unknown program books nothing. That is the
// cooperative residue, said so in D-238: a script file (`sh /tmp/mutate.sh`
// other than the named check scripts), `make test`, `go test $pkgs` read as
// one package, an alias, a function defined in another call, an exported
// GOFLAGS, `tmux new -d 'claude ...'`. A script that runs heavy work inside
// asks `buddy mem admit` itself.
//
// THE COST IS THE PARSE TREE'S, not a count of matches. Parts that run one
// after another (a list, `&&`, `||`, `if`, `case`) take the MAX: one build is
// over before the next starts. Parts that run at once (a pipeline, a statement
// backgrounded with `&`) SUM. A loop books its body once when the body runs in
// the foreground (one iteration at a time), and its item count, or three when
// the count is unknown, when the body backgrounds work, starts lanes or runs
// xargs in parallel: the local gate booked three for ANY loop, and a review of
// this design found the converse failing input, twelve `claude --resume`
// starts in a `for` loop booked as one lane. Lane starts always sum: a lane
// keeps running after the call that started it.
//
// DETACHED work is booked apart from foreground work, because it outlives the
// call: a lane start, a VM start, and a statement backgrounded with `&` that no
// later `wait` in the same list collects. PostToolUse releases only the
// foreground row (memslot.go); a detached one fades. Releasing everything at
// PostToolUse let `herdr agent start` (seconds) and `go test ./... > log &`
// book nothing while they ran: #144's own shape (Codex design pass, panel).
//
// A panic here books NOTHING (classifyMem recovers): under D-107's hook line
// any panic exits 2, which blocks the call, and a classifier bug must never
// block a light call machine-wide (panel lens A). The cost of that direction is
// one heavy call unjudged, and the panic is a bug to fix.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// memKindCost is the memory, in GB, the slot books for one start of each kind.
// The figures are the operator's measured local gate's (2026-11-17, one 24 GB
// laptop; D-238 says which were measured): a cold compile of the largest
// package peaked at 2.3 GB of linker RSS (gotest 3), a fresh VM start lowered
// available memory by 2 to 3 GB (vm 4), a Claude session starts near 0.25 GB
// and grows (lane 1). race and goall are not measured; tier is check.sh.
var memKindCost = map[string]float64{
	"tier":   8,
	"goall":  6,
	"race":   5,
	"vm":     4,
	"gotest": 3,
	"check":  3,
	"lane":   1,
	"codex":  0.5,
}

// memKinds is the kinds in usage order, heaviest first.
var memKinds = []string{"tier", "goall", "race", "vm", "gotest", "check", "lane", "codex"}

// memLoopFanout is what a loop whose iteration count is unknown multiplies a
// fanning-out body by; memLoopCap bounds a literal count.
const (
	memLoopFanout = 3
	memLoopCap    = 32
	memKindsShown = 12
)

// memCost is one part's booking: foreground memory (released when the call
// ends) and detached memory (it fades), each with the kinds that make it up,
// so a refusal can print the admit that covers it. fanout marks a part that
// starts work concurrently with whatever follows it.
type memCost struct {
	fg, det           float64
	fgKinds, detKinds []string
	fanout            bool
}

func (c memCost) total() float64 { return c.fg + c.det }

func (c memCost) kinds() []string {
	out := append(append([]string(nil), c.fgKinds...), c.detKinds...)
	sort.SliceStable(out, func(i, j int) bool { return memKindCost[out[i]] > memKindCost[out[j]] })
	return out
}

// seq is two parts that run one after the other: the larger foreground part,
// and every detached part (it is still running).
func seq(a, b memCost) memCost {
	out := memCost{det: a.det + b.det, detKinds: joinKinds(a.detKinds, b.detKinds), fanout: a.fanout || b.fanout}
	if b.fg > a.fg {
		out.fg, out.fgKinds = b.fg, b.fgKinds
	} else {
		out.fg, out.fgKinds = a.fg, a.fgKinds
	}
	return out
}

// par is two parts that run at once.
func par(a, b memCost) memCost {
	return memCost{
		fg: a.fg + b.fg, det: a.det + b.det,
		fgKinds: joinKinds(a.fgKinds, b.fgKinds), detKinds: joinKinds(a.detKinds, b.detKinds),
		fanout: a.fanout || b.fanout,
	}
}

func (c memCost) times(n int) memCost {
	out := memCost{fanout: c.fanout}
	for range n {
		out = par(out, c)
	}
	return out
}

// detach makes a part's foreground memory detached: it outlives the call.
func (c memCost) detach() memCost {
	return memCost{det: c.fg + c.det, detKinds: joinKinds(c.fgKinds, c.detKinds), fanout: true}
}

func joinKinds(a, b []string) []string {
	out := append(append([]string(nil), a...), b...)
	if len(out) > memKindsShown*memLoopCap {
		out = out[:memKindsShown*memLoopCap]
	}
	return out
}

func kindCost(kind string, detached bool) memCost {
	if detached {
		return memCost{det: memKindCost[kind], detKinds: []string{kind}}
	}
	return memCost{fg: memKindCost[kind], fgKinds: []string{kind}}
}

// memWalker carries what one command's walk found besides its cost.
type memWalker struct {
	parses, parsed int
	// override is the first literal, non-empty BUDDY_MEM_OK assignment at a
	// command position (memslot.go says how it is honored).
	override string
	// under is the first self-gated admit that does not cover the commands
	// chained after it: the gate refuses with this text.
	under string
}

// classifyMem reads one shell command. A panic books nothing (see the header).
func classifyMem(cmd string) (memCost, *memWalker) {
	return classifyRecovered(func(w *memWalker) memCost { return w.script(cmd, 0) })
}

// classifyRecovered runs one walk under the recover, apart from the walk so
// a test can hand it one that panics: no input is known to panic the walker,
// and a recover no test reaches is one a refactor could drop unseen.
func classifyRecovered(walk func(*memWalker) memCost) (c memCost, w *memWalker) {
	w = &memWalker{}
	defer func() {
		if recover() != nil {
			c, w = memCost{}, &memWalker{}
		}
	}()
	return walk(w), w
}

func (w *memWalker) script(src string, depth int) memCost {
	if depth >= shellMaxDepth || len(src) > shellMaxScript {
		return memCost{}
	}
	w.parses++
	w.parsed += len(src)
	if w.parses > shellMaxParses || w.parsed > shellMaxParsedBytes {
		return memCost{}
	}
	tree, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		// Unparseable text books nothing: the cooperative residue a script
		// file is too. Not a refusal: a refusal here would land on light
		// commands the bash parser rejects (zsh's own syntax).
		return memCost{}
	}
	return w.stmts(tree.Stmts, depth)
}

// stmts is a statement list: foreground statements one after another, each
// `&` statement at once with everything after it. A backgrounded statement is
// detached unless a later `wait` in the same list collects it.
func (w *memWalker) stmts(list []*syntax.Stmt, depth int) memCost {
	var fgPart, bgPart memCost
	for i, s := range list {
		c := w.stmt(s, depth)
		if s.Background {
			if waitsLater(list[i+1:]) {
				c.fanout = true
				bgPart = par(bgPart, c)
			} else {
				bgPart = par(bgPart, c.detach())
			}
			continue
		}
		fgPart = seq(fgPart, c)
	}
	return par(fgPart, bgPart)
}

// waitsLater: a later bare `wait` collects every job. `wait $!`, `wait %1` or
// `wait $pid` collects one, maybe another (`go test ./... & sleep 1 & wait
// $!` waits for the sleep, Codex code pass), so only a bare one counts: a
// part wrongly called detached fades, one wrongly called collected is
// released at PostToolUse while it runs.
func waitsLater(rest []*syntax.Stmt) bool {
	for _, s := range rest {
		if call, ok := s.Cmd.(*syntax.CallExpr); ok && len(call.Args) == 1 {
			if sw := decodeWord(call.Args[0]); sw.known && sw.text == "wait" {
				return true
			}
		}
	}
	return false
}

func (w *memWalker) stmt(s *syntax.Stmt, depth int) memCost {
	if s == nil {
		return memCost{}
	}
	c := memCost{}
	for _, r := range s.Redirs {
		c = seq(c, w.substs(r.Word, depth))
		c = seq(c, w.substs(r.Hdoc, depth))
	}
	return seq(c, w.command(s.Cmd, s, depth))
}

func (w *memWalker) command(cmd syntax.Command, s *syntax.Stmt, depth int) memCost {
	switch x := cmd.(type) {
	case nil:
		return memCost{}
	case *syntax.CallExpr:
		return w.call(x, s, depth)
	case *syntax.BinaryCmd:
		switch x.Op {
		case syntax.AndStmt:
			return w.andChain(x, depth)
		case syntax.OrStmt:
			return seq(w.stmt(x.X, depth), w.stmt(x.Y, depth))
		default: // a pipeline: every member runs at once
			return par(w.stmt(x.X, depth), w.stmt(x.Y, depth))
		}
	case *syntax.Subshell:
		return w.stmts(x.Stmts, depth)
	case *syntax.Block:
		return w.stmts(x.Stmts, depth)
	case *syntax.IfClause:
		c := memCost{}
		for clause := x; clause != nil; clause = clause.Else {
			c = seq(c, w.stmts(clause.Cond, depth))
			c = seq(c, w.stmts(clause.Then, depth))
		}
		return c
	case *syntax.WhileClause:
		// The condition runs every iteration too: `until go test ...; do
		// :; done` (panel lens C).
		body := seq(w.stmts(x.Cond, depth), w.stmts(x.Do, depth))
		return loopCost(body, 0)
	case *syntax.ForClause:
		n := 0
		c := memCost{}
		if it, ok := x.Loop.(*syntax.WordIter); ok {
			n = len(it.Items)
			for _, item := range it.Items {
				c = seq(c, w.substs(item, depth))
				if sw := decodeWord(item); !sw.known || wordMayExpand(item) {
					// A glob, a brace range or an expansion: the count is not
					// known. `{1..12}` and `*.md` decode as ONE known literal,
					// so twelve `claude --bg` starts booked as one (Fable).
					// Only the UNQUOTED text counts: a quoted `'^Test[A-Z]'`
					// is one item, and four of them booked three (Codex
					// re-verify 1).
					n = 0
				}
			}
			if !it.InPos.IsValid() {
				n = 0 // `for x; do`: the positional parameters
			}
		} else {
			c = seq(c, w.substs(x.Loop, depth))
		}
		return seq(c, loopCost(w.stmts(x.Do, depth), n))
	case *syntax.CaseClause:
		c := w.substs(x.Word, depth)
		for _, item := range x.Items {
			c = seq(c, w.stmts(item.Stmts, depth))
		}
		return c
	case *syntax.TimeClause:
		return w.stmt(x.Stmt, depth) // `time` alone has no statement
	case *syntax.CoprocClause:
		return w.stmt(x.Stmt, depth).detach()
	case *syntax.FuncDecl:
		// Booked as if called: a function defined and run in one call is the
		// ordinary case, and the over-book of one never called fades.
		return w.stmt(x.Body, depth)
	default:
		// DeclClause, LetClause, ArithmCmd, TestClause, and any node a later
		// parser adds: what runs inside them is a substitution.
		return w.substs(x, depth)
	}
}

// loopCost: a body that fans out (backgrounds work, starts lanes, runs xargs
// in parallel) books its literal iteration count, else three; any other body
// runs one iteration at a time and books once.
func loopCost(body memCost, n int) memCost {
	if !body.fanout && body.det == 0 {
		return body
	}
	if n <= 0 {
		n = memLoopFanout
	}
	return body.times(min(n, memLoopCap))
}

// substs is every command substitution and process substitution under node,
// each run before (or beside) the command around it. A nested one is read by
// the statements it holds.
func hasProcSubst(word *syntax.Word) bool {
	found := false
	// It descends into `$(...)` on purpose: a process substitution there may
	// outlive it, `printf %s <(go test ./x)` returns without waiting, so the
	// test runs beside the command. `go test -run "$(cat <(go test ./x -list
	// .))" ./...` over-books 9 GB for 6, the side that refuses (Codex
	// re-verify 1 raised it; stopping at `$(...)` under-booked: round 2).
	syntax.Walk(word, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.ProcSubst); ok {
			found = true
		}
		return !found
	})
	return found
}

func (w *memWalker) substs(node syntax.Node, depth int) memCost {
	c := memCost{}
	if node == nil {
		return c
	}
	// A typed nil (*syntax.Word)(nil) inside the interface would panic in Walk.
	if word, ok := node.(*syntax.Word); ok && word == nil {
		return c
	}
	syntax.Walk(node, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.CmdSubst:
			c = seq(c, w.stmts(x.Stmts, depth))
			return false
		case *syntax.ProcSubst:
			c = par(c, w.stmts(x.Stmts, depth))
			return false
		}
		return true
	})
	return c
}

// andChain reads `a && b && c` (left-associative in the tree) as one chain, so
// a self-gated `[cd X &&] buddy mem admit K... --wait N && cmd...` exempts the
// commands chained after the admit by `&&`, and nothing else: an `||` ends the
// chain (`admit ... && go test ./... || echo FAIL` judges the echo), and every
// other statement of the call is judged. The local gate exempted any command
// that merely CONTAINED its admit, so `admit codex; go test -race ./...` booked
// 0.5 GB for a 6 GB start. The admit must also COVER its tail: a tail heavier
// than the kinds it admits is refused, naming the kinds that cover it.
func (w *memWalker) andChain(x *syntax.BinaryCmd, depth int) memCost {
	var ops []*syntax.Stmt
	var flatten func(s *syntax.Stmt)
	flatten = func(s *syntax.Stmt) {
		if b, ok := s.Cmd.(*syntax.BinaryCmd); ok && b.Op == syntax.AndStmt && memPlainStmt(s) {
			flatten(b.X)
			flatten(b.Y)
			return
		}
		ops = append(ops, s)
	}
	flatten(x.X)
	flatten(x.Y)
	c := memCost{}
	for i := 0; i < len(ops); i++ {
		kinds, ok := admitLeaf(ops[i])
		if !ok {
			c = seq(c, w.stmt(ops[i], depth))
			continue
		}
		// What the admit's own redirections and assignments run, runs before
		// it and is judged: `admit goall >"$(go test ./...; ...)" && ...`
		// started a 6 GB test unbooked once the redirection was allowed
		// (Codex re-verify 1). Its arguments are all literals, or it is no
		// admit.
		c = seq(c, w.admitSubsts(ops[i], depth))
		// An admit covers the tail up to the next admit, which covers its own:
		// `admit lane && herdr agent start a && admit lane && herdr agent start
		// b` is the "one admit per start" the refusal teaches, and it was
		// refused as one lane admit under two starts (Codex code pass).
		j := i + 1
		tail := memCost{}
		for ; j < len(ops); j++ {
			if _, next := admitLeaf(ops[j]); next {
				break
			}
			tail = seq(tail, w.stmt(ops[j], depth))
		}
		covered := 0.0
		for _, k := range kinds {
			covered += memKindCost[k]
		}
		if tail.total() > covered && w.under == "" {
			w.under = fmt.Sprintf("its `buddy mem admit %s` covers %s GB, and the commands chained after it start %s GB (%s). Admit what they start, `buddy mem admit %s --wait 900 && ...` (lane starts: one admit per start), or split the call",
				strings.Join(kinds, " "), gb(covered), gb(tail.total()), strings.Join(tail.kinds(), ", "), strings.Join(tail.kinds(), " "))
			return seq(c, tail)
		}
		i = j - 1
	}
	return c
}

// admitSubsts: the substitutions in an admit statement's redirections and its
// command's assignments.
func (w *memWalker) admitSubsts(s *syntax.Stmt, depth int) memCost {
	c := memCost{}
	for _, r := range s.Redirs {
		c = seq(c, w.substs(r, depth))
	}
	if call, ok := s.Cmd.(*syntax.CallExpr); ok {
		for _, a := range call.Assigns {
			c = seq(c, w.substs(a, depth))
		}
	}
	return c
}

// memPlainStmt: no `!`, no `&`, no redirection, so its operands are the chain's.
func memPlainStmt(s *syntax.Stmt) bool { return !s.Negated && !s.Background && len(s.Redirs) == 0 }

// admitLeaf recognizes `buddy mem admit <kind>...` (buddy, or any path ending
// /buddy), optionally through `env` with its options and assignments (`env -u
// CLAUDE_CODE_SESSION_ID buddy mem admit ...`, the identity remedy a Codex
// lane is told to use; Codex design pass). Every kind must be one the slot
// knows; anything else is not an admit and is judged as what it is.
func admitLeaf(s *syntax.Stmt) ([]string, bool) {
	call, ok := s.Cmd.(*syntax.CallExpr)
	// Negated or backgrounded, the admit no longer gates its tail; a
	// redirection (`2>/dev/null`, silencing the waits) leaves it gating
	// (Fable cross-check: it was judged as no admit, the tail booked twice).
	if !ok || s.Negated || s.Background || len(call.Args) == 0 {
		return nil, false
	}
	words := make([]shellWord, 0, len(call.Args))
	for _, a := range call.Args {
		words = append(words, decodeWord(a))
	}
	if len(words) > 0 && words[0].known && commandName(words[0]) == "env" {
		words = skipOptions(words[1:], wrapperOptions["env"])
		for len(words) > 0 && words[0].known && isAssignment(words[0].text) {
			words = words[1:]
		}
	}
	if len(words) < 4 || !words[0].known || commandName(words[0]) != "buddy" {
		return nil, false
	}
	if !words[1].known || words[1].text != "mem" || !words[2].known || words[2].text != "admit" {
		return nil, false
	}
	var kinds []string
	for _, wd := range words[3:] {
		if !wd.known {
			return nil, false
		}
		if strings.HasPrefix(wd.text, "-") {
			break
		}
		if _, ok := memKindCost[wd.text]; !ok {
			return nil, false
		}
		kinds = append(kinds, wd.text)
	}
	return kinds, len(kinds) > 0
}

func isAssignment(s string) bool {
	name, _, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// skipOptions drops a wrapper's leading options, with the value of each
// option in valued; a bare `--` ends them.
func skipOptions(words []shellWord, valued map[string]bool) []shellWord {
	for len(words) > 0 && words[0].known && strings.HasPrefix(words[0].text, "-") && words[0].text != "-" {
		a := words[0].text
		words = words[1:]
		if a == "--" {
			break
		}
		if !strings.Contains(a, "=") && valued[a] && len(words) > 0 {
			words = words[1:]
		}
	}
	return words
}

func (w *memWalker) call(x *syntax.CallExpr, s *syntax.Stmt, depth int) memCost {
	c := memCost{}
	race := false
	for _, as := range x.Assigns {
		if as.Value != nil {
			c = seq(c, w.substs(as.Value, depth))
		}
		if as.Array != nil {
			c = seq(c, w.substs(as.Array, depth))
		}
		if as.Name == nil || as.Value == nil {
			continue
		}
		v := decodeWord(as.Value)
		switch as.Name.Value {
		case memOverrideVar:
			if v.known && strings.TrimSpace(v.text) != "" && w.override == "" {
				w.override = v.text
			}
		case "GOFLAGS":
			if v.known && hasRaceFlag(strings.Fields(v.text)) {
				race = true
			}
		}
	}
	// A command substitution runs before the command; a process substitution
	// runs beside it and beside every other one (`diff <(go test ./a) <(go
	// test ./b)` is two runs at once: Codex code pass). The argument walk is
	// split so the second sums.
	ps := memCost{}
	for _, a := range x.Args {
		if hasProcSubst(a) {
			ps = par(ps, w.substs(a, depth))
		} else {
			c = seq(c, w.substs(a, depth))
		}
	}
	words := make([]shellWord, 0, len(x.Args))
	for _, a := range x.Args {
		words = append(words, decodeWord(a))
	}
	return par(seq(c, w.program(words, s, depth, race)), ps)
}

// program classifies a command's words: wrappers first, then the program.
func (w *memWalker) program(words []shellWord, s *syntax.Stmt, depth int, race bool) memCost {
	mult := 1
	for len(words) > 0 {
		if !words[0].known {
			return memCost{} // a dynamic command name: cooperative
		}
		name := commandName(words[0])
		rest := words[1:]
		switch {
		case name == "env":
			rest = skipOptions(rest, wrapperOptions["env"])
			for len(rest) > 0 && rest[0].known && isAssignment(rest[0].text) {
				if n, v, _ := strings.Cut(rest[0].text, "="); n == "GOFLAGS" && hasRaceFlag(strings.Fields(v)) {
					race = true
				}
				rest = rest[1:]
			}
			words = rest
			continue
		case name == "xargs":
			if p, ok := xargsProcs(rest); ok && p != 1 {
				if p <= 0 || p > memLoopFanout {
					p = memLoopFanout // -P 0 is unlimited (Codex design pass)
				}
				mult = max(mult, p)
			}
			words = skipOptions(rest, wrapperOptions["xargs"])
			continue
		case name == "command" && lookupOnly(rest):
			return memCost{} // command -v / -V: a lookup
		case wrapperOptions[name] != nil:
			rest = skipOptions(rest, wrapperOptions[name])
			if n := wrapperLeadOperands[name]; n > 0 && len(rest) >= n {
				rest = rest[n:]
			}
			words = rest
			continue
		}
		c := w.named(name, rest, s, depth, race)
		if mult > 1 && c.total() > 0 {
			c = c.times(mult)
			c.fanout = true
		}
		return c
	}
	return memCost{}
}

func xargsProcs(words []shellWord) (int, bool) {
	for i := 0; i < len(words); i++ {
		wd := words[i]
		if !wd.known || !strings.HasPrefix(wd.text, "-") || wd.text == "--" {
			break
		}
		var v string
		switch {
		case wd.text == "-P" || wd.text == "--max-procs":
			if i+1 < len(words) && words[i+1].known {
				v = words[i+1].text
			}
		case strings.HasPrefix(wd.text, "--max-procs="):
			v = strings.TrimPrefix(wd.text, "--max-procs=")
		case strings.HasPrefix(wd.text, "-P"):
			v = strings.TrimPrefix(wd.text, "-P")
		default:
			if wrapperOptions["xargs"][wd.text] {
				i++ // -n 1: its value is not the command
			}
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return memLoopFanout, true // unknown parallelism: the unknown count
		}
		return n, true
	}
	return 1, false
}

func (w *memWalker) named(name string, rest []shellWord, s *syntax.Stmt, depth int, race bool) memCost {
	arg := func(i int) string {
		if i < len(rest) && rest[i].known {
			return rest[i].text
		}
		return ""
	}
	switch name {
	case "go":
		return goCost(rest, race)
	case "golangci-lint":
		if arg(0) == "run" {
			return kindCost("gotest", false)
		}
	case "herdr":
		if arg(0) == "agent" && arg(1) == "start" {
			return kindCost("lane", true)
		}
	case "claude":
		return claudeCost(rest)
	case "codex":
		if arg(0) == "exec" || arg(0) == "e" {
			return kindCost("codex", false)
		}
	case "podman":
		if arg(0) == "machine" && arg(1) == "start" {
			return kindCost("vm", true)
		}
	case "buddy":
		// `buddy update` builds every binary; its --help and --dry-run
		// build nothing (Codex code pass: both were refused at 6 GB free).
		if arg(0) == "update" && !hasAnyWord(rest[1:], "--help", "-h", "help", "--dry-run") {
			return kindCost("gotest", false)
		}
	}
	if scriptShells[name] {
		return w.shell(rest, s, depth)
	}
	if k := scriptKind(name); k != "" {
		return kindCost(k, false)
	}
	return memCost{}
}

// scriptKind names the scripts the slot books by name: a test tier, a
// done-check, a Codex pass, an install (which builds every binary).
func scriptKind(name string) string {
	switch {
	case name == "check.sh" || name == "check-all.sh":
		return "tier"
	case strings.HasPrefix(name, "check-") && strings.HasSuffix(name, ".sh"):
		return "check"
	case name == "codex-review.sh":
		return "codex"
	case name == "install.sh":
		return "gotest"
	}
	return ""
}

// shell reads sh/bash/zsh's own arguments: `-n` runs nothing (a syntax
// check); `-c` (alone or in a cluster such as `-lc`) runs its operand, read
// when it is a literal; a script operand is booked by its name; no operand
// reads the script from stdin, a heredoc or here-string read when literal.
func (w *memWalker) shell(rest []shellWord, s *syntax.Stmt, depth int) memCost {
	command := false
	for len(rest) > 0 && rest[0].known && (strings.HasPrefix(rest[0].text, "-") || strings.HasPrefix(rest[0].text, "+")) && len(rest[0].text) > 1 {
		a := rest[0].text
		rest = rest[1:]
		if a == "--" || a == "-" {
			break
		}
		if strings.HasPrefix(a, "--") {
			continue // --login, --norc, --posix ...
		}
		if a[0] == '-' && strings.ContainsRune(a, 'n') {
			return memCost{}
		}
		if strings.ContainsRune(a, 'c') {
			command = true
		}
		if (strings.ContainsRune(a, 'o') || strings.ContainsRune(a, 'O')) && len(rest) > 0 {
			rest = rest[1:] // -o pipefail, -O extglob
		}
	}
	if command {
		if len(rest) == 0 || !rest[0].known {
			return memCost{}
		}
		return w.script(rest[0].text, depth+1)
	}
	if len(rest) > 0 {
		if !rest[0].known {
			return memCost{}
		}
		if k := scriptKind(commandName(rest[0])); k != "" {
			return kindCost(k, false)
		}
		return memCost{}
	}
	if s == nil {
		return memCost{}
	}
	for _, r := range s.Redirs {
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			if r.Hdoc != nil {
				if text, ok := shellLiteral(r.Hdoc.Parts, false); ok {
					return w.script(text, depth+1)
				}
			}
		case syntax.WordHdoc:
			if r.Word != nil {
				if sw := decodeWord(r.Word); sw.known {
					return w.script(sw.text, depth+1)
				}
			}
		}
	}
	return memCost{}
}

// goValueFlags are the go command's build and test flags that take a value as
// the next argument, so the value is not read as a package.
var goValueFlags = set("-run", "-bench", "-count", "-cpu", "-parallel", "-timeout", "-p", "-tags", "-o",
	"-coverprofile", "-cpuprofile", "-memprofile", "-blockprofile", "-mutexprofile", "-trace", "-outputdir",
	"-exec", "-ldflags", "-gcflags", "-asmflags", "-mod", "-modfile", "-overlay", "-pkgdir", "-toolexec",
	"-covermode", "-coverpkg", "-benchtime", "-shuffle", "-skip", "-fuzz", "-fuzztime", "-fuzzminimizetime",
	"-list", "-C", "-memprofilerate", "-blockprofilerate", "-mutexprofilefraction", "-installsuffix",
	"-buildmode", "-compiler", "-gccgoflags", "-pgo", "-vet", "-test.run", "-test.count", "-test.timeout")

// goCost: `go test|build|vet|install|run` books gotest for one package,
// goall for a `...` pattern or two or more packages, race for -race on one
// package (over `...` the heavier goall: the local gate booked race 5 there).
func goCost(rest []shellWord, race bool) memCost {
	for len(rest) >= 2 && rest[0].known && rest[0].text == "-C" {
		rest = rest[2:]
	}
	if len(rest) == 0 || !rest[0].known {
		return memCost{}
	}
	sub := rest[0].text
	switch sub {
	case "test", "build", "vet", "install", "run":
	default:
		return memCost{}
	}
	pkgs, all := 0, false
	args := rest[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.known {
			pkgs++ // `go test $pkgs`: read as one (D-238 records it open)
			continue
		}
		t := a.text
		if t == "-args" || t == "--args" {
			break
		}
		if strings.HasPrefix(t, "-") {
			flag := "-" + strings.TrimLeft(t, "-")
			name, _, inline := strings.Cut(flag, "=")
			if name == "-race" && (!inline || strings.HasSuffix(flag, "=true")) {
				race = true
			}
			if !inline && goValueFlags[name] {
				i++
			}
			continue
		}
		pkgs++
		if strings.HasSuffix(t, "...") || mayExpand(t) {
			all = true // `./internal/{cli,store}` or `./internal/*/` is several (Fable)
		}
		if sub == "run" {
			break // what follows is the program's own arguments
		}
	}
	switch {
	case all || pkgs >= 2:
		return kindCost("goall", false)
	case race:
		return kindCost("race", false)
	}
	return kindCost("gotest", false)
}

// mayExpand: an unquoted literal the shell would still turn into several
// words, a glob or a brace expansion. Over-reading here books more, never less.
func mayExpand(t string) bool {
	return strings.ContainsAny(t, "*?[") || (strings.Contains(t, "{") && strings.Contains(t, "}"))
}

// wordMayExpand is mayExpand over a word's unquoted text only, each quoted
// part standing in as one plain character, so `{a,'b'}` still reads as a
// brace expansion while `'^Test[A-Z]'` does not.
func wordMayExpand(word *syntax.Word) bool {
	var b strings.Builder
	for _, part := range word.Parts {
		if lit, ok := part.(*syntax.Lit); ok {
			b.WriteString(lit.Value)
		} else {
			b.WriteByte('q')
		}
	}
	return mayExpand(b.String())
}

func hasAnyWord(words []shellWord, want ...string) bool {
	for _, wd := range words {
		if wd.known && slices.Contains(want, wd.text) {
			return true
		}
	}
	return false
}

func hasRaceFlag(fields []string) bool {
	for _, f := range fields {
		if f == "-race" || f == "--race" || f == "-race=true" {
			return true
		}
	}
	return false
}

// claudeCost: a Claude session start. --bg and the resume/session flags start
// a lane that outlives the call (detached); -p/--print runs one turn in the
// call's own process (foreground).
func claudeCost(rest []shellWord) memCost {
	lane, print := false, false
	for _, wd := range rest {
		if !wd.known {
			continue
		}
		flag, _, _ := strings.Cut(wd.text, "=")
		switch flag {
		case "-p", "--print":
			print = true
		case "--bg", "--resume", "-r", "--continue", "-c", "--session-id", "-n":
			lane = true
		}
	}
	switch {
	case print:
		return kindCost("lane", false)
	case lane:
		return kindCost("lane", true)
	}
	return memCost{}
}

// gb formats a GB figure the way the refusal prints it.
func gb(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
