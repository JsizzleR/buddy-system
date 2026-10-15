package cli

// Why a verb could not use a ledger it can see (F1, the 2026-10-14 Codex fleet
// trial).
//
// The ledger lives in the git COMMON directory, and a sandbox that confines
// writes to the worktree keeps that directory read-only. Codex's
// workspace-write sandbox does: in the trial every buddy verb a Codex session
// ran failed first with
//
//	buddy status: inspect ledger schema version: unable to open database file (14)
//
// and an auto-review then approved the same command escalated. One failed call
// and one review per buddy call, and the error named neither the cause nor the
// fix. Reproduced with Codex's own runner (`codex sandbox -P :workspace`) and a
// one-line seatbelt profile, both of which log a `file-write-data` denial on
// buddy.db. Two shapes, depending on whether a peer holds the ledger open:
// with no `-wal`/`-shm` beside it every verb fails at open with CANTOPEN (14);
// with them present a read succeeds and every write fails with READONLY (8).
// Every buddy verb opens the ledger read-write, so neither shape is a verb
// that "only reads" getting lucky.
//
// THIS IS WORDING, NOT A VERDICT. The verb still fails exactly as it did, and
// the gate is untouched: it is not routed through errVerb, so an unreadable
// ledger still DENIES there and never becomes feature-off (invariants 2 and 3).
// Nothing here reads, retries or opens the ledger read-only to get a verb
// through; a write that a sandbox refuses must go to the operator's approval,
// which is the escalation this text names.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// envCodexSandbox is set by Codex in every command its sandbox runs
// (measured on codex-cli 0.162.1: CODEX_SANDBOX=seatbelt on macOS). It is
// used only to say which sandbox, never to decide anything.
const envCodexSandbox = "CODEX_SANDBOX"

// sqliteAccessCodes are SQLite's primary result codes for "this process may
// not open or write the database file": CANTOPEN (14) and READONLY (8). An
// extended code carries the primary one in its low byte.
var sqliteAccessCodes = map[int]bool{8: true, 14: true}

// looksLikeLedgerAccess reports whether err is SQLite failing to open or write
// the database. The chain is checked first; the message is the fallback,
// because several verbs flatten an error through fencedErr before it gets
// here, and these are SQLite's own fixed strings for the same two codes.
func looksLikeLedgerAccess(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) && sqliteAccessCodes[coded.Code()&0xff] {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unable to open database file") || strings.Contains(msg, "readonly database")
}

// writeDenied opens the ledger for writing, without creating or truncating
// anything, and reports whether the OS refused. It is the evidence the
// explanation rests on: an SQLite code alone also fires for a missing
// directory or a full disk, and this text must not send a caller to ask for
// escalation it does not need.
func writeDenied(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return false, nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err // the path is printed once, by the caller
	}
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES), err
}

// explainLedgerAccess returns err with the cause and the fix appended when the
// ledger exists and this process may not write it. Every other error comes
// back unchanged. Only the error path pays for it: one git process and one
// open, after the verb has already failed.
func explainLedgerAccess(err error, env Env) error {
	if err == nil || !looksLikeLedgerAccess(err) {
		return err
	}
	rc, rerr := resolveRepo(env.Cwd)
	if rerr != nil || rc.ledger == "" {
		return err
	}
	if _, serr := os.Stat(rc.ledger); serr != nil {
		return err
	}
	denied, why := writeDenied(rc.ledger)
	if !denied {
		return err
	}
	sandbox := "a sandbox that keeps the git directory read-only (Codex's workspace-write does)"
	if v := env.getenv(envCodexSandbox); v != "" {
		sandbox = "the Codex sandbox ($" + envCodexSandbox + "=" + fence.Line(v, 32) + "), which keeps the git directory read-only"
	}
	return fmt.Errorf("%w\n  cause: this process may not write the ledger %s (%s). It lives in the git common directory, outside the writable roots of %s.\n"+
		"  fix: run buddy with sandbox escalation requested UP FRONT, for every buddy call (Codex: sandbox_permissions \"require_escalated\"), instead of letting the sandboxed call fail first. "+
		"Every verb needs write access, read-only ones included: with no peer holding the ledger open, the open itself fails.",
		err, fence.Line(rc.ledger, 512), fence.Line(why.Error(), 256), sandbox)
}
