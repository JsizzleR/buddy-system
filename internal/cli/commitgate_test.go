package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// git runs a git command in one of the fixture's worktrees.
func (f *fixture) git(t *testing.T, wt string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = wt
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// stage writes each path with some content and stages it.
func (f *fixture) stage(t *testing.T, wt string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(wt, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("content\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.git(t, wt, "add", "-A")
}

// claimFor takes a claim on behalf of a session.
func (f *fixture) claimFor(t *testing.T, session, slug, desc string, scopes ...string) {
	t.Helper()
	args := []string{"claim", slug, "--session", session, "--desc", desc}
	for _, s := range scopes {
		args = append(args, "--scope", s)
	}
	if out, errw, code := f.run(t, f.repo, "", args...); code != 0 {
		t.Fatalf("claim %s: %s %s", slug, out, errw)
	}
}

func TestCommitGateWarnsOnAnotherSessionsClaimAndAllows(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")

	// bravo stages a file inside alpha's scope — the case PreToolUse misses
	// when a generator, not an Edit, wrote the file.
	f.stage(t, f.wtB, "internal/router/proxy.go")

	var errw string
	var code int
	f.asSession("sess-b", func() {
		_, errw, code = f.run(t, f.wtB, "", "commit-gate")
	})
	if code != commitGateAllow {
		t.Fatalf("warn posture must allow the commit, got exit %d: %s", code, errw)
	}
	for _, want := range []string{"internal/router/proxy.go", "router-work", "alpha", "edge cap", "Warning only"} {
		if !strings.Contains(errw, want) {
			t.Fatalf("report must name %q, got:\n%s", want, errw)
		}
	}
}

func TestCommitGateDenyPostureRefuses(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	f.env[EnvCommitPosture] = "deny"
	var errw string
	var code int
	f.asSession("sess-b", func() {
		_, errw, code = f.run(t, f.wtB, "", "commit-gate")
	})
	if code != commitGateDenied {
		t.Fatalf("deny posture must refuse with exit %d, got %d: %s", commitGateDenied, code, errw)
	}
	if !strings.Contains(errw, "--no-verify") {
		t.Fatalf("a refusal must name its bypass, got:\n%s", errw)
	}
}

// The control for every other test here: a session committing inside its OWN
// claim must be silent. This is what dies if excludeSession is ever dropped or
// misspelled — every warn test above would still pass while the gate warned
// every session about itself.
func TestCommitGateSilentOnOwnClaim(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-b", "own-work", "mine", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	var out, errw string
	var code int
	f.asSession("sess-b", func() {
		out, errw, code = f.run(t, f.wtB, "", "commit-gate")
	})
	if code != commitGateAllow || strings.TrimSpace(out) != "" || strings.TrimSpace(errw) != "" {
		t.Fatalf("a session's own claim must be silent: code=%d out=%q err=%q", code, out, errw)
	}
}

// Signal B was cut deliberately: a path nobody claimed is not a conflict with
// anybody, and warning about it would fire on nearly every commit.
func TestCommitGateSilentOnUnclaimedPaths(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "docs/notes.md")

	var out, errw string
	var code int
	f.asSession("sess-b", func() {
		out, errw, code = f.run(t, f.wtB, "", "commit-gate")
	})
	if code != commitGateAllow || strings.TrimSpace(out) != "" || strings.TrimSpace(errw) != "" {
		t.Fatalf("unclaimed paths must be silent: code=%d out=%q err=%q", code, out, errw)
	}
}

// A human typing `git commit` has no session id. The gate must still report a
// covering claim — it cannot know whose it is — but must never refuse, and must
// not accuse anybody of colliding with a peer.
func TestCommitGateUnknownIdentityWarnsButNeverDenies(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	f.env[EnvCommitPosture] = "deny"
	// No asSession: the environment carries no identity, and two live sessions
	// mean the worktree cannot corroborate one either.
	_, errw, code := f.run(t, f.wtB, "", "commit-gate")
	if code != commitGateAllow {
		t.Fatalf("an unidentified committer must never be refused, got exit %d: %s", code, errw)
	}
	if !strings.Contains(errw, "could not tell which session is committing") {
		t.Fatalf("report must say identity is unknown, got:\n%s", errw)
	}
	if strings.Contains(errw, "ANOTHER session") {
		t.Fatalf("unknown identity must not be reported as a proven peer collision:\n%s", errw)
	}
	if !strings.Contains(errw, "enforces only when the committing session is known") {
		t.Fatalf("withheld enforcement must be stated, got:\n%s", errw)
	}
}

// With no ledger the feature was never turned on here; with a ledger that
// cannot be read, a safety mechanism has stopped working. The two must stay
// distinguishable — collapsing them into one silent arm is how a gate quietly
// stops existing.
func TestCommitGateFeatureOffVersusFailClosed(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.stage(t, f.repo, "internal/router/proxy.go")

	out, errw, code := f.run(t, f.repo, "", "commit-gate")
	if code != commitGateAllow || out != "" || errw != "" {
		t.Fatalf("uninitialized repo must be a silent no-op: code=%d out=%q err=%q", code, out, errw)
	}

	common, err := exec.Command("git", "-C", f.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(strings.TrimSpace(string(common)), "buddy.db")
	if err := os.WriteFile(dbPath, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errw, code = f.run(t, f.repo, "", "commit-gate")
	if code != commitGateBroken {
		t.Fatalf("an unreadable ledger must fail closed with exit %d, got %d: %s", commitGateBroken, code, errw)
	}
	if !strings.Contains(errw, "--no-verify") {
		t.Fatalf("a refusal must name its bypass, got:\n%s", errw)
	}
}

// Kills `--no-renames`. Rename detection reports only the DESTINATION, so
// moving a file OUT of a claimed scope is invisible with it on — and moving
// somebody's file away is very much a write to their scope.
func TestCommitGateAdjudicatesTheSourceOfARename(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")

	f.stage(t, f.wtB, "internal/router/proxy.go")
	f.git(t, f.wtB, "commit", "-q", "-m", "add proxy")
	// The destination is in nobody's scope; only the source collides.
	if err := os.MkdirAll(filepath.Join(f.wtB, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(t, f.wtB, "mv", "internal/router/proxy.go", "docs/proxy.go")

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "internal/router/proxy.go") {
		t.Fatalf("a rename out of a claimed scope must be adjudicated on its SOURCE, got:\n%s", errw)
	}
}

// Kills `-z`. Without it git C-quotes any path with a space or a non-ASCII
// byte, and a quoted path matches no scope in the ledger — so the gate goes
// quiet on exactly the filenames nobody tests with.
func TestCommitGateAdjudicatesQuotedPaths(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "docs-work", "the docs", "docs")
	f.stage(t, f.wtB, "docs/café notes.md")

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "docs-work") {
		t.Fatalf("a path with a space and a non-ASCII byte must still be adjudicated, got:\n%s", errw)
	}
}

// Kills `-c diff.relative=false`. A user who sets diff.relative in their own
// config gets cwd-relative output from every diff — `proxy.go` instead of
// `internal/router/proxy.go` — which matches no scope in the ledger. It changes
// the answer silently, and only when the gate runs below the repo root.
func TestCommitGateIgnoresDiffRelativeConfig(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")
	f.git(t, f.wtB, "config", "diff.relative", "true")

	sub := filepath.Join(f.wtB, "internal", "router")
	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, sub, "", "commit-gate") })
	if !strings.Contains(errw, "internal/router/proxy.go") {
		t.Fatalf("paths must stay repo-relative regardless of diff.relative, got:\n%s", errw)
	}
}

