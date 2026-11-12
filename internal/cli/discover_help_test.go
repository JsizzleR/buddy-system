package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The digest's pointer at `buddy --help`, and the user-level skill, are the
// only places most sessions learn a verb exists. Both name verbs and flags in
// prose, and prose does not fail to compile: a flag renamed in the usage table
// would leave every session being taught a refusal. So every `buddy <verb>
// --flag` either one names is read back against that verb's own usage line.

var (
	backtickSpan = regexp.MustCompile("`([^`]+)`")
	quotedSpan   = regexp.MustCompile(`"[^"]*"`)
)

// checkNamedVerbs returns one error per verb, subcommand or flag the text
// names that `buddy <verb> --help` does not answer, and how many it checked.
// usageOf is the dispatch table's usage lookup (helpUsage), a parameter so the
// drift test can rename a flag on the USAGE side too.
// A backticked span is read from its `buddy` (so `/loop buddy wait check`
// counts); a span that is only flags (`--shared`) belongs to the verb most
// recently named. Placeholders (<slug>), quoted values and brackets are not
// names. A word straight after the verb is checked only when the verb HAS
// subcommands (its usage spells `buddy <verb> <word>`), so a slug in an
// example is not mistaken for one.
func checkNamedVerbs(text string, usageOf func(string) (string, bool)) (errs []string, checked int) {
	return checkNamedVerbsInto(text, usageOf, nil)
}

// checkNamedVerbsInto is checkNamedVerbs recording, when taught is non-nil,
// every verb, subcommand and flag the text names and the usage table answers:
// taught[verb][""] for the verb, taught[verb][name] for the rest. The
// coverage gate reads it in the other direction (usage table → text).
func checkNamedVerbsInto(text string, usageOf func(string) (string, bool), taught map[string]map[string]bool) (errs []string, checked int) {
	mark := func(verb, name string) {
		if taught == nil {
			return
		}
		if taught[verb] == nil {
			taught[verb] = map[string]bool{}
		}
		taught[verb][name] = true
	}
	last := ""
	for _, m := range backtickSpan.FindAllStringSubmatch(text, -1) {
		span := quotedSpan.ReplaceAllString(m[1], "")
		span = strings.NewReplacer("[", " ", "]", " ").Replace(span)
		var toks []string
		switch i := strings.Index(span, "buddy "); {
		case i == 0 || (i > 0 && span[i-1] == ' '):
			toks = strings.Fields(span[i+len("buddy "):])
			if len(toks) == 0 || strings.HasPrefix(toks[0], "-") || strings.HasPrefix(toks[0], "<") {
				continue // `buddy --help`, `buddy <verb> --help`
			}
			last = toks[0]
			if _, ok := usageOf(last); !ok {
				errs = append(errs, fmt.Sprintf("`%s`: no verb %q", m[1], last))
				last = ""
				continue
			}
			checked++
			mark(last, "")
			toks = toks[1:]
		case strings.HasPrefix(span, "--"):
			if last == "" {
				errs = append(errs, fmt.Sprintf("`%s`: a flag with no verb named before it", m[1]))
				continue
			}
			toks = strings.Fields(span)
		default:
			continue
		}
		usage, _ := usageOf(last)
		// A word straight after a verb that has NO subcommands is read as a
		// value (a slug), so deleting every subcommand from a verb's usage
		// would pass prose that still names one. Accepted: that is a rewrite
		// of the verb, not a rename, and the verb's own tests see it.
		subcommands := regexp.MustCompile(`buddy ` + regexp.QuoteMeta(last) + ` [a-z]`).MatchString(usage)
		for j, tok := range toks {
			for _, name := range strings.Split(tok, "|") {
				switch {
				case strings.HasPrefix(name, "--"):
					if name == "--help" {
						continue
					}
					// Bounded on BOTH sides by a non-name byte: \b sits
					// inside "--dry-run", so --dry matched it, and with no
					// left bound --shared matched a renamed --co--shared.
					if !regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(name) + `($|[^\w-])`).MatchString(usage) {
						errs = append(errs, fmt.Sprintf("`%s`: buddy %s --help does not name %s", m[1], last, name))
					} else {
						mark(last, name)
					}
					checked++
				case j == 0 && subcommands && regexp.MustCompile(`^[A-Za-z]+$`).MatchString(name):
					// Whole word: `ids see` is a prefix of `ids seed`.
					if !regexp.MustCompile(`buddy ` + regexp.QuoteMeta(last) + ` ` + regexp.QuoteMeta(name) + `(\s|$)`).MatchString(usage) {
						errs = append(errs, fmt.Sprintf("`%s`: buddy %s has no subcommand %q", m[1], last, name))
					} else {
						mark(last, name)
					}
					checked++
				}
			}
		}
	}
	return errs, checked
}

