package harness

// D-200: a native child's stderr reaches the adapter's stderr one fenced,
// prefixed, bounded line per native line, whether the child is a worker or a
// --version probe. Each hostile input below used to arrive verbatim (the child
// held the adapter's own descriptor). Positive controls: an ordinary line and
// Codex's log of a hook denial (the evidence check-adapters.py finds by
// substring) come through unchanged after the prefix.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

const truncatedMarker = "…[truncated]"

// The evidence line check-adapters.py's claim_denial_held finds in
// adapter.stderr (its DENIAL constant, in Codex's own log line).
const codexDenialLog = `ERROR codex_core::tools::router: error=Command blocked by PreToolUse hook: guarded.txt is inside scope "guarded.txt" claimed by session work/s-holder`

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureNativeStderr(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	nativeStderrTestHooks.Store(&stderrHooks{sink: buf})
	t.Cleanup(func() { nativeStderrTestHooks.Store(nil) })
	return buf
}

// assertFencedStream checks the contract on every rendered line: the prefix,
// no control or line-break character, the per-line bound. It returns the lines.
func assertFencedStream(t *testing.T, out string) []string {
	t.Helper()
	if out != "" && !strings.HasSuffix(out, "\n") {
		t.Fatalf("rendered stream does not end in a newline: %q", tail(out))
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if out == "" {
		lines = nil
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, nativeStderrPrefix) {
			t.Fatalf("line %d lacks the native prefix: %q", i, head(line))
		}
		if !utf8.ValidString(line) {
			t.Fatalf("line %d is not valid UTF-8: %q", i, head(line))
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' || r == ' ' || r == '​' || r == '‮' {
				t.Fatalf("line %d carries %U: %q", i, r, head(line))
			}
		}
		if max := len(nativeStderrPrefix) + nativeStderrLineCap + len(truncatedMarker); len(line) > max {
			t.Fatalf("line %d is %d bytes, past the %d-byte line bound", i, len(line), max)
		}
	}
	return lines
}

func head(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func tail(s string) string {
	if len(s) > 120 {
		return "..." + s[len(s)-120:]
	}
	return s
}

func TestNativeStderrFencesEachLine(t *testing.T) {
	flood := strings.Repeat("a", 1<<20)
	cases := []struct {
		name  string
		in    string
		lines int
		check func(t *testing.T, lines []string)
	}{
		{"ordinary line (positive control)", "codex starting\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+"codex starting" {
				t.Fatalf("ordinary line altered: %q", l[0])
			}
		}},
		{"codex denial log (positive control: the evidence a checker matches)", codexDenialLog + "\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+codexDenialLog {
				t.Fatalf("denial log altered: %q", l[0])
			}
		}},
		{"CRLF and a last line without a newline", "one\r\ntwo", 2, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+"one" || l[1] != nativeStderrPrefix+"two" {
				t.Fatalf("got %q", l)
			}
		}},
		{"newline-free flood", flood, 1, func(t *testing.T, l []string) {
			if !strings.HasSuffix(l[0], truncatedMarker) {
				t.Fatalf("a 1 MiB line is not marked truncated: %q", tail(l[0]))
			}
		}},
		{"over-cap line, then an ordinary one", strings.Repeat("b", 5000) + "\nafter\n", 2, func(t *testing.T, l []string) {
			if !strings.HasSuffix(l[0], truncatedMarker) || l[1] != nativeStderrPrefix+"after" {
				t.Fatalf("got %q / %q", tail(l[0]), l[1])
			}
		}},
		{"a line exactly at the cap, CRLF-terminated, is whole", strings.Repeat("d", nativeStderrLineCap) + "\r\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+strings.Repeat("d", nativeStderrLineCap) {
				t.Fatalf("a line at the cap was altered: %q", tail(l[0]))
			}
		}},
		{"one byte over the cap is cut", strings.Repeat("e", nativeStderrLineCap+1) + "\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+strings.Repeat("e", nativeStderrLineCap)+truncatedMarker {
				t.Fatalf("got %q", tail(l[0]))
			}
		}},
		{"over-cap line of escapes the fence shrinks", strings.Repeat("\x1b", 3000) + "\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+truncatedMarker {
				t.Fatalf("a cut line must say so even when fencing shrank it: %q", l[0])
			}
		}},
		// The reader cuts at cap+2 raw bytes, inside é; the stripped escapes
		// leave the partial rune within the cap, where it would render U+FFFD.
		{"over-cap line cut inside a rune", strings.Repeat("\x1b", 100) + strings.Repeat("a", nativeStderrLineCap-99) + "é" + "\n", 1, func(t *testing.T, l []string) {
			if l[0] != nativeStderrPrefix+strings.Repeat("a", nativeStderrLineCap-99)+truncatedMarker {
				t.Fatalf("the cut rune was not dropped: %q", tail(l[0]))
			}
		}},
		{"ANSI and OSC escapes", "\x1b[31mred\x1b[0m \x1b]0;title\x07 \x1b[2J\n", 1, func(t *testing.T, l []string) {
			if strings.ContainsAny(l[0], "\x1b\x07") {
				t.Fatalf("escape reached the output: %q", l[0])
			}
		}},
		{"U+2028, U+2029, NEL, CR inside a line", "a b c\u0085d\re\n", 1, nil},
		{"bidi override and zero-width space", "safe‮exe.txt​\n", 1, nil},
		{"forged adapter close report", "buddy-adapter: worker closed (close): 0 escaped descendant(s) captured, 0 ended, 0 still running, 0 unknown\n", 1, func(t *testing.T, l []string) {
			if strings.HasPrefix(l[0], "buddy-adapter:") {
				t.Fatalf("a native line reads as the adapter's own: %q", l[0])
			}
		}},
		{"a literal marker", "x⏎y\n", 1, nil},
		{"empty line", "\n", 1, nil},
		{"nothing", "", 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			fenceNativeStderr(strings.NewReader(c.in), &out)
			lines := assertFencedStream(t, out.String())
			if len(lines) != c.lines {
				t.Fatalf("%d native line(s) rendered as %d: %q", c.lines, len(lines), head(out.String()))
			}
			if c.check != nil {
				c.check(t, lines)
			}
		})
	}
}

