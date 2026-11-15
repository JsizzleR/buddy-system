package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE GATE NEVER REACHES THE FLEET LEDGER (D-191, the plan's 5.4): a
// source-shape gate on internal/cli's call graph, not a grep. The verdict
// tests (TestGateVerdictsIgnoreTheFleet) hold the gate's OUTPUT; they cannot
// see an open whose result is thrown away, which prints nothing and changes no
// verdict until the day a later edit reads it. This holds the structure.
//
// THE GRAPH over-approximates where it cheaply can. Every identifier in a
// function's body that names a package-level function, a method (by name,
// whatever the receiver, so a call through an interface reaches every
// same-named method in this package), or a package-level variable or constant
// is an edge, save a bare one an assignment only stores into (`seam = nil`
// calls nothing). A package-level variable's edges are its initializer's AND
// the right-hand side of every assignment to it anywhere in the package
// (Codex code pass, finding C4: a seam such as afterAncestorDiscovery, set in
// one function and called from resolveRepo, was an empty node, so a function
// no root reaches could plant an open the gate then ran). Over-approximation
// costs a false alarm, which is read and answered; the by-name method rule is
// the loudest source of one (every `.Error()` call reaches every Error
// method).
//
// ENVFLEET IS AN EDGE ONLY WHERE IT IS READ: EnvFleet, or a "BUDDY_FLEET" /
// "BUDDY_FLEET=" literal, alone, concatenated or as a composite-literal
// element, as an argument of a call named getenv, Getenv, LookupEnv, Setenv or
// Unsetenv (env.getenv, os.Getenv, os.LookupEnv, syscall.Getenv,
// os.Unsetenv); and, so a local alias cannot hide a read (`k := EnvFleet;
// env.getenv(k)`, Codex re-verify round 1), any function whose body both
// mentions EnvFleet or the literal AND makes such a call. A mention as message
// text is not a read: the first walk that counted every mention flagged
// cmdGate -> Error -> EnvFleet, the by-name edge into fleetErr.Error's
// Sprintf, which calls no getenv and which no gate path can construct.
//
// WHAT IT DOES NOT SEE, each a way for this test to pass when it should not:
//   - code outside package cli: internal/store and every dependency are not
//     walked, so a resolver reached through one is not seen here;
//   - reflection, and an interface call whose implementation lives outside
//     the package;
//   - a method called from OUTSIDE the package: types are not nodes, so fmt
//     reaching a String or Error method through %v, or flag reaching a Set
//     method, is an edge only where this package names the method itself;
//   - Run's pre-dispatch use of the verbs table, which the skip that keeps
//     dispatch out of Run's root hides as well (TestGateVerdictsIgnoreTheFleet
//     holds that behaviour, and check.sh pins it by name);
//   - a func value carried through a local, a parameter, a struct field of a
//     non-package value, a channel or a pointer, when the only function that
//     names it is itself unreachable (an assignment through `*p` is followed
//     only when p is the package variable itself);
//   - a function value a package-variable initializer passes INTO a call it
//     makes at init, which the callee then calls (the parameter case above;
//     the initializer's own calls are roots, see THE ROOTS);
//   - a verbs-row wrapper (errVerb, heardVerb) that does real work when it
//     is built: the verbs initializer's calls are not roots, because the
//     closures they build run only on dispatch;
//   - any other read of BUDDY_FLEET: an os.Environ scan, a getenv through a
//     variable or a wrapper of another name, the name passed to ANOTHER
//     function that reads it (a parameter), a dependency's read;
//   - a constant that inherits the previous spec's expression in a const
//     block (`const ( a = fleetHomeRel; b )`): its ValueSpec has no Values,
//     so neither the alias pass nor the graph sees b name fleetHomeRel or
//     EnvFleet (Codex re-verify of D-191, round 2, finding 2; answered at the
//     round ceiling, not folded);
//   - _test.go files, which are not in the binary a gate process runs.
//
// THE ROOTS are where a `buddy gate` or `buddy commit-gate` process runs code:
// cmdGate, cmdCommitGate, every package-level `func init()` (Go runs each in
// every process before main; Codex code pass, finding C4), every call a
// package-variable INITIALIZER evaluates, the body of an immediately invoked
// func literal included (Codex re-verify round 1: `var probe = func() bool {
// openFleetLedger(...) }()` opened the fleet before every command; both join
// the init node, while a function only NAMED there, as the verbs table names
// every verb, is a value and not a root), and Run's own
// pre-dispatch body (Codex design pass, finding 3: an open placed in Run before
// `v.run`, when cmd == "gate", is reached from neither cmd function and
// changes no verdict). Run is walked WITHOUT its edge into the `verbs` table,
// which is the dynamic dispatch whose gate rows are the two cmd roots;
// following it would reach every legitimate fleet verb. cmd/buddy's main is
// held to calling cli.Run and naming cli.Env, nothing else.
//
// READING BUDDY_FLEET is forbidden too, from cmdGate, cmdCommitGate and init
// (finding C4): a read is where a verdict would start to depend on the fleet,
// before any resolver is named. NOT from Run: its fleetScrub reads EnvFleet to
// decide whether a TYPED verb discovers from the filesystem, short-circuits on
// fleetVerbs[cmd] (which never holds a gate verb), and is held by the resolver
// clause like the rest of Run's body. The test asserts Run itself is not
// reachable from the other roots, so fleetScrub's read counts for none of them.
//
// TWO CLAUSES, as CLAUDE.md asks of a source-shape gate: the scan (nothing
// forbidden reachable from a root), and controls the scan must agree with, so
// a respelled resolver or a broken walk cannot pass by finding nothing: the
// resolver and EnvFleet ARE reachable where they belong, and planted packages,
// one per root and per edge kind (Run's body, a method hop from cmdGate, a
// reach from cmdCommitGate alone, init, a package-variable seam, a read of
// BUDDY_FLEET), are flagged while their clean twin, and a gate path that only
// formats EnvFleet into a message, are not.

// fleetForbidden are the functions that open, or walk to, a fleet ledger, and
// fleetHomeRel, the fleet home under $HOME: a mention is an edge, so a path
// built by hand (`store.OpenEscaped(filepath.Join(home, fleetHomeRel, ...))`)
// is caught where no resolver is named (Fable cross-check, F3b).
var fleetForbidden = []string{"openFleetLedger", "openSessionLedger", "mustFleetLedger", "fleetLedgerPath", "cmdInitFleet", "fleetHomeRel"}

// pkgInit is the one node for every package-level `func init()`: nothing can
// name or call one, so it is a root and never an edge, and a method that
// happens to be called init stays a node of its own.
const pkgInit = "init#pkg"

// fleetEnvNode is EnvFleet's node, reached only through a read (envRead).
const fleetEnvNode = "EnvFleet"

// envCallees are the call names that read or write the environment.
var envCallees = map[string]bool{"getenv": true, "Getenv": true, "LookupEnv": true, "Setenv": true, "Unsetenv": true}

// calleeName is a call's function name, `f` or the selector of `x.f`.
func calleeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.ParenExpr:
		return calleeName(x.X)
	}
	return ""
}

