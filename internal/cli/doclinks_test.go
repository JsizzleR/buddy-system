package cli

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// The docs cross-reference each other by relative path and by heading anchor
// (`decisions.md#d-072--...`, `USAGE.md#messages--buddy-msg`), and a heading
// renamed in one file silently strands every anchor aimed at it: GitHub
// renders a dead fragment as the top of the page, so nothing looks broken.
// This test reads the documents an agent is told to read and checks that
// every relative link names a file that exists, spelled with its exact case,
// and every anchor into a Markdown file names a heading GitHub would slug.
//
// What it deliberately does NOT do: fetch external URLs (a hermetic tier has
// no network), check anchors into non-Markdown targets (`claude.go#L10` is a
// line, not a heading), parse setext headings (the corpus has none; a link to
// one fails loud here rather than passing silently), or resolve
// `[text][label]` references against their definitions — definitions' own
// destinations are checked like inline links.

// docLinkFiles are the documents checked, relative to the repository root.
// docs/*.md is a glob so a new document is covered the day it lands.
func docLinkFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{"README.md", "CLAUDE.md", "AGENTS.md", "skills/buddy/SKILL.md"}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatalf("no docs/*.md under %s: the glob is aimed at the wrong root", root)
	}
	for _, d := range docs {
		files = append(files, "docs/"+filepath.Base(d))
	}
	return files
}

type docLinkReport struct {
	links    int // relative links whose target was checked
	anchors  int // fragments checked against a Markdown file's headings
	problems []string
}

var (
	// An inline link or image: [text](dest) or [text](dest "title"). The text
	// may not hold a ']' — a nested bracket is missed, never misread.
	inlineLinkRE = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^\s)]+)(?:\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\s*\)`)
	// A link reference definition: [label]: dest
	refDefRE = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)
	// An ATX heading; the optional closing run of '#' is not part of the text.
	atxHeadingRE = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	schemeRE     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	// Heading text that renders as something else: a link or image keeps its
	// text, an HTML tag renders as nothing.
	headingLinkRE = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTagRE     = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	htmlAnchorRE  = regexp.MustCompile(`<a\s[^>]*(?:name|id)\s*=\s*"([^"]+)"`)
)

// githubSlug is github-slugger's rule, which is what GitHub uses for heading
// ids: lower-case, drop every character that is not a letter, number, mark,
// '-', '_' or space, then turn each space into '-'. Nothing is collapsed, so
// "Messages — `buddy msg`" is "messages--buddy-msg": the em dash goes, both
// spaces around it stay. It slugs the RENDERED text: a link keeps its text and
// an HTML tag vanishes, but only outside a code span. `.buddy/slot/<name>` is
// literal there, and stripping it as a tag lost "name" from a real anchor.
func githubSlug(heading string) string {
	var spans []string
	text := mapCodeSpans(heading, func(code string) string {
		spans = append(spans, code)
		return string(rune(0xE000 + len(spans) - 1)) // private use: never in a doc
	})
	text = headingLinkRE.ReplaceAllString(text, "$1")
	text = htmlTagRE.ReplaceAllString(text, "")
	for i, code := range spans {
		text = strings.Replace(text, string(rune(0xE000+i)), code, 1)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fenceMarker returns the fence a line opens or closes (``` or ~~~, at its
// full length), or "".
func fenceMarker(line string) string {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return ""
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return s[:n]
}

// docLines yields each line outside a fenced code block, with code spans
// removed: `[a](b)` in backticks is an example, not a link.
func docLines(body string) []string {
	var out []string
	open := ""
	for _, line := range strings.Split(body, "\n") {
		if m := fenceMarker(line); m != "" {
			switch {
			case open == "":
				open = m
				out = append(out, "")
				continue
			case m[0] == open[0] && len(m) >= len(open) && strings.TrimSpace(line) == m:
				open = ""
				out = append(out, "")
				continue
			}
		}
		if open != "" {
			out = append(out, "")
			continue
		}
		out = append(out, mapCodeSpans(line, func(string) string { return "" }))
	}
	return out
}

// mapCodeSpans replaces each code span (a run of n backticks through the next
// run of exactly n) with code(its content). An unmatched run is literal text,
// as CommonMark has it.
func mapCodeSpans(line string, code func(string) string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		end := -1
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			k := 0
			for j+k < len(line) && line[j+k] == '`' {
				k++
			}
			if k == n {
				end = j + k
				break
			}
			j += k
		}
		if end < 0 {
			b.WriteString(line[i : i+n])
			i += n
			continue
		}
		b.WriteString(code(line[i+n : end-n]))
		i = end
	}
	return b.String()
}