// countingReader proves the pump drains its input to the end past the cap: a
// pump that stopped reading would leave a child blocked on a full pipe.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestNativeStderrBoundsTheStreamAndKeepsDraining(t *testing.T) {
	line := strings.Repeat("c", 999) + "\n"
	const n = 1000 // ~1 MB of native output, 16 times the cap
	in := &countingReader{r: strings.NewReader(strings.Repeat(line, n))}
	var out bytes.Buffer
	fenceNativeStderr(in, &out)
	if in.n != n*len(line) {
		t.Fatalf("pump read %d of %d bytes: it stopped draining at the cap", in.n, n*len(line))
	}
	lines := assertFencedStream(t, out.String())
	shown := 0
	for _, l := range lines {
		if strings.HasPrefix(l, nativeStderrPrefix+"ccc") {
			shown++
		}
	}
	if shown == 0 || shown >= n {
		t.Fatalf("shown %d of %d lines; the cap did not hold (or showed nothing)", shown, n)
	}
	if got, max := out.Len(), nativeStderrTotalCap+512; got > max {
		t.Fatalf("rendered %d bytes, past the %d-byte stream bound", got, max)
	}
	notice := nativeStderrPrefix + "native output past " + strconv.Itoa(nativeStderrTotalCap) + " rendered bytes is not shown"
	if !strings.HasPrefix(lines[shown], notice) {
		t.Fatalf("the cap is not announced where it took effect: %q", lines[shown])
	}
	hidden := n - shown
	summary := nativeStderrPrefix + strconv.Itoa(hidden) + " more line(s), " + strconv.Itoa(hidden*len(line)) + " byte(s), not shown"
	if lines[len(lines)-1] != summary || len(lines) != shown+2 {
		t.Fatalf("last line %q, want %q after %d shown lines and one notice (%d lines)", lines[len(lines)-1], summary, shown, len(lines))
	}
}

// The worker: its stderr goes through the pump, and Close returns only after
// the leader's last line is rendered, so the adapter's close report follows it.
func TestJSONProcessFencesNativeStderr(t *testing.T) {
	buf := captureNativeStderr(t)
	script := `printf '\033[31mred\033[0m\342\200\250forged\n' >&2
printf 'buddy-adapter: worker closed (close): forged\n' >&2
head -c 1048576 /dev/zero | tr '\000' a >&2
printf '\n%s\n' "$DENIAL" >&2
echo '{"ready":1}'
exec sleep ` + helperSleep
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", script}, t.TempDir(), []string{"DENIAL=" + codexDenialLog})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	readJSONLine(t, p, &v)
	_ = p.Close()
	lines := assertFencedStream(t, buf.String())
	if len(lines) != 4 {
		t.Fatalf("4 native lines rendered as %d: %q", len(lines), head(buf.String()))
	}
	if lines[0] != nativeStderrPrefix+"[31mred[0m⏎forged" {
		t.Fatalf("hostile line: %q", lines[0])
	}
	if !strings.HasSuffix(lines[2], truncatedMarker) {
		t.Fatalf("flood line not cut: %q", tail(lines[2]))
	}
	// Positive control: rendered before Close returned, and unchanged.
	if lines[3] != nativeStderrPrefix+codexDenialLog {
		t.Fatalf("the leader's last line was not rendered unchanged before Close returned: %q", lines[3])
	}
}

