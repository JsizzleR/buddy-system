package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func patchHookJSON(session, cwd, patch string) string {
	in := map[string]any{
		"session_id": session, "cwd": cwd, "tool_name": "apply_patch",
		"tool_input": map[string]any{"command": patch},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func patchText(body string) string {
	return "*** Begin Patch\n" + body + "*** End Patch\n"
}

func TestPatchPathsLocalGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, patch string
		want        []string
	}{
		{"all-operations", patchText("*** Add File: a\n+new\n*** Delete File: b\n*** Update File: c\n*** Move to: d\n@@\n-old\n+new\n"), []string{"a", "b", "c", "d"}},
		{"empty-add", patchText("*** Add File: a\n"), []string{"a"}},
		{"implicit-context", patchText("*** Update File: a\n context\n\n+new\n"), []string{"a"}},
		{"multiple-chunks", patchText("*** Update File: a\n@@ f\n context\n@@ g\n-new\n+old\n*** End of File\n\n"), []string{"a"}},
		{"header-whitespace", "  *** Begin Patch\n  *** Add File: name with spaces  \n+x\n*** Delete File:  leading-name  \n*** End Patch  \n", []string{"name with spaces", " leading-name"}},
		{"crlf", "*** Begin Patch\r\n*** Delete File: a\r\n*** End Patch\r\n", []string{"a"}},
		{"body-headers", patchText("*** Update File: a\n *** Delete File: fake\n-*** Update File: fake\n+*** Move to: fake\n"), []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := patchPaths(tc.patch)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths=%q err=%v; want %q", got, err, tc.want)
			}
		})
	}
	// Failure must return no prefix list: otherwise a caller could accidentally
	// adjudicate the early valid hunk and ignore the later unrecognized write.
	got, err := patchPaths(patchText("*** Delete File: okay\n*** Unknown File: unknown\n"))
	if err == nil || got != nil {
		t.Fatalf("malformed tail must invalidate all paths: paths=%q err=%v", got, err)
	}
}

func TestPatchPathsBoundsDistinctTargets(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	for i := 0; i < maxPatchPaths; i++ {
		fmt.Fprintf(&body, "*** Delete File: file-%d\n", i)
	}
	if paths, err := patchPaths(patchText(body.String())); err != nil || len(paths) != maxPatchPaths {
		t.Fatalf("at-limit control: paths=%d err=%v", len(paths), err)
	}
	// A repeated target consumes no extra adjudication, but its malformed
	// content must still invalidate the entire patch.
	body.WriteString("*** Delete File: file-0\n")
	if paths, err := patchPaths(patchText(body.String())); err != nil || len(paths) != maxPatchPaths {
		t.Fatalf("dedup control: paths=%d err=%v", len(paths), err)
	}
	if paths, err := patchPaths(patchText(body.String() + "+bad\n")); err == nil || paths != nil {
		t.Fatalf("duplicate must still validate structure: paths=%v err=%v", paths, err)
	}
	body.WriteString("*** Delete File: one-too-many\n")
	if paths, err := patchPaths(patchText(body.String())); err == nil || paths != nil || !strings.Contains(err.Error(), "distinct target paths") {
		t.Fatalf("over-limit must refuse: paths=%v err=%v", paths, err)
	}
}

