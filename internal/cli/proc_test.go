package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issue #50 (D-058): a `claude --bg` lane retitles its own argv to
// "claude bg-spare", so the walk passed it and anchored the session at the
// shared claude daemon. The trees below are the measured shapes (2026-09-27,
// 2.1.283), written as the procargs2 names the walk reads: the exec path
// first, then argv[0].

type fakeProc struct {
	ppid  int
	names []string
	born  int64
}

func fakeTree(tree map[int]fakeProc) func(int) (int, []string, int64, bool) {
	return func(pid int) (int, []string, int64, bool) {
		p, ok := tree[pid]
		return p.ppid, p.names, p.born, ok
	}
}

const (
	versioned = "/Users/me/.local/share/claude/versions/2.1.283" // base "2.1.283"
	launcher  = "/Users/me/.local/bin/claude"
)

// measured is the --bg chain as it was measured: hook -> lane -> pty host ->
// daemon -> launchd. The hook's DIRECT parent is the lane (a hook's ppid was
// the pid `claude agents --json` names).
func measured() map[int]fakeProc {
	return map[int]fakeProc{
		300: {ppid: 301, names: []string{versioned, "claude bg-spare"}, born: 3},
		301: {ppid: 302, names: []string{versioned, "claude bg-pty-host"}, born: 2},
		302: {ppid: 1, names: []string{launcher, launcher}, born: 1},
	}
}

func TestAnchorWalk(t *testing.T) {
	boundedParallel(t)
	interactive := func() map[int]fakeProc {
		return map[int]fakeProc{
			100: {ppid: 50, names: []string{launcher, "claude"}, born: 10},
			50:  {ppid: 1, names: []string{"/bin/zsh", "-zsh"}, born: 5},
			// A retitled DESCENDANT of the interactive session (a helper, a
			// subagent host): below the hook's parent, so never on its walk.
			110: {ppid: 100, names: []string{versioned, "claude helper"}, born: 11},
			// A wrapper around the hook line.
			200: {ppid: 100, names: []string{"/bin/sh", "sh"}, born: 20},
		}
	}
	for _, tc := range []struct {
		name         string
		tree         map[int]fakeProc
		start        int
		wantPID      int // 0 = unbound
		wantRetitled bool
	}{
		{"interactive, hook's direct parent", interactive(), 100, 100, false},
		{"interactive through a wrapper", interactive(), 200, 100, false},
		{"a --bg lane anchors at itself, not the daemon", measured(), 300, 300, true},
		{"control: the daemon alone is still an exact match", measured(), 302, 302, false},
		{"npm harness stays unbound", map[int]fakeProc{
			400: {ppid: 1, names: []string{"/usr/local/bin/node", "node"}}}, 400, 0, false},
		{"a lookalike word is not the harness", map[int]fakeProc{
			500: {ppid: 1, names: []string{"/opt/x", "claudette x"}}}, 500, 0, false},
		{"a suffix lookalike is not the harness", map[int]fakeProc{
			501: {ppid: 1, names: []string{"/opt/x", "xclaude y"}}}, 501, 0, false},
		{"a PATH before the space is not the retitle", map[int]fakeProc{
			502: {ppid: 1, names: []string{"/opt/x", "/opt/claude x"}}}, 502, 0, false},
		{"an exec path with a space in it is not the retitle", map[int]fakeProc{
			503: {ppid: 1, names: []string{"/Users/x/claude stuff/bin/node", "node"}}}, 503, 0, false},
		{"a bare word and a trailing space is not a retitle", map[int]fakeProc{
			504: {ppid: 1, names: []string{"/opt/x", "claude "}}}, 504, 0, false},
		{"an exact name wins over a retitle on the same process", map[int]fakeProc{
			600: {ppid: 1, names: []string{launcher, "claude bg-spare"}, born: 6}}, 600, 600, false},
		{"...whichever order the names come in", map[int]fakeProc{
			601: {ppid: 1, names: []string{"claude bg-spare", launcher}, born: 6}}, 601, 601, false},
		{"a lookup that fails mid-walk is unbound", map[int]fakeProc{
			700: {ppid: 701, names: []string{"/bin/sh", "sh"}}}, 700, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := anchorWalk(tc.start, fakeTree(tc.tree))
			if tc.wantPID == 0 {
				if ok {
					t.Fatalf("want unbound, anchored at %+v", got)
				}
				return
			}
			if !ok || got.Ref.PID != tc.wantPID || got.Retitled != tc.wantRetitled {
				t.Fatalf("anchor = %+v ok=%v, want pid %d retitled=%v", got, ok, tc.wantPID, tc.wantRetitled)
			}
			if want := tc.tree[tc.wantPID].born; got.Ref.Born != want {
				t.Errorf("born = %d, want the anchor's own %d", got.Ref.Born, want)
			}
		})
	}
}

