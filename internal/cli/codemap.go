package cli

// Language servers: `buddy init` says whether a Claude Code session in this
// repo will get code lookups (definition, references, symbols) for the
// languages the repo is mostly written in, and names the fix when it will not
// (D-051).
//
// THE COST (scripts/startup-report.sh, 30 days to 2026-09-26: 14 sessions
// here reached an edit). A fresh session spends a median 22 minutes before
// its first edit to a repo file (an upper bound: it includes the operator's
// own turns), in a median 29 tool calls returning 89 KB, and 0 of 518 calls
// were LSP. By hand, the same sessions map the code by grep and `sed -n`
// through Bash, and 277 of those commands name internal/cli/cli.go. The cost
// is ROUND TRIPS, not bytes. Claude Code answers "where is X defined, who
// calls it" in one call through a language server plugin, and none was
// installed. Several sessions in parallel each pay it again.
//
// THE TRAP, measured the same day while fixing the first. `go install
// golang.org/x/tools/gopls@latest` succeeded and put gopls in ~/go/bin, which
// this machine's PATH does not name. The gopls-lsp plugin runs `gopls` from
// PATH, so it would have been ENABLED with nothing to run, and every session
// would have gone back to grep with nothing on screen to say so. A server
// installed and a plugin enabled are two facts, both checked, and "found
// where go put it but not on PATH" is its own diagnosis.
//
// WHERE, AND WHERE NOT. `init` is the step an operator runs in each repo they
// turn buddy on in, and setup-clone.sh (so install.sh) runs it for this
// checkout: the operator is the one who can install things, and init is the
// moment they are looking. Cut:
//   - a line in the SessionStart digest: every session pays for it, forever,
//     in the budget D-034/D-036 ration;
//   - installing the server or enabling the plugin: that edits the
//     operator's machine and ~/.claude/settings.json, which install.sh
//     deliberately never does (D-050);
//   - asking `claude plugin list --json`: it would make the claims binary
//     fork the harness's own CLI, which a hermetic test cannot have. The
//     settings files are the record that command reads.
//   - a generated code map committed to the repo: stale the moment a
//     parallel session edits, and a stale map misleads (Codex, same pass).
//
// WHAT IT SAYS, and all it says: what the settings files and PATH hold when
// init runs — hence "configured", never "ready". An id enabled in settings is
// the first of the harness's stages (declared, fetched, loaded), and only the
// session can see the last: its own tool list, which is why the skill has it
// check (Codex, D-051 code pass). Of the harness's six settings sources only
// user, project and local are read: managed (whose true/false nothing
// overrides), --settings and --add-dir are not, so a plugin forced on or
// blocked there reads wrong here. The PATH is init's own, which is the
// harness's only when claude was started from the same shell: the harness
// starts a server "by name from the user's PATH" (plugins docs). It never changes init's exit status: whatever
// it cannot read, it prints and carries on — and it never BLOCKS on a read
// (a settings path that is a FIFO held the first draft open forever).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// languageServer is one row of the official marketplace's LSP plugins
// (claude-plugins-official, .claude-plugin/marketplace.json, read
// 2026-09-26): the command the plugin runs and the extensions it takes.
// Only the plugins whose server installs in ONE command are listed; a
// language not here is silent rather than told something half right.
type languageServer struct {
	lang    string
	exts    []string
	server  string // the command the plugin runs, looked up on PATH
	plugin  string // its name in lsMarketplace
	install string // one command that puts server on the machine
	goBin   bool   // installed by `go install`, which lands off PATH on many machines
}