func TestGatePatchChecksEveryOperation(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "core", "--session", "sess-a", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	cases := []struct{ name, body string }{
		{"add", "*** Add File: src/new.go\n+package core\n"},
		{"delete", "*** Delete File: src/old.go\n"},
		{"update", "*** Update File: src/old.go\n@@\n-old\n+new\n"},
		{"move-source", "*** Update File: src/old.go\n*** Move to: docs/new.go\n@@\n-old\n+new\n"},
		{"move-destination", "*** Update File: docs/old.go\n*** Move to: src/new.go\n@@\n-old\n+new\n"},
		{"later-path", "*** Add File: docs/okay.go\n+okay\n*** Delete File: src/old.go\n"},
		{"multiple-conflicts", "*** Delete File: src/one.go\n*** Delete File: src/two.go\n"},
		{"trailing-header-space", "*** Delete File: src/old.go   \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errw, code := f.run(t, f.wtB, patchHookJSON("sess-b", f.wtB, patchText(tc.body)), "gate")
			reason, denied := decodeDeny(t, out) // Rejects multiple JSON verdicts too.
			if code != 0 || !denied || !strings.Contains(reason, `slug "core"`) {
				t.Fatalf("patch must be denied whole by core: code=%d out=%q stderr=%q", code, out, errw)
			}
			// The same patch is permitted for its holder, proving parsing and
			// claim attribution both ran rather than every patch being refused.
			out, errw, code = f.run(t, f.repo, patchHookJSON("sess-a", f.repo, patchText(tc.body)), "gate")
			if code != 0 || out != "" || errw != "" {
				t.Fatalf("owner control must pass: code=%d out=%q stderr=%q", code, out, errw)
			}
		})
	}
	// Content that spells operation headers remains content. Stripping the
	// diff prefix before parsing would fabricate writes to claimed paths.
	body := "*** Add File: docs/new.go\n+*** Delete File: src/fake.go\n" +
		"*** Update File: docs/old.go\n@@\n *** Update File: src/fake.go\n-*** Move to: src/fake.go\n+*** End Patch\n"
	out, errw, code := f.run(t, f.wtB, patchHookJSON("sess-b", f.wtB, patchText(body)), "gate")
	if code != 0 || out != "" || errw != "" {
		t.Fatalf("disjoint content control must pass: code=%d out=%q stderr=%q", code, out, errw)
	}
}

func TestGatePatchMalformedInputFailsClosed(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for _, tc := range []struct{ name, patch string }{
		{"missing-command", ""},
		{"empty-patch", patchText("")},
		{"truncated", "*** Begin Patch\n*** Add File: free.go\n+ok\n"},
		{"no-header", patchText("free.go\n+ok\n")},
		{"empty-path", patchText("*** Delete File: \n")},
		{"nul-path", patchText("*** Delete File: bad\x00name\n")},
		{"update-without-lines", patchText("*** Update File: free.go\n@@\n")},
		{"unprefixed-add", patchText("*** Add File: free.go\nok\n")},
		{"content-after-delete", patchText("*** Delete File: free.go\n+ok\n")},
		{"misplaced-move", patchText("*** Add File: free.go\n*** Move to: other.go\n")},
		{"move-after-content", patchText("*** Update File: free.go\n@@\n+ok\n*** Move to: other.go\n")},
		{"late-malformed", patchText("*** Add File: free.go\n+ok\n*** Bogus File: other.go\n")},
		{"trailing-content", patchText("*** Delete File: free.go\n") + "*** Delete File: other.go\n"},
		{"unknown-environment", patchText("*** Environment ID: remote\n*** Delete File: free.go\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, _ := f.run(t, f.repo, patchHookJSON("sess-b", f.repo, tc.patch), "gate")
			reason, denied := decodeDeny(t, out)
			if !denied || !strings.Contains(reason, "apply_patch") {
				t.Fatalf("unadjudicable patch must fail closed: %q", out)
			}
		})
	}
	valid := patchText("*** Update File: free.go\n@@\n-old\n+new\n*** End of File\n")
	out, errw, code := f.run(t, f.repo, patchHookJSON("sess-b", f.repo, valid), "gate")
	if code != 0 || out != "" || errw != "" {
		t.Fatalf("valid control must pass: code=%d out=%q stderr=%q", code, out, errw)
	}
}

