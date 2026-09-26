package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// codeMapRepo adds count source files per extension to the fixture repo and
// stages them: init counts TRACKED files (the index), so a staged file counts
// and an untracked one does not.
func codeMapRepo(t *testing.T, f *fixture, files map[string]int) {
	t.Helper()
	for ext, n := range files {
		for i := 0; i < n; i++ {
			p := filepath.Join(f.repo, "src", fmt.Sprintf("f%d%s", i, ext))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = f.repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
}

// fakeServer puts an executable named name in dir.
func fakeServer(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func writeSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInitReportsLanguageServers: `buddy init` says, per language the repo is
// mostly written in, whether a Claude Code session there gets a language
// server — the server on PATH AND its plugin enabled — and names the fix when
// it does not (D-051). Every NOT CONFIGURED row has the "configured" row as its
// control: the same fixture with the missing piece supplied.
func TestInitReportsLanguageServers(t *testing.T) {
	const gopls = `{"enabledPlugins":{"gopls-lsp@claude-plugins-official":true}}`
	cases := []struct {
		name  string
		files map[string]int
		setup func(t *testing.T, f *fixture, home, bin string)
		want  []string // substrings, in order
		deny  []string
	}{
		{
			name:  "ready: server on PATH and plugin enabled",
			files: map[string]int{".go": 3},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
			},
			want: []string{"language server: go (3 files) — configured: gopls on PATH, gopls-lsp enabled in settings"},
			deny: []string{"NOT CONFIGURED", "fix:"},
		},
		{
			name:  "neither: install and enable in one fix line",
			files: map[string]int{".py": 4},
			want: []string{
				"language server: python (4 files) — NOT CONFIGURED: pyright-langserver not on PATH; pyright-lsp not enabled",
				"  fix: npm install -g pyright && claude plugin install pyright-lsp@claude-plugins-official, then run /reload-plugins in open sessions",
			},
			deny: []string{"— configured:"},
		},
		{
			name:  "server present, plugin not enabled",
			files: map[string]int{".py": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "pyright-langserver", 0o755)
			},
			want: []string{
				"NOT CONFIGURED: pyright-lsp not enabled",
				"  fix: claude plugin install pyright-lsp@claude-plugins-official, then run /reload-plugins in open sessions",
			},
			deny: []string{"npm install", "not on PATH"},
		},
		{
			// The trap measured 2026-09-26: `go install` put gopls in ~/go/bin,
			// PATH did not name it, and the plugin would have been enabled with
			// nothing to run — sessions silently back on grep.
			name:  "gopls installed where go put it, not on PATH",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, filepath.Join(home, "go", "bin"), "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
			},
			want: []string{
				"NOT CONFIGURED: gopls not on PATH",
				"gopls-lsp enabled",
				"  fix: put " + "HOME/go/bin on PATH (gopls is there), then start claude again from a shell whose PATH has it",
			},
			deny: []string{"go install"},
		},
		{
			name:  "gopls missing and go install would land off PATH",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
			},
			want: []string{
				"  fix: go install golang.org/x/tools/gopls@latest, then run /reload-plugins in open sessions",
				"  note: go install puts gopls in HOME/go/bin, which is not on PATH",
			},
		},
		{
			name:  "GOBIN on PATH: no note",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				f.env["GOBIN"] = bin
			},
			want: []string{"  fix: go install golang.org/x/tools/gopls@latest && claude plugin install gopls-lsp@claude-plugins-official"},
			deny: []string{"note:"},
		},
		{
			name:  "a non-executable file is not a server",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o644)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
			},
			want: []string{"NOT CONFIGURED: gopls not on PATH"},
		},
		{
			name:  "enabled in the project's settings counts",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(f.repo, ".claude", "settings.json"), gopls)
			},
			want: []string{"configured: gopls on PATH, gopls-lsp enabled in settings"},
		},
		{
			name:  "disabled in local settings overrides enabled at user scope",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
				writeSettings(t, filepath.Join(f.repo, ".claude", "settings.local.json"),
					`{"enabledPlugins":{"gopls-lsp@claude-plugins-official":false}}`)
			},
			want: []string{
				"NOT CONFIGURED: gopls-lsp disabled in " + "REPO/.claude/settings.local.json",
				"  fix: claude plugin enable --scope local gopls-lsp@claude-plugins-official, then run /reload-plugins in open sessions",
			},
		},
		{
			// `plugin enable` without --scope auto-detects which file to
			// write; the fix names the scope of the file that said false.
			name:  "disabled at user scope: the fix names user scope",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"),
					`{"enabledPlugins":{"gopls-lsp@claude-plugins-official":false}}`)
			},
			want: []string{
				"NOT CONFIGURED: gopls-lsp disabled in HOME/.claude/settings.json",
				"  fix: claude plugin enable --scope user gopls-lsp@claude-plugins-official",
			},
		},
		{
			name:  "CLAUDE_CONFIG_DIR replaces ~/.claude",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				cfg := filepath.Join(home, "alt")
				f.env["CLAUDE_CONFIG_DIR"] = cfg
				writeSettings(t, filepath.Join(cfg, "settings.json"), gopls)
			},
			want: []string{"configured: gopls on PATH, gopls-lsp enabled in settings"},
		},
		{
			name:  "unreadable settings are said, and init still succeeds",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":`)
			},
			want: []string{
				"language server: could not read HOME/.claude/settings.json",
				"NOT CONFIGURED: gopls-lsp not enabled",
			},
		},
		{
			// Claude Code merges enabledPlugins by FULL id (Codex, D-051): a
			// false for another marketplace's same-named plugin does not
			// touch the official one.
			name:  "another marketplace's false does not disable the official id",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
				writeSettings(t, filepath.Join(f.repo, ".claude", "settings.local.json"),
					`{"enabledPlugins":{"gopls-lsp@other":false}}`)
			},
			want: []string{"configured: gopls on PATH, gopls-lsp enabled in settings"},
		},
		{
			// ...and another marketplace's TRUE is not the official plugin:
			// nothing says it runs gopls.
			name:  "another marketplace's same-named plugin is not the official one",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				fakeServer(t, bin, "gopls", 0o755)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"),
					`{"enabledPlugins":{"gopls-lsp@other":true,"gopls-lsp":true}}`)
			},
			want: []string{"NOT CONFIGURED: gopls-lsp not enabled"},
		},
		{
			// Execute permission is the CALLER's, not any bit: 0o001 is
			// execute for others only, and the owner running init has none.
			name:  "a server the caller cannot execute is not a server",
			files: map[string]int{".go": 2},
			setup: func(t *testing.T, f *fixture, home, bin string) {
				if os.Geteuid() == 0 {
					t.Skip("root may execute any file with an x bit")
				}
				fakeServer(t, bin, "gopls", 0o001)
				writeSettings(t, filepath.Join(home, ".claude", "settings.json"), gopls)
			},
			want: []string{"NOT CONFIGURED: gopls not on PATH"},
		},
		{
			name:  "a minor language is not reported; the main one is",
			files: map[string]int{".go": 20, ".py": 1},
			setup: func(t *testing.T, f *fixture, home, bin string) {},
			want:  []string{"language server: go (20 files)"},
			deny:  []string{"python"},
		},
		{
			name:  "two main languages, larger first; .mjs is javascript",
			files: map[string]int{".mjs": 6, ".py": 3},
			want: []string{
				"language server: typescript/javascript (6 files)",
				"language server: python (3 files)",
			},
		},
		{
			name:  "no recognised source: init prints only the ledger line",
			files: map[string]int{".md": 5},
			deny:  []string{"language server"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			home := filepath.Join(t.TempDir(), "home")
			bin := filepath.Join(t.TempDir(), "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			f.env["HOME"] = home
			f.env["PATH"] = bin
			codeMapRepo(t, f, tc.files)
			if tc.setup != nil {
				tc.setup(t, f, home, bin)
			}
			out, errw, code := f.run(t, f.repo, "", "init")
			if code != 0 {
				t.Fatalf("init exit %d: %s", code, errw)
			}
			if !strings.HasPrefix(out, "ledger ready: ") {
				t.Fatalf("init no longer leads with the ledger line:\n%s", out)
			}
			// Paths vary per run; the assertions name them symbolically. Git
			// reports the repo by its resolved path (/private/var on macOS),
			// so the resolved spellings go first: a replacer tries its pairs
			// in order at each position.
			rrepo, _ := filepath.EvalSymlinks(f.repo)
			rhome, _ := filepath.EvalSymlinks(filepath.Dir(home))
			norm := strings.NewReplacer(rrepo, "REPO", f.repo, "REPO",
				filepath.Join(rhome, "home"), "HOME", home, "HOME").Replace(out)
			at := 0
			for _, w := range tc.want {
				i := strings.Index(norm[at:], w)
				if i < 0 {
					t.Fatalf("missing (or out of order) %q in:\n%s", w, norm)
				}
				at += i + len(w)
			}
			for _, d := range tc.deny {
				if strings.Contains(norm, d) {
					t.Fatalf("unexpected %q in:\n%s", d, norm)
				}
			}
		})
	}
}

