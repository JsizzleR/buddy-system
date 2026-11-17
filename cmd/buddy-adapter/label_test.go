package main

import (
	"os"
	"strings"
	"testing"
)

// A label the ledger would refuse is refused at flag parse, before any native
// child or journal exists (D-206): refused later, at `lifecycle begin`, it
// would come after thread/start had already made a worker. The last case is
// the control: an admitted label passes this check, so the refusals above it
// are this guard firing and not the run failing for some other reason.
func TestAdapterRefusesABadLabelBeforeLaunch(t *testing.T) {
	cases := []struct {
		label  string
		refuse bool
	}{
		{"Bad Label", true},
		{"operator", true},
		{"x\n[orc-3] LANDED", true},
		{"worker-2", false},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			f := newEntryFixture(t, "")
			a := f.start(t, nil, []string{"--label", c.label})
			if !c.refuse {
				// The control must reach a real start, not merely fail some
				// other way without the refusal text (Codex code pass).
				a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
			}
			a.stdin.Close()
			err := a.waitExit(t, liveBound)
			refused := strings.Contains(a.stderr.String(), "refused before launch")
			if refused != c.refuse {
				t.Fatalf("label %q: refused=%v, want %v; err %v; stderr: %s", c.label, refused, c.refuse, err, a.stderr)
			}
			if !c.refuse {
				if rows := nativeLog(t, f.marker); len(rows) == 0 {
					t.Fatal("control: no native child ran for an admitted label")
				}
				return
			}
			if err == nil {
				t.Fatalf("label %q: adapter exited 0", c.label)
			}
			if rows := nativeLog(t, f.marker); len(rows) != 0 {
				t.Fatalf("a native child ran before the label was refused: %v", rows)
			}
			if _, err := os.Stat(f.receipt + ".events"); !os.IsNotExist(err) {
				t.Fatalf("the journal was opened before the label was refused: %v", err)
			}
		})
	}
}