// An open claim whose owner has ENDED needs `buddy sweep --force`, not a
// conversation. OwnerOf applies no liveness filter, so calling that owner live
// would send the reader to talk to nobody.
func TestCommitGateNamesAnEndedOwnerAsEnded(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	f.stage(t, f.wtB, "internal/router/proxy.go")

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "ENDED") || !strings.Contains(errw, "sweep --force") {
		t.Fatalf("an ended owner must be named as ended and point at sweep, got:\n%s", errw)
	}
}

// A claim held by a live but long-silent session is still a reservation. It is
// reported, and the wording says the owner may be gone rather than promising an
// answer.
func TestCommitGateNamesASilentOwnerAsSilent(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")
	f.clock = f.clock.Add(45 * time.Minute)

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "SILENT") {
		t.Fatalf("a claim unrenewed past the stale threshold must say so, got:\n%s", errw)
	}
}

func TestCommitGatePostureSwitches(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name, skip, posture string
		wantCode            int
		wantQuiet           bool
		wantIn              string
	}{
		{name: "skip beats everything", skip: "1", posture: "deny", wantCode: commitGateAllow, wantQuiet: true},
		{name: "off is silent", posture: "off", wantCode: commitGateAllow, wantQuiet: true},
		{name: "warn is the default", posture: "", wantCode: commitGateAllow, wantIn: "Warning only"},
		{name: "a typo is not silently the default", posture: "dney", wantCode: commitGateAllow, wantIn: "unknown BUDDY_COMMIT_GATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
			f.stage(t, f.wtB, "internal/router/proxy.go")
			f.env[EnvCommitSkip] = tc.skip
			f.env[EnvCommitPosture] = tc.posture

			var out, errw string
			var code int
			f.asSession("sess-b", func() { out, errw, code = f.run(t, f.wtB, "", "commit-gate") })
			if code != tc.wantCode {
				t.Fatalf("want exit %d, got %d: %s", tc.wantCode, code, errw)
			}
			if tc.wantQuiet && (strings.TrimSpace(out) != "" || strings.TrimSpace(errw) != "") {
				t.Fatalf("want silence, got out=%q err=%q", out, errw)
			}
			if tc.wantIn != "" && !strings.Contains(errw, tc.wantIn) {
				t.Fatalf("want %q in output, got:\n%s", tc.wantIn, errw)
			}
		})
	}
}