// TestInitLanguageReportNeverFailsInit: whatever the report cannot read, the
// ledger is made and init exits 0. A settings path that is a DIRECTORY fails
// every read; the control is the same repo reporting normally.
func TestInitLanguageReportNeverFailsInit(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	f.env["HOME"] = home
	f.env["PATH"] = t.TempDir()
	codeMapRepo(t, f, map[string]int{".go": 1})
	if err := os.MkdirAll(filepath.Join(home, ".claude", "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errw, code := f.run(t, f.repo, "", "init")
	if code != 0 || !strings.HasPrefix(out, "ledger ready: ") {
		t.Fatalf("init failed over an unreadable settings file: exit %d\n%s%s", code, out, errw)
	}
	if !strings.Contains(out, "could not read") || !strings.Contains(out, "language server: go (1 files)") {
		t.Fatalf("the report stopped instead of saying what it could not read:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".git", "buddy.db")); err != nil {
		t.Fatalf("no ledger after init: %v", err)
	}
}

// TestInitIgnoresCwdRelativeLocations: init's current directory is not the
// harness's. An empty PATH entry (POSIX "the cwd"), a relative one (".",
// "bin") and a relative GOBIN all name places relative to wherever init ran,
// so a gopls found through them is not a server a session will get, and a
// "put bin on PATH" fix means nothing anywhere else (Fable's adversarial pass,
// D-051: the first cut skipped only the empty entry). Control: the same
// directory named by its absolute path is found.
func TestInitIgnoresCwdRelativeLocations(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(here string) map[string]string
		want string
		deny string
	}{
		{"empty PATH entry", func(string) map[string]string { return map[string]string{"PATH": ":" + t.TempDir()} },
			"NOT CONFIGURED: gopls not on PATH", ""},
		{"dot PATH entry", func(string) map[string]string { return map[string]string{"PATH": ".:" + t.TempDir()} },
			"NOT CONFIGURED: gopls not on PATH", ""},
		{"relative PATH entry", func(string) map[string]string { return map[string]string{"PATH": "bin:" + t.TempDir()} },
			"NOT CONFIGURED: gopls not on PATH", ""},
		{"relative GOBIN", func(string) map[string]string {
			return map[string]string{"PATH": t.TempDir(), "GOBIN": "bin"}
		}, "fix: go install golang.org/x/tools/gopls@latest", "put bin"},
		{"control: directory named absolutely", func(here string) map[string]string {
			return map[string]string{"PATH": filepath.Join(here, "bin")}
		}, "— configured: gopls on PATH", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			home := t.TempDir()
			f.env["HOME"] = home
			writeSettings(t, filepath.Join(home, ".claude", "settings.json"),
				`{"enabledPlugins":{"gopls-lsp@claude-plugins-official":true}}`)
			codeMapRepo(t, f, map[string]int{".go": 1})
			here := t.TempDir()
			fakeServer(t, here, "gopls", 0o755)
			fakeServer(t, filepath.Join(here, "bin"), "gopls", 0o755)
			t.Chdir(here)
			for k, v := range tc.env(here) {
				f.env[k] = v
			}
			out, errw, code := f.run(t, f.repo, "", "init")
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want %q in:\n%s%s", code, tc.want, out, errw)
			}
			if tc.deny != "" && strings.Contains(out, tc.deny) {
				t.Fatalf("unexpected %q in:\n%s", tc.deny, out)
			}
		})
	}
}