// envRead reports whether an environment call's argument spells BUDDY_FLEET:
// EnvFleet, a package-level alias of it, or the literal, alone, in a `+`
// concatenation or as an element of a composite literal, through parentheses.
func envRead(e ast.Expr, aliases map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return aliases[x.Name]
	case *ast.BasicLit:
		s, err := strconv.Unquote(x.Value)
		return x.Kind == token.STRING && err == nil && (s == EnvFleet || strings.HasPrefix(s, EnvFleet+"="))
	case *ast.ParenExpr:
		return envRead(x.X, aliases)
	case *ast.BinaryExpr:
		return x.Op == token.ADD && (envRead(x.X, aliases) || envRead(x.Y, aliases))
	case *ast.CompositeLit:
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			if envRead(el, aliases) {
				return true
			}
		}
	}
	return false
}

// callGraph is a package's conservative identifier graph.
type callGraph struct {
	edges map[string]map[string]bool // node -> nodes it names
	run   map[string]bool            // Run's edges, minus the verbs table
}

func isPkgInit(d *ast.FuncDecl) bool { return d.Recv == nil && d.Name.Name == "init" }

// assignedVar is the package variable an assignment's left side writes, or
// "": `v = f`, `v.field = f`, `v[k] = f` and `*v = f` all store into what v
// names. A local of the same name is conflated with it (over-approximation).
func assignedVar(e ast.Expr, pkgVars map[string]bool) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			if pkgVars[x.Name] {
				return x.Name
			}
			return ""
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return ""
		}
	}
}

