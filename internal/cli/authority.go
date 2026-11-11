package cli

// Authority files: the standing rules a session read at start, and whether
// the copy on disk has moved since.
//
// THE FAILURE (issue #19, wishlist §14). A coordinator quoted the project's
// conventions file to two sessions as current fact. The sentence it quoted
// had been corrected on disk at 12:48 that day; its copy came from a context
// snapshot taken before that, and a compaction had carried the stale copy
// forward. Every check it could plausibly have run said current — main had
// moved ZERO commits since its snapshot's tip, because the correction was in
// a commit the snapshot already contained; the snapshot of the FILE predated
// it. The corrected sentence warned against the very error being made.
// Fixing the file did not reach the copies already issued, and the failure
// is invisible from inside: nothing distinguishes "I read this an hour ago"
// from "nine hours ago and it changed twice since".
//
// Buddy is the only thing in the workflow that knows when a session started.
// The check is one Stat per watched file: the file's recorded modification
// time postdates this session's registration. That is what it says and all
// it says — not that the contents differ from what the session read, not
// that the session has not re-read them since, and in a linked worktree not
// that main has moved (the file there changes only when that worktree
// pulls). An ADVISORY, worded as one (Codex design pass, D-028).
//
// TWO SURFACES. `buddy status`/`who` print an AUTHORITY block (pull). And
// `beat` carries a ONE-SHOT notice into the session's context the first
// tool call after each change — the coordinator never ran a status command
// because it did not know it was stale, which is the whole shape of the
// defect; the notice is what would have caught it. Deduplicated in the
// ledger per (session, incarnation, file, CONTENT) since D-167 (the mtime
// before it, D-028): the notice fires when the bytes on disk are not the copy
// the session started with, wrote itself, or was last told of, so a file
// changed twice warns twice, and a checkout of the same bytes or the
// session's own edit warns not at all. Cost on the hot path: N Stats (two
// built-ins plus MaxAuthority) and, only for a file whose mtime postdates the
// session's start, one read and hash of it and one SELECT.
//
// THE LIST LIVES IN THE LEDGER (`buddy authority add|rm`), not in git config:
// reading git config is a fork, 7-9 ms against a 100 ms hook budget, and the
// beat path already has the ledger open. CLAUDE.md and AGENTS.md are ALWAYS
// watched: either harness can otherwise keep stale participation rules in
// context. `add` extends the set. Paths are repo-relative, normalized like scopes.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// alwaysWatched names the instruction files independent of the configured
// list. Adding a built-in spends none of its eight slots; older ledgers may
// already contain one, so every comparison still uses the shared case/NFC fold.
var alwaysWatched = [...]string{"CLAUDE.md", "AGENTS.md"}

// alwaysWatchedPath returns the canonical spelling, or empty for an extension.
func alwaysWatchedPath(p string) string {
	for _, builtIn := range alwaysWatched {
		if store.SamePath(p, builtIn) {
			return builtIn
		}
	}
	return ""
}

// authorityFile is one watched file whose recorded modification time
// postdates a session's registration.
type authorityFile struct {
	Path    string
	ModTime time.Time
	Info    os.FileInfo // for contentKey: regular or not, and its size
}

// authorityChanged lists the watched files that changed on disk after si
// registered. A file that does not exist, or cannot be stat-ed, is skipped:
// "no such file" is not "changed". os.Stat follows symlinks, because what a
// session reads is the target (Codex: Lstat would report an unchanged link
// over a changed target).
//
// unread counts the watched paths that could not be stat-ed as a file — a
// missing file, a directory — so the all-clear can say what it did not
// look at rather than assert that nothing changed (Codex code pass).
func authorityChanged(st *store.Store, top string, si store.SessionInfo) (changed []authorityFile, unread int, err error) {
	paths, err := watchList(st)
	if err != nil {
		return nil, 0, err
	}
	for _, p := range paths {
		fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() {
			unread++
			continue
		}
		if fi.ModTime().After(si.Started) {
			changed = append(changed, authorityFile{Path: p, ModTime: fi.ModTime(), Info: fi})
		}
	}
	return changed, unread, nil
}

