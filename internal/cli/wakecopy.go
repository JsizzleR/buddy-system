package cli

import (
	"fmt"
	"io"
	"os"
)

// writeWakeCopy writes the stderr copy of a send's wake line (D-052) — unless
// stdout and stderr are the same open file, in which case the reader already
// has it once, on the result line (D-097, amending D-052).
//
// WHY THE COPY EXISTS. A sender that treats the result line as a receipt
// discards stdout (`>/dev/null`), and the wake is the one part of it that asks
// the sender to act (issue #42). That is still true, so the copy stays
// whenever the two streams go to different places.
//
// WHY NOT ALWAYS. Under `2>&1` both copies land in one place: 121 of ~200
// `msg` results in one bucket of one fleet carried the wake twice, at 321
// bytes or more each, in the context of the session that sent it. "Same
// place" is decided by the files themselves, fstat device and inode
// (os.SameFile): `2>&1` duplicates the descriptor, so a pipe, a file and a
// terminal each compare equal to themselves. Anything that is not an
// *os.File, or cannot be stat'ed, gets the copy: when in doubt the wake is
// said twice rather than lost. So does a SOCKET: darwin reports device and
// inode 0 for a TCP socket, so two different connections compare equal
// (Codex code pass, D-097).
func writeWakeCopy(env Env, format string, args ...any) {
	if sameOpenFile(env.Stdout, env.Stderr) {
		return
	}
	fmt.Fprintf(env.Stderr, format, args...)
}

// sameOpenFile reports whether a and b are files with one device and inode.
func sameOpenFile(a, b io.Writer) bool {
	fa, ok := a.(*os.File)
	if !ok {
		return false
	}
	fb, ok := b.(*os.File)
	if !ok {
		return false
	}
	sa, err := fa.Stat()
	if err != nil {
		return false
	}
	sb, err := fb.Stat()
	if err != nil {
		return false
	}
	if sa.Mode()&os.ModeSocket != 0 || sb.Mode()&os.ModeSocket != 0 {
		return false
	}
	return os.SameFile(sa, sb)
}