// helpUsage is what `buddy <verb> --help` prints: the dispatch table's line.
func helpUsage(v string) (string, bool) {
	vb, ok := verbs[v]
	return vb.usage, ok
}

func TestHelloPointsAtHelp(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if out := helloB(t, f); !strings.Contains(out, helloHelpLine) {
		t.Fatalf("the digest does not point at buddy --help:\n%s", out)
	}
}

func TestHelloHelpLineNamesOnlyWhatHelpAnswers(t *testing.T) {
	errs, checked := checkNamedVerbs(helloHelpLine, helpUsage)
	for _, e := range errs {
		t.Error(e)
	}
	// Nine names are in the line today; a checker that read none of them
	// would pass every rename.
	if checked < 9 {
		t.Fatalf("checked only %d names in the help line; the parser no longer reads it", checked)
	}
}

// The line names wait's TIMER form, not only `--on` (D-054, issue #44): the
// parks that cost most — a session on its own hour-plus run, a lane waiting
// for its orchestrator — have no claim to name, and nothing else a session
// reads at start tells it a wait needs none. A count floor cannot see one
// name dropped from the line, so this asks for the name itself.
func TestHelloHelpLineTeachesTheTimerWait(t *testing.T) {
	taught := map[string]map[string]bool{}
	if errs, _ := checkNamedVerbsInto(helloHelpLine, helpUsage, taught); len(errs) > 0 {
		t.Fatalf("the help line names what help does not answer: %v", errs)
	}
	for _, name := range []string{"--on", "--until", "check"} {
		if !taught["wait"][name] {
			t.Errorf("the help line does not teach `buddy wait %s`:\n%s", name, helloHelpLine)
		}
	}
}

// skillFiles are the shipped skills (D-114, D-121): the core every session
// loads, the fleet skill only a coordinator loads, and the lead skill a
// first-time user's helper loads, the three SKILL.md first in that order; then
// the fleet skill's reference files (D-171), the read-once sections its
// SKILL.md names and says when to read. A name any file teaches must exist,
// and every verb and flag must be taught by at least one of them: a verb
// taught only in a reference file is taught, and a stale name there is caught.
// The list is written out, and TestSkillFilesAreEveryShippedFile holds it to
// what skills/ ships, so a file added there or dropped from here fails. Last
// come the files of a skill that names no buddy verb (noVerbSkills).
var skillFiles = []string{
	"../../skills/buddy/SKILL.md", "../../skills/buddy-fleet/SKILL.md", "../../skills/buddy-lead/SKILL.md",
	"../../skills/buddy-fleet/codex-lanes.md",
	"../../skills/buddy-fleet/fleet-of-fleets.md",
	"../../skills/buddy-fleet/handoff.md",
	"../../skills/buddy-fleet/model-pick.md",
	"../../skills/buddy-fleet/not-code.md",
	"../../skills/buddy-fleet/restack.md",
	"../../skills/model-router/SKILL.md",
	"../../skills/model-router/reference.md",
	"../../skills/model-router/router.py",
}

// noVerbSkills are the shipped skills that work with no buddy installed at
// all (D-177): the model router is dropped into a machine that may never have
// run buddy, so it must teach no buddy verb. Its files are held to the
// checker's OTHER half, not exempted from it: no error (a misspelt verb, a
// bare backticked flag, which the checker already reports as "a flag with no
// verb named before it") AND exactly zero names checked. The floor the buddy
// skills keep cannot apply to them, and dropping it alone would be a wholesale
// exemption by another name (the Codex design pass's Q6), so the positive
// control is a wrong name planted in each such file, which the same checker
// must report, and a right one, which the zero-name rule must refuse.
var noVerbSkills = []string{"../../skills/model-router/"}