// docAnchors is every fragment a Markdown file answers to: each heading's
// slug, de-duplicated the way github-slugger does it (the second "Usage" is
// "usage-1"), plus explicit <a name/id> anchors. Headings are read from the
// RAW line, code spans included: `apply_patch` in a heading keeps its '_'.
func docAnchors(body string) map[string]bool {
	anchors := map[string]bool{}
	taken := map[string]bool{} // slugs only: an <a name> does not bump a heading's
	seen := map[string]int{}
	open := ""
	for _, line := range strings.Split(body, "\n") {
		if m := fenceMarker(line); m != "" {
			if open == "" {
				open = m
				continue
			}
			if m[0] == open[0] && len(m) >= len(open) && strings.TrimSpace(line) == m {
				open = ""
				continue
			}
		}
		if open != "" {
			continue
		}
		for _, m := range htmlAnchorRE.FindAllStringSubmatch(line, -1) {
			anchors[m[1]] = true
		}
		m := atxHeadingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		base := githubSlug(m[1])
		slug := base
		for taken[slug] {
			seen[base]++
			slug = fmt.Sprintf("%s-%d", base, seen[base])
		}
		taken[slug] = true
		anchors[slug] = true
	}
	return anchors
}

// existsExact reports whether rel names an existing file or directory under
// root with EXACTLY that spelling. macOS's default volume is case-insensitive,
// so os.Stat would accept `readme.md` for README.md; GitHub would 404 it.
func existsExact(root, rel string) bool {
	dir := root
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		found := false
		for _, e := range entries {
			if e.Name() == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		dir = filepath.Join(dir, part)
	}
	return true
}

// checkDocLinks checks every relative link in files (repo-relative, '/'
// separated) against root. Problems name the file and line, so a failure
// points at the link to fix.
func checkDocLinks(root string, files []string) docLinkReport {
	var rep docLinkReport
	bodies := map[string]string{}
	read := func(rel string) (string, bool) {
		if b, ok := bodies[rel]; ok {
			return b, true
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", false
		}
		bodies[rel] = string(raw)
		return bodies[rel], true
	}
	anchorSets := map[string]map[string]bool{}

	for _, file := range files {
		body, ok := read(file)
		if !ok {
			rep.problems = append(rep.problems, fmt.Sprintf("%s: cannot read the document itself", file))
			continue
		}
		for i, line := range docLines(body) {
			var dests []string
			for _, m := range inlineLinkRE.FindAllStringSubmatch(line, -1) {
				dests = append(dests, m[1])
			}
			if m := refDefRE.FindStringSubmatch(line); m != nil {
				dests = append(dests, m[1])
			}
			for _, raw := range dests {
				dest := strings.TrimSuffix(strings.TrimPrefix(raw, "<"), ">")
				if schemeRE.MatchString(dest) || strings.HasPrefix(dest, "//") {
					continue // external: no network in a hermetic tier
				}
				where := fmt.Sprintf("%s:%d: link %q", file, i+1, raw)
				target, frag, _ := strings.Cut(dest, "#")
				targetPath, err := url.PathUnescape(target)
				if err != nil {
					rep.problems = append(rep.problems, fmt.Sprintf("%s: bad escape: %v", where, err))
					continue
				}
				resolved := file
				if targetPath != "" {
					if strings.HasPrefix(targetPath, "/") {
						resolved = path.Clean(strings.TrimPrefix(targetPath, "/"))
					} else {
						resolved = path.Clean(path.Join(path.Dir(file), targetPath))
					}
					if resolved == ".." || strings.HasPrefix(resolved, "../") {
						rep.problems = append(rep.problems, fmt.Sprintf("%s: leaves the repository", where))
						continue
					}
					rep.links++
					if !existsExact(root, resolved) {
						rep.problems = append(rep.problems, fmt.Sprintf("%s: %s does not exist (exact case)", where, resolved))
						continue
					}
				}
				if frag == "" || !strings.HasSuffix(strings.ToLower(resolved), ".md") {
					continue
				}
				fragment, err := url.PathUnescape(frag)
				if err != nil {
					rep.problems = append(rep.problems, fmt.Sprintf("%s: bad escape in anchor: %v", where, err))
					continue
				}
				set, ok := anchorSets[resolved]
				if !ok {
					tb, ok := read(resolved)
					if !ok {
						rep.problems = append(rep.problems, fmt.Sprintf("%s: cannot read %s for its anchors", where, resolved))
						continue
					}
					set = docAnchors(tb)
					anchorSets[resolved] = set
				}
				rep.anchors++
				if !set[fragment] {
					rep.problems = append(rep.problems, fmt.Sprintf("%s: %s has no heading with anchor #%s", where, resolved, fragment))
				}
			}
		}
	}
	return rep
}

