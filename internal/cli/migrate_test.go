package cli

// Issue #79 (D-136) at the binary: a dev build run in a linked worktree does
// not migrate the checkout's shared ledger, and the binary the hook lines run
// ($HOME/bin/buddy) does, after a backup. The reproduction first: at main
// 7b17cc2 the refused half of TestDevBuildDoesNotMigrateTheSharedLedger fails
// (the dev build migrates the shared ledger in place, with no backup).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHooksBinaryPermit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if ok, why := hooksBinaryPermit(); ok || !strings.Contains(why, "there is no "+filepath.Join(home, "bin", "buddy")) {
		t.Fatalf("no $HOME/bin/buddy: %v %q", ok, why)
	}
	if err := os.Mkdir(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "bin", "buddy")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, why := hooksBinaryPermit(); ok || !strings.Contains(why, "it is not "+other) || !strings.Contains(why, exe) {
		t.Fatalf("another file at $HOME/bin/buddy: %v %q", ok, why)
	}
	// The control: a symlink to this very executable is the hooks' binary.
	os.Remove(other)
	if err := os.Symlink(exe, other); err != nil {
		t.Fatal(err)
	}
	if ok, why := hooksBinaryPermit(); !ok {
		t.Fatalf("control: $HOME/bin/buddy -> this executable must permit: %q", why)
	}
	for _, h := range []string{"relative/home", ""} {
		t.Setenv("HOME", h)
		if ok, why := hooksBinaryPermit(); ok || !strings.Contains(why, "unset or relative") {
			t.Fatalf("HOME %q must not permit, and must say why: %v %q", h, ok, why)
		}
	}
}

func TestDevBuildDoesNotMigrateTheSharedLedger(t *testing.T) {
	boundedParallel(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dev := filepath.Join(dir, "dev", "buddy")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// A plain build, as a lane's `go build`/`go run` makes one: no stamp.
	build := exec.CommandContext(ctx, "go", "build", "-o", dev, "./cmd/buddy")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	f := newFixture(t)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(bin string, args ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = f.wtB // a LINKED worktree: the ledger is the common dir's
		// Never os.Environ(): the suite runs under a Claude session.
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		return out.String(), errb.String(), err
	}
	if _, stderr, err := run(dev, "init"); err != nil {
		t.Fatalf("a fresh ledger is created by any build: %v %s", err, stderr)
	}
	db := filepath.Join(f.repo, ".git", "buddy.db")
	old := ledgerVersion(t, db) - 1
	setLedgerVersion(t, db, old)

	_, stderr, err := run(dev, "ls")
	if err == nil {
		t.Fatalf("the dev build opened an older shared ledger: %s", stderr)
	}
	for _, want := range []string{"refusing to migrate a shared ledger", "there is no " + filepath.Join(home, "bin", "buddy"), "Fix: install this build"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must say %q: %s", want, stderr)
		}
	}
	if v := ledgerVersion(t, db); v != old {
		t.Fatalf("the dev build migrated the shared ledger to %d", v)
	}
	if m, _ := filepath.Glob(db + ".pre-schema*"); len(m) != 0 {
		t.Fatalf("a refused open wrote %v", m)
	}

	// The control: the same binary, as $HOME/bin/buddy, is what the hooks
	// run, so it migrates, and backs up first.
	hooks := filepath.Join(home, "bin", "buddy")
	if err := os.Link(dev, hooks); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(hooks, "ls"); err != nil {
		t.Fatalf("control: the hooks' binary must migrate: %v %s", err, stderr)
	}
	if v := ledgerVersion(t, db); v != old+1 {
		t.Fatalf("control: not migrated: %d", v)
	}
	bak := fmt.Sprintf("%s.pre-schema%d.bak", db, old+1)
	if v := ledgerVersion(t, bak); v != old {
		t.Fatalf("control: the backup reads %d, want %d", v, old)
	}
}

// At the gate the refusal is "ledger exists but cannot be opened": DENY,
// naming the cause and the fix (invariant 3, D-084's register). The test
// binary is never $HOME/bin/buddy, so in-process it is refused; the control
// is the same call at the binary's own version.
func TestGateDeniesARefusedMigration(t *testing.T) {
	// Not parallel: the permit reads the PROCESS environment (it is set at
	// init, before any Env exists), so the test pins it rather than depend
	// on the machine's HOME or an exported BUDDY_MIGRATE (the refusal lens).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BUDDY_MIGRATE", "")
	f := newFixture(t)
	f.initAndHello(t)
	db := filepath.Join(f.repo, ".git", "buddy.db")
	edit := hookJSON("sess-a", f.wtB, "Edit", filepath.Join(f.wtB, "x.go"))
	out, _, _ := f.run(t, f.wtB, edit, "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatalf("control: an unclaimed edit at the binary's version is allowed, got %q", out)
	}
	setLedgerVersion(t, db, ledgerVersion(t, db)-1)
	out, _, _ = f.run(t, f.wtB, edit, "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("a refused migration must fail CLOSED, got %q", out)
	}
	for _, want := range []string{"refusing to migrate a shared ledger", "Fix: install this build", "BUDDY_MIGRATE=1"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the deny must say %q: %s", want, reason)
		}
	}
}

func ledgerVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return v
}

func setLedgerVersion(t *testing.T, path string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
		t.Fatal(err)
	}
}
