package cli

// Every place a write to one tool path can land.
//
// The gate used to adjudicate ONE spelling of a target, canon(path): the joined
// path with every symlink followed by EvalSymlinks. Two harness behaviours land
// somewhere else, and both were reproduced as failing gate tests in the
// 2026-10-06 review:
//
//   - `..` after a symlinked directory (F-06). Codex resolves `..` LEXICALLY
//     (PathUri::join, rust-v0.160.1), so `<repo>/docs/link/../core.go` deletes
//     docs/core.go, while EvalSymlinks follows docs/link first and checked
//     src/core.go. The kernel resolves `..` PHYSICALLY, after the link, so a
//     tool that hands the raw string to the filesystem lands on the other one.
//     Which one Claude's tools do was not measured. Both are checked. This
//     predates the Codex work for an absolute Edit.
//   - A symlink's own directory entry (F-05). Codex's Delete File, and a
//     move's source, unlink the ENTRY (symlink_metadata then remove_file), and
//     the gate only ever checked where the link points. AGENTS.md -> CLAUDE.md
//     is a common layout.
//
// So the gate checks every spelling and refuses if any one is held: lexical and
// physical, each with the final link followed and as the entry itself, plus a
// dangling link's would-be target. Checking all of them for every operation,
// rather than threading Delete/Move metadata out of the patch parser, is the
// deliberately conservative policy: a write through a link reaches its target,
// an unlink reaches its entry, and a claim on either is a reservation the caller
// does not hold. At most five spellings per path after deduplication, and
// usually one, so the patch parser's 128-path bound still bounds the work.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gateTargets returns the canonical absolute spellings of p, relative to cwd,
// that a write or unlink of p can reach. Every one is safe to place without
// resolving it again (placeCanonical, gateRepoKey).
func gateTargets(cwd, p string) ([]string, error) {
	raw := p
	if !filepath.IsAbs(raw) {
		// NOT filepath.Join, which cleans: the physical spelling is the
		// uncleaned one, with `..` still after whatever precedes it.
		raw = cwd + string(filepath.Separator) + raw
	}
	lex := filepath.Clean(raw)
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	add(canon(lex))
	add(entryOf(lex, canon))
	add(physical(raw))
	add(entryOf(raw, physical))
	var resolutionErr error
	for _, e := range append([]string(nil), out...) {
		target, err := danglingTarget(e)
		if err != nil {
			// Keep the already-known entries for jurisdiction discovery. A
			// resolution failure must refuse where a ledger governs, without
			// turning an ungoverned directory into a fleet-wide refusal.
			// A failed lexical spelling must not hide a physical spelling
			// reaching a governed repo (V-03 constrained-review follow-up).
			if resolutionErr == nil {
				resolutionErr = err
			}
			continue
		}
		add(target)
	}
	return out, resolutionErr
}

// entryOf is p's own directory entry: its parent resolved by resolve, and its
// final component kept as a name rather than followed. "" when the final
// component is not a name (`.`, `..`, or a trailing separator), because that
// spelling names a directory the other spellings already cover.
func entryOf(p string, resolve func(string) string) string {
	i := strings.LastIndexByte(p, filepath.Separator)
	if i < 0 {
		return ""
	}
	base := p[i+1:]
	switch base {
	case "", ".", "..":
		return ""
	}
	dir := p[:i]
	if dir == "" {
		dir = string(filepath.Separator)
	}
	return filepath.Join(resolve(dir), base)
}

// physical resolves p the way the kernel does: each `..` applies to whatever
// the components before it resolved to, links included (EvalSymlinks does this
// for a path that exists). For a path that does not exist yet, it resolves the
// deepest existing prefix the same way and keeps the remaining names.
func physical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	i := strings.LastIndexByte(p, filepath.Separator)
	if i <= 0 {
		return canon(p)
	}
	base := p[i+1:]
	switch base {
	case "", ".", "..":
		return canon(p)
	}
	return filepath.Join(physical(p[:i]), base)
}

// danglingTarget is where a write through a DANGLING symlink would create its
// file. EvalSymlinks fails on a dangling link, so canon falls back to the
// link's own spelling, and the file the write would create was never checked.
// "" for anything that is not a dangling link.
func danglingTarget(p string) (string, error) {
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return "", nil
	}
	if _, err := os.Stat(p); err == nil {
		return "", nil // not dangling: canon already followed it
	}
	return resolveMissingTarget(p)
}

// resolveMissingTarget follows symlinks component by component, including a
// chain whose final target does not exist. EvalSymlinks cannot return that
// target. Resolving only one readlink lost a second dangling link, and cleaning
// readlink contents before following their directories made `link/..` lexical
// instead of physical (verification V-03). Both let a write create a file in a
// peer-held directory while the gate checked an unclaimed spelling.
//
// Missing components are retained: a tool may create them. Every `..` applies
// after the preceding links. Forty traversals bound cycles and excessive chains;
// an unresolved target is uncertainty, never an absent claim.
func resolveMissingTarget(p string) (string, error) {
	const maxSymlinks = 40
	remaining := strings.Split(p, string(filepath.Separator))
	resolved := string(filepath.Separator)
	links := 0
	for len(remaining) > 0 {
		component := remaining[0]
		remaining = remaining[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, component)
		fi, err := os.Lstat(next)
		if os.IsNotExist(err) {
			resolved = next
			continue
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		links++
		if links > maxSymlinks {
			return "", fmt.Errorf("target resolution exceeds %d symlink traversals (cycle or excessive chain)", maxSymlinks)
		}
		link, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			resolved = string(filepath.Separator)
		}
		remaining = append(strings.Split(link, string(filepath.Separator)), remaining...)
	}
	return resolved, nil
}
