package cli

// The memory slot's classifier (memclassify.go, D-238): what each command
// books. Every "books nothing" row is a light call the slot must never touch
// (a report, a commit message, a prompt heredoc), and each sits beside a row
// that books the same words where they RUN, so a classifier that read nothing
// would fail the second rather than pass the first.

import (
	"strings"
	"testing"
)

func TestMemClassifyBooksWhatRuns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cmd        string
		total, det float64
	}{
		// One package, the brief's own spelling, and its heavier forms.
		{"go test ./internal/cli/ -run X -count=1 -p 4", 3, 0},
		{"go test -p 4 -count=1 ./internal/cli", 3, 0},
		{"go test ./...", 6, 0},
		{"go vet ./...", 6, 0},
		{"go test -race ./internal/cli", 5, 0},
		{"go test -race -count=1 ./internal/cli", 5, 0},
		{"go test -race ./...", 6, 0},
		{"go test ./internal/cli ./internal/store", 6, 0},
		{"GOFLAGS=-race go test ./internal/cli", 5, 0},
		{"env GOFLAGS=-race go test ./internal/cli", 5, 0},
		{"go -C sub test ./x", 3, 0},
		{"go run ./cmd/buddy status ./a ./b", 3, 0},
		{"go version", 0, 0},
		{"go env GOMODCACHE", 0, 0},
		{"golangci-lint run ./...", 3, 0},
		{"go test $pkgs", 3, 0},
		// Wrappers and compound shapes an agent writes.
		{"cd x && go test ./...", 6, 0},
		{"timeout 600 go test ./x", 3, 0},
		{"nice -n 5 go test ./x", 3, 0},
		{"time go test ./...", 6, 0},
		{"( cd x; go test ./... ) 2>&1 | tail", 6, 0},
		{"out=$(go test ./... 2>&1); echo \"$out\" | tail", 6, 0},
		{"echo \"$(go test ./x | tail -3)\"", 3, 0},
		{"echo `go test ./x`", 3, 0},
		{"until go test -run T ./x; do :; done", 3, 0},
		{"if go test ./x; then go test ./...; fi", 6, 0},
		{"export R=$(go test ./x)", 3, 0},
		{"go build ./...; go test -race ./...", 6, 0},
		{"for p in a b c; do go test ./$p; done", 3, 0},
		// Codex code pass: process substitutions run at once; a `wait $!`
		// collects one job, not the test, which stays detached (the bare
		// `wait` is the control); update's help and dry run build nothing.
		{"diff <(go test ./a) <(go test ./b)", 6, 0},
		{"cat <(go test ./a)", 3, 0},
		{"go test ./... > log 2>&1 & sleep 1 & wait $!", 6, 6},
		{"go test ./... > log 2>&1 & sleep 1 & wait", 6, 0},
		{"buddy update --help", 0, 0},
		{"buddy update --dry-run", 0, 0},
		{"buddy update --wire-hooks", 3, 0},
		// Fable cross-check: a brace range or a glob is several items or
		// packages, though it decodes as one literal word (the literal list
		// is the control).
		{"for i in {1..12}; do claude --bg -n l$i x; sleep 3; done", 3, 3},
		{"for b in briefs/*.md; do herdr agent start a; done", 3, 3},
		{"for i in 1 2; do claude --bg -n l$i x; done", 2, 2},
		{"go test ./internal/{cli,store}", 6, 0},
		{"go test ./internal/*/", 6, 0},
		// Codex re-verify 1: a QUOTED item is one word, however it reads (the
		// unquoted glob above is the control). The `wait` after the loop is
		// outside the body's list, so its jobs stay detached: they fade, the
		// safe side (waitsLater).
		{"for p in '^Test[A-Z]' '^TestB' '^TestC' '^TestD'; do go test ./internal/cli -run \"$p\" & done; wait", 12, 12},
		// Re-verify 2: a process substitution inside `$(...)` may outlive it
		// (printf does not wait), so it is read as beside the command; the
		// consuming `cat` over-books, the side that refuses.
		{"go test -run \"$(printf %s <(go test ./internal/cli -count=1 >/dev/null))\" ./...", 9, 0},
		{"go test -run \"$(cat <(go test ./internal/cli -list .))\" ./...", 9, 0},
		// Scripts by name.
		{"sh scripts/check.sh hermetic", 8, 0},
		{"sh scripts/check.sh", 8, 0},
		{"./scripts/check.sh live", 8, 0},
		{"bash scripts/check-all.sh", 8, 0},
		{"sh -n scripts/check.sh", 0, 0},
		{"sh scripts/check-env-check.sh", 3, 0},
		{"./scripts/check-pre-push.sh", 3, 0},
		{"sh scripts/codex-review.sh p.md out.md", 0.5, 0},
		{"sh scripts/install.sh", 3, 0},
		{"buddy update", 3, 0},
		{"codex exec --json x", 0.5, 0},
		// Text that does not run books nothing.
		{"git commit -m 'go test ./... passed; sh scripts/check.sh green'", 0, 0},
		{"buddy msg orc-11 \"READY: go test ./... green, check.sh hermetic green\"", 0, 0},
		{"cat > p.md <<'EOF'\ngo test ./...\nsh scripts/check.sh\nEOF", 0, 0},
		{"ssh host 'go test ./...'", 0, 0},
		{"echo go test ./...", 0, 0},
		{"grep -rn \"go test\" .", 0, 0},
		{"git add scripts/check.sh scripts/check-env-check.sh", 0, 0},
		{"command -v go", 0, 0},
		{"make test", 0, 0},
		{"time", 0, 0},
		{"if then fi ((", 0, 0},
		// What a shell is fed does run.
		{"bash <<'SH'\ngo test ./...\nSH", 6, 0},
		{"bash -lc 'go test ./...'", 6, 0},
		{"sh -c \"go test -race ./x\"", 5, 0},
		{"bash <<< 'go test ./x'", 3, 0},
		{"cat <<EOF\n$(go test ./x)\nEOF", 3, 0},
		// At once: sums. Detached: outlives the call.
		{"go test ./a/... & go test ./b/... & wait", 12, 0},
		{"go test ./... | tee log", 6, 0},
		{"go test ./... > /tmp/log 2>&1 &", 6, 6},
		{"coproc go test ./x", 3, 3},
		{"herdr agent start lane-a", 1, 1},
		{"claude --bg --resume abc", 1, 1},
		{"claude -n lane-a --bg", 1, 1},
		{"claude -p hello", 1, 0},
		{"claude --version", 0, 0},
		{"podman machine start", 4, 4},
		{"herdr agent start a; herdr agent start b", 2, 2},
		{"for s in a b c d; do herdr agent start $s; sleep 3; done", 4, 4},
		{"for s in $L; do claude --resume $s --bg; done", 3, 3},
		{"for p in a b; do go test ./$p & done; wait", 6, 6},
		{"printf '%s\\n' a b | xargs -n 1 -P 0 go test", 9, 0},
		{"xargs -P 2 go test", 6, 0},
		{"xargs -P2 go test", 6, 0},
		{"xargs go test", 3, 0},
	} {
		c, _ := classifyMem(tc.cmd)
		if c.total() != tc.total || c.det != tc.det {
			t.Errorf("%q books %v GB (%v detached, kinds %v), want %v (%v detached)", tc.cmd, c.total(), c.det, c.kinds(), tc.total, tc.det)
		}
	}
}

