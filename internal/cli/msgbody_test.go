package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// runStdin is fixture.run with a caller-supplied stdin, so a test can hand the
// binary a real *os.File and exercise the stdinIsTTY branch. fixture.run wraps
// a string in a strings.Reader, which is never a terminal.
func (f *fixture) runStdin(t *testing.T, cwd string, stdin io.Reader, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errw bytes.Buffer
	code = Run(args, Env{
		Stdin:   stdin,
		Stdout:  &out,
		Stderr:  &errw,
		Cwd:     cwd,
		Now:     func() time.Time { return f.clock },
		Getenv:  func(k string) string { return f.env[k] },
		SockDir: f.sockDir,
	})
	return out.String(), errw.String(), code
}

// `buddy msg` reading its body from stdin (issue #14, wishlist §13).
//
// The incident: a broadcast sent with a heredoc body printed the usage line
// and never sent. The item recorded two causes — "ignores stdin" and "the
// usage path exits 0" — and only the first was there. Both halves are pinned
// here: the stdin body works, and the usage path still exits NON-ZERO, so the
// refuted half cannot quietly become true later.

// charDevice is an *os.File that stdinIsTTY reports as a terminal. /dev/null
// is a character device, which is the same test readHook applies, so this
// exercises the real branch rather than a stand-in for it.
func charDevice(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no %s to stand in for a terminal: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestMsgReadsTheBodyFromStdin(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// The incident's shape: a target, a --from, and the text on stdin.
	out, errw, code := f.run(t, f.repo, "hold until the gate is green\n", "msg", "bravo", "--from", "ana")
	if code != 0 {
		t.Fatalf("stdin body refused: %s %s", out, errw)
	}
	if !strings.Contains(out, "queued for") {
		t.Fatalf("no send reported: %q", out)
	}

	// POSITIVE CONTROL for the whole test: the message actually arrives. A
	// "queued" line proves the command returned, not that anything was stored.
	in, errw, code := f.run(t, f.wtB, "", "inbox", "--session", "sess-b")
	if code != 0 {
		t.Fatalf("inbox: %s", errw)
	}
	if !strings.Contains(in, "hold until the gate is green") {
		t.Fatalf("stdin body never reached the inbox: %q", in)
	}
	// The heredoc's trailing newline must not ride along as a trailing ⏎.
	if strings.Contains(in, "green⏎") {
		t.Fatalf("trailing newline was not trimmed: %q", in)
	}
}

func TestMsgBodySourceAndRefusals(t *testing.T) {
	boundedParallel(t)

	cases := []struct {
		name     string
		stdin    string
		tty      bool
		args     []string
		wantCode int
		wantIn   string // substring of the message as the inbox renders it
		wantErr  string // substring of stderr when refused
	}{
		{
			name: "argv wins and stdin is not read", stdin: "FROM STDIN",
			args: []string{"msg", "bravo", "from", "argv"}, wantIn: "from argv",
		},
		{
			name: "stdin is the body when argv has none", stdin: "from stdin",
			args: []string{"msg", "bravo"}, wantIn: "from stdin",
		},
		{
			name: "interior newlines survive and are fenced", stdin: "line one\nline two\n",
			args: []string{"msg", "bravo"}, wantIn: "line one⏎line two",
		},
		{
			name: "empty stdin is refused, and not as a usage error", stdin: "",
			args: []string{"msg", "bravo"}, wantCode: 1, wantErr: "stdin was empty",
		},
		{
			name: "whitespace-only stdin is refused too", stdin: "  \n\t\n",
			args: []string{"msg", "bravo"}, wantCode: 1, wantErr: "stdin was empty",
		},
		{
			// The hang readHook's stdinIsTTY was added to prevent, one verb over.
			name: "a terminal is never read", tty: true,
			args: []string{"msg", "bravo"}, wantCode: 1, wantErr: "usage: buddy msg",
		},
		{
			// The WHOLE body, not a prefix of it. Codex predicted the mutation
			// this closes: truncating the body to 64 bytes inside msgBody
			// would have satisfied an assertion that only looked for 64 x's,
			// while discarding 4032 bytes of a message reported as sent.
			name: "exactly the cap is accepted, whole", stdin: strings.Repeat("x", maxMsgBody),
			args: []string{"msg", "bravo"}, wantIn: strings.Repeat("x", maxMsgBody),
		},
		{
			name:  "one byte over the cap is refused, naming the numbers",
			stdin: strings.Repeat("x", maxMsgBody+1), args: []string{"msg", "bravo"},
			wantCode: 1, wantErr: "would be cut silently",
		},
		{
			// Codex P2: a heredoc's own trailing newline must not cost a byte
			// of the allowance, because the cap covers what is STORED and the
			// newline is trimmed before storage.
			name:  "the cap counts the trimmed body, so a heredoc newline is free",
			stdin: strings.Repeat("x", maxMsgBody) + "\n", args: []string{"msg", "bravo"},
			wantIn: strings.Repeat("x", maxMsgBody),
		},
		{
			// Codex P2, its exact input: 4096 raw bytes that RENDER to 4098,
			// because fence.Line expands the newline to ⏎ at three bytes. The
			// old raw-byte cap accepted this and the trailing Z vanished from
			// what the recipient read.
			name:  "a body that renders over the cap is refused, though its bytes fit",
			stdin: strings.Repeat("x", maxMsgBody-2) + "\nZ", args: []string{"msg", "bravo"},
			wantCode: 1, wantErr: "renders to 4098 bytes",
		},
		{
			// Codex P2: the guard must not be reachable around via argv.
			name:     "argv is capped too, not just stdin",
			args:     []string{"msg", "bravo", strings.Repeat("x", maxMsgBody+1)},
			wantCode: 1, wantErr: "would be cut silently",
		},
		{
			name: "empty argv text is refused as empty, not as usage",
			args: []string{"msg", "bravo", "   "}, wantCode: 1, wantErr: "message text is empty",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)

			var out, errw string
			var code int
			if tc.tty {
				out, errw, code = f.runStdin(t, f.repo, charDevice(t), tc.args...)
			} else {
				out, errw, code = f.run(t, f.repo, tc.stdin, tc.args...)
			}

			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d (out %q err %q)", code, tc.wantCode, out, errw)
			}
			if tc.wantErr != "" && !strings.Contains(errw, tc.wantErr) {
				t.Fatalf("stderr %q does not carry %q", errw, tc.wantErr)
			}
			if tc.wantIn == "" {
				return
			}
			in, _, code := f.run(t, f.wtB, "", "inbox", "--session", "sess-b")
			if code != 0 {
				t.Fatalf("inbox exit %d", code)
			}
			if !strings.Contains(in, tc.wantIn) {
				t.Fatalf("inbox %q does not carry %q", in, tc.wantIn)
			}
		})
	}
}

