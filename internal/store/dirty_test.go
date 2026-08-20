package store

import (
	"strings"
	"testing"
	"time"
)

// paths flattens a record list to its paths, for compact assertions.
func paths(recs []DirtyRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Path)
	}
	return out
}

func labels(recs []DirtyRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Owner.Label)
	}
	return out
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// A scan RETRACTS what git no longer reports dirty. Without it a row names an
// owner forever, and a path its holder committed an hour ago still answers
// with their name.
func TestAScanRetractsPathsTheSessionNoLongerHolds(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")

	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md", "docs/status.md"}); err != nil {
		t.Fatal(err)
	}
	if got := paths(mustHolders(t, st, "CHANGELOG.md")); !has(got, "CHANGELOG.md") {
		t.Fatalf("precondition: the path must be held, got %v", got)
	}

	// alpha commits CHANGELOG.md; the next scan sees only the other file.
	clk.advance(time.Second)
	if err := st.RetainDirty("/wt/a", []string{"docs/status.md"}, clk.now()); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "CHANGELOG.md"); len(got) != 0 {
		t.Fatalf("a committed path must stop naming its former holder, got %v", labels(got))
	}
	if got := mustHolders(t, st, "docs/status.md"); len(got) != 1 {
		t.Fatalf("the still-dirty path must survive the replace, got %v", labels(got))
	}
}

// A replace is scoped to ONE worktree. A session scanning its own checkout
// must not retract what it recorded in another one.
func TestAScanOnlyReplacesItsOwnWorktree(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/b", []string{"shared.txt"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Second)
	if err := st.RetainDirty("/wt/a", []string{"other.txt"}, clk.now()); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "shared.txt"); len(got) != 1 {
		t.Fatalf("a scan of /wt/a must not retract a row keyed to /wt/b, got %v", got)
	}
}

// The check/mark split beat uses: pending is a pure read, so a notice can be
// composed, DELIVERED, and only then marked. Marking during composition
// silenced a notice forever whenever the hook's write then failed.
func TestDirtyWarnPendingIsSeparableFromMarking(t *testing.T) {
	st, _ := openTest(t)
	pending, err := st.DirtyWarnPending("sess-b", "/wt/a", "CHANGELOG.md")
	if err != nil || !pending {
		t.Fatalf("an unwarned path must be pending (err=%v)", err)
	}
	// Asking twice must not consume it: only delivery does.
	if again, _ := st.DirtyWarnPending("sess-b", "/wt/a", "CHANGELOG.md"); !again {
		t.Fatal("the pure read must not consume the one shot; a caller that asks and " +
			"then fails to deliver owes the notice again")
	}
	if err := st.MarkDirtyWarned("sess-b", "/wt/a", "CHANGELOG.md"); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.DirtyWarnPending("sess-b", "/wt/a", "CHANGELOG.md"); after {
		t.Fatal("once delivered and marked, it must not be pending again")
	}
	// Folded, like every other path comparison in the ledger.
	if cased, _ := st.DirtyWarnPending("sess-b", "/wt/a", "CHANGELOG.MD"); cased {
		t.Fatal("the mark must be case-folded, or a re-spelling re-announces the same file")
	}
}

// leaseExpired's future-stamp arm: a backwards clock step would otherwise
// suppress scanning until wall time caught up, and a suppressed scan means
// retraction stops — which is what makes rows name the wrong owner.
func TestAFutureScanStampDoesNotSuppressScanningForever(t *testing.T) {
	st, clk := openTest(t)
	if due, _ := st.DueForDirtyScan("sess-a", "/wt/a", time.Minute); !due {
		t.Fatal("precondition: the first scan is due")
	}
	clk.advance(-time.Hour) // the clock steps backwards; the stamp is now in the future
	if due, err := st.DueForDirtyScan("sess-a", "/wt/a", time.Minute); err != nil || !due {
		t.Fatalf("a stamp in the future must read as EXPIRED, not as 'not due' — otherwise "+
			"scanning stops until wall time catches up (err=%v)", err)
	}
}