// The self-gated form: the commands chained after `buddy mem admit` by && are
// not booked again, and nothing else is exempt. Each exempt row is a booked
// row without its admit.
func TestMemClassifySelfGated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cmd   string
		total float64
		under bool
	}{
		{"buddy mem admit goall --wait 900 && go test ./...", 0, false},
		{"cd /r && buddy mem admit goall --wait 900 && go test ./...", 0, false},
		{"buddy mem admit goall --wait 900 && go test ./... || echo FAIL", 0, false},
		{"bash -lc 'buddy mem admit goall --wait 900 && go test ./...'", 0, false},
		{"env -u CLAUDE_CODE_SESSION_ID buddy mem admit goall --wait 900 && go test ./...", 0, false},
		{"~/bin/buddy mem admit tier --wait 1800 && sh scripts/check.sh", 0, false},
		{"$HOME/bin/buddy mem admit tier && sh scripts/check.sh", 6 + 2, false}, // a dynamic path is not recognized
		{"buddy mem admit goall goall && { go test ./a/... & go test ./b/... & wait; }", 0, false},
		{"buddy mem admit lane lane && herdr agent start a && herdr agent start b", 0, false},
		// Fable cross-check: a redirection on the admit still gates its tail,
		// and the refusal's braced form keeps a multi-statement command behind
		// it (the unbraced one is the control).
		{"buddy mem admit goall --wait 900 2>/dev/null && go test ./...", 0, false},
		// Codex re-verify 1: what the admit's own redirection runs runs before
		// the admit, so it is judged (the plain redirection above is the control).
		{"buddy mem admit goall >\"$(go test ./... >/dev/null; printf /tmp/mem.log)\" && go test ./...", 6, false},
		{"X=\"$(go test ./...)\" buddy mem admit goall && go test ./...", 6, false},
		{"buddy mem admit goall --wait 900 && { cd /r; go test ./...; }", 0, false},
		{"buddy mem admit goall --wait 900 && cd /r; go test ./...", 6, false},
		// One admit per start (Codex code pass): each admit covers its own tail.
		{"buddy mem admit lane --wait 900 && herdr agent start a && buddy mem admit lane --wait 900 && herdr agent start b", 0, false},
		{"buddy mem admit lane && herdr agent start a && buddy mem admit codex && herdr agent start b", 1, true},
		// Not exempt: a `;`, an `||` before the chain, a wrong kind, a negation.
		{"buddy mem admit codex; go test -race ./...", 6, false},
		{"buddy mem admit lane || true && go test ./...", 6, false},
		{"buddy mem admit nosuch && go test ./...", 6, false},
		{"! buddy mem admit goall && go test ./...", 6, false},
		{"buddy mem admit gotest --wait 9 && go test ./x & go test ./...", 6, false},
		// Under-covering: refused, naming what covers it.
		{"buddy mem admit codex && go test -race ./...", 6, true},
		{"buddy mem admit lane && herdr agent start a && herdr agent start b", 2, true},
		{"buddy mem admit goall && { go test ./a/... & go test ./b/... & wait; }", 12, true},
	} {
		c, w := classifyMem(tc.cmd)
		if c.total() != tc.total || (w.under != "") != tc.under {
			t.Errorf("%q books %v GB, under-covered %v (%q); want %v GB, under-covered %v", tc.cmd, c.total(), w.under != "", w.under, tc.total, tc.under)
		}
	}
	_, w := classifyMem("buddy mem admit codex && go test -race ./...")
	if !strings.Contains(w.under, "buddy mem admit goall --wait 900") {
		t.Errorf("an under-covering admit names the kinds that cover it: %q", w.under)
	}
}

