package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issue #75. A lane killed with no bye (a herdr crash, a pane closed without
// SessionEnd: 68 of 119 closes on one fleet, D-091) leaves its claims open, and
// every remedy the refusals named did nothing for a day: `sweep --force`
// orphans only sessions silent past ForceAfter (24h), and a bare `buddy release`
// from a shared checkout cannot tell who is calling. What does work is a plain
// `buddy bye <id>` (every process it registered is gone, so nothing refuses
// it) and then a plain `buddy sweep`, which orphans ended sessions' claims.
//
// So every view that judges the holder GONE names exactly that, and the test
// then RUNS what it named and watches the claim go. A live holder and a silent
// one (its process still running) are the controls: neither is named for bye,
// because a bye there would end a session that may still be working, and the
// GONE arm must be the only one that says it.
func TestAGoneHolderIsNamedForByeAndThatFreesIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(300, 3, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	f.claimFor(t, "sess-a", "held", "holding", "internal/api")
	f.stage(t, f.wtB, "internal/api/x.go")

	views := func() map[string]string {
		v := map[string]string{}
		out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "internal/api/x.go")), "gate")
		reason, denied := decodeDeny(t, out)
		if !denied {
			t.Fatalf("the gate must deny whatever the words say:\n%s", out)
		}
		v["gate"] = reason
		out, _, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "w", "--scope", "internal/api")
		if code == 0 {
			t.Fatal("the claim must refuse whatever the words say (D-022)")
		}
		v["refusal"] = out
		v["dry run"], _, _ = f.run(t, f.wtB, "", "claim", "wants", "--dry-run", "--session", "sess-b", "--scope", "internal/api")
		whose, _, _ := f.run(t, f.repo, "", "whose", "internal/api/x.go")
		v["whose"] = rowWith(t, whose, "held")
		who, _, _ := f.run(t, f.repo, "", "who", "sess-a")
		v["who"] = rowWith(t, who, "held")
		f.asSession("sess-b", func() { _, v["commit gate"], _ = f.run(t, f.wtB, "", "commit-gate") })
		return v
	}
	const bye = "buddy bye sess-a"
	noBye := func(phase string, v map[string]string) {
		t.Helper()
		for name, out := range v {
			if strings.Contains(out, "buddy bye") {
				t.Fatalf("%s, %s: a holder whose process runs is never named for bye:\n%s", phase, name, out)
			}
		}
	}

	noBye("live", views())
	// Silent: past StaleAfter with no beat, its process still running. The
	// STALE arm fires (the control that the views below are armed at all) and
	// still says nothing of bye.
	f.clock = f.clock.Add(store.StaleAfter + time.Minute)
	v := views()
	if !strings.Contains(v["refusal"], "STALE") || !strings.Contains(v["commit gate"], "SILENT") {
		t.Fatalf("silent control: the STALE arms must fire:\n%s\n%s", v["refusal"], v["commit gate"])
	}
	noBye("silent", v)

	f.kill(300)
	v = views()
	for _, name := range []string{"gate", "refusal", "dry run", "whose", "who", "commit gate"} {
		if !strings.Contains(v[name], bye) || !strings.Contains(v[name], "buddy sweep") {
			t.Fatalf("GONE, %s: want %q and then a sweep in:\n%s", name, bye, v[name])
		}
		if strings.Contains(v[name], "--force") {
			t.Fatalf("GONE, %s: the remedy needs no --force, and naming it would teach the form that ends a live session:\n%s", name, v[name])
		}
	}

	// The remedy, exactly as named, from a terminal (no hook input).
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatalf("the named bye: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "sweep"); code != 0 {
		t.Fatalf("the named sweep: %s", errw)
	}
	if out, errw, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "w", "--scope", "internal/api"); code != 0 {
		t.Fatalf("after the named remedy the scope must be free:\n%s%s", out, errw)
	}
}

// An id the reader cannot paste back as a command is named, not printed as
// one: a newline the fence would alter, and a leading dash `bye` would read as
// a flag (`buddy bye --help` prints the usage and ends nothing). The plain id
// is the positive control that the command form is still written.
func TestByeRemedyWritesACommandOnlyWhenItPastesBack(t *testing.T) {
	if got := byeRemedy("sess-a"); got != "buddy bye sess-a, then a plain buddy sweep" {
		t.Fatalf("control: a plain id is written as the command: %q", got)
	}
	for _, id := range []string{"--help", "-x", "a\nb", ""} {
		if got := byeRemedy(id); !strings.Contains(got, "<the holder's id>") {
			t.Fatalf("id %q must be named, not written as a command: %q", id, got)
		}
	}
}
