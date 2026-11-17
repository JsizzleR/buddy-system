package harness

// NATIVE STDERR (D-200). A worker's stderr and a --version probe's stderr used
// to be the adapter's own file descriptor (cmd.Stderr = os.Stderr), so every
// byte a native build, a hook it ran, or a tool descendant still holding the
// descriptor wrote reached the adapter's readers verbatim. Those readers are
// not only a terminal: a qualify runner saves the stream as `adapter.stderr`
// evidence, quotes it in its own exception when the adapter exits early, and
// a coordinator model reads that file or the Bash tool output it ran the
// adapter under. Raw, one native write could drive the terminal (ANSI, OSC),
// reorder text (bidi), flood a model's context (a newline-free megabyte), or
// forge the adapter's own lines: the checkers count exactly one line starting
// `buddy-adapter: worker closed (` and find a refusal by substring, and a
// native line could be either.
//
// Now the child gets a pipe of its own and this pump renders each native line
// as ONE line: the `native stderr: ` prefix (no native line can begin as the
// adapter's own), then fence.Line at nativeStderrLineCap. The stream as a
// whole is bounded at nativeStderrTotalCap rendered bytes; past it the pump
// says so once, keeps DRAINING (a child blocked on a full stderr pipe would
// wedge mid-turn, which a cap must never cause), and prints the count of
// what it did not show when the stream ends. Nothing parses native stderr,
// before or after (process.go's header); the evidence a checker needs from it
// (Codex's log of a hook denial, check-adapters.py) is plain ASCII on a short
// line and survives the fence as a substring.
//
// Refuted alternatives: a raw copy kept beside the fenced one (no reader
// needs native bytes; the evidence hash binds whatever file the runner saved);
// exec's own copying goroutine (a non-*os.File Stderr makes cmd.Wait wait for
// EOF, so an escaped descendant holding the pipe would hang terminate forever:
// the same reason stdout is our own pipe); a tail kept past the cap (the head
// carries the start-up failure, the cap is a flood bound, not a log).
//
// The pump ends at EOF, when every holder of the write end is gone. A
// descendant that left the worker's group may still hold it; finish then
// releases OUR read end once nothing is unread or a bound passes, as
// releaseAfterExit does for stdout. That descendant's next write meets a
// closed pipe, as its next stdout write already did; it is not signalled.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

const (
	// nativeStderrPrefix starts every line the pump writes. It is not the
	// adapter's own `buddy-adapter:` prefix, so a native line can never be
	// read as an adapter report.
	nativeStderrPrefix = "native stderr: "
	// nativeStderrLineCap bounds one native line, in bytes, before
	// fence.Line's truncation marker. A longer raw line is cut on the first
	// nativeStderrLineCap bytes read; the rest is drained unread.
	nativeStderrLineCap = 1024
	// nativeStderrTotalCap bounds the rendered stream per child. Native
	// stderr volume was not measured; this is a flood bound sized for a
	// model reading the saved file (~16k tokens), not a quota on a log.
	nativeStderrTotalCap = 64 << 10
)

// nativeStderrTestHooks, when a test sets them, apply to children started
// after: sink replaces the adapter's stderr as the pump's destination;
// pumping runs before the pump's first read (a test's hold on it); finishing
// runs as finish begins.
var nativeStderrTestHooks atomic.Pointer[stderrHooks]

type stderrHooks struct {
	sink      io.Writer
	pumping   func()
	finishing func()
}

func nativeStderrOut() io.Writer {
	if h := nativeStderrTestHooks.Load(); h != nil && h.sink != nil {
		return h.sink
	}
	return os.Stderr
}

type nativeStderr struct {
	r         *os.File
	done      chan struct{}
	closeOnce sync.Once
	finishing func()
}

// newNativeStderr returns the pump and the write end the child is given. The
// caller closes the write end once the child holds it (or the launch failed),
// and calls finish when the child is reaped.
func newNativeStderr(out io.Writer) (*nativeStderr, *os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	s := &nativeStderr{r: r, done: make(chan struct{}), finishing: func() {}}
	pumping := func() {}
	if h := nativeStderrTestHooks.Load(); h != nil {
		if h.pumping != nil {
			pumping = h.pumping
		}
		if h.finishing != nil {
			s.finishing = h.finishing
		}
	}
	go func() {
		defer close(s.done)
		// At EOF too: a reader left open past its stream leaked a descriptor
		// per worker and probe (Codex code pass).
		defer s.release()
		pumping()
		fenceNativeStderr(r, out)
	}()
	return s, w, nil
}

