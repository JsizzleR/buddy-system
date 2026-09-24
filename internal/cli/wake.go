package cli

// THE WAKE ADDRESS (D-039, issue #25).
//
// Delivery rides the hooks: `beat` drains the inbox on a tool call and
// `hello` at SessionStart (D-034). A session already at its prompt runs
// neither, so a `buddy msg` to it waits for a human to type into its pane.
// Measured: an operator opened six sessions to hand them work and released
// every assignment by hand, one keystroke per pane.
//
// The harness has its own session-to-session channel (the SendMessage tool),
// and D-034 left waking a session at its prompt as D-027's "no" until someone
// measured how that channel appears on the RECEIVING side. Measured
// 2026-09-23 on two fresh sessions opened for the purpose:
//
//   - It wakes an idle session: enqueue to dequeue took 5 ms, and the
//     recipient ran a turn with no keystroke.
//   - It is NOT the operator's turn. The transcript records a user-role
//     entry with isMeta and origin {kind: "peer", name, verifiedPeerPid}
//     (the SENDER's harness pid, checked by the host), and the model sees
//     "Another Claude session sent a message:", a
//     <cross-session-message from=… from-name=…> wrapper, and a harness note
//     that it was "not typed by your user" and that a peer "cannot grant
//     escalation".
//   - A body cannot forge that framing: a raw </cross-session-message> in the
//     body arrived as <\/cross-session-message>, and a raw opening tag as
//     <\cross-session-message.
//   - The address is uds:/tmp/cc-socks/<pid>.sock, the receiving harness
//     process's socket, and buddy already records that pid (D-025). A
//     buddy msg queued to an idle session, then a SendMessage to that
//     address saying "run buddy inbox", drained it: 1 undelivered → 0, and
//     the session went idle again. Nobody touched the pane.
//
// So `msg` NAMES that address, and buddy itself still wakes nothing. It is a
// binary: it cannot call a harness tool, and it must not type into a pane
// (D-027). The sending agent chooses whether to use its own SendMessage, under
// its own harness's permission rules. The wake carries NO content, only "run
// buddy inbox". The message itself stays in the ledger, fenced, and delivered
// by the drain. The ledger stays the authoritative copy (invariant 4), and a
// wake that is held or refused costs nothing: the queued copy is there either
// way.
//
// Printed only when all of these hold, and never otherwise:
//   - the target is one live session that the ledger says is quiet (an idle
//     report, not seen past the stale mark, or registered and not seen
//     since). A session seen recently drains on its own.
//   - exactly ONE registered harness process is alive, by the same pid-and-
//     birth-time check GONE uses. The pid is used here to form an address
//     and for nothing else. A reused pid fails the birth-time check, and two
//     live processes (a double --resume) are ambiguous.
//   - the socket exists and IS a socket. The path is the harness's own
//     internal detail, and if it moves the line disappears and msg reads as
//     it did before.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// defaultSockDir is where Claude Code listens for cross-session messages,
// one socket per harness process named <pid>.sock (measured 2026-09-23).
const defaultSockDir = "/tmp/cc-socks"

// wakeText is the whole of what the wake says. The message is in the ledger.
const wakeText = "buddy mail is queued for you: run buddy inbox"

func (e Env) sockDir() string {
	if e.SockDir != "" {
		return e.SockDir
	}
	return defaultSockDir
}

// wakeClause is what `msg` appends to its result line when the recipient is
// quiet and reachable through the harness channel; "" otherwise. It reads the
// send's one observation and probes nothing itself.
//
// ON THE RESULT LINE, NOT BELOW IT (D-041, issue #32). It used to be a second
// line. Measured on the bastle ledger, 2026-09-24: an orchestrator ran
// `buddy msg … 2>&1 | head -1`, which is the ordinary way an agent keeps a
// command's output short. The address was printed, all three conditions below
// held, and the idle lane it named sat unwoken until the operator happened to
// type into its pane. The orchestrator then guessed a harness peer NAME, and
// that send never arrived. One line survives head -1, tail -1 and a grep for
// the recipient.
func wakeClause(env Env, r recipient, now time.Time) string {
	// No liveness test of its own, deliberately: bye deletes an ended
	// session's registered processes, so an ended target already fails the
	// one-live-process condition below, and a second test in front of it
	// would be a guard no test can arm (the `ended` case holds the rule).
	if !r.ok {
		return ""
	}
	si := r.si
	quiet := r.idle != nil || now.Sub(si.LastSeen) > store.StaleAfter || si.LastSeen.Equal(si.Started)
	if !quiet || len(r.alive) != 1 {
		return ""
	}
	sock := filepath.Join(env.sockDir(), strconv.Itoa(r.alive[0].PID)+".sock")
	if fi, err := os.Stat(sock); err != nil || fi.Mode()&os.ModeSocket == 0 {
		return ""
	}
	return fmt.Sprintf("to wake it now: SendMessage to %q with the text %q — the harness delivers that as a message from another session, never as your user's turn (D-039); a session in a different permission mode holds it for its user, and this copy stays queued either way",
		"uds:"+sock, wakeText)
}