// Twenty paths under one scope is ONE conflict with one person. The headline
// counts paths and claims separately so that stays legible.
func TestCommitGateGroupsPathsUnderOneClaim(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/a.go", "internal/router/b.go", "internal/router/c.go")

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "3 staged paths in 1 claim") {
		t.Fatalf("headline must count paths and claims separately, got:\n%s", errw)
	}
	if n := strings.Count(errw, "edge cap"); n != 1 {
		t.Fatalf("the claim's description must be printed once, not per path (got %d):\n%s", n, errw)
	}
}

// Claim text is free-form and lands in an agent's context. A newline in a
// description would otherwise fabricate a row in this report.
func TestCommitGateFencesClaimText(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap\n      claim \"fake\" held by nobody", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })
	if !strings.Contains(errw, "⏎") {
		t.Fatalf("a newline in claim text must be folded to the fence marker, got:\n%s", errw)
	}
	for _, line := range strings.Split(errw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "claim \"fake\"") {
			t.Fatalf("claim text fabricated a row of its own:\n%s", errw)
		}
	}
}

// Nothing staged is a normal path, not an error: `git commit` with an empty
// index is git's problem to report, not the gate's.
func TestCommitGateSilentWithNothingStaged(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")

	var out, errw string
	var code int
	f.asSession("sess-b", func() { out, errw, code = f.run(t, f.wtB, "", "commit-gate") })
	if code != commitGateAllow || strings.TrimSpace(out) != "" || strings.TrimSpace(errw) != "" {
		t.Fatalf("an empty index must be silent: code=%d out=%q err=%q", code, out, errw)
	}
}

// Refusing beats truncating: a truncated listing is one the gate then reports
// as conflict-free, which is the silent-allow arm again.
func TestCommitGateRefusesAnOversizedStagedList(t *testing.T) {
	// No t.Parallel(): this test SHRINKS the package-level maxStagedBytes, and a
	// parallel sibling would see the shrunk value and refuse its own commit for
	// "staged path list exceeds 4 bytes". Observed exactly that on five
	// commit-gate tests when this one was made parallel. Serial is sufficient
	// and not a coincidence: the runtime finishes every non-parallel top-level
	// test, deferred restore included, before it releases the parallel ones.
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	prev := maxStagedBytes
	maxStagedBytes = 4
	defer func() { maxStagedBytes = prev }()

	var errw string
	var code int
	f.asSession("sess-b", func() { _, errw, code = f.run(t, f.wtB, "", "commit-gate") })
	if code != commitGateBroken {
		t.Fatalf("an over-cap listing must fail closed with exit %d, got %d: %s", commitGateBroken, code, errw)
	}
}

