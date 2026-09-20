package buddylist

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// Request is one JSON line on the control socket.
type Request struct {
	Op    string `json:"op"` // say | read | stat | ack | alerts | alertack | who | dm | status | health | presence
	Room  string `json:"room,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Text  string `json:"text,omitempty"`
	After int64  `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`

	// Read window (see ReadOpts): Before walks backwards, Tail takes the
	// newest rows, Mentions filters to rows naming one of these tokens.
	Before   int64    `json:"before,omitempty"`
	Tail     int      `json:"tail,omitempty"`
	Mentions []string `json:"mentions,omitempty"`

	// Session keys the per-session read cursor (read with SinceLast, stat,
	// ack). It is a bookkeeping key and confers no authority: the socket is
	// loopback-only and every caller on it is already trusted equally, so a
	// forged session id costs its owner a wrong cursor and nothing else.
	Session string `json:"session,omitempty"`
	// SinceLast starts a read at Session's stored cursor.
	SinceLast bool `json:"since_last,omitempty"`
	// Seq is the cursor an ack (or alertack) advances to.
	Seq int64 `json:"seq,omitempty"`
	// Label is the sender name this session's own room messages carry, used
	// by `alerts` to recognize the echoes of its own posts. Like Session it
	// confers no authority; a wrong one costs its owner a self-alert.
	Label string `json:"label,omitempty"`
	// Slugs are the claim slugs this session currently holds. They ride along
	// so the caller that already read the ledger for its own purposes can
	// present them in the room; the daemon never reads the ledger itself.
	Slugs []string `json:"slugs,omitempty"`
	// Gone retires this session's presence now instead of at its idle
	// timeout. Presentation only — it neither ends a session nor touches a
	// claim, both of which are the ledger's business.
	Gone bool `json:"gone,omitempty"`
}

// Response is the one JSON line answered per request.
type Response struct {
	OK        bool                `json:"ok"`
	Error     string              `json:"error,omitempty"`
	Msgs      []Msg               `json:"msgs,omitempty"`
	Gap       bool                `json:"gap,omitempty"`
	Connected bool                `json:"connected,omitempty"`
	Note      string              `json:"note,omitempty"`
	Rooms     map[string][]string `json:"rooms,omitempty"`
	Stats     []RoomStat          `json:"stats,omitempty"`
	// Cursor is the read cursor this request USED (read) or LEFT IN FORCE
	// (ack) — never what the caller asked for, which ack may decline to
	// apply.
	Cursor int64 `json:"cursor,omitempty"`
	// Alerts is one entry per room with an unalerted addressed backlog.
	Alerts []RoomAlert `json:"alerts,omitempty"`
}

const maxRequestLine = 64 * 1024

func (d *Daemon) serveSocket(ctx context.Context) error {
	// Single-instance guard: an exclusive flock on a sidecar lockfile, held
	// for the daemon's lifetime and released by the kernel if it dies. Only
	// the lock holder may unlink and rebind the socket — stealing it from a
	// live daemon would leave that daemon connected but unreachable while
	// this one loops on a nick collision: a silent fleet-wide outage. (A
	// dial probe alone left a probe→unlink window where two daemons starting
	// together could both bind — Codex finding.)
	lock, err := os.OpenFile(d.cfg.SocketPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("buddylistd socket lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return fmt.Errorf("buddylistd socket: another daemon is already serving %s (its lock is held)", d.cfg.SocketPath)
	}
	defer lock.Close()              // drops the flock when the daemon's socket server exits
	_ = os.Remove(d.cfg.SocketPath) // ours by lock: any existing socket is stale
	ln, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("buddylistd socket: %w", err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			d.log.Error("accept failed", "err", err)
			time.Sleep(100 * time.Millisecond) // no tight loop on persistent errors
			continue
		}
		go d.serveConn(conn)
	}
}

func (d *Daemon) serveConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), maxRequestLine)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			enc.Encode(Response{Error: "bad request: " + err.Error()})
			return
		}
		// A reply that cannot be written ends the connection: continuing after
		// a lost response would leave the caller guessing (Codex finding).
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if err := enc.Encode(d.dispatch(req)); err != nil {
			return
		}
	}
}

func (d *Daemon) dispatch(req Request) Response {
	switch req.Op {
	case "say":
		if req.Room == "" || req.Text == "" {
			return Response{Error: "say needs room and text"}
		}
		if err := d.Say(req.Room, req.From, req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "dm":
		if req.To == "" || req.Text == "" {
			return Response{Error: "dm needs to and text"}
		}
		if err := d.DM(req.To, req.From, req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "read":
		if req.Room == "" {
			return Response{Error: "read needs room"}
		}
		opts := ReadOpts{Room: req.Room, After: req.After, Before: req.Before,
			Tail: req.Tail, Limit: req.Limit, Mentions: req.Mentions}
		if req.SinceLast {
			// A cursor advance is only safe over a window that leaves nothing
			// unseen behind it, so since_last owns the whole selection: it
			// refuses to ride on top of a caller-chosen start, a backwards or
			// tail window, or a filter. Silently ignoring one of these would
			// advance the cursor past rows the caller was never shown, and
			// those rows are then unreachable by the cursor forever.
			switch {
			case req.Session == "":
				return Response{Error: "since_last needs a session id"}
			case req.After != 0:
				return Response{Error: "since_last and after both set: pass one"}
			case req.Before != 0 || req.Tail != 0:
				return Response{Error: "since_last cannot be combined with before/tail: the cursor would advance past rows outside that window"}
			case len(req.Mentions) > 0:
				return Response{Error: "since_last cannot be combined with mentions: the cursor would advance past rows the filter dropped"}
			}
			cur, err := d.cfg.Journal.Cursor(req.Session, req.Room)
			if err != nil {
				return Response{Error: err.Error()}
			}
			opts.After = cur
		}
		msgs, gap, err := d.cfg.Journal.Read(opts)
		if err != nil {
			return Response{Error: err.Error()}
		}
		// AN UNKNOWN ROOM MUST NOT READ AS A QUIET ONE.
		//
		// THE FAILURE (2026-09-07, and again): sessions were told to read a
		// room called `lobby`, which does not exist. Journal.Read is
		// `WHERE room=? AND seq>?`, so a wrong name returns zero rows and
		// renders as `(no messages)` — indistinguishable from a room nobody is
		// talking in. Measured then: a session reported "the room is empty, all
		// traffic goes through the message hook instead" while its actual
		// project room held thousands of messages.
		//
		// The digest's room name was made derived rather than hardcoded, but
		// the derivation is still a GUESS — it is the label's project half, and
		// a linked worktree's label names the worktree, not the checkout. So a
		// worktree session is told to read a room that was never joined. Two
		// independent reviews landed on the same conclusion: fix the READ, not
		// the guess, because the guess is only one of the ways a wrong name
		// arrives (an explicit --label, a typo, and an MCP schema that until
		// now said `e.g. "lobby"` are the others).
		//
		// Note the asymmetry this closes: Say already refuses with
		// `not joined to room %q`. Send told the truth and read did not.
		//
		// NOT SERVED **AND** NO HISTORY, both clauses load-bearing. A room
		// dropped from the configured list after accruing rows stays readable;
		// the @sent/@dm pseudo-rooms, which no config lists, stay readable; and
		// a CONFIGURED room with no traffic yet is still a truthful
		// `(no messages)`. Only a name this daemon has never served and has
		// never journalled is refused.
		//
		// The `len(msgs) == 0` guard buys COST, not behaviour, and a mutation
		// removing it SURVIVES: Read's WHERE begins `room=?` on the same
		// messages table KnowsRoom queries, so any row it returns implies
		// KnowsRoom is true and the arm below could not have fired anyway. It is
		// an equivalent mutation, recorded rather than chased with a test that
		// cannot exist — what the guard actually prevents is a second query on
		// every read of a busy room.
		if len(msgs) == 0 {
			if served := d.servesRoom(fold(req.Room)); served == "" {
				known, kerr := d.cfg.Journal.KnowsRoom(req.Room)
				if kerr != nil {
					return Response{Error: kerr.Error()}
				}
				if !known {
					return Response{Error: fmt.Sprintf("room %q: this daemon does not serve it and has no history for it; it serves %s",
						fence.Line(req.Room, 64), fence.Line(strings.Join(d.cfg.Rooms, ", "), 256))}
				}
			}
		}
		return Response{OK: true, Msgs: msgs, Gap: gap, Cursor: opts.After}
	case "stat":
		stats, err := d.cfg.Journal.Stat(req.Session, req.Mentions)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Stats: stats}
	case "ack":
		if req.Session == "" || req.Room == "" {
			return Response{Error: "ack needs session and room"}
		}
		cur, err := d.cfg.Journal.SetCursor(req.Session, req.Room, req.Seq)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Cursor: cur}
	case "alerts":
		// Read-only from the caller's side: it reports what a session has not
		// been told about and does NOT advance the cursor for anything it
		// hands back. The caller acks after it has actually delivered, so a
		// lost reply costs a repeat rather than a silently dropped alert.
		if req.Session == "" {
			return Response{Error: "alerts needs a session id"}
		}
		// Presence rides the alert hook rather than adding a second hook line
		// and a second round-trip to every tool call: this request already
		// carries the identity and the claim slugs, because the alert needed
		// them for its own mention tokens. note does no I/O, so the hook's
		// measured cost is unchanged.
		d.presence.note(req.Session, req.Label, req.Slugs)
		alerts, err := d.cfg.Journal.Addressed(req.Session, req.Label, req.Mentions, req.Limit)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Alerts: alerts}
	case "alertack":
		if req.Session == "" || req.Room == "" {
			return Response{Error: "alertack needs session and room"}
		}
		cur, err := d.cfg.Journal.SetAlertCursor(req.Session, req.Room, req.Seq)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Cursor: cur}
	case "status":
		if err := d.Status(req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "presence":
		// The explicit door for what `alerts` does implicitly, plus the
		// retirement a SessionEnd hook can announce. It always answers OK:
		// presence is decoration, and a caller must never be given a failure
		// to handle over it.
		if req.Session == "" {
			return Response{Error: "presence needs a session id"}
		}
		if req.Gone {
			d.presence.forget(req.Session)
		} else {
			d.presence.note(req.Session, req.Label, req.Slugs)
		}
		online, wanted := d.presence.live()
		return Response{OK: true, Note: fmt.Sprintf("presence: %d/%d session buddies online", online, wanted)}
	case "who":
		connected, rooms := d.Who()
		return Response{OK: true, Connected: connected, Rooms: rooms}
	case "health":
		connected, note := d.Health()
		return Response{OK: true, Connected: connected, Note: note}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// defaultCallTimeout bounds a socket round-trip. Five seconds is what every
// verb passed before Client existed, save one: the presence/SessionEnd path
// names its own tighter presenceCallTime, which is why zero here means "the
// default" and not "no deadline". It is generous because the daemon may be
// mid-reconnect, and the caller is a person or an MCP tool that can wait.
const defaultCallTimeout = 5 * time.Second

// Client is one caller's handle on the daemon socket: the path, and the bound.
//
// It exists because `Call(defaultSocket(), ...)` was spelled out at nine sites
// in cmd/buddylist — seven verbs and two dep closures — and the socket path
// repeated nine times is the half that matters: a verb that dialed a different
// socket than the rest would simply report the daemon as unreachable, which
// reads as "the daemon is down" rather than "this verb is wrong". The five-
// second bound was repeated at six of those nine (the two dep closures forward
// their caller's timeout and presence passes its own tighter one), and a bound
// repeated six times is a bound that gets changed in five places.
//
// Call stays exported beside it. It is the whole client protocol, the daemon's
// own tests dial it directly with their own temp socket, and a struct is not an
// improvement for a caller that already has both values in hand.
type Client struct {
	// Socket is the control socket path.
	Socket string
	// Timeout bounds dial, write and read together. Zero means
	// defaultCallTimeout; a caller with a tighter budget — SessionEnd, which
	// must not be held up by a wedged daemon — names its own.
	Timeout time.Duration
}

// timeout resolves the bound. It is its own method so the defaulting rule can
// be asserted without a socket: the rule that matters is that a ZERO Timeout
// does not reach net.DialTimeout, where 0 means "no deadline at all" — a hook
// that forgot to name a bound would then block forever on a wedged daemon
// instead of costing one notice.
func (c Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return defaultCallTimeout
	}
	return c.Timeout
}

func (c Client) Call(req Request) (Response, error) {
	return Call(c.Socket, req, c.timeout())
}

// Call is the client half: one request, one response, over the unix socket.
func Call(socketPath string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return Response{}, fmt.Errorf("buddylistd not reachable at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	enc, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(enc, '\n')); err != nil {
		return Response{}, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 10*1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Response{}, err
		}
		return Response{}, errors.New("buddylistd closed the connection without answering")
	}
	var resp Response
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "chatd refused without a reason"
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