func isNoVerbSkill(f string) bool {
	for _, d := range noVerbSkills {
		if strings.HasPrefix(f, d) {
			return true
		}
	}
	return false
}

// shippedSkillFiles is every entry install-skill.sh's files() takes up under
// <skills>/<name>/, in the installer's own terms: its glob skips a dotfile (a
// Finder .DS_Store; Codex code pass), and its ONE named exception (D-178)
// skips a real DIRECTORY named exactly __pycache__, which Python writes beside
// router.py whenever something imports it. Counting that one was a red on a
// correct checkout that read as a missing skill file (#93). Everything else
// is returned, a symlink named __pycache__, an ordinary subdirectory and a
// regular FILE named __pycache__ included: the installer installs or refuses
// each of those, so none may slip past the comparison below. The test is
// Lstat, as the installer's `[ -d ] && [ ! -L ]` is: a Stat would follow a
// symlink to a directory and skip what the installer refuses.
func shippedSkillFiles(skills string) ([]string, error) {
	all, err := filepath.Glob(filepath.Join(skills, "*", "*"))
	if err != nil {
		return nil, err
	}
	var shipped []string
	for _, f := range all {
		b := filepath.Base(f)
		// Go's Glob matches a leading dot and sh's does not, at either
		// level: the installer's skills/*/SKILL.md never takes up a
		// .scratch/ skill directory (Codex code pass, #93).
		if strings.HasPrefix(b, ".") || strings.HasPrefix(filepath.Base(filepath.Dir(f)), ".") {
			continue
		}
		if b == "__pycache__" {
			fi, err := os.Lstat(f)
			if err != nil {
				return nil, err
			}
			if fi.IsDir() {
				continue
			}
		}
		shipped = append(shipped, f)
	}
	return shipped, nil
}

// skillFilesMismatch is one error per shipped entry listed names no test
// reads, and per listed name nothing ships.
func skillFilesMismatch(shipped, listed []string) []string {
	var errs []string
	for _, f := range shipped {
		if !slices.Contains(listed, f) {
			errs = append(errs, fmt.Sprintf("%s ships with its skill and is not in skillFiles: the skill tests do not read it", f))
		}
	}
	for _, f := range listed {
		if !slices.Contains(shipped, f) {
			errs = append(errs, fmt.Sprintf("skillFiles names %s, which skills/ does not ship", f))
		}
	}
	return errs
}

// Every file under skills/<name>/ is installed with its skill
// (install-skill.sh's files()), so every one is a file a session may read and
// the skill tests must read it too.
func TestSkillFilesAreEveryShippedFile(t *testing.T) {
	shipped, err := shippedSkillFiles("../../skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range skillFilesMismatch(shipped, skillFiles) {
		t.Error(e)
	}
	if len(shipped) < 9 {
		t.Fatalf("the glob found %d files under skills/; it is aimed at the wrong root", len(shipped))
	}
}

