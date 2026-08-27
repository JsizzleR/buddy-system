package buddylist

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// Request is one JSON line on the control socket.
type Request struct {
	Op    string `json:"op"` // say | read | stat | ack | alerts | alertack | who | dm | status | health
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