func (s *nativeStderr) release() { s.closeOnce.Do(func() { _ = s.r.Close() }) }

// finish waits for the pump to reach EOF, then, while another holder keeps the
// write end open, releases our read end once nothing is unread, or at bound
// with unread bytes left (they are not shown; the pump has already rendered
// what it read). It returns once the pump has written its last line, so a
// report the adapter prints afterwards follows every native line, unless the
// pump is blocked writing to an adapter stderr nobody drains: closing the read
// end cannot interrupt that write, so finish stops waiting one more bound
// later and leaves the pump blocked, where the adapter's own next stderr write
// would block too (Codex code pass: Close and the probe hung there forever).
func (s *nativeStderr) finish(bound time.Duration) {
	s.finishing()
	deadline := time.Now().Add(bound)
	for {
		select {
		case <-s.done:
			return
		case <-time.After(20 * time.Millisecond):
		}
		pending, err := pipePending(s.r)
		if err != nil || !pending || !time.Now().Before(deadline) {
			break
		}
	}
	s.release()
	select {
	case <-s.done:
	case <-time.After(bound):
	}
}

// abandon closes the read end at once, for a launch that never started a
// child (no native line can be pending).
func (s *nativeStderr) abandon() {
	s.release()
	<-s.done
}

// fenceNativeStderr renders r onto out, one fenced line per native line, until
// r ends or fails. A write error on out is ignored: the pump keeps draining so
// the child never blocks on its own stderr because the adapter's is gone.
func fenceNativeStderr(r io.Reader, out io.Writer) {
	// Room for a line at the cap plus its CRLF: a buffer of exactly the cap
	// marked a 1024-byte line truncated when nothing of it was cut.
	br := bufio.NewReaderSize(r, nativeStderrLineCap+2)
	shown := 0
	hiddenLines, hiddenBytes := 0, 0
	for {
		chunk, err := br.ReadSlice('\n')
		line := string(chunk)
		raw := len(chunk)
		long := errors.Is(err, bufio.ErrBufferFull)
		// Drain the rest of an over-long line without holding it.
		for errors.Is(err, bufio.ErrBufferFull) {
			var more []byte
			more, err = br.ReadSlice('\n')
			raw += len(more)
		}
		if raw > 0 && hiddenLines > 0 {
			hiddenLines++
			hiddenBytes += raw
		} else if raw > 0 {
			rendered := renderNativeStderrLine(line, long)
			if shown+len(rendered) > nativeStderrTotalCap {
				hiddenLines, hiddenBytes = 1, raw
				_, _ = io.WriteString(out, fmt.Sprintf("%snative output past %d rendered bytes is not shown; its size follows when the stream ends\n", nativeStderrPrefix, nativeStderrTotalCap))
			} else {
				shown += len(rendered)
				_, _ = io.WriteString(out, rendered)
			}
		}
		if err != nil {
			break
		}
	}
	if hiddenLines > 0 {
		_, _ = io.WriteString(out, fmt.Sprintf("%s%d more line(s), %d byte(s), not shown\n", nativeStderrPrefix, hiddenLines, hiddenBytes))
	}
}

// renderNativeStderrLine is one native line as the pump writes it: prefixed,
// fenced, bounded, newline-terminated. long says the raw line was cut at the
// reader's buffer, so it is marked truncated even when fencing shrank it.
func renderNativeStderrLine(line string, long bool) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if long {
		// The cut may split a rune; drop the partial one rather than render
		// replacement characters for it.
		i := len(line) - 1
		for i > 0 && i > len(line)-utf8.UTFMax && !utf8.RuneStart(line[i]) {
			i--
		}
		if i >= 0 && !utf8.FullRuneInString(line[i:]) {
			line = line[:i]
		}
	}
	text := fence.Line(line, nativeStderrLineCap)
	if long && !strings.HasSuffix(text, "…[truncated]") {
		text += "…[truncated]"
	}
	return nativeStderrPrefix + text + "\n"
}