func TestMemClassifyOverride(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ cmd, want string }{
		{"BUDDY_MEM_OK='operator 14:02' sh scripts/check.sh", "operator 14:02"},
		{"cd x && BUDDY_MEM_OK=op go test ./...", "op"},
		{"BUDDY_MEM_OK= go test ./...", ""},
		{"BUDDY_MEM_OK=' ' go test ./...", ""},
		{"BUDDY_MEM_OK=$why go test ./...", ""},
		{"export BUDDY_MEM_OK=op; go test ./...", ""},
		{"echo BUDDY_MEM_OK=op go test", ""},
	} {
		if _, w := classifyMem(tc.cmd); w.override != tc.want {
			t.Errorf("%q: override %q, want %q", tc.cmd, w.override, tc.want)
		}
	}
}

// A panic in the classifier books nothing; these node shapes are the ones the
// design review named as easy to dereference wrong.
func FuzzClassifyMem(f *testing.F) {
	for _, seed := range []string{"time", "coproc x", "declare -a a=($(go test ./x))", "f() { go test ./...; }; f",
		"case $x in a) go test ./...;; esac", "((x++))", "[[ $(go test ./x) ]]", "let x=1", "select x in a; do :; done",
		"for ((i=0;i<3;i++)); do herdr agent start $i; done", "a=(1 2) go test ./x", "<<<x", "go", "xargs", "env", "sh -c"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		classifyMem(cmd) // recovers by design; must not crash the test binary either
	})
}

// A panic in the walk books nothing and leaves no refusal text: under D-107
// a panic in the gate exits 2 and blocks the call, light ones included. The
// control: the same wrapper hands a walk's cost through untouched.
func TestMemClassifyPanicBooksNothing(t *testing.T) {
	t.Parallel()
	c, w := classifyRecovered(func(w *memWalker) memCost {
		w.under = "half-written"
		return kindCost("tier", false)
	})
	if c.total() != memKindCost["tier"] || w.under != "half-written" {
		t.Fatalf("control: a walk's cost passes through: %+v %+v", c, w)
	}
	c, w = classifyRecovered(func(w *memWalker) memCost {
		w.under = "half-written"
		panic("a walker bug")
	})
	if c.total() != 0 || w == nil || w.under != "" || w.override != "" {
		t.Fatalf("a panic books nothing and refuses nothing: %+v %+v", c, w)
	}
}