// A sub-second interval truncates to zero against a second-resolution column,
// which would make every caller due forever.
func TestASubSecondScanIntervalIsRefused(t *testing.T) {
	st, _ := openTest(t)
	for _, every := range []time.Duration{0, -time.Second, 500 * time.Millisecond} {
		if _, err := st.DueForDirtyScan("sess-a", "/wt/a", every); err == nil {
			t.Errorf("interval %v must be refused, not silently truncated to zero", every)
		}
	}
}

// The warn is once per (session, worktree, path), decided durably in the
// ledger — the mark is what survives a session restart.
func TestFirstDirtyWarnIsTrueExactlyOnce(t *testing.T) {
	st, _ := openTest(t)
	first, err := st.FirstDirtyWarn("sess-b", "/wt/a", "CHANGELOG.md")
	if err != nil || !first {
		t.Fatalf("the first warn must report first=true (err=%v)", err)
	}
	again, err := st.FirstDirtyWarn("sess-b", "/wt/a", "CHANGELOG.md")
	if err != nil || again {
		t.Fatalf("a repeat must report first=false (err=%v)", err)
	}
	// Folded: the same file under a different spelling is the same file.
	if cased, _ := st.FirstDirtyWarn("sess-b", "/wt/a", "CHANGELOG.MD"); cased {
		t.Fatal("the dedup must be case-folded, or a re-spelling re-announces the same file")
	}
	// A different path, worktree, or session is different information.
	for _, tc := range []struct{ name, sess, wt, path string }{
		{"other path", "sess-b", "/wt/a", "docs/status.md"},
		{"other worktree", "sess-b", "/wt/b", "CHANGELOG.md"},
		{"other session", "sess-c", "/wt/a", "CHANGELOG.md"},
	} {
		if ok, _ := st.FirstDirtyWarn(tc.sess, tc.wt, tc.path); !ok {
			t.Errorf("%s must warn on its own merits, not be muted by an unrelated mark", tc.name)
		}
	}
}

// The scan throttle claims its slot in the same transaction that grants it, so
// two callers cannot both decide they are due.
func TestDirtyScanThrottleGrantsOneSlotPerInterval(t *testing.T) {
	st, clk := openTest(t)
	due, err := st.DueForDirtyScan("sess-a", "/wt/a", time.Minute)
	if err != nil || !due {
		t.Fatalf("the first scan must be due (err=%v)", err)
	}
	if again, _ := st.DueForDirtyScan("sess-a", "/wt/a", time.Minute); again {
		t.Fatal("a second caller inside the interval must not also scan")
	}
	// Another worktree keeps its own budget: they are separate git trees.
	if other, _ := st.DueForDirtyScan("sess-a", "/wt/b", time.Minute); !other {
		t.Fatal("the throttle must be per worktree, not per session")
	}
	clk.advance(time.Minute + time.Second)
	if later, _ := st.DueForDirtyScan("sess-a", "/wt/a", time.Minute); !later {
		t.Fatal("the slot must reopen once the interval has passed")
	}
}