// TestDocLinksResolve is the check on the real tree. The floor on links and
// anchors is the positive control: a scanner that matched nothing would
// report no problems too.
func TestDocLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	rep := checkDocLinks(root, docLinkFiles(t, root))
	for _, p := range rep.problems {
		t.Error(p)
	}
	if rep.links < 50 || rep.anchors < 10 {
		t.Errorf("checked %d links and %d anchors: fewer than the corpus holds, so the scanner is not matching", rep.links, rep.anchors)
	}
	t.Logf("checked %d relative links, %d anchors", rep.links, rep.anchors)
}

func TestGithubSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Messages — `buddy msg`", "messages--buddy-msg"},
		{"Structured adapters (experimental)", "structured-adapters-experimental"},
		{"D-072 — Offline diagnostics inspect a private ledger snapshot", "d-072--offline-diagnostics-inspect-a-private-ledger-snapshot"},
		{"F-01 An `apply_patch` sent", "f-01-an-apply_patch-sent"},
		{"6. Native qualification **[operator authorization]**", "6-native-qualification-operator-authorization"},
		{"See [the plan](PLAN.md) now", "see-the-plan-now"},
		{"Native rerun on the final bytes, 2026-10-13", "native-rerun-on-the-final-bytes-2026-10-13"},
		{"Café über", "café-über"},
		{"Resource slots — a claim on `.buddy/slot/<name>`", "resource-slots--a-claim-on-buddyslotname"},
		{"Tags <em>vanish</em> outside code", "tags-vanish-outside-code"},
	} {
		if got := githubSlug(c.in); got != c.want {
			t.Errorf("githubSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDocLinksFixture runs the checker over small trees, each with one shape
// of link. Every broken case has a passing twin in the same tree, so "it
// refused" cannot be "it never looked".
func TestDocLinksFixture(t *testing.T) {
	const target = "# Guide\n\n## Usage\n\n## Usage\n\n## Messages — `buddy msg`\n\n```\n## Not a heading\n```\n\n<a name=\"pinned\"></a>\n"
	for _, c := range []struct {
		name   string
		doc    string
		broken string // substring of the one expected problem; "" means clean
	}{
		{"good anchors", "[a](docs/GUIDE.md#usage) [b](docs/GUIDE.md#usage-1) [c](docs/GUIDE.md#messages--buddy-msg) [d](docs/GUIDE.md#pinned)\n", ""},
		{"broken anchor", "[a](docs/GUIDE.md#usage) [b](docs/GUIDE.md#usage-2)\n", "has no heading with anchor #usage-2"},
		{"anchor only inside a fence", "[a](docs/GUIDE.md#usage) [b](docs/GUIDE.md#not-a-heading)\n", "anchor #not-a-heading"},
		{"slug collapses dashes", "[a](docs/GUIDE.md#messages-buddy-msg)\n", "anchor #messages-buddy-msg"},
		{"same-file anchor", "# Top\n\n[a](#top) [b](#bottom)\n", "anchor #bottom"},
		{"missing file", "[a](docs/GUIDE.md) [b](docs/MISSING.md)\n", "docs/MISSING.md does not exist"},
		{"wrong case", "[a](docs/GUIDE.md) [b](docs/guide.md)\n", "docs/guide.md does not exist"},
		{"leaves the repo", "[a](docs/GUIDE.md) [b](../outside.md)\n", "leaves the repository"},
		{"code span and fence are not links", "[a](docs/GUIDE.md) `[b](nope.md)`\n\n~~~\n[c](nope.md)\n~~~\n", ""},
		{"external and non-md anchors skipped", "[a](https://example.com/x.md#nope) [b](main.go#L99) [c](mailto:x@y)\n", ""},
		{"reference definition", "[a][g]\n\n[g]: docs/GONE.md\n", "docs/GONE.md does not exist"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range map[string]string{
				"README.md":     c.doc,
				"docs/GUIDE.md": target,
				"main.go":       "package main\n",
			} {
				p := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			rep := checkDocLinks(root, []string{"README.md"})
			if c.broken == "" {
				if len(rep.problems) != 0 {
					t.Fatalf("want clean, got %q", rep.problems)
				}
				if rep.links+rep.anchors == 0 {
					t.Fatalf("nothing was checked: the case proves nothing")
				}
				return
			}
			if len(rep.problems) != 1 || !strings.Contains(rep.problems[0], c.broken) {
				t.Fatalf("want exactly one problem containing %q, got %q", c.broken, rep.problems)
			}
		})
	}
}