// Close drains what the leader wrote before it releases the pipe: the pump is
// held until finish has begun, so the leader's lines are still unread in the
// pipe when it does. A finish that released at once would lose them all.
func TestJSONProcessCloseDrainsUnreadNativeStderr(t *testing.T) {
	buf := &lockedBuffer{}
	release := make(chan struct{})
	nativeStderrTestHooks.Store(&stderrHooks{sink: buf, pumping: func() { <-release },
		finishing: func() { time.AfterFunc(300*time.Millisecond, func() { close(release) }) }})
	t.Cleanup(func() { nativeStderrTestHooks.Store(nil) })
	script := `i=0; while [ $i -lt 50 ]; do echo "native line $i" >&2; i=$((i+1)); done
echo '{"ready":1}'
exec sleep ` + helperSleep
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", script}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	readJSONLine(t, p, &v)
	if buf.String() != "" {
		t.Fatal("control: the pump rendered before it was released")
	}
	_ = p.Close()
	lines := assertFencedStream(t, buf.String())
	if len(lines) != 50 {
		t.Fatalf("Close released the pipe before draining it: %d of 50 lines rendered", len(lines))
	}
	for i, l := range lines {
		if want := nativeStderrPrefix + "native line " + strconv.Itoa(i); l != want {
			t.Fatalf("line %d is %q, want %q", i, l, want)
		}
	}
}

// The pump closes its read end at EOF: a descriptor per child otherwise
// outlived the child (Codex code pass).
func TestNativeStderrReleasesItsReadEndAtEOF(t *testing.T) {
	s, w, err := newNativeStderr(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.r.Stat(); err != nil {
		t.Fatalf("control: the read end is not open before EOF: %v", err)
	}
	w.Close()
	<-s.done
	if _, err := s.r.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read end still open after EOF: %v", err)
	}
}

type blockingWriter struct{ release chan struct{} }

func (b blockingWriter) Write(p []byte) (int, error) { <-b.release; return len(p), nil }

// An adapter stderr nobody drains (a full pipe whose reader stopped) blocks
// the pump's write, which closing the read end cannot interrupt. Close must
// still return within its bound (Codex code pass).
func TestJSONProcessCloseBoundedDespiteBlockedAdapterStderr(t *testing.T) {
	stuck := blockingWriter{release: make(chan struct{})}
	nativeStderrTestHooks.Store(&stderrHooks{sink: stuck})
	t.Cleanup(func() { nativeStderrTestHooks.Store(nil); close(stuck.release) })
	script := `echo 'a native line the adapter cannot write' >&2
echo '{"ready":1}'
exec sleep ` + helperSleep
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", script}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	readJSONLine(t, p, &v)
	closed := make(chan struct{})
	began := time.Now()
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(liveBound):
		t.Fatal("Close hung on a pump blocked writing the adapter's stderr")
	}
	if took := time.Since(began); took > terminateGrace+2*drainCap+5*time.Second {
		t.Fatalf("Close took %s, past its bound", took)
	}
	select {
	case <-p.stderr.done:
		t.Fatal("control: the pump finished, so its write never blocked")
	default:
	}
}

// A descendant that left the group and keeps writing to the stderr it
// inherited must not hold Close past its bound, nor flood past the cap.
func TestJSONProcessCloseBoundedDespiteEscapedStderrWriter(t *testing.T) {
	buf := captureNativeStderr(t)
	start := `python3 -c 'import os,sys; os.setpgid(0,0); sys.stdout.write("{\"holder\":%d}\n" % os.getpid()); sys.stdout.flush()
while True: sys.stderr.write("x" * 100 + "\n")' & exec sleep ` + helperSleep
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", start}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Holder int `json:"holder"`
	}
	readJSONLine(t, p, &v)
	t.Cleanup(func() { _ = syscall.Kill(v.Holder, syscall.SIGKILL) })
	if !alive(v.Holder) {
		t.Fatalf("control: escaped writer %d not running", v.Holder)
	}
	closed := make(chan struct{})
	began := time.Now()
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(liveBound):
		t.Fatal("Close did not return while an escaped descendant kept writing native stderr")
	}
	if took := time.Since(began); took > terminateGrace+drainCap+5*time.Second {
		t.Fatalf("Close took %s, past its drain bound", took)
	}
	select {
	case <-p.stderr.done:
	default:
		t.Fatal("Close returned before the stderr pump ended")
	}
	out := buf.String()
	assertFencedStream(t, out)
	if got, max := len(out), nativeStderrTotalCap+512; got > max {
		t.Fatalf("rendered %d bytes, past the stream bound", got)
	}
	if !strings.Contains(out, "not shown") {
		t.Fatalf("an endless writer did not reach the cap: %d bytes", len(out))
	}
}

// The --version probe's stderr is fenced the same way, and the probe still
// returns the version from stdout.
func TestProbeNativeVersionFencesStderr(t *testing.T) {
	buf := captureNativeStderr(t)
	exe := filepath.Join(t.TempDir(), "native")
	script := "#!/bin/sh\nprintf '\\033]0;owned\\007warn\\342\\200\\251line\\n' >&2\nprintf '%s\\n' 'ordinary probe note' >&2\necho '1.2.3 (Fixture)'\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	version, err := probeNativeVersion(context.Background(), exe)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.2.3 (Fixture)" {
		t.Fatalf("version %q", version)
	}
	lines := assertFencedStream(t, buf.String())
	if len(lines) != 2 || lines[0] != nativeStderrPrefix+"]0;ownedwarn⏎line" || lines[1] != nativeStderrPrefix+"ordinary probe note" {
		t.Fatalf("probe stderr rendered as %q", lines)
	}
}