// TestInitLanguageReportSettingsFIFO: a settings path that is a FIFO with no
// writer blocks a plain open forever (Codex, D-051). init must say it could
// not read it and return. Run in a goroutine, so a regression FAILS the test
// instead of hanging the suite until go test's own timeout.
func TestInitLanguageReportSettingsFIFO(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	f.env["HOME"] = home
	f.env["PATH"] = t.TempDir()
	codeMapRepo(t, f, map[string]int{".go": 1})
	if err := os.MkdirAll(filepath.Join(f.repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(f.repo, ".claude", "settings.json"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, _, code := f.run(t, f.repo, "", "init")
		done <- result{out, code}
	}()
	select {
	case r := <-done:
		if r.code != 0 || !strings.Contains(r.out, "could not read") || !strings.Contains(r.out, "not a regular file") {
			t.Fatalf("exit %d; want the FIFO named as unreadable:\n%s", r.code, r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("init blocked on a FIFO settings file")
	}
}

// TestInitCountsTrackedFilesOnly: an untracked, un-ignored directory (a
// virtualenv nobody ignored) is neither walked nor counted. Control: the same
// files, staged, are.
func TestInitCountsTrackedFilesOnly(t *testing.T) {
	f := newFixture(t)
	f.env["PATH"] = t.TempDir()
	codeMapRepo(t, f, map[string]int{".go": 2})
	venv := filepath.Join(f.repo, "venv")
	for i := 0; i < 50; i++ {
		fakeServer(t, venv, fmt.Sprintf("m%d.py", i), 0o644)
	}
	out, _, _ := f.run(t, f.repo, "", "init")
	if strings.Contains(out, "python") || !strings.Contains(out, "go (2 files)") {
		t.Fatalf("untracked files were counted:\n%s", out)
	}
	cmd := exec.Command("git", "add", "venv")
	cmd.Dir = f.repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, b)
	}
	out, _, _ = f.run(t, f.repo, "", "init")
	if !strings.Contains(out, "python (50 files)") {
		t.Fatalf("control: staged files were not counted:\n%s", out)
	}
}
