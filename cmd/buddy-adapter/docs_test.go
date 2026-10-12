package main

// Every flag buddy-adapter defines is documented in USAGE's "Structured
// adapters" section (review F-45, 2026-10-06). The skill gate
// (TestSkillTeachesEveryVerbAndFlag) covers only `buddy`'s usage table, so a
// renamed or added adapter flag went undocumented with nothing to say so.
//
// The flag list is read from the FlagSet calls in this package's own source,
// not from a second hand-kept list: two lists maintained by hand agree only
// until one of them is edited.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// definedFlags returns every flag name passed as the first argument to a
// FlagSet definition (String, Bool, Int, Duration, Var, ...) in files.
func definedFlags(t *testing.T, files ...string) []string {
	t.Helper()
	var names []string
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "String", "Bool", "Int", "Int64", "Uint", "Duration", "Float64", "Var",
				"StringVar", "BoolVar", "IntVar", "DurationVar":
			default:
				return true
			}
			// fs.String("name", ...) names its flag first; fs.Var and the
			// *Var forms take the destination first and the name second.
			arg := call.Args[0]
			if strings.HasSuffix(sel.Sel.Name, "Var") {
				arg = call.Args[1]
			}
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if name, err := strconv.Unquote(lit.Value); err == nil && !slices.Contains(names, name) {
				names = append(names, name)
			}
			return true
		})
	}
	return names
}

func adapterSection(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "USAGE.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i := strings.Index(doc, "\n## Structured adapters (experimental)\n")
	if i < 0 {
		t.Fatal(`USAGE.md has no "## Structured adapters (experimental)" section`)
	}
	rest := doc[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		rest = rest[:j+3]
	}
	return rest
}

func undocumented(flags []string, section string) []string {
	var missing []string
	for _, f := range flags {
		if !strings.Contains(section, "--"+f) {
			missing = append(missing, "--"+f)
		}
	}
	return missing
}

func TestUsageDocumentsEveryAdapterFlag(t *testing.T) {
	flags := definedFlags(t, "main.go", "hook.go")
	if len(flags) < 8 {
		// The positive control for the parser: the adapter defines at least
		// --harness --executable --buddy --launch --receipt --label --resume
		// --operator-fd. Fewer means the walk stopped seeing definitions.
		t.Fatalf("found only %d flag definitions (%v); the source walk is broken", len(flags), flags)
	}
	section := adapterSection(t)
	for _, m := range undocumented(flags, section) {
		t.Errorf("buddy-adapter defines %s and docs/USAGE.md \"Structured adapters\" never mentions it", m)
	}
	// The gate fires: the same check over the section with one flag's every
	// mention cut must report exactly that flag.
	cut := strings.ReplaceAll(section, "--operator-fd", "REMOVED")
	if got := undocumented(flags, cut); !slices.Contains(got, "--operator-fd") {
		t.Fatalf("with every --operator-fd cut, the gate reported %v", got)
	}
}