// The notice's audience is every OTHER session holding the path in the SAME
// worktree. Two exclusions, and two deliberate INCLUSIONS.
func TestDirtyPeersIsSameWorktreeAndIncludesTheQuiet(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/a")
	c := hello(t, st, "sess-c", "charlie", "/wt/b")
	d := hello(t, st, "sess-d", "delta", "/wt/a")

	for _, s := range []struct {
		si SessionInfo
		wt string
	}{{a, "/wt/a"}, {b, "/wt/a"}, {c, "/wt/b"}, {d, "/wt/a"}} {
		if err := st.RecordDirty(s.si.SessionID, s.wt, []string{"CHANGELOG.md"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Bye(d.SessionID, d.Incarnation); err != nil {
		t.Fatal(err)
	}

	got := labels(mustPeers(t, st, "/wt/a", "CHANGELOG.md", a.SessionID))
	if has(got, "alpha") {
		t.Errorf("the asking session is not its own peer: %v", got)
	}
	if has(got, "charlie") {
		t.Errorf("another checkout is a different FILE, not a conflict: %v", got)
	}
	// delta ENDED without committing, so its hunk is still sitting in the tree.
	// Whether its author is answering has nothing to do with whether the edit
	// is there, and filtering it out stayed silent on a real collision.
	if !has(got, "bravo") || !has(got, "delta") {
		t.Fatalf("a holder that has gone quiet is still a collision: %v", got)
	}

	// What liveness changes is the WORDING, so the caller must be able to tell
	// them apart.
	clk.advance(StaleAfter + time.Minute)
	for _, p := range mustPeers(t, st, "/wt/a", "CHANGELOG.md", a.SessionID) {
		switch p.Owner.Label {
		case "bravo":
			if !p.Stale(clk.now()) {
				t.Error("a silent holder must be reportable as stale")
			}
		case "delta":
			if p.Owner.Live() {
				t.Error("an ended holder must be distinguishable")
			}
		}
	}
}

// `whose` asks a broader question than the warn — "who do I talk to?" — so it
// reports every holder, including the ones the warn deliberately ignores. The
// caller labels them; filtering them away would answer "nobody" about a file
// that is visibly dirty, which is the failure being fixed.
func TestDirtyHoldersReportsEveryWorktreeAndEndedSessions(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	c := hello(t, st, "sess-c", "charlie", "/wt/b")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordDirty(c.SessionID, "/wt/b", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	clk.advance(StaleAfter + time.Minute)

	got := labels(mustHolders(t, st, "CHANGELOG.md"))
	if !has(got, "alpha") || !has(got, "charlie") {
		t.Fatalf("whose must see ended and other-worktree holders alike; got %v", got)
	}
	// And it must carry enough for the caller to LABEL them rather than
	// silently mixing an ended or stale owner in with a live one.
	for _, h := range mustHolders(t, st, "CHANGELOG.md") {
		switch h.Owner.Label {
		case "alpha":
			if h.Owner.Live() {
				t.Error("an ended holder must be distinguishable")
			}
		case "charlie":
			if !h.Stale(clk.now()) {
				t.Error("a silent holder must be reportable as stale")
			}
		}
	}
}

// first_seen answers "how long have they been sitting on this?", so it must
// survive a re-record; only last_seen moves.
func TestRerecordingAPathKeepsWhenItWasFirstSeen(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	start := clk.now()
	clk.advance(20 * time.Minute)
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	got := mustHolders(t, st, "CHANGELOG.md")
	if len(got) != 1 {
		t.Fatalf("a re-record must update the row, not duplicate it: %v", got)
	}
	if !got[0].FirstSeen.Equal(start) {
		t.Fatalf("first_seen must survive a re-record: %v, want %v", got[0].FirstSeen, start)
	}
	if !got[0].LastSeen.Equal(clk.now()) {
		t.Fatalf("last_seen must move: %v, want %v", got[0].LastSeen, clk.now())
	}
}

// Sweep must NOT delete an attribution on a timer. An ended session can leave
// real uncommitted edits, and its row is the only record of whose they are —
// git attributes nothing, so a scan cannot reconstruct it. The right trigger
// for forgetting is the FILE BECOMING CLEAN, which RetainDirty handles.
func TestSweepNeverDeletesAnAttributionOnATimer(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FirstDirtyWarn(a.SessionID, "/wt/a", "CHANGELOG.md"); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}

	// A week later -- far past the 24h claims TTL that sweeps everything else.
	clk.advance(7 * 24 * time.Hour)
	if _, _, err := st.Sweep(24*time.Hour, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "CHANGELOG.md"); len(got) != 1 {
		t.Fatal("the hunk is still in the tree; deleting who left it there destroys the " +
			"only answer to the question this feature exists to answer")
	}
	// The WARN mark is different: re-announcing a file to a session that has
	// been gone for three months is a new day's information.
	if ok, _ := st.FirstDirtyWarn(a.SessionID, "/wt/a", "CHANGELOG.md"); !ok {
		t.Fatal("sweep must clear the warn marks of a long-gone session")
	}
	// ...and its scan lease, or a revived id inherits a throttle it never set.
	if due, _ := st.DueForDirtyScan(a.SessionID, "/wt/a", time.Minute); !due {
		t.Fatal("sweep must clear the scan lease of a long-gone session")
	}
	// And a scan proving the path clean DOES retract it, timer or no timer.
	if err := st.RetainDirty("/wt/a", nil, clk.now()); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "CHANGELOG.md"); len(got) != 0 {
		t.Fatalf("a clean path must retract even an ended session's row: %v", labels(got))
	}
}

// The backstop. Retraction is the right way to forget an attribution, but it
// can only run where a scan runs — a removed worktree, or a repo whose sessions
// have all stopped, has no remover at all. So a row whose session ended long
// enough ago that its label means nothing to anyone is dropped, which bounds
// growth without deleting anything still actionable.
func TestAnAttributionIsBoundedAtTheLongHorizon(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}

	clk.advance(DirtyKeepAfterEnd - time.Hour)
	if _, _, err := st.Sweep(24*time.Hour, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "CHANGELOG.md"); len(got) != 1 {
		t.Fatal("inside the horizon the attribution must survive")
	}
	clk.advance(2 * time.Hour)
	if _, _, err := st.Sweep(24*time.Hour, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "CHANGELOG.md"); len(got) != 0 {
		t.Fatalf("past the horizon it must be dropped, or a repo with no live session " +
			"accumulates rows nothing can ever remove")
	}
}

func mustHolders(t *testing.T, st *Store, path string) []DirtyRecord {
	t.Helper()
	got, err := st.DirtyHolders(path, false)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustPeers(t *testing.T, st *Store, wt, path, exclude string) []DirtyRecord {
	t.Helper()
	got, err := st.DirtyPeersInWorktree(wt, path, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// SamePath exists so a caller outside the store compares paths the way the
// store KEYED them. strings.EqualFold looks equivalent and is not: it does not
// normalize, so on macOS — which hands out NFD from the filesystem while a
// literal in a Go source file is NFC — it answers "different file" about two
// spellings of one name.
func TestSamePathFoldsCaseAndNormalization(t *testing.T) {
	nfc := "docs/résumé.md"   // é as one rune
	nfd := "docs/résumé.md" // e + combining acute
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "CHANGELOG.md", "CHANGELOG.md", true},
		{"case", "CHANGELOG.md", "changelog.md", true},
		{"normalization", nfc, nfd, true},
		{"normalization and case", strings.ToUpper(nfc), nfd, true},
		{"different files", "docs/a.md", "docs/b.md", false},
		{"prefix is not equality", "docs", "docs/a.md", false},
	}
	for _, tc := range cases {
		if got := SamePath(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: SamePath(%q,%q)=%v, want %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

// A SCAN MAY NEVER ATTRIBUTE. git status describes a working TREE, and several
// sessions share one, so its output is the union of everybody's edits. An
// earlier version made it authoritative for the scanning session: measured, a
// session that had done nothing but a read became a recorded holder of a
// peer's hunk, and with six sessions in one checkout every session would hold
// every dirty file. RetainDirty may only remove.
func TestRetainDirtyNeverAddsARow(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
		t.Fatal(err)
	}

	// bravo's scan sees alpha's dirty file, because they share the checkout.
	clk.advance(time.Second)
	if err := st.RetainDirty("/wt/a", []string{"CHANGELOG.md", "docs/status.md"}, clk.now()); err != nil {
		t.Fatal(err)
	}
	got := labels(mustHolders(t, st, "CHANGELOG.md"))
	if has(got, "bravo") {
		t.Fatalf("a scan attributed a peer's edit to the scanner: %v", got)
	}
	if !has(got, "alpha") {
		t.Fatalf("and it must not have disturbed the real holder either: %v", got)
	}
	if n := len(mustHolders(t, st, "docs/status.md")); n != 0 {
		t.Fatalf("a path git calls dirty but nobody recorded must stay unattributed, got %d holders", n)
	}
}

// Retraction is session-agnostic and therefore safe in a shared checkout: a
// path git reports CLEAN is clean for EVERYONE, so a scan retracts every
// session's row for it. That is also the only way a dead session's row is ever
// cleaned — no scan of its own will run again.
func TestACleanPathRetractsEveryHoldersRow(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/a")
	for _, si := range []SessionInfo{a, b} {
		if err := st.RecordDirty(si.SessionID, "/wt/a", []string{"CHANGELOG.md"}); err != nil {
			t.Fatal(err)
		}
	}
	// A scan proving the file clean retracts EVERY session's row for it: if git
	// says nobody has uncommitted changes, every row naming it is false. That
	// is also the only way a dead session's row is ever cleaned, since no scan
	// of its own will run again.
	clk.advance(time.Second)
	if err := st.RetainDirty("/wt/a", nil, clk.now()); err != nil {
		t.Fatal(err)
	}
	if got := labels(mustHolders(t, st, "CHANGELOG.md")); len(got) != 0 {
		t.Fatalf("a clean path must retract every holder, not just the scanner: %v", got)
	}
}

// The `before` fence: git ran BEFORE the retraction transaction, so a row
// recorded in the meantime describes an edit the scan could not have seen.
// Dropping it would lose an attribution that is live and correct.
func TestRetainDirtyDoesNotDropARowWrittenAfterTheScanStarted(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")

	started := clk.now() // a scan begins; git reports the tree clean
	clk.advance(time.Second)
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"raced.go"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RetainDirty("/wt/a", nil, started); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "raced.go"); len(got) != 1 {
		t.Fatal("a row written after the scan started must survive it, or a live edit " +
			"loses its attribution to a result that predates it")
	}

	// The BOUNDARY, which the doc comment promises explicitly and which `<=`
	// would silently break. Unix-second resolution makes last_seen == cutoff
	// the COMMON production case, not an edge one: beat takes `started` and
	// RecordDirty stamps within the same beat.
	same := clk.now()
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"sameSecond.go"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RetainDirty("/wt/a", nil, same); err != nil {
		t.Fatal(err)
	}
	if got := mustHolders(t, st, "sameSecond.go"); len(got) != 1 {
		t.Fatal("a row written in the SAME second the scan started must survive: the " +
			"boundary must err toward keeping a stale row over dropping a fresh one")
	}
}

// A directory is a legitimate subject: "whose is src/?" was answered "clean"
// for want of any prefix handling, in a tree full of dirty files under it.
func TestDirtyHoldersCanAnswerForADirectory(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{
		"src/api/handler.go", "src/main.go", "srcfile.go", "docs/x.md"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.DirtyHolders("src", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("a directory must gather what is under it, got %v", paths(got))
	}
	// "srcfile.go" starts with "src" and is NOT under src/ — a prefix match on
	// the bare string would swallow it.
	for _, p := range paths(got) {
		if p == "srcfile.go" || p == "docs/x.md" {
			t.Fatalf("a sibling sharing a name prefix is not inside the directory: %v", paths(got))
		}
	}
	// As a FILE the same name matches nothing, which is the old behaviour and
	// still right: a caller that did not stat a directory gets exact matching.
	if got, _ := st.DirtyHolders("src", false); len(got) != 0 {
		t.Fatalf("exact matching must not silently become a prefix match: %v", paths(got))
	}
}

// A directory whose name contains a LIKE wildcard must not match its siblings.
func TestADirectoryNameWithWildcardsIsEscaped(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordDirty(a.SessionID, "/wt/a", []string{"a_b/inside.go", "axb/outside.go"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.DirtyHolders("a_b", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "a_b/inside.go" {
		t.Fatalf("_ is a LIKE wildcard and must be escaped, or a_b/ swallows axb/: %v", paths(got))
	}
}