// The installer's exception and nothing wider (#93, D-178), on a skills/ tree
// this test makes, never the checkout's. A real __pycache__ directory (with a
// .pyc in it, as Python leaves it), a dotfile and a dot-named skill directory
// are skipped; the clean tree is the positive control that the comparison
// passes at all. A directory named __PYCACHE__ (the installer's test is
// case-exact; an EqualFold would skip it), a symlink named __pycache__,
// dangling or to a directory (a Stat would call that one a directory), an
// ordinary subdirectory and a regular file named __pycache__ must each be
// reported.
func TestShippedSkillFilesSkipsOnlyARealPycache(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, dir string) string // the planted entry's path
		fails bool
	}{
		{"clean tree", func(t *testing.T, dir string) string { return "" }, false},
		{"real __pycache__ directory", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "__pycache__")
			mustMkdir(t, p)
			mustWrite(t, filepath.Join(p, "router.cpython-313.pyc"), "bytecode")
			return p
		}, false},
		{"dotfile", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, ".DS_Store")
			mustWrite(t, p, "finder")
			return p
		}, false},
		{"dot-named skill directory", func(t *testing.T, dir string) string {
			p := filepath.Join(filepath.Dir(dir), ".scratch", "SKILL.md")
			mustWrite(t, p, "x")
			return p
		}, false},
		{"real directory named __PYCACHE__", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "__PYCACHE__")
			mustMkdir(t, p)
			return p
		}, true},
		{"dangling symlink named __pycache__", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "__pycache__")
			if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), p); err != nil {
				t.Fatal(err)
			}
			return p
		}, true},
		{"symlink named __pycache__ to a directory", func(t *testing.T, dir string) string {
			target := filepath.Join(t.TempDir(), "elsewhere")
			mustMkdir(t, target)
			p := filepath.Join(dir, "__pycache__")
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			return p
		}, true},
		{"ordinary subdirectory", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "cache")
			mustMkdir(t, p)
			return p
		}, true},
		{"regular file named __pycache__", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "__pycache__")
			mustWrite(t, p, "not a directory")
			return p
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skills := t.TempDir()
			dir := filepath.Join(skills, "model-router")
			mustMkdir(t, dir)
			listed := []string{filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "router.py")}
			for _, f := range listed {
				mustWrite(t, f, "x")
			}
			planted := tc.plant(t, dir)
			shipped, err := shippedSkillFiles(skills)
			if err != nil {
				t.Fatal(err)
			}
			errs := skillFilesMismatch(shipped, listed)
			if !tc.fails {
				if len(errs) != 0 {
					t.Errorf("want no mismatch, got %q", errs)
				}
				return
			}
			want := planted + " ships with its skill and is not in skillFiles: the skill tests do not read it"
			if len(errs) != 1 || errs[0] != want {
				t.Errorf("want exactly %q, got %q", want, errs)
			}
		})
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// readSkills returns each skill's text, in skillFiles' order. Each is parsed
// on its own: a flags-only span belongs to the verb most recently named, and
// across a file boundary that verb would be the other file's.
func readSkills(t *testing.T) []string {
	t.Helper()
	var bodies []string
	for _, f := range skillFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(b))
	}
	return bodies
}

// fleetSkill is the fleet skill whole: its SKILL.md, then each reference
// file beside it (D-171), for a test that asks whether the fleet skill still
// teaches a rule wherever in it the rule now lives.
func fleetSkill(bodies []string) string {
	all := bodies[1]
	for i, f := range skillFiles {
		if i >= 3 && strings.HasPrefix(f, "../../skills/buddy-fleet/") {
			all += "\n" + bodies[i]
		}
	}
	return all
}

func TestSkillNamesOnlyWhatHelpAnswers(t *testing.T) {
	noVerb := 0
	for i, body := range readSkills(t) {
		errs, checked := checkNamedVerbs(body, helpUsage)
		for _, e := range errs {
			t.Errorf("%s: %s", skillFiles[i], e)
		}
		if isNoVerbSkill(skillFiles[i]) {
			noVerb++
			if checked != 0 {
				t.Errorf("%s names %d buddy name(s); a skill that ships to machines without buddy names none (D-177)", skillFiles[i], checked)
			}
			// The controls, each on this file's own text: a wrong verb and a
			// bare flag are reported, so the file is read under the rule and
			// "0 checked" is not "never parsed"; a right verb is counted, so
			// the zero-name rule above is armed.
			for _, plant := range []string{"`buddy no-such-verb`", "`--no-such-flag`"} {
				if errs, _ := checkNamedVerbs(body+"\n"+plant+"\n", helpUsage); len(errs) == 0 {
					t.Errorf("with %s planted in %s, the checker reported nothing: the file is not read under the rule", plant, skillFiles[i])
				}
			}
			if _, n := checkNamedVerbs(body+"\n`buddy claim`\n", helpUsage); n != 1 {
				t.Errorf("with `buddy claim` planted in %s, the checker counted %d names, want 1: the zero-name rule is not armed", skillFiles[i], n)
			}
			continue
		}
		// A SKILL.md names dozens; a reference file is one section, and the
		// smallest (codex-lanes.md) names 13.
		floor := 20
		if i >= 3 {
			floor = 5
		}
		if checked < floor {
			t.Fatalf("checked only %d names in %s; the parser no longer reads it", checked, skillFiles[i])
		}
	}
	if noVerb < 3 {
		t.Errorf("held %d file(s) to the no-verb rule, want the model router's three: noVerbSkills is aimed at the wrong directory", noVerb)
	}
}