func buildCallGraph(t *testing.T, files []*ast.File) callGraph {
	t.Helper()
	nodes := map[string]bool{}
	pkgVars := map[string]bool{}
	var specs []*ast.ValueSpec
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !isPkgInit(d) {
					nodes[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						specs = append(specs, vs)
						for _, n := range vs.Names {
							nodes[n.Name] = true
							if d.Tok == token.VAR {
								pkgVars[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	// aliases are EnvFleet and every package-level constant or variable
	// whose initializer spells it or another alias, to a fixed point (Fable
	// cross-check, F3a: `const fleetKey = EnvFleet` at package level hid a
	// gate's env.getenv(fleetKey); round 1 closed only the local alias).
	aliases := map[string]bool{fleetEnvNode: true}
	for changed := true; changed; {
		changed = false
		for _, vs := range specs {
			spells := false
			for _, v := range vs.Values {
				ast.Inspect(v, func(x ast.Node) bool {
					switch x := x.(type) {
					case *ast.Ident:
						spells = spells || aliases[x.Name]
					case *ast.BasicLit:
						spells = spells || envRead(x, aliases)
					}
					return !spells
				})
			}
			for _, n := range vs.Names {
				if spells && !aliases[n.Name] {
					aliases[n.Name], changed = true, true
				}
			}
		}
	}
	g := callGraph{edges: map[string]map[string]bool{}, run: map[string]bool{}}
	add := func(from string, n ast.Node, skip string) {
		if g.edges[from] == nil {
			g.edges[from] = map[string]bool{}
		}
		// A bare identifier on an assignment's left is stored into, never
		// evaluated: `seam = nil` calls nothing, and an edge there would hand
		// the function every value any other function ever stored in seam
		// (measured: the clean twin's `func init() { typed = nil }` reached
		// cmdClaim's `typed = openFleetLedger`). `v.f = x` and `v[k] = x`
		// read v, and keep their edge.
		writes := map[*ast.Ident]bool{}
		ast.Inspect(n, func(x ast.Node) bool {
			if as, ok := x.(*ast.AssignStmt); ok && (as.Tok == token.ASSIGN || as.Tok == token.DEFINE) {
				for _, l := range as.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						writes[id] = true
					}
				}
			}
			return true
		})
		mentions, envCall := false, false // for the co-occurrence read
		ast.Inspect(n, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.Ident:
				mentions = mentions || aliases[x.Name]
				// EnvFleet's edge is a read, never a mention.
				if nodes[x.Name] && !writes[x] && x.Name != fleetEnvNode && x.Name != from && x.Name != skip {
					g.edges[from][x.Name] = true
				}
			case *ast.BasicLit:
				mentions = mentions || envRead(x, aliases)
			case *ast.CallExpr:
				if !envCallees[calleeName(x.Fun)] {
					break
				}
				envCall = true
				for _, a := range x.Args {
					if envRead(a, aliases) {
						g.edges[from][fleetEnvNode] = true
					}
				}
			}
			return true
		})
		if mentions && envCall && from != fleetEnvNode {
			g.edges[from][fleetEnvNode] = true
		}
	}
	// initCalls joins to the init node every call a package-variable
	// initializer evaluates, and the whole body of a func literal it invokes
	// on the spot. A func literal it does not invoke, and a function it only
	// names (`"claim": cmdClaim`, `errVerb("init", cmdInit)`'s argument), are
	// values: rooting them would root every verb through the verbs table.
	initCalls := func(e ast.Expr) {
		ast.Inspect(e, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.FuncLit:
				return false
			case *ast.CallExpr:
				fn := x.Fun
				for p, ok := fn.(*ast.ParenExpr); ok; p, ok = fn.(*ast.ParenExpr) {
					fn = p.X
				}
				if fl, ok := fn.(*ast.FuncLit); ok {
					add(pkgInit, fl.Body, "")
				} else if c := calleeName(fn); nodes[c] {
					if g.edges[pkgInit] == nil {
						g.edges[pkgInit] = map[string]bool{}
					}
					g.edges[pkgInit][c] = true
				}
			}
			return true
		})
	}
	// assigns gives a package variable the edges of whatever any assignment
	// in n stores into it, whichever function the assignment sits in: the
	// variable is the edge from a seam's setter to its caller. `:=` never
	// writes a package variable, so it is skipped; a multi-value right side
	// (`a, b = f()`) feeds every left side. A `for v = range` store is not
	// followed.
	assigns := func(n ast.Node) {
		ast.Inspect(n, func(x ast.Node) bool {
			as, ok := x.(*ast.AssignStmt)
			if !ok || as.Tok == token.DEFINE {
				return true
			}
			for i, l := range as.Lhs {
				v := assignedVar(l, pkgVars)
				if v == "" {
					continue
				}
				if len(as.Lhs) == len(as.Rhs) {
					add(v, as.Rhs[i], "")
					continue
				}
				for _, r := range as.Rhs {
					add(v, r, "")
				}
			}
			return true
		})
	}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				name := d.Name.Name
				if isPkgInit(d) {
					name = pkgInit
				}
				if d.Recv == nil && name == "Run" {
					// Run as a root: its body without the verbs table. Its
					// node in the graph keeps the table (a function that
					// called Run would dispatch through it).
					add("Run#root", d.Body, "verbs")
					g.run = g.edges["Run#root"]
					delete(g.edges, "Run#root")
				}
				add(name, d.Body, "")
				assigns(d.Body)
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, n := range vs.Names {
						for _, v := range vs.Values {
							add(n.Name, v, "")
						}
					}
					// The verbs table is dispatch, as Run#root's skip says. Its
					// errVerb and heardVerb calls do run at init, but only to
					// build closures, which run when a wrapped verb is
					// dispatched; the gate rows are not wrapped (cli.go:
					// `{usageGate, cmdGate}`), and the walk counts a
					// closure's body as its builder's, so rooting these calls
					// reached explainLedgerAccess and noteHeard from init.
					dispatch := len(vs.Names) == 1 && vs.Names[0].Name == "verbs"
					for _, v := range vs.Values {
						assigns(v)
						if d.Tok == token.VAR && !dispatch {
							initCalls(v)
						}
					}
				}
			}
		}
	}
	return g
}

