package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
	"github.com/JsizzleR/buddy-system/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.ScrubGitEnv()
	os.Exit(m.Run())
}

func TestCLILedgerRealBinaryCapturedQueueAndLifecycle(t *testing.T) {
	for _, name := range []string{"BUDDY_SESSION", "BUDDY_HARNESS", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(name, "")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "buddy")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/buddy")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Buddy: %v %s", err, out)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	st, err := store.Open(filepath.Join(repo, ".git", "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l := CLILedger{Binary: binary, Worktree: repo}
	worker, err := l.Begin(ctx, "claude", "claude-native", "Claude worker")
	if err != nil {
		t.Fatal(err)
	}
	if !worker.Valid() || worker.Harness != "claude" || worker.SessionID != "claude-native" {
		t.Fatalf("Claude identity not preserved: %+v", worker)
	}
	sender, err := l.Begin(ctx, "codex", "codex-native", "Codex sender")
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Valid() || sender.Harness != "codex" || sender.SessionID != "codex-native" {
		t.Fatalf("Codex identity not preserved: %+v", sender)
	}
	from := store.AdapterIdentity{SessionID: sender.SessionID, Incarnation: sender.Incarnation}
	to := store.AdapterIdentity{SessionID: worker.SessionID, Incarnation: worker.Incarnation}
	var last int64
	for n := 1; n <= 21; n++ {
		d, err := st.QueueAdapter(from, to, "assignment "+strconv.Itoa(n))
		if err != nil {
			t.Fatal(err)
		}
		last = d.MessageID
	}
	first, err := st.AdapterInbox(to)
	if err != nil || first.Count != 21 || len(first.Items) != 20 || !first.More {
		t.Fatalf("bounded fixture not armed: %+v %v", first, err)
	}
	for n := 0; n < 2; n++ {
		queued, err := l.Queued(ctx, worker, last)
		if err != nil || !queued {
			t.Fatalf("exact 21st message became absence: %v %v", queued, err)
		}
	}
	after, err := st.AdapterInbox(to)
	if err != nil || after.Count != 21 {
		t.Fatalf("queue lookup consumed messages: %+v %v", after, err)
	}
	if got, err := l.Queued(ctx, worker, last+100); err != nil || got {
		t.Fatalf("nonexistent message not absent: %v %v", got, err)
	}
	if got, err := l.Queued(ctx, sender, last); err != nil || got {
		t.Fatalf("another recipient's message considered queued: %v %v", got, err)
	}
	if _, err := st.DeliverAdapter(to, last); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Queued(ctx, worker, last); err != nil || got {
		t.Fatalf("recorded body delivery still queued: %v %v", got, err)
	}
	for _, event := range []string{"busy", "idle", "refresh"} {
		if err := l.Event(ctx, worker, event); err != nil {
			t.Fatalf("Claude %s: %v", event, err)
		}
	}
	if _, err := l.Inspect(ctx, worker); err != nil {
		t.Fatal(err)
	}
	// The actual lifecycle envelope must retain the requested native harness;
	// the bridge cannot silently reuse the CLI's historical Codex default.
	raw, err := l.call(ctx, "lifecycle", "refresh", "--harness", worker.Harness, "--session", worker.SessionID, "--incarnation", worker.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	var refreshed Identity
	if err := json.Unmarshal(raw, &refreshed); err != nil {
		t.Fatal(err)
	}
	if !refreshed.Same(worker) {
		t.Fatalf("lifecycle changed captured identity/harness: %+v", refreshed)
	}
	if err := l.Event(ctx, worker, "end"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Inspect(ctx, worker); err == nil {
		t.Fatal("ended receipt passed live inspection")
	}
	next, err := st.BeginLogical(worker.SessionID, "", repo, worker.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Event(ctx, worker, "busy"); err == nil {
		t.Fatal("late bridge event acquired successor token")
	}
	if _, err := l.Queued(ctx, worker, last-1); err == nil {
		t.Fatal("late bridge inbox read acquired successor token")
	}
	current := worker
	current.Incarnation = next.Incarnation
	if _, err := l.Inspect(ctx, current); err != nil {
		t.Fatalf("successor positive control: %v", err)
	}
	if got, err := l.Queued(ctx, current, last-1); err != nil || got {
		t.Fatalf("predecessor message migrated to successor: %v %v", got, err)
	}
}