// The split holds only if each skill loads for its own reader: the core
// points a coordinator at the fleet skill by its name, the lead skill points
// at both (it routes, it does not restate: D-121), and each frontmatter name
// is the directory install-skill.sh installs it as. A renamed directory or
// frontmatter would leave the pointer naming nothing.
func TestSkillSplitPointsAtTheFleetSkill(t *testing.T) {
	bodies := readSkills(t)
	if len(bodies) < 3 {
		t.Fatalf("skillFiles lists %d files; this test knows the three skills that ship (core, fleet, lead) first", len(bodies))
	}
	for i, want := range []string{"name: buddy\n", "name: buddy-fleet\n", "name: buddy-lead\n"} {
		if !strings.HasPrefix(bodies[i], "---\n"+want) {
			t.Errorf("%s does not open with frontmatter %q", skillFiles[i], want)
		}
	}
	if !strings.Contains(bodies[0], "`buddy-fleet` skill") {
		t.Errorf("the core skill no longer points a coordinator at the `buddy-fleet` skill")
	}
	if !strings.Contains(bodies[1], "Not for a lane") {
		t.Errorf("the fleet skill's description no longer tells a lane it is not for it")
	}
	for _, want := range []string{"`buddy` skill", "`buddy-fleet`", "Not for a session already working an item", "scripts/env-check.sh"} {
		if !strings.Contains(bodies[2], want) {
			t.Errorf("the lead skill no longer carries %q: it routes to the other skills and the read-only check, it restates neither", want)
		}
	}
	// The description is a YAML plain scalar: ": " or " #" inside one is a
	// parse error or a comment (Codex code pass: the fleet skill's first
	// description held "one item: a lane"), and a loader that rejects the
	// frontmatter loses the one line that decides who loads the skill.
	// Each fleet reference file is named in the fleet SKILL.md with when to
	// read it ("read `restack.md` beside this file"), and every file such a
	// pointer names exists: a renamed file would leave the pointer naming
	// nothing, and a file nothing names is never opened (D-171).
	pointer := regexp.MustCompile("`([A-Za-z0-9._-]+\\.md)` beside this file")
	named := map[string]bool{}
	for _, m := range pointer.FindAllStringSubmatch(bodies[1], -1) {
		named[m[1]] = true
		if _, err := os.Stat("../../skills/buddy-fleet/" + m[1]); err != nil {
			t.Errorf("the fleet skill points at %s beside it, which does not ship: %v", m[1], err)
		}
	}
	for _, f := range skillFiles[3:] {
		if dir, base := filepath.Split(f); dir == "../../skills/buddy-fleet/" && !named[base] {
			t.Errorf("%s ships beside the fleet skill and its SKILL.md has no \"`%s` beside this file\" pointer saying when to read it", f, base)
		}
	}
	if len(named) < 6 {
		t.Errorf("the fleet skill names %d reference files beside it, want the six it moved out (D-171)", len(named))
	}
	// Every SKILL.md, not only the first three: a fourth skill's frontmatter
	// is read by the same loader (D-177). Its name is its directory, which is
	// what install-skill.sh installs it as.
	skills := 0
	for i, body := range bodies {
		dir, base := filepath.Split(skillFiles[i])
		if base != "SKILL.md" {
			continue
		}
		skills++
		if want := "---\nname: " + filepath.Base(dir) + "\n"; !strings.HasPrefix(body, want) {
			t.Errorf("%s does not open with frontmatter %q, the directory it is installed as", skillFiles[i], want)
		}
		for _, line := range strings.Split(body, "\n")[1:4] {
			v, ok := strings.CutPrefix(line, "description: ")
			if ok && (strings.Contains(v, ": ") || strings.Contains(v, " #") || strings.ContainsAny(v[:1], "'\"&*!|>%@`[]{}")) {
				t.Errorf("%s: the description is not a valid YAML plain scalar (\": \", \" #\" or a leading indicator): %.80s", skillFiles[i], v)
			}
		}
	}
	if skills < 4 {
		t.Errorf("read %d SKILL.md frontmatters, want the four that ship", skills)
	}
}