var languageServers = []languageServer{
	{lang: "go", exts: []string{".go"}, server: "gopls", plugin: "gopls-lsp",
		install: "go install golang.org/x/tools/gopls@latest", goBin: true},
	{lang: "python", exts: []string{".py", ".pyi"}, server: "pyright-langserver", plugin: "pyright-lsp",
		install: "npm install -g pyright"},
	{lang: "typescript/javascript", exts: []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
		server: "typescript-language-server", plugin: "typescript-lsp",
		install: "npm install -g typescript-language-server typescript"},
	{lang: "rust", exts: []string{".rs"}, server: "rust-analyzer", plugin: "rust-analyzer-lsp",
		install: "rustup component add rust-analyzer"},
	// Extensions match exactly, never folded: .C and .H are C++ by convention.
	{lang: "c/c++", exts: []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hxx", ".C", ".H"},
		server: "clangd", plugin: "clangd-lsp", install: "xcode-select --install"},
	{lang: "swift", exts: []string{".swift"}, server: "sourcekit-lsp", plugin: "swift-lsp",
		install: "xcode-select --install"},
	{lang: "ruby", exts: []string{".rb", ".rake", ".gemspec", ".ru", ".erb"}, server: "ruby-lsp",
		plugin: "ruby-lsp", install: "gem install ruby-lsp"},
}

const (
	lsMarketplace = "claude-plugins-official"
	// A language is reported when it is at least this share (percent) of the
	// repo's recognised source files. A Go repo with one Python script is
	// not a Python repo, and a warning about it is one the operator learns to
	// skip — along with the warning that mattered.
	lsMinSharePct = 10
	// A settings file is a few KB; one larger than this is not read whole.
	lsSettingsCap = 1 << 20
)

// lsListBudget bounds the file listing: init is interactive, not a hook. A
// variable so a test can shorten it.
var lsListBudget = 10 * time.Second