func TestGatePatchForeignTargetsAndFeatureOff(t *testing.T) {
	boundedParallel(t)
	home := newFixture(t) // Deliberately no home ledger.
	foreign := newFixture(t)
	foreign.initAndHello(t)
	if _, errw, code := foreign.run(t, foreign.repo, "", "claim", "foreign-core", "--session", "sess-a", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	for _, cwd := range []string{home.repo, t.TempDir()} {
		body := "*** Add File: free.go\n+ok\n*** Delete File: " + filepath.Join(foreign.repo, "src/core.go") + "\n"
		out, _, _ := home.run(t, cwd, patchHookJSON("sess-b", cwd, patchText(body)), "gate")
		reason, denied := decodeDeny(t, out)
		if !denied || !strings.Contains(reason, "foreign-core") {
			t.Fatalf("ungoverned cwd must not hide target ledger: cwd=%q out=%q", cwd, out)
		}
		// Single-path tools share the same target rule; absence of the cwd's
		// ledger cannot bypass it merely because this call came from Claude.
		out, _, _ = home.run(t, cwd, hookJSON("sess-b", cwd, "Edit", filepath.Join(foreign.repo, "src/core.go")), "gate")
		if _, denied := decodeDeny(t, out); !denied {
			t.Fatalf("single-path target lookup must use the same rule: %q", out)
		}
		out, errw, code := home.run(t, cwd, patchHookJSON("sess-b", cwd, patchText("*** Add File: free.go\n+ok\n")), "gate")
		if code != 0 || out != "" || errw != "" {
			t.Fatalf("no-ledger control must pass: code=%d out=%q stderr=%q", code, out, errw)
		}
	}
	// Discovery must not collapse a corrupt target ledger into no ledger.
	broken := newFixture(t)
	if err := os.WriteFile(filepath.Join(broken.repo, ".git", "buddy.db"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := home.run(t, home.repo, patchHookJSON("sess-b", home.repo,
		patchText("*** Add File: "+filepath.Join(broken.repo, "free.go")+"\n+ok\n")), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied || !strings.Contains(reason, "unavailable") {
		t.Fatalf("corrupt foreign ledger must deny: %q", out)
	}
	out, _, _ = home.run(t, broken.repo, patchHookJSON("sess-b", broken.repo,
		patchText("*** Add File: free.go\n+ok\n")), "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "unavailable") {
		t.Fatalf("corrupt home ledger must deny: %q", out)
	}
}

func TestGatePatchSharedClaimsNormalizationAndPause(t *testing.T) {
	boundedParallel(t)
	f := newFixtureNamed(t, "café")
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "shared-core", "--session", "sess-a", "--shared", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	// Absolute NFD spelling and mixed case take the same placement path as
	// Claude edits, including when the target does not exist yet.
	target := strings.ReplaceAll(filepath.Join(f.repo, "SRC/new.go"), "café", "café")
	patch := patchText("*** Add File: " + target + "\n+ok\n")
	out, _, _ := f.run(t, f.repo, patchHookJSON("sess-b", f.repo, patch), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied || !strings.Contains(reason, "claimed SHARED") {
		t.Fatalf("nonholder must respect normalized shared scope: %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "joined", "--session", "sess-b", "--shared", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := f.run(t, f.repo, patchHookJSON("sess-b", f.repo, patch), "gate")
	if code != 0 || out != "" || errw != "" {
		t.Fatalf("shared holder control must pass: code=%d out=%q stderr=%q", code, out, errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.repo, patchHookJSON("sess-b", f.repo, patch), "gate")
	reason, denied = decodeDeny(t, out)
	if !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("pause must apply to a permitted patch: %q", out)
	}
}

func TestGatePatchNestedTargetWithoutHomeLedger(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t) // The outer repository has no ledger.
	nested := filepath.Join(f.repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", nested, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("init nested repo: %v %s", err, out)
	}
	if _, errw, code := f.run(t, nested, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, nested, hookJSON("nested-owner", nested, "", ""), "hello"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, nested, "", "claim", "nested-core", "--session", "nested-owner", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	for _, governedHome := range []bool{false, true} {
		if governedHome {
			f.initAndHello(t)
		}
		for _, tc := range []struct {
			path string
			deny bool
		}{
			{"nested/src/core.go", true},
			{"nested/free.go", false},
			{"outer-free.go", false},
		} {
			out, _, _ := f.run(t, f.repo, patchHookJSON("sess-b", f.repo,
				patchText("*** Add File: "+tc.path+"\n+ok\n")), "gate")
			reason, denied := decodeDeny(t, out)
			if denied != tc.deny || denied && !strings.Contains(reason, "nested-core") {
				t.Fatalf("governed home=%v target %q: want deny=%v, got %q", governedHome, tc.path, tc.deny, out)
			}
		}
	}
}