// The refuted half of issue #14, pinned so it cannot become true: a usage
// error exits NON-ZERO. The rc=0 in the field report came from a pipe, since
// `sh` has no pipefail — not from this path.
func TestMsgUsageErrorExitsNonZero(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// A terminal with no argv text is the usage path.
	_, errw, code := f.runStdin(t, f.repo, charDevice(t), "msg", "bravo", "--from", "ana")
	if code == 0 {
		t.Fatalf("usage error exited 0; that is the failure the field report inferred and it must stay false: %q", errw)
	}
	if !strings.Contains(errw, "usage: buddy msg") {
		t.Fatalf("no usage line: %q", errw)
	}

	// POSITIVE CONTROL: the same fixture sends fine when given text, so the
	// non-zero above is the usage path and not a broken ledger.
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "ok"); code != 0 {
		t.Fatalf("control send failed, so the refusal above proves nothing: %s", errw)
	}
}

func TestMsgDryRunResolvesAndSendsNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--dry-run", "would", "this", "arrive")
	if code != 0 {
		t.Fatalf("dry run refused: %s %s", out, errw)
	}
	if !strings.Contains(out, "17 byte(s)") {
		t.Fatalf("dry run did not measure the body: %q", out)
	}
	if !strings.Contains(out, "nothing was queued") {
		t.Fatalf("dry run did not say it sent nothing: %q", out)
	}
	if in, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); strings.Contains(in, "would this arrive") {
		t.Fatalf("dry run QUEUED the message: %q", in)
	}

	// POSITIVE CONTROL: the identical command without --dry-run does queue, so
	// the empty inbox above is the flag and not a target that never resolved.
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "would", "this", "arrive"); code != 0 {
		t.Fatalf("control send failed: %s", errw)
	}
	if in, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); !strings.Contains(in, "would this arrive") {
		t.Fatal("control send did not queue, so the dry-run assertion proves nothing")
	}
}

// Codex P3: `--dry-run` against an ENDED target used to print "this is queued
// against its id" from the shared resolver and then "nothing was queued" from
// the preview — one command contradicting itself, and the false half is the
// exact shape of assurance this whole issue is about.
func TestMsgDryRunDoesNotClaimAnEndedTargetWasQueued(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}

	out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--dry-run", "still there?")
	if code != 0 {
		t.Fatalf("dry run to an ended target should still forecast: %s %s", out, errw)
	}
	if strings.Contains(out+errw, "is queued against its id") {
		t.Fatalf("preview claimed a queue that never happened: out %q err %q", out, errw)
	}
	if !strings.Contains(out, "has ENDED") {
		t.Fatalf("preview dropped the ENDED fact, which is what the sender needs: %q", out)
	}

	// POSITIVE CONTROL: a REAL send to the same ended target still carries the
	// ENDED fact — on the result line itself since issue #24, not as a stderr
	// aside — so the assertion above is the preview path and not a lost
	// warning.
	out, errw, code = f.run(t, f.repo, "", "msg", "bravo", "still there?")
	if code != 0 {
		t.Fatalf("control send: %s", errw)
	}
	if !strings.Contains(out, "queued for bravo — it ENDED") {
		t.Fatalf("the real send lost its ENDED fact: out %q err %q", out, errw)
	}
}

// A dry run must not report success for a target that does not resolve: the
// resolution is the thing being checked, so it happens before the flag.
func TestMsgDryRunStillRefusesAnUnresolvableTarget(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	if _, _, code := f.run(t, f.repo, "", "msg", "nobody-by-that-name", "--dry-run", "hello"); code == 0 {
		t.Fatal("dry run reported success for a target that resolves to nothing")
	}
	// POSITIVE CONTROL: a resolvable target does pass the dry run.
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--dry-run", "hello"); code != 0 {
		t.Fatalf("control dry run failed: %s", errw)
	}
}