// The new rule may not move an interactive session's anchor, above or below.
// Below: its retitled descendants (110 in the table's tree) are never on its
// hook's walk. Above: an interactive `claude` started from INSIDE a --bg
// lane (a Bash tool in the lane running `claude`) has a retitled ancestor,
// and the walk must stop at the interactive one it meets first.
func TestAnchorWalkStopsAtTheFirstMatch(t *testing.T) {
	boundedParallel(t)
	tree := measured()
	tree[120] = fakeProc{ppid: 300, names: []string{launcher, "claude"}, born: 12}
	tree[121] = fakeProc{ppid: 120, names: []string{"/bin/sh", "sh"}, born: 13}
	if got, ok := anchorWalk(121, fakeTree(tree)); !ok || got.Ref.PID != 120 || got.Retitled {
		t.Fatalf("an interactive hook anchors at its own claude, not the lane above it: %+v %v", got, ok)
	}
}

// The hop bound still holds: a chain longer than maxAnchorHops is unbound,
// and one inside it is not (the control).
func TestAnchorWalkIsBounded(t *testing.T) {
	boundedParallel(t)
	chain := func(n int) map[int]fakeProc {
		tree := map[int]fakeProc{}
		for i := 0; i < n; i++ {
			tree[1000+i] = fakeProc{ppid: 1000 + i + 1, names: []string{"/bin/sh", "sh"}}
		}
		tree[1000+n] = fakeProc{ppid: 1, names: []string{versioned, "claude bg-spare"}}
		return tree
	}
	if _, ok := anchorWalk(1000, fakeTree(chain(maxAnchorHops))); ok {
		t.Fatalf("a harness %d hops up is past the bound and must stay unbound", maxAnchorHops)
	}
	if got, ok := anchorWalk(1000, fakeTree(chain(maxAnchorHops-1))); !ok || !got.Retitled {
		t.Fatalf("control: inside the bound it anchors: %+v %v", got, ok)
	}
}

// A retitled anchor records the pid and NO pane: the lane runs in the claude
// daemon's environment, so a pane there is whoever started the daemon
// (measured: the launcher's). The interactive control, one input away,
// records it.
func TestHelloRecordsNoPaneForARetitledAnchor(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name     string
		retitled bool
		wantPane bool
	}{
		{"background lane", true, false},
		{"control: interactive", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
				t.Fatal(errw)
			}
			f.env["HERDR_PANE_ID"] = "w18:pA"
			f.proc = store.ProcRef{PID: 4242, Born: 7}
			f.alive[4242] = 7
			f.retitled = tc.retitled
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "hello", "--label", "lane"); code != 0 {
				t.Fatalf("hello: %s", errw)
			}
			roster, _, _ := f.run(t, f.repo, "", "sessions")
			row := rowFor(roster, "lane")
			if !strings.Contains(row, "pid 4242") {
				t.Fatalf("the anchor's pid is recorded either way:\n%s", roster)
			}
			if got := strings.Contains(row, "pane "); got != tc.wantPane {
				t.Fatalf("pane on the row = %v, want %v:\n%s", got, tc.wantPane, roster)
			}
		})
	}
}