// reach returns every node reachable from the roots, with the path to each.
func (g callGraph) reach(roots ...string) map[string][]string {
	path := map[string][]string{}
	var queue []string
	push := func(n string, via []string) {
		if _, seen := path[n]; seen {
			return
		}
		path[n] = append(append([]string{}, via...), n)
		queue = append(queue, n)
	}
	for _, r := range roots {
		if r == "Run" {
			path["Run"] = []string{"Run"}
			for n := range g.run {
				push(n, []string{"Run"})
			}
			continue
		}
		push(r, nil)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		next := make([]string, 0, len(g.edges[n]))
		for m := range g.edges[n] {
			next = append(next, m)
		}
		sort.Strings(next)
		for _, m := range next {
			push(m, path[n])
		}
	}
	return path
}

func parseDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, m, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s: the scan would pass on nothing", dir)
	}
	return files
}

func parseSrc(t *testing.T, src string) []*ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "planted.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return []*ast.File{f}
}

// forbiddenFrom names each forbidden function reachable from the gate's roots,
// with its path.
func forbiddenFrom(g callGraph) []string {
	r := g.reach("Run", pkgInit, "cmdGate", "cmdCommitGate")
	var hits []string
	for _, f := range fleetForbidden {
		if p, ok := r[f]; ok {
			hits = append(hits, strings.Join(p, " -> "))
		}
	}
	return hits
}

