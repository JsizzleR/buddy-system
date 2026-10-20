package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-097, amending D-052: `msg` writes its wake line's stderr copy only when
// stderr is not the same open file as stdout. Under `2>&1` the reader had it
// twice — 121 of ~200 results in one bucket of one fleet.
func TestMsgWakeCopyOnlyWhenStderrIsAnotherFile(t *testing.T) {
	boundedParallel(t)
	const wakeMark = "to wake it now:"
	for _, tc := range []struct {
		name string
		// streams returns stdout and stderr for the send, and the files to
		// read back afterwards, in order.
		streams func(t *testing.T, dir string) (out, errw *os.File, read []string)
		want    []int // wake lines per file read back
	}{
		{"2>&1: stderr is a dup of stdout's descriptor", func(t *testing.T, dir string) (*os.File, *os.File, []string) {
			p := filepath.Join(dir, "both")
			out := create(t, p)
			fd, err := syscall.Dup(int(out.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			errw := os.NewFile(uintptr(fd), p)
			t.Cleanup(func() { errw.Close() })
			return out, errw, []string{p}
		}, []int{1}},
		{"one *os.File passed as both", func(t *testing.T, dir string) (*os.File, *os.File, []string) {
			p := filepath.Join(dir, "both")
			out := create(t, p)
			return out, out, []string{p}
		}, []int{1}},
		// The control: the same send with the streams apart keeps the copy,
		// as D-052 has it — a sender discarding stdout still sees the wake.
		{"two files", func(t *testing.T, dir string) (*os.File, *os.File, []string) {
			po, pe := filepath.Join(dir, "out"), filepath.Join(dir, "err")
			return create(t, po), create(t, pe), []string{po, pe}
		}, []int{1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.sockDir = shortSockDir(t)
			f.initAndHello(t)
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			idleB(t, f)
			f.clock = f.clock.Add(10 * time.Minute)
			out, errw, read := tc.streams(t, t.TempDir())
			if code := Run([]string{"msg", "bravo", "take the router bundle"}, f.envWith(out, errw)); code != 0 {
				t.Fatalf("msg failed (code %d)", code)
			}
			for i, p := range read {
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if n := strings.Count(string(b), wakeMark); n != tc.want[i] {
					t.Fatalf("%s: %d wake line(s), want %d:\n%s", filepath.Base(p), n, tc.want[i], b)
				}
				if i == 0 && !strings.HasPrefix(string(b), "queued for ") {
					t.Fatalf("the result line leads stdout:\n%s", b)
				}
			}
		})
	}
}

// sameOpenFile decides on the file, not the writer value: a pipe compares
// equal to its own dup and not to another pipe, and anything that is not a
// file is "different" (the copy is written: said twice, never lost).
func TestSameOpenFile(t *testing.T) {
	r1, w1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close()
	defer w1.Close()
	_, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	fd, err := syscall.Dup(int(w1.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	w1dup := os.NewFile(uintptr(fd), "dup")
	defer w1dup.Close()
	// Sockets: darwin stats a TCP socket as device and inode 0, so two
	// connections would compare equal; no socket is ever "the same file".
	pair := func() (*os.File, *os.File) {
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		a, b := os.NewFile(uintptr(fds[0]), "s0"), os.NewFile(uintptr(fds[1]), "s1")
		t.Cleanup(func() { a.Close(); b.Close() })
		return a, b
	}
	sock, sock2 := pair()
	sfd, err := syscall.Dup(int(sock.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	sockDup := os.NewFile(uintptr(sfd), "sdup")
	defer sockDup.Close()
	for _, tc := range []struct {
		name string
		a, b any
		want bool
	}{
		{"a pipe and its dup", w1, w1dup, true},
		{"a socket and its dup is still not trusted", sock, sockDup, false},
		{"two sockets", sock, sock2, false},
		{"two pipes", w1, w2, false},
		{"a file and a buffer", w1, &strings.Builder{}, false},
		{"two buffers", &strings.Builder{}, &strings.Builder{}, false},
	} {
		a, _ := tc.a.(interface{ Write([]byte) (int, error) })
		b, _ := tc.b.(interface{ Write([]byte) (int, error) })
		if got := sameOpenFile(a, b); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func create(t *testing.T, p string) *os.File {
	t.Helper()
	fh, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fh.Close() })
	return fh
}

// envWith is the fixture's Env with real files for the two output streams.
func (f *fixture) envWith(out, errw *os.File) Env {
	return Env{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: errw,
		Cwd:    f.repo,
		Now:    func() time.Time { return f.clock },
		Getenv: func(k string) string { return f.env[k] },
		Anchor: func() (harnessProc, bool) { return harnessProc{Ref: f.proc, Retitled: f.retitled}, f.proc.PID != 0 },
		ProcAlive: func(p store.ProcRef) bool {
			born, ok := f.alive[p.PID]
			return ok && (p.Born == 0 || born == 0 || born == p.Born)
		},
		ProcTable: func() ([]procEntry, int, bool) { return f.procs, f.self, true },
		SockDir:   f.sockDir,
	}
}
