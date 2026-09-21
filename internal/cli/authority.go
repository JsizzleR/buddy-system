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
// ledger per (session, incarnation, file, mtime), so a file changed twice
// warns twice and a file changed once warns once. Cost on the hot path: N
// Stats (bounded by maxAuthority) and, only when one has moved, one SELECT.
//
// THE LIST LIVES IN THE LEDGER (`buddy authority add|rm`), not in git config:
// reading git config is a fork, 7-9 ms against a 100 ms hook budget, and the
// beat path already has the ledger open. CLAUDE.md is ALWAYS watched — it is
// the harness's own authority file and the one the incident was about — and
// `add` extends the set. Paths are repo-relative, normalized like scopes.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// alwaysWatched is watched whether or not the list names it. The harness
// reads it at every session start, which is exactly the snapshot that rots.
const alwaysWatched = "CLAUDE.md"

// authorityFile is one watched file whose recorded modification time
// postdates a session's registration.
type authorityFile struct {
	Path    string
	ModTime time.Time
}

// authorityChanged lists the watched files that changed on disk after si
// registered. A file that does not exist, or cannot be stat-ed, is skipped:
// "no such file" is not "changed". os.Stat follows symlinks, because what a
// session reads is the target (Codex: Lstat would report an unchanged link
// over a changed target).
func authorityChanged(st *store.Store, top string, si store.SessionInfo) ([]authorityFile, error) {
	paths, err := watchList(st)
	if err != nil {
		return nil, err
	}
	var out []authorityFile
	for _, p := range paths {
		fi, err := os.Stat(filepath.Join(top, filepath.FromSlash(p)))
		if err != nil || fi.IsDir() {
			continue
		}
		if fi.ModTime().After(si.Started) {
			out = append(out, authorityFile{Path: p, ModTime: fi.ModTime()})
		}
	}
	return out, nil
}

// watchList is the configured list with CLAUDE.md always first.
func watchList(st *store.Store) ([]string, error) {
	paths, err := st.AuthorityPaths()
	if err != nil {
		return nil, err
	}
	out := []string{alwaysWatched}
	for _, p := range paths {
		if !store.SamePath(p, alwaysWatched) {
			out = append(out, p)
		}
	}
	return out, nil
}

// authorityNotice is the one-shot line beat injects: every watched file that
// changed after this session started and has not yet been announced to
// this incarnation at this mtime. Fail-open like the dirty notice — an
// error here costs the line, never the heartbeat — and the mark is claimed
// only through the returned commit, after the write succeeded, for the
// reason the dirty warn's is (a lost write must cost nothing permanently).
func authorityNotice(st *store.Store, top string, si store.SessionInfo, now time.Time) (string, func() error) {
	nothing := func() error { return nil }
	changed, err := authorityChanged(st, top, si)
	if err != nil || len(changed) == 0 {
		return "", nothing
	}
	var b strings.Builder
	var marks []authorityFile
	for _, a := range changed {
		pending, err := st.AuthorityWarnPending(si.SessionID, si.Incarnation, a.Path, a.ModTime.UnixNano())
		if err != nil || !pending {
			continue
		}
		fmt.Fprintf(&b, "BUDDY: %s changed on disk %s ago, AFTER this session started (%s ago) — the copy in your context may be stale; re-read it before quoting or acting on it.\n",
			fence.Line(a.Path, 512), age(now, a.ModTime), age(now, si.Started))
		marks = append(marks, a)
	}
	if len(marks) == 0 {
		return "", nothing
	}
	return b.String(), func() error {
		for _, a := range marks {
			if err := st.MarkAuthorityWarned(si.SessionID, si.Incarnation, a.Path, a.ModTime.UnixNano()); err != nil {
				return err
			}
		}
		return nil
	}
}

// cmdAuthority: `buddy authority` lists, `add <path>` and `rm <path>` edit.
func cmdAuthority(args []string, env Env) error {
	const usage = "usage: buddy authority [add <path> | rm <path>]   (repo-relative; CLAUDE.md is always watched)"
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
			if store.SamePath(p, alwaysWatched) {
				note = "  (always)"
			}
			fmt.Fprintf(env.Stdout, "%s%s\n", fence.Line(p, 512), note)
		}
		return nil
	case len(args) == 2 && args[0] == "add":
		n, err := store.NormalizeScope(args[1])
		if err != nil {
			return err
		}
		if store.SamePath(n, alwaysWatched) {
			// Not stored: it is watched whether or not it is listed, and a
			// row for it would spend one of the eight slots on nothing.
			fmt.Fprintf(env.Stdout, "%s is always watched\n", alwaysWatched)
			return nil
		}
		if err := st.AuthorityAdd(n); err != nil {
			return fencedErr(err)
		}
		fmt.Fprintf(env.Stdout, "watching %s: sessions are told once, on their next tool call, when it changes after they started\n", fence.Line(n, 512))
		return nil
	case len(args) == 2 && args[0] == "rm":
		n, err := store.NormalizeScope(args[1])
		if err != nil {
			return err
		}
		if store.SamePath(n, alwaysWatched) {
			return fmt.Errorf("%s is always watched", alwaysWatched)
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