// The positive control: the checker refuses each shape of drift it exists
// for, so "no errors" above means the names are right and not that nothing
// was compared.
func TestCheckNamedVerbsRefusesDrift(t *testing.T) {
	for _, bad := range []string{
		"`buddy claim --bogus`",              // a flag the verb lacks
		"`buddy frobnicate`",                 // a verb that does not exist
		"`buddy msg --lead|--guess`",         // one arm of an alternation
		"`buddy wait --on x` then `--force`", // a trailing flag, read against wait
		"`buddy ids give <space> 3`",         // a subcommand that does not exist
		"`buddy claim --dry`",                // a prefix of a real flag: \b sits inside "--dry-run"
		"`--shared` with no verb before it",
		"`buddy ids see <space> 1`", // a prefix of the real subcommand `seed`
		"`buddy ids TAKE <space> 1`",
	} {
		if errs, _ := checkNamedVerbs(bad, helpUsage); len(errs) == 0 {
			t.Errorf("%s: accepted", bad)
		}
	}
	// The usage side renamed, the prose left alone: the checker must notice.
	renamed := func(v string) (string, bool) {
		u, ok := helpUsage(v)
		return strings.ReplaceAll(u, "--shared", "--co--shared"), ok
	}
	if errs, _ := checkNamedVerbs("`buddy claim --shared`", renamed); len(errs) == 0 {
		t.Error("a flag renamed in the usage table (--shared → --co--shared) was accepted")
	}
	for _, good := range []string{
		"`buddy claim orchestrator --desc \"<n>\" --scope x`", // a slug is not a subcommand
		"`/loop buddy wait check`",
		"`buddy --help`",
	} {
		if errs, _ := checkNamedVerbs(good, helpUsage); len(errs) != 0 {
			t.Errorf("%s: refused: %v", good, errs)
		}
	}
}

// THE OTHER DIRECTION, a standing gate: every verb, subcommand and flag in
// the usage table is TAUGHT by the skill, or is on the list below with the
// reason it is not. The check above catches a flag the skill names and the
// table lost; nothing caught a feature the table gained and the skill never
// mentioned, and the skill is where a session in another repo learns how the
// verbs fit (D-046). A new verb or flag fails here until it is taught or
// exempted on purpose — the decision is forced, never forgotten.
//
// skillExempt[verb] = reason exempts a whole verb; skillExempt["verb --flag"]
// or ["verb sub"] one name. Every entry needs a reason a reader can check.
var skillExempt = map[string]string{
	"init":        "setup, run once per checkout by the operator (README, setup-clone.sh)",
	"hello":       "a hook; the session never types it (README's hook wiring)",
	"bye":         "a hook; the session never types it (README's hook wiring)",
	"beat":        "a hook; the session never types it (README's hook wiring)",
	"idle":        "a hook; the session never types it (README's hook wiring)",
	"busy":        "a hook; the session never types it (README's hook wiring)",
	"gate":        "a hook; the session never types it (README's hook wiring)",
	"commit-gate": "the git pre-commit hook runs it (setup-clone.sh)",
	"pause":       "the operator's brake; a session is told it is paused, it never pauses a peer",
	"resume":      "the operator's brake; a session is told it is paused, it never pauses a peer",
	"authority":   "the operator curates the watched files; a session only sees the notice, which the skill explains",
	// Adapter-implementer surface (review F-43, 2026-10-06): programs and the
	// Codex lifecycle hook wiring type these, not a lane. Teaching every flag
	// here loaded ~10 KB into every lane's skill; docs/USAGE.md documents them.
	"lifecycle": "adapter/hook surface, typed by programs and hook wiring; docs/USAGE.md \"Structured adapters\" teaches it",
	"adapter":   "adapter surface, typed by buddy-adapter and coordinators that run it; docs/USAGE.md \"Structured adapters\" teaches it",
}

// skillExemptFlag exempts a flag on EVERY verb that has it.
var skillExemptFlag = map[string]string{
	"--help":    "every verb answers it, and the skill says so once",
	"--session": "the harness names the caller ($CLAUDE_CODE_SESSION_ID); a session never needs to",
}