// watchList is the configured list with the built-in instruction files first.
func watchList(st *store.Store) ([]string, error) {
	paths, err := st.AuthorityPaths()
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), alwaysWatched[:]...)
	for _, p := range paths {
		if alwaysWatchedPath(p) == "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// authorityHashCap bounds what contentKey reads. A rules file is tens of KB
// (CLAUDE.md is about 40); past the cap the file is keyed by its mtime, the
// D-028 key, rather than read whole on a hot-path hook (Codex code pass).
const authorityHashCap = 1 << 20

// contentKey is the key authority_warned stores for one copy of a watched
// file (D-167): the first eight bytes of its SHA-256, or its mtime in
// nanoseconds when it cannot be hashed (the D-028 key, so such a file still
// warns once per change): not a regular file, past authorityHashCap, or
// unreadable. It is computed only for a file whose mtime already postdates
// the session's start, and at hello; a file nobody touched costs its Stat and
// nothing more, as before.
//
// A FIFO NEVER OPENS. The mtime key never opened anything, and an open of a
// FIFO with no writer blocks: a watched path made one would have hung beat
// before its output and the inbox (Codex code pass). Regular files only, and
// opened O_NONBLOCK too, for a path swapped between the Stat and the open.
func contentKey(top, p string, fi os.FileInfo) int64 {
	mtime := fi.ModTime().UnixNano()
	if !fi.Mode().IsRegular() || fi.Size() > authorityHashCap {
		return mtime
	}
	f, err := os.OpenFile(filepath.Join(top, filepath.FromSlash(p)), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return mtime
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return mtime
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, authorityHashCap+1))
	if err != nil || n > authorityHashCap {
		return mtime
	}
	return int64(binary.BigEndian.Uint64(h.Sum(nil)))
}

// authorityBaseline records, at hello, the copy of each watched file the
// session starts with, so a later checkout of the same bytes is not a change
// (D-167). Best-effort: a missing file records nothing, and a lost record
// costs one notice the old keying would have given anyway.
//
// ONLY WHERE THIS INCARNATION HAS NO ROW. A hello on a live incarnation (a
// hand-run `hello --session`, a resume) would otherwise record a peer's
// change the session never saw as its copy, and no notice would follow
// (Codex code pass). The cost is the other direction: a refresh that did
// reload the file still gets one notice.
func authorityBaseline(st *store.Store, top string, si store.SessionInfo) {
	paths, err := watchList(st)
	if err != nil {
		return
	}
	for _, p := range paths {
		fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() {
			continue
		}
		if _, known, err := st.AuthorityLast(si.SessionID, si.Incarnation, p); err != nil || known {
			continue
		}
		_ = st.AuthorityRecord(si.SessionID, si.Incarnation, p, contentKey(top, p, fi))
	}
}

// authorityNotice is the one-shot line beat injects: every watched file whose
// mtime postdates this session's start and whose CONTENT is not the copy the
// session is last known to hold (D-167, amending D-028's mtime key). That copy
// is the newest of: what hello found, what the session's own write left, what
// the last notice announced. So a checkout or worktree that rewrites the same
// bytes says nothing, nor does a session's own Edit of the file (ownWrite, the
// repo-relative path a write tool of THIS call named: PostToolUse runs after
// the write, so the bytes on disk are the ones in its context). A git
// operation of its own that brings in OTHER bytes (a merge, a pull) is
// announced: the copy in its context really is stale then.
//
// Fail-open like the dirty notice: an error here costs the line, never the
// heartbeat, and the announced copy is recorded only through the returned
// commit, after the write succeeded (a lost write must cost nothing
// permanently). The own-write record is made at once: nothing is written for
// it to wait on.
func authorityNotice(st *store.Store, top string, si store.SessionInfo, now time.Time, ownWrite string) (string, func() error) {
	nothing := func() error { return nil }
	changed, _, err := authorityChanged(st, top, si)
	if err != nil || len(changed) == 0 {
		return "", nothing
	}
	type mark struct {
		path string
		key  int64
	}
	var b strings.Builder
	var marks []mark
	for _, a := range changed {
		key := contentKey(top, a.Path, a.Info)
		last, known, err := st.AuthorityLast(si.SessionID, si.Incarnation, a.Path)
		if err != nil || (known && last == key) {
			continue
		}
		if ownWrite != "" && store.SamePath(ownWrite, a.Path) {
			_ = st.AuthorityRecord(si.SessionID, si.Incarnation, a.Path, key)
			continue
		}
		fmt.Fprintf(&b, "BUDDY: %s changed on disk %s ago, AFTER this session started (%s ago) — the copy in your context may be stale; re-read it before quoting or acting on it.\n",
			fence.Line(a.Path, 512), age(now, a.ModTime), age(now, si.Started))
		marks = append(marks, mark{a.Path, key})
	}
	if len(marks) == 0 {
		return "", nothing
	}
	return b.String(), func() error {
		for _, m := range marks {
			if err := st.AuthorityRecord(si.SessionID, si.Incarnation, m.path, m.key); err != nil {
				return err
			}
		}
		return nil
	}
}

