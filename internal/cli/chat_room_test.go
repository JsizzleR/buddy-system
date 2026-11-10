package cli

import (
	"strings"
	"testing"
)

// Issue #83 (a). The digest's room line derived the room from the label's text
// before "/": right for a default label in the main checkout, wrong for a
// BUDDY_LABEL label (no project half: `chat_read fable-review`) and for a
// default label in a linked worktree (`chat_read wtB`), neither a room the
// daemon serves. The daemon names its rooms for projects, and a project is
// the main checkout's directory name (D-101's rule for `orc-<project>`).
func TestHelloRoomIsTheMainCheckoutsName(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for _, tc := range []struct {
		name, session, dir string
		args               []string
	}{
		{"default label, main checkout (the control)", "sess-r1", f.repo, nil},
		{"BUDDY_LABEL label", "sess-r2", f.repo, []string{"--label", "fable-review"}},
		{"default label, linked worktree", "sess-r3", f.wtB, nil},
	} {
		out, errw, code := f.run(t, tc.dir, hookJSON(tc.session, tc.dir, "", ""), append([]string{"hello"}, tc.args...)...)
		if code != 0 {
			t.Fatalf("%s: hello: %s", tc.name, errw)
		}
		if !strings.Contains(out, "`chat_read repo`") {
			t.Fatalf("%s: the room is the main checkout's name:\n%s", tc.name, out)
		}
	}
}

func TestMainCheckoutName(t *testing.T) {
	for _, tc := range []struct{ common, want string }{
		{"/w/buddy-system/.git", "buddy-system"},
		{"/w/buddy-system.git", "buddy-system"}, // a bare repository
		{"/w/odd", "odd"},
		{"/.git", ""},
		{"", ""},
	} {
		if got := mainCheckoutName(tc.common); got != tc.want {
			t.Errorf("mainCheckoutName(%q) = %q, want %q", tc.common, got, tc.want)
		}
	}
}