// The roster's pid tracks the LANE: gone when the lane is, whatever the
// daemon does. Before #50 the row named the daemon, which outlived every
// lane, so `pid N GONE` could never fire for one.
func TestARetitledAnchorReadsGoneWithTheLane(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	lane, _ := anchorWalk(300, fakeTree(measured()))
	if lane.Ref != (store.ProcRef{PID: 300, Born: 3}) || !lane.Retitled {
		t.Fatalf("the walk must anchor at the lane itself, got %+v", lane)
	}
	f.proc, f.retitled = lane.Ref, lane.Retitled
	f.alive[300], f.alive[302] = 3, 1 // the lane, and the daemon above it
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "hello", "--label", "lane"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, fmt.Sprintf("pid %d", lane.Ref.PID)) || strings.Contains(row, "GONE") {
		t.Fatalf("control: the live lane's own pid, not GONE:\n%s", roster)
	}
	delete(f.alive, 300) // `claude stop`: the lane exits, the daemon (302) lives on
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, "pid 300 GONE") {
		t.Fatalf("the stopped lane must read GONE:\n%s", roster)
	}
}

// The retitled anchor rides beat and bye as it rides hello (Codex, D-058 code
// pass: both call sites were unexercised). beat BINDS an unbound lane to
// itself; the lane's own bye, sent while it is still alive at SessionEnd,
// ends the row; and a second live process on the id still holds it open,
// which is D-025's fence judging the lane instead of the daemon.
func TestARetitledLaneThroughBeatAndBye(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	// A hand-run hello registers no process: the session is unbound.
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "sess-l", "--label", "lane"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	lane := store.ProcRef{PID: 300, Born: 3}
	f.proc, f.retitled = lane, true
	f.alive[300], f.alive[302] = 3, 1
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "Read", ""), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, "pid 300") {
		t.Fatalf("a retitled beat binds the unbound lane to itself:\n%s", roster)
	}
	// A second process registers on the same id and is alive: the lane's bye
	// must NOT end the row under it.
	f.proc, f.retitled = store.ProcRef{PID: 400, Born: 4}, false
	f.alive[400] = 4
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "Read", ""), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	f.proc, f.retitled = lane, true
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); strings.Contains(row, "ended") {
		t.Fatalf("a second live process holds the row open:\n%s", roster)
	}
	// Control: with the second one gone, the lane's own bye ends it — sent
	// while the lane itself is still alive, as SessionEnd is.
	delete(f.alive, 400)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, "ended") {
		t.Fatalf("the lane's own bye ends its row:\n%s", roster)
	}
}

// THE UPGRADE RESIDUAL, pinned (Codex, D-058 code pass; D-058 records it). A
// lane that was already running when this binary was installed registered
// the DAEMON, and its pane. After the upgrade its hooks anchor at the lane:
// a refresh brings no pane, which the store reads as "keep" (the D-025 rule
// for a manual refresh), and its bye is a stranger to the daemon's
// registration, which is alive — so the row stays live. The remedy is the
// operator's `buddy bye <id> --force`, or stopping --bg lanes before an
// install. A lane started after the upgrade never registers the daemon or a
// pane (the tests above). If this is ever fixed, flip these assertions.
func TestALaneLiveAcrossTheUpgradeKeepsItsDaemonRegistration(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.env["HERDR_PANE_ID"] = "w18:pA"
	f.alive[300], f.alive[302] = 3, 1
	// Before the upgrade: the walk passed the lane and anchored at the daemon.
	f.proc, f.retitled = store.ProcRef{PID: 302, Born: 1}, false
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "hello", "--label", "lane"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	// After it: the same lane's hooks anchor at the lane.
	f.proc, f.retitled = store.ProcRef{PID: 300, Born: 3}, true
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "hello", "--label", "lane"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, "pane herdr:w18:pA") {
		t.Fatalf("residual: a live refresh keeps the pane the old anchor recorded:\n%s", roster)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); strings.Contains(row, "ended") {
		t.Fatalf("residual: the live daemon's old registration holds the row open:\n%s", roster)
	}
	// The remedy.
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-l", "--force"); code != 0 {
		t.Fatalf("bye --force: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "lane"); !strings.Contains(row, "ended") {
		t.Fatalf("the operator's bye --force ends it:\n%s", roster)
	}
}