// reportLanguageServers writes init's language-server lines. It returns
// nothing: a report it cannot complete says so and stops, and init goes on.
func reportLanguageServers(w io.Writer, top string, env Env) {
	if top == "" {
		return // a bare repo has no files to count
	}
	counts, err := sourceCounts(top)
	if err != nil {
		fmt.Fprintf(w, "language server: could not list this repo's files (%s)\n", fence.Line(err.Error(), 200))
		return
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	var main []int // indexes into languageServers
	for i := range languageServers {
		if n := counts[i]; n > 0 && n*100 >= total*lsMinSharePct {
			main = append(main, i)
		}
	}
	if len(main) == 0 {
		return
	}
	sort.SliceStable(main, func(a, b int) bool { return counts[main[a]] > counts[main[b]] })

	plugins, unread := enabledPlugins(top, env)
	for _, u := range unread {
		fmt.Fprintf(w, "language server: could not read %s\n", fence.Line(u, 600))
	}
	path := env.getenv("PATH")
	for _, i := range main {
		fmt.Fprint(w, languageLine(languageServers[i], counts[i], path, plugins, env))
	}
}

// languageLine is one language's verdict and, when it is not configured, the fix.
func languageLine(ls languageServer, n int, path string, plugins map[string]pluginState, env Env) string {
	head := fmt.Sprintf("language server: %s (%d files) — ", ls.lang, n)
	serverOK := onPath(ls.server, path) != ""
	ps := plugins[ls.plugin+"@"+lsMarketplace]
	pluginOK := ps.set && ps.on
	if serverOK && pluginOK {
		return head + fmt.Sprintf("configured: %s on PATH, %s enabled in settings\n", ls.server, ls.plugin)
	}

	var bad, good, fix []string
	if serverOK {
		good = append(good, ls.server+" on PATH")
	} else {
		bad = append(bad, ls.server+" not on PATH")
		dir := ""
		if ls.goBin {
			dir = goInstallDir(env)
		}
		switch {
		case dir != "" && isExecutable(filepath.Join(dir, ls.server)):
			fix = append(fix, fmt.Sprintf("put %s on PATH (%s is there)", fence.Line(dir, 512), ls.server))
		default:
			fix = append(fix, ls.install)
			// Following the fix must fix it: an install that lands off PATH
			// takes the PATH step with it (Codex second pass — the first cut
			// said so in a note below a fix that ended in /reload-plugins).
			if dir != "" && !pathNames(path, dir) {
				fix = append(fix, fmt.Sprintf("put %s on PATH (go install puts %s there)", fence.Line(dir, 512), ls.server))
			}
		}
	}
	switch {
	case pluginOK:
		good = append(good, ls.plugin+" enabled")
	case ps.set:
		bad = append(bad, fmt.Sprintf("%s disabled in %s", ls.plugin, fence.Line(ps.file, 512)))
		// --scope of the file that said false: without it, `plugin enable`
		// auto-detects a scope, which need not be the one that disabled it.
		fix = append(fix, "claude plugin enable --scope "+ps.scope+" "+fence.Line(ps.key, 128))
	default:
		bad = append(bad, ls.plugin+" not enabled")
		fix = append(fix, "claude plugin install "+ls.plugin+"@"+lsMarketplace)
	}

	line := head + "NOT CONFIGURED: " + strings.Join(bad, "; ")
	if len(good) > 0 {
		line += " (" + strings.Join(good, ", ") + ")"
	}
	// A running session picks up a plugin, and restarts its servers, on
	// /reload-plugins (verified against the harness docs, D-051: "only a new
	// session" was this file's first claim, and wrong). It never picks up a
	// new PATH: that is fixed when claude starts.
	then := ", then run /reload-plugins in open sessions"
	for _, f := range fix {
		if strings.HasPrefix(f, "put ") {
			then = ", then start claude again from a shell whose PATH has it"
		}
	}
	return line + "\n  fix: " + joinSteps(fix) + then + "\n"
}

// joinSteps chains commands with && and puts a prose step ("put X on PATH")
// behind a semicolon, so no command's success waits on a sentence. Pasted
// whole, a line with a prose step is a syntax error in bash and, in zsh, a
// "command not found: put" followed by the commands after it (Fable, D-051):
// the directory it names comes from the operator's own environment, never
// from the repo, and is fenced.
func joinSteps(steps []string) string {
	var b strings.Builder
	for i, s := range steps {
		if i > 0 {
			if strings.HasPrefix(s, "put ") || strings.HasPrefix(steps[i-1], "put ") {
				b.WriteString("; ")
			} else {
				b.WriteString(" && ")
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// sourceCounts counts the repo's TRACKED files (the index, so a staged file
// counts) per languageServers row. Not --others: an un-ignored virtualenv or
// build directory would be walked and counted as what the repo is written
// in. Streamed, so a huge repo's listing is never held whole; an unmerged
// path is listed once per stage, and ls-files output is sorted, so a repeat
// is always the line before.
func sourceCounts(top string) (map[int]int, error) {
	byExt := map[string]int{}
	for i, ls := range languageServers {
		for _, e := range ls.exts {
			byExt[e] = i
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsListBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", top, "ls-files", "-z")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// The context kills git, not what git forked: a wrapper whose child holds
	// stdout keeps the pipe open, and the Scanner below would wait for that
	// child however long it runs (Codex second pass; reproduced with a shim
	// that backgrounds a sleep). So the budget closes the pipe itself.
	stop := context.AfterFunc(ctx, func() { out.Close() })
	defer stop()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	counts := map[int]int{}
	prev := ""
	for sc.Scan() {
		p := sc.Text()
		if p == "" || p == prev {
			continue
		}
		prev = p
		if i, ok := byExt[filepath.Ext(p)]; ok {
			counts[i]++
		}
	}
	scanErr := sc.Err()
	if scanErr != nil {
		io.Copy(io.Discard, out) // let git finish rather than die on a closed pipe
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("listing took longer than %v", lsListBudget)
		}
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("listing took longer than %v", lsListBudget)
	}
	return counts, scanErr
}

// pluginState is what the settings files say about one plugin id, the
// highest-precedence file that names it winning.
type pluginState struct {
	set   bool   // some file names it
	on    bool   // and that file says true
	key   string // the id, "name@marketplace"
	file  string // the file that decided
	scope string // its scope as `claude plugin --scope` names it
}

// enabledPlugins reads enabledPlugins from the three settings files Claude
// Code layers — user, then the repo's shared, then its local — later
// overriding earlier, keyed by FULL plugin id, the way the harness merges
// them. The first draft keyed by the name before '@', so another
// marketplace's `gopls-lsp@other: false` in local settings reported the
// official plugin disabled (Codex). Only lsMarketplace ids are ever looked
// up: a same-named plugin from elsewhere need not run the same server.
// unread names every file that exists and could not be read or parsed.
func enabledPlugins(top string, env Env) (map[string]pluginState, []string) {
	type layer struct{ path, scope string }
	var files []layer
	if dir := env.getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		files = append(files, layer{filepath.Join(dir, "settings.json"), "user"})
	} else if home := env.getenv("HOME"); home != "" {
		files = append(files, layer{filepath.Join(home, ".claude", "settings.json"), "user"})
	}
	files = append(files,
		layer{filepath.Join(top, ".claude", "settings.json"), "project"},
		layer{filepath.Join(top, ".claude", "settings.local.json"), "local"})

	states := map[string]pluginState{}
	var unread []string
	for _, l := range files {
		f := l.path
		raw, err := readSettings(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var doc struct {
			EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &doc)
		}
		if err != nil {
			unread = append(unread, fmt.Sprintf("%s (%s)", f, errReason(err)))
			continue
		}
		for k, raw := range doc.EnabledPlugins {
			v := string(bytes.TrimSpace(raw))
			if v != "true" && v != "false" {
				continue // a shape this reader does not know says nothing
			}
			states[k] = pluginState{set: true, on: v == "true", key: k, file: f, scope: l.scope}
		}
	}
	return states, unread
}

// readSettings reads one settings file without ever blocking: opened
// O_NONBLOCK (a FIFO with no writer otherwise blocks the open itself), then
// refused unless it is a regular file, and read no further than
// lsSettingsCap.
func readSettings(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, lsSettingsCap+1))
	if err == nil && len(raw) > lsSettingsCap {
		err = fmt.Errorf("larger than %d bytes", lsSettingsCap)
	}
	return raw, err
}

// errReason is an error without the path os errors repeat.
func errReason(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// goInstallDir is where `go install` puts a binary: GOBIN, else the first
// GOPATH entry's bin, else ~/go/bin. "" when none can be named ABSOLUTELY: a
// relative one names a place relative to init's cwd, which `go install`
// itself refuses for GOBIN, and "put bin on PATH" means nothing elsewhere.
func goInstallDir(env Env) string {
	if d := env.getenv("GOBIN"); d != "" {
		return absOrEmpty(d)
	}
	if gp := env.getenv("GOPATH"); gp != "" {
		if first := filepath.SplitList(gp)[0]; first != "" {
			return absOrEmpty(filepath.Join(first, "bin"))
		}
	}
	if home := env.getenv("HOME"); home != "" {
		return absOrEmpty(filepath.Join(home, "go", "bin"))
	}
	return ""
}

func absOrEmpty(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return ""
}

// onPath is exec.LookPath against the PATH given rather than the process's
// own, so a test can hand it one. Only ABSOLUTE entries are searched: an
// empty one (POSIX "the cwd"), ".", or "bin" names a place relative to
// wherever init ran, and a server found only because init ran next to one is
// not a server the harness will find. (The first cut skipped only the empty
// entry; Fable's adversarial pass found "." and "bin" honoured.)
func onPath(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p := filepath.Join(dir, name); isExecutable(p) {
			return p
		}
	}
	return ""
}

func pathNames(path, dir string) bool {
	for _, d := range filepath.SplitList(path) {
		if filepath.IsAbs(d) && filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// isExecutable: a regular file THIS user may execute. Any x bit is not
// enough — 0o001 is execute for others only, and its owner cannot run it
// (Codex). access(2) asks the kernel, with the real uid, which is init's.
func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && syscall.Access(p, 0x1 /* X_OK */) == nil
}
