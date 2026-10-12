package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F-23: the executable string was resolved once for --version and again for
// launch, so an updater re-pointing ~/.local/bin/claude between the two ran an
// unchecked binary; relative paths were checked against one cwd and run in
// another. Now: absolute only, resolved once, both steps on the same file.
func TestNativeExecutableIsAbsoluteAndResolvedOnce(t *testing.T) {
	// Relative names that DO resolve from the test's cwd, so only the
	// absolute-path check can refuse them.
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", filepath.Join("bin", "codex")} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	for _, relative := range []string{"claude", "./claude", "bin/codex"} {
		if _, err := NewClaude(relative); err == nil {
			t.Errorf("NewClaude accepted relative %q", relative)
		}
		if _, err := NewCodex(relative); err == nil {
			t.Errorf("NewCodex accepted relative %q", relative)
		}
	}
	_, launch, ctx := claudeFixture(t)
	dir := t.TempDir()
	checked := filepath.Join(dir, "claude-2.1.289")
	other := filepath.Join(dir, "claude-2.1.288")
	if err := os.WriteFile(checked, []byte(claudeFake), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(strings.ReplaceAll(claudeFake, claudeContractVersion, "2.1.288")), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(checked, link); err != nil {
		t.Fatal(err)
	}
	b, err := NewClaude(link)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	want, err := filepath.EvalSymlinks(checked)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.(*claudeBackend).executable; got != want {
		t.Fatalf("executable not resolved once: %q, want %q", got, want)
	}
	// The updater re-points the link after construction.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(ctx, launch, testAuthorization(t)); err != nil {
		t.Fatalf("the resolved, checked binary did not launch: %v", err)
	}
	// Control: resolving the re-pointed link now sees the other version, so
	// the launch above really was pinned to the first resolution.
	late, err := NewClaude(link)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	if _, err := late.Start(ctx, launch, testAuthorization(t)); err == nil {
		t.Fatal("control: the re-pointed link should have failed the version check")
	}
}

// The file whose version was checked must still be the file launched.
func TestNativeExecutableChangedAfterVersionCheckRefuses(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "unchanged-positive-control"
		if replace {
			name = "replaced-after-version-check"
		}
		t.Run(name, func(t *testing.T) {
			c, _, launch := codexFixture(t)
			started := false
			start := c.start
			c.start = func(ctx context.Context, auth *LaunchAuthorization, exe string, args []string, cwd string, env []string) (codexProcess, error) {
				started = true
				return start(ctx, auth, exe, args, cwd, env)
			}
			c.version = func(context.Context, string) (string, error) {
				if replace {
					next := c.executable + ".new"
					if err := os.WriteFile(next, []byte("#!/bin/sh\necho swapped\n"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(next, c.executable); err != nil {
						t.Fatal(err)
					}
				}
				return "codex-cli 0.160.0", nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.Start(ctx, launch, testAuthorization(t))
			if replace {
				if err == nil || started {
					t.Fatalf("launched a binary replaced after its version check: err=%v started=%v", err, started)
				}
				return
			}
			if err != nil || !started {
				t.Fatalf("unchanged binary refused: %v", err)
			}
		})
	}
}