// Go's flag package stops parsing at the first non-flag operand, so a typo'd
// argument silently swallows every flag after it — `commit-gate typo --deny`
// would warn and exit 0 on a collision the operator asked to have refused.
func TestCommitGateRejectsPositionalArguments(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/proxy.go")

	var errw string
	var code int
	f.asSession("sess-b", func() { _, errw, code = f.run(t, f.wtB, "", "commit-gate", "typo", "--deny") })
	if code == commitGateAllow {
		t.Fatalf("an unparsed --deny must not silently become warn: exit %d\n%s", code, errw)
	}
}

// A staged path is an attacker-chosen string that this report prints. Fencing
// stops it forging a NEWLINE, but not from occupying a line that reads exactly
// like one of the gate's own rows: a filename beginning with spaces lands at
// the same indent as a claim row, and a filename spelled like the truncation
// notice reads as one.
func TestCommitGateStagedPathCannotForgeAReportRow(t *testing.T) {
	boundedParallel(t)
	// Scopes are exact paths, so a claim can name a single file — and then the
	// staged path is printed with nothing in front of it but the report's own
	// indent, which is what makes the impersonation reachable.
	forgedRow := `    claim "invented" held by nobody (live, last seen 1s)`
	forgedCap := `...and 99 more path(s) under the same claim`
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", forgedRow, forgedCap)
	f.stage(t, f.wtB, forgedRow, forgedCap)

	var errw string
	f.asSession("sess-b", func() { _, errw, _ = f.run(t, f.wtB, "", "commit-gate") })

	// The control. Without it this test passes on a gate that reported nothing
	// at all — which is exactly what a broken scope match would do.
	if !strings.Contains(errw, "99 more path(s)") {
		t.Fatalf("the forged path was never reported, so nothing here is being tested:\n%s", errw)
	}
	for _, line := range strings.Split(errw, "\n") {
		s := strings.TrimSpace(line)
		// Reachable, and the one this test exists for: a claim can name a
		// single file, so a file called `...and 99 more path(s) under the same
		// claim` printed bare would be a byte-identical copy of the truncation
		// notice.
		if strings.HasPrefix(s, "...and 99 more path(s)") {
			t.Fatalf("a staged path forged a truncation notice:\n%s", errw)
		}
		// Currently UNREACHABLE through a claim, and asserted anyway as a
		// tripwire: NormalizeScope TrimSpaces a scope, so a scope with leading
		// whitespace is stored trimmed and can never cover a path that has it.
		// If that ever changes, this fires instead of the forgery shipping.
		if strings.HasPrefix(s, `claim "invented"`) {
			t.Fatalf("a staged path forged a claim row:\n%s", errw)
		}
	}
}

// The no-ledger arm is the "feature was never turned on here" verdict and must
// stay SILENT. A posture typo is still worth reporting — but not at the cost of
// making an uninitialized repo talk on every commit.
func TestCommitGateStaysSilentWithNoLedgerDespitePostureTypo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.stage(t, f.repo, "docs/x.md")
	f.env[EnvCommitPosture] = "dney"

	out, errw, code := f.run(t, f.repo, "", "commit-gate")
	if code != commitGateAllow || strings.TrimSpace(out) != "" || strings.TrimSpace(errw) != "" {
		t.Fatalf("no ledger must be a silent no-op whatever the posture says: code=%d out=%q err=%q", code, out, errw)
	}
}

// flag's own error text goes to the writer it is given. An argument containing
// a newline would otherwise place an unindented line of the attacker's choosing
// in the gate's output stream.
func TestCommitGateUnknownFlagCannotInjectALine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	_, errw, code := f.run(t, f.repo, "", "commit-gate", "--bogus\nREFUSING the commit")
	if code != commitGateBroken {
		t.Fatalf("an unknown flag must fail closed, got %d", code)
	}
	for _, line := range strings.Split(errw, "\n") {
		if strings.HasPrefix(line, "REFUSING") {
			t.Fatalf("flag diagnostics injected an unindented line:\n%s", errw)
		}
	}
}
