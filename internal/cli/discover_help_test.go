package cli

import (
	"fmt"
	"os"
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

func TestSkillNamesOnlyWhatHelpAnswers(t *testing.T) {
	body, err := os.ReadFile("../../skills/buddy/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	errs, checked := checkNamedVerbs(string(body), helpUsage)
	for _, e := range errs {
		t.Error(e)
	}
	if checked < 20 {
		t.Fatalf("checked only %d names in the skill; the parser no longer reads it", checked)
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
	body, err := os.ReadFile("../../skills/buddy/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	taught := map[string]map[string]bool{}
	checkNamedVerbsInto(string(body), helpUsage, taught)
	for _, m := range untaught(taught) {
		t.Errorf("%s is in the usage table and the skill never teaches it: add it to skills/buddy/SKILL.md "+
			"in a backticked `buddy <verb> --flag` (then recopy it to ~/.claude/skills/buddy/), or exempt it in skillExempt WITH a reason", m)
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
	body, err := os.ReadFile("../../skills/buddy/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []struct{ from, want string }{
		{"--ready", "buddy wait --ready"},
		{"--outcome", "buddy release --outcome"},
		{"buddy sent", "buddy sent"},
		{"wait check", "buddy wait check"},
	} {
		text := strings.ReplaceAll(string(body), cut.from, "REMOVED")
		taught := map[string]map[string]bool{}
		checkNamedVerbsInto(text, helpUsage, taught)
		if !slices.Contains(untaught(taught), cut.want) {
			t.Errorf("with every %q cut from the skill, the gate did not report %q", cut.from, cut.want)
		}
	}
}
