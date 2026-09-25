package cli

import (
	"fmt"
	"os"
	"regexp"
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
					}
					checked++
				case j == 0 && subcommands && regexp.MustCompile(`^[A-Za-z]+$`).MatchString(name):
					// Whole word: `ids see` is a prefix of `ids seed`.
					if !regexp.MustCompile(`buddy ` + regexp.QuoteMeta(last) + ` ` + regexp.QuoteMeta(name) + `(\s|$)`).MatchString(usage) {
						errs = append(errs, fmt.Sprintf("`%s`: buddy %s has no subcommand %q", m[1], last, name))
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