// envFleetFrom is the path to a read of BUDDY_FLEET from the gate verbs or
// init, or "". Run is not a root here (the header says why).
func envFleetFrom(g callGraph) string {
	if p, ok := g.reach(pkgInit, "cmdGate", "cmdCommitGate")[fleetEnvNode]; ok {
		return strings.Join(p, " -> ")
	}
	return ""
}

func TestGateNeverReachesTheFleetLedger(t *testing.T) {
	boundedParallel(t)
	g := buildCallGraph(t, parseDir(t, "."))

	// Clause 1: the scan.
	if hits := forbiddenFrom(g); len(hits) > 0 {
		t.Fatalf("a gate process can reach the fleet ledger (D-191, plan 5.4):\n  %s", strings.Join(hits, "\n  "))
	}
	if p := envFleetFrom(g); p != "" {
		t.Fatalf("a gate verb or init reads BUDDY_FLEET (D-191, plan 5.4):\n  %s", p)
	}
	// Run must stay out of the non-Run roots' reach: from there it would
	// dispatch through the verbs table to every verb, and fleetScrub's read,
	// excused only as Run's own, would count for the gate.
	if p, ok := g.reach(pkgInit, "cmdGate", "cmdCommitGate")["Run"]; ok {
		t.Fatalf("Run is reachable from a gate verb or init, so every verb is: %s", strings.Join(p, " -> "))
	}

	// Clause 2a: the resolver IS reachable where it belongs, so a respelled
	// resolver (or a walk that finds nothing) cannot pass by absence.
	for _, from := range []string{"cmdHello", "cmdBeat", "cmdBusy", "cmdIdle", "cmdBye", "cmdClaim", "cmdMsg", "noteHeard", "beatOffSuccess"} {
		if _, ok := g.reach(from)["openFleetLedger"]; !ok {
			t.Fatalf("control: openFleetLedger is not reachable from %s; the scan above proves nothing until it is", from)
		}
	}
	// So is EnvFleet: Run's fleetScrub and the resolver read it, so a
	// respelled constant cannot pass the read clause by absence.
	for _, from := range []string{"Run", "openFleetLedger"} {
		if _, ok := g.reach(from)[fleetEnvNode]; !ok {
			t.Fatalf("control: %s is not reachable from %s; the read clause above proves nothing until it is", fleetEnvNode, from)
		}
	}
	// And the roots are real: each reaches what a gate does reach.
	r := g.reach("Run", pkgInit, "cmdGate", "cmdCommitGate")
	for _, n := range []string{"openRepo", "openGateRepo", "scrubHookGitEnv", "decodeHook", "hooksBinaryPermit"} {
		if _, ok := r[n]; !ok {
			t.Fatalf("control: %s is not reachable from the gate's roots; the walk is broken", n)
		}
	}

	// Clause 2b: planted packages, one per root and per edge kind, each
	// flagged by the clause it names; the clean twin, whose Run reads
	// BUDDY_FLEET through fleetScrub and whose typed verb sets and calls a
	// seam the gate never names, is flagged by neither.
	planted := []struct {
		name          string
		src           string
		resolver, env bool // forbiddenFrom flags it; envFleetFrom flags it
	}{
		{"finding 3: an open in Run's pre-dispatch body", `package p
var verbs = map[string]func(){"claim": cmdClaim}
func Run(cmd string) { if cmd == "gate" { openFleetLedger() }; verbs[cmd]() }
func cmdGate() {}
func cmdCommitGate() {}
func cmdClaim() { openFleetLedger() }
func openFleetLedger() {}`, true, false},
		{"two hops from cmdGate through a method", `package p
var verbs = map[string]func(){"gate": cmdGate}
func Run(cmd string) { verbs[cmd]() }
func cmdGate() { helper() }
func helper() { var e env; e.open() }
type env struct{}
func (env) open() { mustFleetLedger() }
func cmdCommitGate() {}
func mustFleetLedger() {}`, true, false},
		// A walk rooted at cmdGate alone (or one that dropped the
		// commit-gate root) passes this one.
		{"C4: cmdCommitGate alone reaches a resolver", `package p
func cmdGate() { openRepo() }
func cmdCommitGate() { stagedLedger() }
func stagedLedger() { fleetLedgerPath() }
func openRepo() {}
func fleetLedgerPath() {}`, true, false},
		// The init body runs in every process; the seam it sets is named by
		// nothing the gate reaches, so only the init root finds this.
		{"C4: init plants an open", `package p
var afterAncestorDiscovery func()
func init() { afterAncestorDiscovery = func() { openFleetLedger() } }
func cmdGate() {}
func cmdCommitGate() {}
func openFleetLedger() {}`, true, false},
		// The setter is unreachable from every root; only the variable's
		// assignment edge connects the gate's call to the open.
		{"C4: a package-variable seam set elsewhere", `package p
var seam func()
func setSeam() { seam = func() { openSessionLedger() } }
func cmdGate() { resolveRepo() }
func resolveRepo() { if seam != nil { seam() } }
func cmdCommitGate() {}
func openSessionLedger() {}`, true, false},
		// Every assignment counts, not just the initializer, and a func NAME
		// on the right is an edge like a literal is.
		{"C4: a reassigned func variable", `package p
var open = openRepo
func swap() { open = cmdInitFleet }
func cmdGate() {}
func cmdCommitGate() { open() }
func openRepo() {}
func cmdInitFleet() {}`, true, false},
		{"C4: cmdGate reads EnvFleet", `package p
const EnvFleet = "BUDDY_FLEET"
func cmdGate() { verdict() }
func verdict() { _ = os.Getenv(EnvFleet) }
func cmdCommitGate() {}`, false, true},
		{"C4: cmdCommitGate reads the literal", `package p
func cmdGate() {}
func cmdCommitGate() { _, _ = os.LookupEnv("BUDDY_FLEET") }`, false, true},
		// The real tree's shape: a by-name Error edge into a method that only
		// formats EnvFleet into its sentence. A mention, not a read.
		{"C4: cmdGate reaches EnvFleet only as message text", `package p
const EnvFleet = "BUDDY_FLEET"
type fleetErr struct{ name string }
func (e fleetErr) Error() string { return fmt.Sprintf("%s=%s names no fleet", EnvFleet, e.name) }
func cmdGate() { var err error; println(err.Error()) }
func cmdCommitGate() {}`, false, false},
		{"C4: cmdGate reaches env.getenv(EnvFleet)", `package p
const EnvFleet = "BUDDY_FLEET"
type fleetErr struct{ name string }
func (e fleetErr) Error() string { return fmt.Sprintf("%s=%s names no fleet", EnvFleet, e.name) }
func cmdGate() { var env Env; ledgerFor(env) }
func ledgerFor(env Env) { _ = env.getenv(EnvFleet) }
func cmdCommitGate() {}`, false, true},
		// Declared before the alias it names, so one pass in source order
		// would miss it; the fixed point does not.
		{"F3a: a package-level alias carries EnvFleet to getenv", `package p
const fleetKey = fleetName
const fleetName = EnvFleet
const EnvFleet = "BUDDY_FLEET"
func cmdGate() { var env Env; _ = env.getenv(fleetKey) }
func cmdCommitGate() {}`, false, true},
		// HOME is read here too, which must not count as a fleet read.
		{"F3b: a gate root builds the fleet home's path by hand", `package p
const fleetHomeRel = ".buddy/fleets"
func cmdGate() { home := os.Getenv("HOME"); st, _ := store.OpenEscaped(filepath.Join(home, fleetHomeRel, "f", "buddy.db")); _ = st }
func cmdCommitGate() {}`, true, false},
		{"re-verify 4: a local alias carries EnvFleet to getenv", `package p
const EnvFleet = "BUDDY_FLEET"
func cmdGate() { var env Env; verdict(env) }
func verdict(env Env) { fleetKey := EnvFleet; _ = env.getenv(fleetKey) }
func cmdCommitGate() {}`, false, true},
		// Nothing names fleetProbe; its initializer runs before every
		// command, the gate's included.
		{"re-verify 3: an invoked initializer opens the fleet", `package p
var fleetProbe = func() bool { s, _, err := openFleetLedger(Env{}); if err == nil { s.Close() }; return true }()
func cmdGate() {}
func cmdCommitGate() {}
func openFleetLedger(env Env) (*store.Store, repoContext, error) { return nil, repoContext{}, nil }`, true, false},
		// The real verbs table's shape: rows name verbs as values, and the
		// one call evaluated at init (wrap) only closes over its argument.
		{"re-verify 3: a dispatch table only names a resolver", `package p
var verbs = map[string]func(){"claim": cmdClaim, "init": wrap(cmdInitFleet)}
var lazy = func() { openFleetLedger() }
func wrap(fn func()) func() { return func() { fn() } }
func cmdGate() {}
func cmdCommitGate() {}
func cmdClaim() { openFleetLedger() }
func cmdInitFleet() {}
func openFleetLedger() {}`, false, false},
		{"C4: init reads EnvFleet", `package p
const EnvFleet = "BUDDY_FLEET"
var fleet string
func init() { fleet = os.Getenv(EnvFleet) }
func cmdGate() {}
func cmdCommitGate() {}`, false, true},
		{"clean twin", `package p
const EnvFleet = "BUDDY_FLEET"
var verbs = map[string]func(){"claim": cmdClaim, "gate": cmdGate}
var typed func()
func init() { typed = nil }
func Run(cmd string) { _ = fleetScrub(cmd); verbs[cmd]() }
func fleetScrub(cmd string) bool { return cmd == "claim" && os.Getenv(EnvFleet) != "" }
func cmdGate() { openRepo() }
func cmdCommitGate() {}
func cmdClaim() { typed = openFleetLedger; typed() }
func openRepo() {}
func openFleetLedger() {}`, false, false},
	}
	for _, p := range planted {
		pg := buildCallGraph(t, parseSrc(t, p.src))
		gotR, gotE := len(forbiddenFrom(pg)) > 0, envFleetFrom(pg) != ""
		if gotR != p.resolver || gotE != p.env {
			t.Fatalf("control %q: flagged resolver=%v env=%v, want %v %v — the scan is broken, not the tree:\n%s", p.name, gotR, gotE, p.resolver, p.env, p.src)
		}
	}
}

// cmd/buddy's main runs before Run's dispatch: it may call cli.Run and name
// cli.Env, and nothing else of the package, so nothing there can open a fleet
// for a gate process.
func TestBuddyMainOnlyCallsRun(t *testing.T) {
	boundedParallel(t)
	src, err := os.ReadFile(filepath.Join("..", "..", "cmd", "buddy", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if se, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := se.X.(*ast.Ident); ok && x.Name == "cli" {
				seen[se.Sel.Name] = true
			}
		}
		return true
	})
	if !seen["Run"] {
		t.Fatalf("control: cmd/buddy/main.go no longer names cli.Run; re-point this test")
	}
	for n := range seen {
		if n != "Run" && n != "Env" {
			t.Fatalf("cmd/buddy/main.go names cli.%s before Run's dispatch; a gate process would run it (D-191)", n)
		}
	}
}