var (
	usageFlag = regexp.MustCompile(`(^|[^\w-])(--[a-z][a-z-]*)`)
)

// untaught lists what the usage table offers and the taught map lacks.
func untaught(taught map[string]map[string]bool) []string {
	var missing []string
	for name, vb := range verbs {
		if _, ok := skillExempt[name]; ok {
			continue
		}
		if !taught[name][""] {
			missing = append(missing, "buddy "+name)
			continue
		}
		seen := map[string]bool{}
		for _, m := range usageFlag.FindAllStringSubmatch(vb.usage, -1) {
			f := m[2]
			if seen[f] {
				continue
			}
			seen[f] = true
			if _, ok := skillExemptFlag[f]; ok {
				continue
			}
			if _, ok := skillExempt[name+" "+f]; ok {
				continue
			}
			if !taught[name][f] {
				missing = append(missing, "buddy "+name+" "+f)
			}
		}
		for _, m := range regexp.MustCompile(`buddy `+regexp.QuoteMeta(name)+` ([a-z]+)`).FindAllStringSubmatch(vb.usage, -1) {
			sub := m[1]
			if seen[sub] {
				continue
			}
			seen[sub] = true
			if _, ok := skillExempt[name+" "+sub]; ok {
				continue
			}
			if !taught[name][sub] {
				missing = append(missing, "buddy "+name+" "+sub)
			}
		}
	}
	return missing
}

func TestSkillTeachesEveryVerbAndFlag(t *testing.T) {
	taught := map[string]map[string]bool{}
	for _, body := range readSkills(t) {
		checkNamedVerbsInto(body, helpUsage, taught)
	}
	for _, m := range untaught(taught) {
		t.Errorf("%s is in the usage table and no skill file teaches it: add it to skills/buddy/SKILL.md "+
			"(what a lane does) or skills/buddy-fleet/SKILL.md or a reference file beside it (what a coordinator does) in a backticked "+
			"`buddy <verb> --flag` (then re-run scripts/install-skill.sh), or exempt it in skillExempt WITH a reason", m)
	}
	// The exemptions must name things that exist, or a renamed verb would
	// sit exempt forever while its new name went untaught.
	for k := range skillExempt {
		v, name, _ := strings.Cut(k, " ")
		u, ok := helpUsage(v)
		if !ok || (name != "" && !strings.Contains(u, name)) {
			t.Errorf("skillExempt[%q] names nothing in the usage table", k)
		}
	}
}

// The positive control: the gate fires on a flag the skill drops, a verb the
// skill drops, and a subcommand the skill drops — each cut from the REAL skill,
// so a parser that read nothing would fail here rather than pass above.
func TestSkillCoverageGateFires(t *testing.T) {
	bodies := readSkills(t)
	for _, cut := range []struct{ from, want string }{
		{"--ready", "buddy wait --ready"},
		{"--outcome", "buddy release --outcome"},
		{"buddy sent", "buddy sent"},
		{"wait check", "buddy wait check"},
		{"--codex-hooks", "buddy diagnose --codex-hooks"}, // taught by the fleet skill alone
	} {
		taught := map[string]map[string]bool{}
		for _, body := range bodies {
			checkNamedVerbsInto(strings.ReplaceAll(body, cut.from, "REMOVED"), helpUsage, taught)
		}
		if !slices.Contains(untaught(taught), cut.want) {
			t.Errorf("with every %q cut from both skills, the gate did not report %q", cut.from, cut.want)
		}
		// The control: cut from ONE skill only, the name is reported exactly
		// when no other skill names it, so the union is what is read.
		for i := range bodies {
			taught := map[string]map[string]bool{}
			others := false
			for j, body := range bodies {
				if j == i {
					body = strings.ReplaceAll(body, cut.from, "REMOVED")
				} else if strings.Contains(body, cut.from) {
					others = true
				}
				checkNamedVerbsInto(body, helpUsage, taught)
			}
			if slices.Contains(untaught(taught), cut.want) != !others {
				t.Errorf("with %q cut from %s only, the gate's report of %q does not follow whether another skill still names it",
					cut.from, skillFiles[i], cut.want)
			}
		}
	}
}