// cmdAuthority: `buddy authority` lists, `add <path>` and `rm <path>` edit.
func cmdAuthority(args []string, env Env) error {
	const usage = usageAuthority
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	switch {
	case len(args) == 0:
		paths, err := watchList(st)
		if err != nil {
			return err
		}
		for _, p := range paths {
			note := ""
			if alwaysWatchedPath(p) != "" {
				note = "  (always)"
			}
			// "watching " in front: a path NAMED "BUDDY: …" at column zero
			// would read as a notice in a tool result (Codex code pass).
			fmt.Fprintf(env.Stdout, "watching %s%s\n", fence.Line(p, 512), note)
		}
		return nil
	case len(args) == 2 && args[0] == "add":
		n, err := store.NormalizeScope(args[1])
		if err != nil {
			return err
		}
		if builtIn := alwaysWatchedPath(n); builtIn != "" {
			// Not stored: it is watched whether or not it is listed, and a
			// row for it would spend one of the eight slots on nothing.
			fmt.Fprintf(env.Stdout, "%s is always watched\n", builtIn)
			return nil
		}
		if err := st.AuthorityAdd(n); err != nil {
			return fencedErr(err)
		}
		// Only beat consumes the notice (authorityNotice's one caller), and
		// under Codex beat is refused (D-069): "sessions are told once, on
		// their next tool call" was never true of a Codex session. Its gate
		// and lifecycle hooks run but deliver no notice (Codex code pass:
		// "no buddy hook runs" was too broad). Its `buddy status` carries
		// the AUTHORITY block (B4's sibling).
		fmt.Fprintf(env.Stdout, "watching %s: a Claude Code session is told once, on its next tool call, when it changes after it started; a Codex session is not told (no Codex hook delivers this notice): its `buddy status` shows it\n", fence.Line(n, 512))
		return nil
	case len(args) == 2 && args[0] == "rm":
		// Held, not strict: rm may name a legacy absolute row so that it
		// can free its slot (D-100); a name it does not hold is not-found.
		n, err := store.NormalizeHeldScope(args[1])
		if err != nil {
			return err
		}
		if builtIn := alwaysWatchedPath(n); builtIn != "" {
			// Before this path became built-in, the operator could store it
			// as an extension. Explicit rm frees that redundant slot without
			// ever removing the built-in watch; reads perform no migration.
			removed, err := st.AuthorityRemove(n)
			if err != nil {
				return err
			}
			if removed {
				fmt.Fprintf(env.Stdout, "removed redundant configured entry for %s; remains always watched\n", builtIn)
				return nil
			}
			return fmt.Errorf("%s is always watched", builtIn)
		}
		removed, err := st.AuthorityRemove(n)
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("%s was not on the list", fence.Line(n, 512))
		}
		fmt.Fprintf(env.Stdout, "no longer watching %s\n", fence.Line(n, 512))
		return nil
	default:
		return errors.New(usage)
	}
}
